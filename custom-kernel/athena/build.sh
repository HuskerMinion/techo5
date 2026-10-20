#!/bin/bash
# SPDX-License-Identifier: MIT
#
# build.sh: build TECHO5's Echo Show 8 2nd gen (athena, MT8183) kernel on Linux 7.2.11 and pack it
# into the boot image tools/install-athena.py writes. The board files, patches, packer and checker
# are the ones tools/linux/athena/kernel uses for 6.18.56; patches/port-7.2.py carries them over to
# 7.2 first. See README.md.
#
#   custom-kernel/athena/build.sh
#
# Environment:
#   WORK       work directory (default ~/techo5-athena-7.2.11): downloads, the kernel tree, output
#   OUT        output directory (default $WORK/out)
#   KTREE      use this unpacked linux-7.2.11 tree in place (every step is idempotent)
#   JOBS       parallel make jobs (default: nproc)
#   CROSS_COMPILE  default aarch64-linux-gnu- on x86_64, none on an arm64 host
#   PANEL      the unit's panel, as patches/panel-dts.py names it (default ili9881c-boe, the one the
#              DTS drives; jd9365-boe-new for jd936x_id=0x?5)
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
UP=${UP:-$(cd "$HERE/../../tools/linux/athena/kernel" && pwd)}
WORK=${WORK:-$HOME/techo5-athena-7.2.11}
OUT=${OUT:-$WORK/out}
JOBS=${JOBS:-$(nproc)}

KVER=7.2.11
KURL=https://cdn.kernel.org/pub/linux/kernel/v7.x/linux-$KVER.tar.xz
KSHA256=fd0fc479f983a8c05313f13e9c36330fe6651cf725396748c40d5cd93fa1e6a4

# MT7663 Wi-Fi/Bluetooth firmware from linux-firmware, pinned as in the 6.18.56 build. It ships as
# separate files in the boot image's ramdisk, under MediaTek's license, not linked into the kernel.
FW_TAG=20260916
FW_URL=https://git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git/plain
FW_FILES="
5e1a65036ccf9f53a6f69d9d8f75a07f546f9a7cec83ca89f24907925c54f079 mediatek/mt7663_n9_rebb.bin
d8b8488f37f65e41ded9a911950afb000d236b36e5aa94ad9650ad0c3a86fd45 mediatek/mt7663_n9_v3.bin
534f2152f9b0f48dfcec9c1727b0bab9a3727a655d76d5ec818cc39a55602336 mediatek/mt7663pr2h.bin
e812442fd631ecbea7d2898c3168cb5780377378a0eed74c14fe8d8057fe547c mediatek/mt7663pr2h_rebb.bin
a90d3f66704d85889945fec5525ea77622549da83aced1aac99828383f8f1805 LICENSES/LICENCE.mediatek
401e9c027b0e105efa9eb9b31683924a8d5f5ab3b1654f0915b174effe2df33d mediatek/mt8183/scp.img
"
RAMDISK_FILES="LICENCE.mediatek mediatek/mt7663_n9_rebb.bin mediatek/mt7663_n9_v3.bin mediatek/mt7663pr2h.bin mediatek/mt7663pr2h_rebb.bin mediatek/mt8183/scp.img"

if [ "$(uname -m)" = aarch64 ]; then default_cross=; else default_cross=aarch64-linux-gnu-; fi
export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE-$default_cross}
export KBUILD_BUILD_USER=techo5 KBUILD_BUILD_HOST=athena-build
# Fixed, so a build is reproducible, but it is what `uname -v` says: moved on whenever the kernel changes,
# so a unit can tell a new kernel from the one before.
export KBUILD_BUILD_TIMESTAMP="2026-10-16 00:00:00 UTC" KBUILD_BUILD_VERSION=1
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
# The SCP firmware's accesses to the media blocks (the codecs, the face detector, the ISP's pass 2)
# reach their registers through the AP (patches/scp-mmproxy.py).
python3 "$HERE/patches/scp-vdec-proxy-fw.py" "$STAGE/mediatek/mt8183/scp.img"

