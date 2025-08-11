/*
 * Firecracker CMS - Docker Client Service
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/centraunit/cu-firecracker-cms-starter/internal/errors"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

// Client wraps Docker client with CMS-specific operations
type Client struct {
	client *client.Client
	logger *logger.Logger
}

// ContainerConfig represents container configuration
type ContainerConfig struct {
	Image        string
	Name         string
	Cmd          []string
	Env          []string
	Mounts       []MountConfig
	Privileged   bool
	NetworkMode  string
	Capabilities []string
	RemoveOnStop bool
}

// MountConfig represents a mount configuration
type MountConfig struct {
	Source string
	Target string
	Type   string
}

// NewClient creates a new Docker client
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, errors.WrapDockerError(err, "docker_client_init", "failed to initialize Docker client")
	}

	return &Client{
		client: cli,
		logger: logger.GetDefault(),
	}, nil
}

// Close closes the Docker client connection
func (c *Client) Close() error {
	return c.client.Close()
}

// CreateContainer creates a new container with the specified configuration
func (c *Client) CreateContainer(ctx context.Context, config *ContainerConfig) (string, error) {
	c.logger.WithFields(logger.Fields{
		"image": config.Image,
		"name":  config.Name,
	}).Debug("Creating Docker container")

	// Convert mount configs
	mounts := make([]mount.Mount, len(config.Mounts))
	for i, m := range config.Mounts {
		mounts[i] = mount.Mount{
			Type:   mount.Type(m.Type),
			Source: m.Source,
			Target: m.Target,
		}
	}

	containerConfig := &container.Config{
		Image: config.Image,
		Cmd:   config.Cmd,
		Env:   config.Env,
	}

	hostConfig := &container.HostConfig{
		Mounts:      mounts,
		Privileged:  config.Privileged,
		NetworkMode: container.NetworkMode(config.NetworkMode),
		CapAdd:      config.Capabilities,
	}

	resp, err := c.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, config.Name)
	if err != nil {
		return "", errors.WrapDockerError(err, "container_create",
			fmt.Sprintf("failed to create container %s", config.Name))
	}

	c.logger.WithFields(logger.Fields{
		"container_id": resp.ID,
		"name":         config.Name,
	}).Info("Container created successfully")

	return resp.ID, nil
}

// StartContainer starts a container by ID
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	c.logger.WithFields(logger.Fields{
		"container_id": containerID,
	}).Debug("Starting Docker container")

	if err := c.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return errors.WrapDockerError(err, "container_start",
			fmt.Sprintf("failed to start container %s", containerID))
	}

	c.logger.WithFields(logger.Fields{
		"container_id": containerID,
	}).Info("Container started successfully")

	return nil
}

// StopContainer stops a container by name or ID
func (c *Client) StopContainer(ctx context.Context, nameOrID string, remove bool) error {
	c.logger.WithFields(logger.Fields{
		"container": nameOrID,
		"remove":    remove,
	}).Debug("Stopping Docker container")

	containers, err := c.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return errors.WrapDockerError(err, "container_list", "failed to list containers")
	}

	var containerID string
	for _, cont := range containers {
		// Check if the container name or ID matches
		if cont.ID == nameOrID {
			containerID = cont.ID
			break
		}
		for _, name := range cont.Names {
			if strings.TrimPrefix(name, "/") == nameOrID {
				containerID = cont.ID
				break
			}
		}
		if containerID != "" {
			break
		}
	}

	if containerID == "" {
		c.logger.WithFields(logger.Fields{
			"container": nameOrID,
		}).Warn("Container not found for stopping")
		return nil // Not an error if container doesn't exist
	}

	// Stop the container
	timeout := int(30) // 30 seconds timeout
	if err := c.client.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	}); err != nil {
		return errors.WrapDockerError(err, "container_stop",
			fmt.Sprintf("failed to stop container %s", nameOrID))
	}

	c.logger.WithFields(logger.Fields{
		"container_id": containerID,
	}).Info("Container stopped successfully")

	// Remove the container if requested
	if remove {
		if err := c.client.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
			return errors.WrapDockerError(err, "container_remove",
				fmt.Sprintf("failed to remove container %s", nameOrID))
		}
		c.logger.WithFields(logger.Fields{
			"container_id": containerID,
		}).Info("Container removed successfully")
	}

	return nil
}

// GetContainerStatus returns the status of a container
func (c *Client) GetContainerStatus(ctx context.Context, nameOrID string) (string, error) {
	containers, err := c.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", errors.WrapDockerError(err, "container_list", "failed to list containers")
	}

	for _, cont := range containers {
		// Check if the container name or ID matches
		if cont.ID == nameOrID {
			return cont.State, nil
		}
		for _, name := range cont.Names {
			if strings.TrimPrefix(name, "/") == nameOrID {
				return cont.State, nil
			}
		}
	}

	return "not_found", nil
}

// BuildImage builds a Docker image from a Dockerfile in the specified directory
func (c *Client) BuildImage(ctx context.Context, buildDir, imageName string) error {
	c.logger.WithFields(logger.Fields{
		"build_dir": buildDir,
		"image":     imageName,
	}).Info("Building Docker image")

	// This is a simplified version - in production, you'd want to use the Docker API
	// For now, we'll use the existing exec approach but wrapped in our error handling
	return errors.NewDockerError("image_build", "image building not yet implemented in Docker client service")
}

// WaitForContainer waits for a container to reach a specific state
func (c *Client) WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	c.logger.WithFields(logger.Fields{
		"container_id": containerID,
		"timeout":      timeout,
	}).Debug("Waiting for container to be ready")

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	statusCh, errCh := c.client.ContainerWait(timeoutCtx, containerID, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		if err != nil {
			return errors.WrapDockerError(err, "container_wait",
				fmt.Sprintf("error waiting for container %s", containerID))
		}
	case <-statusCh:
		// Container finished
	case <-timeoutCtx.Done():
		return errors.NewDockerError("container_wait",
			fmt.Sprintf("timeout waiting for container %s", containerID))
	}

	return nil
}
