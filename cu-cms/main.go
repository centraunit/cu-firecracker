/*
 * Firecracker CMS - Advanced Plugin System with microVM Isolation
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 *
 * This software is proprietary and confidential.
 * Unauthorized copying, distribution, or use is strictly prohibited.
 * See LICENSE file for terms and conditions.
 *
 * Contributors: @centraunit-dev, @issa-projects
 */

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/centraunit/cu-firecracker-cms/internal/config"
	"github.com/centraunit/cu-firecracker-cms/internal/logger"
	"github.com/centraunit/cu-firecracker-cms/internal/server"
	"github.com/centraunit/cu-firecracker-cms/internal/services"
)

func main() {
	// Initialize configuration
	cfg := config.NewConfig()
	if err := cfg.LoadFromEnv(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Initialize logging
	if err := logger.Init(cfg.GetLogLevel(), cfg.LogDir); err != nil {
		log.Fatalf("Failed to initialize logging: %v", err)
	}

	log_instance := logger.GetDefault()
	log_instance.WithFields(logger.Fields{
		"version": "1.0.0",
	}).Info("Starting CMS application")

	// Initialize VM service
	vmService, err := services.NewVMService(cfg)
	if err != nil {
		log_instance.WithFields(logger.Fields{
			"error": err,
		}).Fatal("Failed to initialize VM service")
	}

	// Initialize plugin service
	pluginService := services.NewPluginService(cfg, log_instance)

	// Initialize server
	srv := server.New(cfg, log_instance, vmService, pluginService)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log_instance.WithFields(logger.Fields{
			"signal": sig,
		}).Info("Received shutdown signal")
		cancel()
	}()

	// Start server in goroutine
	serverErrChan := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			serverErrChan <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErrChan:
		log_instance.WithFields(logger.Fields{
			"error": err,
		}).Error("Server failed")
	case <-ctx.Done():
		log_instance.Info("Shutting down server")

		// Graceful shutdown with timeout
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := srv.Stop(shutdownCtx); err != nil {
			log_instance.WithFields(logger.Fields{
				"error": err,
			}).Error("Server shutdown failed")
		}

		// Stop VM service
		vmService.Shutdown(shutdownCtx)

		log_instance.Info("Graceful shutdown completed")
	}
}