# 3. Board files, the 7.2 carry-over, then the athena patches in their order
DTSDIR=$KTREE/arch/arm64/boot/dts/mediatek
cp "$UP/dts/mt8183-amazon-athena.dts" "$DTSDIR/"
python3 "$HERE/patches/panel-dts.py" "$DTSDIR/mt8183-amazon-athena.dts" "${PANEL:-ili9881c-boe}"
python3 "$HERE/patches/gpu-dts.py" "$DTSDIR/mt8183-amazon-athena.dts"
python3 "$HERE/patches/scp-dts.py" "$DTSDIR/mt8183-amazon-athena.dts"
python3 "$UP/dsp_slot.py" empty "$DTSDIR/mt8183-amazon-athena-dsp-slot.bin"
cp "$UP/src/mt8183-athena.c" "$KTREE/sound/soc/mediatek/mt8183/"

P=$UP/patches
if ! grep -q "leave cache-only mode before the clocks are enabled" \
		"$KTREE/sound/soc/mediatek/mt8183/mt8183-afe-pcm.c"; then
	patch -d "$KTREE" -p1 -N -s --no-backup-if-mismatch \
		-i "$P/0001-mt8183-afe-leave-cache-only-before-enabling-clocks.patch"
fi
python3 "$HERE/patches/port-7.2.py" "$KTREE" "$P"
python3 "$P/patch-kbuild.py" "$KTREE"
python3 "$P/patch-afe-di2.py" "$KTREE"
python3 "$P/patch-afe-awb-dl1.py" "$KTREE"
python3 "$P/patch-usb-force-vbus.py" "$KTREE"
python3 "$P/patch-btmtksdio-bdaddr.py" "$KTREE"
python3 "$P/patch-btmtksdio-p192.py" "$KTREE"
python3 "$P/patch-btmtksdio-late-register.py" "$KTREE"
python3 "$P/patch-mt7663s-firmware-hold.py" "$KTREE"
python3 "$P/patch-display.py" "$KTREE"
python3 "$HERE/patches/jd9365-dsi-mode.py" "$KTREE"
python3 "$HERE/patches/jd9365-power-mode-log.py" "$KTREE"
python3 "$HERE/patches/mtk-dsi-cmd-clock-lp.py" "$KTREE"
python3 "$P/patch-tas5805m-dsp-slot.py" "$KTREE"
python3 "$P/patch-cci-sram.py" "$KTREE"
python3 "$P/patch-thermal-devinfo.py" "$KTREE"
python3 "$HERE/patches/athena-camera.py" "$KTREE"
python3 "$HERE/patches/mt7663s-temperature.py" "$KTREE"
python3 "$HERE/patches/mtk-jpeg-enc-error.py" "$KTREE"
python3 "$HERE/patches/mdp3-mt8183-cros-abi.py" "$KTREE"
python3 "$HERE/patches/mdp3-mt8183-isp.py" "$KTREE"
python3 "$HERE/patches/mdp3-dip.py" "$KTREE" "$DTSDIR/mt8183-amazon-athena.dts"
python3 "$HERE/patches/mdp3-cached-capture.py" "$KTREE"
python3 "$HERE/patches/mdp3-rgba.py" "$KTREE"
python3 "$HERE/patches/scp-mmproxy.py" "$KTREE" "$DTSDIR/mt8183-amazon-athena.dts"
python3 "$HERE/patches/mtk-fd.py" "$KTREE" "$DTSDIR/mt8183-amazon-athena.dts"
python3 "$HERE/patches/vpu.py" "$KTREE" "$DTSDIR/mt8183-amazon-athena.dts"
# The AP's side of MCDI (CPU idle; patches/mcdi.py): every core takes the CPU-level idle state, which
# powers it off between wakes, and the cluster-level one, which powers the cluster off after its last
# core (research/mcdi-cpuidle.md: cpu7 off 86% of the time, each cluster's top off about a third of it
# with echod running, the SoC 1.2 C cooler at idle than in WFI). MCDI_CPUS (like 7, or 4,5,6,7) narrows
# it to those cores, at CPU level only; MCDI_CPUS=off
# keeps them all in WFI, with cpuidle.off=1 on the command line (pack.py).
MCDI_CPUS=${MCDI_CPUS:-0,1,2,3,4,5,6,7}
[ "$MCDI_CPUS" = off ] && MCDI_CPUS=
python3 "$HERE/patches/mcdi.py" "$KTREE" "$DTSDIR/mt8183-amazon-athena.dts" "$MCDI_CPUS"
python3 "$HERE/patches/wdt-kexec.py" "$KTREE"

