#!/bin/sh
# mkrootfs.sh — build the TECHO5 persistent root filesystem as a tarball.
#
# Runs on the device, or on an x86 Linux host (WSL) with Alpine's static apk,
# QEMU user emulation and binfmt so the packages' triggers can execute inside
# the armv7 root, and a user namespace for the file ownership (see
# deploy-rootfs.sh, which drives both ways). Either way apk installs the
# packages properly, with their triggers and a package database — which is
# what makes `apk add bluez` possible later.
#
#   mkrootfs.sh -i <indir> -o <out.tar.gz> [-w <workdir>] [-V <version>] [-z <timezone>]
#               [-a <arch>] [-A <apk binary>]
#
# <indir> layout (what deploy-rootfs.sh stages):
#   bin/techo5 bin/fbprobe bin/audioprobe bin/rebootto bin/btbridge  Go binaries, armv7
#   bin/techo5-aec                                         WebRTC echo canceller helper (C++, build-aec.sh), optional
#   tools/slotctl tools/techo5-lib.sh tools/packages-rootfs.txt
#   overlay/                                               tools/linux/rootfs from the repo
#   inputs/alpine-minirootfs-*-armv7.tar.gz
#   inputs/vendor.tar.gz                                   optional, development only: LineageOS's vendor/.
#                                                          Published images never carry it: each unit keeps
#                                                          its own in the store (etc/techo5/boot.sh)
#   inputs/apks312/*.apk                                   wpa_supplicant 2.9 + libssl1.1 + libcrypto1.1
set -e

IN=; OUT=; WORK=/data/techo5-linux/build; VERSION=dev; TZNAME=UTC; ARCH=; APK=apk
while [ $# -gt 0 ]; do
	case "$1" in
	-i) IN=$2; shift 2;;
	-o) OUT=$2; shift 2;;
	-w) WORK=$2; shift 2;;
	-V) VERSION=$2; shift 2;;
	-z) TZNAME=$2; shift 2;;
	-a) ARCH=$2; shift 2;;
	-A) APK=$2; shift 2;;
	*) echo "mkrootfs: unknown argument $1" >&2; exit 1;;
	esac
done
[ -n "$IN" ] && [ -n "$OUT" ] || { echo "usage: mkrootfs.sh -i indir -o out.tar.gz [-w workdir] [-V version] [-z timezone]" >&2; exit 1; }

say() { echo "mkrootfs: $*"; }
R=$WORK/root
rm -rf "$R"
mkdir -p "$R"

mini=$IN/inputs/alpine-minirootfs-3.24.2-armv7.tar.gz
say "base: $(basename "$mini")"
tar -xzf "$mini" -C "$R"

# Package installation needs a resolver inside the root.
cp /etc/resolv.conf "$R/etc/resolv.conf"

