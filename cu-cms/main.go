package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"archive/zip"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// Configuration constants
const (
	DefaultPort            = "80"
	DefaultHTTPTimeout     = 1 * time.Second // Fast timeout for instant execution
	DefaultHealthRetries   = 15
	DefaultHealthDelay     = 1 * time.Second
	MaxPluginExecutionTime = 30 * time.Second
	MaxConcurrentActions   = 10 // Limit concurrent plugin executions
)

// Global logger and HTTP client pool
var (
	logger     *logrus.Logger
	httpClient *http.Client
)

// HTTPResponse represents a standardized API response
type HTTPResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// ValidationError represents input validation errors
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ActionExecutionResult represents the result of plugin action execution
type ActionExecutionResult struct {
	PluginSlug    string        `json:"plugin_slug"`
	Success       bool          `json:"success"`
	Result        interface{}   `json:"result,omitempty"`
	Error         string        `json:"error,omitempty"`
	ExecutionTime time.Duration `json:"execution_time_ms"`
}

// Plugin represents a CMS plugin with action-based hooks
type Plugin struct {
	Slug        string                  `json:"slug"` // Unique identifier
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Version     string                  `json:"version"`
	Author      string                  `json:"author"`
	Runtime     string                  `json:"runtime"` // Runtime environment (python, typescript, php, etc.)
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

// CMS represents the main CMS application with enhanced features
type CMS struct {
	plugins         map[string]*Plugin // slug -> Plugin
	instances       map[string]*VMInstance
	mutex           sync.RWMutex
	vmManager       *VMManager
	httpServer      *http.Server
	actionSemaphore chan struct{} // Limit concurrent actions
	shutdownChan    chan bool
	ctx             context.Context
	cancel          context.CancelFunc
}

// VMManager handles Firecracker microVM operations
type VMManager struct {
	firecrackerPath string
	kernelPath      string
	instances       map[string]*firecracker.Machine
	ipPool          map[string]string // instanceID -> IP mapping
	usedIPs         map[string]bool   // IP -> used status
	nextIP          int               // Next available IP (2-254)
	snapshotDir     string            // Directory for storing plugin snapshots
	mutex           sync.RWMutex
}

// NewCMS creates a new CMS instance with enhanced features
func NewCMS() *CMS {
	ctx, cancel := context.WithCancel(context.Background())

	cms := &CMS{
		plugins:         make(map[string]*Plugin),
		instances:       make(map[string]*VMInstance),
		vmManager:       NewVMManager(),
		actionSemaphore: make(chan struct{}, MaxConcurrentActions),
		shutdownChan:    make(chan bool, 1),
		ctx:             ctx,
		cancel:          cancel,
	}

	// Load existing plugins from disk
	cms.loadPlugins()

	// Initialize snapshot directory
	if err := cms.vmManager.initSnapshotDir(); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to initialize snapshot directory")
	}

	// Setup graceful shutdown
	cms.setupGracefulShutdown()

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
		nextIP:          2,                  // Start from 192.168.127.2
		snapshotDir:     "./data/snapshots", // Directory for plugin snapshots
	}
}

// initSnapshotDir creates the snapshot directory if it doesn't exist
func (vm *VMManager) initSnapshotDir() error {
	return os.MkdirAll(vm.snapshotDir, 0755)
}

// setupGracefulShutdown configures signal handling for graceful shutdown
func (cms *CMS) setupGracefulShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.WithFields(logrus.Fields{"signal": sig}).Info("Received shutdown signal")
		cms.Shutdown()
	}()
}

// Shutdown gracefully shuts down the CMS
func (cms *CMS) Shutdown() {
	logger.Info("Initiating graceful shutdown")

	// Cancel context to stop ongoing operations
	cms.cancel()

	// Shutdown HTTP server
	if cms.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := cms.httpServer.Shutdown(ctx); err != nil {
			logger.WithFields(logrus.Fields{"error": err}).Error("HTTP server shutdown failed")
		}
	}

	// Stop all running VMs
	cms.vmManager.StopAllVMs()

	// Signal shutdown completion
	cms.shutdownChan <- true
	logger.Info("Graceful shutdown completed")
}

