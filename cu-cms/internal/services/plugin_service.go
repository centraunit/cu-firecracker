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
	config    *config.Config
	logger    *logger.Logger
	plugins   map[string]*models.Plugin
	mutex     sync.RWMutex
	vmService *VMService
}

// NewPluginService creates a new plugin service
func NewPluginService(cfg *config.Config, log *logger.Logger, vmService *VMService) *PluginService {
	service := &PluginService{
		config:    cfg,
		logger:    log,
		plugins:   make(map[string]*models.Plugin),
		vmService: vmService,
	}

	// Load existing plugins from disk
	service.loadPlugins()

	// Restore active plugins after startup
	service.restoreActivePlugins()

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

	// Validate plugin metadata
	if metadata.Name == "" {
		return nil, fmt.Errorf("plugin must provide a name in plugin.json")
	}

	if metadata.Version == "" {
		return nil, fmt.Errorf("plugin must provide a version in plugin.json")
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
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": metadata.Slug,
			"old_version": existingPlugin.Version,
			"new_version": metadata.Version,
			"old_status":  existingPlugin.Status,
		}).Info("Updating existing plugin")

		// Handle cleanup of existing plugin resources if it's active or has resources
		if existingPlugin.Status == "active" || existingPlugin.AssignedIP != "" || existingPlugin.TapDevice != "" {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": metadata.Slug,
				"status":      existingPlugin.Status,
				"has_ip":      existingPlugin.AssignedIP != "",
				"has_tap":     existingPlugin.TapDevice != "",
			}).Info("Cleaning up existing plugin resources before update")

			// Remove from prewarm pool if exists
			ps.vmService.RemoveFromPrewarmPool(metadata.Slug)

			// Stop any running VM instance
			instanceID := metadata.Slug
			if err := ps.vmService.StopVM(instanceID); err != nil {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": metadata.Slug,
					"error":       err,
				}).Warn("Failed to stop existing VM during update")
				// Continue with update even if cleanup fails
			}

			// Delete existing snapshot to force fresh snapshot creation
			if err := ps.vmService.DeleteSnapshot(metadata.Slug); err != nil {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": metadata.Slug,
					"error":       err,
				}).Warn("Failed to delete existing snapshot during update")
				// Continue with update even if snapshot deletion fails
			}

			ps.logger.WithFields(logger.Fields{
				"plugin_slug": metadata.Slug,
			}).Info("Successfully cleaned up existing plugin resources")
		}

		// Update existing plugin metadata
		existingPlugin.Name = metadata.Name
		existingPlugin.Description = metadata.Description
		existingPlugin.Version = metadata.Version
		existingPlugin.Author = metadata.Author
		existingPlugin.Runtime = metadata.Runtime
		existingPlugin.RootfsPath = rootfsPath
		existingPlugin.UpdatedAt = time.Now()
		existingPlugin.Status = "ready" // Will be updated to "installed" after validation
		existingPlugin.Actions = metadata.Actions
		existingPlugin.Health = models.PluginHealth{Status: "unknown"}
		// Preserve existing network configuration for now, will be updated during validation
		// Note: We'll validate and potentially update network config during the health check phase

		// Save plugins registry
		if err := ps.savePluginsUnsafe(); err != nil {
			return nil, fmt.Errorf("failed to save plugins: %v", err)
		}

		// Start VM for health check and installation validation (for updates)
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": existingPlugin.Slug,
		}).Info("Starting VM for plugin update validation")

		// Use plugin slug as instance ID for consistency
		instanceID := existingPlugin.Slug

		// Start VM for health check
		if err := ps.vmService.StartVM(instanceID, existingPlugin); err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": existingPlugin.Slug,
				"error":       err,
			}).Error("Failed to start VM for plugin update validation")
			return nil, fmt.Errorf("failed to start VM for update validation: %v", err)
		}

		// Get VM IP from static networking
		vmIP, exists := ps.vmService.GetVMIP(instanceID)
		if !exists {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": existingPlugin.Slug,
			}).Error("Failed to get VM IP after start")
			// Clean up VM on failure
			if stopErr := ps.vmService.StopVM(instanceID); stopErr != nil {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": existingPlugin.Slug,
					"error":       stopErr,
				}).Error("Failed to stop VM after IP retrieval failure")
			}
			return nil, fmt.Errorf("failed to get VM IP for update validation")
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": existingPlugin.Slug,
			"vm_ip":       vmIP,
		}).Info("VM started successfully for update validation")

		// Allow extra time for VM boot and application initialization
		time.Sleep(3 * time.Second)

		// Perform health validation using centralized method
		if err := ps.validatePluginHealth(existingPlugin, instanceID, vmIP, "plugin_update"); err != nil {
			return nil, err
		}

		// Update plugin with assigned IP and TAP device
		// For updates, try to preserve existing network configuration if available
		if existingPlugin.AssignedIP == "" || existingPlugin.TapDevice == "" {
			// No existing network config, use new assignment
			existingPlugin.AssignedIP = vmIP
			existingPlugin.TapDevice = ps.vmService.GetTapNameForPlugin(existingPlugin.Slug)
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": existingPlugin.Slug,
				"assigned_ip": existingPlugin.AssignedIP,
				"tap_device":  existingPlugin.TapDevice,
			}).Info("Assigned new network configuration for plugin update")
		} else {
			// Preserve existing network configuration
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": existingPlugin.Slug,
				"assigned_ip": existingPlugin.AssignedIP,
				"tap_device":  existingPlugin.TapDevice,
			}).Info("Preserved existing network configuration for plugin update")
		}
		existingPlugin.Status = "installed"
		existingPlugin.UpdatedAt = time.Now()

		// Save updated plugin state
		if err := ps.savePluginsUnsafe(); err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": existingPlugin.Slug,
				"error":       err,
			}).Error("Failed to save plugin state after successful update")
			// Clean up VM on save failure
			ps.cleanupPluginVM(existingPlugin.Slug, instanceID, "plugin_update_save_failure")
			return nil, fmt.Errorf("failed to save plugin state: %v", err)
		}

		// Clean up VM and network - no prewarm during update, clean for next step
		ps.cleanupPluginVM(existingPlugin.Slug, instanceID, "plugin_update_success")

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": existingPlugin.Slug,
			"version":     metadata.Version,
			"assigned_ip": existingPlugin.AssignedIP,
			"tap_device":  existingPlugin.TapDevice,
			"status":      existingPlugin.Status,
		}).Info("Plugin updated and installed successfully")

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

	// Start VM for health check and installation validation
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
	}).Info("Starting VM for plugin installation validation")

	// Use plugin slug as instance ID for consistency
	instanceID := plugin.Slug

	// Start VM for health check
	if err := ps.vmService.StartVM(instanceID, plugin); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"error":       err,
		}).Error("Failed to start VM for plugin installation validation")
		return nil, fmt.Errorf("failed to start VM for installation validation: %v", err)
	}

	// Get VM IP from static networking
	vmIP, exists := ps.vmService.GetVMIP(instanceID)
	if !exists {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
		}).Error("Failed to get VM IP after start")
		// Clean up VM on failure
		if stopErr := ps.vmService.StopVM(instanceID); stopErr != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       stopErr,
			}).Error("Failed to stop VM after IP retrieval failure")
		}
		return nil, fmt.Errorf("failed to get VM IP for installation validation")
	}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"vm_ip":       vmIP,
	}).Info("VM started successfully for installation validation")

	// Allow extra time for VM boot and application initialization
	time.Sleep(3 * time.Second)

	// Perform health validation using centralized method
	if err := ps.validatePluginHealth(plugin, instanceID, vmIP, "plugin_upload"); err != nil {
		return nil, err
	}

	// Update plugin with assigned IP and TAP device
	plugin.AssignedIP = vmIP
	plugin.TapDevice = ps.vmService.GetTapNameForPlugin(plugin.Slug)
	plugin.Status = "installed"
	plugin.UpdatedAt = time.Now()

	// Save updated plugin state
	if err := ps.savePluginsUnsafe(); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"error":       err,
		}).Error("Failed to save plugin state after successful installation")
		// Clean up VM on save failure
		ps.cleanupPluginVM(plugin.Slug, instanceID, "plugin_upload_save_failure")
		return nil, fmt.Errorf("failed to save plugin state: %v", err)
	}

	// Clean up VM and network - no prewarm during upload, clean for next step
	ps.cleanupPluginVM(plugin.Slug, instanceID, "plugin_upload_success")

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"name":        metadata.Name,
		"version":     metadata.Version,
		"assigned_ip": plugin.AssignedIP,
		"tap_device":  plugin.TapDevice,
		"status":      plugin.Status,
	}).Info("Plugin uploaded and installed successfully")

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
func (ps *PluginService) ActivatePlugin(slug string) (*models.Plugin, error) {
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
	if ps.vmService.HasSnapshot(slug) {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
		}).Info("Plugin has existing snapshot, marking as active")

		// With static networking, ensure TAP interface exists
		// IP is already assigned and persisted
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
		}).Info("Static networking will handle network configuration")

		plugin.Status = "active"
		plugin.UpdatedAt = time.Now()

		if err := ps.savePluginsUnsafe(); err != nil {
			return nil, fmt.Errorf("failed to save plugin state: %v", err)
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
		}).Info("Plugin activated with existing snapshot")
		return plugin, nil
	}

	// Create temporary VM to warm up and take snapshot
	instanceID := slug // Use plugin slug as instance ID for consistency
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
		"instance_id": instanceID,
	}).Info("Creating VM for snapshot generation")

	if err := ps.vmService.StartVM(instanceID, plugin); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to start VM for snapshot")
		return nil, fmt.Errorf("failed to start VM: %v", err)
	}

	// Get VM IP from static networking
	vmIP, exists := ps.vmService.GetVMIP(instanceID)
	if !exists {
		return nil, fmt.Errorf("failed to get VM IP after start")
	}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
		"vm_ip":       vmIP,
	}).Info("VM started successfully with static networking")

	// Allow extra time for VM boot and Python app initialization
	time.Sleep(3 * time.Second)

	// Perform health validation using centralized method
	if err := ps.validatePluginHealth(plugin, instanceID, vmIP, "plugin_activation"); err != nil {
		return nil, err
	}

	// Create snapshot for fast future execution (use full snapshot for first time)
	snapshotPath := ps.vmService.GetSnapshotPath(slug)
	if err := ps.vmService.CreateSnapshot(instanceID, snapshotPath, false); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Error("Failed to create snapshot")
		return nil, fmt.Errorf("failed to create snapshot: %v", err)
	}

	// Pause the VM and add it to pre-warm pool for instant execution
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
		"instance_id": instanceID,
		"vm_ip":       vmIP,
	}).Info("Pausing VM and adding to pre-warm pool")

	// Pause the VM (keep it in memory for instant resume)
	if err := ps.vmService.PauseVM(instanceID); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Warn("Failed to pause VM, will stop it instead")
		// Fallback: stop the VM if pause fails
		if stopErr := ps.vmService.StopVM(instanceID); stopErr != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": slug,
				"error":       stopErr,
			}).Error("Failed to stop VM after pause failure")
		}
	} else {
		// VM is already in prewarm pool from StartVM
		// No need to manually add it

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"instance_id": instanceID,
			"vm_ip":       vmIP,
		}).Info("VM paused and added to pre-warm pool for instant execution")
	}

	// Persist the assigned IP and TAP device for this plugin
	plugin.AssignedIP = vmIP
	plugin.TapDevice = ps.vmService.GetTapNameForPlugin(plugin.Slug)

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
	}).Info("Plugin activated successfully with snapshot and persistent networking")

	return plugin, nil
}

