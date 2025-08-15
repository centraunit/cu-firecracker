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

	firecrackerLogger *logrus.Entry

	// Pre-warming pool for ultra-fast plugin execution
	prewarmPool map[string]*PrewarmInstance // instanceID -> prewarm instance
	poolMutex   sync.RWMutex
	maxPoolSize int // Maximum instances per plugin in pool

	// IP allocation for static networking
	ipPool      map[string]bool // IP -> allocated status
	ipPoolMutex sync.RWMutex
	nextIP      net.IP // Next IP to allocate
}

// PrewarmInstance represents a pre-warmed VM instance ready for immediate use
type PrewarmInstance struct {
	InstanceID   string
	Machine      *firecracker.Machine // Store the actual machine for operations
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
		firecrackerLogger: logger.GetDefault().WithComponent("firecracker"),
		prewarmPool:       make(map[string]*PrewarmInstance),
		maxPoolSize:       cfg.PrewarmPoolSize, // Use configurable pool size
		ipPool:            make(map[string]bool),
		ipPoolMutex:       sync.RWMutex{},
		nextIP:            net.ParseIP("192.168.127.2"), // Start from 192.168.127.2
	}

	// Initialize snapshot directory
	if err := service.initSnapshotDir(); err != nil {
		return nil, fmt.Errorf("failed to initialize snapshot directory: %v", err)
	}

	// Clean up orphaned resources and validate persisted state
	if err := service.cleanupAndValidateState(); err != nil {
		service.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to cleanup and validate state, continuing with fresh state")
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
		"mode":             service.config.GetModeString(),
	}).Info("VM service initialized with pre-warming pool")

	// Mode-specific initialization messages
	if service.config.IsDevelopmentMode() {
		service.logger.Info("🔧 Development mode: Enhanced debugging and verbose logging enabled")
		service.logger.Info("⚡ Pre-warming pool size: " + fmt.Sprintf("%d", service.maxPoolSize))
	} else if service.config.IsTestMode() {
		service.logger.Info("🧪 Test mode: Comprehensive testing environment")
		service.logger.Info("⚡ Pre-warming pool size: " + fmt.Sprintf("%d", service.maxPoolSize))
	} else {
		service.logger.Info("🏭 Production mode: Optimized for performance and reliability")
		service.logger.Info("⚡ Pre-warming pool size: " + fmt.Sprintf("%d", service.maxPoolSize))
	}

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

	for pluginSlug, instance := range vm.prewarmPool {
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

			// Remove from pool
			delete(vm.prewarmPool, pluginSlug)
		}
	}

	vm.logger.WithFields(logger.Fields{
		"total_pools": len(vm.prewarmPool),
	}).Debug("Pre-warm pool maintenance completed")
}

// GetPrewarmInstance retrieves a ready instance from the pre-warm pool
func (vm *VMService) GetPrewarmInstance(pluginSlug string) *PrewarmInstance {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	instance, exists := vm.prewarmPool[pluginSlug]
	if !exists {
		return nil
	}

	instance.LastUsed = time.Now()

	vm.logger.WithFields(logger.Fields{
		"plugin_slug":   pluginSlug,
		"instance_id":   instance.InstanceID,
		"snapshot_type": instance.SnapshotType,
	}).Info("Retrieved pre-warmed instance")

	return instance
}

// ReturnPrewarmInstance returns an instance to the pool for reuse
func (vm *VMService) ReturnPrewarmInstance(pluginSlug string, instance *PrewarmInstance) {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	// Simply add back to pool (one instance per plugin)
	vm.prewarmPool[pluginSlug] = instance

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": pluginSlug,
		"instance_id": instance.InstanceID,
	}).Debug("Returned instance to pre-warm pool")
}

// AddToPrewarmPool adds an instance to the pre-warm pool
func (vm *VMService) AddToPrewarmPool(pluginSlug string, instance *PrewarmInstance) {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	// Add to pool (one instance per plugin)
	vm.prewarmPool[pluginSlug] = instance

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": pluginSlug,
		"instance_id": instance.InstanceID,
	}).Info("Added instance to pre-warm pool")
}

