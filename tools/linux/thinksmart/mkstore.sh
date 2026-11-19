#!/bin/bash
# mkstore.sh — the techo5-store image for the Lenovo ThinkSmart View (APQ8053): a slot store whose
# root is also the boot environment, because postmarketOS's initramfs mounts it as the root and runs
# its /sbin/init. Made from tools/linux/skylight/mkstore.sh; the board, its init and the kernel's
# modules and firmware differ.
#
#   mkstore.sh -r <rootfs.tar.gz> -k <authorized_keys>|none -M <modules.tar> -F <firmware.tar>
#              -o <store.img> [-U <uuid>] [-S <MiB>] [-W <wpa_supplicant.conf>] [-i <inputs>]
#              [-w <workdir>] [-f <fbprobe>] [-s <slot a state>] [-L <label>]
#
# Linux (WSL) with qemu-user-static + binfmt for arm, Alpine's static apk at ~/apk/apk.static, a user
# namespace (uidmap) and e2fsprogs; network for apk. Where a user namespace is not allowed to own
# files (Ubuntu's AppArmor, kernel.apparmor_restrict_unprivileged_userns), run it as root with
# --inner. What it makes:
#
#   /sbin/init            tools/linux/thinksmart/init (the initramfs runs it)
#   /bin, /lib, /usr ...  Alpine armv7 + dropbear, wpa_supplicant 2.9, iw, e2fsprogs, udhcpd:
#                         the rescue environment, which never changes with an update
#   /bin/busybox.static   PID 1's interpreter
#   /lib/modules, /lib/firmware   -M and -F: the kernel's own, as tar files of modules/<version> and
#                         firmware/ (from a unit running that kernel, or the kernel's packages, with
#                         the out-of-tree snd-soc-tas5782m.ko in extra/). They go with the kernel in
#                         lk2nd's boot partition, not with a slot, and are bound into the slot that
#                         boots. The audio DSP's firmware is the unit's own, read from its modem
#                         partition at boot, and is not in the image.
#   /root/.ssh/authorized_keys   <authorized_keys>: the rescue's SSH keys, and the slot's bench keys
#                         (BENCH_SSH_KEYS in device.conf); `-k none` leaves it empty, as an image
#                         meant for anyone must. A key here opens SSH on every interface, Wi-Fi
#                         included, from every boot: for a unit on the bench only
#   /root/wpa_supplicant.conf   -W: a network for a bench unit, copied to a unit that has none
#   /.techo5-store        the store marker, naming the board (board=thinksmart): slotctl then installs
#                         only a ThinkSmart View rootfs into the other slot
#   /active, /slots/a (<rootfs.tar.gz>), /slots/a.state   the store, as slotctl has it
#   /format-data          tells the first boot to make techo5-data
#
# -U gives the file system the UUID lk2nd's extlinux.conf names (mkimage.sh -u), and -S its size:
# 2048 for mkimage.sh, which wants the store to fill its partition. The rootfs is made with
# DEVICE_OVERLAY=tools/linux/thinksmart/overlay.
set -euo pipefail
# Modes come from the repo, not from the host's umask (Ubuntu's default 002 gives group write).
umask 022

HERE=$(cd "$(dirname "$0")" && pwd)
LINUX=$(cd "$HERE/.." && pwd)
INPUTS=$(cd "$HERE/../../.." && pwd)/inputs
ROOTFS=; KEYS=; OUT=; WORK=$HOME/techo5-thinksmart-store; FBPROBE=; STATE=good; LABEL=techo5-store; MODULES=; FIRMWARE=; UUID=; SIZE=; WIFI=
APK=${APK:-$HOME/apk/apk.static}
INNER=
while [ $# -gt 0 ]; do
	case "$1" in
	-r) ROOTFS=$2; shift 2;;
	-k) KEYS=$2; shift 2;;
	-o) OUT=$2; shift 2;;
	-i) INPUTS=$2; shift 2;;
	-w) WORK=$2; shift 2;;
	-f) FBPROBE=$2; shift 2;;
	-s) STATE=$2; shift 2;;
	-L) LABEL=$2; shift 2;;
	-M) MODULES=$2; shift 2;;
	-F) FIRMWARE=$2; shift 2;;
	-U) UUID=$2; shift 2;;
	-S) SIZE=$2; shift 2;;
	-W) WIFI=$2; shift 2;;
	--inner) INNER=1; shift;;
	*) echo "mkstore: unknown argument $1" >&2; exit 1;;
	esac
