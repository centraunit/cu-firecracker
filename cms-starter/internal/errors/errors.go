/*
 * Firecracker CMS - Error Handling
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package errors

import (
	"fmt"
)

// Error types for better error handling and categorization
type ErrorType string

const (
	ErrTypeValidation    ErrorType = "validation"
	ErrTypeDocker        ErrorType = "docker"
	ErrTypeFileSystem    ErrorType = "filesystem"
	ErrTypeNetwork       ErrorType = "network"
	ErrTypePlugin        ErrorType = "plugin"
	ErrTypeConfiguration ErrorType = "configuration"
	ErrTypeInternal      ErrorType = "internal"
)

// CMSError represents a custom application error with context
type CMSError struct {
	Type      ErrorType `json:"type"`
	Message   string    `json:"message"`
	Operation string    `json:"operation"`
	Cause     error     `json:"cause,omitempty"`
}

// Error implements the error interface
func (e *CMSError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Operation, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Message)
}

// Unwrap returns the underlying cause for error wrapping
func (e *CMSError) Unwrap() error {
	return e.Cause
}

// New creates a new CMSError
func New(errType ErrorType, operation, message string) *CMSError {
	return &CMSError{
		Type:      errType,
		Message:   message,
		Operation: operation,
	}
}

// Wrap wraps an existing error with additional context
func Wrap(err error, errType ErrorType, operation, message string) *CMSError {
	return &CMSError{
		Type:      errType,
		Message:   message,
		Operation: operation,
		Cause:     err,
	}
}

// Validation error constructors
func NewValidationError(operation, message string) *CMSError {
	return New(ErrTypeValidation, operation, message)
}

func WrapValidationError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeValidation, operation, message)
}

// Docker error constructors
func NewDockerError(operation, message string) *CMSError {
	return New(ErrTypeDocker, operation, message)
}

func WrapDockerError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeDocker, operation, message)
}

// FileSystem error constructors
func NewFileSystemError(operation, message string) *CMSError {
	return New(ErrTypeFileSystem, operation, message)
}

func WrapFileSystemError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeFileSystem, operation, message)
}

// Plugin error constructors
func NewPluginError(operation, message string) *CMSError {
	return New(ErrTypePlugin, operation, message)
}

func WrapPluginError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypePlugin, operation, message)
}

// Configuration error constructors
func NewConfigurationError(operation, message string) *CMSError {
	return New(ErrTypeConfiguration, operation, message)
}

func WrapConfigurationError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeConfiguration, operation, message)
}

// Internal error constructors
func NewInternalError(operation, message string) *CMSError {
	return New(ErrTypeInternal, operation, message)
}

func WrapInternalError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeInternal, operation, message)
}

// IsType checks if an error is of a specific type
func IsType(err error, errType ErrorType) bool {
	if cmsErr, ok := err.(*CMSError); ok {
		return cmsErr.Type == errType
	}
	return false
}

// GetType returns the error type if it's a CMSError, otherwise returns ErrTypeInternal
func GetType(err error) ErrorType {
	if cmsErr, ok := err.(*CMSError); ok {
		return cmsErr.Type
	}
	return ErrTypeInternal
}
