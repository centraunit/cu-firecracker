package main

import (
	"context"
	"encoding/json"
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
	CMSImageName     = "issaprodev/cu-cms:latest"
	DataDir          = "./cms-data"
	FirecrackerDir   = "./firecracker-tmp"
	CMSContainerName = "cu-firecracker-cms"
)

type CMSStarter struct {
	dockerClient *client.Client
	dataDir      string
	port         int
}

type PluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Description string `json:"description"`
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
- Plugin development and export tools
- Cross-platform support

Example usage:
  cms-starter start                    # Start on default port 80
  cms-starter start --port 8080       # Start on port 8080
  cms-starter start --data-dir ./my-data  # Use custom data directory
  cms-starter stop                     # Stop the CMS
  cms-starter status                   # Check CMS status
  cms-starter plugin build --plugin plugins/my-plugin  # Build a plugin
  cms-starter plugin build --plugin plugins/my-plugin --size 2000  # Build with custom size`,
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

	pluginCmd = &cobra.Command{
		Use:   "plugin",
		Short: "Plugin management commands",
		Long:  "Commands for building and managing plugins",
	}

	buildCmd = &cobra.Command{
		Use:   "build",
		Short: "Build a plugin from Dockerfile",
		Long:  "Build a plugin from Dockerfile and export to ext4 filesystem",
		RunE:  runPluginBuild,
	}
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	// Start command flags
	startCmd.Flags().IntVarP(&port, "port", "p", 80, "Port to run the CMS on")
	startCmd.Flags().StringVarP(&dataDir, "data-dir", "d", DataDir, "Data directory for CMS")

	// Plugin build command flags
	buildCmd.Flags().String("plugin", "", "Plugin directory path")
	buildCmd.Flags().Int("size", 1000, "Filesystem size in MB (default: 1000)")
	buildCmd.MarkFlagRequired("plugin")

	// Add commands
	pluginCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(startCmd, stopCmd, restartCmd, statusCmd, pluginCmd)
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

func runPluginBuild(cmd *cobra.Command, args []string) error {
	pluginDir, _ := cmd.Flags().GetString("plugin")
	sizeMB, _ := cmd.Flags().GetInt("size")

	if pluginDir == "" {
		return fmt.Errorf("plugin directory is required")
	}

	fmt.Printf("🔨 Building plugin from directory: %s\n", pluginDir)

	// Validate plugin directory
	if err := validatePluginDirectory(pluginDir); err != nil {
		return fmt.Errorf("plugin validation failed: %v", err)
	}

	// Read plugin manifest
	manifest, err := readPluginManifest(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to read plugin manifest: %v", err)
	}

	// Create build directory and set output path
	buildDir := filepath.Join(pluginDir, "build")
	outputPath := filepath.Join(buildDir, fmt.Sprintf("%s.ext4", manifest.Name))

	fmt.Printf("Building plugin: %s (v%s)\n", manifest.Name, manifest.Version)
	fmt.Printf("Filesystem size: %d MB\n", sizeMB)

	// Step 1: Build Docker image
	imageName, err := buildDockerImage(pluginDir, manifest)
	if err != nil {
		return fmt.Errorf("failed to build Docker image: %v", err)
	}

	// Step 2: Export container and create ext4 filesystem
	if err := exportToExt4(imageName, outputPath, sizeMB); err != nil {
		// Clean up Docker image
		cleanupDockerImage(imageName)
		return fmt.Errorf("failed to export to ext4: %v", err)
	}

	// Step 3: Clean up Docker image
	if err := cleanupDockerImage(imageName); err != nil {
		fmt.Printf("⚠️  Warning: Failed to clean up Docker image: %v\n", err)
	}

	fmt.Printf("✅ Plugin exported successfully to: %s\n", outputPath)
	return nil
}

