# Stage 1: Compile the TypeScript plugin
FROM node:18-alpine AS builder

WORKDIR /plugin
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build
RUN npm prune --omit=dev

# Stage 2: Build minimal Alpine root filesystem
FROM alpine:latest

# Install Node.js runtime
RUN apk add --no-cache nodejs npm

# Set working directory for the plugin
WORKDIR /plugin

# Copy the built plugin from builder stage
COPY --from=builder /plugin/index.js /plugin/
COPY --from=builder /plugin/package.json /plugin/
COPY --from=builder /plugin/package-lock.json /plugin/

# Install only production dependencies
RUN npm install --omit=dev

# Create init script that starts the HTTP server
RUN echo '#!/bin/sh' > /tmp/init.sh && \
    echo 'set -e' >> /tmp/init.sh && \
    echo '' >> /tmp/init.sh && \
    echo 'cd /plugin' >> /tmp/init.sh && \
    echo 'echo "Starting TypeScript CMS Plugin HTTP Server..."' >> /tmp/init.sh && \
    echo '' >> /tmp/init.sh && \
    echo '# Start Node.js HTTP server' >> /tmp/init.sh && \
    echo 'exec /usr/bin/node index.js' >> /tmp/init.sh && \
    chmod +x /tmp/init.sh && \
    mv /tmp/init.sh /sbin/init

# Expose plugin port
EXPOSE 80

# No CMD needed; /sbin/init will be used as entrypoint in Firecracker rootfs
