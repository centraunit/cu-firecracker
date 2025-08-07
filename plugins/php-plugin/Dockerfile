FROM php:8.2-cli-alpine

# Install required packages  
RUN apk add --no-cache zip unzip

# Set working directory
WORKDIR /app

# Copy application files
COPY index.php /app/
COPY plugin.json /app/

# Create a more robust init script with proper PHP paths
RUN rm -f /sbin/init && \
    echo '#!/bin/sh' > /sbin/init && \
    echo 'set -e' >> /sbin/init && \
    echo 'export PATH="/usr/local/bin:/usr/bin:/bin:$PATH"' >> /sbin/init && \
    echo 'echo "=== PHP Content Manager Plugin Starting ==="' >> /sbin/init && \
    echo 'cd /app' >> /sbin/init && \
    echo 'echo "Working directory: $(pwd)"' >> /sbin/init && \
    echo 'echo "PATH: $PATH"' >> /sbin/init && \
    echo 'echo "Files in /app:"' >> /sbin/init && \
    echo 'ls -la /app/' >> /sbin/init && \
    echo 'echo "Looking for PHP binary..."' >> /sbin/init && \
    echo 'which php || echo "PHP not found in PATH, trying absolute paths..."' >> /sbin/init && \
    echo 'ls -la /usr/local/bin/php* || echo "No PHP in /usr/local/bin"' >> /sbin/init && \
    echo 'ls -la /usr/bin/php* || echo "No PHP in /usr/bin"' >> /sbin/init && \
    echo 'PHP_BIN="/usr/local/bin/php"' >> /sbin/init && \
    echo 'if [ ! -f "$PHP_BIN" ]; then' >> /sbin/init && \
    echo '    echo "ERROR: PHP binary not found at $PHP_BIN"' >> /sbin/init && \
    echo '    exit 1' >> /sbin/init && \
    echo 'fi' >> /sbin/init && \
    echo 'echo "PHP version: $($PHP_BIN -v | head -1)"' >> /sbin/init && \
    echo 'echo "Testing PHP syntax..."' >> /sbin/init && \
    echo '$PHP_BIN -l /app/index.php' >> /sbin/init && \
    echo 'echo "Starting PHP HTTP server on 0.0.0.0:80..."' >> /sbin/init && \
    echo '$PHP_BIN -S 0.0.0.0:80 -t /app /app/index.php 2>&1 &' >> /sbin/init && \
    echo 'PHP_PID=$!' >> /sbin/init && \
    echo 'echo "PHP server started with PID: $PHP_PID"' >> /sbin/init && \
    echo 'sleep 2' >> /sbin/init && \
    echo 'echo "Checking if PHP server is running..."' >> /sbin/init && \
    echo 'if kill -0 $PHP_PID 2>/dev/null; then' >> /sbin/init && \
    echo '    echo "PHP server is running successfully"' >> /sbin/init && \
    echo 'else' >> /sbin/init && \
    echo '    echo "ERROR: PHP server failed to start!"' >> /sbin/init && \
    echo '    exit 1' >> /sbin/init && \
    echo 'fi' >> /sbin/init && \
    echo 'echo "PHP CMS Plugin HTTP server listening on port 80"' >> /sbin/init && \
    echo 'echo "Waiting for PHP server process..."' >> /sbin/init && \
    echo 'wait $PHP_PID' >> /sbin/init && \
    chmod +x /sbin/init

# Expose port
EXPOSE 80

# Set init as entrypoint
CMD ["/sbin/init"] 