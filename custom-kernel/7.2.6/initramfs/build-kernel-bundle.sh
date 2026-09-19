#!/bin/sh
# Package modules and firmware for an installed Alpine root filesystem.
set -eu
KDIR=${KDIR:-/src/linux-7.2.6}
R=/tmp/techo5-kernel-rootfs
rm -rf "$R"
mkdir -p "$R/lib/firmware" "$R/etc/techo5" "$R/usr/share/licenses/techo5-kernel"
cp /host/initramfs/firmware/LICENSE "$R/usr/share/licenses/techo5-kernel/regulatory-db"
release=$(make -s -C "$KDIR" ARCH=arm64 O=out kernelrelease)
make -s -C "$KDIR" ARCH=arm64 O=out INSTALL_MOD_PATH="$R" modules_install
mkdir -p "$R/lib/modules/$release/extra"
cp /out/mt76x8_wlan.ko "$R/lib/modules/$release/extra/"
rm -f "$R/lib/modules/$release/build" "$R/lib/modules/$release/source"
cp -R /host/initramfs/firmware/amazon /host/initramfs/firmware/mediatek "$R/lib/firmware/"
cp /host/initramfs/firmware/regulatory.db* "$R/lib/firmware/"
cp /host/initramfs/firmware/tas5805m/tas5805m_dsp_default.bin "$R/lib/firmware/"
printf '%s\n' "$release" > "$R/etc/techo5/kernel-release"
tar -czf /out/kernel-rootfs.tar.gz -C "$R" .
