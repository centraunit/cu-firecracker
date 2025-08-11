/*
 * Firecracker CMS - Plugin Validator
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
)

// DefaultValidator implements the Validator interface
type DefaultValidator struct {
	minSize int
	maxSize int
}

// NewValidator creates a new plugin validator
func NewValidator(minSize, maxSize int) *DefaultValidator {
	return &DefaultValidator{
		minSize: minSize,
		maxSize: maxSize,
	}
}

// ValidateManifest validates a plugin manifest
func (v *DefaultValidator) ValidateManifest(manifest *Manifest) error {
	if manifest == nil {
		return errors.NewValidationError("validate_manifest", "manifest cannot be nil")
	}

	// Validate required fields
	if manifest.Slug == "" {
		return errors.NewValidationError("validate_manifest", "plugin slug is required")
	}

	if manifest.Name == "" {
		return errors.NewValidationError("validate_manifest", "plugin name is required")
	}

	if manifest.Version == "" {
		return errors.NewValidationError("validate_manifest", "plugin version is required")
	}

	// Validate slug format (lowercase alphanumeric with hyphens)
	if err := v.validateSlugFormat(manifest.Slug); err != nil {
		return err
	}

	// Validate version format (semantic versioning)
	if err := v.validateVersionFormat(manifest.Version); err != nil {
		return err
	}

	// Validate runtime if specified
	if manifest.Runtime != "" {
		if err := v.validateRuntime(manifest.Runtime); err != nil {
			return err
		}
	}

	return nil
}

// ValidateDirectory validates a plugin directory structure
func (v *DefaultValidator) ValidateDirectory(pluginDir string) error {
	// Check if directory exists
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return errors.NewFileSystemError("validate_directory",
			fmt.Sprintf("plugin directory does not exist: %s", pluginDir))
	}

	// Check for required files
	requiredFiles := []string{"plugin.json", "Dockerfile"}
	for _, file := range requiredFiles {
		filePath := filepath.Join(pluginDir, file)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return errors.NewValidationError("validate_directory",
				fmt.Sprintf("required file missing: %s", file))
		}
	}

	return nil
}

// ValidateSize validates plugin filesystem size
func (v *DefaultValidator) ValidateSize(sizeMB int) error {
	if sizeMB < v.minSize {
		return errors.NewValidationError("validate_size",
			fmt.Sprintf("size %dMB is below minimum %dMB", sizeMB, v.minSize))
	}

	if sizeMB > v.maxSize {
		return errors.NewValidationError("validate_size",
			fmt.Sprintf("size %dMB exceeds maximum %dMB", sizeMB, v.maxSize))
	}

	return nil
}

// validateSlugFormat validates the plugin slug format
func (v *DefaultValidator) validateSlugFormat(slug string) error {
	// Slug should be lowercase alphanumeric with hyphens, 3-50 characters
	if len(slug) < 3 || len(slug) > 50 {
		return errors.NewValidationError("validate_slug",
			"slug must be 3-50 characters long")
	}

	slugRegex := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !slugRegex.MatchString(slug) {
		return errors.NewValidationError("validate_slug",
			"slug must contain only lowercase letters, numbers, and hyphens")
	}

	// Slug cannot start or end with hyphen
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return errors.NewValidationError("validate_slug",
			"slug cannot start or end with a hyphen")
	}

	return nil
}

// validateVersionFormat validates the version format (basic semantic versioning)
func (v *DefaultValidator) validateVersionFormat(version string) error {
	// Basic semantic versioning: X.Y.Z or X.Y.Z-suffix
	versionRegex := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[a-zA-Z0-9\-\.]+)?$`)
	if !versionRegex.MatchString(version) {
		return errors.NewValidationError("validate_version",
			"version must follow semantic versioning format (e.g., 1.0.0)")
	}

	return nil
}

// validateRuntime validates the runtime specification
func (v *DefaultValidator) validateRuntime(runtime string) error {
	validRuntimes := []string{"python", "node", "php", "go", "rust", "java"}

	runtime = strings.ToLower(runtime)
	for _, valid := range validRuntimes {
		if runtime == valid {
			return nil
		}
	}

	return errors.NewValidationError("validate_runtime",
		fmt.Sprintf("unsupported runtime: %s (supported: %s)",
			runtime, strings.Join(validRuntimes, ", ")))
}
