# Use Alpine Python for minimal size and fast startup
FROM python:3.11-alpine

# Install system dependencies for performance
RUN apk add --no-cache \
    linux-headers \
    gcc \
    musl-dev

# Set working directory
WORKDIR /app

# Copy requirements first for better caching
COPY requirements.txt .

# Install Python dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy application files
COPY app.py .
COPY plugin.json .

# Make app executable
RUN chmod +x app.py

# Create init script that starts the HTTP server
RUN echo '#!/bin/sh' > /tmp/init.sh && \
    echo 'set -e' >> /tmp/init.sh && \
    echo '' >> /tmp/init.sh && \
    echo 'cd /app' >> /tmp/init.sh && \
    echo 'echo "Starting Python CMS Plugin HTTP Server..."' >> /tmp/init.sh && \
    echo '' >> /tmp/init.sh && \
    echo '# Start Python HTTP server' >> /tmp/init.sh && \
    echo 'exec /usr/local/bin/python3 app.py' >> /tmp/init.sh && \
    chmod +x /tmp/init.sh && \
    mv /tmp/init.sh /sbin/init

# Expose port 80
EXPOSE 80

# Health check endpoint
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:80/health || exit 1

# No CMD needed; /sbin/init will be used as entrypoint in Firecracker rootfs 