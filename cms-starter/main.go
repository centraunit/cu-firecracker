package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/spf13/cobra"
)

const (
	CMSImageName   = "issaprodev/cu-cms:latest"
	DataDir        = "./cms-data"
	FirecrackerDir = "./firecracker-tmp"
)

type CMSStarter struct {
	dockerClient *client.Client
	dataDir      string
	port         int
}

var (
	port    int
	dataDir string
	verbose bool
	rootCmd = &cobra.Command{
		Use:   "cms-starter",
		Short: "CMS Starter - Start your CMS system with Firecracker plugin isolation",
		Long: `CMS Starter is a lightweight CLI tool that manages your CMS system.

It checks for Docker installation, sets up host requirements, and starts the CMS
container with Firecracker microVM support for plugin isolation.

Features:
- Automatic Docker installation check
- Host requirements setup (/tmp/firecracker)
- Data directory management
- Container lifecycle management
- Cross-platform support

Example usage:
  cms-starter start                    # Start on default port 80
  cms-starter start --port 8080       # Start on port 8080
  cms-starter start --data-dir ./my-data  # Use custom data directory
  cms-starter stop                     # Stop the CMS
  cms-starter status                   # Check CMS status`,
	}

	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Start the CMS system",
		Long:  "Start the CMS system with Firecracker plugin isolation",
		RunE:  runStart,
	}

	stopCmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop the CMS system",
		Long:  "Stop the CMS system and clean up containers",
		RunE:  runStop,
	}

	restartCmd = &cobra.Command{
		Use:   "restart",
		Short: "Restart the CMS system",
		Long:  "Restart the CMS system while preserving data",
		RunE:  runRestart,
	}

	statusCmd = &cobra.Command{
		Use:   "status",
		Short: "Check CMS system status",
		Long:  "Check the current status of the CMS system",
		RunE:  runStatus,
	}
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	// Start command flags
	startCmd.Flags().IntVarP(&port, "port", "p", 80, "Port to run the CMS on")
	startCmd.Flags().StringVarP(&dataDir, "data-dir", "d", DataDir, "Data directory for CMS")
	startCmd.MarkFlagRequired("port")

	// Add commands
	rootCmd.AddCommand(startCmd, stopCmd, restartCmd, statusCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runStart(cmd *cobra.Command, args []string) error {
	fmt.Println("🚀 CMS Starter - Starting your CMS system...")

	starter := &CMSStarter{
		dataDir: dataDir,
		port:    port,
	}

	if err := starter.Start(); err != nil {
		return fmt.Errorf("Failed to start CMS: %v", err)
	}

	fmt.Println("✅ CMS started successfully!")
	fmt.Printf("📊 Dashboard: http://localhost:%d\n", starter.port)
	fmt.Printf("📁 Data directory: %s\n", starter.dataDir)
	fmt.Println("\nPress Ctrl+C to stop the CMS")

	// Keep the process running
	select {}
}

func runStop(cmd *cobra.Command, args []string) error {
	fmt.Println("🛑 Stopping CMS system...")

	starter := &CMSStarter{
		dataDir: dataDir,
		port:    port,
	}

	if err := starter.checkDocker(); err != nil {
		return err
	}

	if err := starter.stopCMSContainer(); err != nil {
		return fmt.Errorf("Failed to stop CMS: %v", err)
	}

	fmt.Println("✅ CMS stopped successfully!")
	return nil
}

func runRestart(cmd *cobra.Command, args []string) error {
	fmt.Println("🔄 Restarting CMS system...")

	starter := &CMSStarter{
		dataDir: dataDir,
		port:    port,
	}

	if err := starter.checkDocker(); err != nil {
		return err
	}

	// Stop existing container without removing volumes
	if err := starter.stopCMSContainerPreserveData(); err != nil {
		return fmt.Errorf("Failed to stop CMS: %v", err)
	}

	// Start the CMS container
	if err := starter.startCMSContainer(); err != nil {
		return fmt.Errorf("Failed to start CMS: %v", err)
	}

	fmt.Println("✅ CMS restarted successfully!")
	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	fmt.Println("📊 Checking CMS system status...")

	starter := &CMSStarter{
		dataDir: dataDir,
		port:    port,
	}

	if err := starter.checkDocker(); err != nil {
		return err
	}

	status, err := starter.getCMSStatus()
	if err != nil {
		return fmt.Errorf("Failed to get CMS status: %v", err)
	}

	fmt.Printf("Status: %s\n", status)
	return nil
}

func (s *CMSStarter) Start() error {
	// Step 1: Check if Docker is installed and running
	if err := s.checkDocker(); err != nil {
		return fmt.Errorf("Docker check failed: %v", err)
	}

	// Step 2: Set up host requirements
	if err := s.setupHostRequirements(); err != nil {
		return fmt.Errorf("Host setup failed: %v", err)
	}

	// Step 3: Create data directory
	if err := s.createDataDirectory(); err != nil {
		return fmt.Errorf("Data directory creation failed: %v", err)
	}

	// Step 4: Start CMS container
	if err := s.startCMSContainer(); err != nil {
		return fmt.Errorf("CMS container start failed: %v", err)
	}

	return nil
}

func (s *CMSStarter) checkDocker() error {
	if verbose {
		fmt.Println("🔍 Checking Docker installation...")
	}

	// Check if docker command exists
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("Docker is not installed. Please install Docker first: https://docs.docker.com/get-docker/")
	}

	// Test Docker daemon
	cmd := exec.Command("docker", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Docker daemon is not running. Please start Docker first")
	}

	// Initialize Docker client with compatible API version
	dockerClient, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
		client.WithHost("unix:///var/run/docker.sock"),
	)
	if err != nil {
		return fmt.Errorf("Failed to connect to Docker: %v", err)
	}

	s.dockerClient = dockerClient
	if verbose {
		fmt.Println("✅ Docker is ready")
	}
	return nil
}

