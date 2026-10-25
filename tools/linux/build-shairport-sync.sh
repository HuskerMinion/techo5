#!/bin/bash
# build-shairport-sync.sh — cross-compile shairport-sync with AirPlay 2, and nqptp, the clock it needs, for the
# devices: armv7, musl. Runs in WSL (or any Linux) with zig as the cross compiler, against an Alpine armv7 root
# that holds the libraries shairport-sync links to:
#
#   sudo apt install autoconf automake libtool pkg-config xxd libplist-utils   # or your distribution's
#   tools/linux/build-shairport-sync.sh [-o DIR]
#
# Alpine's shairport-sync is built without AirPlay 2, and Alpine has no nqptp, so the image's classic receiver
# (packages-rootfs.txt) can't be grouped with other speakers on an iPhone. These two can: the daemon runs them
# where the image carries them, and the classic one where it doesn't or they won't start (feature/streaming).
#
# Built rather than emulated, as build-librespot.sh is: a cross build is far faster than one under QEMU, and
# needs no binfmt. zig compiles for armv7 musl; the Alpine root (the minirootfs from TECHO5_INPUTS, with
# Alpine's -dev packages added by apk, which checks them against the index's signature) gives it the
# headers and libraries to link to, the same ones the image carries.
#
# AirPlay 2 decodes with FFmpeg. Alpine's FFmpeg would bring more than 100 MB of codecs and their libraries
# into the image, so a small one is built here instead and linked in statically: the ALAC and AAC decoders
# and swresample, which is all shairport-sync uses. What shairport-sync links to dynamically is then
# libplist, libsodium, libgcrypt (with libgpg-error), libuuid, and what the classic receiver already uses
# (avahi, OpenSSL's libcrypto, soxr, libconfig, popt), all from packages-rootfs.txt. nqptp is static.
#
# Each source is checked against the SHA-256 below before it is built: shairport-sync and nqptp as GitHub
# serves their release tags, ffmpeg as ffmpeg.org serves its release (the sum build-ffmpeg.sh checks too).
# nqptp and shairport-sync talk through shared memory whose layout each names by a version (smi11 here),
# so the two are only ever moved on together.
#
# One change is made to nqptp: nqptp-run-as-user.patch, beside this script, adds -u <user>. nqptp needs
# root only to bind PTP's ports (319 and 320); with -u it then becomes that user, with its groups, before it
# reads anything from the network, as nqptp already does on OpenBSD. The daemon runs it with -u streaming.
# (Capabilities would do it too, but the Dot's 3.18 kernel has no ambient ones, and a file capability does
# not survive the rootfs tarball.)
#
# Environment:
#   TECHO5_INPUTS  where the Alpine armv7 minirootfs is (default: inputs/ in this repository)
#   APK            a static apk for this machine (default ~/apk/apk.static, as build-ffmpeg.sh uses)
#   WORK           scratch (default ~/shairport-sync-build; ROOTS=fresh makes its Alpine root again)
#   J              parallel jobs (default: the number of CPUs)
set -euo pipefail
ROOT=${TECHO5_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
APK=${APK:-$HOME/apk/apk.static}
WORK=${WORK:-$HOME/shairport-sync-build}
J=${J:-$(nproc)}
OUT=$ROOT/bin
SPS_VERSION=5.6.1
SPS_SHA256=48d2ac1afa52d86766bc328694fa5d2d5f13a674f3e980f00d95d957453e5ee1
NQPTP_VERSION=1.2.9
NQPTP_SHA256=6aa1d73b8ee760bf19378c319db0d0537a867ff961764b150e19f87d96a02210
FF_VERSION=8.1.2
FF_SHA256=464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c
# What shairport-sync is linked against, from Alpine.
DEV_PACKAGES="musl-dev libplist-dev libsodium-dev libgcrypt-dev util-linux-dev libconfig-dev popt-dev openssl-dev avahi-dev soxr-dev"
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done

for z in "$HOME"/zig/zig-*/; do [ -x "$z/zig" ] && PATH="$z:$PATH"; done
command -v zig >/dev/null || { echo "zig is not on PATH" >&2; exit 1; }
for t in autoreconf make patch pkg-config xxd plistutil; do
	command -v $t >/dev/null || { echo "$t is not installed (see the top of this script)" >&2; exit 1; }
done

SYS=$WORK/sysroot
mkdir -p "$WORK/dl" "$WORK/bin"
if [ "${ROOTS:-}" = fresh ] || [ ! -e "$SYS/usr/include/plist/plist.h" ]; then
	echo "== making the Alpine armv7 root at $SYS"
	[ -x "$APK" ] || { echo "no static apk at $APK" >&2; exit 1; }
	rm -rf "$SYS"; mkdir -p "$SYS"
	tar -xzf "$(ls "$INPUTS"/alpine-minirootfs-*-armv7.tar.gz | head -1)" -C "$SYS"
	# Unpacked as this user, so apk is told to leave ownership as it is.
	"$APK" --root "$SYS" --arch armv7 --usermode --no-scripts --no-cache add $DEV_PACKAGES
fi

fetch() { # name url sha256
	local f=$WORK/dl/$1
	[ -f "$f" ] || { curl -fsSL -A "techo5 build-shairport-sync.sh" -o "$f.part" "$2" && mv "$f.part" "$f"; }
	echo "$3  $f" | sha256sum -c - || { echo "$1 is not the one this script was written for" >&2; exit 1; }
}
fetch shairport-sync-$SPS_VERSION.tar.gz https://github.com/mikebrady/shairport-sync/archive/refs/tags/$SPS_VERSION.tar.gz $SPS_SHA256
fetch nqptp-$NQPTP_VERSION.tar.gz https://github.com/mikebrady/nqptp/archive/refs/tags/$NQPTP_VERSION.tar.gz $NQPTP_SHA256
fetch ffmpeg-$FF_VERSION.tar.xz https://ffmpeg.org/releases/ffmpeg-$FF_VERSION.tar.xz $FF_SHA256

# zig as the compilers configure is given: armv7 musl for the Echos' Cortex-A53s (build-ffmpeg.sh builds for
# them too), with the Alpine root's headers and libraries. LDFLAGS=-s strips the binaries as they link.
cat > "$WORK/bin/armcc" <<EOF
#!/bin/sh
exec zig cc -target arm-linux-musleabihf -mcpu=cortex_a53 -isystem "$SYS/usr/include" -L"$SYS/usr/lib" "\$@"
EOF
sed 's/zig cc/zig c++/' "$WORK/bin/armcc" > "$WORK/bin/armcxx"
chmod +x "$WORK/bin/armcc" "$WORK/bin/armcxx"
export PATH="$WORK/bin:$PATH" CC=armcc CXX=armcxx AR="zig ar" RANLIB="zig ranlib" LDFLAGS=-s
export ZIG_GLOBAL_CACHE_DIR=$WORK/zig-cache ZIG_LOCAL_CACHE_DIR=$WORK/zig-cache
HOSTS="--host=arm-linux-musleabihf --build=$(uname -m)-linux-gnu"

unpack() { # tarball dir
	rm -rf "$WORK/$2"; mkdir -p "$WORK/$2"
	tar -xf "$WORK/dl/$1" -C "$WORK/$2" --strip-components=1
}

echo "== ffmpeg $FF_VERSION: ALAC and AAC decoders and swresample, static"
unpack ffmpeg-$FF_VERSION.tar.xz ffmpeg
FF=$WORK/ffmpeg-out
rm -rf "$FF"
(cd "$WORK/ffmpeg" && ./configure --prefix="$FF" --enable-cross-compile --target-os=linux --arch=arm --enable-neon \
	--cc=armcc --ar="zig ar" --ranlib="zig ranlib" --strip=true --pkg-config=pkg-config \
	--disable-autodetect --disable-everything --disable-doc --disable-debug --disable-programs \
	--disable-avdevice --disable-avfilter --disable-swscale --disable-network \
	--enable-static --disable-shared --enable-pthreads \
	--enable-avcodec --enable-avformat --enable-avutil --enable-swresample \
	--enable-decoder=alac,aac >/dev/null && make -j"$J" >/dev/null && make install >/dev/null)

echo "== nqptp $NQPTP_VERSION"
unpack nqptp-$NQPTP_VERSION.tar.gz nqptp
(cd "$WORK/nqptp" && patch -p1 --forward --no-backup-if-mismatch < "$ROOT/tools/linux/nqptp-run-as-user.patch")
(cd "$WORK/nqptp" && autoreconf -fi >/dev/null 2>&1 && ./configure $HOSTS --prefix=/usr >/dev/null && make -j"$J" >/dev/null)

echo "== shairport-sync $SPS_VERSION with AirPlay 2"
unpack shairport-sync-$SPS_VERSION.tar.gz shairport-sync
# The small FFmpeg is found before anything in the Alpine root, which has none.
export PKG_CONFIG_SYSROOT_DIR= PKG_CONFIG_LIBDIR="$FF/lib/pkgconfig:$SYS/usr/lib/pkgconfig:$SYS/usr/share/pkgconfig"
(cd "$WORK/shairport-sync" && autoreconf -fi >/dev/null 2>&1 && ./configure $HOSTS --prefix=/usr --sysconfdir=/etc \
	--with-airplay-2 --with-avahi --with-ssl=openssl --with-soxr --with-stdout --with-metadata >/dev/null &&
	make -j"$J" >/dev/null)

# Checked before anything is copied, so a build that fails a check never lands in bin/.
# The two must speak the same shared memory, and shairport-sync must be the AirPlay 2 build.
smi() { grep -o 'NQPTP_SHM_STRUCTURES_VERSION [0-9]*' "$1/nqptp-shm-structures.h" | cut -d' ' -f2; }
sv=$(smi "$WORK/shairport-sync") nv=$(smi "$WORK/nqptp")
grep -q '^#define CONFIG_AIRPLAY_2 1' "$WORK/shairport-sync/config.h" || { echo "shairport-sync was built without AirPlay 2" >&2; exit 1; }
[ -n "$sv" ] && [ "$sv" = "$nv" ] || { echo "shairport-sync wants nqptp's smi$sv, nqptp is smi$nv" >&2; exit 1; }
for b in shairport-sync nqptp; do
	# Debug paths would name whoever built it; the binaries ship as built, so they must not.
	if grep -a -q -F "$HOME" "$WORK/$b/$b"; then
		echo "$WORK/$b/$b names $HOME; not copying it" >&2
		exit 1
	fi
done
mkdir -p "$OUT"
for b in shairport-sync nqptp; do cp "$WORK/$b/$b" "$OUT/techo5-$b-arm"; done
file "$OUT/techo5-shairport-sync-arm" "$OUT/techo5-nqptp-arm" 2>/dev/null || true
ls -la "$OUT/techo5-shairport-sync-arm" "$OUT/techo5-nqptp-arm"
echo "built: shairport-sync $SPS_VERSION with AirPlay 2, nqptp $NQPTP_VERSION (smi$nv)"
sha256sum "$OUT/techo5-shairport-sync-arm" "$OUT/techo5-nqptp-arm"
