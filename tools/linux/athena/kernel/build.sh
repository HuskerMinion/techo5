#!/bin/bash
# SPDX-License-Identifier: MIT
#
# build.sh: build the TECHO5 mainline kernel for the Echo Show 8 2nd gen (athena, MT8183) from
# the kernel.org linux-6.18.56 tarball and the patches here, and pack it into a boot image with
# the MT7663 firmware in its ramdisk. Runs on Linux or in WSL. See README.md.
#
#   tools/linux/athena/kernel/build.sh
#
# Environment:
#   WORK       work directory (default ~/techo5-athena-kbuild): downloads, the kernel tree, output
#   OUT        output directory (default $WORK/out)
#   KTREE      use this unpacked linux-6.18.56 tree in place instead of unpacking the tarball
#              afresh (faster rebuilds). It must be pristine or patched by this script before:
#              every step below is idempotent.
#   JOBS       parallel make jobs (default: nproc)
#   CROSS_COMPILE  default aarch64-linux-gnu- (built and tested with Ubuntu 24.04's GCC 13.3)
#
# Needs: curl, sha256sum, tar, xz, make, gcc, bison, flex, bc, perl, python3, patch, an aarch64
# cross GCC, libssl-dev, libelf-dev.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${WORK:-$HOME/techo5-athena-kbuild}
OUT=${OUT:-$WORK/out}
JOBS=${JOBS:-$(nproc)}

KVER=6.18.56
KURL=https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$KVER.tar.xz
KSHA256=4633bbda2ab95eaf2a7af4041236451d6759c72e6946265531af4b3aacefbf89

# MT7663 Wi-Fi/Bluetooth firmware from linux-firmware, pinned. It ships as separate files in the
# boot image's ramdisk (lib/firmware/...), under MediaTek's license, not linked into the kernel.
FW_TAG=20260916
FW_URL=https://git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git/plain
FW_FILES="
5e1a65036ccf9f53a6f69d9d8f75a07f546f9a7cec83ca89f24907925c54f079 mediatek/mt7663_n9_rebb.bin
d8b8488f37f65e41ded9a911950afb000d236b36e5aa94ad9650ad0c3a86fd45 mediatek/mt7663_n9_v3.bin
534f2152f9b0f48dfcec9c1727b0bab9a3727a655d76d5ec818cc39a55602336 mediatek/mt7663pr2h.bin
e812442fd631ecbea7d2898c3168cb5780377378a0eed74c14fe8d8057fe547c mediatek/mt7663pr2h_rebb.bin
a90d3f66704d85889945fec5525ea77622549da83aced1aac99828383f8f1805 LICENSES/LICENCE.mediatek
"
# in the ramdisk under lib/firmware (the license text as lib/firmware/LICENCE.mediatek)
RAMDISK_FILES="LICENCE.mediatek mediatek/mt7663_n9_rebb.bin mediatek/mt7663_n9_v3.bin mediatek/mt7663pr2h.bin mediatek/mt7663pr2h_rebb.bin"

export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
export KBUILD_BUILD_USER=techo5 KBUILD_BUILD_HOST=athena-build
# Fixed, so a build is reproducible, but it is what `uname -v` says: bump it whenever the kernel
# changes (a patch, the config, the kernel.org version), or a unit cannot tell the new kernel from
# the one before. 2026-10-15 was v1.2.16 to v1.2.18's, 2026-10-16 v1.2.19 to v1.2.23's, 2026-10-19
# v1.2.24's, 2026-10-20 12:00 the echo reference and panic on an RCU stall, 2026-10-23 12:00 the
# 0x35 panel on Amazon's DSI link (#187).
export KBUILD_BUILD_TIMESTAMP="2026-10-23 12:00:00 UTC" KBUILD_BUILD_VERSION=1
export LC_ALL=C TZ=UTC

mkdir -p "$WORK/cache" "$OUT"
WORK=$(cd "$WORK" && pwd) OUT=$(cd "$OUT" && pwd)
CACHE=$WORK/cache

fetch() { # fetch URL DEST SHA256
	if [ ! -f "$2" ] || ! echo "$3  $2" | sha256sum -c --quiet - 2>/dev/null; then
		mkdir -p "$(dirname "$2")"
		curl -sSfL -o "$2.part" "$1"
		mv "$2.part" "$2"
	fi
	echo "$3  $2" | sha256sum -c --quiet - || { echo "sha256 mismatch: $2" >&2; exit 1; }
}

# 1. Kernel source
if [ -n "${KTREE:-}" ]; then
	KTREE=$(cd "$KTREE" && pwd)
	grep -q "^SUBLEVEL = ${KVER##*.}$" "$KTREE/Makefile" || { echo "$KTREE is not linux-$KVER" >&2; exit 1; }
else
	fetch "$KURL" "$CACHE/linux-$KVER.tar.xz" "$KSHA256"
	KTREE=$WORK/linux-$KVER
	rm -rf "$KTREE"
	tar -C "$WORK" -xJf "$CACHE/linux-$KVER.tar.xz"
fi

