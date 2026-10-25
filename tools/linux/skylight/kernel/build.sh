#!/bin/bash
# SPDX-License-Identifier: MIT
#
# build.sh: build TECHO5's mainline kernel for the Skylight Calendar 15 (150-CAL; Rockchip RK3566,
# Skyworth board D156) from the kernel.org linux-6.18.56 tarball and the board files here, and pack
# it as the Android boot image (header v2 with a Rockchip resource image) that the stock U-Boot starts
# from the recovery partition. Runs on Linux or in WSL. See README.md.
#
#   tools/linux/skylight/kernel/build.sh
#   INSTALLER=1 tools/linux/skylight/kernel/build.sh      (a release build: the installer image too)
#
# Environment:
#   WORK       work directory (default ~/techo5-skylight-kbuild): downloads, the kernel tree, output
#   OUT        output directory (default $WORK/out)
#   KTREE      use this unpacked linux-6.18.56 tree in place instead of unpacking the tarball afresh
#              (faster rebuilds). It must be pristine or set up by this script before: every step
#              below is idempotent.
#   JOBS       parallel make jobs (default: nproc)
#   BENCH      also pack boot-skylight-bench.img: the same kernel with a bench initramfs (a root
#              shell on USB serial; bench/init)
#   INSTALLER  also pack boot-skylight-installer.img: the same kernel with the installer initramfs
#              (installer/init, ../install/install.sh and an empty techo5-data image), which
#              tools/install-skylight.py writes into recovery for the one boot that installs; needs
#              mke2fs (e2fsprogs)
#   BUSYBOX    the busybox in the bench and installer initramfs (default: inputs/busybox.static, the
#              armv7 one tools/fetch-inputs.py takes from Alpine's busybox-static package, which the
#              store's rescue environment runs too; this kernel runs 32-bit programs, as it runs the
#              Show's whole userland)
#   CROSS_COMPILE  default aarch64-linux-gnu- (built and tested with Ubuntu 24.04's GCC 13.3)
#
# Needs: curl, sha256sum, tar, xz, make, gcc, bison, flex, bc, perl, python3 with Pillow, cpio 2.13 or
# later, gzip, an aarch64 cross GCC, libssl-dev, libelf-dev.
#
# Out: boot-skylight.img (the release's boot image, and what a rootfs carries with KERNEL_IMAGE),
# boot-skylight-installer.img with INSTALLER, linux-6.18.56-techo5-skylight-source.tar.xz (the source
# of both, for the GPL), the kernel's parts and its configuration, and SHA256SUMS. check-boot.py and
# check-source.py check them here, and release.ps1 checks them again before it publishes.
#
# Nothing of the unit's own goes in: its Wi-Fi and Bluetooth firmware are copied from its stock
# vendor partition into the store at install (tools/linux/skylight/README.md), never shipped.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../../../.." && pwd)
WORK=${WORK:-$HOME/techo5-skylight-kbuild}
OUT=${OUT:-$WORK/out}
JOBS=${JOBS:-$(nproc)}
BUSYBOX=${BUSYBOX:-$REPO/inputs/busybox.static}

KVER=6.18.56
KURL=https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$KVER.tar.xz
KSHA256=4633bbda2ab95eaf2a7af4041236451d6759c72e6946265531af4b3aacefbf89

export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
export KBUILD_BUILD_USER=techo5 KBUILD_BUILD_HOST=skylight-build
# Fixed, so a build is reproducible, but it is what `uname -v` says: bump it whenever the kernel
# changes (the board files, the config, the kernel.org version), or a unit cannot tell the new kernel
# from the one before. The initramfs files and the source tarball carry this time too.
export KBUILD_BUILD_TIMESTAMP="2026-10-17 00:00:00 UTC" KBUILD_BUILD_VERSION=1
export LC_ALL=C TZ=UTC
EPOCH=$(date -u -d "${KBUILD_BUILD_TIMESTAMP% UTC}" +%s)

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

