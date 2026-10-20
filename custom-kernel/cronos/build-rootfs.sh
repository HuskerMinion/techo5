#!/bin/sh
# Build an Alpine armv7 slot with the application and matching mainline modules.
#
# T5_ARCH=aarch64 builds it 64-bit instead: an aarch64 Alpine and daemon on the 64-bit kernel, with the
# Alpine base and Alpine 3.12 packages from $TECHO5_INPUTS/aarch64 and the helpers from bin/*-arm64.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(dirname "$(dirname "$D")")
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
STAGE=$D/out/rootfs-stage
# The Alpine base tools/fetch-inputs.py fetches, by the name tools/techo5lib.py pins.
ALPINE=$(sed -n "s|^ALPINE_URL = '.*/\(alpine-minirootfs-[^']*\)'|\1|p" "$ROOT/tools/techo5lib.py")
T5_ARCH=${T5_ARCH:-armv7}
case $T5_ARCH in
armv7) ARCH_IN=$INPUTS PLATFORM=linux/arm/v7 GOENV="GOARCH=arm GOARM=7" BIN=arm;;
aarch64) ARCH_IN=$INPUTS/aarch64 PLATFORM=linux/arm64 GOENV="GOARCH=arm64" BIN=arm64
	ALPINE=$(echo "$ALPINE" | sed 's/-armv7\.tar\.gz$/-aarch64.tar.gz/');;
*) echo "T5_ARCH is armv7 or aarch64" >&2; exit 1;;
esac
BASE=$ARCH_IN/$ALPINE
IMAGE=techo5-rootfs:$(echo "$ALPINE" | sed 's/^alpine-minirootfs-//; s/\.tar\.gz$//')
VERSION=${VERSION:-mainline}

[ -f "$BASE" ] || { echo "Run tools/fetch-inputs.py --device show first" >&2; exit 1; }
rm -rf "$STAGE"
mkdir -p "$STAGE/bin" "$STAGE/tools" "$STAGE/inputs/apks312"
cp "$BASE" "$STAGE/inputs/"
cp "$ARCH_IN"/apks312/wpa_supplicant-*.apk "$ARCH_IN"/apks312/libssl1.1-*.apk "$ARCH_IN"/apks312/libcrypto1.1-*.apk "$STAGE/inputs/apks312/"
cp "$ROOT/tools/linux/mkrootfs.sh" "$ROOT/tools/linux/packages-rootfs.txt" \
   "$ROOT/tools/linux/slotctl" "$ROOT/tools/linux/techo5-lib.sh" "$STAGE/tools/"
