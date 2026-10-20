#!/bin/sh
# build.sh — techo5-gpu for the 64-bit userspace (aarch64, musl), in an arm64 Alpine container, against
# Alpine's EGL/GLES/GBM/libdrm headers; at run time it loads the lean Mesa of tools/linux/build-mesa.sh,
# which has the same libraries. Output: bin/techo5-gpu-arm64, which build-rootfs.sh installs.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
mkdir -p "$ROOT/bin"
docker run --rm --platform linux/arm64 -v "$ROOT/tools/gpu:/src:ro" -v "$ROOT/bin:/out" alpine:3.24 sh -ec '
	apk add -q --no-cache build-base mesa-dev libdrm-dev >/dev/null
	gcc -O2 -Wall -Wextra -Wno-unused-parameter -o /tmp/techo5-gpu /src/techo5-gpu.c \
		$(pkg-config --cflags --libs egl glesv2 gbm libdrm)
	strip /tmp/techo5-gpu
	cp /tmp/techo5-gpu /out/techo5-gpu-arm64
	ls -l /out/techo5-gpu-arm64'
