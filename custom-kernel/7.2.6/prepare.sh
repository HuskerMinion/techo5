#!/bin/sh
# Prepare the pinned kernel source and install TECHO5 board support.
set -eu
KDIR=${KDIR:-/src/linux-7.2.6}
export KDIR
cd "$KDIR"
base_patches="/patches/0000-mt8163-linux-7.2.patch
    /patches/0001-mt8163-dtsi-thermal-include.patch
    /patches/0003-phy-mtk-mipi-dsi-mt8163-determine-rate.patch
    /patches/0004-clk-mt8163-apmixedsys-register-plls-dev.patch"
checksum=$(sha256sum $base_patches | sha256sum | cut -d ' ' -f 1)
if [ -f .techo5-platform ]; then
    [ "$(cat .techo5-platform)" = "$checksum" ] || {
        echo "Platform patches changed; prepare a fresh kernel source directory" >&2
        exit 1
    }
else
for p in /patches/0000-mt8163-linux-7.2.patch \
    /patches/0001-mt8163-dtsi-thermal-include.patch \
    /patches/0003-phy-mtk-mipi-dsi-mt8163-determine-rate.patch \
    /patches/0004-clk-mt8163-apmixedsys-register-plls-dev.patch; do
    if git apply --check "$p" 2>/dev/null; then
        git apply "$p"
    elif ! git apply --reverse --check "$p" 2>/dev/null; then
        echo "Kernel source does not match $p" >&2
        exit 1
    fi
done
echo "$checksum" > .techo5-platform
fi
for p in /patches/0002-cronos-techo5-dtsi.sh \
    /patches/0004-mt8163-mt-boot.sh \
    /patches/0005-cronos-panel-driver.sh \
    /patches/0005-mt8163-cronos-sound.sh \
    /patches/0006-amazon-spi-pcm.sh \
    /patches/0007-tlv320aic3101.sh \
    /patches/0008-tas5805m-delay.sh \
    /patches/0009-cronos-privacy.sh \
    /patches/0010-cronos-camera.sh \
    /patches/0011-mt8163-thermal.sh; do
    sh "$p"
done
mkdir -p out
cp /cfg/cronos.config out/.config
make -s ARCH=arm64 O=out olddefconfig