# 4. Configuration: the 6.18.56 configuration, with 7.2's new options at their defaults
cd "$KTREE"
export KCFLAGS="-fdebug-prefix-map=$KTREE=." KAFLAGS="-fdebug-prefix-map=$KTREE=."
cp "$UP/athena_defconfig" .config
# The camera (patches/athena-camera.py) needs V4L2 built in: the image ships no modules.
scripts/config --enable MEDIA_SUPPORT --enable VIDEO_DEV --enable VIDEO_ATHENA_CAMERA
# /proc/net/wireless, where echod reads the Wi-Fi signal (metrics.Wifi), comes with cfg80211's
# wireless extensions.
scripts/config --enable CFG80211_WEXT
# The Mali-G72 (panfrost; the DTS's &gpu, enabled by patches/gpu-dts.py) and the JPEG encoder
# (mtk-jpeg, mt8183.dtsi's jpeg-encoder), which the camera encodes with.
scripts/config --enable DRM_PANFROST --enable VIDEO_MEDIATEK_JPEG
# The SCP (patches/scp-dts.py) and the MDP3 scaler/converter it plans for (patches/mdp3-mt8183-cros-abi.py).
# The H.264 codecs and the face detector run through it too, their registers reached through the AP
# (patches/scp-mmproxy.py).
scripts/config --enable MTK_SCP --enable MTK_CMDQ --enable VIDEO_MEDIATEK_MDP3
# The ISP's pass 2 in front of the MDP (patches/mdp3-mt8183-isp.py, patches/mdp3-dip.py).
scripts/config --enable VIDEO_MEDIATEK_DIP
# The H.264 encoder.
scripts/config --enable VIDEO_MEDIATEK_VCODEC
# The face detector (patches/mtk-fd.py).
scripts/config --enable VIDEO_MEDIATEK_FD
# The VPU (patches/vpu.py: /dev/techo5-vpu, which echod's person detection runs its model on), and
# in a build asked for it (VPU_DEBUG=1) its registers and memory in debugfs, for research (tools/vpu/).
scripts/config --enable TECHO5_VPU
[ -n "${VPU_DEBUG:-${VPU_PROBE:-}}" ] && scripts/config --enable DEBUG_FS --enable TECHO5_VPU_DEBUG
# The SCP firmware's EC side: its console in debugfs (cros_scp/console_log), for when a block misbehaves.
scripts/config --enable CROS_EC_RPMSG --enable CROS_EC_CHARDEV --enable CROS_EC_DEBUGFS
# A core that never comes back (stuck in the secure firmware) leaves the others feeding the hardware
# watchdog, and the unit wedged for good: the lockup detectors panic instead (as does an RCU stall, on
# pack.py's command line), and panic=5 reboots.
scripts/config --enable SOFTLOCKUP_DETECTOR --set-val BOOTPARAM_SOFTLOCKUP_PANIC 1 \
	--enable HARDLOCKUP_DETECTOR --enable BOOTPARAM_HARDLOCKUP_PANIC
