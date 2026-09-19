FROM --platform=linux/arm64 debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential bc bison flex libssl-dev libelf-dev libncurses-dev \
    python3 python3-pip git ca-certificates kmod cpio xz-utils lz4 zstd \
    device-tree-compiler rsync file wget curl pkg-config dwarves \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /work
