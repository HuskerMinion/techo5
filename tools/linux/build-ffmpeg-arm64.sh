#!/bin/bash
# build-ffmpeg-arm64.sh — the video player's ffmpeg (build-ffmpeg.sh) for the boards with a 64-bit kernel:
# the same version, configure flags and RTSP patch, built for aarch64 and linked fully static, with more
# in it:
#   - the H.265, VP8 and VP9 decoders and their parsers. Only the Echo Show 8 2nd gen (athena) and the
#     Skylight Calendar get this build, and even there not at every size: echod finds them in the binary
#     (feature/video, proc.go) and holds each board to what its cores decode in time (cores_show.go).
#     Every other device keeps build-ffmpeg.sh's set.
#   - the Matroska muxer and the extract_extradata filter, which carry a stream's H.264 untouched to a
#     hardware decoder, its parameter sets found in an MPEG-TS stream too.
#
#   tools/linux/build-ffmpeg-arm64.sh [-o bin/techo5-ffmpeg-arm64]
#
# Every image runs a 32-bit (armv7) userspace, athena's and the Skylight's too, on their 64-bit kernels.
# So nothing of the rootfs can be linked in: musl, OpenSSL and zlib are all static here, and the binary
# needs nothing but an aarch64 kernel. The image carries it beside the 32-bit build as techo5-ffmpeg64
# (deploy-rootfs.sh), and echod runs it only on a 64-bit kernel; an athena still on Amazon's 32-bit
# kernel keeps the 32-bit one.
#
# A cross build, in WSL or on x86_64 Linux, without QEMU: an Alpine x86_64 root (~/alpine-x86_64-ffmpeg64)
# with clang and lld, and in it an Alpine aarch64 root (/sysroot) that only supplies headers and static
# libraries. Needs what build-ffmpeg.sh needs (uidmap, ~/apk/apk.static from tools/fetch-inputs.py). The
# Alpine base below and ffmpeg's tarball (build-ffmpeg.sh's) are checked against their SHA-256; apk
# checks every package against Alpine's signing keys. The first run makes the roots (about a minute);
# later runs reuse them and rebuild only what changed. OpenSSL is linked in, so a fix to it reaches the
# devices only through a rebuild of this binary: the root is made again with ROOTS=fresh.
set -euo pipefail
ROOT=${TECHO5_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
export TECHO5_ROOT=$ROOT
OUT=$ROOT/bin/techo5-ffmpeg-arm64
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done
ALPINE=3.24.2
MINI=alpine-minirootfs-$ALPINE-x86_64.tar.gz
MINI_SHA256=c5ca053cfe1d85c5b96dff8b9bc57045f7f184a30ffb6b65776409ca90388677
REPO=https://dl-cdn.alpinelinux.org/alpine/v${ALPINE%.*}
SDK=${CROSS_SDK:-$HOME/alpine-x86_64-ffmpeg64}
APK=$HOME/apk/apk.static

B=$ROOT/tools/linux/build-ffmpeg.sh
FFVER=$(sed -n 's/^FFVER=//p' "$B")
SHA256=$(sed -n 's/^SHA256=//p' "$B")
# build-ffmpeg.sh's configure flags, for aarch64 (NEON is part of the architecture there)
FLAGS=$(sed -n '/^FLAGS="/,/"$/p' "$B" | tr -d '\\\n\t"' | sed -e 's/^FLAGS=//' \
	-e 's/--arch=arm --cpu=cortex-a53 --enable-neon/--arch=aarch64 --cpu=cortex-a53/')
# ...and the 64-bit build's own (above); configure adds each list to the ones before it.
FLAGS="$FLAGS --enable-decoder=hevc,vp8,vp9 --enable-parser=hevc,vp8,vp9 --enable-muxer=matroska --enable-bsf=extract_extradata"
case $FLAGS in *--arch=aarch64*) ;; *) echo "build-ffmpeg.sh's flags are not the ones expected" >&2; exit 1;; esac
# The cross build: clang for aarch64 against /sysroot, everything static.
FLAGS="$FLAGS --enable-cross-compile --target-os=linux --sysroot=/sysroot --cc=aarch64-cc --ld=aarch64-cc --host-cc=gcc \
--nm=llvm-nm --ar=llvm-ar --ranlib=llvm-ranlib --strip=llvm-strip --pkg-config=pkg-config --pkg-config-flags=--static --extra-ldflags=-static"
# build-ffmpeg.sh's RTSP patch, as it stands there
PATCH=$(sed -n "s/^\tperl -0pi -e '\(.*\)' \"\$RTSPC\"$/\1/p" "$B")
[ -n "$PATCH" ] || { echo "build-ffmpeg.sh's RTSP patch not found" >&2; exit 1; }

if [ -z "${TECHO5_IN_NS:-}" ]; then
	exec unshare -Ur --map-auto env TECHO5_IN_NS=1 bash "$0" -o "$OUT"
fi

