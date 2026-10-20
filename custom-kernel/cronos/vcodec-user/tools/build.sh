#!/bin/sh
# Build everything in Docker (arm64 or amd64 host; native, no emulation).
#   tools/build.sh [VENDOR_LIB_DIR] [make targets...]
# VENDOR_LIB_DIR: the device's /vendor/lib files (default ./vendor-lib).
# Needs libvcodecdrv.so, libvcodec_utility.so, libvcodec_oal.so, libmtk_drvb.so.
# Output: out/lib (stubs), out/bin (harnesses), out/staging (with "stage").
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
VENDOR=${1:-$HERE/vendor-lib}
[ $# -gt 0 ] && shift
VENDOR=$(cd "$VENDOR" && pwd)
CACHE=${CACHE:-$HERE/.cache}
mkdir -p "$CACHE"
CACHE=$(cd "$CACHE" && pwd)
TARGETS=${*:-stage}

docker build -q -t vcodec-user-build "$HERE/docker" >/dev/null
docker run --rm -u "$(id -u):$(id -g)" \
	-v "$HERE:/src" -v "$CACHE:/cache" -v "$VENDOR:/vendor:ro" -w /src \
	vcodec-user-build sh -c "CACHE=/cache tools/fetch-deps.sh && make -j\$(nproc) CACHE=/cache VENDOR=/vendor $TARGETS"
