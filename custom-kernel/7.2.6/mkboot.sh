#!/bin/sh
# Assemble the versioned kernel, board device tree, and Alpine rescue image.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
exec python3 "$D/mkboot-injected.py" --lineage "${BOOT_TEMPLATE:-$D/boot-lineage.img}" \
    --ramdisk "$D/out/initramfs-rescue.xz" --kernel "$D/out/Image.gz" \
    --dtb "$D/out/cronos-display.dtb" --cmdline-append 'clk_ignore_unused pd_ignore_unused' \
    -o "$D/out/techo5-linux-7.2.6.img"