// Start starts the CMS web server with enhanced configuration
func (cms *CMS) Start(port string) error {
	r := mux.NewRouter()

	// Add middleware for request logging and validation
	r.Use(cms.loggingMiddleware)
	r.Use(cms.recoveryMiddleware)

	// Plugin management endpoints
	r.HandleFunc("/api/plugins", cms.handleListPlugins).Methods("GET")
	r.HandleFunc("/api/plugins", cms.handleUploadPlugin).Methods("POST")
	r.HandleFunc("/api/plugins/{slug}", cms.validateSlugMiddleware(cms.handleGetPlugin)).Methods("GET")
	r.HandleFunc("/api/plugins/{slug}", cms.validateSlugMiddleware(cms.handleDeletePlugin)).Methods("DELETE")

	// Plugin activation endpoints
	r.HandleFunc("/api/plugins/{slug}/activate", cms.validateSlugMiddleware(cms.handleActivatePlugin)).Methods("POST")
	r.HandleFunc("/api/plugins/{slug}/deactivate", cms.validateSlugMiddleware(cms.handleDeactivatePlugin)).Methods("POST")

	// Action execution endpoint (WordPress-style) with enhanced validation
	r.HandleFunc("/api/execute", cms.handleExecuteAction).Methods("POST")

	// Health check with detailed status
	r.HandleFunc("/health", cms.handleHealthCheck).Methods("GET")

	// Metrics endpoint
	r.HandleFunc("/metrics", cms.handleMetrics).Methods("GET")

	cms.httpServer = &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	logger.WithFields(logrus.Fields{"port": port}).Info("Starting CMS server")

	// Start server in goroutine
	go func() {
		if err := cms.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithFields(logrus.Fields{"error": err}).Error("Server failed")
		}
	}()

	// Wait for shutdown signal
	<-cms.shutdownChan
	return nil
}

// Middleware functions

// loggingMiddleware logs all HTTP requests
func (cms *CMS) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap ResponseWriter to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		logger.WithFields(logrus.Fields{
			"method":      r.Method,
			"url":         r.URL.String(),
			"remote_addr": r.RemoteAddr,
			"user_agent":  r.UserAgent(),
			"status_code": wrapped.statusCode,
			"duration_ms": time.Since(start).Milliseconds(),
		}).Debug("HTTP request")
	})
}

// recoveryMiddleware recovers from panics and logs them
func (cms *CMS) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.WithFields(logrus.Fields{"error": err}).Error("Panic recovered")
				cms.sendErrorResponse(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// validateSlugMiddleware validates plugin slug format
func (cms *CMS) validateSlugMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		slug := vars["slug"]

		if err := cms.validateSlug(slug); err != nil {
			cms.sendErrorResponse(w, err.Error(), http.StatusBadRequest)
			return
		}

		next(w, r)
	}
}

// Response helper functions

// sendSuccessResponse sends a standardized success response
func (cms *CMS) sendSuccessResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	response := HTTPResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// sendErrorResponse sends a standardized error response
func (cms *CMS) sendErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	response := HTTPResponse{
		Success:   false,
		Error:     message,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// Validation functions

// validateSlug validates plugin slug format
func (cms *CMS) validateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("slug cannot be empty")
	}
	if len(slug) > 50 {
		return fmt.Errorf("slug too long (max 50 characters)")
	}
	if !isValidSlugFormat(slug) {
		return fmt.Errorf("slug contains invalid characters (use only letters, numbers, hyphens)")
	}
	return nil
}

// isValidSlugFormat checks if slug contains only valid characters
func isValidSlugFormat(slug string) bool {
	for _, char := range slug {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return false
		}
	}
	return true
}

// validateActionRequest validates execute action request
func (cms *CMS) validateActionRequest(req *struct {
	Action  string                 `json:"action"`
	Payload map[string]interface{} `json:"payload"`
}) []ValidationError {
	var errors []ValidationError

	if req.Action == "" {
		errors = append(errors, ValidationError{
			Field:   "action",
			Message: "action is required",
		})
	}

	if len(req.Action) > 100 {
		errors = append(errors, ValidationError{
			Field:   "action",
			Message: "action name too long (max 100 characters)",
		})
	}

	return errors
}

// Enhanced handler functions

// handleListPlugins returns all registered plugins
func (cms *CMS) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling list plugins request")

	cms.mutex.RLock()
	defer cms.mutex.RUnlock()

	plugins := make([]*Plugin, 0, len(cms.plugins))
	for _, plugin := range cms.plugins {
		plugins = append(plugins, plugin)
	}

	logger.WithFields(logrus.Fields{"count": len(plugins)}).Info("Listed plugins")

	cms.sendSuccessResponse(w, plugins, http.StatusOK)
}

