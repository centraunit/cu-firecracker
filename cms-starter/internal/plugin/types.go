/*
 * Firecracker CMS - Plugin Types
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package plugin

import (
	"time"
)

// Manifest represents a plugin manifest (plugin.json)
type Manifest struct {
	Slug        string                 `json:"slug"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Runtime     string                 `json:"runtime"`
	Actions     map[string]interface{} `json:"actions"`
}

// BuildConfig represents plugin build configuration
type BuildConfig struct {
	PluginDir    string
	Size         int
	OutputDir    string
	CleanupImage bool
}

// BuildResult represents the result of a plugin build
type BuildResult struct {
	ZipPath      string        `json:"zip_path"`
	RootfsPath   string        `json:"rootfs_path"`
	ManifestPath string        `json:"manifest_path"`
	BuildTime    time.Duration `json:"build_time"`
	Success      bool          `json:"success"`
	Error        string        `json:"error,omitempty"`
}

// Validator interface for plugin validation
type Validator interface {
	ValidateManifest(manifest *Manifest) error
	ValidateDirectory(pluginDir string) error
	ValidateSize(sizeMB int) error
}

// Builder interface for plugin building
type Builder interface {
	Build(config *BuildConfig) (*BuildResult, error)
	ValidateConfig(config *BuildConfig) error
}

// Manager interface for plugin management
type Manager interface {
	LoadManifest(pluginDir string) (*Manifest, error)
	CreateZip(zipPath, rootfsPath, manifestPath string) error
	ExtractZip(zipPath, destDir string) error
}
