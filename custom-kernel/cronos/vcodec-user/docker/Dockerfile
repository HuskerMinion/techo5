# Build/analysis environment for vcodec-user (runs natively on arm64 or amd64).
# Debian's clang/lld + the Android NDK *sysroot* (headers, API-30 stub libs,
# crt objects, compiler-rt builtins) are enough to build armv7 bionic
# binaries; the NDK's own x86_64 toolchain binaries are not needed.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      clang-14 lld-14 llvm-14 make ca-certificates curl libarchive-tools \
      python3 python3-pyelftools python3-capstone binutils-arm-linux-gnueabihf file \
    && rm -rf /var/lib/apt/lists/*
ENV PATH=/usr/lib/llvm-14/bin:$PATH
WORKDIR /work
