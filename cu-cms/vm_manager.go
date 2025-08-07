package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
)

// allocateIP allocates a unique IP address for a VM instance
func (vm *VMManager) allocateIP(instanceID string) (string, error) {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	// Check if this instance already has an IP
	if ip, exists := vm.ipPool[instanceID]; exists {
		logger.WithFields(logrus.Fields{
			"instance_id": instanceID,
			"ip":          ip,
		}).Debug("IP already allocated")
		return ip, nil
	}

	// Find next available IP
	for ip := vm.nextIP; ip < 255; ip++ {
		ipStr := fmt.Sprintf("192.168.127.%d", ip)
		if !vm.usedIPs[ipStr] {
			vm.usedIPs[ipStr] = true
			vm.ipPool[instanceID] = ipStr
			vm.nextIP = ip + 1
			if vm.nextIP <= 2 || vm.nextIP >= 254 {
				vm.nextIP = 2 // Wrap around
			}
			logger.WithFields(logrus.Fields{
				"instance_id": instanceID,
				"ip":          ipStr,
			}).Debug("Allocated IP")
			return ipStr, nil
		}
	}

	// Wrap around and check from 2 to original nextIP
	for ip := 2; ip < vm.nextIP; ip++ {
		ipStr := fmt.Sprintf("192.168.127.%d", ip)
		if !vm.usedIPs[ipStr] {
			vm.usedIPs[ipStr] = true
			vm.ipPool[instanceID] = ipStr
			vm.nextIP = ip + 1
			logger.WithFields(logrus.Fields{
				"instance_id": instanceID,
				"ip":          ipStr,
			}).Debug("Allocated IP (wrapped)")
			return ipStr, nil
		}
	}

	return "", fmt.Errorf("no available IPs")
}

// deallocateIP releases an IP address when a VM is stopped
func (vm *VMManager) deallocateIP(instanceID string) {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	if ip, exists := vm.ipPool[instanceID]; exists {
		delete(vm.usedIPs, ip)
		delete(vm.ipPool, instanceID)
		logger.WithFields(logrus.Fields{
			"instance_id": instanceID,
			"ip":          ip,
		}).Debug("Deallocated IP")
	}
}

// getVMIP returns the allocated IP for an instance
func (vm *VMManager) getVMIP(instanceID string) (string, bool) {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	ip, exists := vm.ipPool[instanceID]
	return ip, exists
}

// createFirecrackerLogger returns our main logger for Firecracker operations
func createFirecrackerLogger() *logrus.Entry {
	return logger.WithField("component", "firecracker-sdk")
}