// handleUploadPlugin handles plugin upload and registration
func (cms *CMS) handleUploadPlugin(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Handling plugin upload request")

	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse multipart form")
		cms.sendErrorResponse(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get optional metadata from form (can be overridden by plugin.json)
	formName := r.FormValue("name")
	formDescription := r.FormValue("description")

	logger.WithFields(logrus.Fields{
		"form_name":        formName,
		"form_description": formDescription,
		"form_fields":      len(r.MultipartForm.Value),
	}).Debug("Plugin upload form data")

	// Get uploaded ZIP file (containing rootfs.ext4 + plugin.json)
	file, header, err := r.FormFile("plugin")
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to get uploaded file")
		cms.sendErrorResponse(w, "Failed to get uploaded plugin ZIP file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Verify it's a ZIP file
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		logger.WithFields(logrus.Fields{"filename": header.Filename}).Error("Invalid file type")
		cms.sendErrorResponse(w, "Plugin must be a ZIP file containing rootfs.ext4 and plugin.json", http.StatusBadRequest)
		return
	}

	logger.WithFields(logrus.Fields{
		"filename":     header.Filename,
		"size":         header.Size,
		"content_type": header.Header.Get("Content-Type"),
	}).Debug("Received plugin ZIP file")

	// Create plugins directory if it doesn't exist
	pluginsDir := "/app/data/plugins"
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		logger.WithFields(logrus.Fields{"path": pluginsDir, "error": err}).Error("Failed to create plugins directory")
		cms.sendErrorResponse(w, "Failed to create plugins directory", http.StatusInternalServerError)
		return
	}

	// Save the ZIP file temporarily for extraction (use system temp, not plugins dir)
	tempDir, err := os.MkdirTemp("", "cms-plugin-upload-")
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to create temp directory")
		cms.sendErrorResponse(w, "Failed to create temp directory", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	zipPath := filepath.Join(tempDir, "plugin.zip")
	dst, err := os.Create(zipPath)
	if err != nil {
		logger.WithFields(logrus.Fields{"path": zipPath, "error": err}).Error("Failed to create ZIP file")
		cms.sendErrorResponse(w, "Failed to save ZIP file", http.StatusInternalServerError)
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save ZIP file")
		cms.sendErrorResponse(w, "Failed to save ZIP file", http.StatusInternalServerError)
		return
	}
	dst.Close()

	logger.WithFields(logrus.Fields{"path": zipPath}).Debug("ZIP file saved")

	// Extract ZIP file
	if err := cms.extractPluginZip(zipPath, tempDir); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to extract ZIP")
		cms.sendErrorResponse(w, fmt.Sprintf("Failed to extract plugin ZIP: %v", err), http.StatusBadRequest)
		return
	}

	// Parse plugin.json FIRST to get the slug
	pluginJsonPath := filepath.Join(tempDir, "plugin.json")
	metadata, err := cms.parsePluginJson(pluginJsonPath)
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse plugin.json")
		cms.sendErrorResponse(w, fmt.Sprintf("Invalid plugin.json: %v", err), http.StatusBadRequest)
		return
	}

	// Validate slug exists
	if metadata.Slug == "" {
		logger.Error("Plugin missing required slug")
		cms.sendErrorResponse(w, "Plugin must provide a unique slug in plugin.json", http.StatusBadRequest)
		return
	}

	// Verify rootfs.ext4 exists
	rootfsTempPath := filepath.Join(tempDir, "rootfs.ext4")
	if _, err := os.Stat(rootfsTempPath); os.IsNotExist(err) {
		logger.Error("rootfs.ext4 not found in ZIP")
		cms.sendErrorResponse(w, "rootfs.ext4 not found in plugin ZIP", http.StatusBadRequest)
		return
	}

	// NOW move rootfs to final location using SLUG-based naming
	rootfsPath := filepath.Join(pluginsDir, metadata.Slug+".ext4")

	// Remove existing plugin file if it exists (for updates)
	os.Remove(rootfsPath)

	// Copy rootfs file (can't use rename due to potential cross-device link)
	if err := copyFile(rootfsTempPath, rootfsPath); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to copy rootfs")
		cms.sendErrorResponse(w, "Failed to install plugin rootfs", http.StatusInternalServerError)
		return
	}

	// Remove temp file after successful copy
	os.Remove(rootfsTempPath)

	logger.WithFields(logrus.Fields{"plugin_slug": metadata.Slug, "rootfs_path": rootfsPath}).Debug("Plugin extracted successfully")

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
		logger.WithFields(logrus.Fields{"slug": metadata.Slug}).Info("Updating existing plugin")

		// Update the plugin (rootfs already replaced above)
		existingPlugin.Name = metadata.Name
		existingPlugin.Description = metadata.Description
		existingPlugin.Version = metadata.Version
		existingPlugin.Author = metadata.Author
		existingPlugin.Runtime = metadata.Runtime
		existingPlugin.RootFSPath = rootfsPath
		existingPlugin.UpdatedAt = time.Now()
		existingPlugin.Status = "uploaded" // Will be set to ready after health check
		existingPlugin.Actions = metadata.Actions
		existingPlugin.Health = PluginHealth{Status: "unknown"}

		cms.mutex.Unlock()

		// Perform health check to verify plugin is working
		logger.WithFields(logrus.Fields{"plugin_slug": existingPlugin.Slug}).Info("Performing health check on updated plugin")
		if err := cms.verifyPluginHealth(existingPlugin); err != nil {
			logger.WithFields(logrus.Fields{"plugin_slug": existingPlugin.Slug, "error": err}).Error("Plugin health check failed")
			cms.sendErrorResponse(w, fmt.Sprintf("Plugin health check failed: %v", err), http.StatusBadRequest)
			return
		}

		// Update plugin status to ready after successful health check
		cms.mutex.Lock()
		existingPlugin.Status = "ready"
		existingPlugin.Health.Status = "healthy"
		existingPlugin.Health.LastCheck = time.Now()
		cms.mutex.Unlock()

		logger.WithFields(logrus.Fields{
			"slug":    metadata.Slug,
			"name":    metadata.Name,
			"version": metadata.Version,
		}).Info("Plugin updated and verified successfully")

		// Save plugins to disk
		cms.mutex.Lock()
		if err := cms.savePlugins(); err != nil {
			logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save plugins")
		}
		cms.mutex.Unlock()

		cms.sendSuccessResponse(w, existingPlugin, http.StatusOK)
		return
	}

	// Create new plugin record
	plugin := &Plugin{
		Slug:        metadata.Slug,
		Name:        metadata.Name,
		Description: metadata.Description,
		Version:     metadata.Version,
		Author:      metadata.Author,
		Runtime:     metadata.Runtime,
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
	logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug}).Info("Performing health check on uploaded plugin")
	if err := cms.verifyPluginHealth(plugin); err != nil {
		// Remove the plugin from registry if health check fails
		cms.mutex.Lock()
		delete(cms.plugins, plugin.Slug)
		cms.mutex.Unlock()

		// Clean up plugin files
		os.Remove(plugin.RootFSPath)

		logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug, "error": err}).Error("Plugin health check failed")
		cms.sendErrorResponse(w, fmt.Sprintf("Plugin health check failed: %v", err), http.StatusBadRequest)
		return
	}

	// Update plugin status to ready after successful health check
	cms.mutex.Lock()
	plugin.Status = "ready"
	plugin.Health.Status = "healthy"
	plugin.Health.LastCheck = time.Now()
	cms.mutex.Unlock()

	logger.WithFields(logrus.Fields{
		"plugin_slug": plugin.Slug,
		"name":        metadata.Name,
		"version":     metadata.Version,
		"actions":     len(metadata.Actions),
	}).Info("Plugin uploaded and verified successfully")

	// Save plugins to disk
	if err := cms.savePlugins(); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save plugins")
	}

	cms.sendSuccessResponse(w, plugin, http.StatusCreated)
}

