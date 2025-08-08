# 🔥 Firecracker CMS - Ultra-Fast Plugin System

> **Proof of Concept**: A modern Content Management System built in Go with a revolutionary plugin architecture powered by AWS Firecracker microVMs

## 🎯 Project Overview

This project demonstrates how to build a **high-performance CMS** with an **isolated plugin system** using **Firecracker microVMs**. Instead of traditional plugin architectures that share memory space, each plugin runs in its own **lightweight virtual machine** (microVM), providing unprecedented **security**, **isolation**, and **performance**.

## 🚀 Key Achievements

### ✨ What We Built

- **🏗️ Go-based CMS Core**: Fast, concurrent web server with RESTful API
- **🔌 Multi-Runtime Plugin Support**: Python, TypeScript/Node.js, and PHP plugins
- **⚡ Firecracker Integration**: Lightweight microVMs (2-5MB memory footprint)
- **📸 Snapshot Technology**: Sub-second plugin execution via VM state snapshots
- **🌐 Isolated Networking**: Each plugin gets its own network namespace
- **🎚️ Priority-Based Execution**: Plugins execute in configurable priority order
- **🔄 Hot Plugin Management**: Upload, update, and manage plugins via API

### 🎪 Plugin Architecture Highlights

```
┌─────────────────┐    ┌──────────────────────┐    ┌─────────────────┐
│   CMS Core      │    │   Firecracker VM     │    │   Plugin Code   │
│   (Go)          │◄──►│   (Linux Kernel)     │◄──►│   (Py/TS/PHP)   │
│                 │    │   • Isolated         │    │   • HTTP Server │
│   • API Server  │    │   • 192.168.127.x    │    │   • Business    │
│   • VM Manager  │    │   • Snapshots        │    │     Logic       │
│   • Networking  │    │   • ~2MB RAM         │    │                 │
└─────────────────┘    └──────────────────────┘    └─────────────────┘
```

## 🏃‍♂️ Quick Start

### Prerequisites

```bash
# Install Docker
sudo apt update && sudo apt install docker.io

# Install Go (1.19+)
wget -O- https://go.dev/dl/go1.24.5.linux-amd64.tar.gz | sudo tar -C /usr/local -xzf -
export PATH=$PATH:/usr/local/go/bin

# Clone the repository
git clone https://github.com/centraunit/cu-firecracker
cd cu-firecracker
```

### 🎬 Running the System

1. **Build and Start CMS**:
   ```bash
   make deploy
   ```
   This automatically:
   - Builds the CMS Docker image
   - Compiles the `cms-starter` CLI tool
   - Restarts the CMS container

2. **Verify CMS is Running**:
   ```bash
   curl http://localhost:80/api/health
   # Expected: {"status":"healthy","timestamp":"..."}
   ```

3. **Build and Upload Sample Plugins**:
   ```bash
   # Build Python Analytics Plugin
   ./cms-starter/bin/cms-starter plugin build --plugin plugins/python-plugin
   curl -X POST -F "plugin=@plugins/python-plugin/build/python-analytics-plugin-1.0.0.zip" \
        http://localhost:80/api/plugins

   # Build TypeScript CMS Plugin  
   ./cms-starter/bin/cms-starter plugin build --plugin plugins/typescript-plugin
   curl -X POST -F "plugin=@plugins/typescript-plugin/build/typescript-cms-plugin-1.0.0.zip" \
        http://localhost:80/api/plugins

   # Build PHP Content Manager Plugin
   ./cms-starter/bin/cms-starter plugin build --plugin plugins/php-plugin
   curl -X POST -F "plugin=@plugins/php-plugin/build/php-content-manager-plugin-1.0.0.zip" \
        http://localhost:80/api/plugins
   ```

4. **Execute Plugin Actions**:
   ```bash
   # Trigger content creation (runs multiple plugins by priority)
   curl -X POST -H "Content-Type: application/json" \
        -d '{"action": "content.create", "payload": {"title": "Hello World", "content": "First post!"}}' \
        http://localhost:80/api/execute
   ```

## 🔧 Architecture Deep Dive

### Core Components

1. **CMS Core (`cu-cms/`)**: Go application handling HTTP API, plugin management, and VM orchestration
2. **VM Manager**: Firecracker integration for creating, managing, and snapshotting microVMs  
3. **Plugin System**: Multi-runtime support with standardized JSON manifests
4. **Network Layer**: Isolated networking with TAP interfaces and bridge networking
5. **CLI Tool (`cms-starter/`)**: Management utility for building and deploying

### Plugin Structure

Each plugin is a zip package containing:
```
plugin-name/
├── plugin.json      # Metadata and action definitions
├── Dockerfile       # Container build instructions  
├── index.[py|ts|php] # HTTP server implementation
└── ...              # Additional files
```

### Sample `plugin.json`:
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

## 🛠️ CLI Tool (`cms-starter`)

The `cms-starter` tool automates the entire CMS lifecycle and plugin building:

### CMS Management
```bash
./cms-starter/bin/cms-starter start [--port 80] [--data-dir ./cms-data]  # Start CMS
./cms-starter/bin/cms-starter stop                                       # Stop CMS  
./cms-starter/bin/cms-starter restart                                    # Restart CMS
./cms-starter/bin/cms-starter status                                     # Check status
```

### Plugin Building
```bash
# Build plugin into bootable ext4 filesystem + ZIP package
./cms-starter/bin/cms-starter plugin build --plugin plugins/my-plugin [--size 400]
```

