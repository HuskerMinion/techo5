#!/bin/sh
# build.sh — techo5-jpeg for the 64-bit userspace (aarch64), in an arm64 Alpine container, linked
# statically against Alpine's libjpeg-turbo (its NEON code included), so the root filesystem needs no
# library for it. Output: bin/techo5-jpeg-arm64, which build-rootfs.sh installs.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
mkdir -p "$ROOT/bin"
# alpine:3.24 is shared with the armv7 builds, and a tag pulled for arm/v7 stays arm/v7 whatever
# --platform says: take the arm64 image first, and refuse a container that is not aarch64.
docker pull -q --platform linux/arm64 alpine:3.24 >/dev/null
docker run --rm --platform linux/arm64 -v "$ROOT/tools/jpeg:/src:ro" -v "$ROOT/bin:/out" alpine:3.24 sh -ec '
	[ "$(uname -m)" = aarch64 ] || { echo "the container is $(uname -m), not aarch64" >&2; exit 1; }
	apk add -q --no-cache build-base libjpeg-turbo-dev libjpeg-turbo-static >/dev/null
	gcc -O2 -Wall -Wextra -static -o /tmp/techo5-jpeg /src/techo5-jpeg.c -lturbojpeg
	strip /tmp/techo5-jpeg
	cp /tmp/techo5-jpeg /out/techo5-jpeg-arm64
	apk info -v libjpeg-turbo-static
	ls -l /out/techo5-jpeg-arm64'