make -s olddefconfig
for want in 'CONFIG_SND_SOC_MT8183_ATHENA=y' 'CONFIG_SND_SOC_TAS5805M=y' 'CONFIG_CRYPTO_LIB_SHA256=y' \
		'# CONFIG_FRAMEBUFFER_CONSOLE is not set' 'CONFIG_QFMT_V2=y' 'CONFIG_TCS3472=y' \
		'CONFIG_BT_MTKSDIO=y' 'CONFIG_MT7663S=y' 'CONFIG_DRM_MEDIATEK=y' \
		'CONFIG_DRM_PANEL_ILITEK_ILI9881C=y' 'CONFIG_MTK_IOMMU=y' 'CONFIG_DRM_FBDEV_EMULATION=y' \
		'CONFIG_EXTRA_FIRMWARE=""' 'CONFIG_BLK_DEV_INITRD=y' 'CONFIG_RD_GZIP=y' \
		'CONFIG_INITRAMFS_SOURCE=""' 'CONFIG_LOCALVERSION="-techo5-athena"' \
		'CONFIG_ARM_MEDIATEK_CPUFREQ=y' 'CONFIG_ARM_MEDIATEK_CCI_DEVFREQ=y' 'CONFIG_MTK_SOC_THERMAL=y' \
		'CONFIG_THERMAL_DEFAULT_GOV_POWER_ALLOCATOR=y' \
		'CONFIG_NETFILTER=y' 'CONFIG_NETFILTER_XTABLES=y' 'CONFIG_NETFILTER_XTABLES_LEGACY=y' \
		'CONFIG_COMPAT=y' 'CONFIG_NETFILTER_XTABLES_COMPAT=y' \
		'CONFIG_NF_CONNTRACK=y' 'CONFIG_NETFILTER_XT_MATCH_STATE=y' 'CONFIG_NETFILTER_XT_MATCH_CONNTRACK=y' \
		'CONFIG_NETFILTER_XT_MATCH_OWNER=y' 'CONFIG_IP_NF_IPTABLES=y' 'CONFIG_IP_NF_IPTABLES_LEGACY=y' \
		'CONFIG_IP_NF_FILTER=y' 'CONFIG_IP_NF_TARGET_REJECT=y' 'CONFIG_IP6_NF_IPTABLES=y' \
		'CONFIG_IP6_NF_IPTABLES_LEGACY=y' 'CONFIG_IP6_NF_FILTER=y' 'CONFIG_IP6_NF_TARGET_REJECT=y' \
		'CONFIG_VIDEO_DEV=y' 'CONFIG_VIDEO_ATHENA_CAMERA=y' 'CONFIG_COMMON_CLK_MT8183_CAMSYS=y' \
		'CONFIG_CFG80211_WEXT=y' 'CONFIG_WEXT_PROC=y' \
		'CONFIG_DRM_PANFROST=y' 'CONFIG_VIDEO_MEDIATEK_JPEG=y' \
		'CONFIG_MTK_SCP=y' 'CONFIG_MTK_CMDQ=y' 'CONFIG_VIDEO_MEDIATEK_MDP3=y' \
		'CONFIG_CROS_EC_RPMSG=y' 'CONFIG_CROS_EC_DEBUGFS=y' 'CONFIG_VIDEO_MEDIATEK_DIP=y' \
		'CONFIG_BOOTPARAM_SOFTLOCKUP_PANIC=1' 'CONFIG_BOOTPARAM_HARDLOCKUP_PANIC=y' \
		'CONFIG_VIDEO_MEDIATEK_FD=y' 'CONFIG_TECHO5_VPU=y'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done
# Options the 6.18.56 configuration set that 7.2 dropped or renamed, for review.
grep '^CONFIG_' "$UP/athena_defconfig" | sort > "$WORK/config-6.18.56"
grep '^CONFIG_' .config | sort > "$WORK/config-$KVER"
comm -23 "$WORK/config-6.18.56" "$WORK/config-$KVER" > "$OUT/config-dropped.txt" || true

# 5. Build
make -s -j"$JOBS" Image.gz mediatek/mt8183-amazon-athena.dtb
cp arch/arm64/boot/Image.gz "$OUT/Image.gz"
cp "$DTSDIR/mt8183-amazon-athena.dtb" "$OUT/athena.dtb"
cp .config "$OUT/kernel.config"
cat include/config/kernel.release > "$OUT/kernel.release"

# 6. Ramdisk and boot image
python3 "$UP/mkfwcpio.py" "$STAGE" "$OUT/ramdisk.cpio.gz" $RAMDISK_FILES
CPUIDLE=${MCDI_CPUS:+1} python3 "$UP/pack.py" "$OUT/Image.gz" "$OUT/athena.dtb" "$OUT/ramdisk.cpio.gz" "$OUT/boot-athena.img"
python3 "$UP/check-boot.py" "$OUT/boot-athena.img"
cd "$OUT" && sha256sum boot-athena.img Image.gz athena.dtb ramdisk.cpio.gz kernel.config > SHA256SUMS && cat SHA256SUMS kernel.release