# 2. Firmware, staged as it goes in the ramdisk
FWDIR=$CACHE/linux-firmware-$FW_TAG
echo "$FW_FILES" | while read -r sha path; do
	if [ -n "$path" ]; then fetch "$FW_URL/$path?h=$FW_TAG" "$FWDIR/$path" "$sha"; fi
done
STAGE=$WORK/ramdisk-firmware
rm -rf "$STAGE"
install -D -m 644 "$FWDIR/LICENSES/LICENCE.mediatek" "$STAGE/LICENCE.mediatek"
for f in $RAMDISK_FILES; do
	[ "$f" = LICENCE.mediatek ] || install -D -m 644 "$FWDIR/$f" "$STAGE/$f"
done

# 3. Board files, then the patches in order
DTSDIR=$KTREE/arch/arm64/boot/dts/mediatek
cp "$HERE/dts/mt8183-amazon-athena.dts" "$DTSDIR/"
python3 "$HERE/dsp_slot.py" empty "$DTSDIR/mt8183-amazon-athena-dsp-slot.bin"
cp "$HERE/src/mt8183-athena.c" "$KTREE/sound/soc/mediatek/mt8183/"

P=$HERE/patches
if ! grep -q "leave cache-only mode before the clocks are enabled" \
		"$KTREE/sound/soc/mediatek/mt8183/mt8183-afe-pcm.c"; then
	patch -d "$KTREE" -p1 -N -s --no-backup-if-mismatch \
		-i "$P/0001-mt8183-afe-leave-cache-only-before-enabling-clocks.patch"
fi
python3 "$P/patch-kbuild.py" "$KTREE"
python3 "$P/patch-afe-di2.py" "$KTREE"
python3 "$P/patch-afe-awb-dl1.py" "$KTREE"
python3 "$P/patch-usb-force-vbus.py" "$KTREE"
python3 "$P/patch-tcs3400.py" "$KTREE"
python3 "$P/patch-btmtksdio-bdaddr.py" "$KTREE"
python3 "$P/patch-btmtksdio-p192.py" "$KTREE"
python3 "$P/patch-btmtksdio-late-register.py" "$KTREE"
python3 "$P/patch-mt7663s-firmware-hold.py" "$KTREE"
python3 "$P/patch-display.py" "$KTREE"
python3 "$P/patch-tas5805m-dsp-slot.py" "$KTREE"
python3 "$P/patch-cci-sram.py" "$KTREE"
python3 "$P/patch-thermal-devinfo.py" "$KTREE"
python3 "$P/patch-camera.py" "$KTREE"

# 4. Configuration
cd "$KTREE"
# The debug info records the build directory, and the linker's build-id (kept in the Image)
# hashes it: map the tree's path away so the output does not depend on where it was built.
export KCFLAGS="-fdebug-prefix-map=$KTREE=." KAFLAGS="-fdebug-prefix-map=$KTREE=."
cp "$HERE/athena_defconfig" .config
make -s olddefconfig
for want in 'CONFIG_SND_SOC_MT8183_ATHENA=y' 'CONFIG_SND_SOC_TAS5805M=y' 'CONFIG_CRYPTO_LIB_SHA256=y' \
		'# CONFIG_FRAMEBUFFER_CONSOLE is not set' 'CONFIG_QFMT_V2=y' 'CONFIG_TCS3472=y' \
		'CONFIG_BT_MTKSDIO=y' 'CONFIG_MT7663S=y' 'CONFIG_DRM_MEDIATEK=y' \
		'CONFIG_DRM_PANEL_ILITEK_ILI9881C=y' 'CONFIG_DRM_PANEL_JADARD_JD9365DA_H3=y' 'CONFIG_MTK_IOMMU=y' 'CONFIG_DRM_FBDEV_EMULATION=y' \
		'CONFIG_EXTRA_FIRMWARE=""' 'CONFIG_BLK_DEV_INITRD=y' 'CONFIG_RD_GZIP=y' \
		'CONFIG_INITRAMFS_SOURCE=""' 'CONFIG_LOCALVERSION="-techo5-athena"' \
		'CONFIG_ARM_MEDIATEK_CPUFREQ=y' 'CONFIG_ARM_MEDIATEK_CCI_DEVFREQ=y' 'CONFIG_MTK_SOC_THERMAL=y' \
		'CONFIG_THERMAL_DEFAULT_GOV_POWER_ALLOCATOR=y' 'CONFIG_MEDIA_SUPPORT=y' 'CONFIG_VIDEO_DEV=y' \
		'CONFIG_MTK_SENINF30=y' 'CONFIG_MTK_CAMSV=y' 'CONFIG_PHY_MTK_MIPI_CSI_0_5=y' 'CONFIG_VIDEO_OV13855=y'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done
