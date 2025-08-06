package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"archive/zip"
	"os/exec"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/gorilla/mux"
)

// Global logger
var logger *slog.Logger

// setupLogger initializes structured logging to console and a file
func setupLogger() error {
	logsDir := "/app/data/logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %v", err)
	}

	// Create log file with date only (YYYY-MM-DD)
	date := time.Now().Format("2006-01-02")
	logFile := filepath.Join(logsDir, fmt.Sprintf("cms_%s.log", date))

	// Open log file
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	// Fix ownership of the log file to match host user (1000:1000)
	if err := fixLogFileOwnership(logFile); err != nil {
		log.Printf("Warning: Failed to fix log file ownership: %v", err)
	}

	// Create multi-writer for both file and console
	multiWriter := io.MultiWriter(os.Stdout, file)

	// Create JSON handler for structured logging
	handler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})

	// Create logger
	logger = slog.New(handler)

	// Set as default logger
	slog.SetDefault(logger)

	logger.Info("Logger initialized",
		"log_file", logFile,
		"level", "debug",
		"timestamp", time.Now().Format(time.RFC3339),
	)

	return nil
}

// fixLogFileOwnership changes the ownership of the log file to the host user
func fixLogFileOwnership(logFile string) error {
	// Change ownership to UID 1000:GID 1000 (host user)
	cmd := exec.Command("chown", "1000:1000", logFile)
	return cmd.Run()
}

// Plugin represents a CMS plugin with action-based hooks
type Plugin struct {
	Slug        string                  `json:"slug"` // Unique identifier
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Version     string                  `json:"version"`
	Author      string                  `json:"author"`
	RootFSPath  string                  `json:"rootfs_path"`
	KernelPath  string                  `json:"kernel_path"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
	Status      string                  `json:"status"` // ready, inactive, active, failed
	Health      PluginHealth            `json:"health"`
	Actions     map[string]PluginAction `json:"actions"`  // action_name -> PluginAction
	Priority    int                     `json:"priority"` // Execution order for same action
}

// PluginHealth represents plugin health status
type PluginHealth struct {
	Status       string    `json:"status"` // healthy, unhealthy, unknown
	LastCheck    time.Time `json:"last_check"`
	Message      string    `json:"message"`
	ResponseTime int64     `json:"response_time_ms"`
}

// PluginAction represents an action hook that a plugin provides
type PluginAction struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Hooks       []string `json:"hooks"`    // Which CMS actions this responds to
	Method      string   `json:"method"`   // HTTP method
	Endpoint    string   `json:"endpoint"` // Plugin endpoint
	Priority    int      `json:"priority"` // Execution order
}

// VMInstance represents a running microVM instance
type VMInstance struct {
	ID         string    `json:"id"`
	PluginSlug string    `json:"plugin_slug"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// CMS represents the main CMS application
type CMS struct {
	plugins   map[string]*Plugin // slug -> Plugin
	instances map[string]*VMInstance
	mutex     sync.RWMutex
	vmManager *VMManager
}

// VMManager handles Firecracker microVM operations
type VMManager struct {
	firecrackerPath string
	kernelPath      string
	instances       map[string]*firecracker.Machine
	ipPool          map[string]string // instanceID -> IP mapping
	usedIPs         map[string]bool   // IP -> used status
	nextIP          int               // Next available IP (2-254)
	mutex           sync.RWMutex
}

// NewCMS creates a new CMS instance
func NewCMS() *CMS {
	cms := &CMS{
		plugins:   make(map[string]*Plugin),
		instances: make(map[string]*VMInstance),
		vmManager: NewVMManager(),
	}

	// Load existing plugins from disk
	cms.loadPlugins()

	return cms
}

// NewVMManager creates a new VM manager
func NewVMManager() *VMManager {
	firecrackerPath := os.Getenv("FIRECRACKER_PATH")
	if firecrackerPath == "" {
		firecrackerPath = "/usr/local/bin/firecracker"
	}

	kernelPath := os.Getenv("KERNEL_PATH")
	if kernelPath == "" {
		kernelPath = "./kernel/vmlinux"
	}

	return &VMManager{
		firecrackerPath: firecrackerPath,
		kernelPath:      kernelPath,
		instances:       make(map[string]*firecracker.Machine),
		ipPool:          make(map[string]string),
		usedIPs:         make(map[string]bool),
		nextIP:          2, // Start from 192.168.127.2
	}
}

// Start starts the CMS web server
func (cms *CMS) Start(port string) error {
	r := mux.NewRouter()

	// Plugin management endpoints
	r.HandleFunc("/api/plugins", cms.handleListPlugins).Methods("GET")
	r.HandleFunc("/api/plugins", cms.handleUploadPlugin).Methods("POST")
	r.HandleFunc("/api/plugins/{id}", cms.handleGetPlugin).Methods("GET")
	r.HandleFunc("/api/plugins/{id}", cms.handleDeletePlugin).Methods("DELETE")

	// Plugin activation endpoints
	r.HandleFunc("/api/plugins/{slug}/activate", cms.handleActivatePlugin).Methods("POST")
	r.HandleFunc("/api/plugins/{slug}/deactivate", cms.handleDeactivatePlugin).Methods("POST")

	// SINGLE EXECUTE ENDPOINT (WordPress-style)
	r.HandleFunc("/api/execute", cms.handleExecuteAction).Methods("POST")

	// VM instance endpoints (legacy - might be removed later)
	r.HandleFunc("/api/instances", cms.handleListInstances).Methods("GET")
	r.HandleFunc("/api/instances", cms.handleCreateInstance).Methods("POST")
	r.HandleFunc("/api/instances/{id}", cms.handleGetInstance).Methods("GET")
	r.HandleFunc("/api/instances/{id}", cms.handleDeleteInstance).Methods("DELETE")

	// Plugin execution endpoint (legacy - use /api/execute instead)
	r.HandleFunc("/api/plugins/{id}/execute", cms.handleExecutePlugin).Methods("POST")

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("CMS is running"))
	}).Methods("GET")

	logger.Info("Starting CMS server", "port", port)
	return http.ListenAndServe(":"+port, r)
}

