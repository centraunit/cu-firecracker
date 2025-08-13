/*
 * Firecracker CMS - VM Service
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package services

import (
	"context"
	"crypto/md5"
	"encoding/json"
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

	"github.com/centraunit/cu-firecracker-cms/internal/config"
	"github.com/centraunit/cu-firecracker-cms/internal/logger"
	cms_models "github.com/centraunit/cu-firecracker-cms/internal/models"
	"github.com/sirupsen/logrus"
)

// VMService handles Firecracker microVM operations
type VMService struct {
	config          *config.Config
	logger          *logger.Logger
	firecrackerPath string
	kernelPath      string
	snapshotDir     string

	// VM instances with metadata for efficient IP tracking
	vms               map[string]*VMInstance // instanceID -> VM instance with metadata
	vmMutex           sync.RWMutex
	firecrackerLogger *logrus.Entry

	// Pre-warming pool for ultra-fast plugin execution
	prewarmPool map[string][]*PrewarmInstance // pluginSlug -> pool of ready instances
	poolMutex   sync.RWMutex
	maxPoolSize int // Maximum instances per plugin in pool

	// IP allocation for static networking
	ipPool      map[string]bool // IP -> allocated status
	ipPoolMutex sync.RWMutex
	nextIP      net.IP // Next IP to allocate
}

// VMInstance represents a running VM with metadata
type VMInstance struct {
	Machine    *firecracker.Machine
	SocketPath string
	InstanceID string
	PluginSlug string
	IP         string
	CreatedAt  time.Time
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
		config:            cfg,
		logger:            logger.GetDefault(),
		firecrackerPath:   firecrackerPath,
		kernelPath:        kernelPath,
		snapshotDir:       snapshotDir,
		vms:               make(map[string]*VMInstance),
		vmMutex:           sync.RWMutex{},
		firecrackerLogger: logger.GetDefault().WithComponent("firecracker"),
		prewarmPool:       make(map[string][]*PrewarmInstance),
		maxPoolSize:       cfg.PrewarmPoolSize, // Use configurable pool size
		ipPool:            make(map[string]bool),
		ipPoolMutex:       sync.RWMutex{},
		nextIP:            net.ParseIP("192.168.127.2"), // Start from 192.168.127.2
	}

	// Initialize snapshot directory
	if err := service.initSnapshotDir(); err != nil {
		return nil, fmt.Errorf("failed to initialize snapshot directory: %v", err)
	}

	// Load existing IP assignments from plugin registry
	if err := service.loadExistingIPAssignments(); err != nil {
		service.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to load existing IP assignments, continuing with fresh pool")
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

// AddToPrewarmPool adds an instance to the pre-warm pool
func (vm *VMService) AddToPrewarmPool(pluginSlug string, instance *PrewarmInstance) {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	// Check if pool is at capacity
	pool := vm.prewarmPool[pluginSlug]
	if len(pool) >= vm.maxPoolSize {
		// Pool full, stop this instance
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
			"instance_id": instance.InstanceID,
		}).Debug("Pool full, stopping instance")

		if err := vm.StopVM(instance.InstanceID); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instance.InstanceID,
				"error":       err,
			}).Error("Failed to stop excess pre-warm instance")
		}
		return
	}

	// Add to pool
	vm.prewarmPool[pluginSlug] = append(pool, instance)

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": pluginSlug,
		"instance_id": instance.InstanceID,
		"pool_size":   len(vm.prewarmPool[pluginSlug]),
	}).Info("Added instance to pre-warm pool")
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
	}).Info("Starting VM with CNI networking")

	// Get or create TAP interface for this plugin
	tapName, err := vm.createTapInterface(plugin.Slug, instanceID)
	if err != nil {
		return fmt.Errorf("failed to create TAP interface: %v", err)
	}

	// Get or allocate IP for this plugin
	var allocatedIP string
	if plugin.AssignedIP != "" {
		// Use existing assigned IP
		allocatedIP = plugin.AssignedIP
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"assigned_ip": allocatedIP,
		}).Info("Using existing assigned IP")
	} else {
		// Allocate new IP
		allocatedIP = vm.allocateIP()
		if allocatedIP == "" {
			return fmt.Errorf("failed to allocate IP for VM")
		}
		vm.logger.WithFields(logger.Fields{
			"plugin_slug":  plugin.Slug,
			"allocated_ip": allocatedIP,
		}).Info("Allocated new IP")
	}

	// Create socket path for this VM instance
	socketPath := filepath.Join("/tmp/firecracker", fmt.Sprintf("%s.sock", instanceID))

	// Ensure socket directory exists
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		if plugin.AssignedIP == "" {
			vm.deallocateIP(allocatedIP) // Only clean up if we allocated new IP
		}
		return fmt.Errorf("failed to create socket directory: %v", err)
	}

	// Configure kernel arguments with static IP
	kernelArgs := fmt.Sprintf("console=ttyS0 reboot=k panic=1 pci=off ip=%s::192.168.127.1:255.255.255.0::eth0:off", allocatedIP)

	// Create machine configuration with static networking
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: vm.kernelPath,
		KernelArgs:      kernelArgs,
		Drives: []models.Drive{{
			DriveID:      firecracker.String("rootfs"),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(false),
			PathOnHost:   firecracker.String(plugin.RootfsPath),
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:       firecracker.Int64(1),
			MemSizeMib:      firecracker.Int64(512),
			TrackDirtyPages: true, // Enable dirty page tracking for differential snapshots
		},
		NetworkInterfaces: []firecracker.NetworkInterface{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				HostDevName: tapName, // Use the created TAP interface
				MacAddress:  "02:FC:00:00:00:01",
			},
		}},
		VMID: plugin.Slug, // Use plugin name as VMID
	}

	// Create Firecracker machine
	machine, err := firecracker.NewMachine(context.Background(), cfg, firecracker.WithLogger(vm.firecrackerLogger))
	if err != nil {
		return fmt.Errorf("failed to create machine: %v", err)
	}

	// Start the machine
	if err := machine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start machine: %v", err)
	}

	// Store VM instance in tracking with allocated IP
	vm.vmMutex.Lock()
	vm.vms[instanceID] = &VMInstance{
		Machine:    machine,
		SocketPath: socketPath,
		InstanceID: instanceID,
		PluginSlug: plugin.Slug,
		IP:         allocatedIP,
		CreatedAt:  time.Now(),
	}
	vm.vmMutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"instance_id": instanceID,
		"assigned_ip": allocatedIP,
	}).Info("VM started successfully with static networking")

	return nil
}

// StopVM stops and cleans up a VM instance
func (vm *VMService) StopVM(instanceID string) error {
	vm.vmMutex.RLock()
	vmInstance, exists := vm.vms[instanceID]
	vm.vmMutex.RUnlock()

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
	if err := vmInstance.Machine.Shutdown(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to shutdown machine gracefully, attempting force kill")

		// Force kill if graceful shutdown fails
		if killErr := vmInstance.Machine.StopVMM(); killErr != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       killErr,
			}).Error("Failed to force kill machine")
		}
	}

	// Deallocate IP before removing from tracking
	if vmInstance.IP != "" {
		vm.deallocateIP(vmInstance.IP)
	}

	// Remove from tracking maps
	vm.vmMutex.Lock()
	delete(vm.vms, instanceID)
	vm.vmMutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("VM stopped successfully")

	return nil
}

// PauseVM pauses a VM instance (keeps it in memory for instant resume)
func (vm *VMService) PauseVM(instanceID string) error {
	vm.vmMutex.RLock()
	vmInstance, exists := vm.vms[instanceID]
	vm.vmMutex.RUnlock()

	if !exists {
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("Pausing VM for pre-warming")

	// Pause the Firecracker machine
	if err := vmInstance.Machine.PauseVM(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to pause VM")
		return fmt.Errorf("failed to pause VM: %v", err)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("VM paused successfully for pre-warming")

	return nil
}

// ResumeVM resumes a paused VM instance
func (vm *VMService) ResumeVM(instanceID string) error {
	vm.vmMutex.RLock()
	vmInstance, exists := vm.vms[instanceID]
	vm.vmMutex.RUnlock()

	if !exists {
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("Resuming paused VM")

	// Resume the Firecracker machine
	if err := vmInstance.Machine.ResumeVM(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to resume VM")
		return fmt.Errorf("failed to resume VM: %v", err)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("VM resumed successfully")

	return nil
}

// CreateSnapshot creates a snapshot of the running VM
func (vm *VMService) CreateSnapshot(instanceID, snapshotDir string, useDifferential bool) error {
	vm.vmMutex.RLock()
	vmInstance, exists := vm.vms[instanceID]
	vm.vmMutex.RUnlock()

	if !exists {
		return fmt.Errorf("VM instance %s not found", instanceID)
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id":      instanceID,
		"snapshot_dir":     snapshotDir,
		"use_differential": useDifferential,
	}).Info("Creating VM snapshot")

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

	if err := vmInstance.Machine.PauseVM(context.Background()); err != nil {
		return fmt.Errorf("failed to pause VM: %v", err)
	}

	// Ensure VM is resumed after snapshot creation
	defer func() {
		if err := vmInstance.Machine.ResumeVM(context.Background()); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       err,
			}).Error("Failed to resume VM after snapshot")
		}
	}()

	// Create snapshot using the correct Firecracker SDK API
	err := vmInstance.Machine.CreateSnapshot(context.Background(), memPath, statePath)
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
	}).Info("Resuming VM from snapshot with static networking")

	snapshotDir := vm.GetSnapshotPath(plugin.Slug)
	memPath := filepath.Join(snapshotDir, "snapshot.mem")
	statePath := filepath.Join(snapshotDir, "snapshot.state")

	// Check if snapshot files exist
	if !vm.HasSnapshot(plugin.Slug) {
		return fmt.Errorf("snapshot not found for plugin %s", plugin.Slug)
	}

	// Get or create TAP interface for this plugin
	tapName, err := vm.createTapInterface(plugin.Slug, instanceID)
	if err != nil {
		return fmt.Errorf("failed to create TAP interface: %v", err)
	}

	// Get or allocate IP for this plugin
	var allocatedIP string
	if plugin.AssignedIP != "" {
		// Use existing assigned IP
		allocatedIP = plugin.AssignedIP
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"assigned_ip": allocatedIP,
		}).Info("Using existing assigned IP for snapshot resume")
	} else {
		// Allocate new IP
		allocatedIP = vm.allocateIP()
		if allocatedIP == "" {
			return fmt.Errorf("failed to allocate IP for VM")
		}
		vm.logger.WithFields(logger.Fields{
			"plugin_slug":  plugin.Slug,
			"allocated_ip": allocatedIP,
		}).Info("Allocated new IP for snapshot resume")
	}

	// Configure kernel arguments with static IP
	kernelArgs := fmt.Sprintf("console=ttyS0 reboot=k panic=1 pci=off ip=%s::192.168.127.1:255.255.255.0::eth0:off", allocatedIP)

	// Create machine configuration with static networking and snapshot
	cfg := firecracker.Config{
		SocketPath:      fmt.Sprintf("/tmp/firecracker/%s.sock", instanceID),
		KernelImagePath: vm.kernelPath,
		KernelArgs:      kernelArgs,
		Drives: []models.Drive{{
			DriveID:      firecracker.String("rootfs"),
			PathOnHost:   firecracker.String(plugin.RootfsPath),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(false),
		}},
		NetworkInterfaces: []firecracker.NetworkInterface{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				HostDevName: tapName, // Use the created TAP interface
				MacAddress:  "02:FC:00:00:00:01",
			},
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:       firecracker.Int64(1),
			MemSizeMib:      firecracker.Int64(512),
			TrackDirtyPages: true, // Enable for differential snapshots
		},
		LogLevel: "Info",
		VMID:     plugin.Slug, // Use plugin name as VMID
	}

	// Create Firecracker logger
	firecrackerLogger := vm.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"instance_id": instanceID,
	})

	// Create machine with snapshot and static networking
	machine, err := firecracker.NewMachine(
		context.Background(),
		cfg,
		firecracker.WithLogger(firecrackerLogger),
		firecracker.WithSnapshot(memPath, statePath),
	)
	if err != nil {
		return fmt.Errorf("failed to create machine from snapshot: %v", err)
	}

	// Start the machine (this will resume from snapshot and setup static networking)
	if err := machine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start machine from snapshot: %v", err)
	}

	// Store VM instance in tracking with allocated IP
	vm.vmMutex.Lock()
	vm.vms[instanceID] = &VMInstance{
		Machine:    machine,
		SocketPath: fmt.Sprintf("/tmp/firecracker/%s.sock", instanceID),
		InstanceID: instanceID,
		PluginSlug: plugin.Slug,
		IP:         allocatedIP,
		CreatedAt:  time.Now(),
	}
	vm.vmMutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"plugin_slug": plugin.Slug,
		"ip":          allocatedIP,
		"tap_name":    tapName,
		"networking":  "static",
	}).Info("VM resumed from snapshot successfully with static networking")

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
	vm.vmMutex.RLock()
	defer vm.vmMutex.RUnlock()

	vmInstance, exists := vm.vms[instanceID]
	if !exists {
		return "", false
	}
	return vmInstance.IP, true
}

// ListVMs returns a list of running VM instance IDs
func (vm *VMService) ListVMs() []string {
	vm.vmMutex.RLock()
	defer vm.vmMutex.RUnlock()

	instanceIDs := make([]string, 0, len(vm.vms))
	for instanceID := range vm.vms {
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
	vm.vmMutex.Lock()
	defer vm.vmMutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"count": len(vm.vms),
	}).Info("Stopping all running VMs")

	for instanceID, vmInstance := range vm.vms {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("Stopping VM")

		// Attempt graceful shutdown first
		if err := vmInstance.Machine.Shutdown(ctx); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       err,
			}).Warn("Graceful shutdown failed, forcing stop")
			// Force stop if graceful shutdown fails
			vmInstance.Machine.StopVMM()
		}

		// CNI handles network cleanup automatically
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("CNI handles network cleanup automatically")
	}

	// Clear all instances
	vm.vms = make(map[string]*VMInstance)

	vm.logger.Info("All VMs stopped successfully")
}

// Helper functions

// GetTapNameForPlugin generates the TAP name for a plugin
func (vm *VMService) GetTapNameForPlugin(pluginSlug string) string {
	// Generate unique TAP name using MD5 hash (max 15 chars for interface names)
	// Format: tap-{first 8 chars of MD5 hash}
	hash := md5.Sum([]byte(pluginSlug))
	hashHex := fmt.Sprintf("%x", hash)
	shortHash := hashHex[:8]
	return fmt.Sprintf("tap-%s", shortHash)
}

// createTapInterface creates a TAP interface for a plugin
func (vm *VMService) createTapInterface(pluginSlug string, instanceID string) (string, error) {
	// Generate unique TAP name using MD5 hash of plugin + instance (max 15 chars for interface names)
	// Format: tap-{first 8 chars of MD5 hash}
	uniqueID := fmt.Sprintf("%s-%s", pluginSlug, instanceID)
	hash := md5.Sum([]byte(uniqueID))
	hashHex := fmt.Sprintf("%x", hash)
	shortHash := hashHex[:8]
	tapName := fmt.Sprintf("tap-%s", shortHash)

	// Check if TAP already exists
	if vm.tapExists(tapName) {
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
		}).Debug("TAP interface already exists")
		return tapName, nil
	}

	// Create TAP interface using ip command
	cmd := exec.Command("ip", "tuntap", "add", tapName, "mode", "tap")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to create TAP interface %s: %v", tapName, err)
	}

	// Set TAP interface up
	cmd = exec.Command("ip", "link", "set", tapName, "up")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to set TAP interface %s up: %v", tapName, err)
	}

	// Add TAP interface to the bridge
	cmd = exec.Command("brctl", "addif", "fcnetbridge0", tapName)
	if err := cmd.Run(); err != nil {
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
			"error":    err,
		}).Warn("Failed to add TAP to bridge (may already be added)")
	}

	vm.logger.WithFields(logger.Fields{
		"tap_name":    tapName,
		"plugin_slug": pluginSlug,
		"instance_id": instanceID,
		"bridge":      "fcnetbridge0",
	}).Info("Created TAP interface and added to bridge")

	return tapName, nil
}

// tapExists checks if a TAP interface exists
func (vm *VMService) tapExists(tapName string) bool {
	cmd := exec.Command("ip", "link", "show", tapName)
	return cmd.Run() == nil
}

// deleteTapInterface deletes a TAP interface
func (vm *VMService) deleteTapInterface(tapName string) error {
	if !vm.tapExists(tapName) {
		return nil // Already deleted
	}

	// Remove TAP interface from bridge first
	cmd := exec.Command("brctl", "delif", "fcnetbridge0", tapName)
	if err := cmd.Run(); err != nil {
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
			"error":    err,
		}).Warn("Failed to remove TAP from bridge (may not be connected)")
	}

	// Delete TAP interface
	cmd = exec.Command("ip", "link", "delete", tapName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to delete TAP interface %s: %v", tapName, err)
	}

	vm.logger.WithFields(logger.Fields{
		"tap_name": tapName,
		"bridge":   "fcnetbridge0",
	}).Info("Deleted TAP interface and removed from bridge")

	return nil
}

// allocateIP allocates a unique IP address for a VM instance
func (vm *VMService) allocateIP() string {
	vm.ipPoolMutex.Lock()
	defer vm.ipPoolMutex.Unlock()

	// Find the next available IP
	for i := 0; i < 254; i++ { // 192.168.127.2 to 192.168.127.255
		ipStr := vm.nextIP.String()

		if !vm.ipPool[ipStr] {
			// Allocate this IP
			vm.ipPool[ipStr] = true

			// Move to next IP for future allocations
			vm.nextIP[3]++ // Increment last octet
			if vm.nextIP[3] == 0 {
				vm.nextIP[3] = 2 // Skip .0 and .1, start from .2
			}

			vm.logger.WithFields(logger.Fields{
				"allocated_ip": ipStr,
			}).Debug("Allocated IP for VM")

			return ipStr
		}

		// Try next IP
		vm.nextIP[3]++
		if vm.nextIP[3] == 0 {
			vm.nextIP[3] = 2 // Skip .0 and .1, start from .2
		}
	}

	vm.logger.Error("No available IPs in pool")
	return ""
}

// deallocateIP releases an IP address back to the pool
func (vm *VMService) deallocateIP(ip string) {
	vm.ipPoolMutex.Lock()
	defer vm.ipPoolMutex.Unlock()

	delete(vm.ipPool, ip)

	vm.logger.WithFields(logger.Fields{
		"deallocated_ip": ip,
	}).Debug("Deallocated IP")
}

// loadExistingIPAssignments loads existing IP assignments from the plugin registry
func (vm *VMService) loadExistingIPAssignments() error {
	registryPath := filepath.Join(vm.config.DataDir, "plugins", "plugins.json")

	// Check if registry file exists
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		vm.logger.Debug("Plugin registry not found, starting with fresh IP pool")
		return nil
	}

	// Read plugin registry
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("failed to read plugin registry: %v", err)
	}

	// Parse JSON to get existing IP assignments
	var registry struct {
		Plugins map[string]struct {
			AssignedIP string `json:"assigned_ip"`
			TapDevice  string `json:"tap_device"`
		} `json:"plugins"`
	}

	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("failed to parse plugin registry: %v", err)
	}

	// Mark existing IPs as allocated
	vm.ipPoolMutex.Lock()
	defer vm.ipPoolMutex.Unlock()

	for _, plugin := range registry.Plugins {
		if plugin.AssignedIP != "" {
			vm.ipPool[plugin.AssignedIP] = true
			vm.logger.WithFields(logger.Fields{
				"assigned_ip": plugin.AssignedIP,
				"tap_device":  plugin.TapDevice,
			}).Debug("Loaded existing IP assignment")
		}
	}

	vm.logger.WithFields(logger.Fields{
		"loaded_assignments": len(registry.Plugins),
	}).Info("Loaded existing IP assignments from plugin registry")

	return nil
}

// getVMIPFromStatic retrieves the IP address for a VM instance with static networking
func (vm *VMService) getVMIPFromStatic(instanceID string) string {
	// Get the VM instance from tracking
	vm.vmMutex.RLock()
	vmInstance, exists := vm.vms[instanceID]
	vm.vmMutex.RUnlock()

	if !exists {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("Instance not found in tracking")
		return ""
	}

	// Return the allocated IP for this VM instance
	return vmInstance.IP
}