// DeactivatePlugin deactivates a plugin and cleans up network resources
func (ps *PluginService) DeactivatePlugin(slug string) (*models.Plugin, error) {
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

	// Remove from prewarm pool
	ps.vmService.RemoveFromPrewarmPool(slug)

	// Delete snapshot files
	if err := ps.vmService.DeleteSnapshot(slug); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": slug,
			"error":       err,
		}).Warn("Failed to delete snapshot during deactivation")
		// Continue with deactivation even if snapshot deletion fails
	}

	// CNI handles network cleanup automatically
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": slug,
	}).Info("CNI handles network cleanup automatically")

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

		// Try to get a pre-warmed instance from the pool
		prewarmInstance := ps.vmService.GetPrewarmInstance(plugin.Slug)

		var instanceID string
		var vmIP string

		if prewarmInstance != nil {
			// Use pre-warmed instance for ultra-fast execution
			instanceID = prewarmInstance.InstanceID
			vmIP = prewarmInstance.IP

			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"instance_id": instanceID,
				"action_hook": actionHook,
			}).Info("Using pre-warmed instance for ultra-fast execution")

			// Resume the paused VM for execution
			if err := ps.vmService.ResumeVM(instanceID); err != nil {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": plugin.Slug,
					"error":       err,
				}).Error("Failed to resume pre-warmed VM")

				results = append(results, map[string]interface{}{
					"plugin_slug":       plugin.Slug,
					"success":           false,
					"result":            map[string]interface{}{"error": fmt.Sprintf("Failed to resume VM: %v", err)},
					"execution_time_ms": int(time.Since(startTime).Milliseconds()),
				})
				continue
			}

			// Return VM to pool after execution
			defer func(pluginSlug string, instance *PrewarmInstance) {
				// Pause VM and return to pool
				if pauseErr := ps.vmService.PauseVM(instance.InstanceID); pauseErr != nil {
					ps.logger.WithFields(logger.Fields{
						"instance_id": instance.InstanceID,
						"error":       pauseErr,
					}).Error("Failed to pause VM for pool return")
				} else {
					ps.vmService.ReturnPrewarmInstance(pluginSlug, instance)
				}
			}(plugin.Slug, prewarmInstance)

		} else {
			// No pre-warmed instance available - this should not happen for active plugins
			// Active plugins should have pre-warmed instances created during CMS startup or plugin activation
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"action_hook": actionHook,
			}).Error("No pre-warmed instance available for active plugin - plugin may not be properly activated")

			results = append(results, map[string]interface{}{
				"plugin_slug":       plugin.Slug,
				"success":           false,
				"result":            map[string]interface{}{"error": "Plugin not ready - no pre-warmed instance available"},
				"execution_time_ms": int(time.Since(startTime).Milliseconds()),
			})
			continue
		}

		// Brief wait for VM to be ready
		time.Sleep(10 * time.Millisecond)

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

		// HTTP REQUEST to the running plugin VM
		actionURL := fmt.Sprintf("http://%s:80%s", vmIP, targetAction.Endpoint)

		requestPayload := map[string]interface{}{
			"hook":    actionHook,
			"payload": payload,
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"action_url":  actionURL,
			"method":      targetAction.Method,
		}).Info("Making HTTP request to running plugin VM")

		response, err := ps.makeHTTPRequest(targetAction.Method, actionURL, requestPayload)
		if err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"action_url":  actionURL,
				"error":       err,
			}).Error("HTTP request to plugin failed")

			results = append(results, map[string]interface{}{
				"plugin_slug":       plugin.Slug,
				"success":           false,
				"result":            map[string]interface{}{"error": fmt.Sprintf("HTTP request failed: %v", err)},
				"execution_time_ms": int(time.Since(startTime).Milliseconds()),
			})
			continue
		}

		// SUCCESS: Actual response from plugin
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
		}).Info("Action executed successfully")
	}

	return map[string]interface{}{
		"action_hook":      actionHook,
		"executed_plugins": len(results),
		"results":          results,
		"timestamp":        time.Now(),
	}, nil
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

