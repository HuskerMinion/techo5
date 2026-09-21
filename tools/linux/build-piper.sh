#!/bin/bash
# build-piper.sh — compile Piper's libpiper and its CLI for armv7 inside an Alpine root under QEMU
# user emulation, in WSL, the way build-aec.sh compiles the echo canceller.
#
#   tools/linux/build-piper.sh [-o bin]
#
# Why this exists, given that nothing in TECHO5 runs Piper: it was the only way to answer whether a
# neural text to speech could run on these devices at all. It can. **It is too slow to use**, measured
# on an Echo Spot with the low quality en_US voice:
#
#   loading the model        ~16 s, once
#   speaking, once loaded    1.8x slower than real time
#   a 1.6 s prompt, cold     19.1 s
#   on disk                  94 MB — 63 model, 20 libraries, 12 espeak-ng data
#
# So the device speaks by playing clips rendered by Piper **on the build machine**, where it is
# instant, and this script is kept for the next person who wonders, and because the two obstacles it
# works around are not written down anywhere else:
#
#   1. cmake cannot fork a child process under QEMU user emulation. Every link step it drives fails,
#      `ar` included, so the sources are compiled and linked by calling the compiler directly.
#   2. libpiper otherwise downloads a prebuilt onnxruntime built against glibc, which is no use on a
#      musl root filesystem. Alpine packages onnxruntime for armv7; it is pointed at that instead.
#
# Needs what wsl-build.sh needs (qemu-user-static, binfmt-support, uidmap) and the Alpine minirootfs
# from TECHO5_INPUTS (default: inputs/ in this repository).
set -euo pipefail
ROOT=${TECHO5_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
SDK=${PIPER_SDK:-$HOME/alpine-armv7-piper}
APK=${APK_STATIC:-$INPUTS/apk.static}
OUT=$ROOT/bin
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done

if [ -z "${TECHO5_IN_NS:-}" ]; then
	exec unshare -Ur --map-auto env TECHO5_IN_NS=1 bash "$0" -o "$OUT"
fi

if [ ! -x "$SDK/usr/bin/g++" ]; then
	echo "== making the armv7 root at $SDK"
	rm -rf "$SDK"; mkdir -p "$SDK"
	tar -xzf "$(ls "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz | head -1)" -C "$SDK"
	cp /etc/resolv.conf "$SDK/etc/resolv.conf"
	"$APK" --root "$SDK" --arch armv7 --no-cache --initdb add \
		alpine-baselayout busybox apk-tools build-base git \
		onnxruntime-dev espeak-ng-dev espeak-ng
fi

if [ ! -d "$SDK/build/piper1-gpl" ]; then
	echo "== fetching piper"
	mkdir -p "$SDK/build"
	git clone --depth 1 https://github.com/OHF-Voice/piper1-gpl "$SDK/build/piper1-gpl"
	python3 "$ROOT/tools/linux/piper-system-espeak.py" \
		"$SDK/build/piper1-gpl/libpiper/CMakeLists.txt"
fi

mkdir -p "$SDK/build/manual"
cat > "$SDK/build/manual/build.sh" <<'INSIDE'
#!/bin/sh
set -e
cd /build/piper1-gpl/libpiper
INC="-I include -I /usr/include/onnxruntime -I /usr/include"
VER='-DPIPER_VERSION="1.8.0"'

echo "== libpiper.so"
g++ -O2 -std=c++17 -fPIC -shared $VER $INC \
	-o /build/manual/libpiper.so \
	src/piper.cpp src/chinese_phonemizer.cpp \
	-lespeak-ng -lonnxruntime

echo "== piper"
g++ -O2 -std=c++17 $VER $INC -I src/main -I src/main/utils \
	-o /build/manual/piper \
	src/main/main.cpp \
	src/main/utils/main_utils.cpp src/main/utils/process.cpp \
	src/main/utils/wav_headers.cpp src/main/utils/wavfile.cpp \
	-L/build/manual -lpiper -lespeak-ng -lonnxruntime
strip /build/manual/piper /build/manual/libpiper.so
INSIDE

echo "== building under QEMU (slow)"
chroot "$SDK" /bin/sh /build/manual/build.sh

mkdir -p "$OUT"
cp "$SDK/build/manual/piper" "$OUT/piper-arm"
cp "$SDK/build/manual/libpiper.so" "$OUT/libpiper-arm.so"
echo "built: $OUT/piper-arm, $OUT/libpiper-arm.so"
echo
echo "To run one on a device it also needs, from the same Alpine root: libonnxruntime, libespeak-ng,"
echo "libprotobuf-lite, libre2, libabsl_*, libutf8_validity, libicu*, libpcaudio, libstdc++, libgcc_s,"
echo "/usr/share/espeak-ng-data, and a voice model. About 20 MB of libraries before the model."
