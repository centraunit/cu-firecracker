FROM ubuntu:22.04

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    wget \
    git \
    build-essential \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Go
RUN wget https://go.dev/dl/go1.24.5.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go1.24.5.linux-amd64.tar.gz && \
    rm go1.24.5.linux-amd64.tar.gz

ENV PATH=$PATH:/usr/local/go/bin

# Install Firecracker
RUN curl -Lo firecracker.tgz https://github.com/firecracker-microvm/firecracker/releases/download/v1.12.1/firecracker-v1.12.1-x86_64.tgz && \
    tar -xzf firecracker.tgz && \
    mv release-v1.12.1-x86_64/firecracker-v1.12.1-x86_64 /usr/local/bin/firecracker && \
    chmod +x /usr/local/bin/firecracker && \
    rm -rf firecracker.tgz release-v1.12.1-x86_64

# Build a Linux kernel for Firecracker
RUN apt-get update && apt-get install -y \
    build-essential \
    libncurses-dev \
    bison \
    flex \
    libssl-dev \
    libelf-dev \
    bc \
    && rm -rf /var/lib/apt/lists/*

# Create kernel directory and download a real v6.x kernel
RUN mkdir -p /opt/kernel && \
    wget -O /opt/kernel/vmlinux https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-6.6.99.tar.xz && \
    cd /opt/kernel && \
    tar -xf vmlinux && \
    cd linux-6.6.99 && \
    make defconfig && \
    make -j$(nproc) vmlinux && \
    cp vmlinux /opt/kernel/vmlinux && \
    cd / && rm -rf /opt/kernel/linux-6.6.99*

# Install Docker (for building plugins)
RUN curl -fsSL https://get.docker.com -o get-docker.sh && \
    sh get-docker.sh

# Install CNI plugins for Firecracker networking
RUN mkdir -p /opt/cni/bin && \
    wget -O /opt/cni/bin/cni-plugins-linux-amd64-v1.7.1.tgz https://github.com/containernetworking/plugins/releases/download/v1.7.1/cni-plugins-linux-amd64-v1.7.1.tgz && \
    tar -xzf /opt/cni/bin/cni-plugins-linux-amd64-v1.7.1.tgz -C /opt/cni/bin && \
    rm /opt/cni/bin/cni-plugins-linux-amd64-v1.7.1.tgz

# Build tc-redirect-tap plugin (required for Firecracker)
RUN git clone https://github.com/awslabs/tc-redirect-tap.git /tmp/tc-redirect-tap && \
    cd /tmp/tc-redirect-tap && \
    make install && \
    rm -rf /tmp/tc-redirect-tap

# Install network tools
RUN apt-get update && apt-get install -y \
    iproute2 \
    bridge-utils \
    iptables \
    && rm -rf /var/lib/apt/lists/*

# Set up CNI configuration for Firecracker VMs
RUN mkdir -p /etc/cni/conf.d && \
    echo '{"name":"fcnet","cniVersion":"1.0.0","plugins":[{"type":"ptp","ipMasq":true,"ipam":{"type":"host-local","subnet":"192.168.127.0/24","resolvConf":"/etc/resolv.conf"}},{"type":"tc-redirect-tap"}]}' > /etc/cni/conf.d/fcnet.conflist

# Add CNI bin to PATH
ENV PATH=$PATH:/opt/cni/bin

# Set up working directory
WORKDIR /app

# Copy Go module files
COPY go.mod go.sum ./

# Download Go dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the CMS application
RUN go build -o cms .

# Create necessary directories
RUN mkdir -p /tmp/firecracker /var/lib/firecracker

# Create network namespace directory for Firecracker
RUN mkdir -p /var/run/netns && chmod 755 /var/run/netns

# Create data directory with proper permissions for host user access
RUN mkdir -p /app/data/logs && \
    chmod 777 /app/data && \
    chown -R 1000:1000 /app/data

# Expose port
EXPOSE 8080

# Set environment variables
ENV FIRECRACKER_PATH=/usr/local/bin/firecracker
ENV KERNEL_PATH=/opt/kernel/vmlinux

# Run the CMS as root (required for Firecracker networking)
CMD ["./cms"] 