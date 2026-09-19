#!/bin/sh
# Run a command in the kernel build container with the Linux-native source volume at /src and the
# host's config/, patches/ and wifi-amazon/ mounted. Usage: ./k6.sh <sh command>
D=$(cd "$(dirname "$0")" && pwd)
if [ ! -f "$D/wifi-amazon/module/Makefile" ] ||
   [ ! -f "$D/sources/amazon-kernel/drivers/misc/mediatek/imgsensor/src/mt8163/ov02b10_mipi_raw/ov02bmipiraw_Sensor.c" ]; then
    echo "Initialize kernel dependencies with git submodule update --init --recursive" >&2
    exit 1
fi
exec docker run --rm --platform linux/arm64 \
  -v techo5-k6:/src \
  -v "$D/prepare.sh:/prepare.sh:ro" -v "$D/config:/cfg:ro" -v "$D/patches:/patches:ro" -v "$D/wifi-amazon:/host/wifi-amazon:ro" \
  -v "$D/sources/amazon-kernel:/host/amazon-kernel:ro" -v "$D/wifi:/host/wifi" -v "$D/codec-work:/host/codec-work" -v "$D/initramfs:/host/initramfs" -v "$D/out:/out" \
  -w /src techo5-kbuild sh -c "$*"
