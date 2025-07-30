# CRM Plugin System

A Go-based CRM system with a plugin architecture using AWS Firecracker microVMs. This system allows developers to create plugins in any language, compile them into `rootfs.ext4` files, and run them in isolated microVMs with HTTP communication.

## 🚀 Quick Start

### Prerequisites
- Docker and Docker Compose
- Go 1.24.5+
- Linux environment for production (KVM required)

### Development Setup
```bash
# Clone the repository
git clone <repository-url>
cd crm

# Start the CRM system
docker-compose up -d

# Build the plugin builder CLI
make build-cli

# Build a sample plugin
./bin/plugin-builder -plugin plugins/typescript-plugin

# Upload the plugin
curl -X POST -F "rootfs=@plugins/typescript-plugin/build/rootfs.ext4" \
  -F "name=typescript-plugin" -F "description=TypeScript plugin" \
  http://localhost:8080/api/plugins
```

## 🏗️ Architecture

### Core Components

1. **CRM Web Application** - HTTP server with REST API for plugin management
2. **Plugin Builder CLI** - Language-agnostic tool for compiling plugins
3. **Firecracker Integration** - MicroVM isolation and execution
4. **Sample Plugins** - TypeScript, Python, and PHP examples

### Plugin System Flow

```
Plugin Developer:
1. Creates plugin with plugin.json + Dockerfile + source code
2. Runs: our-plugin-cli export → builds rootfs.ext4
3. Uploads to CRM

CRM System:
1. Receives rootfs.ext4 via HTTP API
2. Creates Firecracker microVM
3. Communicates with plugin via HTTP
4. Returns responses to client
```

## 📁 Project Structure

```
crm/
├── main.go                 # CRM web application
├── vm_manager.go           # Firecracker VM management
├── cmd/plugin-builder/     # Plugin compilation CLI
├── plugins/                # Sample plugins
│   ├── typescript-plugin/
│   ├── python-plugin/
│   └── php-plugin/
├── Dockerfile              # CRM container build
├── docker-compose.yml      # Development environment
└── HANDOVER.md            # Detailed project documentation
```

## 🔌 Plugin Development

### Plugin Structure
```
my-plugin/
├── plugin.json          # Manifest file
├── Dockerfile          # Runtime configuration
└── src/                # Source code (any language)
    └── main.py         # Plugin entry point
```

### Required Endpoints
Plugins must expose these HTTP endpoints:
- `GET /health` - Health check
- `GET /` - Plugin info
- `POST /execute` - Main execution endpoint

### Example plugin.json
```json
{
  "name": "my-plugin",
  "version": "1.0.0",
  "port": 8080,
  "description": "Plugin description"
}
```

## 🛠️ API Endpoints

### Plugins
- `GET /api/plugins` - List all plugins
- `POST /api/plugins` - Upload plugin
- `GET /api/plugins/{id}` - Get specific plugin
- `DELETE /api/plugins/{id}` - Delete plugin

### Instances
- `GET /api/instances` - List all VM instances
- `POST /api/instances` - Create VM instance
- `GET /api/instances/{id}` - Get specific instance
- `DELETE /api/instances/{id}` - Delete instance

### Execution
- `POST /api/plugins/{id}/execute` - Execute plugin function

## 🧪 Testing

### Build and Test a Plugin
```bash
# Build the CLI tool
make build-cli

# Build a plugin
./bin/plugin-builder -plugin plugins/typescript-plugin

# Start CRM
docker-compose up -d

# Upload plugin
curl -X POST -F "rootfs=@plugins/typescript-plugin/build/rootfs.ext4" \
  -F "name=typescript-plugin" -F "description=TypeScript plugin" \
  http://localhost:8080/api/plugins

# Create VM instance
curl -X POST -H "Content-Type: application/json" \
  -d '{"plugin_id":"PLUGIN_ID"}' \
  http://localhost:8080/api/instances

# Execute plugin
curl -X POST -H "Content-Type: application/json" \
  -d '{"action":"test"}' \
  http://localhost:8080/api/plugins/PLUGIN_ID/execute
```

## 📊 Current Status

### ✅ Completed
- CRM web application with REST API
- Plugin upload and persistence
- Language-agnostic plugin builder CLI
- Docker integration with Firecracker
- Sample plugins (TypeScript, Python, PHP)
- Plugin persistence across restarts

### 🔄 In Progress
- Firecracker VM startup (KVM issues on macOS)
- Plugin communication via HTTP
- Network configuration for microVMs

### 📋 TODO
- Fix VM startup issues
- Implement proper networking
- Add error handling and logging
- Security hardening
- Performance optimization

## 🐛 Known Issues

1. **macOS Development**: Firecracker requires KVM which is Linux-only
2. **Network Configuration**: CNI setup needs refinement
3. **Kernel Loading**: Firecracker kernel loading issues

## 📚 Documentation

- [HANDOVER.md](HANDOVER.md) - Detailed project documentation and architecture
- [API Documentation](#-api-endpoints) - REST API reference
- [Plugin Development Guide](#-plugin-development) - How to create plugins

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.

## 🆘 Support

For questions and issues:
1. Check the [HANDOVER.md](HANDOVER.md) for detailed documentation
2. Review the code comments and commit history
3. Open an issue on GitHub

---

**Note**: This is a development project. For production use, ensure proper security measures and testing on Linux environments with KVM support.
