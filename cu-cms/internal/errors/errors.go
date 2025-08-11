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
	ErrTypeValidation  ErrorType = "validation"
	ErrTypeHTTP        ErrorType = "http"
	ErrTypePlugin      ErrorType = "plugin"
	ErrTypeVM          ErrorType = "vm"
	ErrTypeFirecracker ErrorType = "firecracker"
	ErrTypeNetwork     ErrorType = "network"
	ErrTypeFileSystem  ErrorType = "filesystem"
	ErrTypeTimeout     ErrorType = "timeout"
	ErrTypeInternal    ErrorType = "internal"
)

// CMSError represents a custom application error with context
type CMSError struct {
	Type      ErrorType              `json:"type"`
	Message   string                 `json:"message"`
	Operation string                 `json:"operation"`
	Component string                 `json:"component,omitempty"`
	Cause     error                  `json:"cause,omitempty"`
	Context   map[string]interface{} `json:"context,omitempty"`
}

// Error implements the error interface
func (e *CMSError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %s (caused by: %v)", e.Type, e.Operation, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Type, e.Operation, e.Message)
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
		Context:   make(map[string]interface{}),
	}
}

// Wrap wraps an existing error with additional context
func Wrap(err error, errType ErrorType, operation, message string) *CMSError {
	return &CMSError{
		Type:      errType,
		Message:   message,
		Operation: operation,
		Cause:     err,
		Context:   make(map[string]interface{}),
	}
}

// WithComponent adds component information to the error
func (e *CMSError) WithComponent(component string) *CMSError {
	e.Component = component
	return e
}

// WithContext adds context information to the error
func (e *CMSError) WithContext(key string, value interface{}) *CMSError {
	if e.Context == nil {
		e.Context = make(map[string]interface{})
	}
	e.Context[key] = value
	return e
}

// Validation error constructors
func NewValidationError(operation, message string) *CMSError {
	return New(ErrTypeValidation, operation, message)
}

func WrapValidationError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeValidation, operation, message)
}

// HTTP error constructors
func NewHTTPError(operation, message string) *CMSError {
	return New(ErrTypeHTTP, operation, message)
}

func WrapHTTPError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeHTTP, operation, message)
}

// Plugin error constructors
func NewPluginError(operation, message string) *CMSError {
	return New(ErrTypePlugin, operation, message)
}

func WrapPluginError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypePlugin, operation, message)
}

// VM error constructors
func NewVMError(operation, message string) *CMSError {
	return New(ErrTypeVM, operation, message)
}

func WrapVMError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeVM, operation, message)
}

// Firecracker error constructors
func NewFirecrackerError(operation, message string) *CMSError {
	return New(ErrTypeFirecracker, operation, message)
}

func WrapFirecrackerError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeFirecracker, operation, message)
}

// Network error constructors
func NewNetworkError(operation, message string) *CMSError {
	return New(ErrTypeNetwork, operation, message)
}

func WrapNetworkError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeNetwork, operation, message)
}

// FileSystem error constructors
func NewFileSystemError(operation, message string) *CMSError {
	return New(ErrTypeFileSystem, operation, message)
}

func WrapFileSystemError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeFileSystem, operation, message)
}

// Timeout error constructors
func NewTimeoutError(operation, message string) *CMSError {
	return New(ErrTypeTimeout, operation, message)
}

func WrapTimeoutError(err error, operation, message string) *CMSError {
	return Wrap(err, ErrTypeTimeout, operation, message)
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

// GetContext returns the error context if it's a CMSError
func GetContext(err error) map[string]interface{} {
	if cmsErr, ok := err.(*CMSError); ok {
		return cmsErr.Context
	}
	return nil
}
