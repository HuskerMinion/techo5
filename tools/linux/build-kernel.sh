#!/bin/bash
# build-kernel.sh — build the Echo Show 5 kernel (LineageOS 18.1 tree, 4.9.337
# arm64) with Bluetooth added, at the exact commit the LineageOS boot image
# was built from so its vendor modules (mt76x8_wlan.ko, mt76x8_bt.ko; built
# with CONFIG_MODVERSIONS) still load. Run inside WSL/Linux.
#
#   tools/linux/build-kernel.sh [-o Image.gz-dtb]
#
# Environment: KSRC (~/kernel: github.com/amazon-oss/android_kernel_amazon_mt8163,
# branch cronos/lineage-18.1), KCOMMIT (8d928c5176cc — `uname -r` on the device
# is 4.9.337-g<KCOMMIT>; the tree must be checked out there and clean, or
# LOCALVERSION_AUTO changes the release string and the modules refuse to load),
# CROSS_COMPILE (an aarch64 GCC; Arm's 8.3-2019.03 release builds it cleanly:
# developer.arm.com/-/media/Files/downloads/gnu-a/8.3-2019.03/binrel/
# gcc-arm-8.3-2019.03-x86_64-aarch64-linux-gnu.tar.xz, no root needed),
# KOUT (build directory; keep it outside the source tree), DEFCONFIG (cronos_defconfig, the 2nd gen;
# checkers_defconfig for the 1st gen, which runs the same commit with its own device trees; give it
# a KOUT of its own).
#
# Afterwards: delete amzn,mic-downmix from the appended device trees the way
# patch-dtb.py does, then build-image.sh KERNEL=<Image.gz-dtb> (see README.md).
set -euo pipefail

KSRC=${KSRC:-$HOME/kernel}
KCOMMIT=${KCOMMIT:-8d928c5176cc}
KOUT=${KOUT:-$HOME/kout}
DEFCONFIG=${DEFCONFIG:-cronos_defconfig}
CROSS_COMPILE=${CROSS_COMPILE:-$HOME/toolchain/gcc-arm-8.3-2019.03-x86_64-aarch64-linux-gnu/bin/aarch64-linux-gnu-}
OUT=
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done

cd "$KSRC"
# KPATCHED=1 allows a tree with local changes (the camera sensor patches on a branch): the
# release string is then pinned by hand to what the vendor modules were built against.
head=$(git rev-parse --short=12 HEAD)
if [ -z "${KPATCHED:-}" ]; then
	case "$head" in "$KCOMMIT"*) ;; *) echo "kernel tree is at $head, not $KCOMMIT (set KPATCHED=1 for a patched branch)" >&2; exit 1;; esac
	if [ -n "$(git status --porcelain)" ]; then
		echo "kernel tree is not clean (LOCALVERSION would get -dirty):" >&2
		git status --short >&2
		exit 1
	fi
fi
# The kernel records who built it and on what machine (uname -v, the boot log); keep the builder's
# account and host name out of an image that may be published. Module loading is unaffected: the
# vendor modules check the release string and symbol versions, not this.
export KBUILD_BUILD_USER=${KBUILD_BUILD_USER:-techo5} KBUILD_BUILD_HOST=${KBUILD_BUILD_HOST:-techo5}
export TZ=UTC # the build date in the version string, without the builder's zone
export ARCH=arm64 CROSS_COMPILE
mkdir -p "$KOUT"
make -s O="$KOUT" "$DEFCONFIG"
# Bluetooth core + BR/EDR + LE, RFCOMM (serial profiles), the virtual HCI
# driver btbridge feeds, and HCI UART/H4 as the alternative transport.
scripts/config --file "$KOUT/.config" \
	-e BT -e BT_BREDR -e BT_LE -e BT_RFCOMM -e BT_RFCOMM_TTY \
	-e BT_HCIVHCI -e BT_HCIUART -e BT_HCIUART_H4 -d BT_DEBUGFS
if [ -n "${KPATCHED:-}" ]; then
	scripts/config --file "$KOUT/.config" -d LOCALVERSION_AUTO --set-str LOCALVERSION "-g$KCOMMIT"
fi
make -s O="$KOUT" olddefconfig
grep -E "^CONFIG_(BT|BT_HCIVHCI|LOCALVERSION_AUTO)=" "$KOUT/.config"
make -j"$(nproc)" O="$KOUT" Image.gz-dtb
echo "release: $(cat "$KOUT/include/config/kernel.release")"
ls -la "$KOUT/arch/arm64/boot/Image.gz-dtb"
if [ -n "$OUT" ]; then
	cp "$KOUT/arch/arm64/boot/Image.gz-dtb" "$OUT"
	echo "copied to $OUT"
fi