// handleListPlugins returns all registered plugins
func (cms *CMS) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling list plugins request",
		"method", r.Method,
		"url", r.URL.String(),
		"remote_addr", r.RemoteAddr,
		"user_agent", r.UserAgent(),
	)

	cms.mutex.RLock()
	defer cms.mutex.RUnlock()

	plugins := make([]*Plugin, 0, len(cms.plugins))
	for _, plugin := range cms.plugins {
		plugins = append(plugins, plugin)
	}

	logger.Info("Listed plugins", "count", len(plugins))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugins)
}

// handleUploadPlugin handles plugin upload and registration
func (cms *CMS) handleUploadPlugin(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling plugin upload request",
		"method", r.Method,
		"url", r.URL.String(),
		"remote_addr", r.RemoteAddr,
		"content_length", r.ContentLength,
	)

	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		logger.Error("Failed to parse multipart form", "error", err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get optional metadata from form (can be overridden by plugin.json)
	formName := r.FormValue("name")
	formDescription := r.FormValue("description")

	logger.Debug("Plugin upload form data",
		"form_name", formName,
		"form_description", formDescription,
		"form_fields", len(r.MultipartForm.Value),
	)

	// Get uploaded ZIP file (containing rootfs.ext4 + plugin.json)
	file, header, err := r.FormFile("plugin")
	if err != nil {
		logger.Error("Failed to get uploaded file", "error", err)
		http.Error(w, "Failed to get uploaded plugin ZIP file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Verify it's a ZIP file
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		logger.Error("Invalid file type", "filename", header.Filename)
		http.Error(w, "Plugin must be a ZIP file containing rootfs.ext4 and plugin.json", http.StatusBadRequest)
		return
	}

	logger.Debug("Received plugin ZIP file",
		"filename", header.Filename,
		"size", header.Size,
		"content_type", header.Header.Get("Content-Type"),
	)

	// Create plugins directory if it doesn't exist
	pluginsDir := "/app/data/plugins"
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		logger.Error("Failed to create plugins directory", "path", pluginsDir, "error", err)
		http.Error(w, "Failed to create plugins directory", http.StatusInternalServerError)
		return
	}

	// Save the ZIP file temporarily for extraction (use system temp, not plugins dir)
	tempDir, err := os.MkdirTemp("", "cms-plugin-upload-")
	if err != nil {
		logger.Error("Failed to create temp directory", "error", err)
		http.Error(w, "Failed to create temp directory", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	zipPath := filepath.Join(tempDir, "plugin.zip")
	dst, err := os.Create(zipPath)
	if err != nil {
		logger.Error("Failed to create ZIP file", "path", zipPath, "error", err)
		http.Error(w, "Failed to save ZIP file", http.StatusInternalServerError)
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		logger.Error("Failed to save ZIP file", "error", err)
		http.Error(w, "Failed to save ZIP file", http.StatusInternalServerError)
		return
	}
	dst.Close()

	logger.Debug("ZIP file saved", "path", zipPath)

	// Extract ZIP file
	if err := cms.extractPluginZip(zipPath, tempDir); err != nil {
		logger.Error("Failed to extract ZIP", "error", err)
		http.Error(w, fmt.Sprintf("Failed to extract plugin ZIP: %v", err), http.StatusBadRequest)
		return
	}

	// Parse plugin.json FIRST to get the slug
	pluginJsonPath := filepath.Join(tempDir, "plugin.json")
	metadata, err := cms.parsePluginJson(pluginJsonPath)
	if err != nil {
		logger.Error("Failed to parse plugin.json", "error", err)
		http.Error(w, fmt.Sprintf("Invalid plugin.json: %v", err), http.StatusBadRequest)
		return
	}

	// Validate slug exists
	if metadata.Slug == "" {
		logger.Error("Plugin missing required slug")
		http.Error(w, "Plugin must provide a unique slug in plugin.json", http.StatusBadRequest)
		return
	}

	// Verify rootfs.ext4 exists
	rootfsTempPath := filepath.Join(tempDir, "rootfs.ext4")
	if _, err := os.Stat(rootfsTempPath); os.IsNotExist(err) {
		logger.Error("rootfs.ext4 not found in ZIP")
		http.Error(w, "rootfs.ext4 not found in plugin ZIP", http.StatusBadRequest)
		return
	}

	// NOW move rootfs to final location using SLUG-based naming
	rootfsPath := filepath.Join(pluginsDir, metadata.Slug+".ext4")

	// Remove existing plugin file if it exists (for updates)
	os.Remove(rootfsPath)

	if err := os.Rename(rootfsTempPath, rootfsPath); err != nil {
		logger.Error("Failed to move rootfs", "error", err)
		http.Error(w, "Failed to install plugin rootfs", http.StatusInternalServerError)
		return
	}

	logger.Debug("Plugin extracted successfully", "plugin_slug", metadata.Slug, "rootfs_path", rootfsPath)

	// Use form data as fallback if not provided in plugin metadata
	if metadata.Name == "" {
		metadata.Name = formName
	}
	if metadata.Description == "" {
		metadata.Description = formDescription
	}

	// Check if plugin with this slug already exists
	cms.mutex.Lock()
	if existingPlugin, exists := cms.plugins[metadata.Slug]; exists {
		// Update existing plugin
		logger.Info("Updating existing plugin", "slug", metadata.Slug)

		// Update the plugin (rootfs already replaced above)
		existingPlugin.Name = metadata.Name
		existingPlugin.Description = metadata.Description
		existingPlugin.Version = metadata.Version
		existingPlugin.Author = metadata.Author
		existingPlugin.RootFSPath = rootfsPath
		existingPlugin.UpdatedAt = time.Now()
		existingPlugin.Status = "uploaded" // Will be set to ready after health check
		existingPlugin.Actions = metadata.Actions
		existingPlugin.Health = PluginHealth{Status: "unknown"}

		cms.mutex.Unlock()

		// Perform health check to verify plugin is working
		logger.Info("Performing health check on updated plugin", "plugin_slug", existingPlugin.Slug)
		if err := cms.verifyPluginHealth(existingPlugin); err != nil {
			logger.Error("Plugin health check failed", "plugin_slug", existingPlugin.Slug, "error", err)
			http.Error(w, fmt.Sprintf("Plugin health check failed: %v", err), http.StatusBadRequest)
			return
		}

		// Update plugin status to ready after successful health check
		cms.mutex.Lock()
		existingPlugin.Status = "ready"
		existingPlugin.Health.Status = "healthy"
		existingPlugin.Health.LastCheck = time.Now()
		cms.mutex.Unlock()

		logger.Info("Plugin updated and verified successfully",
			"slug", metadata.Slug,
			"name", metadata.Name,
			"version", metadata.Version,
		)

		// Save plugins to disk
		cms.mutex.Lock()
		if err := cms.savePlugins(); err != nil {
			logger.Error("Failed to save plugins", "error", err)
		}
		cms.mutex.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(existingPlugin)
		return
	}

	// Create new plugin record
	plugin := &Plugin{
		Slug:        metadata.Slug,
		Name:        metadata.Name,
		Description: metadata.Description,
		Version:     metadata.Version,
		Author:      metadata.Author,
		RootFSPath:  rootfsPath,
		KernelPath:  cms.vmManager.kernelPath,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Status:      "ready",
		Health:      PluginHealth{Status: "unknown"},
		Actions:     metadata.Actions,
		Priority:    0,
	}

	cms.plugins[metadata.Slug] = plugin
	cms.mutex.Unlock()

	// Perform health check to verify plugin is working
	logger.Info("Performing health check on uploaded plugin", "plugin_slug", plugin.Slug)
	if err := cms.verifyPluginHealth(plugin); err != nil {
		// Remove the plugin from registry if health check fails
		cms.mutex.Lock()
		delete(cms.plugins, plugin.Slug)
		cms.mutex.Unlock()

		// Clean up plugin files
		os.Remove(plugin.RootFSPath)

		logger.Error("Plugin health check failed", "plugin_slug", plugin.Slug, "error", err)
		http.Error(w, fmt.Sprintf("Plugin health check failed: %v", err), http.StatusBadRequest)
		return
	}

	// Update plugin status to ready after successful health check
	cms.mutex.Lock()
	plugin.Status = "ready"
	plugin.Health.Status = "healthy"
	plugin.Health.LastCheck = time.Now()
	cms.mutex.Unlock()

	logger.Info("Plugin uploaded and verified successfully",
		"plugin_slug", plugin.Slug,
		"name", metadata.Name,
		"version", metadata.Version,
		"actions", len(metadata.Actions),
	)

	// Save plugins to disk
	if err := cms.savePlugins(); err != nil {
		logger.Error("Failed to save plugins", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(plugin)
}

// handleGetPlugin returns a specific plugin by slug
func (cms *CMS) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["id"] // Using 'id' but it's actually a slug now

	logger.Debug("Handling get plugin request",
		"plugin_slug", pluginSlug,
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.RLock()
	plugin, exists := cms.plugins[pluginSlug]
	cms.mutex.RUnlock()

	if !exists {
		logger.Warn("Plugin not found", "plugin_slug", pluginSlug)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	logger.Debug("Retrieved plugin", "plugin_slug", pluginSlug, "name", plugin.Name, "version", plugin.Version)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugin)
}

// handleDeletePlugin deletes a plugin by slug
func (cms *CMS) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["id"] // Using 'id' but it's actually a slug now

	logger.Debug("Handling delete plugin request",
		"plugin_slug", pluginSlug,
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		logger.Warn("Plugin not found for deletion", "plugin_slug", pluginSlug)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	// Remove rootfs file
	if err := os.Remove(plugin.RootFSPath); err != nil {
		logger.Error("Failed to remove rootfs file", "plugin_slug", pluginSlug, "error", err)
	}

	delete(cms.plugins, pluginSlug)

	logger.Info("Plugin deleted successfully",
		"plugin_slug", pluginSlug,
		"name", plugin.Name,
		"version", plugin.Version,
		"rootfs_path", plugin.RootFSPath,
	)

	// Save plugins to disk
	if err := cms.savePlugins(); err != nil {
		logger.Error("Failed to save plugins", "error", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleListInstances returns all VM instances
func (cms *CMS) handleListInstances(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling list instances request",
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.RLock()
	defer cms.mutex.RUnlock()

	instances := make([]*VMInstance, 0, len(cms.instances))
	for _, instance := range cms.instances {
		instances = append(instances, instance)
	}

	logger.Info("Listed instances", "count", len(instances))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instances)
}

// handleCreateInstance creates a new VM instance
func (cms *CMS) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling create instance request",
		"method", r.Method,
		"url", r.URL.String(),
	)

	var req struct {
		PluginSlug string `json:"plugin_slug"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Error("Failed to decode create instance request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	logger.Debug("Creating instance", "plugin_slug", req.PluginSlug)

	cms.mutex.RLock()
	plugin, exists := cms.plugins[req.PluginSlug]
	cms.mutex.RUnlock()

	if !exists {
		logger.Warn("Plugin not found for instance creation", "plugin_slug", req.PluginSlug)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	instanceID := generateID()
	instance := &VMInstance{
		ID:         instanceID,
		PluginSlug: req.PluginSlug,
		Status:     "creating",
		CreatedAt:  time.Now(),
	}

	logger.Info("Creating new instance",
		"instance_id", instanceID,
		"plugin_slug", req.PluginSlug,
		"plugin_name", plugin.Name,
	)

	// Start VM in background
	go func() {
		if err := cms.vmManager.StartVM(instanceID, plugin); err != nil {
			logger.Error("Failed to start VM", "instance_id", instanceID, "plugin_slug", req.PluginSlug, "error", err)
			cms.mutex.Lock()
			instance.Status = "failed"
			cms.mutex.Unlock()
		} else {
			logger.Info("VM instance started successfully",
				"instance_id", instanceID,
				"plugin_slug", req.PluginSlug,
			)
			cms.mutex.Lock()
			instance.Status = "running"
			cms.mutex.Unlock()
		}
	}()

	cms.mutex.Lock()
	cms.instances[instanceID] = instance
	cms.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(instance)
}

// handleGetInstance returns a specific VM instance
func (cms *CMS) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["id"]

	logger.Debug("Handling get instance request",
		"instance_id", instanceID,
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.RLock()
	instance, exists := cms.instances[instanceID]
	cms.mutex.RUnlock()

	if !exists {
		logger.Warn("Instance not found", "instance_id", instanceID)
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	logger.Debug("Retrieved instance", "instance_id", instanceID, "status", instance.Status)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instance)
}

// handleDeleteInstance stops and deletes a VM instance
func (cms *CMS) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["id"]

	logger.Debug("Handling delete instance request",
		"instance_id", instanceID,
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.Lock()
	_, exists := cms.instances[instanceID]
	if !exists {
		cms.mutex.Unlock()
		logger.Warn("Instance not found for deletion", "instance_id", instanceID)
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}
	delete(cms.instances, instanceID)
	cms.mutex.Unlock()

	logger.Info("Deleting VM instance", "instance_id", instanceID)

	// Stop VM
	if err := cms.vmManager.StopVM(instanceID); err != nil {
		logger.Error("Failed to stop VM", "instance_id", instanceID, "error", err)
	} else {
		logger.Info("VM instance stopped successfully", "instance_id", instanceID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleExecutePlugin executes a plugin via HTTP request to the microVM
func (cms *CMS) handleExecutePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["id"] // URL param is "id" but it's actually a slug

	logger.Debug("Handling plugin execution request",
		"plugin_slug", pluginSlug,
		"method", r.Method,
		"url", r.URL.String(),
		"content_length", r.ContentLength,
	)

	cms.mutex.RLock()
	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		cms.mutex.RUnlock()
		logger.Warn("Plugin not found for execution", "plugin_slug", pluginSlug)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}
	cms.mutex.RUnlock()

	// Parse request body for action and data
	var requestBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		logger.Error("Failed to parse execution request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	action, _ := requestBody["action"].(string)
	if action == "" {
		action = "default"
	}

	logger.Info("Executing plugin",
		"plugin_slug", pluginSlug,
		"plugin_name", plugin.Name,
		"action", action,
		"request_data", requestBody,
	)

	// Generate a unique instance ID for this execution
	instanceID := generateID()

	logger.Debug("Starting VM for plugin execution",
		"instance_id", instanceID,
		"plugin_slug", pluginSlug,
		"rootfs_path", plugin.RootFSPath,
	)

	// Start the Firecracker microVM
	if err := cms.vmManager.StartVM(instanceID, plugin); err != nil {
		logger.Error("Failed to start VM for plugin execution", "plugin_slug", pluginSlug, "instance_id", instanceID, "error", err)
		http.Error(w, "Failed to start plugin VM", http.StatusInternalServerError)
		return
	}

	logger.Debug("VM started, executing command in VM", "instance_id", instanceID)

	// Execute the command in the VM via stdin/stdout
	pluginRequest := map[string]interface{}{
		"action": action,
		"data":   requestBody["data"],
	}

	result, err := cms.vmManager.ExecuteInVM(instanceID, pluginRequest)
	if err != nil {
		logger.Error("Failed to execute command in VM", "plugin_slug", pluginSlug, "instance_id", instanceID, "error", err)
		http.Error(w, "Plugin execution failed", http.StatusInternalServerError)
		return
	}

	logger.Info("Plugin execution completed successfully",
		"plugin_slug", pluginSlug,
		"instance_id", instanceID,
		"action", action,
		"result", result,
	)

	response := map[string]interface{}{
		"plugin_slug": pluginSlug,
		"status":      "executed",
		"action":      action,
		"result":      result,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	// Stop the VM immediately after execution (Lambda-like behavior)
	go func() {
		if err := cms.vmManager.StopVM(instanceID); err != nil {
			logger.Error("Failed to stop VM after execution", "instance_id", instanceID, "error", err)
		} else {
			logger.Info("VM stopped successfully after execution", "instance_id", instanceID)
		}
	}()
}

// OLD DISCOVERY FUNCTIONS REMOVED - we now use plugin.json from ZIP files

// savePlugins saves plugins to persistent storage
func (cms *CMS) savePlugins() error {
	// Note: Caller must hold cms.mutex.Lock() or cms.mutex.RLock()

	pluginsDir := "/app/data/plugins"
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		logger.Error("Failed to create plugins directory", "path", pluginsDir, "error", err)
		return err
	}

	pluginsFile := filepath.Join(pluginsDir, "plugins.json")
	data, err := json.MarshalIndent(cms.plugins, "", "  ")
	if err != nil {
		logger.Error("Failed to marshal plugins to JSON", "error", err)
		return err
	}

	if err := os.WriteFile(pluginsFile, data, 0644); err != nil {
		logger.Error("Failed to write plugins file", "path", pluginsFile, "error", err)
		return err
	}

	logger.Info("Plugins saved to registry",
		"file", pluginsFile,
		"plugin_count", len(cms.plugins),
		"file_size", len(data),
	)

	return nil
}

// loadPlugins loads plugins from persistent storage
func (cms *CMS) loadPlugins() {
	pluginsFile := "/app/data/plugins/plugins.json"

	logger.Debug("Loading plugins from registry", "file", pluginsFile)

	data, err := os.ReadFile(pluginsFile)
	if err != nil {
		logger.Info("No existing plugins registry found", "file", pluginsFile)
		return
	}

	var plugins map[string]*Plugin
	if err := json.Unmarshal(data, &plugins); err != nil {
		logger.Error("Failed to parse plugins registry", "file", pluginsFile, "error", err)
		return
	}

	cms.mutex.Lock()
	defer cms.mutex.Unlock()
	cms.plugins = plugins

	logger.Info("Loaded plugins from registry",
		"file", pluginsFile,
		"count", len(plugins),
	)
}

// makeHTTPRequest makes an HTTP request and returns the response as a map
func (cms *CMS) makeHTTPRequest(method, url string, body interface{}) (map[string]interface{}, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	var reqBody io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(bodyBytes)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// handleActivatePlugin activates a plugin
func (cms *CMS) handleActivatePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["slug"]

	logger.Debug("Handling activate plugin request", "plugin_slug", pluginSlug)

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		logger.Warn("Plugin not found for activation", "plugin_slug", pluginSlug)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	if plugin.Status == "active" {
		logger.Info("Plugin already active", "plugin_slug", pluginSlug)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_active"})
		return
	}

	plugin.Status = "active"
	plugin.UpdatedAt = time.Now()

	// Save to registry
	if err := cms.savePlugins(); err != nil {
		logger.Error("Failed to save plugins after activation", "error", err)
	}

	logger.Info("Plugin activated successfully", "plugin_slug", pluginSlug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugin)
}

// handleDeactivatePlugin deactivates a plugin
func (cms *CMS) handleDeactivatePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["slug"]

	logger.Debug("Handling deactivate plugin request", "plugin_slug", pluginSlug)

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		logger.Warn("Plugin not found for deactivation", "plugin_slug", pluginSlug)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	plugin.Status = "inactive"
	plugin.UpdatedAt = time.Now()

	// Save to registry
	if err := cms.savePlugins(); err != nil {
		logger.Error("Failed to save plugins after deactivation", "error", err)
	}

	logger.Info("Plugin deactivated successfully", "plugin_slug", pluginSlug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugin)
}

// handleExecuteAction executes an action across all plugins that hook to it
func (cms *CMS) handleExecuteAction(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling execute action request")

	// Parse request body to get the action and payload
	var requestBody struct {
		Action  string                 `json:"action"`
		Payload map[string]interface{} `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		logger.Error("Failed to parse execute action request body", "error", err)
		http.Error(w, "Invalid request body. Expected: {\"action\":\"hook.name\",\"payload\":{...}}", http.StatusBadRequest)
		return
	}

	if requestBody.Action == "" {
		logger.Error("Action is required")
		http.Error(w, "Action is required in request body", http.StatusBadRequest)
		return
	}

	actionHook := requestBody.Action
	logger.Debug("Executing action", "action_hook", actionHook)

	cms.mutex.RLock()

	// Find all plugins that hook to this action (from plugins.json registry)
	var pluginActions []struct {
		Plugin *Plugin
		Action PluginAction
	}

	for _, plugin := range cms.plugins {
		if plugin.Status != "active" {
			continue
		}

		for _, action := range plugin.Actions {
			for _, hook := range action.Hooks {
				if hook == actionHook {
					pluginActions = append(pluginActions, struct {
						Plugin *Plugin
						Action PluginAction
					}{
						Plugin: plugin,
						Action: action,
					})
				}
			}
		}
	}
	cms.mutex.RUnlock()

	if len(pluginActions) == 0 {
		logger.Warn("No active plugins found for action", "action_hook", actionHook)
		http.Error(w, fmt.Sprintf("No plugins registered for action: %s", actionHook), http.StatusNotFound)
		return
	}

	logger.Info("Executing action across plugins",
		"action_hook", actionHook,
		"plugin_count", len(pluginActions))

	// Execute action on all plugins that hook to it
	results := make([]map[string]interface{}, 0, len(pluginActions))

	for _, pa := range pluginActions {
		result, err := cms.executePluginAction(pa.Plugin, pa.Action, actionHook, requestBody.Payload)
		if err != nil {
			logger.Error("Failed to execute action on plugin",
				"plugin_slug", pa.Plugin.Slug,
				"action_hook", actionHook,
				"error", err)

			results = append(results, map[string]interface{}{
				"plugin_slug": pa.Plugin.Slug,
				"success":     false,
				"error":       err.Error(),
			})
		} else {
			results = append(results, map[string]interface{}{
				"plugin_slug": pa.Plugin.Slug,
				"success":     true,
				"result":      result,
			})
		}
	}

	response := map[string]interface{}{
		"action_hook":      actionHook,
		"executed_plugins": len(pluginActions),
		"results":          results,
		"timestamp":        time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// executePluginAction executes a specific action on a specific plugin
func (cms *CMS) executePluginAction(plugin *Plugin, action PluginAction, hook string, payload map[string]interface{}) (map[string]interface{}, error) {
	// Generate instance ID for this execution
	instanceID := generateID()

	// Start VM for the plugin (TODO: Use snapshots for performance)
	if err := cms.vmManager.StartVM(instanceID, plugin); err != nil {
		return nil, fmt.Errorf("failed to start VM: %v", err)
	}

	// Clean up VM after execution
	defer func() {
		if stopErr := cms.vmManager.StopVM(instanceID); stopErr != nil {
			logger.Error("Failed to stop VM after action execution", "instance_id", instanceID, "error", stopErr)
		}
	}()

	// Get VM IP
	vmIP, exists := cms.vmManager.getVMIP(instanceID)
	if !exists {
		return nil, fmt.Errorf("VM IP not found for instance %s", instanceID)
	}

	// Prepare request to plugin
	pluginRequest := map[string]interface{}{
		"hook":    hook,
		"payload": payload,
	}

	// Make request to plugin action endpoint
	actionURL := fmt.Sprintf("http://%s:80%s", vmIP, action.Endpoint)
	result, err := cms.makeHTTPRequest("POST", actionURL, pluginRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to execute plugin action: %v", err)
	}

	return result, nil
}

// extractPluginZip extracts a plugin ZIP file containing rootfs.ext4 and plugin.json
func (cms *CMS) extractPluginZip(zipPath, destDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open ZIP file: %v", err)
	}
	defer reader.Close()

	// Track required files
	hasRootfs := false
	hasPluginJson := false

	for _, file := range reader.File {
		// Security check: prevent path traversal
		if strings.Contains(file.Name, "..") {
			return fmt.Errorf("invalid file path in ZIP: %s", file.Name)
		}

		// Only extract required files
		if file.Name != "rootfs.ext4" && file.Name != "plugin.json" {
			continue
		}

		destPath := filepath.Join(destDir, file.Name)

		fileReader, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open file %s in ZIP: %v", file.Name, err)
		}

		destFile, err := os.Create(destPath)
		if err != nil {
			fileReader.Close()
			return fmt.Errorf("failed to create file %s: %v", destPath, err)
		}

		_, err = io.Copy(destFile, fileReader)
		fileReader.Close()
		destFile.Close()

		if err != nil {
			return fmt.Errorf("failed to extract file %s: %v", file.Name, err)
		}

		if file.Name == "rootfs.ext4" {
			hasRootfs = true
		} else if file.Name == "plugin.json" {
			hasPluginJson = true
		}
	}

	if !hasRootfs {
		return fmt.Errorf("rootfs.ext4 not found in plugin ZIP")
	}
	if !hasPluginJson {
		return fmt.Errorf("plugin.json not found in plugin ZIP")
	}

	return nil
}

// parsePluginJson reads and parses plugin.json metadata
func (cms *CMS) parsePluginJson(jsonPath string) (*Plugin, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read plugin.json: %v", err)
	}

	var metadata struct {
		Slug        string                  `json:"slug"`
		Name        string                  `json:"name"`
		Description string                  `json:"description"`
		Version     string                  `json:"version"`
		Author      string                  `json:"author"`
		Actions     map[string]PluginAction `json:"actions"`
	}

	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse plugin.json: %v", err)
	}

	// Validate required fields
	if metadata.Slug == "" {
		return nil, fmt.Errorf("plugin slug is required")
	}
	if metadata.Name == "" {
		return nil, fmt.Errorf("plugin name is required")
	}
	if metadata.Version == "" {
		return nil, fmt.Errorf("plugin version is required")
	}

	return &Plugin{
		Slug:        metadata.Slug,
		Name:        metadata.Name,
		Description: metadata.Description,
		Version:     metadata.Version,
		Author:      metadata.Author,
		Actions:     metadata.Actions,
	}, nil
}

// verifyPluginHealth starts a temporary VM and checks the plugin's health endpoint
func (cms *CMS) verifyPluginHealth(plugin *Plugin) error {
	logger.Debug("Starting health check VM", "plugin_slug", plugin.Slug)

	// Generate temporary instance ID for health check
	healthCheckID := "health-" + generateID()

	// Start temporary VM for health check
	err := cms.vmManager.StartVM(healthCheckID, plugin)
	if err != nil {
		return fmt.Errorf("failed to start health check VM: %v", err)
	}

	// Clean up VM after health check
	defer func() {
		if stopErr := cms.vmManager.StopVM(healthCheckID); stopErr != nil {
			logger.Error("Failed to stop health check VM", "instance_id", healthCheckID, "error", stopErr)
		}
	}()

	// Get VM IP
	vmIP, exists := cms.vmManager.getVMIP(healthCheckID)
	if !exists {
		return fmt.Errorf("VM IP not found for health check instance %s", healthCheckID)
	}

	// Try to reach health endpoint with retries
	maxRetries := 10
	retryDelay := 500 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		logger.Debug("Health check attempt",
			"plugin_slug", plugin.Slug,
			"attempt", attempt,
			"vm_ip", vmIP)

		healthURL := fmt.Sprintf("http://%s:80/health", vmIP)
		result, err := cms.makeHTTPRequest("GET", healthURL, nil)

		if err == nil {
			// Check if the response indicates healthy status
			if status, ok := result["status"].(string); ok && status == "healthy" {
				logger.Info("Plugin health check passed", "plugin_slug", plugin.Slug)
				return nil
			}
		}

		if attempt < maxRetries {
			time.Sleep(retryDelay)
		}
	}

	return fmt.Errorf("plugin failed health check after %d attempts", maxRetries)
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func main() {
	// Initialize structured logging
	if err := setupLogger(); err != nil {
		log.Fatal("Failed to setup logger:", err)
	}

	logger.Info("Starting CMS application",
		"version", "1.0.0",
		"timestamp", time.Now().Format(time.RFC3339),
	)

	cms := NewCMS()

	port := os.Getenv("CMS_PORT")
	if port == "" {
		port = "8080"
	}

	logger.Info("CMS configuration",
		"port", port,
		"firecracker_path", os.Getenv("FIRECRACKER_PATH"),
		"kernel_path", os.Getenv("KERNEL_PATH"),
	)

	if err := cms.Start(port); err != nil {
		logger.Error("Failed to start CMS", "error", err)
		log.Fatal(err)
	}
}
