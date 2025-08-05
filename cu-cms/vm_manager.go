package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

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

	// CNI will handle network interface creation

	// Create temporary directory for VM
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		logger.Error("Failed to create VM directory", "instance_id", instanceID, "path", vmDir, "error", err)
		return fmt.Errorf("failed to create VM directory: %v", err)
	}

	logger.Debug("Created VM directory", "instance_id", instanceID, "path", vmDir)

	// Create socket path
	socketPath := filepath.Join(vmDir, "firecracker.sock")

	// Check if rootfs file exists and is accessible
	if _, err := os.Stat(plugin.RootFSPath); err != nil {
		logger.Error("Rootfs file not accessible", "instance_id", instanceID, "path", plugin.RootFSPath, "error", err)
		return fmt.Errorf("rootfs file not accessible: %v", err)
	}

	logger.Debug("Rootfs file verified", "instance_id", instanceID, "path", plugin.RootFSPath)

	logger.Debug("Configuring Firecracker",
		"instance_id", instanceID,
		"socket_path", socketPath,
		"kernel_args", "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw",
	)

	// Configure Firecracker
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: vm.kernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw",
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
		// Use CNI for network configuration
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				CNIConfiguration: &firecracker.CNIConfiguration{
					NetworkName: "fcnet",
					IfName:      "veth0",
					Force:       true,
				},
			},
		},
	}

	logger.Debug("Creating Firecracker machine", "instance_id", instanceID)

	// Create Firecracker machine
	machine, err := firecracker.NewMachine(context.Background(), cfg)
	if err != nil {
		logger.Error("Failed to create Firecracker machine", "instance_id", instanceID, "error", err)
		return fmt.Errorf("failed to create machine: %v", err)
	}

	logger.Debug("Starting Firecracker machine", "instance_id", instanceID)

	// Start the machine with a shorter timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := machine.Start(ctx); err != nil {
		logger.Error("Failed to start Firecracker machine", "instance_id", instanceID, "error", err)
		return fmt.Errorf("failed to start machine: %v", err)
	}

	logger.Debug("Firecracker machine started successfully", "instance_id", instanceID)

	// Store the machine instance
	vm.instances[instanceID] = machine

	// Get the actual IP assigned by CNI
	actualIP, err := vm.GetVMIP(instanceID)
	if err != nil {
		logger.Warn("Failed to get VM IP", "instance_id", instanceID, "error", err)
	} else {
		logger.Info("Actual VM IP obtained from CNI", "instance_id", instanceID, "actual_ip", actualIP)
	}

	logger.Info("VM started successfully",
		"instance_id", instanceID,
		"plugin_id", plugin.ID,
		"plugin_name", plugin.Name,
		"vcpu_count", 1,
		"memory_mib", 128,
		"actual_ip", actualIP,
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

	// Shutdown the machine
	if err := machine.Shutdown(context.Background()); err != nil {
		logger.Error("Failed to shutdown Firecracker machine", "instance_id", instanceID, "error", err)
		return fmt.Errorf("failed to shutdown machine: %v", err)
	}

	logger.Debug("Waiting for VM shutdown", "instance_id", instanceID)

	// Wait for shutdown
	if err := machine.Wait(context.Background()); err != nil {
		logger.Error("VM shutdown error", "instance_id", instanceID, "error", err)
	} else {
		logger.Info("VM shutdown completed", "instance_id", instanceID)
	}

	// Clean up
	delete(vm.instances, instanceID)

	// Clean up temporary directory
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.RemoveAll(vmDir); err != nil {
		logger.Error("Failed to clean up VM directory", "instance_id", instanceID, "path", vmDir, "error", err)
	} else {
		logger.Debug("Cleaned up VM directory", "instance_id", instanceID, "path", vmDir)
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

// GetVMIP gets the actual IP assigned by CNI to a VM instance
func (vm *VMManager) GetVMIP(instanceID string) (string, error) {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	machine, exists := vm.instances[instanceID]
	if !exists {
		return "", fmt.Errorf("VM instance %s not found", instanceID)
	}

	// Get the actual IP from CNI configuration
	if len(machine.Cfg.NetworkInterfaces) > 0 && machine.Cfg.NetworkInterfaces[0].StaticConfiguration != nil {
		if machine.Cfg.NetworkInterfaces[0].StaticConfiguration.IPConfiguration != nil {
			actualIP := machine.Cfg.NetworkInterfaces[0].StaticConfiguration.IPConfiguration.IPAddr.IP.String()
			logger.Debug("Retrieved VM IP from CNI", "instance_id", instanceID, "ip", actualIP)
			return actualIP, nil
		}
	}

	return "", fmt.Errorf("no IP configuration found for VM %s", instanceID)
}
