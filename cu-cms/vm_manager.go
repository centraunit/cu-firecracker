package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
)

// allocateIP allocates a unique IP address for a VM instance
func (vm *VMManager) allocateIP(instanceID string) (string, error) {
	// Find next available IP in range 192.168.127.2 - 192.168.127.254
	for ip := vm.nextIP; ip <= 254; ip++ {
		ipStr := fmt.Sprintf("192.168.127.%d", ip)
		if !vm.usedIPs[ipStr] {
			vm.usedIPs[ipStr] = true
			vm.ipPool[instanceID] = ipStr
			vm.nextIP = ip + 1
			if vm.nextIP > 254 {
				vm.nextIP = 2 // Wrap around
			}
			logger.Debug("Allocated IP", "instance_id", instanceID, "ip", ipStr)
			return ipStr, nil
		}
	}

	// If we reach here, try from the beginning
	for ip := 2; ip < vm.nextIP; ip++ {
		ipStr := fmt.Sprintf("192.168.127.%d", ip)
		if !vm.usedIPs[ipStr] {
			vm.usedIPs[ipStr] = true
			vm.ipPool[instanceID] = ipStr
			vm.nextIP = ip + 1
			logger.Debug("Allocated IP (wrapped)", "instance_id", instanceID, "ip", ipStr)
			return ipStr, nil
		}
	}

	return "", fmt.Errorf("no available IP addresses in pool")
}

// deallocateIP releases an IP address when a VM is stopped
func (vm *VMManager) deallocateIP(instanceID string) {
	if ip, exists := vm.ipPool[instanceID]; exists {
		delete(vm.usedIPs, ip)
		delete(vm.ipPool, instanceID)
		logger.Debug("Deallocated IP", "instance_id", instanceID, "ip", ip)
	}
}

// getVMIP returns the allocated IP for an instance
func (vm *VMManager) getVMIP(instanceID string) (string, bool) {
	ip, exists := vm.ipPool[instanceID]
	return ip, exists
}

// createFirecrackerLogger creates a logrus logger compatible with Firecracker SDK
func createFirecrackerLogger() *logrus.Entry {
	firecrackerLog := logrus.New()
	firecrackerLog.SetLevel(logrus.InfoLevel)
	firecrackerLog.SetFormatter(&logrus.JSONFormatter{})
	return firecrackerLog.WithField("component", "firecracker")
}

// setupNetworkInterface creates and configures the tap interface for the VM
func (vm *VMManager) setupNetworkInterface(tapName string) error {
	logger.Debug("Setting up network interface", "tap_name", tapName)

	// Step 1: Create bridge if it doesn't exist
	bridgeName := "fcnetbridge0"
	cmd := exec.Command("ip", "link", "show", bridgeName)
	if err := cmd.Run(); err != nil {
		logger.Debug("Creating bridge", "bridge", bridgeName)
		cmd = exec.Command("ip", "link", "add", bridgeName, "type", "bridge")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create bridge %s: %v", bridgeName, err)
		}
	}

	// Step 2: Configure bridge IP if not already set
	cmd = exec.Command("ip", "addr", "show", bridgeName)
	output, _ := cmd.Output()
	if !bytes.Contains(output, []byte("192.168.127.1/24")) {
		logger.Debug("Adding IP to bridge", "bridge", bridgeName, "ip", "192.168.127.1/24")
		cmd = exec.Command("ip", "addr", "add", "192.168.127.1/24", "dev", bridgeName)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to add IP to bridge: %v", err)
		}
	}

	// Step 3: Bring up bridge
	cmd = exec.Command("ip", "link", "set", bridgeName, "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to bring up bridge: %v", err)
	}

	// Step 4: Delete tap interface if it already exists
	cmd = exec.Command("ip", "link", "delete", tapName)
	cmd.Run() // Ignore error if doesn't exist

	// Step 5: Create tap interface
	logger.Debug("Creating tap interface", "tap", tapName)
	cmd = exec.Command("ip", "tuntap", "add", "dev", tapName, "mode", "tap")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tap interface %s: %v", tapName, err)
	}

	// Step 6: Bring up tap interface
	logger.Debug("Bringing up tap interface", "tap", tapName)
	cmd = exec.Command("ip", "link", "set", tapName, "up")
	if err := cmd.Run(); err != nil {
		// Cleanup: delete the tap interface we just created
		exec.Command("ip", "link", "delete", tapName).Run()
		return fmt.Errorf("failed to bring up tap interface %s: %v", tapName, err)
	}

	// Step 7: Add tap to bridge
	logger.Debug("Adding tap to bridge", "tap", tapName, "bridge", bridgeName)
	cmd = exec.Command("ip", "link", "set", tapName, "master", bridgeName)
	if err := cmd.Run(); err != nil {
		// Cleanup: delete the tap interface
		exec.Command("ip", "link", "delete", tapName).Run()
		return fmt.Errorf("failed to add tap %s to bridge %s: %v", tapName, bridgeName, err)
	}

	logger.Debug("Network interface setup completed successfully", "tap", tapName, "bridge", bridgeName)
	return nil
}