// RemoveFromPrewarmPool removes an instance from the pre-warm pool
func (vm *VMService) RemoveFromPrewarmPool(pluginSlug string) {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	if instance, exists := vm.prewarmPool[pluginSlug]; exists {
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
			"instance_id": instance.InstanceID,
		}).Info("Removing instance from pre-warm pool")
		delete(vm.prewarmPool, pluginSlug)
	} else {
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": pluginSlug,
		}).Debug("No instance found in pre-warm pool to remove")
	}
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
	return vm.createVM(instanceID, plugin, false, "", "")
}

// ResumeFromSnapshot creates a new VM instance from an existing snapshot
func (vm *VMService) ResumeFromSnapshot(instanceID string, plugin *cms_models.Plugin) error {
	snapshotDir := vm.GetSnapshotPath(plugin.Slug)
	memPath := filepath.Join(snapshotDir, "snapshot.mem")
	statePath := filepath.Join(snapshotDir, "snapshot.state")

	// Check if snapshot files exist
	if !vm.HasSnapshot(plugin.Slug) {
		return fmt.Errorf("snapshot not found for plugin %s", plugin.Slug)
	}

	return vm.createVM(instanceID, plugin, true, memPath, statePath)
}

// createVM is the unified method for creating VMs (fresh or from snapshot)
func (vm *VMService) createVM(instanceID string, plugin *cms_models.Plugin, useSnapshot bool, memPath, statePath string) error {
	vmType := "fresh VM"
	if useSnapshot {
		vmType = "VM from snapshot"
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
		"plugin_slug": plugin.Slug,
		"vm_type":     vmType,
	}).Info("Creating VM with static networking")

	// Get or create TAP interface for this plugin
	tapName, err := vm.getOrCreateTapInterface(plugin, instanceID)
	if err != nil {
		return fmt.Errorf("failed to setup TAP interface: %v", err)
	}

	// Get or allocate IP for this plugin
	allocatedIP, err := vm.getOrAllocateIP(plugin)
	if err != nil {
		return fmt.Errorf("failed to setup IP: %v", err)
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

	// Create machine configuration
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
				HostDevName: tapName,
				MacAddress:  "02:FC:00:00:00:01",
			},
		}},
		VMID: plugin.Slug, // Use plugin name as VMID
	}

	// Add snapshot-specific configuration if needed
	if useSnapshot {
		cfg.LogLevel = "Info"
	}

	// Create Firecracker machine
	var machine *firecracker.Machine
	if useSnapshot {
		machine, err = firecracker.NewMachine(
			context.Background(),
			cfg,
			firecracker.WithLogger(vm.firecrackerLogger),
			firecracker.WithSnapshot(memPath, statePath),
		)
	} else {
		machine, err = firecracker.NewMachine(context.Background(), cfg, firecracker.WithLogger(vm.firecrackerLogger))
	}

	if err != nil {
		return fmt.Errorf("failed to create machine: %v", err)
	}

	// Start the machine
	if err := machine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start machine: %v", err)
	}

	// Store VM instance in prewarm pool with allocated IP
	snapshotType := "none"
	if useSnapshot {
		snapshotType = "full"
	}

	vm.poolMutex.Lock()
	vm.prewarmPool[instanceID] = &PrewarmInstance{
		InstanceID:   instanceID,
		Machine:      machine,
		IP:           allocatedIP,
		TapName:      tapName,
		CreatedAt:    time.Now(),
		LastUsed:     time.Now(),
		SnapshotType: snapshotType,
	}
	vm.poolMutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"plugin_slug": plugin.Slug,
		"instance_id": instanceID,
		"assigned_ip": allocatedIP,
		"tap_name":    tapName,
		"vm_type":     vmType,
	}).Info("VM created successfully with static networking")

	return nil
}

