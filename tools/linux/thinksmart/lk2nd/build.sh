#!/bin/bash
# SPDX-License-Identifier: MIT
#
# build.sh: build the lk2nd that install-thinksmart.py writes into a Lenovo ThinkSmart View's boot
# partition, from lk2nd 23.1, signed and without an appended QCDT. Runs on Linux or in WSL.
#
#   tools/linux/thinksmart/lk2nd/build.sh
#
# Why not lk2nd's own release image: this board's stock bootloader cannot be unlocked (it crashes
# while unlocked), and locked it starts only an AVB1 signed boot image (any key: lk2nd signs with the
# AOSP test key in its tree) without a QCDT appended. lk2nd's release images are unsigned, and from
# 21.0 on carry a QCDT; the stock bootloader crashes on them, to a black screen (the SoC waits in its
# crash dump mode, USB 05c6:900e). lk2nd's own notes for this board
# (lk2nd/device/dts/msm8953/apq8053-lenovo-cd-18781y.dts) give the make line used here.
#
# Build with a bare-metal ARM GCC (arm-none-eabi-). An image built by a Linux hard-float GCC
# (arm-linux-gnueabihf-) builds without complaint and hangs on this board at the stock bootloader's
# logo.
#
# Output in $OUT: lk2nd-thinksmart.img, lk2nd-23.1-source.tar.gz (the tree it was built from: some
# of what goes into the image, this board's device tree and the panel drivers, is GPL-2.0-only, so a
# release that publishes the image publishes this with it) and SHA256SUMS. The build is reproducible
# with the same compiler (Ubuntu 26.04's gcc-arm-none-eabi 14.2.1 here): two builds are
# byte-identical.
#
# Environment:
#   WORK                  work directory (default ~/techo5-thinksmart-lk2nd): the lk2nd tree, a venv
#   OUT                   output directory (default $WORK/out)
#   JOBS                  parallel make jobs (default: nproc)
#   TOOLCHAIN_PREFIX      default arm-none-eabi-
#
# Needs: git, make, python3 (with venv), an arm-none-eabi GCC (Debian/Ubuntu: gcc-arm-none-eabi).
# lk2nd's signing script needs pycryptodome, pyasn1 and pyasn1_modules, which this installs into a venv
# in $WORK.
set -euo pipefail

WORK=${WORK:-$HOME/techo5-thinksmart-lk2nd}
OUT=${OUT:-$WORK/out}
JOBS=${JOBS:-$(nproc)}
TOOLCHAIN_PREFIX=${TOOLCHAIN_PREFIX:-arm-none-eabi-}

LGIT=https://github.com/msm8916-mainline/lk2nd.git
LTAG=23.1
LCOMMIT=e9c8b21

command -v "${TOOLCHAIN_PREFIX}gcc" >/dev/null ||
	{ echo "build.sh: ${TOOLCHAIN_PREFIX}gcc not found (Debian/Ubuntu: apt install gcc-arm-none-eabi)" >&2; exit 1; }
case $TOOLCHAIN_PREFIX in
*linux-gnueabihf*) echo "build.sh: $TOOLCHAIN_PREFIX builds an lk2nd that hangs on this board; use arm-none-eabi-" >&2; exit 1;;
esac

mkdir -p "$WORK" "$OUT"
SRC=$WORK/lk2nd-$LTAG
if [ ! -d "$SRC/.git" ]; then
	rm -rf "$SRC"
	git -c advice.detachedHead=false clone -q --depth 1 --branch "$LTAG" "$LGIT" "$SRC"
fi
got=$(git -C "$SRC" rev-parse HEAD)
case $got in
"$LCOMMIT"*) ;;
*) echo "build.sh: $SRC is at $got, not lk2nd $LTAG ($LCOMMIT)" >&2; exit 1;;
esac

if [ ! -x "$WORK/venv/bin/python3" ]; then
	python3 -m venv "$WORK/venv"
	"$WORK/venv/bin/pip" -q install pycryptodome pyasn1 pyasn1_modules
fi

cd "$SRC"
rm -rf build-lk2nd-msm8953
# bootsignature.py runs as python3 from PATH: the venv's.
PATH=$WORK/venv/bin:$PATH make -j"$JOBS" TOOLCHAIN_PREFIX="$TOOLCHAIN_PREFIX" SIGN_BOOTIMG=1 LK2ND_QCDTBS= \
	lk2nd-msm8953 >"$WORK/build.log" 2>&1 ||
	{ tail -20 "$WORK/build.log" >&2; echo "build.sh: make failed; the whole log is $WORK/build.log" >&2; exit 1; }

cp build-lk2nd-msm8953/lk2nd.img "$OUT/lk2nd-thinksmart.img"
git archive --format=tar --prefix="lk2nd-$LTAG/" HEAD | gzip -n >"$OUT/lk2nd-$LTAG-source.tar.gz"
cd "$OUT"
sha256sum lk2nd-thinksmart.img "lk2nd-$LTAG-source.tar.gz" >SHA256SUMS
cat SHA256SUMS
