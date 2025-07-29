package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

// StartVM starts a new Firecracker microVM
func (vm *VMManager) StartVM(instanceID string, plugin *Plugin) error {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	// Create temporary directory for VM
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("failed to create VM directory: %v", err)
	}

	// Create socket path
	socketPath := filepath.Join(vmDir, "firecracker.sock")

	// Configure Firecracker
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: vm.kernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off",
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("1"),
				PathOnHost:   firecracker.String(plugin.RootFSPath),
				IsReadOnly:   firecracker.Bool(false),
				IsRootDevice: firecracker.Bool(true),
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(128),
		},
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				CNIConfiguration: &firecracker.CNIConfiguration{
					NetworkName: "fcnet",
					IfName:      "veth0",
				},
			},
		},
	}

	// Create Firecracker machine
	machine, err := firecracker.NewMachine(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("failed to create machine: %v", err)
	}

	// Start the machine
	if err := machine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start machine: %v", err)
	}

	// Store the machine instance
	vm.instances[instanceID] = machine

	log.Printf("Started VM %s for plugin %s", instanceID, plugin.ID)
	return nil
}

// StopVM stops a Firecracker microVM
func (vm *VMManager) StopVM(instanceID string) error {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	machine, exists := vm.instances[instanceID]
	if !exists {
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	// Shutdown the machine
	if err := machine.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("failed to shutdown machine: %v", err)
	}

	// Wait for shutdown
	if err := machine.Wait(context.Background()); err != nil {
		log.Printf("VM %s shutdown error: %v", instanceID, err)
	} else {
		log.Printf("VM %s shutdown completed", instanceID)
	}

	// Clean up
	delete(vm.instances, instanceID)

	// Clean up temporary directory
	vmDir := filepath.Join("/tmp", "firecracker-"+instanceID)
	if err := os.RemoveAll(vmDir); err != nil {
		log.Printf("Failed to clean up VM directory %s: %v", vmDir, err)
	}

	return nil
}

// GetVMStatus returns the status of a VM instance
func (vm *VMManager) GetVMStatus(instanceID string) (string, error) {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	_, exists := vm.instances[instanceID]
	if !exists {
		return "not_found", nil
	}

	return "running", nil
}

// ListVMs returns all running VM instances
func (vm *VMManager) ListVMs() []string {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	instances := make([]string, 0, len(vm.instances))
	for instanceID := range vm.instances {
		instances = append(instances, instanceID)
	}

	return instances
}
 