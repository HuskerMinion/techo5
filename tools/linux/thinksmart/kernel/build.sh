#!/bin/bash
# SPDX-License-Identifier: MIT
#
# build.sh: build the TECHO5 kernel for the Lenovo ThinkSmart View (CD-18781Y, APQ8053) from
# msm8953-mainline v7.1.3-r0 (the tree postmarketOS packages for this SoC) with the drivers and
# patches here, and the configuration in thinksmart_defconfig. Runs on Linux or in WSL. See README.md.
#
#   tools/linux/thinksmart/kernel/build.sh
#
# Output in $OUT: boot-thinksmart.img, the kernel as a root filesystem carries it for the update
# (deploy-rootfs.sh KERNEL_IMAGE; the boot layer starts each slot with its own: lk2nd-ab), vmlinuz (Image.gz) and
# apq8053-lenovo-cd-18781y.dtb for an image's boot partition (mkimage.sh -K), kernel.config, the
# source tarball, and SHA256SUMS. Every driver is built in: there are no modules. There is no
# initramfs: the kernel maps the store out of the system partition itself (dm-mod.create on the
# command line below) and runs its /sbin/init.
#
# Environment:
#   WORK       work directory (default ~/techo5-thinksmart-kbuild): the kernel tree, output
#   OUT        output directory (default $WORK/out)
#   KTREE      use this msm8953-mainline v7.1.3-r0 checkout in place instead of cloning afresh
#              (faster rebuilds). It must be pristine or prepared by this script before: every
#              step below is idempotent.
#   JOBS       parallel make jobs (default: nproc)
#   CROSS_COMPILE         default aarch64-linux-gnu- (built and tested with Ubuntu's GCC 15.2)
#   CROSS_COMPILE_COMPAT  default arm-linux-gnueabihf- (the 32-bit vDSO: the userland is armv7)
#
# Needs: git, tar, xz, make, gcc, bison, flex, bc, perl, python3, patch, aarch64 and armhf cross
# GCCs, libssl-dev, libelf-dev.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${WORK:-$HOME/techo5-thinksmart-kbuild}
OUT=${OUT:-$WORK/out}
JOBS=${JOBS:-$(nproc)}

KGIT=https://github.com/msm8953-mainline/linux.git
KTAG=v7.1.3-r0
KCOMMIT=7213855948c70fd165356526f1de1c3b6bf4d554
KVER=7.1.3
DTB=apq8053-lenovo-cd-18781y

export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
export CROSS_COMPILE_COMPAT=${CROSS_COMPILE_COMPAT:-arm-linux-gnueabihf-}
export KBUILD_BUILD_USER=techo5 KBUILD_BUILD_HOST=thinksmart-build
# Fixed, so a build is reproducible, but it is what `uname -v` says: bump it whenever the kernel
# changes (a patch, the config, the base tree), or a unit cannot tell the new kernel from the one
# before. 2026-11-20 12:00 was the first; 19:00 the first with every driver built in; 23:00 the
# first with the video decoder's interrupt fix (patches/0006); 2026-11-21 12:00 the first whose speaker
# driver plays untuned without its DSP table, and whose console stands the way the device does.
export KBUILD_BUILD_TIMESTAMP="2026-11-21 12:00:00 UTC" KBUILD_BUILD_VERSION=1
export LC_ALL=C TZ=UTC
# The release string is the configuration's CONFIG_LOCALVERSION alone, never a git suffix.
MAKE="make -s LOCALVERSION="

# The command line, in the boot image's header (pack.py), from which mkimage.sh and the update take
# system1's. The device mapper makes the two partitions of the MBR inside the GPT partition named
# system (mkimage.sh's offsets: keep the two the same) as system1, lk2nd's /boot, and system2, the
# store, which is mounted read-only as / with its /sbin/init. The device stands the other way up from
# the panel's own top, so the display plane turns the picture half a turn (echod turns the touch
# readings: hardware/touch/thinksmart.go). The panel is portrait and the device stands landscape:
# echod turns its picture a quarter turn clockwise onto it, and fbcon=rotate:1 turns the console's
# text the same way. The camera wants 128 MiB of contiguous memory. A panic
# restarts, which is how a kernel on trial that panics gives way to system1's; and the wait for the
# root device ends, in a panic, after 30 s: a plain rootwait waits forever, and a kernel on trial
# that cannot find its root would hang there instead of giving way.
MAP="system1,,,rw,0 497664 linear PARTLABEL=system 2048;system2,,,rw,0 4194304 linear PARTLABEL=system 499712"
CMDLINE="consoleblank=0 vt.global_cursor_default=0 video=DSI-1:800x1280,rotate=180 fbcon=rotate:1 cma=128M panic=10 dm-mod.waitfor=PARTLABEL=system dm-mod.create=\"$MAP\" root=/dev/dm-1 rootfstype=ext4 ro rootwait=30 init=/sbin/init"