func exportToExt4(imageName, outputPath string, sizeMB int) error {
	// Create output directory
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Generate unique container name
	containerName := fmt.Sprintf("export-%s", sanitizeImageName(imageName))

	fmt.Printf("Exporting container filesystem to ext4: %s\n", outputPath)

	// Create container from image
	createCmd := exec.Command("docker", "create", "--name", containerName, imageName)
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create container: %v", err)
	}

	// Clean up container when done
	defer func() {
		exec.Command("docker", "rm", containerName).Run()
	}()

	// Create empty ext4 filesystem
	fmt.Printf("Creating %d MB ext4 filesystem...\n", sizeMB)
	cmd := exec.Command("dd", "if=/dev/zero", fmt.Sprintf("of=%s", outputPath), "bs=1M", fmt.Sprintf("count=%d", sizeMB))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create empty file: %v", err)
	}

	// Format as ext4
	cmd = exec.Command("mkfs.ext4", "-F", outputPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to format ext4: %v", err)
	}

	// Create temporary mount point
	tempDir, err := os.MkdirTemp("", "plugin-mount-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Mount the ext4 filesystem
	cmd = exec.Command("sudo", "mount", "-o", "loop", outputPath, tempDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to mount ext4: %v", err)
	}

	// Ensure umount happens
	defer func() {
		exec.Command("sudo", "umount", tempDir).Run()
	}()

	// Export container filesystem to tar and extract to mounted ext4
	exportCmd := exec.Command("docker", "export", containerName)
	extractCmd := exec.Command("sudo", "tar", "-xf", "-", "-C", tempDir)

	// Connect the commands with a pipe
	extractCmd.Stdin, _ = exportCmd.StdoutPipe()

	// Start the extract command
	if err := extractCmd.Start(); err != nil {
		return fmt.Errorf("failed to start extract command: %v", err)
	}

	// Run the export command
	if err := exportCmd.Run(); err != nil {
		return fmt.Errorf("failed to export container: %v", err)
	}

	// Wait for extract to complete
	if err := extractCmd.Wait(); err != nil {
		return fmt.Errorf("failed to extract to ext4: %v", err)
	}

	fmt.Printf("Successfully created ext4 filesystem: %s\n", outputPath)
	return nil
}

func validatePluginDirectory(pluginDir string) error {
	requiredFiles := []string{"plugin.json", "Dockerfile"}

	for _, file := range requiredFiles {
		filePath := filepath.Join(pluginDir, file)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return fmt.Errorf("required file not found: %s", file)
		}
	}

	return nil
}

func readPluginManifest(pluginDir string) (*PluginManifest, error) {
	manifestPath := filepath.Join(pluginDir, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read plugin.json: %v", err)
	}

	var manifest PluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse plugin.json: %v", err)
	}

	return &manifest, nil
}

func buildDockerImage(pluginDir string, manifest *PluginManifest) (string, error) {
	imageName := fmt.Sprintf("plugin-%s-%s", sanitizeImageName(manifest.Name), manifest.Version)

	fmt.Printf("Building Docker image: %s\n", imageName)

	cmd := exec.Command("docker", "build", "-t", imageName, pluginDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build Docker image: %v", err)
	}

	return imageName, nil
}

func cleanupDockerImage(imageName string) error {
	cmd := exec.Command("docker", "rmi", imageName)
	return cmd.Run()
}

func sanitizeImageName(name string) string {
	// Replace invalid characters for Docker image names
	// Docker image names can only contain: [a-z0-9._-]
	// This is a simple sanitization
	result := ""
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '.' || char == '_' || char == '-' {
			result += string(char)
		} else {
			result += "-"
		}
	}
	return result
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
		CMSContainerName,
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
			// Check for our specific CMS container
			if strings.Contains(name, CMSContainerName) {
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
			// Check for our specific CMS container
			if strings.Contains(name, CMSContainerName) {
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
			// Check for our specific CMS container
			if strings.Contains(name, CMSContainerName) {
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
			// Check for our specific CMS container
			if strings.Contains(name, CMSContainerName) {
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
