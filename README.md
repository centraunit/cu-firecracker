# CRM Plugin System with Firecracker MicroVMs

A Customer Relationship Management (CRM) system built in Go that uses AWS Firecracker microVMs to run plugins in isolated environments. Each plugin runs in its own microVM, providing security and isolation.

## Features

- **Plugin System**: Upload and manage plugins as rootfs.ext4 files
- **MicroVM Isolation**: Each plugin runs in its own Firecracker microVM
- **HTTP API**: RESTful API for plugin management and execution
- **Language Agnostic**: Plugins can be written in any language that runs in Docker
- **CLI Tool**: Command-line tool for building plugins into rootfs.ext4

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   CRM Web App   │    │  Plugin Builder │    │  Plugin Dev     │
│   (main.go)     │    │   (CLI Tool)    │    │ (any language)  │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         │                       │                       │
         ▼                       ▼                       ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  Firecracker    │    │   rootfs.ext4   │    │   Dockerfile    │
│   MicroVM       │    │   Generation    │    │   + Code        │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## Plugin Development Flow

### 1. Create Plugin Structure

Each plugin is a directory with:

```
my-plugin/
├── plugin.json      # Plugin manifest
├── Dockerfile       # Runtime configuration
├── app/
│   └── main.py      # Your code (any language)
```

### 2. Plugin Manifest (plugin.json)

```json
{
  "name": "hello-world",
  "version": "1.0.0",
  "port": 8080,
  "description": "Simple web plugin"
}
```

### 3. Dockerfile (Python example)

```dockerfile
FROM python:3.11-slim

WORKDIR /plugin
COPY app/ /app
RUN pip install flask

EXPOSE 8080
CMD ["python", "main.py"]
```

### 4. Plugin Code (main.py)

```python
from flask import Flask, request
app = Flask(__name__)

@app.route("/onPageLoad", methods=["POST"])
def on_page_load():
    data = request.json
    return {"message": f"Hello from plugin! URL was: {data['url']}"}

app.run(host="0.0.0.0", port=8080)
```

### 5. Build Plugin

```bash
plugin-builder -plugin ./my-plugin
```

This creates:
```
my-plugin/
└── build/
    └── rootfs.ext4  ✅ Firecracker-ready image
```

## Prerequisites

- Go 1.24.5 or later
- Docker (for building plugins)
- Firecracker binary (for microVM support)
- Linux kernel image (vmlinux)

## Installation

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd crm
   ```

2. **Install dependencies**:
   ```bash
   go mod tidy
   ```

3. **Build the CLI tool**:
   ```bash
   go build -o bin/plugin-builder cmd/plugin-builder/main.go
   ```

4. **Install Firecracker** (if not already installed):
   ```bash
   # On macOS
   brew install firecracker
   
   # On Linux
   # Download from https://github.com/firecracker-microvm/firecracker/releases
   ```

## Usage

### 1. Start the CRM Server

```bash
go run main.go vm_manager.go
```

The server will start on port 8080 (or set CRM_PORT environment variable).

### 2. Build a Plugin

```bash
# Build TypeScript plugin
./bin/plugin-builder -plugin ./plugins/typescript-plugin

# Build Python plugin
./bin/plugin-builder -plugin ./plugins/python-plugin

# Build PHP plugin
./bin/plugin-builder -plugin ./plugins/php-plugin
```

### 3. Upload a Plugin

```bash
curl -X POST http://localhost:8080/api/plugins \
  -F "name=Hello World Plugin" \
  -F "description=A sample plugin" \
  -F "rootfs=@./plugins/typescript-plugin/build/rootfs.ext4"
```

### 4. Create a VM Instance

```bash
curl -X POST http://localhost:8080/api/instances \
  -H "Content-Type: application/json" \
  -d '{"plugin_id":"<plugin-id>"}'
```

### 5. Execute a Plugin

```bash
curl -X POST http://localhost:8080/api/plugins/<plugin-id>/execute
```

## API Endpoints

### Plugin Management

- `GET /api/plugins` - List all plugins
- `POST /api/plugins` - Upload a new plugin
- `GET /api/plugins/{id}` - Get plugin details
- `DELETE /api/plugins/{id}` - Delete a plugin

### VM Instance Management

- `GET /api/instances` - List all VM instances
- `POST /api/instances` - Create a new VM instance
- `GET /api/instances/{id}` - Get instance details
- `DELETE /api/instances/{id}` - Stop and delete an instance

### Plugin Execution

- `POST /api/plugins/{id}/execute` - Execute a plugin

### Health Check

- `GET /health` - Check if the CRM is running

## Sample Plugins

### TypeScript Plugin

```bash
cd plugins/typescript-plugin
./bin/plugin-builder -plugin .
```

### Python Plugin

```bash
cd plugins/python-plugin
./bin/plugin-builder -plugin .
```

### PHP Plugin

```bash
cd plugins/php-plugin
./bin/plugin-builder -plugin .
```

## CLI Tool Usage

The plugin builder CLI tool has the following options:

```bash
./bin/plugin-builder -help
```

Options:
- `-plugin`: Path to plugin directory (required)
- `-output`: Output path for rootfs.ext4 (optional, defaults to ./build/rootfs.ext4)
- `-help`: Show help

## Plugin Development Guidelines

### 1. Plugin Structure

Your plugin directory must contain:
- `plugin.json` - Plugin manifest with name, version, port, description
- `Dockerfile` - Runtime configuration and startup command
- Source code in any language

### 2. Plugin Manifest

The `plugin.json` file defines your plugin:

```json
{
  "name": "my-crm-plugin",
  "version": "1.0.0",
  "port": 8080,
  "description": "My awesome CRM plugin"
}
```

### 3. Dockerfile

Your Dockerfile should:
- Set up the runtime environment
- Install dependencies
- Copy your code
- Expose the port specified in plugin.json
- Define the startup command

### 4. Plugin Code

Your plugin should:
- Start an HTTP server on the port specified in plugin.json
- Handle requests from the CRM system
- Return JSON responses

## Security Considerations

- Each plugin runs in its own isolated microVM
- Plugins have limited access to host resources
- Network access is controlled and isolated
- Root filesystem is read-only by default

## Development Notes

- The current implementation uses placeholder files for ext4 generation
- In production, you'd use tools like `virt-make-fs` for proper ext4 creation
- Firecracker requires specific kernel configurations
- Network setup requires CNI plugins

## Troubleshooting

1. **Docker not found**: Ensure Docker is installed and running
2. **Permission denied**: Docker operations may require appropriate permissions
3. **Plugin build fails**: Check that plugin.json and Dockerfile are valid
4. **Firecracker not found**: Ensure Firecracker is installed and in PATH

## Future Enhancements

- [ ] Proper ext4 filesystem generation
- [ ] Network communication between CRM and plugins
- [ ] Plugin configuration management
- [ ] Metrics and monitoring
- [ ] Plugin versioning
- [ ] Hot reloading of plugins
- [ ] Resource limits and quotas
- [ ] Plugin marketplace

## License

[Add your license here] # cu-firecracker
