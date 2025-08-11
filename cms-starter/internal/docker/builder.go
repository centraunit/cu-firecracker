/*
 * Firecracker CMS - Docker Image Builder
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package docker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
)

// Builder handles Docker image building operations
type Builder struct {
	logger *logger.Logger
}

// NewBuilder creates a new Docker builder
func NewBuilder() *Builder {
	return &Builder{
		logger: logger.GetDefault(),
	}
}

// BuildPluginImage builds a Docker image for a plugin
func (b *Builder) BuildPluginImage(pluginDir, imageName string) error {
	b.logger.WithFields(logger.Fields{
		"plugin_dir": pluginDir,
		"image":      imageName,
	}).Info("Building plugin Docker image")

	// Validate plugin directory exists
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return errors.NewFileSystemError("build_plugin_image",
			fmt.Sprintf("plugin directory does not exist: %s", pluginDir))
	}

	// Check for Dockerfile
	dockerfilePath := filepath.Join(pluginDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return errors.NewFileSystemError("build_plugin_image",
			fmt.Sprintf("Dockerfile not found in plugin directory: %s", pluginDir))
	}

	// Build the Docker image
	cmd := exec.Command("docker", "build", "-t", imageName, pluginDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return errors.WrapDockerError(err, "build_plugin_image",
			fmt.Sprintf("failed to build Docker image %s", imageName))
	}

	b.logger.WithFields(logger.Fields{
		"image": imageName,
	}).Info("Plugin Docker image built successfully")

	return nil
}

// RemoveImage removes a Docker image
func (b *Builder) RemoveImage(imageName string) error {
	b.logger.WithFields(logger.Fields{
		"image": imageName,
	}).Debug("Removing Docker image")

	cmd := exec.Command("docker", "rmi", imageName)
	if err := cmd.Run(); err != nil {
		// Don't treat image removal failures as critical errors
		b.logger.WithFields(logger.Fields{
			"image": imageName,
			"error": err,
		}).Warn("Failed to remove Docker image")
	}

	return nil
}

// ImageExists checks if a Docker image exists
func (b *Builder) ImageExists(imageName string) bool {
	cmd := exec.Command("docker", "image", "inspect", imageName)
	err := cmd.Run()
	return err == nil
}

// GetImageSize returns the size of a Docker image in bytes
func (b *Builder) GetImageSize(imageName string) (int64, error) {
	// This would require parsing docker inspect output
	// For now, return 0 as a placeholder
	return 0, nil
}
