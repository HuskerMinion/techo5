#!/usr/bin/env bash
# deploy-rootfs.sh — build the daemon and tools, then the root filesystem (tools/linux/mkrootfs.sh), and
# either keep the tarball (--out) or send it to a unit and optionally install it into the inactive slot.
#
#   tools/linux/deploy-rootfs.sh --out rootfs.tar.gz [--version vX.Y.Z]            build here, keep the file
#   HOST=<unit> tools/linux/deploy-rootfs.sh [--install] [--reboot] [--version vX.Y.Z] [--on-device]
#
# Where the root filesystem is built:
#   Linux (x86_64)   here, when qemu-user-static and binfmt-support are installed and Alpine's static apk
#                    is at ~/apk/apk.static (tools/fetch-inputs.py fetches it)
#   Windows          in WSL (Ubuntu) with the same packages, driven from Git Bash
#   otherwise        on the unit itself over SSH (macOS, or --on-device), which needs HOST
# mkrootfs.sh runs inside a user namespace so files can be owned by root without sudo.
#
# Environment: HOST (the unit; needed unless --out), KEY (the SSH key the unit accepts; default
# TECHO5_SSH_KEY, else ~/.ssh/id_ed25519), TECHO5_INPUTS (the Alpine minirootfs, apks312/ and models/;
# default inputs/ in this repository; tools/fetch-inputs.py), TZ_NAME (a new unit's zone until Home
# Assistant sets it; UTC), GO (go binary), WSL_DISTRO (Ubuntu), PREBUILT_DAEMON (the echod-arm or
# echod-arm-spot the "Build release binaries" GitHub Actions workflow built from this release's tag,
# vX.Y.Z or spot-vX.Y.Z with --version vX.Y.Z; skips building it here, so the rootfs carries exactly what
# CI attested instead of a local rebuild; needs gh).
set -euo pipefail

HOST=${HOST:-}
KEY=${KEY:-${TECHO5_SSH_KEY:-$HOME/.ssh/id_ed25519}}
INPUTS=${TECHO5_INPUTS:-$(cd "$(dirname "$0")/../.." && pwd)/inputs}
# The zone a unit starts on before Home Assistant tells it its own (feature/timezone); images meant
# for anyone keep UTC.
TZ_NAME=${TZ_NAME:-UTC}
GO=${GO:-go}
VERSION=${VERSION:-}
WSL_DISTRO=${WSL_DISTRO:-Ubuntu}
# Another device: BUILD_TAGS (the daemon's, e.g. spot) and DEVICE_OVERLAY (files laid over tools/linux/rootfs,
# with etc/techo5/device.conf). VENDOR_TGZ puts a LineageOS vendor/ tarball into the image, for
# development only: images are published without it, and a unit mounts its own (etc/techo5/boot.sh).
BUILD_TAGS=${BUILD_TAGS:-}
VENDOR_TGZ=${VENDOR_TGZ:-}
MAINLINE_BUNDLE=${MAINLINE_BUNDLE:-}
DEVICE_OVERLAY=${DEVICE_OVERLAY:-}
INSTALL=; REBOOT=; ONDEVICE=; KEEP=
while [ $# -gt 0 ]; do
	case "$1" in
	--out) KEEP=$2; shift 2;;
	--install) INSTALL=1; shift;;
	--reboot) REBOOT=1; shift;;
	--version) VERSION=$2; shift 2;;
	--on-device) ONDEVICE=1; shift;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done

[ -n "$HOST" ] || [ -n "$KEEP" ] || { echo "HOST is not set: HOST=<the device's address> $0 ..., or $0 --out rootfs.tar.gz" >&2; exit 1; }
[ -z "$KEEP" ] || [ -z "$ONDEVICE" ] || { echo "--out builds on this computer; it does not go with --on-device" >&2; exit 1; }
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
[ -n "$VERSION" ] || VERSION="dev-$(git -C "$ROOT" rev-parse --short HEAD)"
STAGE=$ROOT/bin/rootfs-stage
SSH=(ssh -i "$KEY" -o StrictHostKeyChecking=no "root@$HOST")

