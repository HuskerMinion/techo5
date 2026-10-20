#!/bin/sh
# SPDX-License-Identifier: MIT
# Build the Echo Show 8 2nd gen (athena) root filesystem slot: the Show's daemon and tools on Alpine
# armv7, with tools/linux/athena/overlay laid over tools/linux/rootfs and busybox-extras added, as
# tools/linux/mkstore.sh expects it. It carries no kernel modules: the athena kernel has its drivers
# built in, so this slot runs on the 7.2.11 kernel and on the 6.18.56 one alike.
#
# Output: out/techo5-rootfs-athena.tar.gz, and the daemon at out/rootfs-stage/bin/techo5.
#
# Environment: VERSION (stamped into the daemon), and RELEASES and RELEASE_KEY, which point the
# daemon's updater at another publisher (release.sh sets all three); unset, it follows upstream's
# releases and trusts upstream's key. KERNEL_IMAGE is a released boot image (build.sh's boot-athena.img,
# its amp tuning slot empty) for the slot to carry: once the slot is good, the daemon writes it to the boot
# partition with the unit's own tuning, on trial (echod/internal/update/kernel), as deploy-rootfs.sh's
# KERNEL_IMAGE does; release.sh passes the release's. TECHO5_INPUTS as for ../cronos/build-rootfs.sh.
#
# T5_ARCH picks the userspace: armv7 (the default, as every other TECHO5 board), or aarch64, a 64-bit
# Alpine and daemon on the 64-bit kernel. aarch64 takes its Alpine base and Alpine 3.12 packages from
# $TECHO5_INPUTS/aarch64 and its helpers from bin/*-arm64 (techo5-aec, techo5-librespot).
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(dirname "$(dirname "$D")")
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
OUT=$D/out
STAGE=$OUT/rootfs-stage
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
OUT=$(cd "$OUT" && pwd) STAGE=$(cd "$STAGE" && pwd)
cp "$BASE" "$STAGE/inputs/"
cp "$ARCH_IN"/apks312/wpa_supplicant-*.apk "$ARCH_IN"/apks312/libssl1.1-*.apk "$ARCH_IN"/apks312/libcrypto1.1-*.apk "$STAGE/inputs/apks312/"
cp "$ROOT/tools/linux/mkrootfs.sh" "$ROOT/tools/linux/packages-rootfs.txt" \
   "$ROOT/tools/linux/slotctl" "$ROOT/tools/linux/techo5-lib.sh" "$STAGE/tools/"
# busybox-extras for udhcpd, which the athena's USB gadget serves addresses with.
echo busybox-extras >> "$STAGE/tools/packages-rootfs.txt"
cp -R "$ROOT/tools/linux/rootfs" "$STAGE/overlay"
cp -R "$ROOT/tools/linux/athena/overlay/." "$STAGE/overlay/"
mkdir -p "$STAGE/overlay/usr/share/techo5/models"
if [ -n "${KERNEL_IMAGE:-}" ]; then
	python3 -I "$ROOT/tools/linux/athena/kernel/check-boot.py" "$KERNEL_IMAGE" >/dev/null ||
		{ echo "KERNEL_IMAGE is not a released athena boot image" >&2; exit 1; }
	mkdir -p "$STAGE/overlay/usr/share/techo5/kernel"
	cp "$KERNEL_IMAGE" "$STAGE/overlay/usr/share/techo5/kernel/boot-athena.img"
	echo "$(sha256sum "$KERNEL_IMAGE" | cut -d' ' -f1)  boot-athena.img" > "$STAGE/overlay/usr/share/techo5/kernel/boot-athena.img.sha256"
fi
cp "$INPUTS"/models/*.tflite "$INPUTS"/models/*.json "$STAGE/overlay/usr/share/techo5/models/"

LDX="-X github.com/HuskerMinion/techo5/echod/internal/layout.Version=$VERSION"
[ -n "${RELEASES:-}" ] && LDX="$LDX -X github.com/HuskerMinion/techo5/echod/internal/update.releases=$RELEASES"
[ -n "${RELEASE_KEY:-}" ] && LDX="$LDX -X github.com/HuskerMinion/techo5/echod/internal/update.releaseKey=$RELEASE_KEY"
docker run --rm -e LDX="$LDX" -e GOENV="$GOENV" -v "$ROOT:/work" -v "$STAGE/bin:/bin-out" \
  -v techo5-go-mod:/go/pkg/mod -v techo5-go-build:/root/.cache/go-build \
  -w /work golang:1.26 sh -ec '
    export GOOS=linux CGO_ENABLED=0 $GOENV
    cd echod
    go build -trimpath -ldflags "-s -w $LDX" -o /bin-out/techo5 ./cmd/echod
    cd ..
    for cmd in fbprobe audioprobe rebootto btbridge; do
      m=.; [ -d echod/cmd/$cmd ] && m=echod
      (cd $m && go build -trimpath -ldflags "-s -w" -o /bin-out/$cmd ./cmd/$cmd)
    done'
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
[ -e "$ROOT/bin/techo5-ffmpeg-$BIN" ] && cp "$ROOT/bin/techo5-ffmpeg-$BIN" "$STAGE/bin/techo5-ffmpeg"
# The video player's way to the hardware decoder (64-bit only): GStreamer, cut down to what it uses
# (tools/linux/build-gst-arm64.sh, under /usr/local/lib/techo5-gst; echod's feature/video, hw.go).
if [ "$T5_ARCH" = aarch64 ] && [ -e "$ROOT/bin/techo5-gst-arm64.tar.gz" ]; then
	tar -xzf "$ROOT/bin/techo5-gst-arm64.tar.gz" -C "$STAGE/overlay"
fi

docker image inspect $IMAGE >/dev/null 2>&1 ||
	docker import --platform $PLATFORM "$BASE" $IMAGE >/dev/null
docker run --rm --platform $PLATFORM -v "$STAGE:/in:ro" -v "$OUT:/out" \
  $IMAGE /bin/sh /in/tools/mkrootfs.sh \
  -i /in -w /tmp/techo5-rootfs -V "$VERSION" -o /out/techo5-rootfs-athena.tar.gz
board=$(tar -xzOf "$OUT/techo5-rootfs-athena.tar.gz" ./etc/techo5/device.conf | grep '^T5_BOARD=' | tail -1)
[ "$board" = T5_BOARD=athena ] || { echo "the rootfs is not athena's (device.conf: '$board')" >&2; exit 1; }
