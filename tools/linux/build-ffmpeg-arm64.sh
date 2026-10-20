#!/bin/sh
# build-ffmpeg-arm64.sh — the video player's ffmpeg (build-ffmpeg.sh) for the 64-bit userspace: the same
# version, configure flags and RTSP patch, built for aarch64 in an Alpine container, with more in it:
#   - the H.265, VP8 and VP9 decoders and their parsers. Only the boards that run a 64-bit userspace (the
#     2nd gen Show 5 and Show 8 on a mainline kernel) are fast enough for them, and even there not at
#     every size: echod finds them in the binary (feature/video, proc.go) and holds each board to what
#     its cores decode in time (cores_show.go). Every armv7 device keeps build-ffmpeg.sh's set.
#   - the Matroska muxer and the extract_extradata filter, which carry a stream's H.264 untouched to a
#     hardware decoder (feature/video, hw.go), its parameter sets found in an MPEG-TS stream too.
#
#   tools/linux/build-ffmpeg-arm64.sh [-o bin/techo5-ffmpeg-arm64]
#
# Needs Docker with arm64 containers (native on Apple silicon, QEMU elsewhere). The tarball and the
# build tree are kept in the volume techo5-ffmpeg-arm64, so later runs rebuild only what changed.
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT=$ROOT/bin/techo5-ffmpeg-arm64
[ "${1:-}" = -o ] && OUT=$2
B=$ROOT/tools/linux/build-ffmpeg.sh
FFVER=$(sed -n 's/^FFVER=//p' "$B")
SHA256=$(sed -n 's/^SHA256=//p' "$B")
# build-ffmpeg.sh's configure flags, for aarch64 (NEON is part of the architecture there)
FLAGS=$(sed -n '/^FLAGS="/,/"$/p' "$B" | tr -d '\\\n\t"' | sed -e 's/^FLAGS=//' \
	-e 's/--arch=arm --cpu=cortex-a53 --enable-neon/--arch=aarch64 --cpu=cortex-a53/')
# ...and the 64-bit build's own (above); configure adds each list to the ones before it.
FLAGS="$FLAGS --enable-decoder=hevc,vp8,vp9 --enable-parser=hevc,vp8,vp9 --enable-muxer=matroska --enable-bsf=extract_extradata"
case $FLAGS in *--arch=aarch64*) ;; *) echo "build-ffmpeg.sh's flags are not the ones expected" >&2; exit 1;; esac
# build-ffmpeg.sh's RTSP patch, as it stands there
PATCH=$(sed -n "s/^\tperl -0pi -e '\(.*\)' \"\$RTSPC\"$/\1/p" "$B")
[ -n "$PATCH" ] || { echo "build-ffmpeg.sh's RTSP patch not found" >&2; exit 1; }
mkdir -p "$(dirname "$OUT")"
# alpine:3.24 is shared with the armv7 builds, and a tag pulled for arm/v7 stays arm/v7 here whatever
# --platform says: take the arm64 image first, and refuse a container that is not aarch64.
docker pull -q --platform linux/arm64 alpine:3.24 >/dev/null
docker run --rm --platform linux/arm64 -v techo5-ffmpeg-arm64:/build -v "$(dirname "$OUT"):/out" \
	-e FFVER="$FFVER" -e SHA256="$SHA256" -e FLAGS="$FLAGS" -e PATCH="$PATCH" -e NAME="$(basename "$OUT")" \
	alpine:3.24 sh -euc '
[ "$(uname -m)" = aarch64 ] || { echo "the container is $(uname -m), not aarch64" >&2; exit 1; }
apk add -q --no-cache build-base pkgconf linux-headers openssl-dev zlib-dev perl curl
cd /build
T=ffmpeg-$FFVER.tar.xz
[ -f $T ] || { curl -fsSL https://ffmpeg.org/releases/$T -o $T.part; mv $T.part $T; }
echo "$SHA256  $T" | sha256sum -c - >/dev/null
S=/build/src-$FFVER
[ -d $S ] || { mkdir -p $S; tar -xJf $T -C $S --strip-components=1; }
cd $S
grep -q "techo5: the camera offers a Basic login, which is ignored" libavformat/rtsp.c || perl -0pi -e "$PATCH" libavformat/rtsp.c
grep -q "techo5: the camera offers a Basic login, which is ignored" libavformat/rtsp.c
if [ ! -f ffbuild/config.mak ] || [ "$(cat .techo5-flags 2>/dev/null)" != "$FLAGS" ]; then
	./configure --prefix=/build/inst $FLAGS --extra-ldflags=-static-libgcc >/dev/null
	echo "$FLAGS" > .techo5-flags
fi
make -s -j$(nproc) ffmpeg
strip ffmpeg
cp ffmpeg /out/$NAME
'
echo "built: $OUT"
ls -la "$OUT"
shasum -a 256 "$OUT"
