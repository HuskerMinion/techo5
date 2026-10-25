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
DEVICE_OVERLAY=${DEVICE_OVERLAY:-}
# KERNEL_IMAGE (athena, Skylight): the released boot image to carry in the rootfs; once the slot is good it
# goes to the kernel's partition (echod/internal/update/kernel). athena's is tools/linux/athena/kernel/build.sh's
# boot-athena.img, its amp tuning slot empty, and goes to the boot partition with the unit's own tuning; the
# Skylight's is tools/linux/skylight/kernel/build.sh's boot-skylight.img, and goes to recovery as it is.
# Without it the rootfs leaves the unit's kernel as it is.
KERNEL_IMAGE=${KERNEL_IMAGE:-}
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
# A tarball kept with --out is one to publish or install from: without the wake word models, a unit
# that starts from it answers to no word at all. Sending a build to a unit of one's own may skip them.
if [ -n "$KEEP" ] && ! ls "$INPUTS"/models/*.tflite >/dev/null 2>&1; then
	echo "no wake word models in $INPUTS/models: fetch them (tools/fetch-inputs.py) before building a rootfs with --out" >&2
	exit 1
fi
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
# The version is stamped into the daemon and reported to Home Assistant, which offers an update
# whenever it differs from a release's, without asking which is newer. So the default has to rank:
# 0.0.0-dev.<commit>.<time> sits below every release, where "dev-<commit>-<time>" ranked nowhere at
# all and left the update card on for good (update.ValidVersion).
[ -n "$VERSION" ] || VERSION="0.0.0-dev.$(git -C "$ROOT" rev-parse --short HEAD).$(date +%Y%m%d%H%M)"
STAGE=$ROOT/bin/rootfs-stage
SSH=(ssh -i "$KEY" -o StrictHostKeyChecking=no "root@$HOST")

echo "== building for armv7 ($VERSION)"
export GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0
pkg=github.com/HuskerMinion/techo5/echod/internal/layout
commit=$(git -C "$ROOT" rev-parse --short HEAD)
# BUILD_DATE fixes the date stamped in the daemon, so two images built from one commit (the Show's and
# the Skylight's, made in one run) carry the same daemon, byte for byte.
date=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
if [ -n "${PREBUILT_DAEMON:-}" ]; then
	# Only what CI built from this release's own tag (spot-vX.Y.Z for BUILD_TAGS=spot): anything else
	# carries another version, and Home Assistant would offer the update forever after it installed.
	tag=${BUILD_TAGS:+$BUILD_TAGS-}$VERSION
	gh attestation verify "$PREBUILT_DAEMON" --repo HuskerMinion/techo5 --source-ref "refs/tags/$tag" >/dev/null \
		|| { echo "$PREBUILT_DAEMON is not attested as built by CI from tag $tag" >&2; exit 1; }
	cp "$PREBUILT_DAEMON" "$ROOT/bin/echod-arm"
else
	(cd "$ROOT/echod" && "$GO" build -tags "$BUILD_TAGS" -trimpath -ldflags "-s -w -X $pkg.Version=$VERSION -X $pkg.GitCommit=$commit -X $pkg.BuildDate=$date" -o "$ROOT/bin/echod-arm" ./cmd/echod)
fi
# audioprobe lives in the daemon's module so it shares the daemon's ALSA code rather than a copy of it.
for c in fbprobe audioprobe rebootto btbridge; do
	m=$ROOT; [ -d "$ROOT/echod/cmd/$c" ] && m=$ROOT/echod
	(cd "$m" && "$GO" build -trimpath -ldflags "-s -w" -o "$ROOT/bin/$c-arm" "./cmd/$c")
done
unset GOOS GOARCH GOARM CGO_ENABLED

echo "== staging"
rm -rf "$STAGE"
mkdir -p "$STAGE/bin" "$STAGE/tools" "$STAGE/overlay" "$STAGE/inputs/apks312"
cp "$ROOT/bin/echod-arm" "$STAGE/bin/techo5"
for c in fbprobe audioprobe rebootto btbridge; do cp "$ROOT/bin/$c-arm" "$STAGE/bin/$c"; done
# The WebRTC echo canceller helper is C++ built separately (tools/linux/build-aec.sh in WSL); ship it when it is there.
[ -e "$ROOT/bin/techo5-aec-arm" ] && cp "$ROOT/bin/techo5-aec-arm" "$STAGE/bin/techo5-aec"
# The Spotify Connect receiver (librespot, Rust) is built separately too (tools/linux/build-librespot.sh in WSL).
[ -e "$ROOT/bin/techo5-librespot-arm" ] && cp "$ROOT/bin/techo5-librespot-arm" "$STAGE/bin/techo5-librespot"
# AirPlay 2's receiver and nqptp, the clock it needs (tools/linux/build-shairport-sync.sh, Linux or WSL): in
# every image, which the daemon runs where it can and keeps the classic receiver where it can't
# (feature/streaming). Not optional: an image without them would quietly be classic AirPlay only.
for b in techo5-shairport-sync techo5-nqptp; do
	[ -e "$ROOT/bin/$b-arm" ] || { echo "bin/$b-arm is missing: build it with tools/linux/build-shairport-sync.sh" >&2; exit 1; }
	cp "$ROOT/bin/$b-arm" "$STAGE/bin/$b"
	echo "== $b: sha256 $(sha256sum "$ROOT/bin/$b-arm" | cut -d' ' -f1)"
done
# The video player's decoder (ffmpeg, C, tools/linux/build-ffmpeg.sh in WSL), for the Show's and the Spot's video
# player (feature/video). Not optional: an image without it offers Video and plays nothing, so a build that
# lacks it stops here rather than ship that. Its SHA-256 is said, to check against the one build-ffmpeg.sh printed.
[ -e "$ROOT/bin/techo5-ffmpeg-arm" ] || { echo "bin/techo5-ffmpeg-arm is missing: build it with tools/linux/build-ffmpeg.sh (in WSL)" >&2; exit 1; }
cp "$ROOT/bin/techo5-ffmpeg-arm" "$STAGE/bin/techo5-ffmpeg"
echo "== ffmpeg: sha256 $(sha256sum "$ROOT/bin/techo5-ffmpeg-arm" | cut -d' ' -f1)"
# Tailscale's daemon (tools/linux/build-tailscaled.sh), for reaching the device from away (feature/tailscale,
# docs/tailscale.md): off until somebody gives it a key. Every image this script builds carries it, and its
# license (BSD-3-Clause) beside it. One static arm64 build serves them all, on their 64-bit kernels; on a
# 32-bit one (an athena on Amazon's kernel) the daemon doesn't offer it (feature/tailscale Available).
TS=$ROOT/bin/tailscaled-arm64
[ -e "$TS" ] && [ -e "$TS.LICENSE" ] || { echo "bin/tailscaled-arm64 (and its .LICENSE) is missing: build it with tools/linux/build-tailscaled.sh" >&2; exit 1; }
[ "$(od -An -tx1 -j4 -N1 "$TS" | tr -d ' ')" = 02 ] && [ "$(od -An -tu2 -j18 -N2 "$TS" | tr -d ' ')" = 183 ] && ! grep -q 'ld-musl-\|ld-linux-' "$TS" 	|| { echo "bin/tailscaled-arm64 is not a static aarch64 binary: build it with tools/linux/build-tailscaled.sh" >&2; exit 1; }
cp "$TS" "$STAGE/bin/tailscaled"
echo "== tailscaled: sha256 $(sha256sum "$TS" | cut -d' ' -f1)"
# athena and the Skylight carry the 64-bit build beside it (tools/linux/build-ffmpeg-arm64.sh in WSL: static,
# with H.265, VP8 and VP9), which the daemon runs on their 64-bit kernels; an athena on Amazon's 32-bit
# kernel keeps the one above. Not optional for them either, and checked for what it must be.
# No overlay (the Show's and the Spot's builds) is no board: sed's failure must not end the script (pipefail).
board=$( (sed -n 's/^T5_BOARD=//p' "${DEVICE_OVERLAY:-/nonexistent}/etc/techo5/device.conf" 2>/dev/null || true) | tail -1)
case "$board" in
athena|skylight)
	F64=$ROOT/bin/techo5-ffmpeg-arm64
	[ -e "$F64" ] || { echo "bin/techo5-ffmpeg-arm64 is missing: build it with tools/linux/build-ffmpeg-arm64.sh (in WSL); the $board image carries it" >&2; exit 1; }
	# An ELF64 for AArch64 (e_machine 183) with no interpreter: nothing of the 32-bit userspace can be linked.
	[ "$(od -An -tx1 -j4 -N1 "$F64" | tr -d ' ')" = 02 ] && [ "$(od -An -tu2 -j18 -N2 "$F64" | tr -d ' ')" = 183 ] 		&& ! grep -q 'ld-musl-\|ld-linux-' "$F64" 		|| { echo "bin/techo5-ffmpeg-arm64 is not a static aarch64 binary: build it with tools/linux/build-ffmpeg-arm64.sh" >&2; exit 1; }
	cp "$F64" "$STAGE/bin/techo5-ffmpeg64"
	echo "== ffmpeg64: sha256 $(sha256sum "$F64" | cut -d' ' -f1)"
	;;
esac
cp "$ROOT/tools/linux/slotctl" "$ROOT/tools/linux/techo5-lib.sh" "$ROOT/tools/linux/mkrootfs.sh" "$ROOT/tools/linux/packages-rootfs.txt" "$STAGE/tools/"
# EXTRA_PACKAGES: Alpine packages a device needs on top of the list (athena: busybox-extras, for udhcpd).
[ -n "${EXTRA_PACKAGES:-}" ] && printf '%s\n' $EXTRA_PACKAGES >> "$STAGE/tools/packages-rootfs.txt"
cp -r "$ROOT/tools/linux/rootfs/." "$STAGE/overlay/"
[ -n "$DEVICE_OVERLAY" ] && cp -r "$DEVICE_OVERLAY/." "$STAGE/overlay/"
mkdir -p "$STAGE/overlay/usr/share/licenses/tailscale" && cp "$TS.LICENSE" "$STAGE/overlay/usr/share/licenses/tailscale/LICENSE"
cp "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz "$STAGE/inputs/"
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

# The kernel goes in after the line endings are fixed too, and is checked as a whole released image.
if [ -n "$KERNEL_IMAGE" ]; then
	kboard=$( (sed -n 's/^T5_BOARD=//p' "$STAGE/overlay/etc/techo5/device.conf" 2>/dev/null || true) | tail -1)
	case "$kboard" in
	athena|skylight) ;;
	*) echo "KERNEL_IMAGE is for athena or the Skylight: DEVICE_OVERLAY=tools/linux/athena/overlay or tools/linux/skylight/overlay" >&2; exit 1;;
	esac
	PY=$(command -v python3 || command -v python) || { echo "KERNEL_IMAGE needs python to check the image" >&2; exit 1; }
	"$PY" "$ROOT/tools/linux/$kboard/kernel/check-boot.py" "$KERNEL_IMAGE" || { echo "KERNEL_IMAGE is not a released $kboard boot image" >&2; exit 1; }
	mkdir -p "$STAGE/overlay/usr/share/techo5/kernel"
	cp "$KERNEL_IMAGE" "$STAGE/overlay/usr/share/techo5/kernel/boot-$kboard.img"
	cmp -s "$KERNEL_IMAGE" "$STAGE/overlay/usr/share/techo5/kernel/boot-$kboard.img" || { echo "kernel copy differs" >&2; exit 1; }
	sum=$(sha256sum "$KERNEL_IMAGE" | cut -d' ' -f1)
	echo "$sum  boot-$kboard.img" > "$STAGE/overlay/usr/share/techo5/kernel/boot-$kboard.img.sha256"
	echo "== kernel: sha256 $sum"
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
