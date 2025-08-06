FROM ubuntu:22.04

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    wget \
    git \
    build-essential \
    ca-certificates \
    libncurses-dev \
    bison \
    flex \
    libssl-dev \
    libelf-dev \
    bc \
    iproute2 \
    bridge-utils \
    iptables \
    && rm -rf /var/lib/apt/lists/*

# Install Go
ENV GO_VERSION=1.24.5
RUN wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz && \
    rm go${GO_VERSION}.linux-amd64.tar.gz
ENV PATH=$PATH:/usr/local/go/bin

# Install Firecracker binary
RUN curl -Lo firecracker.tgz https://github.com/firecracker-microvm/firecracker/releases/download/v1.12.1/firecracker-v1.12.1-x86_64.tgz && \
    tar -xzf firecracker.tgz && \
    mv release-v1.12.1-x86_64/firecracker-v1.12.1-x86_64 /usr/local/bin/firecracker && \
    chmod +x /usr/local/bin/firecracker && \
    rm -rf firecracker.tgz release-v1.12.1-x86_64
    
# Build a Firecracker-compatible Linux kernel
RUN mkdir -p /opt/kernel && \
    wget https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-6.6.99.tar.xz -O /opt/kernel/linux.tar.xz && \
    cd /opt/kernel && tar -xf linux.tar.xz && \
    cd linux-6.6.99 && \
    make ARCH=x86_64 x86_64_defconfig && \
    scripts/config --enable CONFIG_VIRTIO_BLK && \
    scripts/config --enable CONFIG_VIRTIO_PCI && \
    scripts/config --enable CONFIG_VIRTIO_NET && \
    scripts/config --enable CONFIG_VIRTIO_MMIO && \
    scripts/config --enable CONFIG_NET && \
    scripts/config --enable CONFIG_NETDEVICES && \
    scripts/config --enable CONFIG_BLK_DEV && \
    scripts/config --enable CONFIG_EXT4_FS && \
    scripts/config --disable CONFIG_MODULES && \
    yes "" | make olddefconfig && \
    make -j$(nproc) vmlinux && \
    cp vmlinux /opt/kernel/vmlinux && \
    cd / && rm -rf /opt/kernel/linux*



# CNI plugins disabled - using manual network setup
# ENV CNI_VERSION=v1.7.1
# RUN mkdir -p /opt/cni/bin && \
#     wget -O /opt/cni/bin/cni-plugins.tgz https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz && \
#     tar -xzf /opt/cni/bin/cni-plugins.tgz -C /opt/cni/bin && rm /opt/cni/bin/cni-plugins.tgz
# ENV PATH=$PATH:/opt/cni/bin

# tc-redirect-tap plugin disabled - using manual network setup  
# RUN git clone https://github.com/awslabs/tc-redirect-tap.git /tmp/tc-redirect-tap && \
#     cd /tmp/tc-redirect-tap && make install && rm -rf /tmp/tc-redirect-tap

# CNI config disabled - using manual network setup
# RUN mkdir -p /etc/cni/conf.d && \
#     echo '{"cniVersion":"1.1.0","name":"fcnet","plugins":[{"type":"bridge","name":"fcnet-bridge","bridge":"fcnetbridge0","isDefaultGateway":true,"ipMasq":true,"hairpinMode":true,"ipam":{"type":"host-local","subnet":"192.168.127.0/24","resolvConf":"/etc/resolv.conf"}},{"type":"firewall"},{"type":"tc-redirect-tap"}]}' > /etc/cni/conf.d/fcnet.conflist



# Set working directory
WORKDIR /app

# Copy Go mod files and install dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy application source
COPY . .

# Build the CMS app
RUN go build -o cms .

# Create necessary directories
RUN mkdir -p /tmp/firecracker /var/lib/firecracker /var/run/netns /app/data/logs && \
    chmod 777 /app/data && \
    chown -R 1000:1000 /app/data

# Expose CMS port
EXPOSE 8080

# Environment variables
ENV FIRECRACKER_PATH=/usr/local/bin/firecracker
ENV KERNEL_PATH=/opt/kernel/vmlinux

# Run the CMS (as root for Firecracker networking)
CMD ["./cms"]
