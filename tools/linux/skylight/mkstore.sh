#!/bin/bash
# mkstore.sh — the techo5-store partition image for the Skylight Calendar 15 (RK3566): a slot store
# whose root is also the boot environment, because TECHO5's kernel boots straight from it (no
# initramfs). Made from tools/linux/athena/mkstore.sh; only the board and its /init differ.
#
#   mkstore.sh -r <rootfs.tar.gz> -k <authorized_keys>|none -o <system.img> [-i <inputs>] [-w <workdir>]
#              [-f <fbprobe>] [-s <slot a state>] [-L <label>] [-F <unit firmware>]
#
# Linux (WSL) with qemu-user-static + binfmt for arm, Alpine's static apk at ~/apk/apk.static, a user
# namespace (uidmap) and e2fsprogs; network for apk. What it makes:
#
#   /init                 tools/linux/skylight/init (the kernel runs it)
#   /bin, /lib, /usr ...  Alpine armv7 + dropbear, wpa_supplicant 2.9, iw, e2fsprogs, udhcpd:
#                         the rescue environment, which never changes with an update
#   /bin/busybox.static   PID 1's interpreter
#   /root/.ssh/authorized_keys   <authorized_keys>: the rescue's SSH keys, and the slot's bench keys
#                         (BENCH_SSH_KEYS in device.conf); `-k none` leaves it empty, as an image
#                         meant for anyone must (rescue is then USB serial only). A key here opens
#                         SSH on every interface, Wi-Fi included, from every boot (the bench hook
#                         in boot.sh, whatever the SSH switch says): for a unit on the bench only
#   /.techo5-store        the store marker, naming the board (board=skylight): slotctl then installs
#                         only a Skylight rootfs into the other slot
#   /active, /slots/a (<rootfs.tar.gz>), /slots/a.state   the store, as slotctl has it
#   /lib/firmware/brcm    -F <dir>: the unit's own Wi-Fi and Bluetooth firmware, copied from its stock
#                         vendor partition (/vendor/etc/firmware: fw_bcm43456c5_ag.bin,
#                         nvram_ap6256.txt, BCM4345C5.hcd), under the names the drivers ask for. They
#                         are the vendor's, so a store image carrying them is for that unit only, never
#                         published; without -F the unit has no Wi-Fi or Bluetooth until they are added.
#
# The filesystem has athena's feature set (no journal, no 64bit or metadata_csum), which slotctl grow
# is known to handle, and is only as large as its contents need, so it writes quickly. The first boot grows it to fill the partition (slotctl grow, from /init), which is
# what makes room for the second slot.
#
# <rootfs.tar.gz> comes from tools/linux/deploy-rootfs.sh --out, with
# DEVICE_OVERLAY=tools/linux/skylight/overlay EXTRA_PACKAGES=busybox-extras.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
LINUX=$(cd "$HERE/.." && pwd)
INPUTS=$(cd "$HERE/../../.." && pwd)/inputs
ROOTFS=; KEYS=; OUT=; WORK=$HOME/techo5-skylight-store; FBPROBE=; STATE=good; LABEL=techo5-store; UNITFW=
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
	-F) UNITFW=$2; shift 2;;
	--inner) INNER=1; shift;;
	*) echo "mkstore: unknown argument $1" >&2; exit 1;;
	esac
done
[ -n "$ROOTFS" ] && [ -n "$KEYS" ] && [ -n "$OUT" ] || {
	echo "usage: mkstore.sh -r rootfs.tar.gz -k authorized_keys|none -o system.img [-i inputs] [-w workdir] [-f fbprobe] [-s 'good'|'trial 3']" >&2; exit 1; }
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
		${FBPROBE:+-f "$FBPROBE"} -s "$STATE" -L "$LABEL" ${UNITFW:+-F "$UNITFW"}
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
lf "$HERE/init" "$R/init" 755
install -d "$R/usr/local/sbin" "$R/usr/local/bin" "$R/etc/profile.d"
lf "$LINUX/slotctl" "$R/usr/local/sbin/slotctl" 755
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
install -d "$R/proc" "$R/sys" "$R/dev" "$R/run" "$R/data" "$R/userdata" "$R/vendor" "$R/newroot"
install -d -m 1777 "$R/tmp"

say "store: slot a from $(basename "$ROOTFS"), $STATE"
echo board=skylight > "$R/.techo5-store"
install -d "$R/slots/a"
tar -xzf "$ROOTFS" -C "$R/slots/a"
for f in sbin/init usr/local/bin/techo5 etc/techo5/boot.sh etc/inittab store; do
	[ -e "$R/slots/a/$f" ] || [ -L "$R/slots/a/$f" ] || { echo "mkstore: not a techo5 rootfs: $f missing" >&2; exit 1; }
done
grep -qx 'T5_BOARD=skylight' "$R/slots/a/etc/techo5/device.conf" 2>/dev/null ||
	{ echo "mkstore: the rootfs has no Skylight device.conf (DEVICE_OVERLAY=tools/linux/skylight/overlay)" >&2; exit 1; }
if [ -n "$UNITFW" ]; then
	say "the unit's own firmware from $UNITFW"
	install -D -m 644 "$UNITFW/fw_bcm43456c5_ag.bin" "$R/lib/firmware/brcm/brcmfmac43456-sdio.bin"
	install -D -m 644 "$UNITFW/nvram_ap6256.txt" "$R/lib/firmware/brcm/brcmfmac43456-sdio.txt"
	install -D -m 644 "$UNITFW/BCM4345C5.hcd" "$R/lib/firmware/brcm/BCM4345C5.hcd"
fi
printf '%s\n' "$STATE" > "$R/slots/a.state"
echo a > "$R/active"
echo "Skylight store built $(date -u '+%Y-%m-%dT%H:%MZ'); slot a: $(cat "$R/slots/a/etc/techo5-release")" > "$R/etc/techo5-store-release"

# Size: the contents plus room for logs of slotctl's own writes and a margin; inodes likewise.
used=$(du -s --block-size=1M "$R" | cut -f1)
files=$(find "$R" | wc -l)
size=$(( (used * 115 / 100 + 64 + 3) / 4 * 4 ))
inodes=$(( files * 13 / 10 + 4096 ))
say "contents ${used} MiB, $files entries; filesystem ${size} MiB, $inodes inodes"
mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
mke2fs -q -t ext4 -b 4096 -I 256 -N "$inodes" -L "$LABEL" -m 0 \
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