func (s *CMSStarter) setupHostRequirements() error {
	if verbose {
		fmt.Println("🔧 Setting up host requirements...")
	}

	// Create firecracker directory with proper permissions
	if err := os.MkdirAll(FirecrackerDir, 0777); err != nil {
		return fmt.Errorf("Failed to create firecracker directory: %v", err)
	}

	// Set permissions (this might require sudo on some systems)
	if runtime.GOOS == "linux" {
		cmd := exec.Command("chmod", "777", FirecrackerDir)
		if err := cmd.Run(); err != nil {
			fmt.Printf("⚠️  Warning: Could not set permissions on %s. You may need to run with sudo.\n", FirecrackerDir)
		}

		// Also try to set ownership to current user
		cmd = exec.Command("chown", "-R", "1000:1000", FirecrackerDir)
		cmd.Run() // Ignore errors for this
	}

	if verbose {
		fmt.Println("✅ Host requirements configured")
	}
	return nil
}

func (s *CMSStarter) createDataDirectory() error {
	if verbose {
		fmt.Println("📁 Creating data directory...")
	}

	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return fmt.Errorf("Failed to create data directory: %v", err)
	}

	// Create subdirectories
	subdirs := []string{"plugins", "uploads", "logs"}
	for _, subdir := range subdirs {
		path := filepath.Join(s.dataDir, subdir)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("Failed to create subdirectory %s: %v", subdir, err)
		}
	}

	if verbose {
		fmt.Printf("✅ Data directory created: %s\n", s.dataDir)
	}
	return nil
}