// StopVM stops and cleans up a VM instance
func (vm *VMService) StopVM(instanceID string) error {
	vm.poolMutex.RLock()
	instance, exists := vm.prewarmPool[instanceID]
	vm.poolMutex.RUnlock()

	if !exists {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("VM instance not found, already stopped")
		return nil
	}

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("Stopping VM")

	// For paused VMs, we need to resume first before shutting down
	// This is because SendCtrlAltDel doesn't work on paused VMs
	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Debug("Resuming VM before shutdown (in case it's paused)")

	if err := instance.Machine.ResumeVM(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Debug("VM was not paused or already running")
		// This is OK - the VM might already be running
	}

	// Give the VM a moment to fully resume
	time.Sleep(100 * time.Millisecond)

	// Stop the Firecracker machine
	if err := instance.Machine.Shutdown(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to shutdown machine gracefully, attempting force kill")

		// Force kill if graceful shutdown fails
		if killErr := instance.Machine.StopVMM(); killErr != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       killErr,
			}).Error("Failed to force kill machine")
		}
	}

	// Wait for the Firecracker process to actually finish
	// This is crucial - the SDK methods above only send signals, but don't wait for the process to exit
	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Debug("Waiting for Firecracker process to exit")

	if err := instance.Machine.Wait(context.Background()); err != nil {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
			"error":       err,
		}).Error("Failed to wait for Firecracker process to exit")
		// Continue with cleanup even if wait fails
	}

	// Deallocate IP before removing from tracking
	if instance.IP != "" {
		vm.deallocateIP(instance.IP)
	}

	// Remove from prewarm pool
	vm.poolMutex.Lock()
	delete(vm.prewarmPool, instanceID)
	vm.poolMutex.Unlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("VM stopped successfully")

	return nil
}

