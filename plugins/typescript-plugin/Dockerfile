FROM node:18-alpine

WORKDIR /plugin

# Copy package files
COPY package*.json ./

# Install all dependencies (including dev for TypeScript)
RUN npm install

# Install openrc (Alpine's init system) and other necessary packages
RUN apk add --no-cache openrc linux-virt

# Copy source code
COPY . .

# Build TypeScript
RUN npm run build

# Remove dev dependencies to reduce image size
RUN npm prune --omit=dev

# Clean up unnecessary files to reduce image size
RUN rm -rf /usr/local/lib/node_modules/npm /usr/local/share/doc /usr/local/share/man /usr/share/doc /usr/share/man /var/cache/apk/*

# Create service file for openrc
RUN echo '#!/sbin/openrc-run' > /etc/init.d/plugin-service && \
    echo 'name="Plugin Service"' >> /etc/init.d/plugin-service && \
    echo 'description="Node.js Plugin Service"' >> /etc/init.d/plugin-service && \
    echo 'command="/usr/bin/node"' >> /etc/init.d/plugin-service && \
    echo 'command_args="/plugin/index.js"' >> /etc/init.d/plugin-service && \
    echo 'command_background="yes"' >> /etc/init.d/plugin-service && \
    echo 'pidfile="/var/run/plugin-service.pid"' >> /etc/init.d/plugin-service && \
    echo 'depend() {' >> /etc/init.d/plugin-service && \
    echo '  need net' >> /etc/init.d/plugin-service && \
    echo '}' >> /etc/init.d/plugin-service && \
    chmod +x /etc/init.d/plugin-service

# Add service to default runlevel
RUN rc-update add plugin-service default

EXPOSE 8080

# Use openrc as init system
CMD ["/sbin/init"] 