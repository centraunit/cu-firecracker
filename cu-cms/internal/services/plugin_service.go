/*
 * Firecracker CMS - Plugin Service
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package services

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/centraunit/cu-firecracker-cms/internal/config"
	"github.com/centraunit/cu-firecracker-cms/internal/logger"
	"github.com/centraunit/cu-firecracker-cms/internal/models"
)

// PluginService handles plugin management operations
type PluginService struct {
	config  *config.Config
	logger  *logger.Logger
	plugins map[string]*models.Plugin
	mutex   sync.RWMutex
}

// NewPluginService creates a new plugin service
func NewPluginService(cfg *config.Config, log *logger.Logger) *PluginService {
	service := &PluginService{
		config:  cfg,
		logger:  log,
		plugins: make(map[string]*models.Plugin),
	}

	// Load existing plugins from disk
	service.loadPlugins()

	return service
}

// ListPlugins returns all registered plugins
func (ps *PluginService) ListPlugins() ([]*models.Plugin, error) {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()

	plugins := make([]*models.Plugin, 0, len(ps.plugins))
	for _, plugin := range ps.plugins {
		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

// GetPlugin returns a specific plugin by slug
func (ps *PluginService) GetPlugin(slug string) (*models.Plugin, error) {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()

	plugin, exists := ps.plugins[slug]
	if !exists {
		return nil, fmt.Errorf("plugin not found")
	}

	return plugin, nil
}

// UploadPlugin handles plugin upload and registration
func (ps *PluginService) UploadPlugin(file multipart.File, filename string) (*models.Plugin, error) {
	ps.logger.WithFields(logger.Fields{
		"filename": filename,
	}).Info("Starting plugin upload")

	// Create plugins directory if it doesn't exist
	pluginsDir := filepath.Join(ps.config.DataDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory: %v", err)
	}

	// Create temporary directory for extraction
	tempDir, err := os.MkdirTemp("", "cms-plugin-upload-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Save ZIP file temporarily
	zipPath := filepath.Join(tempDir, "plugin.zip")
	dst, err := os.Create(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create ZIP file: %v", err)
	}

	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		return nil, fmt.Errorf("failed to save ZIP file: %v", err)
	}
	dst.Close()

	// Extract ZIP file
	if err := ps.extractPluginZip(zipPath, tempDir); err != nil {
		return nil, fmt.Errorf("failed to extract ZIP: %v", err)
	}

	// Parse plugin.json to get metadata
	pluginJsonPath := filepath.Join(tempDir, "plugin.json")
	metadata, err := ps.parsePluginJson(pluginJsonPath)
	if err != nil {
		return nil, fmt.Errorf("invalid plugin.json: %v", err)
	}

	// Validate required fields
	if metadata.Slug == "" {
		return nil, fmt.Errorf("plugin must provide a unique slug in plugin.json")
	}

	// Move rootfs to final location using slug-based naming
	rootfsTempPath := filepath.Join(tempDir, "rootfs.ext4")
	rootfsPath := filepath.Join(pluginsDir, metadata.Slug+".ext4")

	// Remove existing plugin file if it exists
	os.Remove(rootfsPath)

	// Copy rootfs file
	if err := ps.copyFile(rootfsTempPath, rootfsPath); err != nil {
		return nil, fmt.Errorf("failed to install plugin rootfs: %v", err)
	}

	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	// Check if plugin already exists (update scenario)
	if existingPlugin, exists := ps.plugins[metadata.Slug]; exists {
		// Update existing plugin
		existingPlugin.Name = metadata.Name
		existingPlugin.Description = metadata.Description
		existingPlugin.Version = metadata.Version
		existingPlugin.Author = metadata.Author
		existingPlugin.Runtime = metadata.Runtime
		existingPlugin.RootfsPath = rootfsPath
		existingPlugin.UpdatedAt = time.Now()
		existingPlugin.Status = "ready"
		existingPlugin.Actions = metadata.Actions
		existingPlugin.Health = models.PluginHealth{Status: "unknown"}

		// Save plugins registry
		if err := ps.savePluginsUnsafe(); err != nil {
			return nil, fmt.Errorf("failed to save plugins: %v", err)
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": metadata.Slug,
			"version":     metadata.Version,
		}).Info("Plugin updated successfully")

		return existingPlugin, nil
	}

	// Create new plugin
	plugin := &models.Plugin{
		Slug:        metadata.Slug,
		Name:        metadata.Name,
		Description: metadata.Description,
		Version:     metadata.Version,
		Author:      metadata.Author,
		Runtime:     metadata.Runtime,
		RootfsPath:  rootfsPath,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Status:      "ready",
		Health:      models.PluginHealth{Status: "unknown"},
		Actions:     metadata.Actions,
		Priority:    0,
	}

	ps.plugins[metadata.Slug] = plugin

	// Save plugins registry
	if err := ps.savePluginsUnsafe(); err != nil {
		return nil, fmt.Errorf("failed to save plugins: %v", err)
	}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"name":        metadata.Name,
		"version":     metadata.Version,
	}).Info("Plugin uploaded successfully")

	return plugin, nil
}

// DeletePlugin deletes a plugin by slug
func (ps *PluginService) DeletePlugin(slug string) error {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	plugin, exists := ps.plugins[slug]
	if !exists {
		return fmt.Errorf("plugin not found")
	}

	// Remove rootfs file
	if err := os.Remove(plugin.RootfsPath); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to remove rootfs file")
	}

	delete(ps.plugins, slug)

	// Save plugins registry
	if err := ps.savePluginsUnsafe(); err != nil {
		return fmt.Errorf("failed to save plugins: %v", err)
	}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
		"name":        plugin.Name,
		"version":     plugin.Version,
	}).Info("Plugin deleted successfully")

	return nil
}

// ActivatePlugin activates a plugin and creates snapshot
func (ps *PluginService) ActivatePlugin(slug string, vmService *VMService) (*models.Plugin, error) {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	plugin, exists := ps.plugins[slug]
	if !exists {
		return nil, fmt.Errorf("plugin not found")
	}

	if plugin.Status == "active" {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
		}).Info("Plugin already active")
		return plugin, nil
	}

	// If snapshot already exists, just mark as active and ensure network config
	if vmService.HasSnapshot(slug) {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
		}).Info("Plugin has existing snapshot, marking as active")

		// Ensure network configuration is set
		if plugin.AssignedIP == "" || plugin.TapDevice == "" {
			// Allocate network configuration
			instanceID := fmt.Sprintf("%s-activate", slug)
			vmIP, err := vmService.allocateIP(instanceID) // This should use a temporary allocation method
			if err != nil {
				return nil, fmt.Errorf("failed to allocate IP: %v", err)
			}
			tapName := vmService.generatePluginTapName(slug)

			plugin.AssignedIP = vmIP
			plugin.TapDevice = tapName
		}

		plugin.Status = "active"
		plugin.UpdatedAt = time.Now()

		if err := ps.savePluginsUnsafe(); err != nil {
			return nil, fmt.Errorf("failed to save plugin state: %v", err)
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"assigned_ip": plugin.AssignedIP,
			"tap_device":  plugin.TapDevice,
		}).Info("Plugin activated with existing snapshot")
		return plugin, nil
	}

	// Create temporary VM to warm up and take snapshot
	instanceID := slug // Use plugin slug as instance ID for consistency
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
		"instance_id": instanceID,
	}).Info("Creating VM for snapshot generation")

	if err := vmService.StartVM(instanceID, plugin); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to start VM for snapshot")
		return nil, fmt.Errorf("failed to start VM: %v", err)
	}

	// Get VM IP and TAP device name from VM service
	vmIP, exists := vmService.GetVMIP(instanceID)
	if !exists {
		return nil, fmt.Errorf("failed to get VM IP after start")
	}

	// Only generate new TAP name if plugin doesn't have one
	var tapName string
	if plugin.TapDevice != "" {
		tapName = plugin.TapDevice
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"tap_device":  tapName,
		}).Info("Reusing existing TAP device for plugin")
	} else {
		tapName = vmService.generatePluginTapName(slug)
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"tap_device":  tapName,
		}).Info("Generated new TAP device for plugin")
	}

	// Store network configuration in plugin
	plugin.AssignedIP = vmIP
	plugin.TapDevice = tapName
	ps.plugins[slug] = plugin

	// Allow extra time for VM boot and Python app initialization
	time.Sleep(3 * time.Second)

	// Use health check with retries instead of fixed wait time (much more efficient)
	if err := ps.healthCheckWithRetries(vmIP, slug, 30, 500*time.Millisecond); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"attempts":    30,
			"error":       err,
		}).Error("VM health check failed during activation")
		return nil, fmt.Errorf("plugin failed health check: %v", err)
	}

	// Create snapshot for fast future execution (use full snapshot for first time)
	snapshotPath := vmService.GetSnapshotPath(slug)
	if err := vmService.CreateSnapshot(instanceID, snapshotPath, false); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to create snapshot")
		return nil, fmt.Errorf("failed to create snapshot: %v", err)
	}

	plugin.Status = "active"
	plugin.UpdatedAt = time.Now()

	if err := ps.savePluginsUnsafe(); err != nil {
		return nil, fmt.Errorf("failed to save plugin state: %v", err)
	}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug":   slug,
		"snapshot_path": snapshotPath,
		"assigned_ip":   plugin.AssignedIP,
		"tap_device":    plugin.TapDevice,
	}).Info("Plugin activated successfully with snapshot")

	return plugin, nil
}

// DeactivatePlugin deactivates a plugin and cleans up network resources
func (ps *PluginService) DeactivatePlugin(slug string, vmService *VMService) (*models.Plugin, error) {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	plugin, exists := ps.plugins[slug]
	if !exists {
		return nil, fmt.Errorf("plugin not found")
	}

	if plugin.Status == "inactive" {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
		}).Info("Plugin already inactive")
		return plugin, nil
	}

	// Delete snapshot files
	if err := vmService.DeleteSnapshot(slug); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Warn("Failed to delete snapshot during deactivation")
		// Continue with deactivation even if snapshot deletion fails
	}

	// Clean up network resources (TAP device and IP)
	if err := vmService.CleanupPluginNetwork(plugin); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to cleanup network during deactivation")
		// Continue with deactivation even if network cleanup fails
	}

	// Clear network configuration from plugin registry
	plugin.AssignedIP = ""
	plugin.TapDevice = ""
	plugin.Status = "inactive"
	plugin.UpdatedAt = time.Now()

	if err := ps.savePluginsUnsafe(); err != nil {
		return nil, fmt.Errorf("failed to save plugin state: %v", err)
	}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Info("Plugin deactivated successfully with network cleanup")

	return plugin, nil
}

// ExecuteAction executes an action on a plugin using external VM service
func (ps *PluginService) ExecuteAction(actionHook string, payload map[string]interface{}, vmService *VMService) (map[string]interface{}, error) {
	ps.logger.WithFields(logger.Fields{
		"action_hook": actionHook,
	}).Info("Executing action")

	// Find plugins that handle this action
	var targetPlugins []*models.Plugin
	for _, plugin := range ps.plugins {
		if plugin.Status == "active" {
			for actionSlug, action := range plugin.Actions {
				for _, hook := range action.Hooks {
					if hook == actionHook {
						targetPlugins = append(targetPlugins, plugin)
						ps.logger.WithFields(logger.Fields{
							"plugin_slug": plugin.Slug,
							"action_slug": actionSlug,
						}).Debug("Found plugin for action")
						break
					}
				}
			}
		}
	}

	if len(targetPlugins) == 0 {
		return map[string]interface{}{
			"action_hook":      actionHook,
			"executed_plugins": 0,
			"results":          []interface{}{},
			"timestamp":        time.Now(),
		}, nil
	}

	// Sort plugins by priority (highest first)
	for i := 0; i < len(targetPlugins)-1; i++ {
		for j := i + 1; j < len(targetPlugins); j++ {
			if targetPlugins[i].Priority < targetPlugins[j].Priority {
				targetPlugins[i], targetPlugins[j] = targetPlugins[j], targetPlugins[i]
			}
		}
	}

	var results []map[string]interface{}

	for _, plugin := range targetPlugins {
		startTime := time.Now()

		// Create unique execution instance ID
		instanceID := fmt.Sprintf("%s-exec-%d", plugin.Slug, time.Now().UnixNano())

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"instance_id": instanceID,
			"action_hook": actionHook,
		}).Info("Starting VM for real action execution")

		// REAL EXECUTION: Resume VM from snapshot for ultra-fast execution
		if err := vmService.ResumeFromSnapshot(instanceID, plugin); err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       err,
			}).Error("Failed to resume VM from snapshot")

			results = append(results, map[string]interface{}{
				"plugin_slug":       plugin.Slug,
				"success":           false,
				"result":            map[string]interface{}{"error": fmt.Sprintf("Failed to resume VM: %v", err)},
				"execution_time_ms": int(time.Since(startTime).Milliseconds()),
			})
			continue
		}

		// Ensure VM cleanup
		defer func(instanceID string) {
			if stopErr := vmService.StopVM(instanceID); stopErr != nil {
				ps.logger.WithFields(logger.Fields{
					"instance_id": instanceID,
					"error":       stopErr,
				}).Error("Failed to stop execution VM")
			}
		}(instanceID)

		// Brief wait for snapshot resume to complete (much faster than cold boot)
		time.Sleep(100 * time.Millisecond)

		// Get VM IP for making REAL HTTP requests
		vmIP, exists := vmService.GetVMIP(instanceID)
		if !exists {
			results = append(results, map[string]interface{}{
				"plugin_slug":       plugin.Slug,
				"success":           false,
				"result":            map[string]interface{}{"error": "Failed to get VM IP"},
				"execution_time_ms": int(time.Since(startTime).Milliseconds()),
			})
			continue
		}

		// Find the appropriate action endpoint
		var targetAction *models.PluginAction
		for _, action := range plugin.Actions {
			for _, hook := range action.Hooks {
				if hook == actionHook {
					actionCopy := action
					targetAction = &actionCopy
					break
				}
			}
			if targetAction != nil {
				break
			}
		}

		if targetAction == nil {
			results = append(results, map[string]interface{}{
				"plugin_slug":       plugin.Slug,
				"success":           false,
				"result":            map[string]interface{}{"error": "Action not found in plugin"},
				"execution_time_ms": int(time.Since(startTime).Milliseconds()),
			})
			continue
		}

		// REAL HTTP REQUEST to the plugin VM
		actionURL := fmt.Sprintf("http://%s:80%s", vmIP, targetAction.Endpoint)

		requestPayload := map[string]interface{}{
			"hook":    actionHook,
			"payload": payload,
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"action_url":  actionURL,
			"method":      targetAction.Method,
		}).Info("Making REAL HTTP request to plugin")

		response, err := ps.makeHTTPRequest(targetAction.Method, actionURL, requestPayload)
		if err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"action_url":  actionURL,
				"error":       err,
			}).Error("REAL HTTP request to plugin failed")

			results = append(results, map[string]interface{}{
				"plugin_slug":       plugin.Slug,
				"success":           false,
				"result":            map[string]interface{}{"error": fmt.Sprintf("HTTP request failed: %v", err)},
				"execution_time_ms": int(time.Since(startTime).Milliseconds()),
			})
			continue
		}

		// REAL SUCCESS: Actual response from plugin
		results = append(results, map[string]interface{}{
			"plugin_slug":       plugin.Slug,
			"success":           true,
			"result":            response,
			"execution_time_ms": int(time.Since(startTime).Milliseconds()),
		})

		ps.logger.WithFields(logger.Fields{
			"plugin_slug":    plugin.Slug,
			"execution_time": time.Since(startTime).Milliseconds(),
			"action_hook":    actionHook,
		}).Info("REAL action executed successfully")
	}

	return map[string]interface{}{
		"action_hook":      actionHook,
		"executed_plugins": len(results),
		"results":          results,
		"timestamp":        time.Now(),
	}, nil
}

// Helper methods

func (ps *PluginService) executePluginAction(plugin *models.Plugin, action models.PluginAction, hook string, payload map[string]interface{}, vmService *VMService) models.ActionExecutionResult {
	start := time.Now()
	result := models.ActionExecutionResult{
		PluginSlug: plugin.Slug,
	}

	// TODO: Implement actual VM execution logic
	// For now, return a placeholder success result
	result.Success = true
	result.Result = map[string]interface{}{
		"message": "Action execution not yet fully implemented",
		"hook":    hook,
		"payload": payload,
	}
	result.ExecutionTime = time.Since(start)

	ps.logger.WithFields(logger.Fields{
		"plugin_slug":       plugin.Slug,
		"action_hook":       hook,
		"execution_time_ms": result.ExecutionTime.Milliseconds(),
	}).Info("Action executed (placeholder)")

	return result
}

func (ps *PluginService) extractPluginZip(zipPath, destDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open ZIP file: %v", err)
	}
	defer reader.Close()

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

func (ps *PluginService) parsePluginJson(jsonPath string) (*models.Plugin, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read plugin.json: %v", err)
	}

	var metadata struct {
		Slug        string                         `json:"slug"`
		Name        string                         `json:"name"`
		Description string                         `json:"description"`
		Version     string                         `json:"version"`
		Author      string                         `json:"author"`
		Runtime     string                         `json:"runtime"`
		Actions     map[string]models.PluginAction `json:"actions"`
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

	plugin := &models.Plugin{
		Slug:        metadata.Slug,
		Name:        metadata.Name,
		Description: metadata.Description,
		Version:     metadata.Version,
		Author:      metadata.Author,
		Runtime:     metadata.Runtime,
		Actions:     metadata.Actions,
	}

	return plugin, nil
}

func (ps *PluginService) copyFile(src, dst string) error {
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

func (ps *PluginService) savePluginsUnsafe() error {
	// Note: Caller must hold ps.mutex.Lock()
	pluginsDir := filepath.Join(ps.config.DataDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return err
	}

	pluginsFile := filepath.Join(pluginsDir, "plugins.json")
	data, err := json.MarshalIndent(ps.plugins, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(pluginsFile, data, 0644); err != nil {
		return err
	}

	ps.logger.WithFields(logger.Fields{
		"file":         pluginsFile,
		"plugin_count": len(ps.plugins),
	}).Info("Plugins saved to registry")

	return nil
}

func (ps *PluginService) loadPlugins() {
	pluginsFile := filepath.Join(ps.config.DataDir, "plugins", "plugins.json")

	ps.logger.WithFields(logger.Fields{
		"file": pluginsFile,
	}).Debug("Loading plugins from registry")

	data, err := os.ReadFile(pluginsFile)
	if err != nil {
		ps.logger.WithFields(logger.Fields{
			"file": pluginsFile,
		}).Info("No existing plugins registry found")
		return
	}

	var plugins map[string]*models.Plugin
	if err := json.Unmarshal(data, &plugins); err != nil {
		ps.logger.WithFields(logger.Fields{
			"file":  pluginsFile,
			"error": err,
		}).Error("Failed to parse plugins registry")
		return
	}

	ps.mutex.Lock()
	defer ps.mutex.Unlock()
	ps.plugins = plugins

	ps.logger.WithFields(logger.Fields{
		"file":  pluginsFile,
		"count": len(plugins),
	}).Info("Loaded plugins from registry")
}

// healthCheckWithRetries performs health check with retry logic
func (ps *PluginService) healthCheckWithRetries(vmIP, pluginSlug string, maxRetries int, retryDelay time.Duration) error {
	healthURL := fmt.Sprintf("http://%s:80/health", vmIP)

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		response, err := ps.makeHTTPRequest("GET", healthURL, nil)
		if err != nil {
			lastErr = err
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": pluginSlug,
				"attempt":     attempt,
				"max_retries": maxRetries,
				"error":       err,
			}).Debug("Health check failed, retrying")

			if attempt < maxRetries {
				time.Sleep(retryDelay)
				continue
			}
		} else {
			// Validate health response
			if status, ok := response["status"].(string); ok && status == "healthy" {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": pluginSlug,
					"attempt":     attempt,
				}).Info("Health check successful")
				return nil
			} else {
				lastErr = fmt.Errorf("unhealthy status response: %v", response)
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": pluginSlug,
					"attempt":     attempt,
					"response":    response,
				}).Debug("Health check returned unhealthy status, retrying")

				if attempt < maxRetries {
					time.Sleep(retryDelay)
					continue
				}
			}
		}
	}

	return fmt.Errorf("health check failed after %d attempts: %v", maxRetries, lastErr)
}

// makeHTTPRequest makes an HTTP request and returns the response as a map
func (ps *PluginService) makeHTTPRequest(method, url string, body interface{}) (map[string]interface{}, error) {
	client := &http.Client{Timeout: 10 * time.Second}

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