mkdir -p "$WORK" "$OUT"
WORK=$(cd "$WORK" && pwd) OUT=$(cd "$OUT" && pwd)

# 1. Kernel source. A shallow clone of the tag (GitHub's tag tarballs of this tree are large and
# their downloads do not resume), checked against the commit.
if [ -n "${KTREE:-}" ]; then
	KTREE=$(cd "$KTREE" && pwd)
else
	KTREE=$WORK/linux-$KVER
	rm -rf "$KTREE"
	git clone -q --depth 1 --branch "$KTAG" "$KGIT" "$KTREE"
fi
[ "$(git -C "$KTREE" rev-parse HEAD)" = "$KCOMMIT" ] || { echo "$KTREE is not $KTAG ($KCOMMIT)" >&2; exit 1; }

# 2. The two drivers the tree does not have, with their Kconfig and Makefile entries.
C=$KTREE/sound/soc/codecs
cp "$HERE"/src/tas5782m.c "$HERE"/src/tas5782m.h "$HERE"/src/tas5782m_priv.h \
	"$HERE"/src/tas5782m_tables.c "$C/"
grep -q SND_SOC_TAS5782M "$C/Makefile" ||
	printf 'snd-soc-tas5782m-y := tas5782m.o tas5782m_tables.o\nobj-$(CONFIG_SND_SOC_TAS5782M) += snd-soc-tas5782m.o\n' >> "$C/Makefile"
M=$KTREE/drivers/media/i2c
cp "$HERE/src/s5kc505a.c" "$M/"
grep -q VIDEO_S5KC505A "$M/Makefile" || echo 'obj-$(CONFIG_VIDEO_S5KC505A) += s5kc505a.o' >> "$M/Makefile"
python3 - "$C/Kconfig" "$M/Kconfig" <<'PY'
import sys
def insert(path, sym, entry, before):
    s = open(path).read()
    if "config " + sym + "\n" in s:
        return
    i = s.index("config " + before + "\n")
    open(path, "w").write(s[:i] + entry + s[i:])
insert(sys.argv[1], "SND_SOC_TAS5782M",
       'config SND_SOC_TAS5782M\n\ttristate "Texas Instruments TAS5782M speaker amplifier"\n'
       '\tdepends on I2C\n\tselect REGMAP_I2C\n\n', "SND_SOC_TAS571X")
insert(sys.argv[2], "VIDEO_S5KC505A",
       'config VIDEO_S5KC505A\n\ttristate "Samsung S5KC505A sensor support"\n\tselect V4L2_CCI_I2C\n'
       '\thelp\n\t  The Lenovo ThinkSmart View\'s front camera.\n\n', "VIDEO_S5K5BAF")
PY