echo "== building for armv7 ($VERSION)"
export GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0
pkg=github.com/HuskerMinion/techo5/echod/internal/layout
commit=$(git -C "$ROOT" rev-parse --short HEAD)
if [ -n "${PREBUILT_DAEMON:-}" ]; then
	# Only what CI built from this release's own tag (spot-vX.Y.Z for BUILD_TAGS=spot): anything else
	# carries another version, and Home Assistant would offer the update forever after it installed.
	tag=${BUILD_TAGS:+$BUILD_TAGS-}$VERSION
	gh attestation verify "$PREBUILT_DAEMON" --repo HuskerMinion/techo5 --source-ref "refs/tags/$tag" >/dev/null \
		|| { echo "$PREBUILT_DAEMON is not attested as built by CI from tag $tag" >&2; exit 1; }
	cp "$PREBUILT_DAEMON" "$ROOT/bin/echod-arm"
else
	(cd "$ROOT/echod" && "$GO" build -tags "$BUILD_TAGS" -trimpath -ldflags "-s -w -X $pkg.Version=$VERSION -X $pkg.GitCommit=$commit" -o "$ROOT/bin/echod-arm" ./cmd/echod)
fi
for c in fbprobe audioprobe rebootto btbridge; do
	(cd "$ROOT" && "$GO" build -trimpath -ldflags "-s -w" -o "$ROOT/bin/$c-arm" "./cmd/$c")
done
unset GOOS GOARCH GOARM CGO_ENABLED