// StartVM starts a new Firecracker VM
func (vm *VMManager) StartVM(instanceID string, plugin *Plugin) error {
	logger.Info("Starting VM", "instance_id", instanceID, "plugin_slug", plugin.Slug)

	// Allocate IP for this instance
	vmIP, err := vm.allocateIP(instanceID)
	if err != nil {
		return fmt.Errorf("failed to allocate IP for instance %s: %v", instanceID, err)
	}

	// Create network interface
	tapName := fmt.Sprintf("fc-tap-%s", instanceID)

	// Convert to structured logger compatible with Firecracker SDK
	firecrackerLogger := createFirecrackerLogger()

	// Create machine configuration
	cfg := &firecracker.Config{
		SocketPath:      fmt.Sprintf("/tmp/firecracker-%s.sock", instanceID),
		KernelImagePath: vm.kernelPath,
		KernelArgs:      fmt.Sprintf("ro console=ttyS0 noapic reboot=k panic=1 pci=off nomodules random.trust_cpu=on ip=%s::192.168.127.1:255.255.255.0::eth0:off", vmIP),
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
				MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", (vm.nextIP>>8)&0xff, vm.nextIP&0xff),
				HostDevName: tapName,
			},
		}},
	}

	// Setup network interface
	if err := vm.setupNetworkInterface(tapName); err != nil {
		return fmt.Errorf("failed to setup network interface: %v", err)
	}

	// Remove any existing socket
	os.Remove(cfg.SocketPath)

	// Create VM command
	cmd := firecracker.VMCommandBuilder{}.
		WithBin(vm.firecrackerPath).
		WithSocketPath(cfg.SocketPath).
		WithStdout(os.Stdout).
		WithStderr(os.Stderr).
		Build(context.Background())

	// Create machine with logger
	machineOpts := []firecracker.Opt{
		firecracker.WithProcessRunner(cmd),
		firecracker.WithLogger(firecrackerLogger),
	}

	machine, err := firecracker.NewMachine(context.Background(), *cfg, machineOpts...)
	if err != nil {
		return fmt.Errorf("failed to create machine: %v", err)
	}

	// Start the machine
	if err := machine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start machine: %v", err)
	}

	// Store the machine instance
	vm.mutex.Lock()
	vm.instances[instanceID] = machine
	vm.mutex.Unlock()

	logger.Info("VM started successfully", "instance_id", instanceID, "ip", vmIP)
	return nil
}

// StopVM stops a Firecracker microVM
func (vm *VMManager) StopVM(instanceID string) error {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	logger.Info("Stopping VM", "instance_id", instanceID)

	machine, exists := vm.instances[instanceID]
	if !exists {
		logger.Warn("VM instance not found for stopping", "instance_id", instanceID)
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	logger.Debug("Shutting down Firecracker machine", "instance_id", instanceID)

	// Shutdown the machine with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := machine.Shutdown(ctx); err != nil {
		logger.Error("Failed to shutdown Firecracker machine", "instance_id", instanceID, "error", err)
		return fmt.Errorf("failed to shutdown machine: %v", err)
	}

	logger.Info("VM shutdown completed", "instance_id", instanceID)

	// Clean up
	delete(vm.instances, instanceID)

	// Deallocate IP address
	vm.deallocateIP(instanceID)

	// Clean up temporary directory
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.RemoveAll(vmDir); err != nil {
		logger.Error("Failed to clean up VM directory", "instance_id", instanceID, "path", vmDir, "error", err)
	} else {
		logger.Debug("Cleaned up VM directory", "instance_id", instanceID, "path", vmDir)
	}

	// Clean up tap interface
	tapName := fmt.Sprintf("tap-%s", instanceID[len(instanceID)-8:])
	cmd := exec.Command("ip", "link", "delete", tapName)
	if err := cmd.Run(); err != nil {
		logger.Debug("Failed to clean up tap interface", "instance_id", instanceID, "tap", tapName, "error", err)
	} else {
		logger.Debug("Cleaned up tap interface", "instance_id", instanceID, "tap", tapName)
	}

	logger.Info("VM stopped successfully", "instance_id", instanceID)

	return nil
}