// PauseVM pauses a VM instance (keeps it in memory for instant resume)
func (vm *VMService) PauseVM(instanceID string) error {
	vm.poolMutex.RLock()
	instance, exists := vm.prewarmPool[instanceID]
	if !exists {
		vm.poolMutex.RUnlock()
		return fmt.Errorf("VM instance %s not found", instanceID)
	}
	// Keep the lock while we use the instance to prevent race conditions
	defer vm.poolMutex.RUnlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("Pausing VM for pre-warming")

	// Pause the Firecracker machine
	if err := instance.Machine.PauseVM(context.Background()); err != nil {
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
	vm.poolMutex.RLock()
	instance, exists := vm.prewarmPool[instanceID]
	if !exists {
		vm.poolMutex.RUnlock()
		return fmt.Errorf("VM instance %s not found", instanceID)
	}
	// Keep the lock while we use the instance to prevent race conditions
	defer vm.poolMutex.RUnlock()

	vm.logger.WithFields(logger.Fields{
		"instance_id": instanceID,
	}).Info("Resuming paused VM")

	// Resume the Firecracker machine
	if err := instance.Machine.ResumeVM(context.Background()); err != nil {
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
	vm.poolMutex.RLock()
	instance, exists := vm.prewarmPool[instanceID]
	if !exists {
		vm.poolMutex.RUnlock()
		return fmt.Errorf("VM instance %s not found", instanceID)
	}
	// Keep the lock while we use the instance to prevent race conditions
	defer vm.poolMutex.RUnlock()

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

	if err := instance.Machine.PauseVM(context.Background()); err != nil {
		return fmt.Errorf("failed to pause VM: %v", err)
	}

	// Ensure VM is resumed after snapshot creation
	defer func() {
		if err := instance.Machine.ResumeVM(context.Background()); err != nil {
			vm.logger.WithFields(logger.Fields{
				"instance_id": instanceID,
				"error":       err,
			}).Error("Failed to resume VM after snapshot")
		}
	}()

	// Create snapshot using the correct Firecracker SDK API
	err := instance.Machine.CreateSnapshot(context.Background(), memPath, statePath)
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

// GetVMIP returns the allocated IP for an instance from prewarm pool
func (vm *VMService) GetVMIP(instanceID string) (string, bool) {
	vm.poolMutex.RLock()
	defer vm.poolMutex.RUnlock()
	instance, exists := vm.prewarmPool[instanceID]
	if !exists {
		return "", false
	}
	return instance.IP, true
}

// ListVMs returns a list of running VM instance IDs from prewarm pool
func (vm *VMService) ListVMs() []string {
	vm.poolMutex.RLock()
	defer vm.poolMutex.RUnlock()

	instanceIDs := make([]string, 0)
	for _, instance := range vm.prewarmPool {
		instanceIDs = append(instanceIDs, instance.InstanceID)
	}

	vm.logger.WithFields(logger.Fields{
		"count":     len(instanceIDs),
		"instances": instanceIDs,
	}).Debug("Listed VM instances from prewarm pool")

	return instanceIDs
}

// Shutdown gracefully shuts down the VM service
func (vm *VMService) Shutdown(ctx context.Context) {
	vm.poolMutex.Lock()
	defer vm.poolMutex.Unlock()

	totalInstances := len(vm.prewarmPool)

	vm.logger.WithFields(logger.Fields{
		"count": totalInstances,
	}).Info("Stopping all VMs in prewarm pool")

	// Stop all VMs in the prewarm pool
	for pluginSlug, instance := range vm.prewarmPool {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instance.InstanceID,
			"plugin_slug": pluginSlug,
		}).Debug("Stopping VM from prewarm pool")

		// Get the machine from prewarm pool
		if instance.Machine != nil {
			// Attempt graceful shutdown first
			if err := instance.Machine.Shutdown(ctx); err != nil {
				vm.logger.WithFields(logger.Fields{
					"instance_id": instance.InstanceID,
					"error":       err,
				}).Warn("Graceful shutdown failed, forcing stop")
				// Force stop if graceful shutdown fails
				instance.Machine.StopVMM()
			}

			// Wait for the Firecracker process to actually finish
			vm.logger.WithFields(logger.Fields{
				"instance_id": instance.InstanceID,
			}).Debug("Waiting for Firecracker process to exit")

			if err := instance.Machine.Wait(ctx); err != nil {
				vm.logger.WithFields(logger.Fields{
					"instance_id": instance.InstanceID,
					"error":       err,
				}).Error("Failed to wait for Firecracker process to exit")
			}
		}

		// Static networking cleanup is handled by TAP interface management
		vm.logger.WithFields(logger.Fields{
			"instance_id": instance.InstanceID,
		}).Debug("Static networking cleanup handled by TAP interface management")
	}

	// Clear all instances from prewarm pool
	vm.prewarmPool = make(map[string]*PrewarmInstance)

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

// ensureTapUp ensures a TAP interface is up and ready
func (vm *VMService) ensureTapUp(tapName string) error {
	vm.logger.WithFields(logger.Fields{
		"tap_name": tapName,
	}).Debug("Ensuring TAP interface is up")

	// Check current state
	cmd := exec.Command("ip", "link", "show", tapName)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check TAP interface %s: %v", tapName, err)
	}

	// Check if interface is up
	if strings.Contains(string(output), "state UP") {
		vm.logger.WithFields(logger.Fields{
			"tap_name": tapName,
		}).Debug("TAP interface is already up")
		return nil
	}

	// Bring interface up
	cmd = exec.Command("ip", "link", "set", tapName, "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to bring up TAP interface %s: %v", tapName, err)
	}

	vm.logger.WithFields(logger.Fields{
		"tap_name": tapName,
	}).Info("TAP interface brought up successfully")

	return nil
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

// cleanupAndValidateState cleans up orphaned resources and validates persisted state
func (vm *VMService) cleanupAndValidateState() error {
	modeStr := vm.config.GetModeString()
	vm.logger.WithFields(logger.Fields{
		"mode": modeStr,
	}).Info("Starting state cleanup and validation")

	// Mode-specific cleanup messages
	if vm.config.IsDevelopmentMode() {
		vm.logger.Info("🧹 Development mode: Aggressive cleanup of orphaned resources")
	} else if vm.config.IsTestMode() {
		vm.logger.Info("🧹 Test mode: Thorough cleanup for clean test environment")
	} else {
		vm.logger.Info("🧹 Production mode: Conservative cleanup for stability")
	}

	// Step 1: Clean up orphaned TAP interfaces (only network cleanup needed)
	if err := vm.cleanupOrphanedTapInterfaces(); err != nil {
		vm.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to cleanup orphaned TAP interfaces")
	}

	// Step 2: Firecracker SDK handles process and socket cleanup automatically
	vm.logger.Debug("Firecracker SDK handles process and socket cleanup automatically")

	// Step 3: Validate and clean up plugin registry state
	if err := vm.validatePluginRegistryState(); err != nil {
		vm.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to validate plugin registry state")
	}

	vm.logger.Info("State cleanup and validation completed")
	return nil
}

// cleanupOrphanedTapInterfaces removes TAP interfaces that are not in use
func (vm *VMService) cleanupOrphanedTapInterfaces() error {
	vm.logger.Debug("Cleaning up orphaned TAP interfaces")

	// First, get the list of TAP devices needed for active plugins
	activeTapDevices := vm.getActivePluginTapDevices()
	vm.logger.WithFields(logger.Fields{
		"active_tap_devices": activeTapDevices,
	}).Debug("Identified TAP devices for active plugins")

	// Get all TAP interfaces
	cmd := exec.Command("ip", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list network interfaces: %v", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "tap-") {
			// Extract TAP interface name
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				tapName := strings.TrimSuffix(fields[1], ":")
				if strings.HasPrefix(tapName, "tap-") {
					// Check if this TAP is needed for an active plugin
					if activeTapDevices[tapName] {
						vm.logger.WithFields(logger.Fields{
							"tap_name": tapName,
						}).Debug("Preserving TAP interface for active plugin")
						continue
					}

					// Remove orphaned TAP interface (Firecracker SDK handles process management)
					vm.logger.WithFields(logger.Fields{
						"tap_name": tapName,
					}).Debug("Removing orphaned TAP interface")

					if err := vm.deleteTapInterface(tapName); err != nil {
						vm.logger.WithFields(logger.Fields{
							"tap_name": tapName,
							"error":    err,
						}).Warn("Failed to remove orphaned TAP interface")
					}
				}
			}
		}
	}

	return nil
}

