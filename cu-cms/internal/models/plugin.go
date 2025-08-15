/*
 * Firecracker CMS - Plugin Domain Models
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package models

import (
	"time"
)

// Plugin represents a CMS plugin with action-based hooks
type Plugin struct {
	Slug        string                  `json:"slug"` // Unique identifier
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Version     string                  `json:"version"`
	Author      string                  `json:"author"`
	Runtime     string                  `json:"runtime"` // Runtime environment (python, typescript, php, etc.)
	RootfsPath  string                  `json:"rootfs_path"`
	KernelPath  string                  `json:"kernel_path"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
	Status      string                  `json:"status"` // installed, active, failed
	Health      PluginHealth            `json:"health"`
	Actions     map[string]PluginAction `json:"actions"`  // action_name -> PluginAction
	Priority    int                     `json:"priority"` // Execution order for same action

	// Network configuration - persistent across activations
	AssignedIP string `json:"assigned_ip,omitempty"` // Assigned IP address
	TapDevice  string `json:"tap_device,omitempty"`  // TAP device name
}

// PluginHealth represents plugin health status
type PluginHealth struct {
	Status       string    `json:"status"` // healthy, unhealthy, unknown
	LastCheck    time.Time `json:"last_check"`
	Message      string    `json:"message"`
	ResponseTime int64     `json:"response_time_ms"`
}

// PluginAction represents an action hook that a plugin provides
type PluginAction struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Hooks       []string `json:"hooks"`    // Which CMS actions this responds to
	Method      string   `json:"method"`   // HTTP method
	Endpoint    string   `json:"endpoint"` // Plugin endpoint
	Priority    int      `json:"priority"` // Execution order
}

// ActionExecutionResult represents the result of plugin action execution
type ActionExecutionResult struct {
	PluginSlug    string        `json:"plugin_slug"`
	Success       bool          `json:"success"`
	Result        interface{}   `json:"result,omitempty"`
	Error         string        `json:"error,omitempty"`
	ExecutionTime time.Duration `json:"execution_time_ms"`
}

// PluginStatus constants
const (
	PluginStatusInstalled = "installed"
	PluginStatusActive    = "active"
	PluginStatusFailed    = "failed"
)

// PluginHealthStatus constants
const (
	HealthStatusHealthy   = "healthy"
	HealthStatusUnhealthy = "unhealthy"
	HealthStatusUnknown   = "unknown"
)

// NewPlugin creates a new plugin with default values
func NewPlugin(slug, name, version string) *Plugin {
	now := time.Now()
	return &Plugin{
		Slug:      slug,
		Name:      name,
		Version:   version,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    PluginStatusInstalled, // New plugins start as installed
		Health: PluginHealth{
			Status:    HealthStatusUnknown,
			LastCheck: now,
		},
		Actions:  make(map[string]PluginAction),
		Priority: 0,
	}
}

// UpdateHealth updates the plugin health status
func (p *Plugin) UpdateHealth(status, message string, responseTime int64) {
	p.Health.Status = status
	p.Health.Message = message
	p.Health.ResponseTime = responseTime
	p.Health.LastCheck = time.Now()
	p.UpdatedAt = time.Now()
}

// SetStatus sets the plugin status and updates the timestamp
func (p *Plugin) SetStatus(status string) {
	p.Status = status
	p.UpdatedAt = time.Now()
}

// IsActive returns true if the plugin is active
func (p *Plugin) IsActive() bool {
	return p.Status == PluginStatusActive
}

// IsInstalled returns true if the plugin is installed
func (p *Plugin) IsInstalled() bool {
	return p.Status == PluginStatusInstalled
}

// IsHealthy returns true if the plugin is healthy
func (p *Plugin) IsHealthy() bool {
	return p.Health.Status == HealthStatusHealthy
}

// GetActionsForHook returns all actions that respond to a specific hook
func (p *Plugin) GetActionsForHook(hook string) []PluginAction {
	var actions []PluginAction
	for _, action := range p.Actions {
		for _, actionHook := range action.Hooks {
			if actionHook == hook {
				actions = append(actions, action)
				break
			}
		}
	}
	return actions
}