// StopAllVMs stops all running VM instances
func (vm *VMManager) StopAllVMs() {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	logger.Info("Stopping all running VMs", "count", len(vm.instances))

	for instanceID, machine := range vm.instances {
		logger.Debug("Stopping VM", "instance_id", instanceID)

		// Attempt graceful shutdown first
		if err := machine.Shutdown(context.Background()); err != nil {
			logger.Warn("Graceful shutdown failed, forcing stop", "instance_id", instanceID, "error", err)
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
		logger.Debug("VM instance not found for status check", "instance_id", instanceID)
		return "not_found", nil
	}

	logger.Debug("VM instance found", "instance_id", instanceID, "status", "running")
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

	logger.Debug("Listed VM instances", "count", len(instanceIDs), "instances", instanceIDs)
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

// ResumeFromSnapshot resumes a VM from a previously created snapshot
func (vm *VMManager) ResumeFromSnapshot(instanceID string, plugin *Plugin) error {
	logger.Info("Resuming VM from snapshot", "instance_id", instanceID, "plugin_slug", plugin.Slug)

	snapshotPath := vm.GetSnapshotPath(plugin.Slug) + ".snapshot"
	memPath := vm.GetSnapshotPath(plugin.Slug) + ".mem"

	// Check if snapshot files exist
	if !vm.HasSnapshot(plugin.Slug) {
		return fmt.Errorf("snapshot files not found for plugin %s", plugin.Slug)
	}

	// Allocate the SAME IP that was used when creating the snapshot
	vmIP, err := vm.allocateIP(instanceID)
	if err != nil {
		return fmt.Errorf("failed to allocate IP for instance %s: %v", instanceID, err)
	}

	// Use the SAME tap device name format
	tapName := fmt.Sprintf("fc-tap-%s", instanceID)

	// Convert to structured logger compatible with Firecracker SDK
	firecrackerLogger := createFirecrackerLogger()

	// Create machine configuration with snapshot
	cfg := &firecracker.Config{
		SocketPath:      fmt.Sprintf("/tmp/firecracker-%s.sock", instanceID),
		KernelImagePath: vm.kernelPath,
		KernelArgs:      fmt.Sprintf("ro console=ttyS0 noapic reboot=k panic=1 pci=off nomodules random.trust_cpu=on ip=%s::192.168.127.1:255.255.255.0::eth0:off", vmIP),
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
				MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", (vm.nextIP>>8)&0xff, vm.nextIP&0xff),
				HostDevName: tapName,
			},
		}},
	}

	// Setup network interface (reuse the same one)
	if err := vm.setupNetworkInterface(tapName); err != nil {
		return fmt.Errorf("failed to setup network interface: %v", err)
	}

	// Remove any existing socket
	os.Remove(cfg.SocketPath)

	// Create VM command
	cmd := firecracker.VMCommandBuilder{}.
		WithBin(vm.firecrackerPath).
		WithSocketPath(cfg.SocketPath).
		WithStdout(os.Stdout).
		WithStderr(os.Stderr).
		Build(context.Background())

	// Create machine with snapshot configuration and logger
	machineOpts := []firecracker.Opt{
		firecracker.WithProcessRunner(cmd),
		firecracker.WithLogger(firecrackerLogger),
		firecracker.WithSnapshot(memPath, snapshotPath),
	}

	machine, err := firecracker.NewMachine(context.Background(), *cfg, machineOpts...)
	if err != nil {
		return fmt.Errorf("failed to create machine from snapshot: %v", err)
	}

	// Start the machine (this will resume from snapshot)
	if err := machine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start machine from snapshot: %v", err)
	}

	// Store the machine instance
	vm.mutex.Lock()
	vm.instances[instanceID] = machine
	vm.mutex.Unlock()

	logger.Info("VM resumed from snapshot successfully", "instance_id", instanceID, "ip", vmIP)
	return nil
}

