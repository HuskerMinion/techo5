#!/bin/sh
# build.sh — techo5-jpeg, in an Alpine container of the root filesystem's architecture, linked statically
# against libjpeg-turbo (its NEON code included), so the root filesystem needs no library for it.
# libjpeg-turbo is built in the container, unmodified, from the release Alpine 3.24 packages, checked
# against the sha512 Alpine's APKBUILD pins for that tarball (aports, 3.24-stable, main/libjpeg-turbo),
# with Alpine's own options for its static library.
#
#   tools/jpeg/build.sh [arm|arm64]
#
# arm (the default) is the armv7 userspace every image runs, athena's on its 64-bit kernel too; arm64 is
# for a 64-bit root filesystem. Under QEMU on another machine it takes a few minutes. Output:
# bin/techo5-jpeg-<arch> (deploy-rootfs.sh stages it as mkrootfs.sh's bin/techo5-jpeg).
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
VERSION=3.1.3
SHA512=d3410a072044b4962c1aa08eb144b4e4b959f4f65203dfac4013b14e2fd987b9a6ee9b59f5570980fa691ddf5e9f9d3aa328a63afb487a46c2e76de722f3d693
mkdir -p "$ROOT/bin"
docker run --rm --platform "$PLATFORM" -e VERSION="$VERSION" -e SHA512="$SHA512" -e ARCH="$ARCH" \
	-v "$ROOT/tools/jpeg:/src:ro" -v "$ROOT/bin:/out" "$IMAGE" sh -ec '
	apk add -q --no-cache build-base cmake samurai curl >/dev/null
	cd /tmp
	curl -fsSLO "https://github.com/libjpeg-turbo/libjpeg-turbo/releases/download/$VERSION/libjpeg-turbo-$VERSION.tar.gz"
	echo "$SHA512  libjpeg-turbo-$VERSION.tar.gz" | sha512sum -c -
	tar -xzf "libjpeg-turbo-$VERSION.tar.gz"
	CFLAGS="-O2 -DNDEBUG" cmake -S "libjpeg-turbo-$VERSION" -B ljt -G Ninja -DCMAKE_INSTALL_PREFIX=/opt/ljt \
		-DCMAKE_INSTALL_LIBDIR=/opt/ljt/lib -DBUILD_SHARED_LIBS=False -DENABLE_SHARED=False \
		-DENABLE_STATIC=True -DWITH_JPEG8=1 -DCMAKE_BUILD_TYPE=None >/tmp/cmake.log 2>&1 \
		|| { tail -25 /tmp/cmake.log; exit 1; }
	# Without its SIMD code it decodes no faster than image/jpeg: refuse a build that lost it.
	grep -q "SIMD extensions: .*WITH_SIMD = 1" /tmp/cmake.log || { grep -i -e simd -e neon /tmp/cmake.log; echo "libjpeg-turbo was configured without its SIMD code" >&2; exit 1; }
	cmake --build ljt >/tmp/build.log 2>&1 || { tail -25 /tmp/build.log; exit 1; }
	cmake --install ljt >/dev/null
	gcc -O2 -Wall -Wextra -static -I/opt/ljt/include -o /tmp/techo5-jpeg /src/techo5-jpeg.c /opt/ljt/lib/libturbojpeg.a
	strip /tmp/techo5-jpeg
	cp /tmp/techo5-jpeg "/out/techo5-jpeg-$ARCH"
	grep -i -e "SIMD extensions" -e neon /tmp/cmake.log
	ls -l "/out/techo5-jpeg-$ARCH"'