// validatePluginRegistryState validates and cleans up plugin registry state
func (vm *VMService) validatePluginRegistryState() error {
	vm.logger.Debug("Validating plugin registry state")

	registryPath := filepath.Join(vm.config.DataDir, "plugins", "plugins.json")
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		return nil // No registry file to validate
	}

	// Read plugin registry
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("failed to read plugin registry: %v", err)
	}

	var registry struct {
		Plugins map[string]*cms_models.Plugin `json:"plugins"`
	}

	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("failed to parse plugin registry: %v", err)
	}

	// Validate each plugin's state
	cleanedPlugins := make(map[string]*cms_models.Plugin)
	for slug, plugin := range registry.Plugins {
		if vm.validatePluginState(plugin) {
			cleanedPlugins[slug] = plugin
		} else {
			vm.logger.WithFields(logger.Fields{
				"plugin_slug": slug,
			}).Info("Removing invalid plugin state from registry")
		}
	}

	// Save cleaned registry if changes were made
	if len(cleanedPlugins) != len(registry.Plugins) {
		cleanedData, err := json.MarshalIndent(map[string]interface{}{
			"plugins": cleanedPlugins,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal cleaned registry: %v", err)
		}

		if err := os.WriteFile(registryPath, cleanedData, 0644); err != nil {
			return fmt.Errorf("failed to write cleaned registry: %v", err)
		}

		vm.logger.WithFields(logger.Fields{
			"original_count": len(registry.Plugins),
			"cleaned_count":  len(cleanedPlugins),
		}).Info("Plugin registry cleaned")
	}

	return nil
}

// validatePluginState validates if a plugin's persisted state is valid
func (vm *VMService) validatePluginState(plugin *cms_models.Plugin) bool {
	// Check if plugin has required fields
	if plugin.Slug == "" || plugin.Name == "" {
		return false
	}

	// If plugin is active, validate its resources
	if plugin.Status == "active" {
		// Check if TAP interface exists
		if plugin.TapDevice != "" && !vm.tapExists(plugin.TapDevice) {
			vm.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"tap_device":  plugin.TapDevice,
			}).Debug("Plugin TAP interface not found, marking as invalid")
			return false
		}

		// Check if snapshot exists
		if !vm.HasSnapshot(plugin.Slug) {
			vm.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
			}).Debug("Plugin snapshot not found, marking as invalid")
			return false
		}
	}

	return true
}

// getVMIPFromStatic retrieves the IP address for a VM instance with static networking
func (vm *VMService) getVMIPFromStatic(instanceID string) string {
	// Get the VM instance from prewarm pool
	vm.poolMutex.RLock()
	instance, exists := vm.prewarmPool[instanceID]
	vm.poolMutex.RUnlock()

	if !exists {
		vm.logger.WithFields(logger.Fields{
			"instance_id": instanceID,
		}).Debug("Instance not found in prewarm pool")
		return ""
	}

	// Return the allocated IP for this VM instance
	return instance.IP
}