// handleGetPlugin returns a specific plugin by slug
func (cms *CMS) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["slug"] // Using 'id' but it's actually a slug now

	logger.WithFields(logrus.Fields{
		"plugin_slug": pluginSlug,
		"method":      r.Method,
		"url":         r.URL.String(),
	}).Debug("Handling get plugin request")

	cms.mutex.RLock()
	plugin, exists := cms.plugins[pluginSlug]
	cms.mutex.RUnlock()

	if !exists {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Warn("Plugin not found")
		cms.sendErrorResponse(w, "Plugin not found", http.StatusNotFound)
		return
	}

	logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "name": plugin.Name, "version": plugin.Version}).Debug("Retrieved plugin")

	cms.sendSuccessResponse(w, plugin, http.StatusOK)
}

// handleDeletePlugin deletes a plugin by slug
func (cms *CMS) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["slug"] // Using 'id' but it's actually a slug now

	logger.WithFields(logrus.Fields{
		"plugin_slug": pluginSlug,
		"method":      r.Method,
		"url":         r.URL.String(),
	}).Debug("Handling delete plugin request")

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Warn("Plugin not found for deletion")
		cms.sendErrorResponse(w, "Plugin not found", http.StatusNotFound)
		return
	}

	// Remove rootfs file
	if err := os.Remove(plugin.RootFSPath); err != nil {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "error": err}).Error("Failed to remove rootfs file")
	}

	delete(cms.plugins, pluginSlug)

	logger.WithFields(logrus.Fields{
		"plugin_slug": pluginSlug,
		"name":        plugin.Name,
		"version":     plugin.Version,
		"rootfs_path": plugin.RootFSPath,
	}).Info("Plugin deleted successfully")

	// Save plugins to disk
	if err := cms.savePlugins(); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save plugins")
	}

	w.WriteHeader(http.StatusNoContent)
}

