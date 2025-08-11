/*
 * Firecracker CMS - VM Models
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package models

import (
	"time"
)

// VMInstance represents a running microVM instance
type VMInstance struct {
	ID         string    `json:"id"`
	PluginSlug string    `json:"plugin_slug"`
	Status     string    `json:"status"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// VMStatus constants
const (
	VMStatusRunning  = "running"
	VMStatusStopped  = "stopped"
	VMStatusStarting = "starting"
	VMStatusStopping = "stopping"
	VMStatusFailed   = "failed"
)

// NewVMInstance creates a new VM instance
func NewVMInstance(id, pluginSlug, ipAddress string) *VMInstance {
	now := time.Now()
	return &VMInstance{
		ID:         id,
		PluginSlug: pluginSlug,
		Status:     VMStatusStarting,
		IPAddress:  ipAddress,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// SetStatus sets the VM status and updates the timestamp
func (vm *VMInstance) SetStatus(status string) {
	vm.Status = status
	vm.UpdatedAt = time.Now()
}

// IsRunning returns true if the VM is running
func (vm *VMInstance) IsRunning() bool {
	return vm.Status == VMStatusRunning
}