# The Wi-Fi firewall (rootfs usr/local/sbin/techo5-firewall) and the video decoder's fence
# (echod feature/video) use the legacy iptables tools, and the image ships no modules: every
# table, match and target they use is built in, or Wi-Fi is left open. The tools are 32-bit (armv7
# userspace on this 64-bit kernel), so xtables' 32-bit translation layer is needed too.
for want in 'CONFIG_NETFILTER=y' 'CONFIG_NETFILTER_XTABLES=y' 'CONFIG_NETFILTER_XTABLES_LEGACY=y' \
		'CONFIG_COMPAT=y' 'CONFIG_NETFILTER_XTABLES_COMPAT=y' \
		'CONFIG_NF_CONNTRACK=y' 'CONFIG_NETFILTER_XT_MATCH_STATE=y' 'CONFIG_NETFILTER_XT_MATCH_CONNTRACK=y' \
		'CONFIG_NETFILTER_XT_MATCH_OWNER=y' 'CONFIG_IP_NF_IPTABLES=y' 'CONFIG_IP_NF_IPTABLES_LEGACY=y' \
		'CONFIG_IP_NF_FILTER=y' 'CONFIG_IP_NF_TARGET_REJECT=y' 'CONFIG_IP6_NF_IPTABLES=y' \
		'CONFIG_IP6_NF_IPTABLES_LEGACY=y' 'CONFIG_IP6_NF_FILTER=y' 'CONFIG_IP6_NF_TARGET_REJECT=y'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done

# 5. Build
make -s -j"$JOBS" Image.gz mediatek/mt8183-amazon-athena.dtb
cp arch/arm64/boot/Image.gz "$OUT/Image.gz"
cp "$DTSDIR/mt8183-amazon-athena.dtb" "$OUT/athena.dtb"
cp .config "$OUT/kernel.config"

# 6. Ramdisk and boot image
python3 "$HERE/mkfwcpio.py" "$STAGE" "$OUT/ramdisk.cpio.gz" $RAMDISK_FILES
python3 "$HERE/pack.py" "$OUT/Image.gz" "$OUT/athena.dtb" "$OUT/ramdisk.cpio.gz" "$OUT/boot-athena.img"
python3 "$HERE/check-boot.py" "$OUT/boot-athena.img"

# 7. The source, for the GPL: the whole patched tree this image was built from, with the build's
# configuration in it as techo5-athena.config, and nothing the build made. Reproducible like the
# image: entries sorted by name, owned by 0:0, all at the build's timestamp, one xz thread.
SRCNAME=linux-$KVER-techo5-athena
SRCTAR=$OUT/$SRCNAME-source.tar.xz
PKG=$WORK/source-pkg
rm -rf "$PKG" && mkdir -p "$PKG"
cp -a "$KTREE" "$PKG/$SRCNAME"
make -s -C "$PKG/$SRCNAME" distclean
# distclean leaves the kernel images behind (seen in v1.2.16's source tarball)
rm -f "$PKG/$SRCNAME"/arch/arm64/boot/Image "$PKG/$SRCNAME"/arch/arm64/boot/Image.gz
cp "$OUT/kernel.config" "$PKG/$SRCNAME/techo5-athena.config"
tar -C "$PKG" --sort=name --mtime="$KBUILD_BUILD_TIMESTAMP" --owner=0 --group=0 --numeric-owner \
	--mode='go-w,a+rX' --format=gnu -cf - "$SRCNAME" | xz -T1 -6 > "$SRCTAR.part"
mv "$SRCTAR.part" "$SRCTAR"
# Nothing of the computer it was built on: no path of this build, and no home directory or user name
# that kernel.org's own tarball does not already have (it has a few /home/ in documentation). The
# kernel.org tarball is fetched for that when KTREE stood in for it.
fetch "$KURL" "$CACHE/linux-$KVER.tar.xz" "$KSHA256"
esc() { printf '%s' "$1" | sed 's/[][\.*^$+?(){}|]/\\&/g'; }
pat="/home/|relbuild|$(esc "$WORK")|$(esc "$KTREE")"
for n in "$(id -un)" "${USER:-}" "${SUDO_USER:-}"; do
	case "$n" in ""|root|techo5) ;; *) pat="$pat|\b$(esc "$n")\b";; esac
done
count() { grep -aoiE "$pat" | tr 'A-Z' 'a-z' | sort | uniq -c; }
ours=$(tar -xJOf "$SRCTAR" | count || true)
theirs=$(tar -xJOf "$CACHE/linux-$KVER.tar.xz" | count || true)
names=$(tar -tJf "$SRCTAR" | grep -iE "$pat" || true)
if [ -n "$names" ] || [ "$ours" != "$theirs" ]; then
	echo "$SRCTAR carries something of this computer:" >&2
	echo "  kernel.org's: $theirs" >&2; echo "  ours: $ours" >&2
	[ -z "$names" ] || echo "  in names: $(echo "$names" | head -5)" >&2
	exit 1
fi
python3 "$HERE/check-source.py" --config "$OUT/kernel.config" "$SRCTAR" "$OUT/boot-athena.img"
rm -rf "$PKG"

cd "$OUT" && sha256sum boot-athena.img Image.gz athena.dtb ramdisk.cpio.gz kernel.config "$SRCNAME-source.tar.xz" > SHA256SUMS && cat SHA256SUMS