// setupNetworkInterface creates and configures the tap interface for the VM
func (vm *VMManager) setupNetworkInterface(tapName string) error {
	bridgeName := "fc-br"

	logger.WithFields(logrus.Fields{"tap_name": tapName}).Debug("Setting up network interface")

	// Check if bridge exists and create if needed
	if _, err := exec.Command("ip", "link", "show", bridgeName).CombinedOutput(); err != nil {
		logger.WithFields(logrus.Fields{"bridge": bridgeName}).Debug("Creating bridge")
		// Bridge doesn't exist, create it
		if output, err := exec.Command("brctl", "addbr", bridgeName).CombinedOutput(); err != nil {
			return fmt.Errorf("failed to create bridge %s: %v, output: %s", bridgeName, err, string(output))
		}
	}

	// Add IP to bridge and bring it up
	logger.WithFields(logrus.Fields{"bridge": bridgeName, "ip": "192.168.127.1/24"}).Debug("Adding IP to bridge")
	if output, err := exec.Command("ip", "addr", "add", "192.168.127.1/24", "dev", bridgeName).CombinedOutput(); err != nil {
		// Ignore error if IP already exists
		if !strings.Contains(string(output), "File exists") && !strings.Contains(string(output), "Address already assigned") {
			return fmt.Errorf("failed to add IP to bridge: exit status %v, output: %s", err, string(output))
		}
	}

	// Bring bridge up
	if output, err := exec.Command("ip", "link", "set", "dev", bridgeName, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to bring bridge up: %v, output: %s", err, string(output))
	}

	// Delete existing tap interface if it exists
	if output, err := exec.Command("ip", "link", "delete", tapName).CombinedOutput(); err != nil {
		// Ignore error if interface doesn't exist
		if !strings.Contains(string(output), "Cannot find device") {
			return fmt.Errorf("failed to delete existing tap interface: %v, output: %s", err, string(output))
		}
	}

	// Create tap interface
	logger.WithFields(logrus.Fields{"tap": tapName}).Debug("Creating tap interface")
	if output, err := exec.Command("ip", "tuntap", "add", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create tap interface %s: %v, output: %s", tapName, err, string(output))
	}

	// Bring tap interface up
	logger.WithFields(logrus.Fields{"tap": tapName}).Debug("Bringing up tap interface")
	if output, err := exec.Command("ip", "link", "set", "dev", tapName, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to bring tap interface up: %v, output: %s", err, string(output))
	}

	// Add tap to bridge
	logger.WithFields(logrus.Fields{"tap": tapName, "bridge": bridgeName}).Debug("Adding tap to bridge")
	if output, err := exec.Command("brctl", "addif", bridgeName, tapName).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add tap to bridge: %v, output: %s", err, string(output))
	}

	logger.WithFields(logrus.Fields{"tap": tapName, "bridge": bridgeName}).Debug("Network interface setup completed successfully")
	return nil
}

// StartVM starts a new Firecracker VM
func (vm *VMManager) StartVM(instanceID string, plugin *Plugin) error {
	logger.WithFields(logrus.Fields{"instance_id": instanceID, "plugin_slug": plugin.Slug}).Info("Starting VM")

	// Allocate IP for this instance
	vmIP, err := vm.allocateIP(instanceID)
	if err != nil {
		return fmt.Errorf("failed to allocate IP for instance %s: %v", instanceID, err)
	}

	// Create network interface with short name (Linux limit: 15 chars)
	tapName := vm.generateTapName(instanceID)

	// Setup network interface
	if err := vm.setupNetworkInterface(tapName); err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to setup network interface: %v", err)
	}

	// Convert to structured logger compatible with Firecracker SDK
	firecrackerLogger := createFirecrackerLogger()

	// Create machine configuration
	cfg := firecracker.Config{
		SocketPath:      fmt.Sprintf("/tmp/firecracker-%s.sock", instanceID),
		KernelImagePath: vm.kernelPath,
		KernelArgs:      fmt.Sprintf("console=ttyS0 reboot=k panic=1 random.trust_cpu=on rootfstype=ext4 rw init=/sbin/init ip=%s::192.168.127.1:255.255.255.0::eth0:off:::", vmIP),
		Drives: []models.Drive{{
			DriveID:      firecracker.String("rootfs"),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(false),
			PathOnHost:   firecracker.String(plugin.RootFSPath),
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(512),
		},
		NetworkInterfaces: []firecracker.NetworkInterface{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				MacAddress:  "02:FC:00:00:03:04",
				HostDevName: tapName,
			},
		}},
	}

	// Create machine with logger
	machine, err := firecracker.NewMachine(
		context.Background(),
		cfg,
		firecracker.WithLogger(firecrackerLogger),
	)
	if err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to create machine: %v", err)
	}

	// Start the machine
	if err := machine.Start(context.Background()); err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to start machine: %v", err)
	}

	// Store the machine instance
	vm.mutex.Lock()
	vm.instances[instanceID] = machine
	vm.mutex.Unlock()

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "ip": vmIP}).Info("VM started successfully")
	return nil
}

