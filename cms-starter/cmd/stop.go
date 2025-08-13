/*
 * Firecracker CMS - Stop Command
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package cmd

import (
	"context"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/services"
	"github.com/spf13/cobra"
)

// stopCmd represents the stop command
var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the CMS",
	Long: `Stop the running CMS container gracefully.

This command will:
• Stop the CMS Docker container
• Clean up resources
• Remove the container if it exists`,
	RunE:         runStop,
	SilenceUsage: true,
}

func runStop(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	logger.Info("Stopping CMS")

	// Create CMS service
	cmsService, err := services.NewCMSService(cfg)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to create CMS service")
		return err
	}
	defer cmsService.Close()

	// Stop the CMS
	if err := cmsService.Stop(ctx); err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to stop CMS")
		return err
	}

	logger.Info("✓ CMS stopped successfully")
	return nil
}