The plugin build process:
1. **Docker Build**: Creates container image from plugin's `Dockerfile`
2. **ext4 Creation**: Exports container to bootable ext4 filesystem (200-800MB)
3. **ZIP Packaging**: Bundles `rootfs.ext4` + `plugin.json` into uploadable ZIP
4. **Output**: Ready-to-upload plugin package in `plugins/my-plugin/build/`

### Size Recommendations
- **Default (200MB)**: Basic plugins with minimal dependencies
- **400MB**: Recommended for TypeScript/Node.js plugins  
- **500-800MB**: Large frameworks, multiple runtimes, or complex dependencies

## 🎯 API Reference

### Plugin Management
- `GET /api/plugins` - List all plugins
- `POST /api/plugins` - Upload plugin (multipart/form-data)
- `DELETE /api/plugins/{slug}` - Remove plugin

### Plugin Execution  
- `POST /api/execute` - Execute action across plugins
- `POST /api/plugins/{slug}/actions/{action}` - Execute specific plugin action

### System
- `GET /api/health` - System health check

## 🏗️ Development

### Adding New Plugin Runtimes

1. Create plugin directory in `plugins/`
2. Add `Dockerfile` with runtime setup
3. Implement HTTP server on port 80 with endpoints:
   - `GET /health` - Health check
   - `GET /actions` - List available actions  
   - `POST /actions/{action}` - Execute action
4. Create `plugin.json` manifest
5. Build and test!

### Performance Optimizations

- **Snapshots**: VMs boot from saved state in ~3ms
- **Concurrent Execution**: Multiple plugins run in parallel
- **Resource Limits**: Each VM limited to prevent resource exhaustion
- **Network Isolation**: No plugin can interfere with others

## 🧪 Testing & Debugging

```bash
# CMS Management
./cms-starter/bin/cms-starter status              # Check CMS status
./cms-starter/bin/cms-starter restart             # Restart after changes

# View CMS logs
docker logs cu-firecracker-cms --tail 100

# Plugin Development
./cms-starter/bin/cms-starter plugin build --plugin plugins/my-plugin --size 400
curl -X POST -F "plugin=@plugins/my-plugin/build/my-plugin-1.0.0.zip" http://localhost:80/api/plugins

# System Debugging
docker exec cu-firecracker-cms ps aux            # Check running processes
docker exec cu-firecracker-cms ip addr show      # Network debugging
docker exec cu-firecracker-cms ls -la /app/data  # Check data persistence

# API Testing
curl http://localhost:80/api/health               # System health
curl http://localhost:80/api/plugins              # List plugins
curl -X POST -H "Content-Type: application/json" \
     -d '{"action": "content.create", "payload": {"title": "Test"}}' \
     http://localhost:80/api/execute              # Execute actions
```

## 🤝 Contributing

**⚠️ Important**: This is a proprietary project. By contributing, you assign all rights to your contributions to CentraUnit Organization.

We welcome contributors interested in:

- **🔌 New Plugin Runtimes**: Rust, Java, .NET, etc.
- **⚡ Performance Improvements**: Faster snapshots, better networking
- **🛡️ Security Enhancements**: Plugin sandboxing, resource limits
- **📊 Monitoring & Observability**: Metrics, tracing, dashboards
- **🎨 Frontend Development**: Management UI for plugins
- **📚 Documentation**: Tutorials, examples, best practices

**Before contributing**: Please read [CONTRIBUTING.md](CONTRIBUTING.md) for legal requirements and process details.

### Development Setup

```bash
# Build and start everything (recommended)
make deploy

# Or manually:
# 1. Build CMS image
cd cu-cms && docker build -t centraunit/cu-firecracker-cms:local .

# 2. Build CLI tool  
cd ../cms-starter && go build -o bin/cms-starter

# 3. Start CMS
./bin/cms-starter start --port 80

# Development workflow
./bin/cms-starter restart  # After code changes
./bin/cms-starter status   # Check if running
./bin/cms-starter stop     # Stop for maintenance

# Run tests
go test ./...
```

## 🎪 Use Cases

- **Multi-tenant SaaS**: Isolated customer plugins
- **Enterprise CMS**: Department-specific extensions
- **E-commerce Platforms**: Secure payment/shipping plugins  
- **Content Pipelines**: Processing plugins in isolation
- **Microservices**: Language-agnostic service mesh

## 🚧 Known Limitations

- **Linux Only**: Firecracker requires Linux KVM
- **Root Privileges**: Network setup needs privileged container
- **Snapshot Storage**: VM snapshots consume disk space
- **Cold Start**: First plugin run requires VM boot

## 📄 License

**Source Available License** - This project uses a GitHub-compliant license that protects intellectual property while enabling collaboration.

- ✅ **View & Study**: Source code available for educational purposes
- ✅ **Fork & Clone**: GitHub-compliant forking for personal use and contributions
- ✅ **Contribute**: Submit pull requests and improvements  
- ✅ **Educational Use**: Perfect for learning and research
- ✅ **Personal Projects**: Use for non-commercial purposes
- ❌ **Commercial Use**: Requires separate commercial license
- ❌ **Redistribution**: Cannot distribute outside of GitHub

**Commercial licensing available** - Contact legal@centraunit.org for business use, enterprise support, or white-label solutions.

See [LICENSE](LICENSE) for complete terms.

## 🙏 Acknowledgments

- **AWS Firecracker** team for the amazing microVM technology
- **Go Community** for excellent tooling and libraries
- **Plugin Developers** testing the early system

---

**⭐ Star this repo if you find it interesting!** 

**🐛 Found a bug?** Open an issue!  
**💡 Have ideas?** Start a discussion!  
**🔧 Want to contribute?** Submit a PR!