// savePlugins saves plugins to persistent storage
func (cms *CMS) savePlugins() error {
	// Note: Caller must hold cms.mutex.Lock() or cms.mutex.RLock()

	pluginsDir := "/app/data/plugins"
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		logger.WithFields(logrus.Fields{"path": pluginsDir, "error": err}).Error("Failed to create plugins directory")
		return err
	}

	pluginsFile := filepath.Join(pluginsDir, "plugins.json")
	data, err := json.MarshalIndent(cms.plugins, "", "  ")
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to marshal plugins to JSON")
		return err
	}

	if err := os.WriteFile(pluginsFile, data, 0644); err != nil {
		logger.WithFields(logrus.Fields{"path": pluginsFile, "error": err}).Error("Failed to write plugins file")
		return err
	}

	logger.WithFields(logrus.Fields{
		"file":         pluginsFile,
		"plugin_count": len(cms.plugins),
		"file_size":    len(data),
	}).Info("Plugins saved to registry")

	return nil
}

// loadPlugins loads plugins from persistent storage
func (cms *CMS) loadPlugins() {
	pluginsFile := "/app/data/plugins/plugins.json"

	logger.WithFields(logrus.Fields{"file": pluginsFile}).Debug("Loading plugins from registry")

	data, err := os.ReadFile(pluginsFile)
	if err != nil {
		logger.WithFields(logrus.Fields{"file": pluginsFile}).Info("No existing plugins registry found")
		return
	}

	var plugins map[string]*Plugin
	if err := json.Unmarshal(data, &plugins); err != nil {
		logger.WithFields(logrus.Fields{"file": pluginsFile, "error": err}).Error("Failed to parse plugins registry")
		return
	}

	cms.mutex.Lock()
	defer cms.mutex.Unlock()
	cms.plugins = plugins

	logger.WithFields(logrus.Fields{
		"file":  pluginsFile,
		"count": len(plugins),
	}).Info("Loaded plugins from registry")
}

// makeHTTPRequest makes an HTTP request and returns the response as a map
func (cms *CMS) makeHTTPRequest(method, url string, body interface{}) (map[string]interface{}, error) {
	client := &http.Client{Timeout: 10 * time.Second} // Increased for snapshot resumption

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

// healthCheckWithRetries performs health check with retry logic
func (cms *CMS) healthCheckWithRetries(vmIP, pluginSlug string, maxRetries int, retryDelay time.Duration) error {
	healthURL := fmt.Sprintf("http://%s:80/health", vmIP)

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		response, err := cms.makeHTTPRequest("GET", healthURL, nil)
		if err != nil {
			lastErr = err
			logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "attempt": attempt, "max_retries": maxRetries, "error": err}).Debug("Health check failed, retrying")

			if attempt < maxRetries {
				time.Sleep(retryDelay)
				continue
			}
		} else {
			// Validate health response
			if status, ok := response["status"].(string); ok && status == "healthy" {
				logger.WithFields(logrus.Fields{
					"plugin_slug": pluginSlug,
					"attempt":     attempt,
				}).Info("Health check successful")
				return nil
			} else {
				lastErr = fmt.Errorf("unhealthy status response: %v", response)
				logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "attempt": attempt, "response": response}).Debug("Health check returned unhealthy status, retrying")

				if attempt < maxRetries {
					time.Sleep(retryDelay)
					continue
				}
			}
		}
	}

	return fmt.Errorf("health check failed after %d attempts: %v", maxRetries, lastErr)
}

