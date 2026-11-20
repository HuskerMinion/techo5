#!/bin/bash
# SPDX-License-Identifier: MIT
#
# build.sh: build the TECHO5 kernel for the Lenovo ThinkSmart View (CD-18781Y, APQ8053) from
# msm8953-mainline v7.1.3-r0 (the tree postmarketOS packages for this SoC) with the drivers and
# patches here, and the configuration in thinksmart_defconfig. Runs on Linux or in WSL. See README.md.
#
#   tools/linux/thinksmart/kernel/build.sh
#
# Output in $OUT: vmlinuz (Image.gz) and apq8053-lenovo-cd-18781y.dtb for the store's boot partition
# (mkimage.sh -K), modules.tar for the store (mkstore.sh -M), kernel.config, the source tarball, and
# SHA256SUMS. There is no initramfs: the kernel maps the store out of the system partition itself
# (dm-mod.create on the command line mkimage.sh writes) and runs its /sbin/init.
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
# before. 2026-11-20 12:00 was the first.
export KBUILD_BUILD_TIMESTAMP="2026-11-20 12:00:00 UTC" KBUILD_BUILD_VERSION=1
export LC_ALL=C TZ=UTC
# The release string is the configuration's CONFIG_LOCALVERSION alone, never a git suffix.
MAKE="make -s LOCALVERSION="

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
# board's device tree. One already in the tree is left (patch -N, checked by a dry run backwards).
for p in "$HERE"/patches/*.patch; do
	if patch -d "$KTREE" -p1 -R --dry-run -s -f --fuzz=0 < "$p" >/dev/null 2>&1; then
		echo "already applied: $(basename "$p")"
	else
		patch -d "$KTREE" -p1 -N -s --fuzz=0 --no-backup-if-mismatch < "$p"
		echo "applied $(basename "$p")"
	fi
done

# 4. Configuration
cd "$KTREE"
export KCFLAGS="-fdebug-prefix-map=$KTREE=." KAFLAGS="-fdebug-prefix-map=$KTREE=."
cp "$HERE/thinksmart_defconfig" .config
$MAKE olddefconfig
# What the boot chain needs built in: the eMMC, ext4 and the device mapper's boot-time tables (the
# store is a partition inside the system partition, which the kernel maps before mounting it as /),
# devtmpfs on /dev before init, and init being a script.
for want in 'CONFIG_LOCALVERSION="-techo5-thinksmart"' '# CONFIG_LOCALVERSION_AUTO is not set' \
		'CONFIG_MMC_SDHCI_MSM=y' 'CONFIG_MMC_BLOCK=y' 'CONFIG_EFI_PARTITION=y' 'CONFIG_EXT4_FS=y' \
		'CONFIG_BLK_DEV_DM=y' 'CONFIG_DM_INIT=y' 'CONFIG_DEVTMPFS_MOUNT=y' 'CONFIG_BINFMT_SCRIPT=y' \
		'CONFIG_COMPAT=y' 'CONFIG_MODULES=y' 'CONFIG_SND_SOC_TAS5782M=m' 'CONFIG_VIDEO_S5KC505A=m' \
		'CONFIG_VIDEO_QCOM_CAMSS=m' 'CONFIG_VCNL4000=m' 'CONFIG_ATH10K_SDIO=m' 'CONFIG_BT_HCIUART_QCA=y'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done
# techo5-firewall and the video decoder's fence use the legacy iptables tools (32-bit, so through
# xtables' compat layer); AirPlay 2's rules need the recent match.
for want in 'CONFIG_NETFILTER_XTABLES_COMPAT=y' 'CONFIG_IP_NF_IPTABLES_LEGACY=m' 'CONFIG_IP6_NF_IPTABLES_LEGACY=m' \
		'CONFIG_IP_NF_FILTER=m' 'CONFIG_IP6_NF_FILTER=m' 'CONFIG_IP_NF_TARGET_REJECT=m' 'CONFIG_IP6_NF_TARGET_REJECT=m' \
		'CONFIG_NETFILTER_XT_MATCH_CONNTRACK=m' 'CONFIG_NETFILTER_XT_MATCH_OWNER=m' 'CONFIG_NETFILTER_XT_MATCH_RECENT=m' \
		'CONFIG_TUN=m'; do
	grep -qxF "$want" .config || { echo "config check failed: $want" >&2; exit 1; }
done

# 5. Build
$MAKE -j"$JOBS" Image.gz modules "qcom/$DTB.dtb"
release=$(cat include/config/kernel.release)
[ "$release" = "$KVER-techo5-thinksmart" ] || { echo "release is $release" >&2; exit 1; }
cp arch/arm64/boot/Image.gz "$OUT/vmlinuz"
cp "arch/arm64/boot/dts/qcom/$DTB.dtb" "$OUT/$DTB.dtb"
cp .config "$OUT/kernel.config"

# 6. Modules, as mkstore.sh -M takes them: a tar of modules/<release>, stripped, owned by root,
# reproducible (sorted, the build's timestamp).
STAGE=$WORK/modules-stage
rm -rf "$STAGE"
$MAKE INSTALL_MOD_PATH="$STAGE" INSTALL_MOD_STRIP=1 modules_install
rm -f "$STAGE/lib/modules/$release/build" "$STAGE/lib/modules/$release/source"
for m in snd-soc-tas5782m s5kc505a q6asm q6asm-dai venus-core xt_owner xt_recent ath10k_sdio; do
	[ -n "$(find "$STAGE/lib/modules/$release" -name "$m.ko")" ] || { echo "no $m.ko" >&2; exit 1; }
done
tar -C "$STAGE/lib" --sort=name --mtime="$KBUILD_BUILD_TIMESTAMP" --owner=0 --group=0 --numeric-owner \
	--format=gnu -cf "$OUT/modules.tar" modules
echo "modules: $(find "$STAGE" -name '*.ko' | wc -l) for $release"

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

cd "$OUT" && sha256sum vmlinuz "$DTB.dtb" modules.tar kernel.config "$SRCNAME-source.tar.xz" > SHA256SUMS && cat SHA256SUMS
