#!/bin/sh
# Build an Alpine armv7 slot with the application and matching mainline modules.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(dirname "$(dirname "$D")")
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
STAGE=$D/out/rootfs-stage
BASE=$INPUTS/alpine-minirootfs-3.24.2-armv7.tar.gz
VERSION=${VERSION:-mainline}

[ -f "$BASE" ] || { echo "Run tools/fetch-inputs.py --device show first" >&2; exit 1; }
rm -rf "$STAGE"
mkdir -p "$STAGE/bin" "$STAGE/tools" "$STAGE/inputs/apks312"
cp "$BASE" "$STAGE/inputs/"
cp "$INPUTS"/apks312/wpa_supplicant-*.apk "$INPUTS"/apks312/libssl1.1-*.apk "$INPUTS"/apks312/libcrypto1.1-*.apk "$STAGE/inputs/apks312/"
cp "$ROOT/tools/linux/mkrootfs.sh" "$ROOT/tools/linux/packages-rootfs.txt" \
   "$ROOT/tools/linux/slotctl" "$ROOT/tools/linux/techo5-lib.sh" "$STAGE/tools/"
rm -rf "$STAGE/overlay"
cp -R "$ROOT/tools/linux/rootfs" "$STAGE/overlay"
mkdir -p "$STAGE/overlay/usr/share/techo5/models"
cp "$INPUTS"/models/*.tflite "$INPUTS"/models/*.json "$STAGE/overlay/usr/share/techo5/models/"

docker run --rm -e VERSION="$VERSION" -v "$ROOT:/work" \
  -v techo5-go-mod:/go/pkg/mod -v techo5-go-build:/root/.cache/go-build \
  -w /work golang:1.26 sh -ec '
    export GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0
    cd echod
    go build -trimpath -ldflags "-s -w -X github.com/HuskerMinion/techo5/echod/internal/layout.Version=$VERSION" \
      -o /work/custom-kernel/7.2.6/out/rootfs-stage/bin/techo5 ./cmd/echod
    cd ..
    for cmd in rebootto btbridge; do
      go build -trimpath -ldflags "-s -w" -o custom-kernel/7.2.6/out/rootfs-stage/bin/$cmd ./cmd/$cmd
    done'

"$D/k6.sh" 'sh /host/initramfs/build-kernel-bundle.sh'
cp "$D/out/kernel-rootfs.tar.gz" "$STAGE/inputs/kernel.tar.gz"
docker import --platform linux/arm/v7 "$BASE" techo5-rootfs:3.24.2 >/dev/null
docker run --rm --platform linux/arm/v7 -v "$STAGE:/in:ro" -v "$D/out:/out" \
  techo5-rootfs:3.24.2 /bin/sh /in/tools/mkrootfs.sh \
  -i /in -w /tmp/techo5-rootfs -V "$VERSION" -o /out/techo5-rootfs-mainline.tar.gz
