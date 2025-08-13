/*
 * Firecracker CMS - Plugin Service
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package services

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/config"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/plugin"
)

// PluginService handles plugin operations
type PluginService struct {
	config    *config.Config
	validator plugin.Validator
	manager   plugin.Manager
	builder   plugin.Builder
	logger    *logger.Logger
}

// NewPluginService creates a new plugin service
func NewPluginService(cfg *config.Config) *PluginService {
	validator := plugin.NewValidator(cfg.MinPluginSize, cfg.MaxPluginSize)
	manager := plugin.NewManager(validator)
	builder := plugin.NewBuilder(validator, manager)

	return &PluginService{
		config:    cfg,
		validator: validator,
		manager:   manager,
		builder:   builder,
		logger:    logger.GetDefault(),
	}
}

// BuildPlugin builds a plugin from the specified directory
func (s *PluginService) BuildPlugin(pluginDir string, sizeMB int) (*plugin.BuildResult, error) {
	s.logger.WithFields(logger.Fields{
		"plugin_dir": pluginDir,
		"size_mb":    sizeMB,
	}).Info("Building plugin")

	// Use default size if not specified
	if sizeMB == 0 {
		sizeMB = s.config.DefaultPluginSize
	}

	// Provide size recommendations
	if sizeMB == s.config.DefaultPluginSize {
		s.logger.WithFields(logger.Fields{
			"size_mb": sizeMB,
		}).Info("Using default filesystem size")
		s.logger.Info("If build fails due to space issues, try increasing --size to 400 or 500")
	}

	// Load plugin manifest to get name for build directory
	manifest, err := s.manager.LoadManifest(pluginDir)
	if err != nil {
		return nil, err
	}

	// Create build output directory
	buildDir := filepath.Join(pluginDir, "build")

	// Create build configuration
	buildConfig := &plugin.BuildConfig{
		PluginDir:    pluginDir,
		Size:         sizeMB,
		OutputDir:    buildDir,
		CleanupImage: true, // Clean up Docker images after build
	}

	// Build the plugin
	result, err := s.builder.Build(buildConfig)
	if err != nil {
		// Check if it's a space-related error and provide helpful guidance
		if s.isSpaceError(err) {
			return result, s.wrapSpaceError(err, sizeMB)
		}
		return result, err
	}

	s.logger.WithFields(logger.Fields{
		"plugin":     manifest.Name,
		"version":    manifest.Version,
		"zip_path":   result.ZipPath,
		"build_time": result.BuildTime,
	}).Info("Plugin built successfully")

	return result, nil
}

// ValidatePlugin validates a plugin directory and manifest
func (s *PluginService) ValidatePlugin(pluginDir string) error {
	s.logger.WithFields(logger.Fields{
		"plugin_dir": pluginDir,
	}).Debug("Validating plugin")

	// Validate directory structure
	if err := s.validator.ValidateDirectory(pluginDir); err != nil {
		return err
	}

	// Load and validate manifest
	_, err := s.manager.LoadManifest(pluginDir)
	if err != nil {
		return err
	}

	s.logger.WithFields(logger.Fields{
		"plugin_dir": pluginDir,
	}).Info("Plugin validation passed")

	return nil
}

// GetPluginInfo returns information about a plugin
func (s *PluginService) GetPluginInfo(pluginDir string) (*plugin.Manifest, error) {
	return s.manager.LoadManifest(pluginDir)
}

// isSpaceError checks if the error is related to insufficient space
func (s *PluginService) isSpaceError(err error) bool {
	errorMsg := err.Error()
	spaceIndicators := []string{
		"No space left on device",
		"Cannot mkdir",
		"Cannot create",
		"Cannot open",
		"Cannot hard link",
		"filesystem too small",
		"appears too small",
	}

	for _, indicator := range spaceIndicators {
		if strings.Contains(errorMsg, indicator) {
			return true
		}
	}

	return false
}

// wrapSpaceError wraps a space-related error with helpful guidance
func (s *PluginService) wrapSpaceError(err error, currentSize int) error {
	message := fmt.Sprintf("Plugin build failed due to insufficient space (%dMB). "+
		"💡 Solution: Increase filesystem size with --size flag. "+
		"Recommended sizes: --size 400, --size 500, or --size 800. "+
		"Original error: %v", currentSize, err)

	return errors.NewPluginError("plugin_build", message)
}
