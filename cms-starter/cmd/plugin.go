/*
 * Firecracker CMS - Plugin Commands
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package cmd

import (
	"fmt"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/services"
	"github.com/spf13/cobra"
)

// pluginCmd represents the plugin command group
var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Plugin management commands",
	Long: `Plugin management commands for building, validating, and packaging plugins.

Available subcommands:
• build   - Build a plugin into a bootable ext4 filesystem
• validate - Validate a plugin directory and manifest
• info    - Show information about a plugin`,
}

// buildCmd represents the plugin build command
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build plugin into bootable ext4",
	Long: `Build a plugin from source into a bootable ext4 filesystem packaged in a ZIP file.

This command will:
• Validate the plugin directory and manifest
• Build a Docker image from the plugin
• Export the filesystem to an ext4 image
• Package everything into a ZIP file ready for CMS upload

The resulting ZIP file contains:
• rootfs.ext4 - The bootable filesystem
• plugin.json - The plugin manifest`,
	RunE:         runPluginBuild,
	SilenceUsage: true,
}

// validateCmd represents the plugin validate command
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a plugin",
	Long: `Validate a plugin directory and manifest for correctness.

This command will check:
• Directory structure and required files
• Plugin manifest format and required fields
• Dockerfile existence and basic syntax`,
	RunE:         runPluginValidate,
	SilenceUsage: true,
}

// infoCmd represents the plugin info command
var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show plugin information",
	Long: `Display detailed information about a plugin including its manifest data.

This will show:
• Plugin metadata (name, version, author, etc.)
• Runtime information
• Actions and hooks defined by the plugin`,
	RunE:         runPluginInfo,
	SilenceUsage: true,
}

func init() {
	// Build command flags
	buildCmd.Flags().String("plugin", "", "Plugin directory (required)")
	buildCmd.Flags().Int("size", 200, "Ext4 filesystem size in MB (200-800)")
	buildCmd.MarkFlagRequired("plugin")

	// Validate command flags
	validateCmd.Flags().String("plugin", "", "Plugin directory (required)")
	validateCmd.MarkFlagRequired("plugin")

	// Info command flags
	infoCmd.Flags().String("plugin", "", "Plugin directory (required)")
	infoCmd.MarkFlagRequired("plugin")

	// Add subcommands to plugin command
	pluginCmd.AddCommand(buildCmd)
	pluginCmd.AddCommand(validateCmd)
	pluginCmd.AddCommand(infoCmd)
}

func runPluginBuild(cmd *cobra.Command, args []string) error {
	pluginDir, _ := cmd.Flags().GetString("plugin")
	sizeMB, _ := cmd.Flags().GetInt("size")

	// User-friendly output like the original
	fmt.Printf("Building plugin from: %s\n", pluginDir)

	// Provide size recommendations like the original
	if sizeMB == 200 { // Default size, provide recommendations
		fmt.Printf("ℹ️  Info: Using default 200MB filesystem\n")
		fmt.Printf("   If build fails due to space issues, try --size 400 or --size 500\n")
	} else {
		fmt.Printf("ℹ️  Info: Using %dMB filesystem\n", sizeMB)
	}

	pluginService := services.NewPluginService(GetConfig())

	result, err := pluginService.BuildPlugin(pluginDir, sizeMB)
	if err != nil {
		return err
	}

	// Success output like the original
	fmt.Printf("✅ Plugin packaged successfully: %s\n", result.ZipPath)
	fmt.Printf("📁 ZIP contains: rootfs.ext4 + plugin.json\n")
	fmt.Printf("📤 Ready to upload to CMS!\n")

	return nil
}

func runPluginValidate(cmd *cobra.Command, args []string) error {
	pluginDir, _ := cmd.Flags().GetString("plugin")

	logger.WithFields(logger.Fields{
		"plugin_dir": pluginDir,
	}).Debug("Validating plugin")

	// Create plugin service
	pluginService := services.NewPluginService(GetConfig())

	// Validate the plugin
	if err := pluginService.ValidatePlugin(pluginDir); err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Plugin validation failed")

		// Provide specific guidance based on error type
		if errors.IsType(err, errors.ErrTypeValidation) {
			fmt.Printf("❌ Validation failed: %v\n", err)
			fmt.Printf("💡 Check your plugin.json format and required fields\n")
		} else if errors.IsType(err, errors.ErrTypeFileSystem) {
			fmt.Printf("❌ File system error: %v\n", err)
			fmt.Printf("💡 Ensure all required files exist (plugin.json, Dockerfile)\n")
		} else {
			fmt.Printf("❌ Validation failed: %v\n", err)
		}

		return err
	}

	fmt.Printf("✓ Plugin validation passed\n")
	logger.Info("Plugin validation successful")

	return nil
}

func runPluginInfo(cmd *cobra.Command, args []string) error {
	pluginDir, _ := cmd.Flags().GetString("plugin")

	logger.WithFields(logger.Fields{
		"plugin_dir": pluginDir,
	}).Debug("Getting plugin info")

	// Create plugin service
	pluginService := services.NewPluginService(GetConfig())

	// Get plugin information
	manifest, err := pluginService.GetPluginInfo(pluginDir)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to get plugin info")
		return err
	}

	// Display plugin information
	fmt.Printf("Plugin Information:\n")
	fmt.Printf("  Slug: %s\n", manifest.Slug)
	fmt.Printf("  Name: %s\n", manifest.Name)
	fmt.Printf("  Version: %s\n", manifest.Version)
	fmt.Printf("  Author: %s\n", manifest.Author)
	fmt.Printf("  Description: %s\n", manifest.Description)
	if manifest.Runtime != "" {
		fmt.Printf("  Runtime: %s\n", manifest.Runtime)
	}
	fmt.Printf("  Actions: %d defined\n", len(manifest.Actions))

	return nil
}