// StopVM stops a Firecracker microVM
func (vm *VMManager) StopVM(instanceID string) error {
	logger.WithFields(logrus.Fields{"instance_id": instanceID}).Info("Stopping VM")

	// Lock only to access the instances map
	vm.mutex.Lock()
	machine, exists := vm.instances[instanceID]
	if !exists {
		vm.mutex.Unlock()
		logger.WithFields(logrus.Fields{"instance_id": instanceID}).Warn("VM instance not found for stopping")
		return fmt.Errorf("VM instance %s not found", instanceID)
	}
	vm.mutex.Unlock()

	logger.WithFields(logrus.Fields{"instance_id": instanceID}).Debug("Shutting down Firecracker machine")

	// Shutdown the machine with timeout (no mutex needed)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := machine.Shutdown(ctx); err != nil {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": err}).Error("Failed to shutdown Firecracker machine")
		return fmt.Errorf("failed to shutdown machine: %v", err)
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID}).Info("VM shutdown completed")

	// Lock only for cleanup operations that modify shared state
	vm.mutex.Lock()
	delete(vm.instances, instanceID)
	vm.mutex.Unlock()

	// Deallocate IP address (has its own mutex)
	vm.deallocateIP(instanceID)

	// Clean up temporary directory (no mutex needed)
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.RemoveAll(vmDir); err != nil {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "path": vmDir, "error": err}).Error("Failed to clean up VM directory")
	} else {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "path": vmDir}).Debug("Cleaned up VM directory")
	}

	// Clean up tap interface (no mutex needed)
	tapName := vm.generateTapName(instanceID)
	cmd := exec.Command("ip", "link", "delete", tapName)
	if err := cmd.Run(); err != nil {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "tap": tapName, "error": err}).Debug("Failed to clean up tap interface")
	} else {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "tap": tapName}).Debug("Cleaned up tap interface")
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID}).Info("VM stopped successfully")

	return nil
}

// StopAllVMs stops all running VM instances
func (vm *VMManager) StopAllVMs() {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	logger.WithFields(logrus.Fields{"count": len(vm.instances)}).Info("Stopping all running VMs")

	for instanceID, machine := range vm.instances {
		logger.WithFields(logrus.Fields{"instance_id": instanceID}).Debug("Stopping VM")

		// Attempt graceful shutdown first
		if err := machine.Shutdown(context.Background()); err != nil {
			logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": err}).Warn("Graceful shutdown failed, forcing stop")
			// Force stop if graceful shutdown fails
			machine.StopVMM()
		}

		// Clean up IP allocation
		vm.deallocateIP(instanceID)
	}

	// Clear all instances
	vm.instances = make(map[string]*firecracker.Machine)

	logger.Info("All VMs stopped successfully")
}

// GetVMStatus returns the status of a VM instance
func (vm *VMManager) GetVMStatus(instanceID string) (string, error) {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	_, exists := vm.instances[instanceID]
	if !exists {
		logger.WithFields(logrus.Fields{"instance_id": instanceID}).Debug("VM instance not found for status check")
		return "not_found", nil
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "status": "running"}).Debug("VM instance found")
	return "running", nil
}

// ListVMs returns a list of running VM instance IDs
func (vm *VMManager) ListVMs() []string {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	instanceIDs := make([]string, 0, len(vm.instances))
	for instanceID := range vm.instances {
		instanceIDs = append(instanceIDs, instanceID)
	}

	logger.WithFields(logrus.Fields{"count": len(instanceIDs), "instances": instanceIDs}).Debug("Listed VM instances")
	return instanceIDs
}

// GetVMIP returns the standard CNI-assigned IP
func (vm *VMManager) GetVMIP(instanceID string) (string, error) {
	vm.mutex.RLock()
	_, exists := vm.instances[instanceID]
	vm.mutex.RUnlock()

	if !exists {
		return "", fmt.Errorf("VM instance %s not found", instanceID)
	}

	// CNI consistently assigns this IP to the first VM
	return "192.168.127.2", nil
}

