/*
 * Firecracker CMS - Root Command
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/config"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/spf13/cobra"
)

var (
	cfg     *config.Config
	cfgFile string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cms-starter",
	Short: "CMS Starter – Run your Firecracker‐isolated CMS",
	Long: `CMS Starter is a professional CLI tool for managing a Firecracker-based 
Content Management System with microVM isolation for plugins.

Features:
• Start/stop/restart CMS with proper lifecycle management
• Build and package plugins into bootable ext4 filesystems
• Comprehensive testing with real plugin validation
• Professional logging with debug controls
• Resilient error handling and recovery`,
	PersistentPreRunE: initializeConfig,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Initialize configuration
	cfg = config.NewConfig()

	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&cfg.Verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVar(&cfg.Debug, "debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&cfg.DevMode, "dev", false, "Enable development mode")
	rootCmd.PersistentFlags().BoolVar(&cfg.TestMode, "test", false, "Enable test mode (runs tests)")
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default is $HOME/.cms-starter.yaml)")

	// Add subcommands
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(pluginCmd)
}

// initializeConfig initializes the configuration and logging
func initializeConfig(cmd *cobra.Command, args []string) error {
	// Load configuration from environment
	if err := cfg.LoadFromEnv(); err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize logging
	logDir := ""
	if cfg.Verbose || cfg.Debug {
		logDir = filepath.Join(cfg.DataDir, "logs")
	}

	if err := logger.Init(cfg.Debug, logDir); err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}

	// Log configuration if debug is enabled
	logger.WithFields(logger.Fields{
		"config": map[string]interface{}{
			"port":      cfg.Port,
			"data_dir":  cfg.DataDir,
			"debug":     cfg.Debug,
			"dev_mode":  cfg.DevMode,
			"test_mode": cfg.TestMode,
		},
	}).Debug("Configuration loaded")

	return nil
}

// GetConfig returns the current configuration
func GetConfig() *config.Config {
	return cfg
}

// printModeInfo prints information about the current mode
func printModeInfo() {
	if cfg.TestMode {
		fmt.Println("🧪 Test mode detected - running comprehensive test suite...")
	} else if cfg.DevMode {
		fmt.Println("🚀 Starting CMS in development mode...")
	} else {
		fmt.Println("🏭 Starting CMS in production mode...")
	}
}
