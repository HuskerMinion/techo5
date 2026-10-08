#!/bin/bash
# build-ffmpeg.sh — a small ffmpeg for the Echo Show's video player (feature/video): armv7, musl, built
# inside an Alpine armv7 root under QEMU user emulation in WSL, the way build-aec.sh builds.
#
#   tools/linux/build-ffmpeg.sh [-o bin/techo5-ffmpeg-arm]
#
# Needs what wsl-build.sh needs (qemu-user-static, binfmt-support, uidmap, ~/apk/apk.static) and the
# Alpine minirootfs from TECHO5_INPUTS (default: inputs/ in this repository). The first run makes the
# root (~/alpine-armv7-cast) and takes a while under QEMU; later runs reuse it and rebuild only what
# changed. A change to the configure flags below reconfigures by itself.
#
# Only what the player uses, so there is less in it to attack: the H.264, MPEG-4, AAC, MP3, Opus, AC-3 and
# E-AC-3 decoders (media servers' films have AC-3 sound as often as not); the MP4, Matroska, MPEG-TS,
# HLS, AAC, MP3, Ogg and WAV demuxers; acompressor and alimiter, which bring a film's quiet sound up; the network protocols
# (HTTP, HTTPS, TCP, TLS, HLS, crypto for AES HLS, and httpproxy for the daemon's guard proxy, which a
# device whose kernel cannot fence the decoder sends everything through) and pipe for its output. There is no file protocol
# at all: nothing a stream or a playlist names can make it read a file on the device, whatever its
# protocol whitelist says. udp is in only because ffmpeg 8's TLS code links against it. The libraries
# are static; only musl, libssl.so.3, libcrypto.so.3 and libz.so.1 (all in the rootfs) are shared.
#
# The source is ffmpeg's release tarball, checked against the SHA-256 below (taken from the tarball
# ffmpeg.org served for 8.1.2; ffmpeg publishes a GPG signature rather than a checksum).
set -euo pipefail
ROOT=${TECHO5_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
export TECHO5_ROOT=$ROOT
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
FFVER=8.1.2
SHA256=464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c
SDK=${CAST_SDK:-$HOME/alpine-armv7-cast}
APK=$HOME/apk/apk.static
OUT=$ROOT/bin/techo5-ffmpeg-arm
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done

if [ -z "${TECHO5_IN_NS:-}" ]; then
	exec unshare -Ur --map-auto env TECHO5_IN_NS=1 bash "$0" -o "$OUT"
fi

if [ ! -x "$SDK/usr/bin/gcc" ]; then
	echo "== making the armv7 root at $SDK"
	rm -rf "$SDK"; mkdir -p "$SDK"
	tar -xzf "$(ls "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz | head -1)" -C "$SDK"
	cp /etc/resolv.conf "$SDK/etc/resolv.conf"
	"$APK" --root "$SDK" --arch armv7 --no-cache add build-base pkgconf linux-headers openssl-dev zlib-dev perl
fi

mkdir -p "$SDK/build"
TARBALL=$SDK/build/ffmpeg-$FFVER.tar.xz
if [ ! -f "$TARBALL" ]; then
	echo "== fetching ffmpeg $FFVER"
	curl -fsSL "https://ffmpeg.org/releases/ffmpeg-$FFVER.tar.xz" -o "$TARBALL.part"
	mv "$TARBALL.part" "$TARBALL"
fi
echo "$SHA256  $TARBALL" | sha256sum -c - || { echo "the tarball is not the ffmpeg $FFVER this script was written for" >&2; exit 1; }
SRC=/build/techo5-ffmpeg-$FFVER
if [ ! -d "$SDK$SRC" ]; then
	mkdir -p "$SDK$SRC"
	tar -xJf "$TARBALL" -C "$SDK$SRC" --strip-components=1
fi

FLAGS="--arch=arm --cpu=cortex-a53 --enable-neon \
	--disable-autodetect --disable-everything --disable-doc --disable-debug \
	--disable-avdevice --enable-ffmpeg --disable-ffprobe --disable-ffplay \
	--enable-static --disable-shared --enable-pthreads \
	--enable-openssl --enable-zlib --enable-network \
	--enable-swscale --enable-swresample --enable-avfilter \
	--enable-decoder=h264,mpeg4,aac,aac_latm,mp3float,opus,ac3,eac3 \
	--enable-demuxer=mov,matroska,mpegts,hls,aac,mp3,ogg,wav \
	--enable-parser=h264,mpeg4video,aac,aac_latm,mpegaudio,opus,ac3 \
	--enable-protocol=pipe,http,https,httpproxy,tcp,udp,tls,hls,crypto \
	--enable-muxer=rawvideo,pcm_s16le,null \
	--enable-encoder=rawvideo,pcm_s16le,wrapped_avframe \
	--enable-filter=scale,format,transpose,pad,fps,hflip,vflip,null,anull,aresample,aformat,acompressor,alimiter"

cat > "$SDK/build/techo5-ffmpeg.sh" <<INSIDE
#!/bin/sh
set -e
cd $SRC
if [ ! -f ffbuild/config.mak ] || [ "\$(cat .techo5-flags 2>/dev/null)" != "$FLAGS" ]; then
	./configure --prefix=/build/inst $FLAGS --extra-ldflags="-static-libgcc"
	echo "$FLAGS" > .techo5-flags
fi
make -j\$(nproc) ffmpeg
strip ffmpeg
ls -la ffmpeg
INSIDE
echo "== building under QEMU"
chroot "$SDK" /bin/sh /build/techo5-ffmpeg.sh
mkdir -p "$(dirname "$OUT")"
cp "$SDK$SRC/ffmpeg" "$OUT"
echo "built: $OUT"
ls -la "$OUT"
sha256sum "$OUT"
