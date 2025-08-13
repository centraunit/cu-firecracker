/*
 * Firecracker CMS - Centralized Logging
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

// Logger wraps logrus with CMS-specific functionality
type Logger struct {
	*logrus.Logger
	debug bool
}

// Fields represents structured logging fields
type Fields = logrus.Fields

var defaultLogger *Logger

// Init initializes the default logger with the specified configuration
func Init(level string, logDir string) error {
	logger := logrus.New()

	// Set log level
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		return fmt.Errorf("invalid log level %s: %w", level, err)
	}
	logger.SetLevel(logLevel)

	// Create log directory if specified
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}

		// Create log file with timestamp
		logFile := filepath.Join(logDir, fmt.Sprintf("cms_%s.log",
			time.Now().Format("2006-01-02")))

		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}

		// Log to both file and console
		logger.SetOutput(io.MultiWriter(os.Stdout, file))
	} else {
		logger.SetOutput(os.Stdout)
	}

	// Use JSON format for structured logging in production
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
	})

	defaultLogger = &Logger{
		Logger: logger,
		debug:  logLevel == logrus.DebugLevel,
	}

	return nil
}

// GetDefault returns the default logger instance
func GetDefault() *Logger {
	if defaultLogger == nil {
		// Fallback to basic logger if not initialized
		logger := logrus.New()
		logger.SetLevel(logrus.InfoLevel)
		defaultLogger = &Logger{Logger: logger, debug: false}
	}
	return defaultLogger
}

// WithFields creates a new logger entry with structured fields
func (l *Logger) WithFields(fields Fields) *logrus.Entry {
	return l.Logger.WithFields(logrus.Fields(fields))
}

// WithComponent creates a logger entry with a component field
func (l *Logger) WithComponent(component string) *logrus.Entry {
	return l.Logger.WithField("component", component)
}

// WithRequest creates a logger entry with request context
func (l *Logger) WithRequest(method, url, remoteAddr string) *logrus.Entry {
	return l.Logger.WithFields(logrus.Fields{
		"method":      method,
		"url":         url,
		"remote_addr": remoteAddr,
	})
}

// WithPlugin creates a logger entry with plugin context
func (l *Logger) WithPlugin(pluginSlug string) *logrus.Entry {
	return l.Logger.WithField("plugin_slug", pluginSlug)
}

// WithVM creates a logger entry with VM context
func (l *Logger) WithVM(instanceID string) *logrus.Entry {
	return l.Logger.WithField("instance_id", instanceID)
}

// Debug logs a debug message (only if debug mode is enabled)
func (l *Logger) Debug(args ...interface{}) {
	if l.debug {
		l.Logger.Debug(args...)
	}
}

// Debugf logs a formatted debug message (only if debug mode is enabled)
func (l *Logger) Debugf(format string, args ...interface{}) {
	if l.debug {
		l.Logger.Debugf(format, args...)
	}
}

// Package-level convenience functions
func Info(args ...interface{}) {
	GetDefault().Info(args...)
}

func Infof(format string, args ...interface{}) {
	GetDefault().Infof(format, args...)
}

func Warn(args ...interface{}) {
	GetDefault().Warn(args...)
}

func Warnf(format string, args ...interface{}) {
	GetDefault().Warnf(format, args...)
}

func Error(args ...interface{}) {
	GetDefault().Error(args...)
}

func Errorf(format string, args ...interface{}) {
	GetDefault().Errorf(format, args...)
}

func Fatal(args ...interface{}) {
	GetDefault().Fatal(args...)
}

func Fatalf(format string, args ...interface{}) {
	GetDefault().Fatalf(format, args...)
}

func Debug(args ...interface{}) {
	GetDefault().Debug(args...)
}

func Debugf(format string, args ...interface{}) {
	GetDefault().Debugf(format, args...)
}

func WithFields(fields Fields) *logrus.Entry {
	return GetDefault().WithFields(fields)
}

func WithComponent(component string) *logrus.Entry {
	return GetDefault().WithComponent(component)
}

func WithRequest(method, url, remoteAddr string) *logrus.Entry {
	return GetDefault().WithRequest(method, url, remoteAddr)
}

func WithPlugin(pluginSlug string) *logrus.Entry {
	return GetDefault().WithPlugin(pluginSlug)
}

func WithVM(instanceID string) *logrus.Entry {
	return GetDefault().WithVM(instanceID)
}
