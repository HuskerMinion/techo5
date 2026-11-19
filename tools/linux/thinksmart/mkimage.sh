#!/bin/bash
# mkimage.sh — the image written to a Lenovo ThinkSmart View's "system" partition: an MBR with lk2nd's
# boot partition and TECHO5's store.
#
#   mkimage.sh -b <boot.tar> -s <store.img> -u <store uuid> -o <system.img> [-a <extra kernel arguments>]
#              [-w <workdir>]
#
#   <boot.tar>    the kernel's own files, from a unit running that kernel (/boot): vmlinuz, initramfs
#                 (postmarketOS's, which finds and mounts the store) and apq8053-lenovo-cd-18781y.dtb
#   <store.img>   tools/linux/thinksmart/mkstore.sh's, made with -U <store uuid> and -S 2048
#
# The unit's system partition is the size of this image exactly, 4694016 sectors, with TECHO5's data
# in a GPT partition of its own after it (tools/linux/thinksmart/repartition.sh). Inside the image:
#
#   p1   2048 + 497664 sectors     ext2 with the few features lk2nd reads; extlinux/extlinux.conf
#   p2   499712 + 4194304          the store (2 GiB)
#
# postmarketOS's initramfs takes an MBR with exactly two partitions, and grows the second over any
# free space it finds after it: so there are two, and they fill system.
#
# Linux with e2fsprogs and sfdisk. No root and no loop devices: the file systems are made from
# directories (mke2fs -d) and copied in at their offsets.
set -euo pipefail
umask 022

BOOT=; STORE=; UUID=; OUT=; EXTRA=; WORK=$HOME/techo5-thinksmart-image
while [ $# -gt 0 ]; do
	case "$1" in
	-b) BOOT=$2; shift 2;;
	-s) STORE=$2; shift 2;;
	-u) UUID=$2; shift 2;;
	-o) OUT=$2; shift 2;;
	-a) EXTRA=$2; shift 2;;
	-w) WORK=$2; shift 2;;
	*) echo "mkimage: unknown argument $1" >&2; exit 1;;
	esac
done
[ -n "$BOOT" ] && [ -n "$STORE" ] && [ -n "$UUID" ] && [ -n "$OUT" ] || {
	echo "usage: mkimage.sh -b boot.tar -s store.img -u store-uuid -o system.img [-a 'kernel arguments'] [-w workdir]" >&2; exit 1; }
say() { echo "mkimage: $*"; }

P1_START=2048; P1_SECTORS=497664
P2_START=499712; P2_SECTORS=4194304
# SECTORS is the whole image, and what repartition.sh makes system: keep the two the same.
SECTORS=$((P2_START + P2_SECTORS))
[ "$(stat -c %s "$STORE")" -eq $((P2_SECTORS * 512)) ] ||
	{ echo "mkimage: $STORE is not $((P2_SECTORS / 2048)) MiB (mkstore.sh -S $((P2_SECTORS / 2048)))" >&2; exit 1; }
[ "$(blkid -o value -s UUID "$STORE")" = "$UUID" ] || { echo "mkimage: $STORE does not have the UUID $UUID" >&2; exit 1; }

rm -rf "$WORK"
mkdir -p "$WORK/boot/extlinux"
tar -xf "$BOOT" -C "$WORK/boot" vmlinuz initramfs apq8053-lenovo-cd-18781y.dtb
for f in vmlinuz initramfs apq8053-lenovo-cd-18781y.dtb; do
	[ -s "$WORK/boot/$f" ] || { echo "mkimage: $BOOT has no $f" >&2; exit 1; }
done
BOOT_UUID=$(cat /proc/sys/kernel/random/uuid)
# lk2nd reads one entry and no more. The initramfs finds both file systems by these UUIDs, and waits
# ten seconds for a framebuffer it has no driver for unless it is told there is none. The device
# stands the other way up from the panel's own top, so the display plane turns the picture half a
# turn (echod turns the touch readings: hardware/touch/thinksmart.go).
cat > "$WORK/boot/extlinux/extlinux.conf" <<EOF
timeout 1
default techo5
menu title ThinkSmart View (TECHO5)

label techo5
	kernel /vmlinuz
	fdtdir /
	initrd /initramfs
	append consoleblank=0 vt.global_cursor_default=0 pmos_boot_uuid=$BOOT_UUID pmos_root_uuid=$UUID pmos_rootfsopts=defaults deviceinfo_no_framebuffer=true video=DSI-1:800x1280,rotate=180${EXTRA:+ $EXTRA}
EOF
[ "$(grep -c '^label' "$WORK/boot/extlinux/extlinux.conf")" = 1 ]

say "boot partition: $(du -sh "$WORK/boot" | cut -f1) of kernel files"
rm -f "$WORK/p1.img"
mke2fs -q -t ext2 -b 1024 -I 256 -L pmOS_boot -U "$BOOT_UUID" \
	-O ^64bit,^extent,^metadata_csum,^has_journal,^flex_bg,^huge_file,^dir_nlink,^extra_isize \
	-E root_owner=0:0 -d "$WORK/boot" "$WORK/p1.img" $((P1_SECTORS / 2))
e2fsck -fn "$WORK/p1.img" >/dev/null

mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
truncate -s $((SECTORS * 512)) "$OUT"
sfdisk -q "$OUT" <<EOF
label: dos
unit: sectors

start=$P1_START, size=$P1_SECTORS, type=ef, bootable
start=$P2_START, size=$P2_SECTORS, type=83
EOF
dd if="$WORK/p1.img" of="$OUT" bs=1M seek=$((P1_START / 2048)) conv=notrunc,sparse status=none
dd if="$STORE" of="$OUT" bs=1M seek=$((P2_START / 2048)) conv=notrunc,sparse status=none
sfdisk -l "$OUT" 2>/dev/null | grep -E "^$OUT|^Device" | sed "s|$OUT|p|"
say "$OUT: $SECTORS sectors, $(du -h --apparent-size "$OUT" | cut -f1) to write, $(du -h "$OUT" | cut -f1) on disk; boot $BOOT_UUID, store $UUID"
