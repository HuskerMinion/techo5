#!/bin/sh
# Package modules and firmware for an installed Alpine root filesystem.
set -eu
KDIR=${KDIR:-/src/linux-7.2.11}
R=/tmp/techo5-kernel-rootfs
F=/out/firmware
python3 - <<'PY'
import hashlib, json
from pathlib import Path
root = Path('/out/firmware')
manifest = json.loads((root / 'manifest.json').read_text())
required = ('amazon/i2s_to_spi_4ch_v193.bin', 'amazon/i2s_to_spi_4ch_v208.bin',
            'mediatek/mt7668pr2h.bin', 'regulatory.db', 'regulatory.db.p7s',
            'tas5805m/tas5805m_dsp_default.bin')
for name in required:
    if hashlib.sha256((root / name).read_bytes()).hexdigest() != manifest.get(name):
        raise SystemExit('Invalid firmware input: ' + name + '; run tools/prepare-firmware.py')
PY
rm -rf "$R"
mkdir -p "$R/lib/firmware" "$R/etc/techo5" "$R/usr/share/licenses/techo5-kernel"
cp /host/initramfs/firmware/LICENSE "$R/usr/share/licenses/techo5-kernel/regulatory-db"
release=$(make -s -C "$KDIR" ARCH=arm64 O=out kernelrelease)
make -s -C "$KDIR" ARCH=arm64 O=out INSTALL_MOD_PATH="$R" modules_install
mkdir -p "$R/lib/modules/$release/extra"
cp /out/mt76x8_wlan.ko "$R/lib/modules/$release/extra/"
rm -f "$R/lib/modules/$release/build" "$R/lib/modules/$release/source"
cp -R "$F/amazon" "$F/mediatek" "$R/lib/firmware/"
cp "$F/regulatory.db" "$F/regulatory.db.p7s" "$R/lib/firmware/"
cp "$F/tas5805m/tas5805m_dsp_default.bin" "$R/lib/firmware/"
printf '%s\n' "$release" > "$R/etc/techo5/kernel-release"
tar -czf /out/kernel-rootfs.tar.gz -C "$R" .
