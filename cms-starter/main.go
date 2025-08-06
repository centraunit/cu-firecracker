package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/spf13/cobra"
)

const (
	CMSImageName     = "issaprodev/cu-cms:latest"
	CMSContainerName = "cu-firecracker-cms"
)

type CMSStarter struct {
	dockerClient *client.Client
	dataDir      string
	port         int
}

type PluginManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

var (
	port    int
	dataDir string
	verbose bool

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
	startCmd.Flags().IntVarP(&port, "port", "p", 80, "CMS port")
	startCmd.Flags().StringVarP(&dataDir, "data-dir", "d", "./cms-data", "Data directory")
	buildCmd.Flags().String("plugin", "", "Plugin folder (required)")
	buildCmd.Flags().Int("size", 200, "Ext4 filesystem size in MB (200-800, try 400 if build fails)")
	buildCmd.MarkFlagRequired("plugin")
	pluginCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(startCmd, stopCmd, restartCmd, statusCmd, pluginCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runStart(cmd *cobra.Command, _ []string) error {
	fmt.Println("Starting CMS...")
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
		fmt.Printf("ℹ️  Info: Using default 200MB filesystem\n")
		fmt.Printf("   If build fails due to space issues, try --size 400 or --size 500\n")
	} else {
		fmt.Printf("ℹ️  Info: Using %dMB filesystem\n", sizeMB)
	}

	buildName := fmt.Sprintf("%s-%s", sanitize(manifest.Name), manifest.Version)
	image := "plugin-" + buildName

	fmt.Printf("Building Docker image: %s\n", image)
	if err := dockerBuild(pluginDir, image); err != nil {
		return fmt.Errorf("failed to build Docker image: %w", err)
	}

	outPath := filepath.Join(pluginDir, "build", buildName+".ext4")
	fmt.Printf("Exporting to: %s\n", outPath)

	if err := exportExt4(image, outPath, sizeMB); err != nil {
		_ = dockerRemove(image) // Clean up on failure
		return err
	}

	fmt.Printf("✅ Plugin built successfully: %s\n", outPath)
	_ = dockerRemove(image)
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
		Image:        CMSImageName,
		Cmd:          []string{"./cms"},
		Env:          []string{fmt.Sprintf("CMS_PORT=%d", s.port)},
		ExposedPorts: nat.PortSet{nat.Port(fmt.Sprintf("%d/tcp", s.port)): {}},
	}
	host := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: absDataDir, Target: "/app/data"},
			{Type: mount.TypeBind, Source: "/dev/kvm", Target: "/dev/kvm"},
			{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
		},
		Privileged: true,
		CapAdd:     []string{"SYS_ADMIN", "NET_ADMIN", "NET_RAW"},
		PortBindings: nat.PortMap{
			nat.Port(fmt.Sprintf("%d/tcp", s.port)): {{HostPort: fmt.Sprintf("%d", s.port)}},
		},
	}
	resp, err := s.dockerClient.ContainerCreate(context.Background(), cfg, host, nil, nil, CMSContainerName)
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
			if strings.Contains(name, CMSContainerName) {
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
			if strings.Contains(name, CMSContainerName) {
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
				"💡 Solution: Increase filesystem size with --size flag\n"+
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
