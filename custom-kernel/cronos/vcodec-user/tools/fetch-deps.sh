#!/bin/sh
# Download (by streaming, no big archives on disk) the two external inputs
# into $CACHE (default: ./.cache, which is not in the repo):
#
#  1. Android NDK r25c *sysroot only* (~85 MB extracted): bionic headers, API
#     30 stub libs, crt objects, compiler-rt builtins. Debian's clang/lld do
#     the compiling, so the x86_64 NDK toolchain binaries are never needed.
#     Apache-2.0 / BSD (see the NDK NOTICE files that are extracted too).
#
#  2. The Android 11 armv7 bionic runtime (linker, libc, libm, libdl,
#     ld-android, libc++, plus AOSP liblog/libcutils/libbase/libutils for
#     reference), taken from the redroid/redroid:11.0.0 arm64 image. That is
#     an AOSP 11 build with 32-bit support, published as a single OCI layer
#     on Docker Hub, which tar can stream-filter (750 MB download, ~240 MB
#     kept). Licenses: AOSP Apache-2.0 / BSD (bionic). The image is pinned by
#     digest.
#     Alternative source: pull the same files from any Android 11 armv7
#     device (see README).
#
# Runs inside the vcodec-user-build container (tools/build.sh calls it). It
# also works on a Mac host, where bsdtar is the system tar.
set -eu

CACHE=${CACHE:-$(dirname "$0")/../.cache}
mkdir -p "$CACHE"
CACHE=$(cd "$CACHE" && pwd)

NDK_VER=r25c
NDK_URL=https://dl.google.com/android/repository/android-ndk-$NDK_VER-linux.zip
REDROID_REPO=redroid/redroid
# arm64 manifest of redroid/redroid:11.0.0-latest (single layer)
REDROID_LAYER=sha256:06cb3c383cf0e765a7c2c8603ddd291a7e30f88ac3541019e6c8367a7065cf9f

if command -v bsdtar >/dev/null 2>&1; then TAR=bsdtar; else TAR=tar; fi

if [ ! -d "$CACHE/ndk/android-ndk-$NDK_VER/toolchains" ]; then
	echo ">> NDK $NDK_VER sysroot -> $CACHE/ndk"
	mkdir -p "$CACHE/ndk"
	curl -fsSL "$NDK_URL" | $TAR -xf - -C "$CACHE/ndk" \
		--include '*/sysroot/usr/include/*' \
		--include '*/sysroot/usr/lib/arm-linux-androideabi/*' \
		--include '*/lib64/clang/*/lib/linux/*arm-android*' \
		--include '*/lib64/clang/*/lib/linux/arm/*' \
		--include '*/NOTICE*' --include "android-ndk-$NDK_VER/source.properties"
fi

if [ ! -f "$CACHE/android11/system/apex/com.android.runtime/bin/linker" ]; then
	echo ">> Android 11 armv7 runtime (redroid layer, streamed) -> $CACHE/android11"
	mkdir -p "$CACHE/android11"
	TOK=$(curl -fsS "https://auth.docker.io/token?service=registry.docker.io&scope=repository:$REDROID_REPO:pull" |
		sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
	curl -fsSL -H "Authorization: Bearer $TOK" \
		"https://registry-1.docker.io/v2/$REDROID_REPO/blobs/$REDROID_LAYER" |
		$TAR -xzf - -C "$CACHE/android11" \
			--include 'system/apex/com.android.runtime/bin/linker' \
			--include 'system/apex/com.android.runtime/lib/bionic/*' \
			--include 'system/lib/ld-android.so' \
			--include 'system/lib/libc++.so' --include 'system/lib/liblog.so' \
			--include 'system/lib/libcutils.so' --include 'system/lib/libbase.so' \
			--include 'system/lib/libutils.so' --include 'system/lib/libion.so' \
			--include 'system/etc/NOTICE*'
fi
echo ">> deps ready in $CACHE"
