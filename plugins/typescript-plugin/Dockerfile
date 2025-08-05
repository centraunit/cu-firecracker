# Stage 1: Compile the TypeScript plugin
FROM node:18-alpine AS builder

WORKDIR /plugin
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build
RUN npm prune --omit=dev

# Stage 2: Build minimal Alpine + OpenRC root filesystem
FROM alpine:latest

RUN apk add --no-cache nodejs npm

WORKDIR /plugin
COPY --from=builder /plugin/index.js /plugin/
COPY --from=builder /plugin/package.json /plugin/
COPY --from=builder /plugin/package-lock.json /plugin/
RUN npm install --omit=dev

# Create a simple init script that starts the Node.js application
RUN echo '#!/bin/sh' > /tmp/init.sh && \
    echo 'set -e' >> /tmp/init.sh && \
    echo 'mount -t proc none /proc' >> /tmp/init.sh && \
    echo 'mount -t sysfs none /sys' >> /tmp/init.sh && \
    echo 'mkdir -p /dev/pts' >> /tmp/init.sh && \
    echo 'mount -t devpts none /dev/pts' >> /tmp/init.sh && \
    echo 'cd /plugin' >> /tmp/init.sh && \
    echo 'exec /usr/bin/node index.js' >> /tmp/init.sh && \
    chmod +x /tmp/init.sh && \
    mv /tmp/init.sh /sbin/init

EXPOSE 8080

CMD ["/sbin/init"]
