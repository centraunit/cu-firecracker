/*
 * Firecracker CMS - Start Command
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package cmd

import (
	"context"
	"time"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/services"
	"github.com/spf13/cobra"
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the CMS",
	Long: `Start the CMS container with proper configuration and health checks.

The start command will:
• Create necessary data directories
• Start the CMS Docker container with proper mounts and privileges
• Wait for the container to be ready
• Provide helpful status information

In test mode, this will run the comprehensive test suite instead.`,
	RunE:         runStart,
	SilenceUsage: true,
}

func init() {
	// Command-specific flags
	startCmd.Flags().IntVarP(&cfg.Port, "port", "p", 80, "CMS port")
	startCmd.Flags().StringVarP(&cfg.DataDir, "data-dir", "d", "./cms-data", "Data directory")
}

func runStart(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Handle test mode
	if cfg.TestMode {
		return runTestMode(ctx)
	}

	// Handle normal start
	return runNormalStart(ctx)
}

func runTestMode(ctx context.Context) error {
	logger.Info("Test mode detected - running comprehensive test suite")

	cmsService, err := services.NewCMSService(cfg)
	if err != nil {
		return err
	}
	defer cmsService.Close()

	startTime := time.Now()
	if err := cmsService.RunTests(ctx); err != nil {
		logger.WithFields(logger.Fields{
			"error":    err,
			"duration": time.Since(startTime),
		}).Error("Test suite failed")
		return err
	}

	logger.WithFields(logger.Fields{
		"duration": time.Since(startTime),
	}).Info("✓ All tests passed successfully!")

	return nil
}

func runNormalStart(ctx context.Context) error {
	printModeInfo()

	// Create CMS service
	cmsService, err := services.NewCMSService(cfg)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to create CMS service")
		return err
	}
	defer cmsService.Close()

	// Start the CMS
	startTime := time.Now()
	if err := cmsService.Start(ctx); err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to start CMS")
		return err
	}

	logger.WithFields(logger.Fields{
		"port":       cfg.Port,
		"data_dir":   cfg.DataDir,
		"start_time": time.Since(startTime),
		"url":        "http://localhost:" + string(rune(cfg.Port)),
	}).Info("✓ CMS started successfully")

	logger.Infof("CMS running at http://localhost:%d", cfg.Port)
	return nil
}
