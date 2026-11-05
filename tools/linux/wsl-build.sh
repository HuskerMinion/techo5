#!/bin/bash
# wsl-build.sh — the WSL half of deploy-rootfs.sh: copy the staged inputs to the
# WSL disk, build the root filesystem there with mkrootfs.sh under QEMU user
# emulation, and print the Windows path of the tarball.
#
#   wsl-build.sh <stage dir, WSL path> <version> <timezone>
#
# Needs qemu-user-static + binfmt-support (apt), Alpine's static apk at
# ~/apk/apk.static, and a user namespace with a subuid range (Ubuntu's default)
# so the build owns files as root without sudo. TECHO5_BUILD_DIR (default ~/techo5-build) is where it
# works, so builds can run side by side, each in its own.
set -euo pipefail
# A file's mode comes from the repo, not from the host: Ubuntu's default umask (002) gave group write.
umask 022
STAGE=$1; VERSION=$2; TZNAME=$3
W=${TECHO5_BUILD_DIR:-$HOME/techo5-build}
APK=$HOME/apk/apk.static
[ -e /proc/sys/fs/binfmt_misc/qemu-arm ] || { echo "wsl-build: no qemu-arm binfmt (apt install qemu-user-static binfmt-support)" >&2; exit 1; }
[ -x "$APK" ] || { echo "wsl-build: no $APK" >&2; exit 1; }

rm -rf "$W"
mkdir -p "$W"
cp -r "$STAGE" "$W/in"
find "$W/in" -type f -name '*.sh' -exec sed -i 's/\r$//' {} +
# --map-auto maps the whole subuid range (files owned by service users come out right); it needs
# newuidmap from the uidmap package. Without it only root is mapped, which apk tolerates for
# the packages here.
map=--map-auto
command -v newuidmap >/dev/null || map=
unshare -Ur $map sh "$W/in/tools/mkrootfs.sh" -i "$W/in" -o "$W/rootfs.tar.gz" -w "$W" \
	-V "$VERSION" -z "$TZNAME" -a armv7 -A "$APK"
# The path the caller copies from: a Windows path under WSL, a plain one on Linux.
wslpath -w "$W/rootfs.tar.gz" 2>/dev/null || echo "$W/rootfs.tar.gz"
