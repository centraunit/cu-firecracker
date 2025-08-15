# CMS in Go and Firecracker

A proof of concept for a modern Content Management System built in Go with a revolutionary plugin architecture powered by AWS Firecracker microVMs.

## What This Is

This project explores how to build a high-performance CMS with isolated plugins using Firecracker microVMs. Instead of traditional plugin architectures that share memory space, each plugin runs in its own lightweight virtual machine, providing unprecedented security, isolation, and performance.

Think of it as a CMS where every plugin is completely isolated - like having separate servers for each plugin, but they're so lightweight they start in milliseconds.

## Specifications

- **CMS Core**: Go-based web server with RESTful API
- **Plugin Isolation**: Firecracker microVMs (2-5MB memory footprint)
- **Multi-Runtime Support**: Python, TypeScript/Node.js, and PHP plugins
- **Snapshot Technology**: Sub-second plugin execution via VM state snapshots
- **Isolated Networking**: Each plugin gets its own network namespace
- **Priority-Based Execution**: Plugins execute in configurable priority order
- **Hot Plugin Management**: Upload, update, and manage plugins via API

## Features

### Core CMS
- Fast, concurrent Go web server
- RESTful API for plugin management
- Health monitoring and metrics
- Comprehensive logging and debugging

### Plugin System
- Multi-language runtime support (Python, TypeScript, PHP)
- Isolated execution environment per plugin
- Hot reloading and management
- Priority-based action execution
- Version control and upgrade management
- Automatic plugin restoration on startup

### VM Management
- Firecracker microVM integration
- Snapshot-based fast startup
- Resource isolation and limits
- Network namespace isolation
- Pre-warmed VM pool for instant execution
- Graceful VM lifecycle management

### Development Tools
- CLI tool for CMS management
- Plugin building and packaging
- Development and testing workflows
- Comprehensive error handling

## Quick Start

### Prerequisites

You'll need:
- Linux with KVM support
- Docker
- Go 1.19+
- Root privileges (for network setup)

```bash
# Install Docker
sudo apt update && sudo apt install docker.io

# Install Go
wget -O- https://go.dev/dl/go1.24.5.linux-amd64.tar.gz | sudo tar -C /usr/local -xzf -
export PATH=$PATH:/usr/local/go/bin

# Clone the repository
git clone https://github.com/centraunit/cu-firecracker
cd cu-firecracker
```

### Running the CMS

The easiest way to get started is using the provided Makefile:

```bash
# Start in development mode
make dev
```

This will:
- Build the CMS Docker image
- Compile the cms-starter CLI tool
- Start the CMS container
- Make it available at http://localhost:80

### Using cms-starter

The `cms-starter` tool is your main interface for managing the CMS:


## Building Plugins

Plugins are packaged as ZIP files containing a bootable filesystem and metadata. Use the cms-starter tool to build them:

```bash
# Build a Python plugin
./cms-starter/bin/cms-starter plugin build --plugin plugins/python-plugin

# Build with custom size (200-800MB)
./cms-starter/bin/cms-starter plugin build --plugin plugins/python-plugin --size 400

# Validate a plugin before building
./cms-starter/bin/cms-starter plugin validate --plugin plugins/python-plugin

# Show plugin information
./cms-starter/bin/cms-starter plugin info --plugin plugins/python-plugin
```

The build process:
1. Validates the plugin directory and manifest
2. Builds a Docker image from the plugin's Dockerfile
3. Exports the filesystem to an ext4 image
4. Packages everything into a ZIP file ready for upload

## Plugin Lifecycle

### 1. Upload

Upload a plugin to the CMS:

```bash
curl -X POST -F "plugin=@plugins/python-plugin/build/python-analytics-plugin-1.0.0.zip" \
     http://localhost:80/api/plugins
```

The CMS will:
- Extract and validate the plugin
- Start a VM to test the plugin
- Perform health checks
- Mark the plugin as "installed"

**Version Control**: The CMS automatically handles version conflicts:
- Higher versions automatically overwrite lower versions
- Same version requires `force=true` parameter
- Lower versions require `force=true` for downgrade

### 2. Activate

Activate a plugin to make it available for execution:

```bash
curl -X POST http://localhost:80/api/plugins/python-analytics/activate
```

This will:
- Start a fresh VM for the plugin
- Create a snapshot for fast future execution
- Add the VM to the pre-warm pool
- Mark the plugin as "active"

**Plugin Restoration**: On CMS startup, active plugins are automatically restored:
- VMs are recreated from snapshots
- Pre-warmed pool is populated
- Health checks are performed
- Plugins remain ready for instant execution