// ResumeFromSnapshot resumes a VM from a snapshot
func (vm *VMManager) ResumeFromSnapshot(instanceID string, plugin *Plugin) error {
	logger.WithFields(logrus.Fields{"instance_id": instanceID, "plugin_slug": plugin.Slug}).Info("Resuming VM from snapshot")

	// Check if snapshot files exist (use same paths as CreateSnapshot generates)
	basePath := vm.GetSnapshotPath(plugin.Slug)
	snapshotPath := basePath + ".snapshot"
	memPath := basePath + ".mem"

	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		return fmt.Errorf("snapshot file not found: %s", snapshotPath)
	}
	if _, err := os.Stat(memPath); os.IsNotExist(err) {
		return fmt.Errorf("memory file not found: %s", memPath)
	}

	// Allocate IP for this instance
	vmIP, err := vm.allocateIP(instanceID)
	if err != nil {
		return fmt.Errorf("failed to allocate IP for instance %s: %v", instanceID, err)
	}

	// Use the SAME tap device name format
	tapName := vm.generateTapName(instanceID)

	// Setup network interface
	if err := vm.setupNetworkInterface(tapName); err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to setup network interface: %v", err)
	}

	// Convert to structured logger compatible with Firecracker SDK
	firecrackerLogger := createFirecrackerLogger()

	// Create machine configuration with snapshot
	cfg := firecracker.Config{
		SocketPath:      fmt.Sprintf("/tmp/firecracker-%s.sock", instanceID),
		KernelImagePath: vm.kernelPath,
		KernelArgs:      fmt.Sprintf("console=ttyS0 reboot=k panic=1 random.trust_cpu=on rootfstype=ext4 rw ip=%s::192.168.127.1:255.255.255.0::eth0:off:::", vmIP),
		Drives: []models.Drive{{
			DriveID:      firecracker.String("rootfs"),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(false),
			PathOnHost:   firecracker.String(plugin.RootFSPath),
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(512),
		},
		NetworkInterfaces: []firecracker.NetworkInterface{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", byte(vm.nextIP), byte(vm.nextIP+1)),
				HostDevName: tapName,
			},
		}},
	}

	// Create machine with logger and snapshot
	machine, err := firecracker.NewMachine(
		context.Background(),
		cfg,
		firecracker.WithLogger(firecrackerLogger),
		firecracker.WithSnapshot(memPath, snapshotPath),
	)
	if err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to create machine with snapshot: %v", err)
	}

	// Start the machine (will resume from snapshot)
	if err := machine.Start(context.Background()); err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to resume machine from snapshot: %v", err)
	}

	// Store the machine instance
	vm.mutex.Lock()
	vm.instances[instanceID] = machine
	vm.mutex.Unlock()

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "ip": vmIP}).Info("VM resumed from snapshot successfully")
	return nil
}

// ExecuteInVM sends a command to the VM via HTTP and reads the result
func (vm *VMManager) ExecuteInVM(instanceID string, request map[string]interface{}) (map[string]interface{}, error) {
	vm.mutex.RLock()
	_, exists := vm.instances[instanceID]
	vmIP, ipExists := vm.getVMIP(instanceID)
	vm.mutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("VM instance %s not found", instanceID)
	}

	if !ipExists {
		return nil, fmt.Errorf("VM IP not found for instance %s", instanceID)
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "vm_ip": vmIP, "request": request}).Debug("Executing command in VM via HTTP")

	// No more sleep - plugins should be ready when they respond to health checks

	// Make HTTP request to the plugin (using port 80)
	pluginURL := fmt.Sprintf("http://%s:80/execute", vmIP)

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "url": pluginURL, "payload": string(requestBytes)}).Debug("Making HTTP request to plugin")

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Post(pluginURL, "application/json", bytes.NewBuffer(requestBytes))
	if err != nil {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "url": pluginURL, "error": err}).Error("Failed to communicate with plugin via HTTP")
		return nil, fmt.Errorf("failed to communicate with plugin: %v", err)
	}
	defer resp.Body.Close()

	// Read response
	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": err}).Error("Failed to read plugin response")
		return nil, fmt.Errorf("failed to read plugin response: %v", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": err}).Error("Failed to unmarshal plugin response")
		return nil, fmt.Errorf("failed to unmarshal plugin response: %v", err)
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "vm_ip": vmIP, "response_size": len(responseBytes)}).Info("Successfully executed command in VM via HTTP")
	return response, nil
}

