/*
 * Firecracker CMS - Configuration Management
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all CMS configuration
type Config struct {
	// Server configuration
	Port   string `json:"port"`
	Host   string `json:"host"`
	Debug  bool   `json:"debug"`
	LogDir string `json:"log_dir"`

	// Mode configuration
	Mode    string `json:"mode"`    // "development", "production", "test"
	Verbose bool   `json:"verbose"` // Verbose logging

	// Data directories
	DataDir     string `json:"data_dir"`
	PluginsDir  string `json:"plugins_dir"`
	SnapshotDir string `json:"snapshot_dir"`

	// Firecracker configuration
	FirecrackerPath string `json:"firecracker_path"`
	KernelPath      string `json:"kernel_path"`

	// VM Pool configuration
	PrewarmPoolSize int `json:"prewarm_pool_size"`
}

// NewConfig creates a new configuration with sensible defaults
func NewConfig() *Config {
	return &Config{
		// Server defaults
		Port:   "80",
		Host:   "0.0.0.0",
		Debug:  false,
		LogDir: "/app/data/logs",

		// Mode defaults
		Mode:    "production", // Default to production
		Verbose: false,

		// Data directories
		DataDir:     "/app/data",
		PluginsDir:  "/app/data/plugins",
		SnapshotDir: "/app/data/snapshots",

		// Firecracker defaults
		FirecrackerPath: "/usr/local/bin/firecracker",
		KernelPath:      "/opt/kernel/vmlinux",

		// VM Pool defaults - configurable, not hardcoded!
		PrewarmPoolSize: 10, // Default to 10, but can be overridden
	}
}

// LoadFromEnv loads configuration from environment variables
func (c *Config) LoadFromEnv() error {
	if port := os.Getenv("CMS_PORT"); port != "" {
		c.Port = port
	}

	if host := os.Getenv("CMS_HOST"); host != "" {
		c.Host = host
	}

	if debug := os.Getenv("CMS_DEBUG"); debug == "true" || debug == "1" {
		c.Debug = true
	}

	if mode := os.Getenv("CMS_MODE"); mode != "" {
		c.Mode = mode
	}

	if verbose := os.Getenv("CMS_VERBOSE"); verbose == "true" || verbose == "1" {
		c.Verbose = true
	}

	if dataDir := os.Getenv("CMS_DATA_DIR"); dataDir != "" {
		c.DataDir = dataDir
	}

	if logDir := os.Getenv("CMS_LOG_DIR"); logDir != "" {
		c.LogDir = logDir
	}

	if pluginsDir := os.Getenv("CMS_PLUGINS_DIR"); pluginsDir != "" {
		c.PluginsDir = pluginsDir
	}

	if snapshotDir := os.Getenv("CMS_SNAPSHOT_DIR"); snapshotDir != "" {
		c.SnapshotDir = snapshotDir
	}

	if firecrackerPath := os.Getenv("FIRECRACKER_PATH"); firecrackerPath != "" {
		c.FirecrackerPath = firecrackerPath
	}

	if kernelPath := os.Getenv("KERNEL_PATH"); kernelPath != "" {
		c.KernelPath = kernelPath
	}

	// Parse PrewarmPoolSize from environment
	if poolSize := os.Getenv("CMS_PREWARM_POOL_SIZE"); poolSize != "" {
		if val, err := strconv.Atoi(poolSize); err == nil && val > 0 {
			c.PrewarmPoolSize = val
		}
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Port == "" {
		return fmt.Errorf("port cannot be empty")
	}

	if c.DataDir == "" {
		return fmt.Errorf("data directory cannot be empty")
	}

	if c.PrewarmPoolSize <= 0 {
		return fmt.Errorf("prewarm pool size must be positive")
	}

	return nil
}

// GetLogLevel returns the configured log level
func (c *Config) GetLogLevel() string {
	if c.Debug {
		return "debug"
	}
	return "info"
}

// IsDebugMode returns true if debug mode is enabled
func (c *Config) IsDebugMode() bool {
	return c.Debug
}

// IsDevelopmentMode returns true if running in development mode
func (c *Config) IsDevelopmentMode() bool {
	return c.Mode == "development" || c.Mode == "dev"
}

// IsProductionMode returns true if running in production mode
func (c *Config) IsProductionMode() bool {
	return c.Mode == "production" || c.Mode == "prod"
}

// IsTestMode returns true if running in test mode
func (c *Config) IsTestMode() bool {
	return c.Mode == "test"
}

// GetModeString returns a human-readable mode string
func (c *Config) GetModeString() string {
	switch c.Mode {
	case "development", "dev":
		return "development"
	case "production", "prod":
		return "production"
	case "test":
		return "test"
	default:
		return "production"
	}
}