done
[ -n "$ROOTFS" ] && [ -n "$KEYS" ] && [ -n "$OUT" ] && [ -n "$MODULES" ] && [ -n "$FIRMWARE" ] || {
	echo "usage: mkstore.sh -r rootfs.tar.gz -k authorized_keys|none -M modules.tar -F firmware.tar -o store.img [-U uuid] [-S MiB] [-i inputs] [-w workdir] [-f fbprobe] [-s 'good'|'trial 3']" >&2; exit 1; }
case "$STATE" in good|"trial "[0-9]) ;; *) echo "mkstore: slot state must be good or 'trial N'" >&2; exit 1;; esac
say() { echo "mkstore: $*"; }

if [ -z "$INNER" ]; then
	[ -e /proc/sys/fs/binfmt_misc/qemu-arm ] || { echo "mkstore: no qemu-arm binfmt" >&2; exit 1; }
	[ -x "$APK" ] || { echo "mkstore: no $APK" >&2; exit 1; }
	if [ "$KEYS" != none ]; then
		grep -q '^ssh-' "$KEYS" || { echo "mkstore: $KEYS holds no public key" >&2; exit 1; }
		if grep -q 'PRIVATE KEY' "$KEYS"; then echo "mkstore: $KEYS is a private key" >&2; exit 1; fi
		echo "mkstore: warning: this image carries SSH keys, and a unit running it accepts them on every" >&2
		echo "mkstore: interface, Wi-Fi too, whatever the SSH switch says. Bench units only; -k none for anyone else." >&2
	fi
	map=--map-auto; command -v newuidmap >/dev/null || map=
	# Files owned by root (and by the slot's service users) without sudo.
	exec unshare -Ur $map bash "$0" --inner -r "$ROOTFS" -k "$KEYS" -o "$OUT" -i "$INPUTS" -w "$WORK" \
		${FBPROBE:+-f "$FBPROBE"} -s "$STATE" -L "$LABEL" -M "$MODULES" -F "$FIRMWARE" ${UUID:+-U "$UUID"} ${SIZE:+-S "$SIZE"} ${WIFI:+-W "$WIFI"}
fi