# cpio_gz DIR OUT: DIR as a gzip'd newc cpio that depends on nothing but the files' contents and
# modes: names sorted, owned by 0:0, every entry at the build's time, no inode numbers.
cpio_gz() {
	find "$1" -exec touch -h -d "@$EPOCH" {} +
	(cd "$1" && find . | sort | cpio -o -H newc --quiet --owner=0:0 --reproducible | gzip -9 -n) > "$2"
}

# The busybox in an initramfs, and a note beside it saying what it is and where its source is (the
# GPL): Alpine's own build, unmodified, the way the store and the Show images carry it.
busybox_in() { # busybox_in ROOT
	[ -f "$BUSYBOX" ] || { echo "no busybox at $BUSYBOX: run tools/fetch-inputs.py, or set BUSYBOX" >&2; exit 1; }
	[ "$(head -c 4 "$BUSYBOX" | od -An -tx1 | tr -d ' ')" = 7f454c46 ] || { echo "$BUSYBOX is not a program" >&2; exit 1; }
	local v bv
	v=$(grep -a -o -m1 'BusyBox v[0-9][0-9.]*[^ ]* ([^)]*)' "$BUSYBOX" | head -1)
	[ -n "$v" ] || { echo "$BUSYBOX is not a busybox" >&2; exit 1; }
	bv=$(echo "$v" | sed 's/^BusyBox v\([0-9.]*\).*/\1/')
	install -D -m 755 "$BUSYBOX" "$1/bin/busybox"
	cat > "$1/SOURCES" <<-EOF
	bin/busybox: $v, sha256 $(sha256sum "$BUSYBOX" | cut -d' ' -f1)
	  BusyBox, GPL-2.0: Alpine Linux's busybox-static package (armv7), unmodified.
	  Source: https://busybox.net/downloads/busybox-$bv.tar.bz2 with Alpine's build files and
	  patches, https://gitlab.alpinelinux.org/alpine/aports/-/tree/3.24-stable/main/busybox
	bin/reboot-normal: TECHO5's tools/linux/skylight/kernel/reboot-normal.c, MIT, built
	  without a C library.
	The kernel: linux-$KVER-techo5-skylight-source.tar.xz, published with the release.
	EOF
}

# reboot-normal: built without a C library, so nothing of one goes into an image.
reboot_normal() { # reboot_normal OUT
	${CROSS_COMPILE}gcc -static -nostdlib -ffreestanding -fno-stack-protector -fno-pie -no-pie -O2 -s \
		-Wl,--build-id=none -o "$1" "$HERE/reboot-normal.c"
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

# 2. Board files: the device tree, the panel driver, the boot logo
cp "$HERE/rk3566-skylight-cal150.dts" "$KTREE/arch/arm64/boot/dts/rockchip/"
grep -q skylight "$KTREE/arch/arm64/boot/dts/rockchip/Makefile" ||
	echo 'dtb-$(CONFIG_ARCH_ROCKCHIP) += rk3566-skylight-cal150.dtb' >> "$KTREE/arch/arm64/boot/dts/rockchip/Makefile"
P=$KTREE/drivers/gpu/drm/panel
cp "$HERE/panel-skylight-cal150.c" "$P/"
grep -q SKYLIGHT_CAL150 "$P/Makefile" ||
	echo 'obj-$(CONFIG_DRM_PANEL_SKYLIGHT_CAL150) += panel-skylight-cal150.o' >> "$P/Makefile"
grep -q SKYLIGHT_CAL150 "$P/Kconfig" || python3 - "$P/Kconfig" <<'EOF'
import sys
p = sys.argv[1]
s = open(p).read()
entry = ('config DRM_PANEL_SKYLIGHT_CAL150\n'
         '\ttristate "Skylight Calendar 15 (150-CAL) DSI-to-LVDS panel"\n'
         '\tdepends on OF && DRM_MIPI_DSI && BACKLIGHT_CLASS_DEVICE\n\n')
i = s.rindex("endmenu")
open(p, "w").write(s[:i] + entry + s[i:])
EOF
python3 "$HERE/mklogo.py" "$REPO/logo/TECHO5_logo.png" "$KTREE/drivers/video/logo/logo_linux_clut224.ppm" "$OUT/logo.bmp"

# 3. Configuration: arm64 defconfig with the fragment over it, and every line of the fragment checked
cd "$KTREE"
# The debug info records the build directory, and the linker's build-id (kept in the Image) hashes
# it: map the tree's path away so the output does not depend on where it was built.
export KCFLAGS="-fdebug-prefix-map=$KTREE=." KAFLAGS="-fdebug-prefix-map=$KTREE=."
make -s defconfig
scripts/kconfig/merge_config.sh -m .config "$HERE/skylight.config" >/dev/null
make -s olddefconfig
bad=0
while read -r l; do
	case "$l" in
	"# CONFIG_"*" is not set") s=${l#\# }; s=${s% is not set}
		grep -q "^$s=" .config && { echo "not off: $s" >&2; bad=1; } ;;
	CONFIG_*) grep -qx "$l" .config || { echo "did not stick: $l" >&2; bad=1; } ;;
	esac