echo "== staging"
rm -rf "$STAGE"
mkdir -p "$STAGE/bin" "$STAGE/tools" "$STAGE/overlay" "$STAGE/inputs/apks312"
cp "$ROOT/bin/echod-arm" "$STAGE/bin/techo5"
for c in fbprobe audioprobe rebootto btbridge; do cp "$ROOT/bin/$c-arm" "$STAGE/bin/$c"; done
# The WebRTC echo canceller helper is C++ built separately (tools/linux/build-aec.sh in WSL); ship it when it is there.
[ -e "$ROOT/bin/techo5-aec-arm" ] && cp "$ROOT/bin/techo5-aec-arm" "$STAGE/bin/techo5-aec"
cp "$ROOT/tools/linux/slotctl" "$ROOT/tools/linux/techo5-lib.sh" "$ROOT/tools/linux/mkrootfs.sh" "$ROOT/tools/linux/packages-rootfs.txt" "$STAGE/tools/"
cp -r "$ROOT/tools/linux/rootfs/." "$STAGE/overlay/"
[ -n "$DEVICE_OVERLAY" ] && cp -r "$DEVICE_OVERLAY/." "$STAGE/overlay/"
cp "$INPUTS/alpine-minirootfs-3.24.2-armv7.tar.gz" "$STAGE/inputs/"
[ -n "$MAINLINE_BUNDLE" ] && cp "$MAINLINE_BUNDLE" "$STAGE/inputs/kernel.tar.gz"
[ -n "$VENDOR_TGZ" ] && cp "$VENDOR_TGZ" "$STAGE/inputs/vendor.tar.gz"
cp "$INPUTS"/apks312/wpa_supplicant-2.9-*.apk "$INPUTS"/apks312/libssl1.1-*.apk "$INPUTS"/apks312/libcrypto1.1-*.apk "$STAGE/inputs/apks312/"
# scripts must reach the device with LF endings whatever the checkout did
# (perl rather than sed -i, which macOS's sed spells differently)
for f in "$STAGE"/tools/*.sh "$STAGE"/tools/slotctl "$STAGE"/tools/*.txt; do perl -pi -e 's/\r$//' "$f"; done
find "$STAGE/overlay" -type f -exec perl -pi -e 's/\r$//' {} +
# Wake word models ship in the image so a fresh unit answers to its default word; boot.sh copies
# them into the state directory when it is empty. They go in after the line endings are fixed:
# the models are binary, and stripping a CR before every LF byte corrupted three of four (v0.2.5).
if [ -d "$INPUTS/models" ]; then
	mkdir -p "$STAGE/overlay/usr/share/techo5/models"
	cp "$INPUTS"/models/*.tflite "$INPUTS"/models/*.json "$STAGE/overlay/usr/share/techo5/models/"
	for m in "$INPUTS"/models/*.tflite; do
		cmp -s "$m" "$STAGE/overlay/usr/share/techo5/models/$(basename "$m")" || { echo "model copy differs: $m" >&2; exit 1; }
	done
fi

OUT=/data/techo5-linux/techo5-rootfs-$VERSION.tar.gz
REMOTE=/data/techo5-linux/build
host_build=
case "$(uname -s)" in
Linux)
	[ -z "$ONDEVICE" ] && [ -e /proc/sys/fs/binfmt_misc/qemu-arm ] && [ -x "$HOME/apk/apk.static" ] && host_build=linux;;
MINGW*|MSYS*|CYGWIN*)
	# Git Bash on Windows: the build runs in WSL.
	[ -z "$ONDEVICE" ] && command -v wsl.exe >/dev/null 2>&1 \
		&& wsl.exe -d "$WSL_DISTRO" -- bash -c 'test -e /proc/sys/fs/binfmt_misc/qemu-arm -a -x "$HOME/apk/apk.static"' 2>/dev/null \
		&& host_build=wsl;;
esac
if [ -n "$KEEP" ] && [ -z "$host_build" ]; then
	echo "--out needs a local build: on Linux install qemu-user-static and binfmt-support and put apk.static at ~/apk/apk.static;" >&2
	echo "on Windows the same inside WSL. Elsewhere set HOST and build on the unit instead." >&2
	exit 1
fi

if [ "$host_build" = linux ]; then
	echo "== building the rootfs here"
	bash "$ROOT/tools/linux/wsl-build.sh" "$STAGE" "$VERSION" "$TZ_NAME" >/dev/null
	tarball=$HOME/techo5-build/rootfs.tar.gz
	[ -s "$tarball" ] || { echo "the build failed" >&2; exit 1; }
	if [ -n "$KEEP" ]; then
		mkdir -p "$(dirname "$KEEP")"; cp "$tarball" "$KEEP"; echo "built: $KEEP"; exit 0
	fi
	echo "== shipping the rootfs to $HOST"
	"${SSH[@]}" "mkdir -p /data/techo5-linux"
	scp -O -i "$KEY" -o StrictHostKeyChecking=no "$tarball" "root@$HOST:$OUT"
elif [ -n "$host_build" ]; then
	echo "== building the rootfs in WSL ($WSL_DISTRO)"
	# Git Bash's /e/... is WSL's /mnt/e/...; no Windows path crosses over (wsl.exe eats backslashes).
	stage_wsl=$(cygpath -u "$STAGE" | sed 's|^/\([a-zA-Z]\)/|/mnt/\L\1/|')
	# The build itself lives in tools/linux/wsl-build.sh: one script, no quoting across wsl.exe.
	helper=$(cygpath -u "$ROOT/tools/linux/wsl-build.sh" | sed 's|^/\([a-zA-Z]\)/|/mnt/\L\1/|')
	tarball=$(MSYS_NO_PATHCONV=1 wsl.exe -d "$WSL_DISTRO" -- bash "$helper" "$stage_wsl" "$VERSION" "$TZ_NAME" | tr -d '\r' | tail -1)
	[ -n "$tarball" ] || { echo "WSL build failed" >&2; exit 1; }
	if [ -n "$KEEP" ]; then
		mkdir -p "$(dirname "$KEEP")"; cp "$(printf '%s' "$tarball" | tr '\\' '/')" "$KEEP"; echo "built: $KEEP"; exit 0
	fi
	echo "== shipping the rootfs to $HOST"
	"${SSH[@]}" "mkdir -p /data/techo5-linux"
	scp -O -i "$KEY" -o StrictHostKeyChecking=no "$tarball" "root@$HOST:$OUT"
else
	echo "== shipping to $HOST"
	"${SSH[@]}" "rm -rf $REMOTE/in && mkdir -p $REMOTE/in"
	tar -czf - -C "$STAGE" . | "${SSH[@]}" "tar -xzf - -C $REMOTE/in"

	echo "== building the rootfs on the device"
	"${SSH[@]}" "sh $REMOTE/in/tools/mkrootfs.sh -i $REMOTE/in -o $OUT -w $REMOTE -V $VERSION -z $TZ_NAME"
fi

if [ -n "$INSTALL" ]; then
	echo "== installing into the inactive slot"
	"${SSH[@]}" "PATH=/usr/local/sbin:\$PATH; slotctl install $OUT && slotctl status"
	if [ -n "$REBOOT" ]; then
		echo "== rebooting"
		"${SSH[@]}" "sync; reboot" || true
	fi
else
	echo "built: $OUT (on the device). Install with: slotctl install $OUT"
fi