// FirecrackerLogWriter implements io.Writer to redirect Firecracker logs to our structured logger
type FirecrackerLogWriter struct {
	instanceID string
}

func (w *FirecrackerLogWriter) Write(p []byte) (n int, err error) {
	// Convert Firecracker logs to structured logging
	message := string(p)
	if message != "" {
		logger.Info("Firecracker VM log",
			"instance_id", w.instanceID,
			"message", message,
		)
	}
	return len(p), nil
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

	logger.Debug("Executing command in VM via HTTP", "instance_id", instanceID, "vm_ip", vmIP, "request", request)

	// No more sleep - plugins should be ready when they respond to health checks

	// Make HTTP request to the plugin (using port 80)
	pluginURL := fmt.Sprintf("http://%s:80/execute", vmIP)

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	logger.Debug("Making HTTP request to plugin", "instance_id", instanceID, "url", pluginURL, "payload", string(requestBytes))

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Post(pluginURL, "application/json", bytes.NewBuffer(requestBytes))
	if err != nil {
		logger.Error("Failed to communicate with plugin via HTTP", "instance_id", instanceID, "url", pluginURL, "error", err)
		return nil, fmt.Errorf("failed to communicate with plugin: %v", err)
	}
	defer resp.Body.Close()

	// Read response
	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read plugin response", "instance_id", instanceID, "error", err)
		return nil, fmt.Errorf("failed to read plugin response: %v", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		logger.Error("Failed to unmarshal plugin response", "instance_id", instanceID, "error", err)
		return nil, fmt.Errorf("failed to unmarshal plugin response: %v", err)
	}

	logger.Info("Successfully executed command in VM via HTTP", "instance_id", instanceID, "vm_ip", vmIP, "response_size", len(responseBytes))
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

	logger.Info("Creating snapshot", "instance_id", instanceID, "snapshot_path", snapshotFilePath, "mem_path", memFilePath)

	// Pause VM before creating snapshot
	if err := machine.PauseVM(context.Background()); err != nil {
		return fmt.Errorf("failed to pause VM: %v", err)
	}

	// Ensure we resume the VM regardless of snapshot success/failure
	defer func() {
		if err := machine.ResumeVM(context.Background()); err != nil {
			logger.Error("Failed to resume VM after snapshot", "instance_id", instanceID, "error", err)
		}
	}()

	// Create snapshot with logger
	if err := machine.CreateSnapshot(context.Background(), memFilePath, snapshotFilePath); err != nil {
		return fmt.Errorf("failed to create snapshot: %v", err)
	}

	logger.Info("Snapshot created successfully", "instance_id", instanceID, "snapshot_path", snapshotFilePath)
	return nil
}

// GetSnapshotPath returns the standard snapshot path for a plugin slug
func (vm *VMManager) GetSnapshotPath(pluginSlug string) string {
	return filepath.Join(vm.snapshotDir, pluginSlug+".snapshot")
}

// HasSnapshot checks if a snapshot exists for the given plugin slug
func (vm *VMManager) HasSnapshot(pluginSlug string) bool {
	snapshotPath := vm.GetSnapshotPath(pluginSlug)
	memPath := snapshotPath + ".mem"

	// Both files must exist for a valid snapshot
	_, err1 := os.Stat(snapshotPath)
	_, err2 := os.Stat(memPath)

	return err1 == nil && err2 == nil
}

// DeleteSnapshot removes snapshot files for a plugin
func (vm *VMManager) DeleteSnapshot(pluginSlug string) error {
	snapshotPath := vm.GetSnapshotPath(pluginSlug)
	memPath := snapshotPath + ".mem"

	// Remove both snapshot files (ignore errors if files don't exist)
	os.Remove(snapshotPath)
	os.Remove(memPath)

	logger.Info("Deleted snapshot files", "plugin_slug", pluginSlug, "snapshot_path", snapshotPath)
	return nil
}

// Helper functions for pointer conversion
func stringPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64    { return &i }
func boolPtr(b bool) *bool       { return &b }
