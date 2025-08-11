# CMS Starter - Firecracker CMS Management CLI

A professional CLI tool for managing a Firecracker-based Content Management System with microVM isolation for plugins.

## 🏗️ Architecture Overview

This refactored codebase follows clean architecture principles with proper separation of concerns:

```
cms-starter/
├── cmd/                    # CLI commands (Cobra)
│   ├── root.go            # Root command & initialization
│   ├── start.go           # Start CMS command
│   ├── stop.go            # Stop CMS command
│   ├── restart.go         # Restart CMS command
│   ├── status.go          # Status check command
│   └── plugin.go          # Plugin management commands
├── internal/              # Internal packages
│   ├── config/            # Configuration management
│   ├── logger/            # Centralized logging
│   ├── errors/            # Error handling & types
│   ├── docker/            # Docker client & builder
│   ├── plugin/            # Plugin operations
│   └── services/          # Business logic services
├── main.go               # Application entry point
├── go.mod               # Go module definition
└── README.md           # This file
```

## ✨ Features

### 🎯 Core Functionality
- **Lifecycle Management**: Start, stop, restart CMS containers with proper health checks
- **Plugin Building**: Build plugins into bootable ext4 filesystems with intelligent sizing
- **Testing Suite**: Comprehensive test infrastructure with real plugin validation
- **Debug Controls**: Professional logging with configurable debug levels

### 🔧 Engineering Excellence
- **Clean Architecture**: Proper separation of concerns with interfaces and dependency injection
- **Error Handling**: Comprehensive error types with contextual information
- **Resilience**: Robust error recovery and validation
- **Observability**: Structured logging with JSON format for production environments

## 🚀 Quick Start

### Prerequisites
- Go 1.24.5+
- Docker Engine
- sudo access (for filesystem operations)

### Installation
```bash
# Build the CLI tool to bin directory
mkdir -p bin && go build -o bin/cms-starter .

# The binary is automatically executable
```

## 📖 Usage

### CMS Management

```bash
# Start CMS in production mode
./bin/cms-starter start

# Start CMS in development mode  
./bin/cms-starter --dev start

# Run comprehensive test suite
./bin/cms-starter --test start

# Check CMS status
./bin/cms-starter status

# Stop CMS
./bin/cms-starter stop

# Restart CMS
./bin/cms-starter restart
```

### Plugin Development

```bash
# Build a plugin (default 200MB filesystem)
./bin/cms-starter plugin build --plugin ./my-plugin

# Build with custom size (recommended for larger plugins)
./bin/cms-starter plugin build --plugin ./my-plugin --size 400

# Validate plugin structure and manifest
./bin/cms-starter plugin validate --plugin ./my-plugin

# Show plugin information
./bin/cms-starter plugin info --plugin ./my-plugin
```

### Configuration Options

```bash
# Global flags (available for all commands)
--debug          # Enable debug logging
--verbose        # Enable verbose output  
--dev           # Development mode
--test          # Test mode

# Start command specific flags
--port, -p      # CMS port (default: 80)
--data-dir, -d  # Data directory (default: ./cms-data)

# Plugin build specific flags
--plugin        # Plugin directory (required)
--size          # Filesystem size in MB (200-800, default: 200)
```

## 🏗️ Plugin Development Guide

### Plugin Structure
```
my-plugin/
├── Dockerfile          # Container definition
├── plugin.json         # Plugin manifest
├── app.py              # Your application code
└── requirements.txt    # Dependencies (if applicable)
```

### Plugin Manifest (plugin.json)
```json
{
  "slug": "my-plugin",
  "name": "My Awesome Plugin", 
  "version": "1.0.0",
  "description": "A sample plugin",
  "author": "Your Name",
  "runtime": "python",
  "actions": {
    "hello": {
      "name": "hello",
      "description": "Say hello",
      "hooks": ["content.create"],
      "method": "POST",
      "endpoint": "/hello",
      "priority": 10
    }
  }
}
```

### Build Process
1. **Validation**: Plugin structure and manifest validation
2. **Docker Build**: Creates container image from your code
3. **Filesystem Export**: Exports container to bootable ext4 filesystem
4. **Packaging**: Creates ZIP file with rootfs.ext4 + plugin.json
5. **Cleanup**: Automatically removes temporary Docker images

## 🐛 Debugging & Troubleshooting

### Enable Debug Logging
```bash
# Debug mode with detailed logs
./bin/cms-starter --debug start

# Verbose output for troubleshooting
./bin/cms-starter --verbose plugin build --plugin ./my-plugin
```

### Common Issues

#### Plugin Build Failures
```bash
# If build fails due to space issues, increase filesystem size
./bin/cms-starter plugin build --plugin ./my-plugin --size 400

# For very large plugins
./bin/cms-starter plugin build --plugin ./my-plugin --size 800
```

#### Container Issues
```bash
# Check container status
./bin/cms-starter status

# Force restart if container is stuck
./bin/cms-starter stop && ./bin/cms-starter start
```

### Log Files
- Debug logs: `./cms-data/logs/cms-starter_YYYY-MM-DD.log`
- Structured JSON format for easy parsing
- Automatic log rotation by date

## 🔄 Development Modes

### Production Mode (default)
- Uses `centraunit/cu-firecracker-cms:latest`
- Minimal logging
- Optimized for performance

### Development Mode (`--dev`)
- Uses `centraunit/cu-firecracker-cms:dev`
- Enhanced logging
- Development-specific configurations

### Test Mode (`--test`)
- Uses `centraunit/cu-firecracker-cms:test`
- Runs comprehensive test suite
- Validates real plugin functionality

## 🏛️ Architecture Details

### Configuration Management
- Environment variable support
- Validation with helpful error messages
- Mode-specific configurations (dev/test/prod)

### Error Handling
- Custom error types with context
- Helpful guidance for common issues
- Graceful error recovery

### Docker Integration
- Proper Docker client abstraction
- Container lifecycle management
- Image building with error handling

### Plugin System
- Manifest validation with semantic versioning
- Configurable filesystem sizing
- ZIP packaging for easy deployment

## 🤝 Contributing

This codebase now follows professional software engineering practices:

- **Clean Architecture**: Proper layer separation
- **Dependency Injection**: Testable and modular
- **Interface-Based Design**: Easy to mock and test
- **Error Handling**: Comprehensive and contextual
- **Logging**: Structured and configurable
- **Validation**: Input validation at all levels

## 📄 License

Copyright (c) 2025 CentraUnit Organization. All rights reserved.

This software is proprietary and confidential. 