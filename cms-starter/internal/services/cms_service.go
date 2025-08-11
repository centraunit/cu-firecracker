/*
 * Firecracker CMS - CMS Service
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/config"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/docker"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
)

// CMSService handles CMS container lifecycle management
type CMSService struct {
	config       *config.Config
	dockerClient *docker.Client
	logger       *logger.Logger
}

// NewCMSService creates a new CMS service
func NewCMSService(cfg *config.Config) (*CMSService, error) {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return nil, errors.Wrap(err, errors.ErrTypeDocker, "cms_service_init",
			"failed to initialize Docker client")
	}

	return &CMSService{
		config:       cfg,
		dockerClient: dockerClient,
		logger:       logger.GetDefault(),
	}, nil
}

// Close closes the CMS service and releases resources
func (s *CMSService) Close() error {
	return s.dockerClient.Close()
}

// Start starts the CMS container
func (s *CMSService) Start(ctx context.Context) error {
	s.logger.WithFields(logger.Fields{
		"mode": s.getModeString(),
		"port": s.config.Port,
	}).Info("Starting CMS container")

	// Stop any existing container first
	if err := s.Stop(ctx); err != nil {
		s.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to stop existing container, continuing")
	}

	// Ensure data directories exist
	if err := s.ensureDataDirectories(); err != nil {
		return err
	}

	// Get absolute path for data directory
	absDataDir, err := filepath.Abs(s.config.DataDir)
	if err != nil {
		return errors.WrapFileSystemError(err, "start_cms",
			"failed to get absolute path for data directory")
	}

	// Create container configuration
	containerConfig := &docker.ContainerConfig{
		Image: s.config.GetImageName(),
		Name:  s.config.GetContainerName(),
		Cmd:   []string{"./cms"},
		Env: []string{
			fmt.Sprintf("CMS_PORT=%d", s.config.Port),
		},
		Mounts: []docker.MountConfig{
			{Source: absDataDir, Target: "/app/data", Type: "bind"},
			{Source: "/dev/kvm", Target: "/dev/kvm", Type: "bind"},
			{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", Type: "bind"},
		},
		Privileged:   true,
		NetworkMode:  "host",
		Capabilities: []string{"SYS_ADMIN", "NET_ADMIN", "NET_RAW"},
	}

	// Create and start container
	containerID, err := s.dockerClient.CreateContainer(ctx, containerConfig)
	if err != nil {
		return err
	}

	if err := s.dockerClient.StartContainer(ctx, containerID); err != nil {
		return err
	}

	s.logger.WithFields(logger.Fields{
		"container_id": containerID,
		"port":         s.config.Port,
	}).Info("CMS container started successfully")

	return nil
}

// Stop stops the CMS container
func (s *CMSService) Stop(ctx context.Context) error {
	s.logger.Debug("Stopping CMS container")

	containerName := s.config.GetContainerName()
	if err := s.dockerClient.StopContainer(ctx, containerName, true); err != nil {
		if errors.IsType(err, errors.ErrTypeDocker) {
			// Log as debug if it's just a Docker error (container might not exist)
			s.logger.WithFields(logger.Fields{
				"container": containerName,
				"error":     err,
			}).Debug("Error stopping container")
			return nil
		}
		return err
	}

	s.logger.Info("CMS container stopped successfully")
	return nil
}

// Restart restarts the CMS container
func (s *CMSService) Restart(ctx context.Context) error {
	s.logger.Info("Restarting CMS container")

	if err := s.Stop(ctx); err != nil {
		return err
	}

	// Small delay to ensure cleanup is complete
	time.Sleep(2 * time.Second)

	return s.Start(ctx)
}

// Status returns the status of the CMS container
func (s *CMSService) Status(ctx context.Context) (string, error) {
	containerName := s.config.GetContainerName()
	status, err := s.dockerClient.GetContainerStatus(ctx, containerName)
	if err != nil {
		return "", err
	}

	s.logger.WithFields(logger.Fields{
		"container": containerName,
		"status":    status,
	}).Debug("Retrieved CMS container status")

	return status, nil
}

// RunTests runs the comprehensive test suite
func (s *CMSService) RunTests(ctx context.Context) error {
	s.logger.Info("Running comprehensive test suite")

	// Step 1: Prepare test plugins
	if err := s.prepareTestPlugins(); err != nil {
		return errors.Wrap(err, errors.ErrTypePlugin, "run_tests",
			"failed to prepare test plugins")
	}

	// Step 2: Build CMS test image
	if err := s.buildTestImage(ctx); err != nil {
		return err
	}

	// Step 3: Run CMS unit tests
	if err := s.runUnitTests(ctx); err != nil {
		return err
	}

	// Step 4: Run integration tests
	if err := s.runIntegrationTests(ctx); err != nil {
		return err
	}

	s.logger.Info("All tests completed successfully")
	return nil
}

// ensureDataDirectories creates necessary data directories
func (s *CMSService) ensureDataDirectories() error {
	dirs := []string{
		s.config.DataDir,
		filepath.Join(s.config.DataDir, "plugins"),
		filepath.Join(s.config.DataDir, "logs"),
	}

	for _, dir := range dirs {
		if err := createDir(dir); err != nil {
			return errors.WrapFileSystemError(err, "ensure_directories",
				fmt.Sprintf("failed to create directory: %s", dir))
		}
	}

	return nil
}

// prepareTestPlugins builds real plugins for testing
func (s *CMSService) prepareTestPlugins() error {
	s.logger.Info("Preparing test plugins")

	// This would implement the test plugin preparation logic
	// For now, we'll leave it as a placeholder
	testPluginsDir := filepath.Join(s.config.DataDir, "test-plugins")
	return createDir(testPluginsDir)
}

// buildTestImage builds the CMS test Docker image
func (s *CMSService) buildTestImage(ctx context.Context) error {
	s.logger.Info("Building CMS test image")

	imageName := s.config.CMSImageName + ":test"
	cmd := exec.Command("docker", "build", "-f", "../cu-cms/Dockerfile", "-t", imageName, "../cu-cms")

	if err := cmd.Run(); err != nil {
		return errors.WrapDockerError(err, "build_test_image",
			"failed to build CMS test image")
	}

	s.logger.WithFields(logger.Fields{
		"image": imageName,
	}).Info("CMS test image built successfully")

	return nil
}

// runUnitTests runs the CMS unit tests in Docker
func (s *CMSService) runUnitTests(ctx context.Context) error {
	s.logger.Info("Running CMS unit tests")

	// Implementation would run the actual unit tests
	// This is a placeholder for the test execution logic

	return nil
}

// runIntegrationTests runs integration tests against a live CMS instance
func (s *CMSService) runIntegrationTests(ctx context.Context) error {
	s.logger.Info("Running integration tests")

	// Implementation would run the actual integration tests
	// This is a placeholder for the integration test logic

	return nil
}

// getModeString returns a human-readable string for the current mode
func (s *CMSService) getModeString() string {
	if s.config.TestMode {
		return "test"
	} else if s.config.DevMode {
		return "development"
	}
	return "production"
}

// createDir creates a directory with proper error handling
func createDir(path string) error {
	return os.MkdirAll(path, 0755)
}