// handleActivatePlugin activates a plugin and creates snapshot for fast execution
func (cms *CMS) handleActivatePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["slug"]

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		cms.sendErrorResponse(w, "Plugin not found", http.StatusNotFound)
		return
	}

	if plugin.Status == "active" {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Info("Plugin already active")
		cms.sendSuccessResponse(w, plugin, http.StatusOK)
		return
	}

	// If snapshot already exists, just mark as active
	if cms.vmManager.HasSnapshot(pluginSlug) {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Info("Plugin has existing snapshot, marking as active")
		plugin.Status = "active"
		plugin.UpdatedAt = time.Now()

		if err := cms.savePlugins(); err != nil {
			logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save plugins after activation")
			cms.sendErrorResponse(w, "Failed to save plugin state", http.StatusInternalServerError)
			return
		}

		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Info("Plugin activated with existing snapshot")
		cms.sendSuccessResponse(w, plugin, http.StatusOK)
		return
	}

	// Create temporary VM to warm up and take snapshot
	instanceID := pluginSlug // Use plugin slug as instance ID for consistency
	logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "instance_id": instanceID}).Info("Creating VM for snapshot generation")

	if err := cms.vmManager.StartVM(instanceID, plugin); err != nil {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "error": err}).Error("Failed to start VM for snapshot")
		cms.sendErrorResponse(w, "Failed to start VM", http.StatusInternalServerError)
		return
	}

	// Clean up temporary VM after snapshotting
	defer func() {
		if stopErr := cms.vmManager.StopVM(instanceID); stopErr != nil {
			logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": stopErr}).Error("Failed to stop temporary VM after snapshot")
		}
	}()

	// Wait for VM to be fully ready (plugin app started)
	time.Sleep(3 * time.Second)

	// Perform health check to ensure VM is ready (with retries for Flask startup)
	vmIP, exists := cms.vmManager.getVMIP(instanceID)
	if !exists {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Error("Failed to get VM IP for snapshot")
		cms.sendErrorResponse(w, "Failed to get VM IP", http.StatusInternalServerError)
		return
	}

	if err := cms.healthCheckWithRetries(vmIP, pluginSlug, 15, 1*time.Second); err != nil {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "attempts": 15, "error": err}).Error("VM health check failed during activation after retries")
		cms.sendErrorResponse(w, "Plugin failed health check", http.StatusInternalServerError)
		return
	}

	// Create snapshot for fast future execution
	snapshotPath := cms.vmManager.GetSnapshotPath(pluginSlug)
	if err := cms.vmManager.CreateSnapshot(instanceID, snapshotPath); err != nil {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "error": err}).Error("Failed to create snapshot")
		cms.sendErrorResponse(w, "Failed to create snapshot", http.StatusInternalServerError)
		return
	}

	plugin.Status = "active"
	plugin.UpdatedAt = time.Now()

	// Save to registry
	if err := cms.savePlugins(); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save plugins after activation")
		cms.sendErrorResponse(w, "Failed to save plugin state", http.StatusInternalServerError)
		return
	}

	logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "snapshot_path": snapshotPath}).Info("Plugin activated successfully with snapshot")

	cms.sendSuccessResponse(w, plugin, http.StatusOK)
}

// handleDeactivatePlugin deactivates a plugin and cleans up snapshots
func (cms *CMS) handleDeactivatePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginSlug := vars["slug"]

	cms.mutex.Lock()
	defer cms.mutex.Unlock()

	plugin, exists := cms.plugins[pluginSlug]
	if !exists {
		cms.sendErrorResponse(w, "Plugin not found", http.StatusNotFound)
		return
	}

	if plugin.Status == "inactive" {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Info("Plugin already inactive")
		cms.sendSuccessResponse(w, plugin, http.StatusOK)
		return
	}

	// Delete snapshot files
	if err := cms.vmManager.DeleteSnapshot(pluginSlug); err != nil {
		logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "error": err}).Warn("Failed to delete snapshot during deactivation")
		// Continue with deactivation even if snapshot deletion fails
	}

	plugin.Status = "inactive"
	plugin.UpdatedAt = time.Now()

	// Save to registry
	if err := cms.savePlugins(); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to save plugins after deactivation")
		cms.sendErrorResponse(w, "Failed to save plugin state", http.StatusInternalServerError)
		return
	}

	logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug}).Info("Plugin deactivated successfully")

	cms.sendSuccessResponse(w, plugin, http.StatusOK)
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
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to parse execute action request body")
		cms.sendErrorResponse(w, "Invalid request body. Expected: {\"action\":\"hook.name\",\"payload\":{...}}", http.StatusBadRequest)
		return
	}

	if requestBody.Action == "" {
		logger.Error("Action is required")
		cms.sendErrorResponse(w, "Action is required in request body", http.StatusBadRequest)
		return
	}

	actionHook := requestBody.Action
	logger.WithFields(logrus.Fields{"action_hook": actionHook}).Debug("Executing action")

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
		logger.WithFields(logrus.Fields{"action_hook": actionHook}).Warn("No active plugins found for action")
		cms.sendErrorResponse(w, fmt.Sprintf("No plugins registered for action: %s", actionHook), http.StatusNotFound)
		return
	}

	logger.WithFields(logrus.Fields{
		"action_hook":  actionHook,
		"plugin_count": len(pluginActions),
	}).Info("Executing action across plugins")

	// Sort plugins by priority (higher priority first)
	sort.Slice(pluginActions, func(i, j int) bool {
		return pluginActions[i].Action.Priority > pluginActions[j].Action.Priority
	})

	// Execute action on all plugins that hook to it
	results := make([]ActionExecutionResult, 0, len(pluginActions))

	for _, pa := range pluginActions {
		result, err := cms.executePluginAction(pa.Plugin, pa.Action, actionHook, requestBody.Payload)
		if err != nil {
			logger.WithFields(logrus.Fields{"plugin_slug": pa.Plugin.Slug, "action_hook": actionHook, "error": err}).Error("Failed to execute action on plugin")

			results = append(results, ActionExecutionResult{
				PluginSlug: pa.Plugin.Slug,
				Success:    false,
				Error:      err.Error(),
			})
		} else {
			results = append(results, result)
		}
	}

	response := map[string]interface{}{
		"action_hook":      actionHook,
		"executed_plugins": len(pluginActions),
		"results":          results,
		"timestamp":        time.Now().Format(time.RFC3339),
	}

	cms.sendSuccessResponse(w, response, http.StatusOK)
}

