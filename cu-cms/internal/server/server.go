/*
 * Firecracker CMS - HTTP Server
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/centraunit/cu-firecracker-cms/internal/config"
	"github.com/centraunit/cu-firecracker-cms/internal/logger"
	"github.com/centraunit/cu-firecracker-cms/internal/models"
	"github.com/centraunit/cu-firecracker-cms/internal/services"
)

// Server represents the HTTP server
type Server struct {
	config        *config.Config
	logger        *logger.Logger
	vmService     *services.VMService
	pluginService *services.PluginService
	server        *http.Server
}

// New creates a new server instance
func New(cfg *config.Config, log *logger.Logger, vmService *services.VMService, pluginService *services.PluginService) *Server {
	return &Server{
		config:        cfg,
		logger:        log,
		vmService:     vmService,
		pluginService: pluginService,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Add middleware
	handler := s.loggingMiddleware(s.recoveryMiddleware(s.corsMiddleware(mux)))

	// Plugin management endpoints
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePluginBySlug)

	// Action execution endpoint
	mux.HandleFunc("/api/execute", s.handleExecuteAction)

	// Health and metrics
	mux.HandleFunc("/health", s.handleHealthCheck)
	mux.HandleFunc("/metrics", s.handleMetrics)

	s.server = &http.Server{
		Addr:         ":" + s.config.Port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.logger.WithFields(logger.Fields{
		"port": s.config.Port,
	}).Info("Starting CMS server")

	return s.server.ListenAndServe()
}

// Stop gracefully stops the server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping server")
	return s.server.Shutdown(ctx)
}

// Middleware functions

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap ResponseWriter to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		s.logger.WithFields(logger.Fields{
			"method":      r.Method,
			"url":         r.URL.String(),
			"remote_addr": r.RemoteAddr,
			"user_agent":  r.UserAgent(),
			"status_code": wrapped.statusCode,
			"duration_ms": time.Since(start).Milliseconds(),
		}).Debug("HTTP request")
	})
}

func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.WithFields(logger.Fields{
					"error": err,
				}).Error("Panic recovered")
				s.sendErrorResponse(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Handler functions

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.handleListPlugins(w, r)
	case "POST":
		s.handleUploadPlugin(w, r)
	default:
		s.sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePluginBySlug(w http.ResponseWriter, r *http.Request) {
	// Extract slug from URL path /api/plugins/{slug}
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		s.sendErrorResponse(w, "Invalid URL format", http.StatusBadRequest)
		return
	}

	slug := pathParts[2]
	if slug == "" {
		s.sendErrorResponse(w, "Plugin slug required", http.StatusBadRequest)
		return
	}

	// Check for action in path (activate/deactivate)
	if len(pathParts) > 3 {
		action := pathParts[3]
		switch action {
		case "activate":
			if r.Method == "POST" {
				s.handleActivatePlugin(w, r, slug)
				return
			}
		case "deactivate":
			if r.Method == "POST" {
				s.handleDeactivatePlugin(w, r, slug)
				return
			}
		}
		s.sendErrorResponse(w, "Invalid action", http.StatusBadRequest)
		return
	}

	// Handle basic CRUD operations
	switch r.Method {
	case "GET":
		s.handleGetPlugin(w, r, slug)
	case "DELETE":
		s.handleDeletePlugin(w, r, slug)
	default:
		s.sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Handling list plugins request")

	plugins, err := s.pluginService.ListPlugins()
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to list plugins")
		s.sendErrorResponse(w, "Failed to list plugins", http.StatusInternalServerError)
		return
	}

	s.logger.WithFields(logger.Fields{
		"count": len(plugins),
	}).Info("Listed plugins")

	s.sendSuccessResponse(w, plugins, http.StatusOK)
}

func (s *Server) handleUploadPlugin(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Handling plugin upload request")

	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
		s.logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to parse multipart form")
		s.sendErrorResponse(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get uploaded ZIP file
	file, header, err := r.FormFile("plugin")
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to get uploaded file")
		s.sendErrorResponse(w, "Failed to get uploaded plugin ZIP file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Verify it's a ZIP file
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		s.logger.WithFields(logger.Fields{
			"filename": header.Filename,
		}).Error("Invalid file type")
		s.sendErrorResponse(w, "Plugin must be a ZIP file containing rootfs.ext4 and plugin.json", http.StatusBadRequest)
		return
	}

	s.logger.WithFields(logger.Fields{
		"filename": header.Filename,
		"size":     header.Size,
	}).Debug("Received plugin ZIP file")

	// Upload the plugin using the plugin service
	plugin, err := s.pluginService.UploadPlugin(file, header.Filename)
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to upload plugin")
		s.sendErrorResponse(w, fmt.Sprintf("Failed to upload plugin: %v", err), http.StatusBadRequest)
		return
	}

	s.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"name":        plugin.Name,
		"version":     plugin.Version,
	}).Info("Plugin uploaded successfully")

	s.sendSuccessResponse(w, plugin, http.StatusCreated)
}

func (s *Server) handleGetPlugin(w http.ResponseWriter, r *http.Request, slug string) {
	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Debug("Handling get plugin request")

	plugin, err := s.pluginService.GetPlugin(slug)
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Warn("Plugin not found")
		s.sendErrorResponse(w, "Plugin not found", http.StatusNotFound)
		return
	}

	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
		"name":        plugin.Name,
		"version":     plugin.Version,
	}).Debug("Retrieved plugin")

	s.sendSuccessResponse(w, plugin, http.StatusOK)
}

func (s *Server) handleDeletePlugin(w http.ResponseWriter, r *http.Request, slug string) {
	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Debug("Handling delete plugin request")

	err := s.pluginService.DeletePlugin(slug)
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to delete plugin")
		s.sendErrorResponse(w, "Failed to delete plugin", http.StatusInternalServerError)
		return
	}

	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Info("Plugin deleted successfully")

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleActivatePlugin(w http.ResponseWriter, r *http.Request, slug string) {
	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Debug("Handling activate plugin request")

	plugin, err := s.pluginService.ActivatePlugin(slug)
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to activate plugin")
		s.sendErrorResponse(w, fmt.Sprintf("Failed to activate plugin: %v", err), http.StatusInternalServerError)
		return
	}

	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Info("Plugin activated successfully")

	s.sendSuccessResponse(w, plugin, http.StatusOK)
}

func (s *Server) handleDeactivatePlugin(w http.ResponseWriter, r *http.Request, slug string) {
	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Debug("Handling deactivate plugin request")

	plugin, err := s.pluginService.DeactivatePlugin(slug)
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to deactivate plugin")
		s.sendErrorResponse(w, fmt.Sprintf("Failed to deactivate plugin: %v", err), http.StatusInternalServerError)
		return
	}

	s.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Info("Plugin deactivated successfully")

	s.sendSuccessResponse(w, plugin, http.StatusOK)
}

func (s *Server) handleExecuteAction(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Handling execute action request")

	if r.Method != "POST" {
		s.sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var requestBody struct {
		Action  string                 `json:"action"`
		Payload map[string]interface{} `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		s.logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to parse execute action request body")
		s.sendErrorResponse(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if requestBody.Action == "" {
		s.sendErrorResponse(w, "Action is required", http.StatusBadRequest)
		return
	}

	s.logger.WithFields(logger.Fields{
		"action": requestBody.Action,
	}).Debug("Executing action")

	// Execute action using plugin service
	results, err := s.pluginService.ExecuteAction(requestBody.Action, requestBody.Payload, s.vmService)
	if err != nil {
		s.logger.WithFields(logger.Fields{
			"action": requestBody.Action,
			"error":  err,
		}).Error("Failed to execute action")
		s.sendErrorResponse(w, fmt.Sprintf("Failed to execute action: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"action_hook":      requestBody.Action,
		"executed_plugins": len(results),
		"results":          results,
		"timestamp":        time.Now().Format(time.RFC3339),
	}

	s.sendSuccessResponse(w, response, http.StatusOK)
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	plugins, _ := s.pluginService.ListPlugins()

	totalPlugins := len(plugins)
	activePlugins := 0
	for _, plugin := range plugins {
		if plugin.Status == "active" {
			activePlugins++
		}
	}

	health := map[string]interface{}{
		"status":         "healthy",
		"total_plugins":  totalPlugins,
		"active_plugins": activePlugins,
		"vm_instances":   len(s.vmService.ListVMs()),
	}

	s.sendSuccessResponse(w, health, http.StatusOK)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	plugins, _ := s.pluginService.ListPlugins()
	vms := s.vmService.ListVMs()

	metrics := map[string]interface{}{
		"plugins_total":   len(plugins),
		"instances_total": len(vms),
	}

	s.sendSuccessResponse(w, metrics, http.StatusOK)
}

// Response helper functions

func (s *Server) sendSuccessResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	response := models.HTTPResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) sendErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	response := models.HTTPResponse{
		Success:   false,
		Error:     message,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
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