R=$WORK/root
rm -rf "$WORK"
mkdir -p "$R"
mini=$(ls "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz | head -1)
# One base only, as mkrootfs.sh: an old one beside a new one would be taken first.
[ "$(ls "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz | wc -l)" -eq 1 ] || { echo "mkstore: more than one alpine-minirootfs-*-armv7.tar.gz in $INPUTS: keep only the one techo5lib.py names" >&2; exit 1; }
say "base: $(basename "$mini")"
tar -xzf "$mini" -C "$R"
cp /etc/resolv.conf "$R/etc/resolv.conf"
say "apk add (rescue tools)"
"$APK" --root "$R" --arch armv7 --no-cache add dropbear iw wireless-tools e2fsprogs e2fsprogs-extra busybox-extras >/dev/null
# wpa_supplicant 2.9 and its 1.1 libraries, as in the slots (tools/linux/README.md).
"$APK" --root "$R" --arch armv7 --no-cache add "$INPUTS"/apks312/wpa_supplicant-2.9-*.apk \
	"$INPUTS"/apks312/libssl1.1-*.apk "$INPUTS"/apks312/libcrypto1.1-*.apk >/dev/null
"$APK" --root "$R" --arch armv7 info -v 2>/dev/null | sort > "$R/etc/techo5-packages"
rm -f "$R/etc/resolv.conf"
ln -s /run/resolv.conf "$R/etc/resolv.conf"

say "boot and rescue files"
install -m 755 "$INPUTS/busybox.static" "$R/bin/busybox.static"
lf() { sed 's/\r$//' "$1" > "$2"; chmod "$3" "$2"; }
# postmarketOS's initramfs runs /sbin/init of the root it mounted, and wants an /etc/os-release there
# (Alpine's base has one).
rm -f "$R/sbin/init"
lf "$HERE/init" "$R/sbin/init" 755
[ -e "$R/etc/os-release" ] || { echo "mkstore: the base has no /etc/os-release, which postmarketOS's initramfs asks for" >&2; exit 1; }
# What turns up after the first pass of drivers asks mdev for its own (the store's /sbin/init).
printf '$MODALIAS=.*\troot:root\t0660\t@modprobe -qb "$MODALIAS"\n' > "$R/etc/mdev.conf"
install -d "$R/usr/local/sbin" "$R/usr/local/bin" "$R/etc/profile.d"
lf "$LINUX/slotctl" "$R/usr/local/sbin/slotctl" 755
lf "$LINUX/bootctl" "$R/usr/local/sbin/bootctl" 755
lf "$LINUX/techo5-lib.sh" "$R/lib/techo5-lib.sh" 644
lf "$HERE/rescue-profile.sh" "$R/etc/profile.d/techo5-rescue.sh" 644
[ -n "$FBPROBE" ] && install -m 755 "$FBPROBE" "$R/usr/local/bin/fbprobe"
echo techo5-rescue > "$R/etc/hostname"
# Read-only root: what the rescue writes goes to /run.
rm -rf "$R/etc/dropbear"
ln -s /run/dropbear "$R/etc/dropbear"
sed -i 's|^root:[^:]*:|root:*:|' "$R/etc/shadow"
install -d -m 700 "$R/root" "$R/root/.ssh"
if [ "$KEYS" = none ]; then
	: > "$R/root/.ssh/authorized_keys"
else
	grep '^ssh-' "$KEYS" | sed 's/\r$//' > "$R/root/.ssh/authorized_keys"
fi
chmod 600 "$R/root/.ssh/authorized_keys"
# -W: a wpa_supplicant.conf for a bench unit, which has no Android store to take a network from and
# nobody at its screen: the store's /sbin/init copies it to a unit that has none. Never in an image
# for anyone else.
if [ -n "$WIFI" ]; then
	grep -q '^network={' "$WIFI" || { echo "mkstore: $WIFI holds no network" >&2; exit 1; }
	echo "mkstore: warning: this image carries a Wi-Fi network and its password. Bench units only." >&2
	install -m 600 "$WIFI" "$R/root/wpa_supplicant.conf"
fi
install -d "$R/proc" "$R/sys" "$R/dev" "$R/run" "$R/data" "$R/userdata" "$R/vendor" "$R/newroot"
install -d -m 1777 "$R/tmp"

say "store: slot a from $(basename "$ROOTFS"), $STATE"
echo board=thinksmart > "$R/.techo5-store"
install -d "$R/slots/a"
tar -xzf "$ROOTFS" -C "$R/slots/a"
for f in sbin/init usr/local/bin/techo5 etc/techo5/boot.sh etc/inittab store; do
	[ -e "$R/slots/a/$f" ] || [ -L "$R/slots/a/$f" ] || { echo "mkstore: not a techo5 rootfs: $f missing" >&2; exit 1; }
done
grep -qx 'T5_BOARD=thinksmart' "$R/slots/a/etc/techo5/device.conf" 2>/dev/null ||
	{ echo "mkstore: the rootfs has no ThinkSmart View device.conf (DEVICE_OVERLAY=tools/linux/thinksmart/overlay)" >&2; exit 1; }
for d in lib/modules lib/firmware; do
	[ -d "$R/slots/a/$d" ] || { echo "mkstore: the rootfs has no /$d to bind the store's onto" >&2; exit 1; }
done
# The kernel's modules and firmware (tools/linux/thinksmart/README.md says where they come from):
# the store's, bound into whichever slot boots. The modem partition's firmware is linked in under
# msm-firmware-loader at boot.
say "modules from $(basename "$MODULES"), firmware from $(basename "$FIRMWARE")"
rm -rf "$R/lib/modules" "$R/lib/firmware"
tar -xf "$MODULES" -C "$R/lib" --no-same-owner
tar -xf "$FIRMWARE" -C "$R/lib" --no-same-owner
kver=$(ls "$R/lib/modules" | head -1)
[ -s "$R/lib/modules/$kver/modules.dep" ] || { echo "mkstore: $MODULES holds no modules/<version>/modules.dep" >&2; exit 1; }
[ -e "$R/lib/modules/$kver/extra/snd-soc-tas5782m.ko" ] || echo "mkstore: warning: no snd-soc-tas5782m.ko in the modules: the unit will have no sound" >&2
install -d "$R/lib/firmware/msm-firmware-loader"
say "kernel modules: $kver, $(find "$R/lib/modules" -name '*.ko*' | wc -l) of them"
# techo5-data is made at the first boot (the store's /sbin/init).
: > "$R/format-data"
printf '%s\n' "$STATE" > "$R/slots/a.state"
echo a > "$R/active"
echo "ThinkSmart View store built $(date -u '+%Y-%m-%dT%H:%MZ'); slot a: $(cat "$R/slots/a/etc/techo5-release")" > "$R/etc/techo5-store-release"

# Size: the contents plus room for logs of slotctl's own writes and a margin; inodes likewise.
used=$(du -s --block-size=1M "$R" | cut -f1)
files=$(find "$R" | wc -l)
size=$(( (used * 115 / 100 + 64 + 3) / 4 * 4 ))
[ -n "$SIZE" ] && { [ "$SIZE" -ge "$size" ] || { echo "mkstore: ${SIZE} MiB is too small for ${used} MiB of contents" >&2; exit 1; }; size=$SIZE; }
inodes=$(( files * 13 / 10 + 4096 ))
[ -n "$SIZE" ] && inodes=$(( inodes * 4 ))
say "contents ${used} MiB, $files entries; filesystem ${size} MiB, $inodes inodes"
mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
mke2fs -q -t ext4 -b 4096 -I 256 -N "$inodes" -L "$LABEL" ${UUID:+-U "$UUID"} -m 0 \
	-O none,ext_attr,resize_inode,dir_index,filetype,extent,sparse_super,large_file,huge_file,uninit_bg,dir_nlink,extra_isize \
	-E root_owner=0:0 -d "$R" "$OUT" "${size}M"
# The nodes the kernel opens before /init has mounted /dev.
printf 'cd /dev
mknod console c 5 1
mknod null c 1 3
mknod kmsg c 1 11
sif console mode 020600
sif null mode 020666
sif kmsg mode 020600
' > "$WORK/devnodes"
debugfs -w -f "$WORK/devnodes" "$OUT" >/dev/null 2>&1
debugfs -R "ls -l /dev" "$OUT" 2>/dev/null | grep -q kmsg || { echo "mkstore: device nodes missing" >&2; exit 1; }
e2fsck -fn "$OUT" >/dev/null
say "fsck clean; $(dumpe2fs -h "$OUT" 2>/dev/null | grep -E '^Filesystem features' | tr -s ' ')"
say "free: $(dumpe2fs -h "$OUT" 2>/dev/null | awk -F: '/^Free blocks/ {printf "%d MiB", $2*4/1024}')"
say "$OUT: $(du -h --apparent-size "$OUT" | cut -f1)"
