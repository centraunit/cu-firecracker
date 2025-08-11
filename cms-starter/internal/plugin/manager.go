/*
 * Firecracker CMS - Plugin Manager
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package plugin

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
)

// DefaultManager implements the Manager interface
type DefaultManager struct {
	logger    *logger.Logger
	validator Validator
}

// NewManager creates a new plugin manager
func NewManager(validator Validator) *DefaultManager {
	return &DefaultManager{
		logger:    logger.GetDefault(),
		validator: validator,
	}
}

// LoadManifest loads and validates a plugin manifest from a directory
func (m *DefaultManager) LoadManifest(pluginDir string) (*Manifest, error) {
	manifestPath := filepath.Join(pluginDir, "plugin.json")

	m.logger.WithFields(logger.Fields{
		"manifest_path": manifestPath,
	}).Debug("Loading plugin manifest")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, errors.WrapFileSystemError(err, "load_manifest",
			fmt.Sprintf("failed to read manifest file: %s", manifestPath))
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, errors.WrapValidationError(err, "load_manifest",
			"failed to parse plugin manifest JSON")
	}

	// Validate the manifest
	if err := m.validator.ValidateManifest(&manifest); err != nil {
		return nil, errors.Wrap(err, errors.ErrTypeValidation, "load_manifest",
			"manifest validation failed")
	}

	m.logger.WithFields(logger.Fields{
		"slug":    manifest.Slug,
		"name":    manifest.Name,
		"version": manifest.Version,
	}).Info("Plugin manifest loaded and validated")

	return &manifest, nil
}

// CreateZip creates a ZIP file containing the rootfs and manifest
func (m *DefaultManager) CreateZip(zipPath, rootfsPath, manifestPath string) error {
	m.logger.WithFields(logger.Fields{
		"zip_path":      zipPath,
		"rootfs_path":   rootfsPath,
		"manifest_path": manifestPath,
	}).Debug("Creating plugin ZIP file")

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
		return errors.WrapFileSystemError(err, "create_zip",
			"failed to create output directory")
	}

	zipFile, err := os.Create(zipPath)
	if err != nil {
		return errors.WrapFileSystemError(err, "create_zip",
			"failed to create ZIP file")
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Add rootfs.ext4
	if err := m.addFileToZip(zipWriter, rootfsPath, "rootfs.ext4"); err != nil {
		return errors.Wrap(err, errors.ErrTypeFileSystem, "create_zip",
			"failed to add rootfs to ZIP")
	}

	// Add plugin.json
	if err := m.addFileToZip(zipWriter, manifestPath, "plugin.json"); err != nil {
		return errors.Wrap(err, errors.ErrTypeFileSystem, "create_zip",
			"failed to add manifest to ZIP")
	}

	m.logger.WithFields(logger.Fields{
		"zip_path": zipPath,
	}).Info("Plugin ZIP file created successfully")

	return nil
}

// ExtractZip extracts a plugin ZIP file to a destination directory
func (m *DefaultManager) ExtractZip(zipPath, destDir string) error {
	m.logger.WithFields(logger.Fields{
		"zip_path": zipPath,
		"dest_dir": destDir,
	}).Debug("Extracting plugin ZIP file")

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return errors.WrapFileSystemError(err, "extract_zip",
			"failed to open ZIP file")
	}
	defer reader.Close()

	// Ensure destination directory exists
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return errors.WrapFileSystemError(err, "extract_zip",
			"failed to create destination directory")
	}

	// Track required files
	hasRootfs := false
	hasManifest := false

	for _, file := range reader.File {
		// Security check: prevent path traversal
		if strings.Contains(file.Name, "..") {
			return errors.NewValidationError("extract_zip",
				fmt.Sprintf("invalid file path in ZIP: %s", file.Name))
		}

		// Only extract required files
		if file.Name != "rootfs.ext4" && file.Name != "plugin.json" {
			m.logger.WithFields(logger.Fields{
				"file": file.Name,
			}).Debug("Skipping non-required file in ZIP")
			continue
		}

		destPath := filepath.Join(destDir, file.Name)
		if err := m.extractFileFromZip(file, destPath); err != nil {
			return errors.Wrap(err, errors.ErrTypeFileSystem, "extract_zip",
				fmt.Sprintf("failed to extract file: %s", file.Name))
		}

		if file.Name == "rootfs.ext4" {
			hasRootfs = true
		} else if file.Name == "plugin.json" {
			hasManifest = true
		}
	}

	// Validate that required files were present
	if !hasRootfs {
		return errors.NewValidationError("extract_zip",
			"rootfs.ext4 not found in plugin ZIP")
	}
	if !hasManifest {
		return errors.NewValidationError("extract_zip",
			"plugin.json not found in plugin ZIP")
	}

	m.logger.WithFields(logger.Fields{
		"zip_path": zipPath,
		"dest_dir": destDir,
	}).Info("Plugin ZIP extracted successfully")

	return nil
}

// addFileToZip adds a file to a ZIP archive
func (m *DefaultManager) addFileToZip(zipWriter *zip.Writer, filePath, zipName string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer, err := zipWriter.Create(zipName)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	return err
}

// extractFileFromZip extracts a single file from a ZIP archive
func (m *DefaultManager) extractFileFromZip(file *zip.File, destPath string) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()

	destFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, reader)
	return err
}

// SanitizeName sanitizes a plugin name for use in file paths
func SanitizeName(name string) string {
	// Convert to lowercase and replace invalid characters with hyphens
	sanitized := strings.ToLower(name)
	sanitized = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, sanitized)

	// Remove multiple consecutive hyphens
	for strings.Contains(sanitized, "--") {
		sanitized = strings.ReplaceAll(sanitized, "--", "-")
	}

	// Trim hyphens from start and end
	sanitized = strings.Trim(sanitized, "-")

	return sanitized
}