done < "$HERE/skylight.config"
[ $bad = 0 ] || exit 1

# 4. Build. Only the Image and the device tree: CONFIG_MODULES stays on as arm64's defconfig has it,
# but no module is built or shipped, so what the board and TECHO5 need is built in (skylight.config).
make -s -j"$JOBS" Image
make -s DTC_FLAGS=-@ rockchip/rk3566-skylight-cal150.dtb
cp arch/arm64/boot/Image "$OUT/Image"
cp arch/arm64/boot/dts/rockchip/rk3566-skylight-cal150.dtb "$OUT/rk3566-skylight-cal150.dtb"
cp .config "$OUT/kernel.config"

# 5. The boot image: an empty ramdisk (the root is the store), the store as root
R=$WORK/ramdisk
rm -rf "$R" && mkdir -p "$R"
cpio_gz "$R" "$OUT/ramdisk.cpio.gz"
CMDLINE="console=ttyS2,1500000n8 earlycon=uart8250,mmio32,0xfe660000 panic=10 loglevel=7"
python3 "$HERE/pack-sky.py" --kernel "$OUT/Image" --dtb "$OUT/rk3566-skylight-cal150.dtb" \
	--ramdisk "$OUT/ramdisk.cpio.gz" --logo "$OUT/logo.bmp" \
	--cmdline "$CMDLINE ro rootwait root=PARTLABEL=techo5-store init=/init vt.global_cursor_default=0 fbcon=logo-count:1 fbcon=logo-pos:center" \
	--out "$OUT/boot-skylight.img"
python3 "$HERE/check-boot.py" "$OUT/boot-skylight.img"
IMAGES=boot-skylight.img

# 6. The bench image, on request: the same kernel with a root shell on USB serial in its initramfs
if [ -n "${BENCH:-}" ]; then
	B=$WORK/bench
	rm -rf "$B" && mkdir -p "$B/bin" "$B/dev" "$B/proc" "$B/sys" "$B/tmp"
	busybox_in "$B"
	sed 's/\r$//' "$HERE/bench/init" > "$B/init" && chmod 755 "$B/init"
	reboot_normal "$B/bin/reboot-normal"
	cpio_gz "$B" "$OUT/bench.cpio.gz"
	python3 "$HERE/pack-sky.py" --kernel "$OUT/Image" --dtb "$OUT/rk3566-skylight-cal150.dtb" \
		--ramdisk "$OUT/bench.cpio.gz" --logo "$OUT/logo.bmp" --cmdline "$CMDLINE rdinit=/init" \
		--out "$OUT/boot-skylight-bench.img"
	IMAGES="$IMAGES boot-skylight-bench.img"
