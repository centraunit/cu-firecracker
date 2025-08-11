/*
 * Firecracker CMS - Restart Command
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

// restartCmd represents the restart command
var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the CMS",
	Long: `Restart the CMS container by stopping and starting it.

This command will:
• Stop the current CMS container
• Wait for clean shutdown
• Start a new CMS container with the same configuration`,
	RunE:         runRestart,
	SilenceUsage: true,
}

func runRestart(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	logger.Info("Restarting CMS")

	// Create CMS service
	cmsService, err := services.NewCMSService(cfg)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to create CMS service")
		return err
	}
	defer cmsService.Close()

	// Restart the CMS
	if err := cmsService.Restart(ctx); err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to restart CMS")
		return err
	}

	logger.WithFields(logger.Fields{
		"port": cfg.Port,
		"url":  "http://localhost:" + string(rune(cfg.Port)),
	}).Info("✓ CMS restarted successfully")

	logger.Infof("CMS running at http://localhost:%d", cfg.Port)
	return nil
}