rm -rf "$STAGE/overlay"
cp -R "$ROOT/tools/linux/rootfs" "$STAGE/overlay"
mkdir -p "$STAGE/overlay/usr/share/techo5/models"
cp "$INPUTS"/models/*.tflite "$INPUTS"/models/*.json "$STAGE/overlay/usr/share/techo5/models/"

# RELEASES and RELEASE_KEY point the daemon's updater at another publisher (release.sh sets both);
# unset, it follows upstream's releases and trusts upstream's key.
LDX="-X github.com/HuskerMinion/techo5/echod/internal/layout.Version=$VERSION"
[ -n "${RELEASES:-}" ] && LDX="$LDX -X github.com/HuskerMinion/techo5/echod/internal/update.releases=$RELEASES"
[ -n "${RELEASE_KEY:-}" ] && LDX="$LDX -X github.com/HuskerMinion/techo5/echod/internal/update.releaseKey=$RELEASE_KEY"
docker run --rm -e LDX="$LDX" -e GOENV="$GOENV" -v "$ROOT:/work" \
  -v techo5-go-mod:/go/pkg/mod -v techo5-go-build:/root/.cache/go-build \
  -w /work golang:1.26 sh -ec '
    export GOOS=linux CGO_ENABLED=0 $GOENV
    cd echod
    go build -trimpath -ldflags "-s -w $LDX" \
      -o /work/custom-kernel/cronos/out/rootfs-stage/bin/techo5 ./cmd/echod
    cd ..
    # audioprobe lives in the daemon module, the others at the top, as deploy-rootfs.sh builds them.
    for cmd in fbprobe audioprobe rebootto btbridge; do
      m=.; [ -d echod/cmd/$cmd ] && m=echod
      (cd $m && go build -trimpath -ldflags "-s -w" -o /work/custom-kernel/cronos/out/rootfs-stage/bin/$cmd ./cmd/$cmd)
    done'
# The echo canceller (C++) and the Spotify Connect receiver (Rust) are built separately, by
# tools/linux/build-aec.sh and build-librespot.sh; shipped when they are there, as deploy-rootfs.sh does.
[ -e "$ROOT/bin/techo5-aec-$BIN" ] && cp "$ROOT/bin/techo5-aec-$BIN" "$STAGE/bin/techo5-aec"
[ -e "$ROOT/bin/techo5-librespot-$BIN" ] && cp "$ROOT/bin/techo5-librespot-$BIN" "$STAGE/bin/techo5-librespot"
# The JPEG decoder (libjpeg-turbo, 64-bit only: tools/jpeg/build.sh), which echod decodes photos and
# camera pictures in where it is there (lib/fastjpeg), and in Go where it is not.
[ -e "$ROOT/bin/techo5-jpeg-$BIN" ] && cp "$ROOT/bin/techo5-jpeg-$BIN" "$STAGE/bin/techo5-jpeg"
# The GPU (64-bit only): the helper that draws the screen and develops the camera on the Mali, and the
# lean Mesa it runs on (tools/linux/build-mesa.sh), with libdrm from Alpine.
if [ "$T5_ARCH" = aarch64 ] && [ -e "$ROOT/bin/techo5-gpu-arm64" ] && [ -e "$ROOT/bin/techo5-mesa-arm64.tar.gz" ]; then
	cp "$ROOT/bin/techo5-gpu-arm64" "$STAGE/bin/techo5-gpu"
	tar -xzf "$ROOT/bin/techo5-mesa-arm64.tar.gz" -C "$STAGE/overlay"
	echo libdrm >> "$STAGE/tools/packages-rootfs.txt"
fi
# The video player's decoder (ffmpeg: tools/linux/build-ffmpeg.sh, build-ffmpeg-arm64.sh), as
# deploy-rootfs.sh ships it: without it the Show's video and camera streams play nothing.
[ -e "$ROOT/bin/techo5-ffmpeg-$BIN" ] && cp "$ROOT/bin/techo5-ffmpeg-$BIN" "$STAGE/bin/techo5-ffmpeg"
# The hardware video decoder's runtime (vcodec-user, tools/bundle.sh): the Android 11 runtime, our
# stand-ins and vcodec-dec, 32-bit, for MediaTek's libraries that stay on the unit's /vendor
# (echod feature/video, hw_vcodec.go; the kernel's /dev/Vcodec is patches/mt8163-vcodec.c).
[ -e "$ROOT/bin/techo5-vcodec-arm.tar.gz" ] && tar -xzf "$ROOT/bin/techo5-vcodec-arm.tar.gz" -C "$STAGE/overlay"

"$D/k6.sh" 'sh /host/initramfs/build-kernel-bundle.sh'
cp "$D/out/kernel-rootfs.tar.gz" "$STAGE/inputs/kernel.tar.gz"
docker image inspect $IMAGE >/dev/null 2>&1 ||
	docker import --platform $PLATFORM "$BASE" $IMAGE >/dev/null
docker run --rm --platform $PLATFORM -v "$STAGE:/in:ro" -v "$D/out:/out" \
  $IMAGE /bin/sh /in/tools/mkrootfs.sh \
  -i /in -w /tmp/techo5-rootfs -V "$VERSION" -o /out/techo5-rootfs-mainline.tar.gz