func (s *CMSStarter) startCMSContainer() error {
	fmt.Println("🐳 Starting CMS container...")

	// Stop any existing CMS container and remove it
	s.stopExistingContainer()

	// Create container configuration
	containerConfig := &container.Config{
		Image: CMSImageName,
		ExposedPorts: nat.PortSet{
			nat.Port(fmt.Sprintf("%d/tcp", s.port)): struct{}{},
		},
		Env: []string{
			fmt.Sprintf("CMS_PORT=%d", s.port),
			"FIRECRACKER_PATH=/usr/local/bin/firecracker",
			"KERNEL_PATH=/opt/kernel/vmlinux",
		},
		Cmd: []string{"./cms"},
	}

	// Create host configuration
	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			nat.Port(fmt.Sprintf("%d/tcp", s.port)): []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: strconv.Itoa(s.port),
				},
			},
		},
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
			},
			{
				Type:   mount.TypeBind,
				Source: s.getAbsoluteDataDir(),
				Target: "/app/data",
			},
			{
				Type:   mount.TypeBind,
				Source: s.getAbsoluteFirecrackerDir(),
				Target: "/tmp/firecracker",
			},
			{
				Type:   mount.TypeBind,
				Source: "/dev/kvm",
				Target: "/dev/kvm",
			},
		},
		Privileged: true, // Required for Firecracker
		CapAdd: []string{
			"SYS_ADMIN", // Required for Firecracker
			"NET_ADMIN",
		},
	}

	// Create the container
	resp, err := s.dockerClient.ContainerCreate(
		context.Background(),
		containerConfig,
		hostConfig,
		nil,
		nil,
		"cms-container",
	)
	if err != nil {
		return fmt.Errorf("Failed to create container: %v", err)
	}

	// Start the container
	if err := s.dockerClient.ContainerStart(
		context.Background(),
		resp.ID,
		container.StartOptions{},
	); err != nil {
		return fmt.Errorf("Failed to start container: %v", err)
	}

	fmt.Printf("✅ CMS container started with ID: %s\n", resp.ID[:12])

	// Wait a moment for the container to fully start
	time.Sleep(3 * time.Second)

	// Check if container is running
	inspect, err := s.dockerClient.ContainerInspect(context.Background(), resp.ID)
	if err != nil {
		return fmt.Errorf("Failed to inspect container: %v", err)
	}

	if !inspect.State.Running {
		// Get container logs for debugging
		logs, err := s.dockerClient.ContainerLogs(
			context.Background(),
			resp.ID,
			container.LogsOptions{ShowStdout: true, ShowStderr: true},
		)
		if err == nil {
			defer logs.Close()
			buf := make([]byte, 1024)
			n, _ := logs.Read(buf)
			if n > 0 {
				fmt.Printf("Container logs: %s\n", string(buf[:n]))
			}
		}
		return fmt.Errorf("Container failed to start")
	}

	return nil
}

func (s *CMSStarter) stopCMSContainer() error {
	containers, err := s.dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("Failed to list containers: %v", err)
	}

	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, "cms-container") {
				fmt.Printf("🛑 Stopping container: %s\n", c.ID[:12])
				if err := s.dockerClient.ContainerStop(context.Background(), c.ID, container.StopOptions{}); err != nil {
					return fmt.Errorf("Failed to stop container: %v", err)
				}
				if err := s.dockerClient.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{RemoveVolumes: false}); err != nil {
					return fmt.Errorf("Failed to remove container: %v", err)
				}
				return nil
			}
		}
	}

	fmt.Println("ℹ️  No CMS container found to stop")
	return nil
}

func (s *CMSStarter) stopCMSContainerPreserveData() error {
	containers, err := s.dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("Failed to list containers: %v", err)
	}

	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, "cms-container") {
				fmt.Printf("🛑 Stopping container: %s\n", c.ID[:12])
				if err := s.dockerClient.ContainerStop(context.Background(), c.ID, container.StopOptions{}); err != nil {
					return fmt.Errorf("Failed to stop container: %v", err)
				}
				// Don't remove the container - just stop it to preserve data
				return nil
			}
		}
	}

	fmt.Println("ℹ️  No CMS container found to stop")
	return nil
}

func (s *CMSStarter) getAbsoluteDataDir() string {
	absPath, err := filepath.Abs(s.dataDir)
	if err != nil {
		// Fallback to current directory if absolute path fails
		return filepath.Join(".", s.dataDir)
	}
	return absPath
}

func (s *CMSStarter) getAbsoluteFirecrackerDir() string {
	absPath, err := filepath.Abs(FirecrackerDir)
	if err != nil {
		// Fallback to current directory if absolute path fails
		return filepath.Join(".", FirecrackerDir)
	}
	return absPath
}

func (s *CMSStarter) getCMSStatus() (string, error) {
	containers, err := s.dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("Failed to list containers: %v", err)
	}

	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, "cms-container") {
				return c.State, nil
			}
		}
	}

	return "not_found", nil
}

func (s *CMSStarter) stopExistingContainer() {
	containers, err := s.dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return
	}

	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, "cms-container") {
				if verbose {
					fmt.Printf("🛑 Stopping existing container: %s\n", c.ID[:12])
				}
				s.dockerClient.ContainerStop(context.Background(), c.ID, container.StopOptions{})
				s.dockerClient.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{RemoveVolumes: false})
				break
			}
		}
	}
}
