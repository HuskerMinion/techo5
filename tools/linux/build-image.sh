#!/usr/bin/env bash
# build-image.sh — build the boot image (LineageOS kernel + the rescue/boot
# initramfs) with tools/linux/mkimage.py.
#
#   tools/linux/build-image.sh [-o out.img] [--no-key]
#
# Inputs come from TECHO5_INPUTS (default: inputs/ in this repository; docs/building.md):
# the LineageOS boot image, the Alpine minirootfs, busybox.static, the apks
# listed in packages.txt (apks/ and apks312/), and the SSH public key. The Go
# tools are built here. Git Bash on Windows is the expected shell.
#
# --no-key builds the image published with releases: no SSH key inside, so the
# rescue environment accepts only keys already on the unit's userdata, and a new
# unit is set up from the USB serial console.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
# The unit's own LineageOS boot image (its kernel and header); never published. Per board, not just
# per unit: the header, load addresses and command line are taken from this file, so a 1st gen Show 5
# or an Echo Show 8 needs its own here rather than the 2nd gen's default.
KERNEL_IMAGE=${KERNEL_IMAGE:-$INPUTS/boot-lineage-18.1-20260904-cronos.img}
# KERNEL=Image.gz-dtb: a kernel built from source replaces the one in KERNEL_IMAGE
# (its header, load addresses and command line still come from KERNEL_IMAGE).
KERNEL=${KERNEL:-}
GO=${GO:-go}
OUT=techo5-linux-boot.img
NOKEY=
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	--no-key) NOKEY=1; shift;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done
mkdir -p "$(dirname "$OUT")"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
# Windows python wants Windows paths, and MSYS_NO_PATHCONV (needed so the
# x=/bin/y arguments survive) turns the automatic conversion off.
W() { cygpath -m "$1" 2>/dev/null || echo "$1"; }
# python3 where it is (Linux, macOS), else python (Windows).
PY=${PYTHON:-$( (python3 -c 1) >/dev/null 2>&1 && echo python3 || echo python )}

echo "== building tools for armv7"
export GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0
# audioprobe lives in the daemon's module so it shares the daemon's ALSA code rather than a copy of it.
for c in fbprobe audioprobe rebootto btbridge; do
	m=$ROOT; [ -d "$ROOT/echod/cmd/$c" ] && m=$ROOT/echod
	(cd "$m" && "$GO" build -trimpath -ldflags "-s -w" -o "$ROOT/bin/$c-arm" "./cmd/$c")
done
unset GOOS GOARCH GOARM CGO_ENABLED

# The package file packages.txt lists, or else the version tools/fetch-inputs.py took in its place when
# Alpine had dropped the listed one (it names the file for the version it fetched).
apk_file() {
	local name=${2%-*-r*.apk} have
	[ -f "$1/$2" ] && { echo "$1/$2"; return; }
	have=$(ls "$1/$name"-[0-9]*-r[0-9]*.apk 2>/dev/null | sort -V | tail -1)
	echo "${have:-$1/$2}"
}
apks=()
for a in $(sed 's/#.*//' "$ROOT/tools/linux/packages.txt"); do
	case "$a" in
	busybox-static-*) continue;;
	wpa_supplicant-2.9*|libssl1.1*|libcrypto1.1*|libnl3-3.5*) apks+=(--apk "$(W "$(apk_file "$INPUTS/apks312" "$a")")");;
	*) apks+=(--apk "$(W "$(apk_file "$INPUTS/apks" "$a")")");;
	esac
done

echo "== mkimage"
export MSYS_NO_PATHCONV=1
R=$(W "$ROOT"); I=$(W "$INPUTS")
mini=$(ls "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz | head -1)
kern=(); [ -n "$KERNEL" ] && kern=(--kernel "$(W "$KERNEL")")
key=(--copy "$I/techo5_ed25519.pub=/root/.ssh/authorized_keys"); [ -n "$NOKEY" ] && key=()
"$PY" "$R/tools/linux/mkimage.py" --kernel-image "$(W "$KERNEL_IMAGE")" "${kern[@]}" \
	--rootfs "$(W "$mini")" \
	"${apks[@]}" \
	--init "$R/tools/linux/init" \
	--add "$I/busybox.static=/bin/busybox.static" \
	--add "$R/bin/fbprobe-arm=/usr/local/bin/fbprobe" \
	--add "$R/bin/audioprobe-arm=/usr/local/bin/audioprobe" \
	--add "$R/bin/rebootto-arm=/usr/local/bin/rebootto" \
	--add "$R/bin/btbridge-arm=/usr/local/bin/btbridge" \
	--script "$R/tools/linux/slotctl=/usr/local/sbin/slotctl" \
	--script "$R/tools/linux/techo5-lib.sh=/lib/techo5-lib.sh" \
	--script "$R/tools/linux/rescue-profile.sh=/etc/profile.d/rescue.sh" \
	"${key[@]}" \
	--compress xz --cmdline-append techo5=linux -o "$(W "$OUT")"
echo "built: $OUT"
echo "flash: fastboot flash boot $OUT && fastboot continue"
