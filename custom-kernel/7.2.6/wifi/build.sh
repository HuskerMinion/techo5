#!/bin/sh
# Build the Amazon gen4 MT7668 Wi-Fi driver against the configured Linux 7.2 kernel.
# Sources: the pristine Amazon tree plus our patches in /host/wifi/patches (applied in order).
set -e
SRC=/host/wifi-amazon/module
K=${KDIR:-/src/linux-7.2.6}/out
W=${WIFI_BUILD_DIR:-/src/wifi-7.2}
rm -rf "$W"; mkdir -p "$W"
cp -a "$SRC"/. "$W"/
cp /host/wifi-amazon/configs/cronos.config "$W/.config"
sed -i -e "s/^MTK_MET_PROFILING_SUPPORT=yes/MTK_MET_PROFILING_SUPPORT=no/" \
    -e "s/^CFG_GET_TEMPURATURE=n/CFG_GET_TEMPURATURE=y/" "$W/.config"
cd "$W"
for p in /host/wifi/patches/*.patch; do [ -e "$p" ] && patch -p1 -s < "$p" && echo "applied $(basename $p)"; done
make -C "$K" M="$W" ARCH=arm64 MODULE_NAME=mt76x8_wlan -j10 modules
cp "$W/mt76x8_wlan.ko" /out/
