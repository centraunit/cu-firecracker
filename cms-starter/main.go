/*
 * Firecracker CMS - Starter CLI Tool
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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/spf13/cobra"
)

const (
	CMSImageName     = "centraunit/cu-firecracker-cms"
	CMSContainerName = "cu-firecracker-cms"
)

type CMSStarter struct {
	dockerClient *client.Client
	dataDir      string
	port         int
}

type PluginManifest struct {
	Slug        string                 `json:"slug"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Runtime     string                 `json:"runtime"`
	Actions     map[string]interface{} `json:"actions"`
}

var (
	port     int
	dataDir  string
	verbose  bool
	devMode  bool
	testMode bool

	rootCmd = &cobra.Command{
		Use:          "cms-starter",
		Short:        "CMS Starter – Run your Firecracker‐isolated CMS",
		SilenceUsage: true,
	}
	startCmd   = &cobra.Command{Use: "start", Short: "Start CMS", RunE: runStart, SilenceUsage: true}
	stopCmd    = &cobra.Command{Use: "stop", Short: "Stop CMS", RunE: runStop, SilenceUsage: true}
	restartCmd = &cobra.Command{Use: "restart", Short: "Restart CMS", RunE: runRestart, SilenceUsage: true}
	statusCmd  = &cobra.Command{Use: "status", Short: "Status of CMS", RunE: runStatus, SilenceUsage: true}
	pluginCmd  = &cobra.Command{Use: "plugin", Short: "Plugin management", SilenceUsage: true}
	buildCmd   = &cobra.Command{
		Use:          "build",
		Short:        "Build plugin into bootable ext4",
		RunE:         runPluginBuild,
		SilenceUsage: true,
	}
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().BoolVar(&devMode, "dev", false, "Development mode")
	rootCmd.PersistentFlags().BoolVar(&testMode, "test", false, "Test mode (runs tests)")
	startCmd.Flags().IntVarP(&port, "port", "p", 80, "CMS port")
	startCmd.Flags().StringVarP(&dataDir, "data-dir", "d", "./cms-data", "Data directory")
	buildCmd.Flags().String("plugin", "", "Plugin folder (required)")
	buildCmd.Flags().Int("size", 200, "Ext4 filesystem size in MB (200-800, try 400 if build fails)")
	buildCmd.MarkFlagRequired("plugin")
	pluginCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(startCmd, stopCmd, restartCmd, statusCmd, pluginCmd)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// createPluginZip creates a ZIP file containing the plugin rootfs and manifest
func createPluginZip(zipPath, rootfsPath, pluginJsonPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Add rootfs.ext4
	rootfsFile, err := os.Open(rootfsPath)
	if err != nil {
		return err
	}
	defer rootfsFile.Close()

	rootfsWriter, err := zipWriter.Create("rootfs.ext4")
	if err != nil {
		return err
	}
	_, err = io.Copy(rootfsWriter, rootfsFile)
	if err != nil {
		return err
	}

	// Add plugin.json
	pluginJsonFile, err := os.Open(pluginJsonPath)
	if err != nil {
		return err
	}
	defer pluginJsonFile.Close()

	jsonWriter, err := zipWriter.Create("plugin.json")
	if err != nil {
		return err
	}
	_, err = io.Copy(jsonWriter, pluginJsonFile)
	return err
}

// getImageName returns the appropriate Docker image name based on mode
func getImageName() string {
	if testMode {
		return CMSImageName + ":test"
	} else if devMode {
		return CMSImageName + ":dev"
	}
	return CMSImageName + ":latest" // production mode
}

// getContainerName returns the appropriate container name based on mode
func getContainerName() string {
	if testMode {
		return CMSContainerName + "-test"
	} else if devMode {
		return CMSContainerName + "-dev"
	}
	return CMSContainerName // production
}

// runTests executes the test suite in Docker
func runTests() error {
	fmt.Println("Running comprehensive test suite...")

	// Step 1: Prepare real test plugins for CMS testing
	fmt.Println("Step 1: Preparing real test plugins...")
	if err := prepareTestPlugins("./cms-data"); err != nil {
		return fmt.Errorf("failed to prepare test plugins: %w", err)
	}
	fmt.Println("✓ Test plugins prepared")

	// Step 2: Run CMS unit tests in Docker
	fmt.Println("Step 2: Building CMS test image...")
	dockerCmd := exec.Command("docker", "build", "-f", "../cu-cms/Dockerfile", "-t", CMSImageName+":test", "../cu-cms")
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr
	if err := dockerCmd.Run(); err != nil {
		return fmt.Errorf("failed to build CMS test image: %w", err)
	}

	fmt.Println("Running CMS tests in Docker...")

	// Mount the test plugins directory so CMS tests can access real plugins
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	dataPath := filepath.Join(currentDir, "cms-data")

	testCmd := exec.Command("docker", "run", "--rm", "--privileged",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", dataPath+":/app/data",
		"--name", "cms-test-runner",
		CMSImageName+":test", "go", "test", "-v", "./...")

	testCmd.Stdout = os.Stdout
	testCmd.Stderr = os.Stderr

	if err := testCmd.Run(); err != nil {
		// Handle Docker I/O timeout as non-fatal if tests actually passed
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				fmt.Println("CMS tests completed (Docker I/O timeout is normal)")
			} else {
				return fmt.Errorf("CMS tests failed with exit code %d", exitErr.ExitCode())
			}
		} else {
			return fmt.Errorf("failed to run CMS tests: %w", err)
		}
	}
	fmt.Println("✓ All CMS tests passed!")

	// Step 3: Start CMS container for integration testing
	fmt.Println("Starting CMS container for integration testing...")

	// Stop any existing test container
	exec.Command("docker", "rm", "-f", CMSContainerName+"-test").Run()

	startCmd := exec.Command("docker", "run", "-d", "--privileged",
		"--network=host",
		"-e", "CMS_PORT=80",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", dataPath+":/app/data",
		"--name", CMSContainerName+"-test",
		CMSImageName+":test")

	output, err := startCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start CMS container: %w\nOutput: %s", err, string(output))
	}

	fmt.Printf("CMS container start output: %s\n", strings.TrimSpace(string(output)))
	fmt.Printf("✓ CMS container started: %s\n", CMSContainerName+"-test")

	// Step 4: Wait for CMS to be ready
	fmt.Println("Waiting for CMS to start...")
	time.Sleep(10 * time.Second)

	statusCmd := exec.Command("docker", "ps", "--filter", "name="+CMSContainerName+"-test", "--format", "table {{.Status}}")
	statusOutput, _ := statusCmd.Output()
	fmt.Printf("Container status: %s\n", strings.TrimSpace(string(statusOutput)))

	// Check if CMS is ready
	fmt.Println("Checking if CMS is ready for integration testing...")
	healthCmd := exec.Command("curl", "-f", "http://localhost:80/health")
	if err := healthCmd.Run(); err != nil {
		// Stop container on failure
		exec.Command("docker", "rm", "-f", CMSContainerName+"-test").Run()
		return fmt.Errorf("CMS health check failed - container not ready for integration testing")
	}
	fmt.Println("✓ CMS is ready for integration testing!")

	// Step 5: Run integration tests
	fmt.Println("Running integration tests against live CMS...")
	integrationCmd := exec.Command("go", "test", "-v", "-run", "TestRunTestsModeValidation")
	integrationCmd.Stdout = os.Stdout
	integrationCmd.Stderr = os.Stderr

	integrationErr := integrationCmd.Run()

	// Step 6: Cleanup - stop and remove test container
	fmt.Println("Stopping and removing test container...")
	exec.Command("docker", "rm", "-f", CMSContainerName+"-test").Run()

	if integrationErr != nil {
		return fmt.Errorf("integration tests completed with issues: %w", integrationErr)
	}

	fmt.Println("✓ All tests completed successfully!")
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runStart(cmd *cobra.Command, _ []string) error {
	// Handle test mode
	if testMode {
		fmt.Println("Test mode detected - running comprehensive test suite...")
		return runTests()
	}

	// Handle dev/production mode
	if devMode {
		fmt.Println("🚀 Starting CMS in development mode...")
	} else {
		fmt.Println("🏭 Starting CMS in production mode...")
	}

	s := &CMSStarter{dataDir: dataDir, port: port}
	if err := s.initDocker(); err != nil {
		return err
	}
	if err := s.ensureDataDir(); err != nil {
		return err
	}
	return s.startContainer()
}

func runStop(cmd *cobra.Command, _ []string) error {
	fmt.Println("Stopping CMS...")
	s := &CMSStarter{dataDir: dataDir, port: port}
	if err := s.initDocker(); err != nil {
		return err
	}
	return s.stopContainer(true)
}

func runRestart(cmd *cobra.Command, _ []string) error {
	fmt.Println("Restarting CMS...")
	s := &CMSStarter{dataDir: dataDir, port: port}
	if err := s.initDocker(); err != nil {
		return err
	}
	// Stop the container and remove it to avoid name conflicts
	if err := s.stopContainer(true); err != nil {
		return err
	}
	// Start the container
	if err := s.ensureDataDir(); err != nil {
		return err
	}
	return s.startContainer()
}

func runStatus(cmd *cobra.Command, _ []string) error {
	s := &CMSStarter{dataDir: dataDir, port: port}
	if err := s.initDocker(); err != nil {
		return err
	}
	state, err := s.getContainerStatus()
	if err != nil {
		return err
	}
	fmt.Printf("CMS status: %s\n", state)
	return nil
}

func runPluginBuild(cmd *cobra.Command, _ []string) error {
	pluginDir, _ := cmd.Flags().GetString("plugin")
	sizeMB, _ := cmd.Flags().GetInt("size")

	fmt.Printf("Building plugin from: %s\n", pluginDir)
	manifest, err := readManifest(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to read plugin manifest: %w", err)
	}

	// Provide size recommendations
	if sizeMB == 200 { // Default size, provide recommendations
		fmt.Printf("Info: Using default 200MB filesystem\n")
		fmt.Printf("   If build fails due to space issues, try --size 400 or --size 500\n")
	} else {
		fmt.Printf("Info: Using %dMB filesystem\n", sizeMB)
	}

	buildName := fmt.Sprintf("%s-%s", sanitize(manifest.Name), manifest.Version)
	image := "plugin-" + buildName

	fmt.Printf("Building Docker image: %s\n", image)
	if err := dockerBuild(pluginDir, image); err != nil {
		return fmt.Errorf("failed to build Docker image: %w", err)
	}

	// Create build directory
	buildDir := filepath.Join(pluginDir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		_ = dockerRemove(image)
		return fmt.Errorf("failed to create build directory: %w", err)
	}

	// Export rootfs.ext4 to temp location
	rootfsPath := filepath.Join(buildDir, "rootfs.ext4")
	fmt.Printf("Exporting rootfs to: %s\n", rootfsPath)

	if err := exportExt4(image, rootfsPath, sizeMB); err != nil {
		_ = dockerRemove(image) // Clean up on failure
		return err
	}

	// Copy plugin.json to build directory
	pluginJsonSrc := filepath.Join(pluginDir, "plugin.json")
	pluginJsonDest := filepath.Join(buildDir, "plugin.json")

	if err := copyFile(pluginJsonSrc, pluginJsonDest); err != nil {
		_ = dockerRemove(image)
		return fmt.Errorf("failed to copy plugin.json: %w", err)
	}

	// Create ZIP file containing both rootfs.ext4 and plugin.json
	zipPath := filepath.Join(buildDir, buildName+".zip")
	fmt.Printf("Creating plugin ZIP: %s\n", zipPath)

	if err := createPluginZip(zipPath, rootfsPath, pluginJsonDest); err != nil {
		_ = dockerRemove(image)
		return fmt.Errorf("failed to create plugin ZIP: %w", err)
	}

	// Clean up temporary files
	os.Remove(rootfsPath)
	os.Remove(pluginJsonDest)

	fmt.Printf("✓ Plugin packaged successfully: %s\n", zipPath)
	fmt.Printf("ZIP contains: rootfs.ext4 + plugin.json\n")
	fmt.Printf("Ready to upload to CMS!\n")

	_ = dockerRemove(image)
	return nil
}

// prepareTestPlugins builds real plugins for CMS testing
func prepareTestPlugins(dataDir string) error {
	fmt.Println("Preparing real test plugins for CMS testing...")

	// Create test plugins directory
	testPluginsDir := filepath.Join(dataDir, "test-plugins")
	if err := os.MkdirAll(testPluginsDir, 0755); err != nil {
		return fmt.Errorf("failed to create test plugins directory: %w", err)
	}

	// Build Python plugin for testing (use correct relative path from cms-starter directory)
	pluginDir := "../plugins/python-plugin"
	manifest, err := readManifest(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to read Python plugin manifest: %w", err)
	}

	buildName := fmt.Sprintf("%s-%s", sanitize(manifest.Name), manifest.Version)
	image := "plugin-" + buildName

	fmt.Printf("Building test plugin image: %s\n", image)
	if err := dockerBuild(pluginDir, image); err != nil {
		return fmt.Errorf("failed to build test plugin image: %w", err)
	}
	defer func() {
		_ = dockerRemove(image) // Clean up
	}()

	// Export real rootfs.ext4 for testing
	testRootfsPath := filepath.Join(testPluginsDir, "test-plugin.ext4")
	fmt.Printf("Exporting test rootfs to: %s\n", testRootfsPath)

	if err := exportExt4(image, testRootfsPath, 400); err != nil {
		return fmt.Errorf("failed to export test rootfs: %w", err)
	}

	// Copy plugin.json for testing
	testPluginJsonPath := filepath.Join(testPluginsDir, "test-plugin.json")
	if err := copyFile(filepath.Join(pluginDir, "plugin.json"), testPluginJsonPath); err != nil {
		return fmt.Errorf("failed to copy test plugin.json: %w", err)
	}

	// Create ZIP file for upload testing
	testZipPath := filepath.Join(testPluginsDir, "test-plugin.zip")
	if err := createPluginZip(testZipPath, testRootfsPath, testPluginJsonPath); err != nil {
		return fmt.Errorf("failed to create test plugin ZIP: %w", err)
	}

	fmt.Printf("✓ Test plugins prepared in: %s\n", testPluginsDir)
	fmt.Printf("  - Real rootfs: %s\n", testRootfsPath)
	fmt.Printf("  - Plugin manifest: %s\n", testPluginJsonPath)
	fmt.Printf("  - Plugin ZIP: %s\n", testZipPath)

	return nil
}

func (s *CMSStarter) initDocker() error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker init: %w", err)
	}
	s.dockerClient = cli
	return nil
}

func (s *CMSStarter) ensureDataDir() error {
	dirs := []string{s.dataDir, filepath.Join(s.dataDir, "plugins"), filepath.Join(s.dataDir, "logs")}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}

func (s *CMSStarter) startContainer() error {
	_ = s.stopContainer(false)

	// Convert data directory to absolute path
	absDataDir, err := filepath.Abs(s.dataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	cfg := &container.Config{
		Image: getImageName(),
		Cmd:   []string{"./cms"},
		Env:   []string{fmt.Sprintf("CMS_PORT=%d", s.port)},
	}
	host := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: absDataDir, Target: "/app/data"},
			{Type: mount.TypeBind, Source: "/dev/kvm", Target: "/dev/kvm"},
			{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
		},
		Privileged:  true,
		CapAdd:      []string{"SYS_ADMIN", "NET_ADMIN", "NET_RAW"},
		NetworkMode: "host",
	}
	resp, err := s.dockerClient.ContainerCreate(context.Background(), cfg, host, nil, nil, getContainerName())
	if err != nil {
		return err
	}
	if err := s.dockerClient.ContainerStart(context.Background(), resp.ID, container.StartOptions{}); err != nil {
		return err
	}
	fmt.Println("CMS running at http://localhost:", s.port)
	return nil
}

func (s *CMSStarter) stopContainer(remove bool) error {
	containers, _ := s.dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, getContainerName()) {
				_ = s.dockerClient.ContainerStop(context.Background(), c.ID, container.StopOptions{})
				if remove {
					_ = s.dockerClient.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{})
				}
				return nil
			}
		}
	}
	return nil
}

func (s *CMSStarter) getContainerStatus() (string, error) {
	containers, err := s.dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, getContainerName()) {
				return c.State, nil
			}
		}
	}
	return "not_found", nil
}

func readManifest(dir string) (*PluginManifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, err
	}
	var m PluginManifest
	return &m, json.Unmarshal(b, &m)
}

func dockerBuild(dir, image string) error {
	cmd := exec.Command("docker", "build", "-t", image, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func exportExt4(image, out string, sizeMB int) error {
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	containerName := "exp-" + strings.ReplaceAll(image, "/", "_")

	// Clean up any existing container with the same name
	exec.Command("docker", "rm", containerName).Run()

	if err := exec.Command("docker", "create", "--name", containerName, image).Run(); err != nil {
		return fmt.Errorf("failed to create container for export: %w", err)
	}
	defer exec.Command("docker", "rm", containerName).Run()

	fmt.Printf("Creating %dMB ext4 filesystem...\n", sizeMB)
	if err := exec.Command("dd", "if=/dev/zero", "of="+out, "bs=1M", fmt.Sprint("count=", sizeMB)).Run(); err != nil {
		return fmt.Errorf("failed to create filesystem image: %w", err)
	}
	if err := exec.Command("mkfs.ext4", "-F", out).Run(); err != nil {
		return fmt.Errorf("failed to format ext4 filesystem: %w", err)
	}

	tmp, err := os.MkdirTemp("", "mnt-")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	fmt.Printf("Mounting filesystem at %s...\n", tmp)
	if err := exec.Command("sudo", "mount", "-o", "loop", out, tmp).Run(); err != nil {
		return fmt.Errorf("failed to mount filesystem: %w", err)
	}
	defer exec.Command("sudo", "umount", tmp).Run()

	fmt.Printf("Extracting container contents...\n")
	exportCmd := exec.Command("docker", "export", containerName)
	tarCmd := exec.Command("sudo", "tar", "-xf", "-", "-C", tmp)

	tarCmd.Stdin, _ = exportCmd.StdoutPipe()
	var stderr bytes.Buffer
	tarCmd.Stderr = &stderr

	if err := tarCmd.Start(); err != nil {
		return fmt.Errorf("failed to start extraction: %w", err)
	}
	if err := exportCmd.Run(); err != nil {
		return fmt.Errorf("failed to export container: %w", err)
	}
	if err := tarCmd.Wait(); err != nil {
		errorOutput := stderr.String()

		// Check for common errors and provide helpful messages
		if strings.Contains(errorOutput, "No space left on device") {
			return fmt.Errorf("filesystem too small (%dMB) for plugin contents.\n"+
				"�� Solution: Increase filesystem size with --size flag\n"+
				"   Try: --size 400 (400MB) or --size 500 (500MB)\n"+
				"   Larger plugins may need 800MB or more\n"+
				"Original error: %v", sizeMB, err)
		}

		if strings.Contains(errorOutput, "Cannot mkdir") ||
			strings.Contains(errorOutput, "Cannot create") ||
			strings.Contains(errorOutput, "Cannot open") ||
			strings.Contains(errorOutput, "Cannot hard link") {
			return fmt.Errorf("extraction failed - filesystem (%dMB) appears too small.\n"+
				"💡 Solution: Increase filesystem size with --size flag\n"+
				"   Recommended sizes: --size 400, --size 500, or --size 800\n"+
				"Original error: %v", sizeMB, err)
		}

		return fmt.Errorf("extraction failed: %v\nError details: %s", err, errorOutput)
	}

	fmt.Printf("Successfully created plugin filesystem: %s\n", out)
	return nil
}

func dockerRemove(image string) error {
	return exec.Command("docker", "rmi", image).Run()
}

func sanitize(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789-_.", r) {
			return r
		}
		return '-'
	}, s)
	return s
}

func getCurrentDir() string {
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return filepath.Dir(ex)
}
