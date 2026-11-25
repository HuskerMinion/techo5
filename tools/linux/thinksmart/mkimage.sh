#!/bin/bash
# mkimage.sh — the image written to a Lenovo ThinkSmart View's "system" partition: an MBR with lk2nd's
# boot partition and TECHO5's store.
#
#   mkimage.sh -K <kernel dir> -s <store.img> -o <system.img> [-w <workdir>]
#
#   <kernel dir>  kernel/build.sh's output: vmlinuz, apq8053-lenovo-cd-18781y.dtb and
#                 boot-thinksmart.img, whose command line extlinux.conf takes (without its trial word)
#   <store.img>   tools/linux/thinksmart/mkstore.sh's, made with -S 2048
#
# The unit's system partition is the size of this image exactly, 4694016 sectors, with TECHO5's data
# in a GPT partition of its own after it (tools/linux/thinksmart/repartition.sh). Inside the image:
#
#   p1   2048 + 497664 sectors     ext2 with the few features lk2nd reads: extlinux/extlinux.conf,
#                                  the kernel and device tree it names (k-<sum>/), and the A/B
#                                  record (techo5-ab: slot a good, slot b empty)
#   p2   499712 + 4194304          the store (2 GiB)
#
# There is no initramfs. The kernel's command line (kernel/build.sh's) has the kernel make both
# partitions in the device mapper (dm-mod.create, system1 and system2, the names init looks for) at
# these offsets in the GPT partition named "system", so the image is the same for every unit, then
# mount system2 read-only as / and run its /sbin/init. p1 is laid out as the boot layer (lk2nd-ab)
# lays it out, with slot a's kernel; every later kernel comes with its slot, and lk2nd-ab puts it here.
#
# Linux with e2fsprogs and sfdisk. No root and no loop devices: the file systems are made from
# directories (mke2fs -d) and copied in at their offsets.
set -euo pipefail
umask 022

KDIR=; STORE=; OUT=; WORK=$HOME/techo5-thinksmart-image
while [ $# -gt 0 ]; do
	case "$1" in
	-K) KDIR=$2; shift 2;;
	-s) STORE=$2; shift 2;;
	-o) OUT=$2; shift 2;;
	-w) WORK=$2; shift 2;;
	*) echo "mkimage: unknown argument $1" >&2; exit 1;;
	esac
done
[ -n "$KDIR" ] && [ -n "$STORE" ] && [ -n "$OUT" ] || {
	echo "usage: mkimage.sh -K kernel-dir -s store.img -o system.img [-w workdir]" >&2; exit 1; }
say() { echo "mkimage: $*"; }

P1_START=2048; P1_SECTORS=497664
P2_START=499712; P2_SECTORS=4194304
# SECTORS is the whole image, and what repartition.sh makes system: keep the two the same.
SECTORS=$((P2_START + P2_SECTORS))
[ "$(stat -c %s "$STORE")" -eq $((P2_SECTORS * 512)) ] ||
	{ echo "mkimage: $STORE is not $((P2_SECTORS / 2048)) MiB (mkstore.sh -S $((P2_SECTORS / 2048)))" >&2; exit 1; }

rm -rf "$WORK"
mkdir -p "$WORK/boot/extlinux"
for f in vmlinuz apq8053-lenovo-cd-18781y.dtb boot-thinksmart.img; do
	[ -s "$KDIR/$f" ] || { echo "mkimage: $KDIR has no $f" >&2; exit 1; }
done
# The kernel's directory is named as lk2nd-ab names it: k- and 16 digits of the boot image's sha256.
DTB=apq8053-lenovo-cd-18781y.dtb
K=k-$(sha256sum "$KDIR/boot-thinksmart.img" | cut -c1-16)
install -D -m 644 "$KDIR/vmlinuz" "$WORK/boot/$K/vmlinuz"
install -D -m 644 "$KDIR/$DTB" "$WORK/boot/$K/$DTB"
# The command line is the boot image's, which must be for this layout and this kernel.
APPEND=$(python3 "$(dirname "$0")/kernel/pack.py" cmdline "$KDIR/boot-thinksmart.img")
MAP="system1,,,rw,0 $P1_SECTORS linear PARTLABEL=system $P1_START;system2,,,rw,0 $P2_SECTORS linear PARTLABEL=system $P2_START"
case "$APPEND" in *"dm-mod.create=\"$MAP\""*) ;; *)
	echo "mkimage: the kernel's command line does not map this image's partitions: $APPEND" >&2; exit 1;; esac
ksz=$(od -An -tu4 -j8 -N4 "$KDIR/boot-thinksmart.img" | tr -d ' ')
cat "$KDIR/vmlinuz" "$KDIR/apq8053-lenovo-cd-18781y.dtb" |
	cmp -s - <(dd if="$KDIR/boot-thinksmart.img" bs=2048 skip=1 status=none | head -c "$ksz") ||
	{ echo "mkimage: vmlinuz and the device tree are not boot-thinksmart.img's kernel" >&2; exit 1; }
# lk2nd reads one entry and no more. lk2nd-ab's install writes this same form.
cat > "$WORK/boot/extlinux/extlinux.conf" <<EOF
timeout 1
default techo5
menu title ThinkSmart View (TECHO5)
label techo5
	kernel /$K/vmlinuz
	fdt /$K/$DTB
	append $APPEND ab.slot=a
EOF
[ "$(grep -c '^label' "$WORK/boot/extlinux/extlinux.conf")" = 1 ]
# The A/B record, bootctl's (AOSP's struct bootloader_control, at byte 2048 of what would be a misc
# partition): slot a priority 15, 3 tries, successful; slot b, which holds nothing yet, priority 0.
python3 - "$WORK/boot/techo5-ab" <<'PY'
import struct, sys, zlib
r = bytearray(32)
r[0:2] = b"_a"
struct.pack_into("<I", r, 4, 0x42414342)
r[8], r[9] = 1, 2
r[12] = 15 | 3 << 4 | 1 << 7
struct.pack_into("<I", r, 28, zlib.crc32(bytes(r[:28])))
m = bytearray(4096)
m[2048:2080] = r
open(sys.argv[1], "wb").write(m)
PY

say "boot partition: $(du -sh "$WORK/boot" | cut -f1) of kernel files"
rm -f "$WORK/p1.img"
mke2fs -q -t ext2 -b 1024 -I 256 -L techo5-boot \
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
say "$OUT: $SECTORS sectors, $(du -h --apparent-size "$OUT" | cut -f1) to write, $(du -h "$OUT" | cut -f1) on disk"
