/*
 * Firecracker CMS - HTTP Models
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package models

import (
	"time"
)

// HTTPResponse represents a standardized API response
type HTTPResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// ValidationError represents input validation errors
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// HealthCheckResponse represents the health check response
type HealthCheckResponse struct {
	Status        string                 `json:"status"`
	TotalPlugins  int                    `json:"total_plugins"`
	ActivePlugins int                    `json:"active_plugins"`
	VMInstances   int                    `json:"vm_instances"`
	Uptime        string                 `json:"uptime"`
	Details       map[string]interface{} `json:"details,omitempty"`
}

// MetricsResponse represents the metrics endpoint response
type MetricsResponse struct {
	PluginsTotal      int `json:"plugins_total"`
	InstancesTotal    int `json:"instances_total"`
	ConcurrentLimit   int `json:"concurrent_limit"`
	ConcurrentCurrent int `json:"concurrent_current"`
}

// ExecuteActionRequest represents the request body for action execution
type ExecuteActionRequest struct {
	Action  string                 `json:"action"`
	Payload map[string]interface{} `json:"payload"`
}

// ExecuteActionResponse represents the response for action execution
type ExecuteActionResponse struct {
	ActionHook      string                  `json:"action_hook"`
	ExecutedPlugins int                     `json:"executed_plugins"`
	Results         []ActionExecutionResult `json:"results"`
	Timestamp       string                  `json:"timestamp"`
}

// NewSuccessResponse creates a standardized success response
func NewSuccessResponse(data interface{}) *HTTPResponse {
	return &HTTPResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

// NewErrorResponse creates a standardized error response
func NewErrorResponse(message string) *HTTPResponse {
	return &HTTPResponse{
		Success:   false,
		Error:     message,
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

// NewValidationErrors creates a list of validation errors
func NewValidationErrors(errors ...ValidationError) []ValidationError {
	return errors
}

// NewHealthCheckResponse creates a health check response
func NewHealthCheckResponse(status string, totalPlugins, activePlugins, vmInstances int, uptime string) *HealthCheckResponse {
	return &HealthCheckResponse{
		Status:        status,
		TotalPlugins:  totalPlugins,
		ActivePlugins: activePlugins,
		VMInstances:   vmInstances,
		Uptime:        uptime,
		Details:       make(map[string]interface{}),
	}
}

// WithDetail adds a detail to the health check response
func (h *HealthCheckResponse) WithDetail(key string, value interface{}) *HealthCheckResponse {
	h.Details[key] = value
	return h
}