// getActivePluginTapDevices returns a map of TAP device names that are needed for active plugins
func (vm *VMService) getActivePluginTapDevices() map[string]bool {
	activeTapDevices := make(map[string]bool)

	// Read the plugin registry to find active plugins
	registryPath := filepath.Join(vm.config.DataDir, "plugins", "plugins.json")

	// Check if registry file exists
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		vm.logger.Debug("Plugin registry not found, no active plugins to preserve")
		return activeTapDevices
	}

	// Read plugin registry
	data, err := os.ReadFile(registryPath)
	if err != nil {
		vm.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to read plugin registry for TAP device preservation")
		return activeTapDevices
	}

	// Parse JSON to get active plugins
	var registry map[string]struct {
		Status    string `json:"status"`
		TapDevice string `json:"tap_device"`
	}

	if err := json.Unmarshal(data, &registry); err != nil {
		vm.logger.WithFields(logger.Fields{
			"error": err,
		}).Warn("Failed to parse plugin registry for TAP device preservation")
		return activeTapDevices
	}

	// Collect TAP devices for active plugins
	for pluginSlug, plugin := range registry {
		if plugin.Status == "active" && plugin.TapDevice != "" {
			activeTapDevices[plugin.TapDevice] = true
			vm.logger.WithFields(logger.Fields{
				"plugin_slug": pluginSlug,
				"tap_device":  plugin.TapDevice,
			}).Debug("Found active plugin with TAP device to preserve")
		}
	}

	return activeTapDevices
}

// getOrCreateTapInterface handles TAP interface setup with proper reuse logic
func (vm *VMService) getOrCreateTapInterface(plugin *cms_models.Plugin, instanceID string) (string, error) {
	if plugin.TapDevice != "" {
		// Use existing TAP device from plugin registry
		tapName := plugin.TapDevice
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"tap_device":  tapName,
		}).Info("Using existing TAP device from plugin registry")

		// Check if TAP device exists
		if !vm.tapExists(tapName) {
			vm.logger.WithFields(logger.Fields{
				"plugin_slug": plugin.Slug,
				"tap_device":  tapName,
			}).Info("TAP device not found, recreating with same name")

			// Recreate TAP device with the same name
			if err := vm.recreateTapInterface(tapName); err != nil {
				return "", fmt.Errorf("failed to recreate TAP device %s: %v", tapName, err)
			}
		}

		// Ensure TAP device is up
		if err := vm.ensureTapUp(tapName); err != nil {
			return "", fmt.Errorf("failed to ensure TAP device %s is up: %v", tapName, err)
		}

		return tapName, nil
	} else {
		// Create new TAP interface
		return vm.createTapInterface(plugin.Slug, instanceID)
	}
}

// getOrAllocateIP handles IP allocation with proper reuse logic
func (vm *VMService) getOrAllocateIP(plugin *cms_models.Plugin) (string, error) {
	if plugin.AssignedIP != "" {
		// Use existing assigned IP
		vm.logger.WithFields(logger.Fields{
			"plugin_slug": plugin.Slug,
			"assigned_ip": plugin.AssignedIP,
		}).Info("Using existing assigned IP")
		return plugin.AssignedIP, nil
	} else {
		// Allocate new IP
		allocatedIP := vm.allocateIP()
		if allocatedIP == "" {
			return "", fmt.Errorf("failed to allocate IP for VM")
		}
		vm.logger.WithFields(logger.Fields{
			"plugin_slug":  plugin.Slug,
			"allocated_ip": allocatedIP,
		}).Info("Allocated new IP")
		return allocatedIP, nil
	}
}

// recreateTapInterface recreates a TAP interface with the same name
func (vm *VMService) recreateTapInterface(tapName string) error {
	vm.logger.WithFields(logger.Fields{
		"tap_name": tapName,
	}).Info("Recreating TAP interface with same name")

	// Create TAP interface using ip command
	cmd := exec.Command("ip", "tuntap", "add", tapName, "mode", "tap")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create TAP interface %s: %v", tapName, err)
	}

	// Set TAP interface up
	cmd = exec.Command("ip", "link", "set", tapName, "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set TAP interface %s up: %v", tapName, err)
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
		"tap_name": tapName,
		"bridge":   "fcnetbridge0",
	}).Info("Recreated TAP interface and added to bridge")

	return nil
}
