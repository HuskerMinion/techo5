#!/bin/sh
# build.sh — techo5-gpu, in an Alpine container of the root filesystem's architecture, against Alpine's
# EGL/GLES/GBM/libdrm headers; at run time it loads the lean Mesa of tools/linux/build-mesa.sh, which has
# the same libraries.
#
#   tools/gpu/build.sh [arm|arm64]
#
# arm (the default) is the armv7 userspace every image runs, athena's on its 64-bit kernel too; arm64 is
# for a 64-bit root filesystem. Output: bin/techo5-gpu-<arch> (deploy-rootfs.sh stages it as
# mkrootfs.sh's bin/techo5-gpu).
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
ARCH=${1:-arm}
case "$ARCH" in
arm) PLATFORM=linux/arm/v7 IMAGE=arm32v7/alpine:3.24;;
arm64) PLATFORM=linux/arm64 IMAGE=arm64v8/alpine:3.24;;
*) echo "usage: $0 [arm|arm64]" >&2; exit 1;;
esac
# The architecture's own Alpine image: a plain alpine tag pulled for one platform would stand in for
# the other, and the build would come out for the wrong one.
mkdir -p "$ROOT/bin"
docker run --rm --platform "$PLATFORM" -e ARCH="$ARCH" -v "$ROOT/tools/gpu:/src:ro" -v "$ROOT/bin:/out" "$IMAGE" sh -ec '
	apk add -q --no-cache build-base mesa-dev libdrm-dev >/dev/null
	gcc -O2 -Wall -Wextra -Wno-unused-parameter -o /tmp/techo5-gpu /src/techo5-gpu.c \
		$(pkg-config --cflags --libs egl glesv2 gbm libdrm)
	strip /tmp/techo5-gpu
	cp /tmp/techo5-gpu "/out/techo5-gpu-$ARCH"
	ls -l "/out/techo5-gpu-$ARCH"'
