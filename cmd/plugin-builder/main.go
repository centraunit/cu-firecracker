package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type PluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Description string `json:"description"`
}

func main() {
	pluginDir := flag.String("plugin", "", "Plugin directory path")
	outputPath := flag.String("output", "", "Output path for rootfs.ext4 (optional)")
	sizeMB := flag.Int("size", 1000, "Filesystem size in MB (default: 1000)")
	flag.Parse()

	if *pluginDir == "" {
		fmt.Println("Usage: plugin-builder -plugin <plugin-directory> [-output <output-path>] [-size <size-in-mb>]")
		fmt.Println("Example: plugin-builder -plugin plugins/my-plugin -size 2000")
		os.Exit(1)
	}

	// Validate plugin directory
	if err := validatePluginDirectory(*pluginDir); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Read plugin manifest
	manifest, err := readPluginManifest(*pluginDir)
	if err != nil {
		fmt.Printf("Error reading plugin manifest: %v\n", err)
		os.Exit(1)
	}

	// Set default output path if not provided
	if *outputPath == "" {
		*outputPath = filepath.Join(*pluginDir, "build", "rootfs.ext4")
	}

	fmt.Printf("Building plugin: %s (v%s)\n", manifest.Name, manifest.Version)
	fmt.Printf("Filesystem size: %d MB\n", *sizeMB)

	// Step 1: Build Docker image
	imageName, err := buildDockerImage(*pluginDir, manifest)
	if err != nil {
		fmt.Printf("Error building Docker image: %v\n", err)
		os.Exit(1)
	}

	// Step 2: Export container and create ext4 filesystem
	if err := exportToExt4(imageName, *outputPath, *sizeMB); err != nil {
		fmt.Printf("Error: %v\n", err)
		// Clean up Docker image
		cleanupDockerImage(imageName)
		os.Exit(1)
	}

	// Step 3: Clean up Docker image
	if err := cleanupDockerImage(imageName); err != nil {
		fmt.Printf("Warning: Failed to clean up Docker image: %v\n", err)
	}

	fmt.Printf("Plugin exported successfully to: %s\n", *outputPath)
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
