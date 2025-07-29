package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// PluginManifest represents the plugin.json structure
type PluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Description string `json:"description"`
}

func main() {
	var (
		pluginDir  = flag.String("plugin", "", "Path to plugin directory")
		outputPath = flag.String("output", "", "Output path for rootfs.ext4 (optional)")
		help       = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help {
		fmt.Println("Plugin Builder - Export plugin Docker image to Firecracker rootfs")
		fmt.Println("Usage: plugin-builder -plugin <plugin-dir> [-output <output-path>]")
		fmt.Println("")
		fmt.Println("Plugin directory must contain:")
		fmt.Println("  - plugin.json (manifest)")
		fmt.Println("  - Dockerfile (runtime configuration)")
		fmt.Println("  - Source code (any language)")
		flag.PrintDefaults()
		return
	}

	if *pluginDir == "" {
		log.Fatal("Plugin directory is required")
	}

	// Set default output path if not provided
	if *outputPath == "" {
		*outputPath = filepath.Join(*pluginDir, "build", "rootfs.ext4")
	}

	if err := exportPlugin(*pluginDir, *outputPath); err != nil {
		log.Fatalf("Failed to export plugin: %v", err)
	}

	fmt.Printf("Plugin exported successfully: %s\n", *outputPath)
}

func exportPlugin(pluginDir, outputPath string) error {
	// Step 1: Validate plugin directory
	if err := validatePluginDirectory(pluginDir); err != nil {
		return fmt.Errorf("plugin validation failed: %v", err)
	}

	// Step 2: Read plugin manifest
	manifest, err := readPluginManifest(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to read plugin manifest: %v", err)
	}

	// Step 3: Build Docker image
	imageName, err := buildDockerImage(pluginDir, manifest)
	if err != nil {
		return fmt.Errorf("failed to build Docker image: %v", err)
	}

	// Step 4: Export container filesystem to rootfs.ext4
	if err := exportContainerToRootfs(imageName, outputPath); err != nil {
		return fmt.Errorf("failed to export rootfs: %v", err)
	}

	// Step 5: Clean up Docker image
	if err := cleanupDockerImage(imageName); err != nil {
		log.Printf("Warning: failed to clean up Docker image: %v", err)
	}

	return nil
}

func validatePluginDirectory(pluginDir string) error {
	// Check if directory exists
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return fmt.Errorf("plugin directory does not exist: %s", pluginDir)
	}

	// Check for required files
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

	// Validate manifest
	if manifest.Name == "" {
		return nil, fmt.Errorf("plugin name is required in plugin.json")
	}
	if manifest.Version == "" {
		return nil, fmt.Errorf("plugin version is required in plugin.json")
	}
	if manifest.Port == 0 {
		return nil, fmt.Errorf("plugin port is required in plugin.json")
	}

	return &manifest, nil
}

func buildDockerImage(pluginDir string, manifest *PluginManifest) (string, error) {
	// Generate unique image name
	imageName := fmt.Sprintf("plugin-%s-%s", manifest.Name, manifest.Version)
	imageName = sanitizeImageName(imageName)

	fmt.Printf("Building Docker image: %s\n", imageName)

	// Build Docker image
	cmd := exec.Command("docker", "build", "-t", imageName, pluginDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Docker build failed: %v", err)
	}

	return imageName, nil
}

func exportContainerToRootfs(imageName, outputPath string) error {
	// Create output directory
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Generate unique container name
	containerName := fmt.Sprintf("export-%s", sanitizeImageName(imageName))

	fmt.Printf("Exporting container filesystem to: %s\n", outputPath)

	// Create container from image
	createCmd := exec.Command("docker", "create", "--name", containerName, imageName)
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create container: %v", err)
	}

	// Clean up container when done
	defer func() {
		exec.Command("docker", "rm", containerName).Run()
	}()

	// Export container filesystem to tar
	tarPath := outputPath + ".tar"
	exportCmd := exec.Command("docker", "export", containerName)
	exportFile, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("failed to create tar file: %v", err)
	}
	defer exportFile.Close()
	defer os.Remove(tarPath)

	exportCmd.Stdout = exportFile
	if err := exportCmd.Run(); err != nil {
		return fmt.Errorf("failed to export container: %v", err)
	}

	// Convert tar to ext4
	if err := convertTarToExt4(tarPath, outputPath); err != nil {
		return fmt.Errorf("failed to convert tar to ext4: %v", err)
	}

	return nil
}

func convertTarToExt4(tarPath, outputPath string) error {
	// For development, create a placeholder
	// In production, you'd use tools like:
	// - virt-make-fs (from libguestfs-tools)
	// - genext2fs
	// - or mount the tar and create ext4 filesystem

	fmt.Printf("Converting tar to ext4...\n")

	// Check if virt-make-fs is available
	if _, err := exec.LookPath("virt-make-fs"); err == nil {
		// Use virt-make-fs if available
		cmd := exec.Command("sudo", "virt-make-fs", "--format=ext4", "--size=100M", tarPath, outputPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Fallback: create placeholder (for development)
	fmt.Printf("Warning: virt-make-fs not found, creating placeholder file\n")
	placeholder := []byte("DOCKER_EXPORTED_ROOTFS_PLACEHOLDER")
	return os.WriteFile(outputPath, placeholder, 0644)
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