// validatePluginHealth performs comprehensive plugin health validation
// This centralizes the health check logic used across different operations
func (ps *PluginService) validatePluginHealth(plugin *models.Plugin, instanceID, vmIP string, context string) error {
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"context":     context,
		"vm_ip":       vmIP,
	}).Info("Starting plugin health validation")

	// VM is already in prewarm pool from StartVM
	// No need to manually add it

	// Perform health check
	if err := ps.healthCheckWithRetries(vmIP, plugin.Slug, 30, 500*time.Millisecond); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"context":     context,
			"vm_ip":       vmIP,
			"error":       err,
		}).Error("Plugin health validation failed")

		// Clean up VM and remove from prewarm pool
		ps.vmService.RemoveFromPrewarmPool(plugin.Slug)
		if stopErr := ps.vmService.StopVM(instanceID); stopErr != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       stopErr,
			}).Error("Failed to stop VM after health validation failure")
		}

		// Mark plugin as failed
		plugin.Status = "failed"
		plugin.Health = models.PluginHealth{Status: "unhealthy", Message: err.Error()}
		if saveErr := ps.savePluginsUnsafe(); saveErr != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       saveErr,
			}).Error("Failed to save plugin failed state")
		}

		return fmt.Errorf("plugin failed health validation: %v", err)
	}

	// Health check passed - mark plugin as healthy
	plugin.Health = models.PluginHealth{Status: "healthy", Message: "Plugin validated successfully"}

	ps.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"context":     context,
		"vm_ip":       vmIP,
	}).Info("Plugin health validation completed successfully")

	return nil
}