[ "${ROOTS:-}" = fresh ] && rm -rf "$SDK"
if [ ! -x "$SDK/usr/bin/ld.lld" ] || [ ! -e "$SDK/sysroot/usr/lib/libssl.a" ]; then
	echo "== making the x86_64 root at $SDK, with the aarch64 sysroot in it"
	rm -rf "$SDK"; mkdir -p "$SDK"
	T=$HOME/.cache/techo5/$MINI
	mkdir -p "$(dirname "$T")"
	[ -f "$T" ] || { curl -fsSL "$REPO/releases/x86_64/$MINI" -o "$T.part"; mv "$T.part" "$T"; }
	echo "$MINI_SHA256  $T" | sha256sum -c - >/dev/null || { echo "$T is not the Alpine base this script was written for" >&2; exit 1; }
	tar -xzf "$T" -C "$SDK"
	cp /etc/resolv.conf "$SDK/etc/resolv.conf"
	"$APK" --root "$SDK" --arch x86_64 --no-cache add build-base clang lld llvm pkgconf linux-headers
	"$APK" --root "$SDK/sysroot" --arch aarch64 --initdb --no-scripts --no-cache \
		--keys-dir "$SDK/usr/share/apk/keys/aarch64" --repositories-file "$SDK/etc/apk/repositories" \
		add musl-dev gcc linux-headers openssl-dev openssl-libs-static zlib-dev zlib-static
	cat > "$SDK/usr/local/bin/aarch64-cc" <<'CC'
#!/bin/sh
exec clang --target=aarch64-alpine-linux-musl --sysroot=/sysroot -fuse-ld=lld -Wno-unused-command-line-argument "$@"
CC
	chmod 755 "$SDK/usr/local/bin/aarch64-cc"
fi
echo "== in it: $("$APK" --root "$SDK/sysroot" --arch aarch64 info -v 2>/dev/null | grep -E '^(musl|openssl-libs-static|zlib-static)-[0-9]' | tr '\n' ' ')"

mkdir -p "$SDK/build"
TARBALL=$SDK/build/ffmpeg-$FFVER.tar.xz
if [ ! -f "$TARBALL" ]; then
	echo "== fetching ffmpeg $FFVER"
	curl -fsSL "https://ffmpeg.org/releases/ffmpeg-$FFVER.tar.xz" -o "$TARBALL.part"
	mv "$TARBALL.part" "$TARBALL"
fi
echo "$SHA256  $TARBALL" | sha256sum -c - >/dev/null || { echo "the tarball is not the ffmpeg $FFVER build-ffmpeg.sh was written for" >&2; exit 1; }
SRC=/build/techo5-ffmpeg64-$FFVER
if [ ! -d "$SDK$SRC" ]; then
	mkdir -p "$SDK$SRC"
	tar -xJf "$TARBALL" -C "$SDK$SRC" --strip-components=1
fi
RTSPC=$SDK$SRC/libavformat/rtsp.c
grep -q 'techo5: the camera offers a Basic login, which is ignored' "$RTSPC" || perl -0pi -e "$PATCH" "$RTSPC"
grep -q 'techo5: the camera offers a Basic login, which is ignored' "$RTSPC" || { echo "the RTSP Basic patch did not apply to $RTSPC" >&2; exit 1; }

# At most 8 jobs, whatever the computer has.
J=$(nproc); [ "$J" -le 8 ] || J=8
cat > "$SDK/build/techo5-ffmpeg64.sh" <<INSIDE
#!/bin/sh
set -e
cd $SRC
export PKG_CONFIG_LIBDIR=/sysroot/usr/lib/pkgconfig PKG_CONFIG_SYSROOT_DIR=/sysroot
if [ ! -f ffbuild/config.mak ] || [ "\$(cat .techo5-flags 2>/dev/null)" != "$FLAGS" ]; then
	./configure --prefix=/build/inst $FLAGS >/dev/null
	echo "$FLAGS" > .techo5-flags
fi
make -s -j$J ffmpeg
llvm-strip ffmpeg
# Static and aarch64, or not shipped: no interpreter, no shared library.
llvm-readelf -h ffmpeg | grep -q 'Machine: *AArch64'
if llvm-readelf -l ffmpeg | grep -q INTERP || llvm-readelf -d ffmpeg | grep -q NEEDED; then exit 1; fi
INSIDE
echo "== cross building"
start=$(date +%s)
chroot "$SDK" /usr/bin/env PATH=/usr/local/bin:/usr/bin:/bin /bin/sh /build/techo5-ffmpeg64.sh || { echo "the build failed, or what it made is not a static aarch64 binary" >&2; exit 1; }
echo "== built in $(( $(date +%s) - start )) s"
# What echod looks for to know the decoders are in it (feature/video, proc.go, camera.go).
for m in 'Two slices reporting being the first in the same frame' 'Discarding interframe without a prior keyframe!' \
	'Invalid compressed header size' 'techo5: the camera offers a Basic login, which is ignored'; do
	grep -qF "$m" "$SDK$SRC/ffmpeg" || { echo "the binary lacks: $m" >&2; exit 1; }
done
mkdir -p "$(dirname "$OUT")"
cp "$SDK$SRC/ffmpeg" "$OUT"
echo "built: $OUT"
ls -la "$OUT"
command -v file >/dev/null && file "$OUT"
sha256sum "$OUT"