# 3. The patches in order: the audio stream fixes, the video decoder's parser, the camera in the
# board's device tree. Each one applied is noted in the tree's .git (so not in the source tarball),
# and not applied again. A tree that has one already without the note (an earlier build.sh) is
# found by a dry run backwards; that only works for the last of patches touching the same lines, so
# the note is what makes a second run of a prepared tree work.
STAMP=$KTREE/.git/techo5-patches
touch "$STAMP"
for p in "$HERE"/patches/*.patch; do
	sum=$(sha256sum "$p" | cut -d' ' -f1)
	if grep -qx "$sum" "$STAMP"; then
		echo "already applied: $(basename "$p")"
		continue
	fi
	if patch -d "$KTREE" -p1 -R --dry-run -s -f --fuzz=0 < "$p" >/dev/null 2>&1; then
		echo "already applied: $(basename "$p")"
	else
		patch -d "$KTREE" -p1 -N -s --fuzz=0 --no-backup-if-mismatch -r - < "$p"
		echo "applied $(basename "$p")"
	fi
	echo "$sum" >> "$STAMP"
done

# 4. Configuration
cd "$KTREE"
export KCFLAGS="-fdebug-prefix-map=$KTREE=." KAFLAGS="-fdebug-prefix-map=$KTREE=."
cp "$HERE/thinksmart_defconfig" .config
$MAKE olddefconfig
# Everything built in, with no modules: the kernel is one file, which the update moves as it is. The
# boot chain's part of it: the eMMC, ext4 and the device mapper's boot-time tables (the store is a
# partition inside the system partition, which the kernel maps before mounting it as /), devtmpfs on
# /dev before init, and init being a script. Then the board's own drivers.
grep -q '=m$' .config && { echo "config check failed: modules in the configuration" >&2; exit 1; }
for want in 'CONFIG_LOCALVERSION="-techo5-thinksmart"' '# CONFIG_LOCALVERSION_AUTO is not set' \
		'# CONFIG_MODULES is not set' \
		'CONFIG_MMC_SDHCI_MSM=y' 'CONFIG_MMC_BLOCK=y' 'CONFIG_EFI_PARTITION=y' 'CONFIG_EXT4_FS=y' \
		'CONFIG_BLK_DEV_DM=y' 'CONFIG_DM_INIT=y' 'CONFIG_DEVTMPFS_MOUNT=y' 'CONFIG_BINFMT_SCRIPT=y' \
		'CONFIG_COMPAT=y' 'CONFIG_SND_SOC_TAS5782M=y' 'CONFIG_VIDEO_S5KC505A=y' \
		'CONFIG_VIDEO_QCOM_CAMSS=y' 'CONFIG_VCNL4000=y' 'CONFIG_ATH10K_SDIO=y' 'CONFIG_BT_HCIUART_QCA=y'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done
# techo5-firewall and the video decoder's fence use the legacy iptables tools (32-bit, so through
# xtables' compat layer); AirPlay 2's rules need the recent match.
for want in 'CONFIG_NETFILTER_XTABLES_COMPAT=y' 'CONFIG_IP_NF_IPTABLES_LEGACY=y' 'CONFIG_IP6_NF_IPTABLES_LEGACY=y' \
		'CONFIG_IP_NF_FILTER=y' 'CONFIG_IP6_NF_FILTER=y' 'CONFIG_IP_NF_TARGET_REJECT=y' 'CONFIG_IP6_NF_TARGET_REJECT=y' \
		'CONFIG_NETFILTER_XT_MATCH_CONNTRACK=y' 'CONFIG_NETFILTER_XT_MATCH_OWNER=y' 'CONFIG_NETFILTER_XT_MATCH_RECENT=y' \
		'CONFIG_TUN=y'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done

# 5. Build
$MAKE -j"$JOBS" Image.gz "qcom/$DTB.dtb"
release=$(cat include/config/kernel.release)
[ "$release" = "$KVER-techo5-thinksmart" ] || { echo "release is $release" >&2; exit 1; }
cp arch/arm64/boot/Image.gz "$OUT/vmlinuz"
cp "arch/arm64/boot/dts/qcom/$DTB.dtb" "$OUT/$DTB.dtb"
cp .config "$OUT/kernel.config"

# 6. The boot image: what a root filesystem carries, what lk2nd-ab writes to recovery for a slot's
# trial boot, and what system1's files and command line are taken from once the slot is good.
python3 "$HERE/pack.py" pack "$OUT/vmlinuz" "$OUT/$DTB.dtb" "$CMDLINE" "$OUT/boot-thinksmart.img"
python3 "$HERE/pack.py" check "$OUT/boot-thinksmart.img"
[ "$(python3 "$HERE/pack.py" cmdline "$OUT/boot-thinksmart.img")" = "$CMDLINE" ] ||
	{ echo "the boot image's command line is not the one given" >&2; exit 1; }
# The recovery partition is 32 MiB.
[ "$(stat -c %s "$OUT/boot-thinksmart.img")" -le $((32 << 20)) ] || { echo "boot-thinksmart.img is over 32 MiB" >&2; exit 1; }

# 7. The source, for the GPL: the whole prepared tree this kernel was built from, with the build's
# configuration in it as techo5-thinksmart.config, and nothing the build made. Reproducible like the
# kernel: entries sorted, owned by 0:0, all at the build's timestamp, one xz thread.
SRCNAME=linux-$KVER-techo5-thinksmart
SRCTAR=$OUT/$SRCNAME-source.tar.xz
PKG=$WORK/source-pkg
rm -rf "$PKG" && mkdir -p "$PKG"
git -C "$KTREE" ls-files -z --cached --others --exclude-standard |
	(cd "$KTREE" && tar --null -T - -cf -) | tar -C "$PKG" --one-top-level="$SRCNAME" -xf -
cp "$OUT/kernel.config" "$PKG/$SRCNAME/techo5-thinksmart.config"
tar -C "$PKG" --sort=name --mtime="$KBUILD_BUILD_TIMESTAMP" --owner=0 --group=0 --numeric-owner \
	--mode='go-w,a+rX' --format=gnu -cf - "$SRCNAME" | xz -T1 -6 > "$SRCTAR.part"
mv "$SRCTAR.part" "$SRCTAR"
rm -rf "$PKG"

rm -f "$OUT/modules.tar"
cd "$OUT" && sha256sum boot-thinksmart.img vmlinuz "$DTB.dtb" kernel.config "$SRCNAME-source.tar.xz" > SHA256SUMS && cat SHA256SUMS