// cleanupPluginVM cleans up VM and network resources after plugin operations
func (ps *PluginService) cleanupPluginVM(pluginSlug, instanceID string, context string) {
	ps.logger.WithFields(logger.Fields{
		"plugin_slug": pluginSlug,
		"context":     context,
	}).Info("Cleaning up VM and network resources")

	// Remove from prewarm pool
	ps.vmService.RemoveFromPrewarmPool(pluginSlug)

	// Stop VM and clean up network resources
	if err := ps.vmService.StopVM(instanceID); err != nil {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
			"context":     context,
			"error":       err,
		}).Error("Failed to stop VM during cleanup")
	} else {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
			"context":     context,
		}).Info("VM and network cleaned up successfully")
	}
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

// restoreActivePlugins restores active plugins after CMS startup
func (ps *PluginService) restoreActivePlugins() {
	ps.logger.Info("Restoring active plugins after startup")

	ps.mutex.RLock()
	activePlugins := make([]*models.Plugin, 0)
	for _, plugin := range ps.plugins {
		if plugin.Status == "active" {
			activePlugins = append(activePlugins, plugin)
		}
	}
	ps.mutex.RUnlock()

	if len(activePlugins) == 0 {
		ps.logger.Info("No active plugins to restore")
		return
	}

	ps.logger.WithFields(logger.Fields{
		"active_count": len(activePlugins),
	}).Info("Found active plugins to restore")

	// Restore each active plugin
	for _, plugin := range activePlugins {
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"assigned_ip": plugin.AssignedIP,
			"tap_device":  plugin.TapDevice,
		}).Info("Restoring active plugin")

		// Always use plugin slug as instance ID for consistency
		instanceID := plugin.Slug

		// Always start fresh VMs for active plugin restoration
		// This ensures clean state and proper network initialization
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
		}).Info("Starting fresh VM for active plugin restoration")

		if err := ps.vmService.StartVM(instanceID, plugin); err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       err,
			}).Error("Failed to start VM for active plugin restoration")
			continue
		}

		// Get VM IP
		vmIP, exists := ps.vmService.GetVMIP(instanceID)
		if !exists {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"instance_id": instanceID,
			}).Error("Failed to get VM IP for active plugin restoration")
			continue
		}

		// Perform health check to ensure VM is working properly
		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"vm_ip":       vmIP,
		}).Info("Performing health check for active plugin restoration")

		if err := ps.healthCheckWithRetries(vmIP, plugin.Slug, 15, 1*time.Second); err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"vm_ip":       vmIP,
				"error":       err,
			}).Error("Health check failed for active plugin restoration")
			// Mark plugin as unhealthy but continue with restoration
			plugin.Health = models.PluginHealth{Status: "unhealthy", Message: err.Error()}
		} else {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"vm_ip":       vmIP,
			}).Info("Health check passed for active plugin restoration")
			// Mark plugin as healthy
			plugin.Health = models.PluginHealth{Status: "healthy", Message: "Plugin restored successfully"}

			// Create fresh snapshot for this plugin
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
			}).Info("Creating fresh snapshot for active plugin")

			snapshotPath := ps.vmService.GetSnapshotPath(plugin.Slug)
			if err := ps.vmService.CreateSnapshot(instanceID, snapshotPath, false); err != nil {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": plugin.Slug,
					"error":       err,
				}).Error("Failed to create snapshot for active plugin restoration")
				// Continue even if snapshot creation fails
			} else {
				ps.logger.WithFields(logger.Fields{
					"plugin_slug": plugin.Slug,
				}).Info("Successfully created fresh snapshot for active plugin")
			}
		}

		// Pause the VM for pre-warming
		if err := ps.vmService.PauseVM(instanceID); err != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       err,
			}).Error("Failed to pause VM for active plugin restoration")
			continue
		}

		// Save plugin health status and network configuration
		if saveErr := ps.savePluginsUnsafe(); saveErr != nil {
			ps.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"error":       saveErr,
			}).Error("Failed to save plugin health status during startup")
		}

		ps.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"instance_id": instanceID,
			"vm_ip":       vmIP,
		}).Info("Successfully restored active plugin")
	}

	ps.logger.Info("Active plugin restoration completed")
}
