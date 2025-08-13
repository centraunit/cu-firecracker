/*
 * Firecracker CMS - Status Command
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package cmd

import (
	"context"
	"fmt"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/services"
	"github.com/spf13/cobra"
)

// statusCmd represents the status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check CMS status",
	Long: `Check the current status of the CMS container.

This command will show:
• Container running state
• Container health information
• Configuration details`,
	RunE:         runStatus,
	SilenceUsage: true,
}

func runStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Create CMS service
	cmsService, err := services.NewCMSService(cfg)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to create CMS service")
		return err
	}
	defer cmsService.Close()

	// Get CMS status
	status, err := cmsService.Status(ctx)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to get CMS status")
		return err
	}

	// Display status information
	fmt.Printf("CMS Status: %s\n", status)
	fmt.Printf("Container: %s\n", cfg.GetContainerName())
	fmt.Printf("Image: %s\n", cfg.GetImageName())
	fmt.Printf("Port: %d\n", cfg.Port)
	fmt.Printf("Data Directory: %s\n", cfg.DataDir)
	fmt.Printf("Mode: %s\n", cfg.GetModeString())

	if status == "running" {
		fmt.Printf("URL: http://localhost:%d\n", cfg.Port)
		logger.Info("✓ CMS is running")
	} else if status == "not_found" {
		logger.Info("⚠ CMS container not found")
	} else {
		logger.WithFields(logger.Fields{
			"status": status,
		}).Info("CMS container exists but is not running")
	}

	return nil
}