# On a host the package architecture has to be said; on the device it is the machine's own.
arch=
[ -n "$ARCH" ] && arch="--arch $ARCH"
pkgs=$(sed 's/#.*//' "$IN/tools/packages-rootfs.txt" | tr '\n' ' ')
say "apk add: $pkgs"
$APK --root "$R" $arch --no-cache add $pkgs
say "apk add (local): $(ls "$IN"/inputs/apks312/*.apk | xargs -n1 basename | tr '\n' ' ')"
$APK --root "$R" $arch --no-cache add --allow-untrusted "$IN"/inputs/apks312/*.apk
$APK --root "$R" $arch info -v | sort > "$R/etc/techo5-packages"

# Vendor tree: Wi-Fi/BT modules, firmware (firmware_class.path=/vendor/firmware on
# the kernel command line), the audio tuning the daemon reads. It is Amazon's and the chip makers',
# so an image leaves /vendor empty: the unit's own tree, kept in the store, is mounted there at boot.
install -d "$R/vendor"
if [ -e "$IN/inputs/vendor.tar.gz" ]; then
	say "vendor tree (development image: not for publishing)"
	tar -xzf "$IN/inputs/vendor.tar.gz" -C "$R" vendor
	# The Wi-Fi driver the device boots with (etc/techo5/device.conf in the overlay; the Show's by default).
	WIFI_MODULE=/vendor/lib/modules/mt76x8_wlan.ko
	[ -r "$IN/overlay/etc/techo5/device.conf" ] && WIFI_MODULE=$(sed -n 's/^WIFI_MODULE=//p' "$IN/overlay/etc/techo5/device.conf" | tr -d '
"')
	[ -e "$R$WIFI_MODULE" ] || { echo "mkrootfs: vendor tree has no $WIFI_MODULE" >&2; exit 1; }
else
	say "no vendor tree: the unit's own is mounted at /vendor"
fi

# Our binaries and scripts.
install -d "$R/usr/local/bin" "$R/usr/local/sbin" "$R/lib" "$R/var/lib/bluetooth" "$R/var/lib/bluealsa" "$R/usr/var/lib/bluealsa"
for b in techo5 fbprobe audioprobe rebootto btbridge techo5-aec; do
	[ -e "$IN/bin/$b" ] && install -m 755 "$IN/bin/$b" "$R/usr/local/bin/$b"
done
install -m 755 "$IN/tools/slotctl" "$R/usr/local/sbin/slotctl"
install -m 644 "$IN/tools/techo5-lib.sh" "$R/lib/techo5-lib.sh"

# Overlay from the repo (tools/linux/rootfs), CRLF-safe.
say "overlay"
(cd "$IN/overlay" && tar -cf - .) | tar -xf - -C "$R"
find "$R/etc/techo5" "$R/usr/local/sbin" "$R/lib/techo5-lib.sh" "$R/etc/inittab" "$R/etc/profile.d/techo5.sh" "$R/etc/hostname" "$R/etc/motd" \
	-type f -exec sed -i 's/\r$//' {} +
chmod 755 "$R"/etc/techo5/*.sh "$R"/usr/local/sbin/*

# Optional mainline modules and firmware, built from the same kernel as boot.
if [ -f "$IN/inputs/kernel.tar.gz" ]; then
	tar -xzf "$IN/inputs/kernel.tar.gz" -C "$R"
fi

# State that must survive an image change lives on userdata.
rm -rf "$R/etc/dropbear"
ln -s /data/techo5-linux/dropbear "$R/etc/dropbear"
rm -f "$R/etc/resolv.conf"
ln -s /run/resolv.conf "$R/etc/resolv.conf"

# Root: no password, and no SSH key in the image. Keys live on userdata and only arrive from Home
# Assistant (echod's ssh_keys action); echod starts dropbear when the SSH switch is on.
rm -rf "$R/root/.ssh"
ln -s /data/misc/techo5/ssh "$R/root/.ssh"
sed -i 's|^root:[^:]*:|root:*:|' "$R/etc/shadow"

# Clock. The zone is the unit's, not the image's: /etc/localtime and /etc/timezone link to userdata,
# where echod writes the zone Home Assistant reports (feature/timezone). boot.sh creates them from
# /etc/techo5/default-timezone (-z, UTC unless given) on a unit that has none yet.
[ -e "$R/usr/share/zoneinfo/$TZNAME" ] || { say "warning: no zoneinfo for $TZNAME; UTC"; TZNAME=UTC; }
echo "$TZNAME" > "$R/etc/techo5/default-timezone"
rm -f "$R/etc/localtime" "$R/etc/timezone"
ln -s /data/misc/techo5/localtime "$R/etc/localtime"
ln -s /data/misc/techo5/timezone "$R/etc/timezone"

mkdir -p "$R/store" "$R/data" "$R/run" "$R/proc" "$R/sys" "$R/dev" "$R/tmp" "$R/newroot"
chmod 1777 "$R/tmp"
# The daemon's own version line comes from running it, which on a host goes through QEMU.
echo "techo5 rootfs $VERSION" > "$R/etc/techo5-release"

say "packing"
rm -f "$OUT"
tar -czf "$OUT" -C "$R" .
say "$(cat "$R/etc/techo5-release")"
say "$OUT: $(du -h "$OUT" | cut -f1) ($(du -sh "$R" | cut -f1) unpacked)"
