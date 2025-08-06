package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
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

// StartVM starts a new Firecracker microVM
func (vm *VMManager) StartVM(instanceID string, plugin *Plugin) error {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	logger.Info("Starting VM",
		"instance_id", instanceID,
		"plugin_id", plugin.ID,
		"plugin_name", plugin.Name,
		"rootfs_path", plugin.RootFSPath,
		"kernel_path", vm.kernelPath,
		"firecracker_path", vm.firecrackerPath,
	)

	// Allocate unique IP for this VM
	vmIP, err := vm.allocateIP(instanceID)
	if err != nil {
		logger.Error("Failed to allocate IP", "instance_id", instanceID, "error", err)
		return fmt.Errorf("failed to allocate IP: %v", err)
	}

	// Create unique tap interface for VM networking
	tapName := fmt.Sprintf("tap-%s", instanceID[len(instanceID)-8:])

	// Create tap interface
	cmd := exec.Command("ip", "tuntap", "add", tapName, "mode", "tap")
	if err := cmd.Run(); err != nil {
		logger.Debug("Tap interface may already exist", "instance_id", instanceID, "tap", tapName, "error", err)
	}

	// Create bridge if it doesn't exist
	cmd = exec.Command("ip", "link", "add", "fcnetbridge0", "type", "bridge")
	if err := cmd.Run(); err != nil {
		logger.Debug("Bridge may already exist", "instance_id", instanceID, "error", err)
	}

	// Add IP to bridge
	cmd = exec.Command("ip", "addr", "add", "192.168.127.1/24", "dev", "fcnetbridge0")
	if err := cmd.Run(); err != nil {
		logger.Debug("Bridge IP may already be set", "instance_id", instanceID, "error", err)
	}

	// Bring up bridge
	cmd = exec.Command("ip", "link", "set", "fcnetbridge0", "up")
	if err := cmd.Run(); err != nil {
		logger.Error("Failed to bring up bridge", "instance_id", instanceID, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("failed to bring up bridge: %v", err)
	}

	// Add tap to bridge
	cmd = exec.Command("ip", "link", "set", tapName, "master", "fcnetbridge0")
	if err := cmd.Run(); err != nil {
		logger.Error("Failed to add tap to bridge", "instance_id", instanceID, "tap", tapName, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("failed to add tap to bridge: %v", err)
	}

	// Bring up tap
	cmd = exec.Command("ip", "link", "set", tapName, "up")
	if err := cmd.Run(); err != nil {
		logger.Error("Failed to bring up tap interface", "instance_id", instanceID, "tap", tapName, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("failed to bring up tap interface: %v", err)
	}

	logger.Debug("Network interfaces created", "instance_id", instanceID, "tap", tapName, "vm_ip", vmIP)

	// Create temporary directory for VM
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		logger.Error("Failed to create VM directory", "instance_id", instanceID, "path", vmDir, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("failed to create VM directory: %v", err)
	}

	logger.Debug("Created VM directory", "instance_id", instanceID, "path", vmDir)

	// Create socket path
	socketPath := filepath.Join(vmDir, "firecracker.sock")

	// Check if rootfs file exists and is accessible
	if _, err := os.Stat(plugin.RootFSPath); err != nil {
		logger.Error("Rootfs file not accessible", "instance_id", instanceID, "path", plugin.RootFSPath, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("rootfs file not accessible: %v", err)
	}

	logger.Debug("Rootfs file verified", "instance_id", instanceID, "path", plugin.RootFSPath)

	// Build dynamic kernel args with allocated IP
	kernelArgs := fmt.Sprintf("console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw ip=%s::192.168.127.1:255.255.255.0:::off:1.1.1.1:1.0.0.1:", vmIP)

	logger.Debug("Configuring Firecracker",
		"instance_id", instanceID,
		"socket_path", socketPath,
		"kernel_args", kernelArgs,
		"vm_ip", vmIP,
	)

	// Configure Firecracker with logging only to our custom writer (no log files)
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: vm.kernelPath,
		KernelArgs:      kernelArgs,
		LogLevel:        "Info", // Reduced log level to minimize noise
		// LogPath:      "", // Disabled - use only our custom writer
		// LogFifo:      "", // Disabled - use only our custom writer
		FifoLogWriter: &FirecrackerLogWriter{instanceID: instanceID},
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("rootfs"),
				PathOnHost:   firecracker.String(plugin.RootFSPath),
				IsReadOnly:   firecracker.Bool(false),
				IsRootDevice: firecracker.Bool(true),
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(128),
		},
		// Use simple tap interface (IP configured via boot args)
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				StaticConfiguration: &firecracker.StaticNetworkConfiguration{
					HostDevName: tapName,
				},
			},
		},
	}

	logger.Debug("Creating Firecracker machine", "instance_id", instanceID)

	// Create Firecracker machine
	machine, err := firecracker.NewMachine(context.Background(), cfg)
	if err != nil {
		logger.Error("Failed to create Firecracker machine", "instance_id", instanceID, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("failed to create machine: %v", err)
	}

	logger.Debug("Starting Firecracker machine", "instance_id", instanceID)

	// Start the machine without timeout (let it run indefinitely)
	if err := machine.Start(context.Background()); err != nil {
		logger.Error("Failed to start Firecracker machine", "instance_id", instanceID, "error", err)
		vm.deallocateIP(instanceID) // Clean up IP on failure
		return fmt.Errorf("failed to start machine: %v", err)
	}

	logger.Debug("Firecracker machine started successfully", "instance_id", instanceID)

	// Store the machine instance
	vm.instances[instanceID] = machine

	logger.Info("VM started successfully",
		"instance_id", instanceID,
		"plugin_id", plugin.ID,
		"plugin_name", plugin.Name,
		"vcpu_count", 1,
		"memory_mib", 128,
		"vm_ip", vmIP,
		"tap_interface", tapName,
	)

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