// Enhanced handler functions

// handleHealthCheck provides detailed health information
func (cms *CMS) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	cms.mutex.RLock()
	totalPlugins := len(cms.plugins)
	activePlugins := 0
	for _, plugin := range cms.plugins {
		if plugin.Status == "active" {
			activePlugins++
		}
	}
	cms.mutex.RUnlock()

	health := map[string]interface{}{
		"status":         "healthy",
		"total_plugins":  totalPlugins,
		"active_plugins": activePlugins,
		"vm_instances":   len(cms.vmManager.instances),
		"uptime":         time.Since(time.Now()).String(), // This would need startup time tracking
	}

	cms.sendSuccessResponse(w, health, http.StatusOK)
}

// handleMetrics provides basic metrics
func (cms *CMS) handleMetrics(w http.ResponseWriter, r *http.Request) {
	cms.mutex.RLock()
	metrics := map[string]interface{}{
		"plugins_total":      len(cms.plugins),
		"instances_total":    len(cms.vmManager.instances),
		"concurrent_limit":   MaxConcurrentActions,
		"concurrent_current": MaxConcurrentActions - len(cms.actionSemaphore),
	}
	cms.mutex.RUnlock()

	cms.sendSuccessResponse(w, metrics, http.StatusOK)
}

// executePluginAction executes action with enhanced error handling and performance optimization
func (cms *CMS) executePluginAction(plugin *Plugin, action PluginAction, hook string, payload map[string]interface{}) (ActionExecutionResult, error) {
	start := time.Now()
	result := ActionExecutionResult{
		PluginSlug: plugin.Slug,
	}

	// Acquire semaphore to limit concurrent executions
	select {
	case cms.actionSemaphore <- struct{}{}:
		defer func() { <-cms.actionSemaphore }()
	case <-time.After(5 * time.Second):
		result.Error = "execution queue full, try again later"
		result.ExecutionTime = time.Since(start)
		return result, fmt.Errorf("execution queue full")
	}

	// Use plugin slug as instance ID for consistency
	instanceID := plugin.Slug

	logger.WithFields(logrus.Fields{
		"plugin_slug":  plugin.Slug,
		"action_hook":  hook,
		"has_snapshot": cms.vmManager.HasSnapshot(plugin.Slug),
		"instance_id":  instanceID,
	}).Debug("Starting VM for action execution")

	var err error
	// Try snapshot resumption first (fast ~100-200ms), fallback to regular start
	if cms.vmManager.HasSnapshot(plugin.Slug) {
		logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug}).Debug("Using snapshot resumption for fast startup")
		err = cms.vmManager.ResumeFromSnapshot(instanceID, plugin)
	} else {
		logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug}).Debug("No snapshot available, using regular VM startup")
		err = cms.vmManager.StartVM(instanceID, plugin)
	}

	if err != nil {
		result.Error = fmt.Sprintf("failed to start VM: %v", err)
		result.ExecutionTime = time.Since(start)
		return result, err
	}

	// Clean up VM after execution
	defer func() {
		if stopErr := cms.vmManager.StopVM(instanceID); stopErr != nil {
			logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": stopErr}).Error("Failed to stop VM after action execution")
		}
	}()

	// Wait for plugin to be ready with health check
	vmIP, exists := cms.vmManager.getVMIP(instanceID)
	if !exists {
		result.Error = fmt.Sprintf("VM IP not found for instance %s", instanceID)
		result.ExecutionTime = time.Since(start)
		return result, fmt.Errorf("VM IP not found")
	}

	// Prepare request to plugin
	pluginRequest := map[string]interface{}{
		"hook":    hook,
		"payload": payload,
	}

	// Execute action via HTTP with timeout
	actionURL := fmt.Sprintf("http://%s:80%s", vmIP, action.Endpoint)
	response, err := cms.makeHTTPRequestWithTimeout(action.Method, actionURL, pluginRequest, DefaultHTTPTimeout)
	if err != nil {
		result.Error = fmt.Sprintf("failed to execute action: %v", err)
		result.ExecutionTime = time.Since(start)
		return result, err
	}

	result.Success = true
	result.Result = response
	result.ExecutionTime = time.Since(start)

	logger.WithFields(logrus.Fields{
		"plugin_slug":       plugin.Slug,
		"action_hook":       hook,
		"vm_ip":             vmIP,
		"execution_time_ms": result.ExecutionTime.Milliseconds(),
		"used_snapshot":     cms.vmManager.HasSnapshot(plugin.Slug),
	}).Info("Action executed successfully")

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

	logger.WithFields(logrus.Fields{"path": jsonPath, "data": string(data)}).Debug("Reading plugin.json content")

	var metadata struct {
		Slug        string                  `json:"slug"`
		Name        string                  `json:"name"`
		Description string                  `json:"description"`
		Version     string                  `json:"version"`
		Author      string                  `json:"author"`
		Runtime     string                  `json:"runtime"`
		Actions     map[string]PluginAction `json:"actions"`
	}

	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse plugin.json: %v", err)
	}

	logger.WithFields(logrus.Fields{
		"slug":          metadata.Slug,
		"name":          metadata.Name,
		"runtime":       metadata.Runtime,
		"runtime_empty": metadata.Runtime == "",
	}).Debug("Parsed plugin metadata")

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

	plugin := &Plugin{
		Slug:        metadata.Slug,
		Name:        metadata.Name,
		Description: metadata.Description,
		Version:     metadata.Version,
		Author:      metadata.Author,
		Runtime:     metadata.Runtime,
		Actions:     metadata.Actions,
	}

	logger.WithFields(logrus.Fields{
		"plugin_slug":          plugin.Slug,
		"plugin_runtime":       plugin.Runtime,
		"plugin_runtime_empty": plugin.Runtime == "",
	}).Debug("Created plugin object")

	return plugin, nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = destFile.ReadFrom(sourceFile)
	return err
}

