#!/bin/sh
# build-gst-arm64.sh — the GStreamer the video player drives the Show 8's hardware decoder with
# (feature/video, hw.go): Alpine 3.24's GStreamer, cut down to the six plugins the player's pipeline
# uses and the libraries they need, for the 64-bit userspace.
#
#   tools/linux/build-gst-arm64.sh [-o bin/techo5-gst-arm64.tar.gz]
#
# The pipeline takes the H.264 that ffmpeg copies out of a stream in Matroska, and gives back the
# panel's pixels: matroskademux, h264parse (videoparsersbad), the stateless decoder (v4l2codecs:
# v4l2slh264dec, the MT8183's decoder), the MDP3 scaler and converter (video4linux2: v4l2convert),
# videorate, and queue and fdsrc/fdsink (coreelements). Nothing else, so the decoder's user has
# nothing more to reach. Everything goes under /usr/local/lib/techo5-gst (bin/gst-launch-1.0, lib/,
# plugins/) and only musl comes from the rootfs; the player points GStreamer there.
#
# Needs Docker with arm64 containers (native on Apple silicon). The packages are Alpine's signed
# ones, at the versions below.
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT=$ROOT/bin/techo5-gst-arm64.tar.gz
[ "${1:-}" = -o ] && OUT=$2
VER=1.28.3-r0
mkdir -p "$(dirname "$OUT")"
docker run --rm --platform linux/arm64 -v "$(dirname "$OUT"):/out" -e VER=$VER -e NAME="$(basename "$OUT")" \
	alpine:3.24 sh -euc '
apk add -q --no-cache scanelf tar "gstreamer=$VER" "gstreamer-tools=$VER" "gst-plugins-base=$VER" \
	"gst-plugins-good=$VER" "gst-plugins-bad=$VER"
D=/stage/usr/local/lib/techo5-gst
mkdir -p $D/bin $D/lib $D/plugins
P=/usr/lib/gstreamer-1.0
for p in coreelements matroska videoparsersbad v4l2codecs video4linux2 videorate; do
	cp $P/libgst$p.so $D/plugins/
done
cp /usr/bin/gst-launch-1.0 $D/bin/
# Every library they load, by name, until nothing new turns up (musl is the rootfs own).
need() { scanelf -qnF "%n" "$@" | tr "," "\n" | grep -v "^libc\.musl" | sort -u; }
todo=$(need $D/plugins/*.so $D/bin/gst-launch-1.0)
while [ -n "$todo" ]; do
	new=
	for l in $todo; do
		[ -e $D/lib/$l ] && continue
		f=$(ls /usr/lib/$l /lib/$l 2>/dev/null | head -1)
		[ -n "$f" ] || { echo "no $l" >&2; exit 1; }
		cp -L "$f" $D/lib/$l
		new="$new $D/lib/$l"
	done
	todo=; [ -n "$new" ] && todo=$(need $new)
done
strip $D/lib/*.so* $D/plugins/*.so $D/bin/gst-launch-1.0 2>/dev/null || true
tar -C /stage -czf /out/$NAME --owner=0 --group=0 --sort=name --mtime=@0 usr
ls $D/lib | tr "\n" " "; echo
du -sh $D
'
echo "built: $OUT"
ls -la "$OUT"
shasum -a 256 "$OUT"
