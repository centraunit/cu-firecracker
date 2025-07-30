# CRM Plugin System Handover Document

## Project Overview

This is a Go-based CRM system with a plugin architecture using AWS Firecracker microVMs. The system allows developers to create plugins in any language, compile them into `rootfs.ext4` files, and run them in isolated microVMs with HTTP communication.

## System Architecture

### Core Components

1. **CRM Web Application** (`main.go`, `vm_manager.go`)
   - HTTP server exposing REST API endpoints
   - Plugin management (upload, list, delete)
   - VM instance management (create, list, delete)
   - Plugin execution via HTTP calls to microVMs
   - Persistent storage of plugins using JSON files

2. **Plugin Builder CLI** (`cmd/plugin-builder/main.go`)
   - Language-agnostic tool for compiling plugins
   - Uses Docker to build plugin images
   - Exports container filesystem as `rootfs.ext4`
   - Reads `plugin.json` manifest and `Dockerfile`

3. **Sample Plugins** (`plugins/`)
   - TypeScript plugin with Express.js server
   - Python plugin with Flask server
   - PHP plugin with built-in server
   - Each plugin exposes HTTP endpoints for CRM interaction

### Plugin System Flow

```
Plugin Developer:
1. Creates plugin folder with:
   - plugin.json (manifest)
   - Dockerfile (runtime + start command)
   - Source code (any language)

2. Runs: our-plugin-cli export
   - CLI builds Docker image
   - Exports container as rootfs.ext4
   - Creates build/rootfs.ext4

3. Uploads rootfs.ext4 to CRM

CRM System:
1. Receives rootfs.ext4 via HTTP API
2. Stores plugin metadata and filesystem
3. Creates Firecracker microVM with rootfs
4. Communicates with plugin via HTTP
5. Returns plugin responses to client
```

## Current State

### ✅ Completed Features

1. **CRM Web Application**
   - HTTP server with REST API endpoints
   - Plugin upload, listing, deletion
   - VM instance creation and management
   - Plugin persistence (survives container restarts)
   - Health check endpoint

2. **Plugin Builder CLI**
   - Language-agnostic design
   - Docker-based build process
   - Reads plugin.json and Dockerfile
   - Exports container as rootfs.ext4
   - Supports TypeScript, Python, PHP examples

3. **Sample Plugins**
   - TypeScript: Express.js server with customer/analytics endpoints
   - Python: Flask server with similar functionality
   - PHP: Built-in server with routing
   - All plugins expose /health, /, /execute endpoints

4. **Docker Integration**
   - CRM runs in Docker container with Firecracker
   - Linux kernel built from source (v6.6.99)
   - CNI networking plugins installed
   - KVM device mounted for virtualization

5. **Persistence**
   - Plugins saved to `./plugins/plugins.json`
   - Rootfs files stored in `./plugins/`
   - Survives container restarts

### 🔄 In Progress

1. **Firecracker VM Startup**
   - Currently failing due to KVM access issues on macOS
   - Need to implement macOS simulation mode
   - Networking configuration needs refinement

2. **Plugin Communication**
   - Hardcoded IP addresses in execute handler
   - Need dynamic IP assignment from Firecracker
   - HTTP communication between CRM and plugins

### ❌ Known Issues

1. **macOS KVM Limitation**
   - Firecracker requires KVM which is Linux-only
   - Docker on macOS can't access KVM
   - Need simulation mode for development

2. **Network Configuration**
   - CNI setup is complex
   - IP assignment not working properly
   - Plugin communication failing

3. **Kernel Loading**
   - Firecracker kernel loading issues
   - Need proper kernel configuration

## Technical Requirements

### Prerequisites
- Docker and Docker Compose
- Go 1.24.5+
- Linux environment for production (KVM required)

### Dependencies
- `github.com/firecracker-microvm/firecracker-go-sdk`
- `github.com/gorilla/mux`
- Docker for plugin building
- Firecracker v1.12.1
- Linux kernel v6.6.99
- CNI plugins v1.7.1

## API Endpoints

### Plugins
- `GET /api/plugins` - List all plugins
- `POST /api/plugins` - Upload plugin (multipart: rootfs, name, description)
- `GET /api/plugins/{id}` - Get specific plugin
- `DELETE /api/plugins/{id}` - Delete plugin

### Instances
- `GET /api/instances` - List all VM instances
- `POST /api/instances` - Create VM instance (JSON: plugin_id)
- `GET /api/instances/{id}` - Get specific instance
- `DELETE /api/instances/{id}` - Delete instance

### Execution
- `POST /api/plugins/{id}/execute` - Execute plugin function

### Health
- `GET /health` - Health check

## Plugin Development

### Plugin Structure
```
my-plugin/
├── plugin.json          # Manifest file
├── Dockerfile          # Runtime configuration
└── src/                # Source code (any language)
    └── main.py         # Plugin entry point
```

### plugin.json Format
```json
{
  "name": "my-plugin",
  "version": "1.0.0",
  "port": 8080,
  "description": "Plugin description"
}
```

### Required Endpoints
Plugins must expose these HTTP endpoints:
- `GET /health` - Health check
- `GET /` - Plugin info
- `POST /execute` - Main execution endpoint

## Development Commands

### Build and Run
```bash
# Build CRM
make build

# Build CLI tool
make build-cli

# Run CRM in Docker
docker-compose up -d

# Build plugin
./bin/plugin-builder -plugin plugins/typescript-plugin
```

### Testing
```bash
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

## MVP Requirements

### Phase 1: Basic Functionality ✅
- [x] Plugin upload and storage
- [x] VM instance creation
- [x] Plugin execution framework
- [x] Multi-language plugin support

### Phase 2: Production Ready 🔄
- [ ] Fix Firecracker VM startup
- [ ] Implement proper networking
- [ ] Add dynamic IP assignment
- [ ] Error handling and logging
- [ ] Security hardening

### Phase 3: Advanced Features
- [ ] Plugin versioning
- [ ] Plugin marketplace
- [ ] Resource monitoring
- [ ] Auto-scaling
- [ ] Plugin isolation policies

## File Structure
```
crm/
├── main.go                 # CRM web application
├── vm_manager.go           # Firecracker VM management
├── go.mod                  # Go dependencies
├── Dockerfile              # CRM container build
├── docker-compose.yml      # Development environment
├── cmd/
│   └── plugin-builder/
│       └── main.go         # Plugin compilation CLI
├── plugins/
│   ├── typescript-plugin/  # TypeScript example
│   ├── python-plugin/      # Python example
│   └── php-plugin/         # PHP example
└── bin/                    # Built binaries
```

## Next Steps

1. **Immediate Priority**: Fix Firecracker VM startup
   - Implement macOS simulation mode
   - Test on Linux environment
   - Fix kernel loading issues

2. **Short Term**: Complete MVP
   - Working plugin execution
   - Proper HTTP communication
   - Error handling

3. **Medium Term**: Production Features
   - Security hardening
   - Performance optimization
   - Monitoring and logging

## Notes for Developers

- The system is designed to be language-agnostic
- Plugins run in isolated Firecracker microVMs
- All communication is via HTTP
- Docker is used for plugin building and CRM deployment
- Persistence is implemented for plugins and instances
- The CLI tool is independent of the CRM system

## Contact

For questions about this handover, refer to the commit history and code comments for implementation details. 