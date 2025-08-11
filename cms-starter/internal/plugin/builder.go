/*
 * Firecracker CMS - Plugin Builder
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package plugin

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/docker"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
)

// DefaultBuilder implements the Builder interface
type DefaultBuilder struct {
	logger    *logger.Logger
	validator Validator
	manager   Manager
	builder   *docker.Builder
}

// NewBuilder creates a new plugin builder
func NewBuilder(validator Validator, manager Manager) *DefaultBuilder {
	return &DefaultBuilder{
		logger:    logger.GetDefault(),
		validator: validator,
		manager:   manager,
		builder:   docker.NewBuilder(),
	}
}

// Build builds a plugin according to the provided configuration
func (b *DefaultBuilder) Build(config *BuildConfig) (*BuildResult, error) {
	startTime := time.Now()

	b.logger.WithFields(logger.Fields{
		"plugin_dir": config.PluginDir,
		"size":       config.Size,
		"output_dir": config.OutputDir,
	}).Info("Starting plugin build")

	result := &BuildResult{}

	// Validate the build configuration
	if err := b.ValidateConfig(config); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Load and validate the plugin manifest
	manifest, err := b.manager.LoadManifest(config.PluginDir)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Create output directory
	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		err = errors.WrapFileSystemError(err, "plugin_build",
			"failed to create output directory")
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Generate build artifacts paths
	buildName := fmt.Sprintf("%s-%s", SanitizeName(manifest.Name), manifest.Version)
	imageName := "plugin-" + buildName
	rootfsPath := filepath.Join(config.OutputDir, "rootfs.ext4")
	manifestPath := filepath.Join(config.OutputDir, "plugin.json")
	zipPath := filepath.Join(config.OutputDir, buildName+".zip")

	// Build Docker image
	b.logger.Debug("Building Docker image for plugin")
	if err := b.builder.BuildPluginImage(config.PluginDir, imageName); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Clean up Docker image if requested
	if config.CleanupImage {
		defer func() {
			if err := b.builder.RemoveImage(imageName); err != nil {
				b.logger.WithFields(logger.Fields{
					"image": imageName,
					"error": err,
				}).Warn("Failed to cleanup Docker image")
			}
		}()
	}

	// Export rootfs
	b.logger.Debug("Exporting plugin rootfs")
	if err := b.exportRootfs(imageName, rootfsPath, config.Size); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Copy plugin manifest
	b.logger.Debug("Copying plugin manifest")
	if err := b.copyManifest(config.PluginDir, manifestPath); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Create plugin ZIP
	b.logger.Debug("Creating plugin ZIP package")
	if err := b.manager.CreateZip(zipPath, rootfsPath, manifestPath); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}

	// Clean up temporary files
	os.Remove(rootfsPath)
	os.Remove(manifestPath)

	// Build completed successfully
	result.Success = true
	result.ZipPath = zipPath
	result.RootfsPath = rootfsPath
	result.ManifestPath = manifestPath
	result.BuildTime = time.Since(startTime)

	b.logger.WithFields(logger.Fields{
		"plugin":     manifest.Name,
		"version":    manifest.Version,
		"zip_path":   zipPath,
		"build_time": result.BuildTime,
	}).Info("Plugin build completed successfully")

	return result, nil
}

// ValidateConfig validates the build configuration
func (b *DefaultBuilder) ValidateConfig(config *BuildConfig) error {
	if config == nil {
		return errors.NewValidationError("validate_build_config", "build config cannot be nil")
	}

	// Validate plugin directory
	if err := b.validator.ValidateDirectory(config.PluginDir); err != nil {
		return err
	}

	// Validate size
	if err := b.validator.ValidateSize(config.Size); err != nil {
		return err
	}

	// Validate output directory is writable
	if config.OutputDir == "" {
		return errors.NewValidationError("validate_build_config", "output directory cannot be empty")
	}

	return nil
}

// exportRootfs exports the Docker container filesystem to an ext4 image
func (b *DefaultBuilder) exportRootfs(imageName, outputPath string, sizeMB int) error {
	// Create container name for export
	containerName := "exp-" + strings.ReplaceAll(imageName, "/", "_")

	// Clean up any existing container
	exec.Command("docker", "rm", containerName).Run()

	// Create container
	if err := exec.Command("docker", "create", "--name", containerName, imageName).Run(); err != nil {
		return errors.WrapDockerError(err, "export_rootfs",
			"failed to create container for export")
	}
	defer exec.Command("docker", "rm", containerName).Run()

	// Create empty ext4 filesystem
	b.logger.WithFields(logger.Fields{
		"size_mb": sizeMB,
		"path":    outputPath,
	}).Debug("Creating ext4 filesystem")

	if err := b.createExt4Filesystem(outputPath, sizeMB); err != nil {
		return err
	}

	// Mount filesystem and extract container contents
	if err := b.extractContainerToFilesystem(containerName, outputPath); err != nil {
		return err
	}

	b.logger.WithFields(logger.Fields{
		"path": outputPath,
	}).Info("Rootfs exported successfully")

	return nil
}

// createExt4Filesystem creates an empty ext4 filesystem
func (b *DefaultBuilder) createExt4Filesystem(path string, sizeMB int) error {
	// Create filesystem image
	if err := exec.Command("dd", "if=/dev/zero", "of="+path, "bs=1M", fmt.Sprintf("count=%d", sizeMB)).Run(); err != nil {
		return errors.WrapFileSystemError(err, "create_ext4",
			"failed to create filesystem image")
	}

	// Format as ext4
	if err := exec.Command("mkfs.ext4", "-F", path).Run(); err != nil {
		return errors.WrapFileSystemError(err, "create_ext4",
			"failed to format ext4 filesystem")
	}

	return nil
}

// extractContainerToFilesystem extracts container contents to a mounted filesystem
func (b *DefaultBuilder) extractContainerToFilesystem(containerName, filesystemPath string) error {
	// Create temporary mount point
	tmpDir, err := os.MkdirTemp("", "cms-mount-")
	if err != nil {
		return errors.WrapFileSystemError(err, "extract_container",
			"failed to create temporary directory")
	}
	defer os.RemoveAll(tmpDir)

	// Mount the filesystem
	if err := exec.Command("sudo", "mount", "-o", "loop", filesystemPath, tmpDir).Run(); err != nil {
		return errors.WrapFileSystemError(err, "extract_container",
			"failed to mount filesystem")
	}
	defer exec.Command("sudo", "umount", tmpDir).Run()

	// Export and extract container contents
	exportCmd := exec.Command("docker", "export", containerName)
	tarCmd := exec.Command("sudo", "tar", "-xf", "-", "-C", tmpDir)

	// Connect the commands
	tarCmd.Stdin, _ = exportCmd.StdoutPipe()

	// Capture stderr for better error reporting
	var stderr bytes.Buffer
	tarCmd.Stderr = &stderr

	if err := tarCmd.Start(); err != nil {
		return errors.WrapDockerError(err, "extract_container",
			"failed to start extraction")
	}

	if err := exportCmd.Run(); err != nil {
		return errors.WrapDockerError(err, "extract_container",
			"failed to export container")
	}

	if err := tarCmd.Wait(); err != nil {
		errorOutput := stderr.String()

		// Check for common errors and provide helpful messages (from original code)
		if strings.Contains(errorOutput, "No space left on device") {
			return errors.New(errors.ErrTypeFileSystem, "extract_container",
				fmt.Sprintf("filesystem too small for plugin contents.\n"+
					"💡 Solution: Increase filesystem size with --size flag\n"+
					"   Try: --size 400 (400MB) or --size 500 (500MB)\n"+
					"   Larger plugins may need 800MB or more\n"+
					"Original error: %v", err))
		}

		if strings.Contains(errorOutput, "Cannot mkdir") ||
			strings.Contains(errorOutput, "Cannot create") ||
			strings.Contains(errorOutput, "Cannot open") ||
			strings.Contains(errorOutput, "Cannot hard link") {
			return errors.New(errors.ErrTypeFileSystem, "extract_container",
				fmt.Sprintf("extraction failed - filesystem appears too small.\n"+
					"💡 Solution: Increase filesystem size with --size flag\n"+
					"   Recommended sizes: --size 400, --size 500, or --size 800\n"+
					"Original error: %v", err))
		}

		return errors.WrapFileSystemError(err, "extract_container",
			fmt.Sprintf("failed to extract container contents. Error details: %s", errorOutput))
	}

	return nil
}

// copyManifest copies the plugin manifest to the output directory
func (b *DefaultBuilder) copyManifest(pluginDir, outputPath string) error {
	srcPath := filepath.Join(pluginDir, "plugin.json")

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return errors.WrapFileSystemError(err, "copy_manifest",
			"failed to open source manifest")
	}
	defer srcFile.Close()

	destFile, err := os.Create(outputPath)
	if err != nil {
		return errors.WrapFileSystemError(err, "copy_manifest",
			"failed to create destination manifest")
	}
	defer destFile.Close()

	if _, err := destFile.ReadFrom(srcFile); err != nil {
		return errors.WrapFileSystemError(err, "copy_manifest",
			"failed to copy manifest")
	}

	return nil
}
