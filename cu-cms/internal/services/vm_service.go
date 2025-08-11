/*
 * Firecracker CMS - VM Service
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package services

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"

	"math/rand"

	"github.com/centraunit/cu-firecracker-cms/internal/config"
	"github.com/centraunit/cu-firecracker-cms/internal/logger"
	cms_models "github.com/centraunit/cu-firecracker-cms/internal/models"
)

// VMService handles Firecracker microVM operations
type VMService struct {
	config          *config.Config
	logger          *logger.Logger
	firecrackerPath string
	kernelPath      string
	snapshotDir     string
	instances       map[string]*firecracker.Machine // instanceID -> machine
	vms             map[string]*firecracker.Machine // Alias for backwards compatibility
	ips             map[string]string               // Alias for backwards compatibility
	ipPool          map[string]string               // instanceID -> IP
	usedIPs         map[string]bool                 // Track used IPs
	nextIP          int                             // Next available IP (2-254)
	mutex           sync.RWMutex

	// Pre-warming pool for ultra-fast plugin execution
	prewarmPool map[string][]*PrewarmInstance // pluginSlug -> pool of ready instances
	poolMutex   sync.RWMutex
	maxPoolSize int // Maximum instances per plugin in pool
}

// PrewarmInstance represents a pre-warmed VM instance ready for immediate use
type PrewarmInstance struct {
	InstanceID   string
	Machine      *firecracker.Machine
	IP           string
	TapName      string // Store TAP device name for reuse
	CreatedAt    time.Time
	LastUsed     time.Time
	SnapshotType string // "full" or "differential"
}

// NewVMService creates a new VM service
func NewVMService(cfg *config.Config) (*VMService, error) {
	// Get Firecracker and kernel paths from config or environment
	firecrackerPath := cfg.FirecrackerPath
	if firecrackerPath == "" {
		firecrackerPath = "/usr/local/bin/firecracker"
	}

	kernelPath := cfg.KernelPath
	if kernelPath == "" {
		kernelPath = "/opt/kernel/vmlinux"
	}

	snapshotDir := cfg.SnapshotDir
	if snapshotDir == "" {
		snapshotDir = filepath.Join(cfg.DataDir, "snapshots")
	}

	service := &VMService{
		config:          cfg,
		logger:          logger.GetDefault(),
		firecrackerPath: firecrackerPath,
		kernelPath:      kernelPath,
		snapshotDir:     snapshotDir,
		instances:       make(map[string]*firecracker.Machine),
		vms:             make(map[string]*firecracker.Machine),
		ips:             make(map[string]string),
		ipPool:          make(map[string]string),
		usedIPs:         make(map[string]bool),
		nextIP:          2, // Start from 192.168.127.2
		prewarmPool:     make(map[string][]*PrewarmInstance),
		maxPoolSize:     cfg.PrewarmPoolSize, // Use configurable pool size
	}

	// Initialize snapshot directory
	if err := service.initSnapshotDir(); err != nil {
		return nil, fmt.Errorf("failed to initialize snapshot directory: %v", err)
	}

	// Start pre-warming background process
	go service.prewarmManager()

	service.logger.WithFields(logger.Fields{
		"firecracker_path": firecrackerPath,
		"kernel_path":      kernelPath,
		"snapshot_dir":     snapshotDir,
		"max_pool_size":    service.maxPoolSize,
	}).Info("VM service initialized with pre-warming pool")

	return service, nil
}

// prewarmManager manages the pre-warming pool for ultra-fast plugin execution
func (vm *VMService) prewarmManager() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	vm.logger.Info("Pre-warm manager started")

	for {
		select {
		case <-ticker.C:
			vm.maintainPrewarmPool()
		}
	}
}

// maintainPrewarmPool ensures each active plugin has pre-warmed instances ready
func (vm *VMService) maintainPrewarmPool() {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	// Clean up expired instances (older than 10 minutes)
	cutoffTime := time.Now().Add(-10 * time.Minute)

	for pluginSlug, pool := range vm.prewarmPool {
		var activeInstances []*PrewarmInstance

		for _, instance := range pool {
			if instance.CreatedAt.Before(cutoffTime) {
				// Stop expired instance
				vm.logger.WithFields(logger.Fields{
					"plugin_slug": pluginSlug,
					"instance_id": instance.InstanceID,
				}).Debug("Removing expired pre-warm instance")

				if err := vm.StopVM(instance.InstanceID); err != nil {
					vm.logger.WithFields(logger.Fields{
						"instance_id": instance.InstanceID,
						"error":       err,
					}).Error("Failed to stop expired pre-warm instance")
				}
			} else {
				activeInstances = append(activeInstances, instance)
			}
		}

		vm.prewarmPool[pluginSlug] = activeInstances
	}

	vm.logger.WithFields(logger.Fields{
		"total_pools": len(vm.prewarmPool),
	}).Debug("Pre-warm pool maintenance completed")
}

// GetPrewarmInstance retrieves a ready instance from the pre-warm pool
func (vm *VMService) GetPrewarmInstance(pluginSlug string) *PrewarmInstance {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	pool, exists := vm.prewarmPool[pluginSlug]
	if !exists || len(pool) == 0 {
		return nil
	}

	// Get the first available instance
	instance := pool[0]
	vm.prewarmPool[pluginSlug] = pool[1:]

	instance.LastUsed = time.Now()

	vm.logger.WithFields(logger.Fields{
		"plugin_slug":    pluginSlug,
		"instance_id":    instance.InstanceID,
		"snapshot_type":  instance.SnapshotType,
		"pool_remaining": len(vm.prewarmPool[pluginSlug]),
	}).Info("Retrieved pre-warmed instance")

	return instance
}

// ReturnPrewarmInstance returns an instance to the pool for reuse
func (vm *VMService) ReturnPrewarmInstance(pluginSlug string, instance *PrewarmInstance) {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	// Check if pool is at capacity
	pool := vm.prewarmPool[pluginSlug]
	if len(pool) >= vm.maxPoolSize {
		// Pool full, stop this instance
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
			"instance_id": instance.InstanceID,
		}).Debug("Pool full, stopping returned instance")

		if err := vm.StopVM(instance.InstanceID); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instance.InstanceID,
				"error":       err,
			}).Error("Failed to stop excess pre-warm instance")
		}
		return
	}

	// Add back to pool
	vm.prewarmPool[pluginSlug] = append(pool, instance)

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": pluginSlug,
		"instance_id": instance.InstanceID,
		"pool_size":   len(vm.prewarmPool[pluginSlug]),
	}).Debug("Returned instance to pre-warm pool")
}

// CreateDifferentialSnapshot creates a differential snapshot from the base snapshot
func (vm *VMService) CreateDifferentialSnapshot(instanceID, pluginSlug string) error {
	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"plugin_slug": pluginSlug,
	}).Info("Creating differential snapshot")

	snapshotDir := vm.GetSnapshotPath(pluginSlug)
	timestamp := time.Now().Unix()

	// Differential snapshots use timestamped names
	diffMemPath := filepath.Join(snapshotDir, fmt.Sprintf("diff-%d.mem", timestamp))
	diffStatePath := filepath.Join(snapshotDir, fmt.Sprintf("diff-%d.state", timestamp))

	// Create differential snapshot (only changed memory pages)
	err := vm.CreateSnapshot(instanceID, snapshotDir, true) // useDifferential = true
	if err != nil {
		return fmt.Errorf("failed to create differential snapshot: %v", err)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id":     instanceID,
		"plugin_slug":     pluginSlug,
		"diff_mem_path":   diffMemPath,
		"diff_state_path": diffStatePath,
	}).Info("Differential snapshot created successfully")

	return nil
}

// initSnapshotDir creates the snapshot directory if it doesn't exist
func (vm *VMService) initSnapshotDir() error {
	return os.MkdirAll(vm.snapshotDir, 0755)
}

// StartVM starts a new Firecracker microVM for a plugin
func (vm *VMService) StartVM(instanceID string, plugin *cms_models.Plugin) error {
	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"plugin_slug": plugin.Slug,
	}).Info("Starting VM")

	// Clean up any existing socket file before starting
	socketPath := fmt.Sprintf("/tmp/firecracker-%s.sock", instanceID)
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"socket":      socketPath,
		}).Debug("Failed to remove existing socket, continuing anyway")
	}

	// Allocate IP for this instance
	vmIP, err := vm.allocateIP(instanceID)
	if err != nil {
		return fmt.Errorf("failed to allocate IP for instance %s: %v", instanceID, err)
	}

	// Create network interface with short name (Linux limit: 15 chars)
	tapName := vm.generateTapName(instanceID)

	// Setup network interface
	if err := vm.setupNetworkInterface(tapName, vmIP); err != nil {
		vm.deallocateIP(instanceID)
		return fmt.Errorf("failed to setup network interface: %v", err)
	}

	// Convert to structured logger compatible with Firecracker SDK
	firecrackerLogger := vm.logger.WithComponent("firecracker")

	// Create machine configuration
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: vm.kernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off",
		Drives: []models.Drive{{
			DriveID:      firecracker.String("rootfs"),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(false),
			PathOnHost:   firecracker.String(plugin.RootfsPath),
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(512),
			// Enable dirty page tracking for differential snapshots
			TrackDirtyPages: true,
		},
		NetworkInterfaces: []firecracker.NetworkInterface{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				MacAddress:  "02:FC:00:00:03:04",
				HostDevName: tapName,
				IPConfiguration: &firecracker.IPConfiguration{
					IPAddr: net.IPNet{
						IP:   net.ParseIP(vmIP),
						Mask: net.CIDRMask(24, 32),
					},
					Gateway:     net.ParseIP("192.168.127.1"),
					Nameservers: []string{"8.8.8.8"},
				},
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
		vm.cleanupNetworkInterface(tapName, vmIP, instanceID)
		os.Remove(socketPath) // Clean up socket on failure
		return fmt.Errorf("failed to create machine: %v", err)
	}

	// Start the machine
	if err := machine.Start(context.Background()); err != nil {
		vm.deallocateIP(instanceID)
		vm.cleanupNetworkInterface(tapName, vmIP, instanceID)
		os.Remove(socketPath) // Clean up socket on failure
		return fmt.Errorf("failed to start machine: %v", err)
	}

	// Store the machine instance
	vm.mutex.Lock()
	vm.instances[instanceID] = machine
	vm.ipPool[instanceID] = vmIP
	vm.mutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"ip":          vmIP,
	}).Info("VM started successfully")

	return nil
}

// StopVM stops and cleans up a VM instance
func (vm *VMService) StopVM(instanceID string) error {
	vm.mutex.RLock()
	machine, exists := vm.instances[instanceID]
	vmIP := vm.ipPool[instanceID]
	vm.mutex.RUnlock()

	if !exists {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("VM instance not found, already stopped")
		return nil
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("Stopping VM")

	// Stop the Firecracker machine
	if err := machine.Shutdown(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to shutdown machine gracefully, attempting force kill")

		// Force kill if graceful shutdown fails
		if killErr := machine.StopVMM(); killErr != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       killErr,
			}).Error("Failed to force kill machine")
		}
	}

	// For execution instances (temporary), clean up TAP device
	// For plugin activation (persistent), keep TAP for reuse
	shouldCleanupTap := strings.Contains(instanceID, "-exec-")

	if shouldCleanupTap {
		// This is a temporary execution instance - cleanup everything
		tapName := vm.generateTapName(instanceID)
		vm.cleanupNetworkInterface(tapName, vmIP, instanceID)

		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"tap_name":    tapName,
		}).Debug("Cleaned up execution VM TAP interface")
	} else {
		// This is a plugin activation VM - keep TAP for reuse, just deallocate IP
		vm.deallocateIP(instanceID)

		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("Preserved TAP interface for plugin reuse")
	}

	// Remove from tracking maps
	vm.mutex.Lock()
	delete(vm.instances, instanceID)
	delete(vm.vms, instanceID)
	delete(vm.ips, instanceID)
	delete(vm.ipPool, instanceID)
	vm.mutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("VM stopped successfully")

	return nil
}

// CreateSnapshot creates a snapshot of the running VM
func (vm *VMService) CreateSnapshot(instanceID, snapshotDir string, useDifferential bool) error {
	vm.logger.WithFields(logger.Fields{
		"instance_id":      instanceID,
		"snapshot_dir":     snapshotDir,
		"use_differential": useDifferential,
	}).Info("Creating VM snapshot")

	vmInstance, exists := vm.instances[instanceID]
	if !exists {
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	// Define snapshot file paths
	memPath := filepath.Join(snapshotDir, "snapshot.mem")
	statePath := filepath.Join(snapshotDir, "snapshot.state")

	// For differential snapshots, use different memory file name
	if useDifferential {
		timestamp := time.Now().Unix()
		memPath = filepath.Join(snapshotDir, fmt.Sprintf("snapshot-diff-%d.mem", timestamp))
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"mem_path":    memPath,
		}).Info("Creating differential snapshot")
	}

	// Pause VM before creating snapshot
	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Debug("Pausing VM for snapshot creation")

	if err := vmInstance.PauseVM(context.Background()); err != nil {
		return fmt.Errorf("failed to pause VM: %v", err)
	}

	// Ensure VM is resumed after snapshot creation
	defer func() {
		if err := vmInstance.ResumeVM(context.Background()); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       err,
			}).Error("Failed to resume VM after snapshot")
		}
	}()

	// Create snapshot using the correct Firecracker SDK API
	err := vmInstance.CreateSnapshot(context.Background(), memPath, statePath)
	if err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to create snapshot")
		return fmt.Errorf("failed to create snapshot: %v", err)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id":      instanceID,
		"mem_path":         memPath,
		"state_path":       statePath,
		"use_differential": useDifferential,
	}).Info("VM snapshot created successfully")

	return nil
}

// ResumeFromSnapshot creates a new VM instance from an existing snapshot
func (vm *VMService) ResumeFromSnapshot(instanceID string, plugin *cms_models.Plugin) error {
	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"plugin_slug": plugin.Slug,
	}).Info("Resuming VM from snapshot")

	snapshotDir := vm.GetSnapshotPath(plugin.Slug)
	memPath := filepath.Join(snapshotDir, "snapshot.mem")
	statePath := filepath.Join(snapshotDir, "snapshot.state")

	// Check if snapshot files exist
	if !vm.HasSnapshot(plugin.Slug) {
		return fmt.Errorf("snapshot not found for plugin %s", plugin.Slug)
	}

	// Use plugin's assigned network configuration or allocate new
	var vmIP, tapName string
	var err error

	if plugin.AssignedIP != "" && plugin.TapDevice != "" {
		// Reuse existing network configuration
		vmIP = plugin.AssignedIP
		tapName = plugin.TapDevice

		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"assigned_ip": vmIP,
			"tap_device":  tapName,
		}).Info("Reusing plugin's assigned network configuration")
	} else {
		// Allocate new network configuration
		vmIP, err = vm.allocateIP(instanceID)
		if err != nil {
			return fmt.Errorf("failed to allocate IP: %v", err)
		}
		tapName = vm.generatePluginTapName(plugin.Slug)

		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"new_ip":      vmIP,
			"new_tap":     tapName,
		}).Info("Allocated new network configuration for plugin")
	}

	// Check if TAP device exists, create if needed
	tapExists := vm.checkTapExists(tapName)
	if !tapExists {
		if err := vm.setupNetworkInterface(tapName, vmIP); err != nil {
			if plugin.AssignedIP == "" {
				vm.deallocateIP(instanceID)
			}
			return fmt.Errorf("failed to setup network: %v", err)
		}
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
			"vm_ip":    vmIP,
		}).Info("Created TAP device for plugin")
	} else {
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
			"vm_ip":    vmIP,
		}).Info("Reusing existing TAP device")
	}

	// Create machine configuration with snapshot
	cfg := firecracker.Config{
		SocketPath:      fmt.Sprintf("/tmp/firecracker/%s.sock", instanceID),
		KernelImagePath: vm.kernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off",
		Drives: []models.Drive{{
			DriveID:      firecracker.String("rootfs"),
			PathOnHost:   firecracker.String(plugin.RootfsPath),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(false),
		}},
		NetworkInterfaces: []firecracker.NetworkInterface{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				MacAddress:  "AA:FC:00:00:00:01",
				HostDevName: tapName,
				IPConfiguration: &firecracker.IPConfiguration{
					IPAddr: net.IPNet{
						IP:   net.ParseIP(vmIP),
						Mask: net.CIDRMask(24, 32),
					},
					Gateway:     net.ParseIP("192.168.127.1"),
					Nameservers: []string{"8.8.8.8"},
				},
			},
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:       firecracker.Int64(1),
			MemSizeMib:      firecracker.Int64(512),
			TrackDirtyPages: true, // Enable for differential snapshots
		},
		LogLevel: "Info",
	}

	// Create Firecracker logger
	firecrackerLogger := vm.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"instance_id": instanceID,
	})

	// Create machine with snapshot
	machine, err := firecracker.NewMachine(
		context.Background(),
		cfg,
		firecracker.WithLogger(firecrackerLogger),
		firecracker.WithSnapshot(memPath, statePath),
	)
	if err != nil {
		if !tapExists {
			vm.cleanupNetworkInterface(tapName, vmIP, instanceID)
		} else if plugin.AssignedIP == "" {
			vm.deallocateIP(instanceID)
		}
		return fmt.Errorf("failed to create machine from snapshot: %v", err)
	}

	// Start the machine (this will resume from snapshot)
	if err := machine.Start(context.Background()); err != nil {
		if !tapExists {
			vm.cleanupNetworkInterface(tapName, vmIP, instanceID)
		} else if plugin.AssignedIP == "" {
			vm.deallocateIP(instanceID)
		}
		return fmt.Errorf("failed to start machine from snapshot: %v", err)
	}

	// Store the VM instance
	vm.mutex.Lock()
	vm.instances[instanceID] = machine
	vm.vms[instanceID] = machine
	vm.ips[instanceID] = vmIP
	vm.ipPool[instanceID] = vmIP
	vm.mutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"plugin_slug": plugin.Slug,
		"vm_ip":       vmIP,
		"tap_name":    tapName,
	}).Info("VM resumed from snapshot successfully")

	return nil
}

// GetSnapshotPath returns the snapshot directory path for a plugin
func (vm *VMService) GetSnapshotPath(pluginSlug string) string {
	pluginSnapshotDir := filepath.Join(vm.snapshotDir, pluginSlug)
	// Ensure the plugin snapshot directory exists
	if err := os.MkdirAll(pluginSnapshotDir, 0755); err != nil {
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
			"directory":   pluginSnapshotDir,
			"error":       err,
		}).Error("Failed to create plugin snapshot directory")
	}
	return pluginSnapshotDir
}

// HasSnapshot checks if a snapshot exists for the given plugin
func (vm *VMService) HasSnapshot(pluginSlug string) bool {
	snapshotDir := vm.GetSnapshotPath(pluginSlug)
	memPath := filepath.Join(snapshotDir, "snapshot.mem")
	statePath := filepath.Join(snapshotDir, "snapshot.state")

	_, memErr := os.Stat(memPath)
	_, stateErr := os.Stat(statePath)

	return memErr == nil && stateErr == nil
}

// DeleteSnapshot deletes snapshot files for a plugin
func (vm *VMService) DeleteSnapshot(pluginSlug string) error {
	snapshotDir := vm.GetSnapshotPath(pluginSlug)
	memPath := filepath.Join(snapshotDir, "snapshot.mem")
	statePath := filepath.Join(snapshotDir, "snapshot.state")

	var errors []string

	// Delete memory file
	if err := os.Remove(memPath); err != nil && !os.IsNotExist(err) {
		errors = append(errors, fmt.Sprintf("failed to delete %s: %v", memPath, err))
	}

	// Delete state file
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		errors = append(errors, fmt.Sprintf("failed to delete %s: %v", statePath, err))
	}

	// Delete any differential snapshots
	diffFiles, err := filepath.Glob(filepath.Join(snapshotDir, "diff-*.mem"))
	if err == nil {
		for _, diffFile := range diffFiles {
			if err := os.Remove(diffFile); err != nil && !os.IsNotExist(err) {
				errors = append(errors, fmt.Sprintf("failed to delete %s: %v", diffFile, err))
			}
		}
	}

	diffStateFiles, err := filepath.Glob(filepath.Join(snapshotDir, "diff-*.state"))
	if err == nil {
		for _, diffFile := range diffStateFiles {
			if err := os.Remove(diffFile); err != nil && !os.IsNotExist(err) {
				errors = append(errors, fmt.Sprintf("failed to delete %s: %v", diffFile, err))
			}
		}
	}

	// Try to remove the plugin directory if it's empty
	if err := os.Remove(snapshotDir); err != nil && !os.IsNotExist(err) {
		// Directory not empty or other error - this is OK
		vm.logger.WithFields(logger.Fields{
			"plugin_slug":  pluginSlug,
			"snapshot_dir": snapshotDir,
		}).Debug("Could not remove snapshot directory (may not be empty)")
	}

	if len(errors) > 0 {
		return fmt.Errorf("snapshot deletion errors: %s", strings.Join(errors, "; "))
	}

	vm.logger.WithFields(logger.Fields{
		"plugin_slug":  pluginSlug,
		"snapshot_dir": snapshotDir,
		"mem_path":     memPath,
		"state_path":   statePath,
	}).Info("Successfully deleted all snapshot files")

	return nil
}

// GetVMIP returns the allocated IP for an instance
func (vm *VMService) GetVMIP(instanceID string) (string, bool) {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	ip, exists := vm.ipPool[instanceID]
	return ip, exists
}

// ListVMs returns a list of running VM instance IDs
func (vm *VMService) ListVMs() []string {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()

	instanceIDs := make([]string, 0, len(vm.instances))
	for instanceID := range vm.instances {
		instanceIDs = append(instanceIDs, instanceID)
	}

	vm.logger.WithFields(logger.Fields{
		"count":     len(instanceIDs),
		"instances": instanceIDs,
	}).Debug("Listed VM instances")

	return instanceIDs
}

// Shutdown gracefully shuts down the VM service
func (vm *VMService) Shutdown(ctx context.Context) {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"count": len(vm.instances),
	}).Info("Stopping all running VMs")

	for instanceID, machine := range vm.instances {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("Stopping VM")

		// Attempt graceful shutdown first
		if err := machine.Shutdown(ctx); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       err,
			}).Warn("Graceful shutdown failed, forcing stop")
			// Force stop if graceful shutdown fails
			machine.StopVMM()
		}

		// Clean up IP allocation
		vm.deallocateIP(instanceID)
	}

	// Clear all instances
	vm.instances = make(map[string]*firecracker.Machine)

	vm.logger.Info("All VMs stopped successfully")
}

// Helper functions

// allocateIP allocates a unique IP address for a VM instance
func (vm *VMService) allocateIP(instanceID string) (string, error) {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	// Check if this instance already has an IP
	if ip, exists := vm.ipPool[instanceID]; exists {
		vm.logger.WithFields(logger.Fields{
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
			vm.logger.WithFields(logger.Fields{
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
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"ip":          ipStr,
			}).Debug("Allocated IP (wrapped)")
			return ipStr, nil
		}
	}

	return "", fmt.Errorf("no available IPs")
}

// deallocateIP releases an IP address when a VM is stopped
func (vm *VMService) deallocateIP(instanceID string) {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	if ip, exists := vm.ipPool[instanceID]; exists {
		delete(vm.usedIPs, ip)
		delete(vm.ipPool, instanceID)
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"ip":          ip,
		}).Debug("Deallocated IP")
	}
}

// setupNetworkInterface creates and configures the tap interface for the VM
func (vm *VMService) setupNetworkInterface(tapName, vmIP string) error {
	bridgeName := "fc-br"

	vm.logger.WithFields(logger.Fields{
		"tap_name": tapName,
	}).Debug("Setting up network interface")

	// Check if bridge exists and create if needed
	if _, err := exec.Command("ip", "link", "show", bridgeName).CombinedOutput(); err != nil {
		vm.logger.WithFields(logger.Fields{
			"bridge": bridgeName,
		}).Debug("Creating bridge")
		// Bridge doesn't exist, create it
		if output, err := exec.Command("brctl", "addbr", bridgeName).CombinedOutput(); err != nil {
			return fmt.Errorf("failed to create bridge %s: %v, output: %s", bridgeName, err, string(output))
		}
	}

	// Add IP to bridge and bring it up
	vm.logger.WithFields(logger.Fields{
		"bridge": bridgeName,
		"ip":     "192.168.127.1/24",
	}).Debug("Adding IP to bridge")
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
	vm.logger.WithFields(logger.Fields{
		"tap": tapName,
	}).Debug("Creating tap interface")
	if output, err := exec.Command("ip", "tuntap", "add", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create tap interface %s: %v, output: %s", tapName, err, string(output))
	}

	// Bring tap interface up
	vm.logger.WithFields(logger.Fields{
		"tap": tapName,
	}).Debug("Bringing up tap interface")
	if output, err := exec.Command("ip", "link", "set", "dev", tapName, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to bring tap interface up: %v, output: %s", err, string(output))
	}

	// Add tap to bridge
	vm.logger.WithFields(logger.Fields{
		"tap":    tapName,
		"bridge": bridgeName,
	}).Debug("Adding tap to bridge")
	if output, err := exec.Command("brctl", "addif", bridgeName, tapName).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add tap to bridge: %v, output: %s", err, string(output))
	}

	vm.logger.WithFields(logger.Fields{
		"tap":    tapName,
		"bridge": bridgeName,
	}).Debug("Network interface setup completed successfully")

	return nil
}

// generateTapName creates a short, unique tap interface name (max 15 chars for Linux)
func (vm *VMService) generateTapName(instanceID string) string {
	// Linux interface names are limited to 15 characters
	// Use format: "fc-" + 8-char hash = 11 characters total
	hash := md5.Sum([]byte(instanceID))
	shortHash := hex.EncodeToString(hash[:4]) // 8 characters
	return fmt.Sprintf("fc-%s", shortHash)
}

// generatePluginTapName generates a consistent TAP device name for a plugin
func (vm *VMService) generatePluginTapName(pluginSlug string) string {
	// Generate a short random TAP name that fits within 15-character Linux limit
	// Format: fc-{8 random chars} = 11 characters total
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return fmt.Sprintf("fc-%s", string(b))
}

// checkTapExists checks if a TAP device with the given name already exists
func (vm *VMService) checkTapExists(tapName string) bool {
	// Check if the tap device exists
	if _, err := exec.Command("ip", "link", "show", tapName).CombinedOutput(); err == nil {
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
		}).Debug("TAP device already exists")
		return true
	}
	vm.logger.WithFields(logger.Fields{
		"tap_name": tapName,
	}).Debug("TAP device does not exist")
	return false
}

// cleanupNetworkInterface deletes the tap interface if it exists
func (vm *VMService) cleanupNetworkInterface(tapName, vmIP, instanceID string) {
	vm.logger.WithFields(logger.Fields{
		"tap": tapName,
	}).Debug("Cleaning up tap interface")
	cmd := exec.Command("ip", "link", "delete", tapName)
	if err := cmd.Run(); err != nil {
		vm.logger.WithFields(logger.Fields{
			"tap":   tapName,
			"error": err,
		}).Debug("Failed to clean up tap interface")
	} else {
		vm.logger.WithFields(logger.Fields{
			"tap": tapName,
		}).Debug("Cleaned up tap interface")
	}
}

// CleanupPluginNetwork removes TAP device and frees IP for a plugin
func (vm *VMService) CleanupPluginNetwork(plugin *cms_models.Plugin) error {
	if plugin.TapDevice == "" && plugin.AssignedIP == "" {
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
		}).Debug("No network configuration to cleanup")
		return nil
	}

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"tap_device":  plugin.TapDevice,
		"assigned_ip": plugin.AssignedIP,
	}).Info("Cleaning up plugin network configuration")

	// Remove TAP device if it exists
	if plugin.TapDevice != "" {
		if vm.checkTapExists(plugin.TapDevice) {
			cmd := exec.Command("ip", "link", "delete", plugin.TapDevice)
			if err := cmd.Run(); err != nil {
				vm.logger.WithFields(logger.Fields{
					"plugin_slug": plugin.Slug,
					"tap_device":  plugin.TapDevice,
					"error":       err,
				}).Error("Failed to delete TAP device")
				return fmt.Errorf("failed to delete TAP device %s: %v", plugin.TapDevice, err)
			}
			vm.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"tap_device":  plugin.TapDevice,
			}).Info("Deleted TAP device")
		}
	}

	// Free the IP address from our pool
	if plugin.AssignedIP != "" {
		vm.mutex.Lock()
		for instanceID, ip := range vm.ipPool {
			if ip == plugin.AssignedIP {
				vm.deallocateIP(instanceID)
				break
			}
		}
		vm.mutex.Unlock()

		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"freed_ip":    plugin.AssignedIP,
		}).Info("Freed IP address")
	}

	return nil
}