// verifyPluginHealth starts a temporary VM and checks the plugin's health endpoint
func (cms *CMS) verifyPluginHealth(plugin *Plugin) error {
	logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug}).Debug("Starting health check VM")

	// Generate temporary instance ID for health check
	healthCheckID := "health-" + plugin.Slug

	// Start temporary VM for health check
	err := cms.vmManager.StartVM(healthCheckID, plugin)
	if err != nil {
		return fmt.Errorf("failed to start health check VM: %v", err)
	}

	// Clean up VM after health check
	defer func() {
		if stopErr := cms.vmManager.StopVM(healthCheckID); stopErr != nil {
			logger.WithFields(logrus.Fields{"instance_id": healthCheckID, "error": stopErr}).Error("Failed to stop health check VM")
		}
	}()

	// Get VM IP
	vmIP, exists := cms.vmManager.getVMIP(healthCheckID)
	if !exists {
		return fmt.Errorf("VM IP not found for health check instance %s", healthCheckID)
	}

	if err := cms.healthCheckWithRetries(vmIP, plugin.Slug, 15, 1*time.Second); err != nil {
		logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug, "attempts": 15, "error": err}).Error("VM health check failed during activation after retries")
		return fmt.Errorf("plugin failed health check after %d attempts", 15)
	}

	logger.WithFields(logrus.Fields{"plugin_slug": plugin.Slug}).Info("Plugin health check passed")
	return nil
}

// generateID generates a unique ID
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

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

	// Create multi-writer for both file and console
	multiWriter := io.MultiWriter(os.Stdout, file)

	// Create JSON handler for structured logging
	handler := logrus.New()
	handler.SetFormatter(&logrus.JSONFormatter{})
	handler.SetOutput(multiWriter)
	handler.SetLevel(logrus.DebugLevel)

	// Set as default logger
	logger = handler

	// Initialize HTTP client with optimized settings
	httpClient = &http.Client{
		Timeout: DefaultHTTPTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	logger.WithFields(logrus.Fields{
		"log_file":     logFile,
		"level":        "debug",
		"http_timeout": DefaultHTTPTimeout,
		"timestamp":    time.Now().Format(time.RFC3339),
	}).Info("Logger and HTTP client initialized")

	return nil
}

// makeHTTPRequestWithTimeout makes an HTTP request with configurable timeout
func (cms *CMS) makeHTTPRequestWithTimeout(method, url string, body interface{}, timeout time.Duration) (map[string]interface{}, error) {
	client := &http.Client{Timeout: timeout}

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

// responseWriter wrapper to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func main() {
	// Initialize structured logging
	if err := setupLogger(); err != nil {
		log.Fatal("Failed to setup logger:", err)
	}

	logger.WithFields(logrus.Fields{
		"version":   "1.0.0",
		"timestamp": time.Now().Format(time.RFC3339),
	}).Info("Starting CMS application")

	cms := NewCMS()

	port := os.Getenv("CMS_PORT")
	if port == "" {
		port = DefaultPort
	}

	logger.WithFields(logrus.Fields{
		"port":             port,
		"firecracker_path": os.Getenv("FIRECRACKER_PATH"),
		"kernel_path":      os.Getenv("KERNEL_PATH"),
	}).Info("CMS configuration")

	if err := cms.Start(port); err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to start CMS")
		log.Fatal(err)
	}
}