fi
# 7. The installer image, on request: the same kernel with install.sh in its initramfs, run by
#    installer/init on the set install-skylight.py stages on /cache
if [ -n "${INSTALLER:-}" ]; then
	command -v mke2fs >/dev/null || { echo "INSTALLER needs mke2fs (e2fsprogs)" >&2; exit 1; }
	I=$WORK/installer
	rm -rf "$I" && mkdir -p "$I/bin" "$I/dev" "$I/proc" "$I/sys" "$I/tmp" "$I/cache" "$I/installer"
	busybox_in "$I"
	sed 's/\r$//' "$HERE/installer/init" > "$I/init" && chmod 755 "$I/init"
	sed 's/\r$//' "$HERE/../install/install.sh" > "$I/installer/install.sh" && chmod 755 "$I/installer/install.sh"
	reboot_normal "$I/bin/reboot-normal"
	# techo5-data as the first boot finds it: an empty ext4 of 64 MiB, which the store's /init grows
	# online to fill its partition. Fixed UUID, hash seed and time, so the image is reproducible.
	rm -f "$WORK/data.img"
	E2FSPROGS_FAKE_TIME=$EPOCH mke2fs -q -t ext4 -b 4096 -m 0 \
		-L techo5-data -U 5ec0da7a-7ec5-4d5a-9a7a-000000000001 \
		-E root_owner=0:0,hash_seed=5ec0da7a-7ec5-4d5a-9a7a-000000000002 "$WORK/data.img" 64M
	echo "$(stat -c %s "$WORK/data.img") $(sha256sum "$WORK/data.img" | cut -d' ' -f1)" > "$I/installer/data.img.info"
	gzip -9 -n -c "$WORK/data.img" > "$I/installer/data.img.gz"
	cpio_gz "$I" "$OUT/installer.cpio.gz"
	python3 "$HERE/pack-sky.py" --kernel "$OUT/Image" --dtb "$OUT/rk3566-skylight-cal150.dtb" \
		--ramdisk "$OUT/installer.cpio.gz" --logo "$OUT/logo.bmp" \
		--cmdline "$CMDLINE rdinit=/init techo5.installer=1" --out "$OUT/boot-skylight-installer.img"
	python3 "$HERE/check-boot.py" --installer --kernel-of "$OUT/boot-skylight.img" "$OUT/boot-skylight-installer.img"
	IMAGES="$IMAGES boot-skylight-installer.img"
fi

# 8. The source, for the GPL: the whole tree these images were built from (kernel.org's with the board
# files, the panel driver's Kconfig and Makefile lines and the boot logo), with the build's
# configuration in it as techo5-skylight.config, and nothing the build made. Reproducible like the
# images: entries sorted by name, owned by 0:0, all at the build's timestamp, one xz thread.
SRCNAME=linux-$KVER-techo5-skylight
SRCTAR=$OUT/$SRCNAME-source.tar.xz
PKG=$WORK/source-pkg
rm -rf "$PKG" && mkdir -p "$PKG"
cp -a "$KTREE" "$PKG/$SRCNAME"
make -s -C "$PKG/$SRCNAME" distclean
# distclean leaves the kernel images behind
rm -f "$PKG/$SRCNAME"/arch/arm64/boot/Image "$PKG/$SRCNAME"/arch/arm64/boot/Image.gz
cp "$OUT/kernel.config" "$PKG/$SRCNAME/techo5-skylight.config"
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
for img in $IMAGES; do
	python3 "$HERE/check-source.py" --config "$OUT/kernel.config" "$SRCTAR" "$OUT/$img"
done
rm -rf "$PKG"

cd "$OUT"
rm -f ./*.sha256
sha256sum $IMAGES Image rk3566-skylight-cal150.dtb logo.bmp kernel.config "$SRCNAME-source.tar.xz" > SHA256SUMS
cat SHA256SUMS