// CreateSnapshot creates a snapshot of the running VM
func (vm *VMManager) CreateSnapshot(instanceID string, snapshotPath string) error {
	vm.mutex.RLock()
	machine, exists := vm.instances[instanceID]
	vm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	// Ensure snapshot directory exists
	if err := vm.initSnapshotDir(); err != nil {
		return fmt.Errorf("failed to create snapshot directory: %v", err)
	}

	memFilePath := snapshotPath + ".mem"
	snapshotFilePath := snapshotPath + ".snapshot"

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "snapshot_path": snapshotFilePath, "mem_path": memFilePath}).Info("Creating snapshot")

	// Pause VM before creating snapshot
	if err := machine.PauseVM(context.Background()); err != nil {
		return fmt.Errorf("failed to pause VM: %v", err)
	}

	// Ensure we resume the VM regardless of snapshot success/failure
	defer func() {
		if err := machine.ResumeVM(context.Background()); err != nil {
			logger.WithFields(logrus.Fields{"instance_id": instanceID, "error": err}).Error("Failed to resume VM after snapshot")
		}
	}()

	// Create snapshot with logger (no additional options available in current SDK version)
	if err := machine.CreateSnapshot(context.Background(), memFilePath, snapshotFilePath); err != nil {
		return fmt.Errorf("failed to create snapshot: %v", err)
	}

	logger.WithFields(logrus.Fields{"instance_id": instanceID, "snapshot_path": snapshotFilePath}).Info("Snapshot created successfully")
	return nil
}

// GetSnapshotPath returns the standard snapshot path for a plugin slug (base path without extension)
func (vm *VMManager) GetSnapshotPath(pluginSlug string) string {
	return filepath.Join(vm.snapshotDir, pluginSlug)
}

// HasSnapshot checks if a snapshot exists for the given plugin slug
func (vm *VMManager) HasSnapshot(pluginSlug string) bool {
	basePath := vm.GetSnapshotPath(pluginSlug)
	snapshotPath := basePath + ".snapshot"
	memPath := basePath + ".mem"

	// Both files must exist for a valid snapshot
	_, err1 := os.Stat(snapshotPath)
	_, err2 := os.Stat(memPath)

	return err1 == nil && err2 == nil
}

// DeleteSnapshot removes snapshot files for a plugin
func (vm *VMManager) DeleteSnapshot(pluginSlug string) error {
	basePath := vm.GetSnapshotPath(pluginSlug)
	snapshotPath := basePath + ".snapshot"
	memPath := basePath + ".mem"

	// Remove both snapshot files (ignore errors if files don't exist)
	os.Remove(snapshotPath)
	os.Remove(memPath)

	logger.WithFields(logrus.Fields{"plugin_slug": pluginSlug, "snapshot_path": snapshotPath}).Info("Deleted snapshot files")
	return nil
}

// generateTapName creates a short, unique tap interface name (max 15 chars for Linux)
func (vm *VMManager) generateTapName(instanceID string) string {
	// Linux interface names are limited to 15 characters
	// Use format: "fc-" + 8-char hash = 11 characters total
	hash := md5.Sum([]byte(instanceID))
	shortHash := hex.EncodeToString(hash[:4]) // 8 characters
	return fmt.Sprintf("fc-%s", shortHash)
}

// Helper functions for pointer conversion
func stringPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64    { return &i }
func boolPtr(b bool) *bool       { return &b }
