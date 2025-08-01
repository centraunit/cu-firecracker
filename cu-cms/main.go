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
	"sync"
	"time"

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

// Plugin represents a CMS plugin
type Plugin struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	RootFSPath  string    `json:"rootfs_path"`
	KernelPath  string    `json:"kernel_path"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`
}

// VMInstance represents a running microVM instance
type VMInstance struct {
	ID        string    `json:"id"`
	PluginID  string    `json:"plugin_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	IP        string    `json:"ip,omitempty"`
}

// CMS represents the main CMS application
type CMS struct {
	plugins   map[string]*Plugin
	instances map[string]*VMInstance
	mutex     sync.RWMutex
	vmManager *VMManager
}

// VMManager handles Firecracker microVM operations
type VMManager struct {
	firecrackerPath string
	kernelPath      string
	instances       map[string]*firecracker.Machine
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

	// VM instance endpoints
	r.HandleFunc("/api/instances", cms.handleListInstances).Methods("GET")
	r.HandleFunc("/api/instances", cms.handleCreateInstance).Methods("POST")
	r.HandleFunc("/api/instances/{id}", cms.handleGetInstance).Methods("GET")
	r.HandleFunc("/api/instances/{id}", cms.handleDeleteInstance).Methods("DELETE")

	// Plugin execution endpoint
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

	// Get plugin metadata
	name := r.FormValue("name")
	description := r.FormValue("description")

	logger.Debug("Plugin upload metadata",
		"name", name,
		"description", description,
		"form_fields", len(r.MultipartForm.Value),
	)

	if name == "" {
		logger.Warn("Plugin upload rejected - missing name")
		http.Error(w, "Plugin name is required", http.StatusBadRequest)
		return
	}

	// Get uploaded rootfs file
	file, header, err := r.FormFile("rootfs")
	if err != nil {
		logger.Error("Failed to get uploaded file", "error", err)
		http.Error(w, "Failed to get uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	logger.Debug("Received plugin file",
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

	// Save the rootfs file
	pluginID := generateID()
	rootfsPath := filepath.Join(pluginsDir, pluginID+".ext4")

	logger.Debug("Saving plugin file", "plugin_id", pluginID, "path", rootfsPath)

	dst, err := os.Create(rootfsPath)
	if err != nil {
		logger.Error("Failed to create rootfs file", "plugin_id", pluginID, "path", rootfsPath, "error", err)
		http.Error(w, "Failed to create rootfs file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		logger.Error("Failed to save rootfs file", "plugin_id", pluginID, "error", err)
		http.Error(w, "Failed to save rootfs file", http.StatusInternalServerError)
		return
	}

	// Create plugin record
	plugin := &Plugin{
		ID:          pluginID,
		Name:        name,
		Description: description,
		RootFSPath:  rootfsPath,
		KernelPath:  cms.vmManager.kernelPath,
		CreatedAt:   time.Now(),
		Status:      "ready",
	}

	cms.mutex.Lock()
	cms.plugins[pluginID] = plugin
	cms.mutex.Unlock()

	logger.Info("Plugin uploaded successfully",
		"plugin_id", pluginID,
		"name", name,
		"rootfs_path", rootfsPath,
		"size", header.Size,
	)

	// Save plugins to disk
	if err := cms.savePlugins(); err != nil {
		logger.Error("Failed to save plugins", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(plugin)
}

// handleGetPlugin returns a specific plugin
func (cms *CMS) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginID := vars["id"]

	logger.Debug("Handling get plugin request",
		"plugin_id", pluginID,
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.RLock()
	plugin, exists := cms.plugins[pluginID]
	cms.mutex.RUnlock()

	if !exists {
		logger.Warn("Plugin not found", "plugin_id", pluginID)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	logger.Debug("Retrieved plugin", "plugin_id", pluginID, "name", plugin.Name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugin)
}

// handleDeletePlugin deletes a plugin
func (cms *CMS) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginID := vars["id"]

	logger.Debug("Handling delete plugin request",
		"plugin_id", pluginID,
		"method", r.Method,
		"url", r.URL.String(),
	)

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginID]
	if !exists {
		logger.Warn("Plugin not found for deletion", "plugin_id", pluginID)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	// Remove rootfs file
	if err := os.Remove(plugin.RootFSPath); err != nil {
		logger.Error("Failed to remove rootfs file", "plugin_id", pluginID, "error", err)
	}

	delete(cms.plugins, pluginID)

	logger.Info("Plugin deleted successfully",
		"plugin_id", pluginID,
		"name", plugin.Name,
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
		PluginID string `json:"plugin_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Error("Failed to decode create instance request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	logger.Debug("Creating instance", "plugin_id", req.PluginID)

	cms.mutex.RLock()
	plugin, exists := cms.plugins[req.PluginID]
	cms.mutex.RUnlock()

	if !exists {
		logger.Warn("Plugin not found for instance creation", "plugin_id", req.PluginID)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	instanceID := generateID()
	instance := &VMInstance{
		ID:        instanceID,
		PluginID:  req.PluginID,
		Status:    "creating",
		CreatedAt: time.Now(),
	}

	logger.Info("Creating VM instance",
		"instance_id", instanceID,
		"plugin_id", req.PluginID,
		"plugin_name", plugin.Name,
	)

	// Start VM in background
	go func() {
		if err := cms.vmManager.StartVM(instanceID, plugin); err != nil {
			logger.Error("Failed to start VM", "instance_id", instanceID, "plugin_id", req.PluginID, "error", err)
			cms.mutex.Lock()
			instance.Status = "failed"
			cms.mutex.Unlock()
		} else {
			logger.Info("VM instance started successfully",
				"instance_id", instanceID,
				"plugin_id", req.PluginID,
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
	pluginID := vars["id"]

	logger.Debug("Handling plugin execution request",
		"plugin_id", pluginID,
		"method", r.Method,
		"url", r.URL.String(),
		"content_length", r.ContentLength,
	)

	cms.mutex.RLock()
	plugin, exists := cms.plugins[pluginID]
	cms.mutex.RUnlock()

	if !exists {
		logger.Warn("Plugin not found for execution", "plugin_id", pluginID)
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

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
		"plugin_id", pluginID,
		"plugin_name", plugin.Name,
		"action", action,
		"request_data", requestBody,
	)

	// Generate a unique instance ID for this execution
	instanceID := generateID()

	logger.Debug("Starting VM for plugin execution",
		"instance_id", instanceID,
		"plugin_id", pluginID,
		"rootfs_path", plugin.RootFSPath,
	)

	// Start the Firecracker microVM
	if err := cms.vmManager.StartVM(instanceID, plugin); err != nil {
		logger.Error("Failed to start VM for plugin execution", "plugin_id", pluginID, "instance_id", instanceID, "error", err)
		http.Error(w, "Failed to start plugin VM", http.StatusInternalServerError)
		return
	}

	logger.Debug("VM started, waiting for boot", "instance_id", instanceID)

	// Firecracker VMs start very quickly, no need for long waits
	// Just a brief moment for the kernel to initialize
	time.Sleep(500 * time.Millisecond)

	// Make HTTP request to the plugin's server inside the microVM
	// In a production environment, you'd get the VM's IP from the network setup
	// For now, we'll use the expected IP from our CNI configuration
	pluginURL := fmt.Sprintf("http://172.16.0.2:8080/execute")

	logger.Debug("Making request to plugin", "url", pluginURL, "instance_id", instanceID)

	// Prepare the request to the plugin
	pluginRequest := map[string]interface{}{
		"action": action,
		"data":   requestBody["data"],
	}

	requestBodyBytes, _ := json.Marshal(pluginRequest)

	logger.Debug("Plugin request payload",
		"instance_id", instanceID,
		"payload_size", len(requestBodyBytes),
		"action", action,
	)

	// Make HTTP request to plugin with increased timeout for plugin startup
	client := &http.Client{
		Timeout: 60 * time.Second, // Increased timeout to allow plugin startup
	}
	resp, err := client.Post(pluginURL, "application/json", bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		logger.Error("Failed to communicate with plugin", "plugin_id", pluginID, "instance_id", instanceID, "error", err)

		// Stop the VM since communication failed
		go func() {
			if err := cms.vmManager.StopVM(instanceID); err != nil {
				logger.Error("Failed to stop VM after communication failure", "instance_id", instanceID, "error", err)
			}
		}()

		http.Error(w, "Failed to communicate with plugin", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	logger.Debug("Received plugin response",
		"instance_id", instanceID,
		"status_code", resp.StatusCode,
		"content_length", resp.ContentLength,
	)

	// Parse the plugin's response
	var pluginResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&pluginResponse); err != nil {
		logger.Error("Failed to parse plugin response", "instance_id", instanceID, "error", err)

		// Stop the VM since parsing failed
		go func() {
			if err := cms.vmManager.StopVM(instanceID); err != nil {
				logger.Error("Failed to stop VM after parsing failure", "instance_id", instanceID, "error", err)
			}
		}()

		http.Error(w, "Failed to parse plugin response", http.StatusInternalServerError)
		return
	}

	logger.Info("Plugin execution completed successfully",
		"plugin_id", pluginID,
		"instance_id", instanceID,
		"action", action,
		"response_success", pluginResponse["success"],
	)

	// Stop the VM after execution - no delay needed
	go func() {
		logger.Debug("Stopping VM after execution", "instance_id", instanceID)
		if err := cms.vmManager.StopVM(instanceID); err != nil {
			logger.Error("Failed to stop VM after execution", "instance_id", instanceID, "error", err)
		} else {
			logger.Info("VM stopped after execution", "instance_id", instanceID)
		}
	}()

	response := map[string]interface{}{
		"plugin_id": pluginID,
		"status":    "executed",
		"result":    pluginResponse,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// savePlugins saves plugins to disk
func (cms *CMS) savePlugins() error {
	cms.mutex.RLock()
	defer cms.mutex.RUnlock()

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

	logger.Info("Plugins saved to disk",
		"file", pluginsFile,
		"plugin_count", len(cms.plugins),
		"file_size", len(data),
	)

	return nil
}

// loadPlugins loads plugins from disk
func (cms *CMS) loadPlugins() {
	pluginsFile := "/app/data/plugins/plugins.json"

	logger.Debug("Loading plugins from disk", "file", pluginsFile)

	data, err := os.ReadFile(pluginsFile)
	if err != nil {
		logger.Info("No existing plugins found", "file", pluginsFile, "error", err)
		return
	}

	var plugins map[string]*Plugin
	if err := json.Unmarshal(data, &plugins); err != nil {
		logger.Error("Failed to parse plugins file", "file", pluginsFile, "error", err)
		return
	}

	cms.mutex.Lock()
	defer cms.mutex.Unlock()
	cms.plugins = plugins

	logger.Info("Loaded plugins from disk",
		"file", pluginsFile,
		"count", len(plugins),
		"file_size", len(data),
	)

	// Log details of each loaded plugin
	for id, plugin := range plugins {
		logger.Debug("Loaded plugin",
			"plugin_id", id,
			"name", plugin.Name,
			"description", plugin.Description,
			"status", plugin.Status,
			"created_at", plugin.CreatedAt,
			"rootfs_path", plugin.RootFSPath,
		)
	}
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