### 3. Execute

Execute actions across all active plugins:

```bash
curl -X POST -H "Content-Type: application/json" \
     -d '{"action": "analytics.calculate", "payload": {"data": "test"}}' \
     http://localhost:80/api/execute
```

The CMS will:
- Find all active plugins that handle the action
- Execute them in priority order
- Use pre-warmed VMs for instant execution
- Resume VMs from paused state for ultra-fast response

## API Reference

### Plugin Management

- `GET /api/plugins` - List all plugins
- `POST /api/plugins` - Upload plugin (multipart/form-data)
- `GET /api/plugins/{slug}` - Get plugin details
- `DELETE /api/plugins/{slug}` - Remove plugin
- `POST /api/plugins/{slug}/activate` - Activate plugin
- `POST /api/plugins/{slug}/deactivate` - Deactivate plugin

### Execution

- `POST /api/execute` - Execute action across plugins
- `POST /api/plugins/{slug}/actions/{action}` - Execute specific plugin action

### System

- `GET /health` - System health check
- `GET /metrics` - System metrics

## Development Workflow

### Makefile Commands

```bash
# Development
make dev          # Start CMS in development mode
```

### Testing

```bash
# work in progress

# Manual testing workflow
# 1. Build plugin
./cms-starter/bin/cms-starter plugin build --plugin plugins/python-plugin

# 2. Upload plugin
curl -X POST -F "plugin=@plugins/python-plugin/build/python-analytics-plugin-1.0.0.zip" \
     http://localhost:80/api/plugins

# 3. Check plugin status
curl -s http://localhost:80/api/plugins | jq '.'

# 4. Activate plugin
curl -X POST http://localhost:80/api/plugins/python-analytics/activate

# 5. Execute action
curl -X POST -H "Content-Type: application/json" \
     -d '{"action":"analytics.calculate","payload":{"test":"data"}}' \
     http://localhost:80/api/execute
```

### Debugging

```bash
# View CMS logs
docker logs cu-firecracker-cms-dev --tail 100 -f

# Check CMS status
./cms-starter/bin/cms-starter status

# Inspect running container
docker exec cu-firecracker-cms-dev ps aux
docker exec cu-firecracker-cms-dev ip addr show
```

## Plugin Development

### Creating a New Plugin

1. Create a plugin directory in `plugins/`
2. Add a `Dockerfile` with your runtime setup
3. Implement an HTTP server on port 80 with these endpoints:
   - `GET /health` - Health check (return `{"status": "healthy"}`)
   - `GET /actions` - List available actions
   - `POST /actions/{action}` - Execute action
4. Create a `plugin.json` manifest
5. Build and test

### Sample plugin.json

```json
{
  "slug": "my-plugin",
  "name": "My Awesome Plugin",
  "version": "1.0.0",
  "runtime": "python",
  "actions": {
    "content.create": {
      "priority": 100,
      "description": "Handle content creation"
    }
  }
}
```

## Performance

- **VM Startup**: ~3ms from snapshot
- **Plugin Execution**: ~13ms total (including network overhead)
- **Memory Usage**: 2-5MB per VM
- **Concurrent Execution**: Multiple plugins run in parallel
- **Pre-warmed Execution**: Instant response from paused VMs
- **Automatic Restoration**: Active plugins restored on startup

## Limitations

This is a proof of concept with some limitations:

- **Linux Only**: Firecracker requires Linux KVM
- **Root Privileges**: Network setup needs privileged container
- **Snapshot Storage**: VM snapshots consume disk space
- **Cold Start**: First plugin run requires VM boot
- **Plugin States**: Only "installed", "active", and "failed" states supported

## Contributing

This is a source-available project. We welcome contributions that:

- Fix bugs or improve functionality
- Add new plugin runtimes
- Enhance performance or security
- Improve documentation
- Add monitoring and observability features

Please read [CONTRIBUTING.md](CONTRIBUTING.md) for details on our contribution process and licensing terms.

## License

This project uses a Source Available License that is GitHub-compliant and allows meaningful collaboration while protecting intellectual property.

- View and study the source code
- Fork and clone for personal use
- Contribute improvements
- Use for educational purposes

Commercial licensing is available for business use. Contact us for enterprise support or white-label solutions.

See [LICENSE](LICENSE) for complete terms.

## Acknowledgments

- AWS Firecracker team for the amazing microVM technology
- Go community for excellent tooling and libraries
- All contributors testing and improving the system

---

This is an experimental project exploring the future of plugin architectures. We're excited to see where this technology can go and welcome collaboration from the community!
