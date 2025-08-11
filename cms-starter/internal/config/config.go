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

// Config holds all application configuration
type Config struct {
	// Server configuration
	Port    int    `json:"port"`
	DataDir string `json:"data_dir"`

	// Mode configuration
	Debug    bool `json:"debug"`
	DevMode  bool `json:"dev_mode"`
	TestMode bool `json:"test_mode"`
	Verbose  bool `json:"verbose"`

	// Docker configuration
	DockerHost string `json:"docker_host"`

	// CMS configuration
	CMSImageName     string `json:"cms_image_name"`
	CMSContainerName string `json:"cms_container_name"`

	// Plugin build configuration
	DefaultPluginSize int `json:"default_plugin_size"`
	MinPluginSize     int `json:"min_plugin_size"`
	MaxPluginSize     int `json:"max_plugin_size"`
}

// NewConfig creates a new configuration with sensible defaults
func NewConfig() *Config {
	return &Config{
		Port:              80,
		DataDir:           "./cms-data",
		Debug:             false,
		DevMode:           false,
		TestMode:          false,
		Verbose:           false,
		DockerHost:        "unix:///var/run/docker.sock",
		CMSImageName:      "centraunit/cu-firecracker-cms",
		CMSContainerName:  "cu-firecracker-cms",
		DefaultPluginSize: 200,
		MinPluginSize:     200,
		MaxPluginSize:     800,
	}
}

// LoadFromEnv loads configuration from environment variables
func (c *Config) LoadFromEnv() error {
	if port := os.Getenv("CMS_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			c.Port = p
		}
	}

	if dataDir := os.Getenv("CMS_DATA_DIR"); dataDir != "" {
		c.DataDir = dataDir
	}

	if debug := os.Getenv("CMS_DEBUG"); debug == "true" || debug == "1" {
		c.Debug = true
	}

	if dockerHost := os.Getenv("DOCKER_HOST"); dockerHost != "" {
		c.DockerHost = dockerHost
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d (must be 1-65535)", c.Port)
	}

	if c.DataDir == "" {
		return fmt.Errorf("data directory cannot be empty")
	}

	if c.DefaultPluginSize < c.MinPluginSize || c.DefaultPluginSize > c.MaxPluginSize {
		return fmt.Errorf("default plugin size %d must be between %d and %d",
			c.DefaultPluginSize, c.MinPluginSize, c.MaxPluginSize)
	}

	return nil
}

// GetImageName returns the appropriate Docker image name based on mode
func (c *Config) GetImageName() string {
	if c.TestMode {
		return c.CMSImageName + ":test"
	} else if c.DevMode {
		return c.CMSImageName + ":dev"
	}
	return c.CMSImageName + ":latest"
}

// GetContainerName returns the appropriate container name based on mode
func (c *Config) GetContainerName() string {
	if c.TestMode {
		return c.CMSContainerName + "-test"
	} else if c.DevMode {
		return c.CMSContainerName + "-dev"
	}
	return c.CMSContainerName
}

// IsProductionMode returns true if running in production mode
func (c *Config) IsProductionMode() bool {
	return !c.DevMode && !c.TestMode
}
