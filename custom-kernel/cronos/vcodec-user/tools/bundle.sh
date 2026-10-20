#!/bin/sh
# bundle.sh — the part of out/staging an image may carry, for the Show 5 2nd gen's hardware video
# (echod feature/video, hw_vcodec.go; H.264 and H.265 decode): the Android 11 runtime (AOSP, Apache-2.0 and BSD), our
# stand-ins for Android's libraries and vcodec-dec, under /usr/local/lib/techo5-vcodec. MediaTek's
# libraries are not in it: they are the unit's own, read from its /vendor/lib at run time. Nor is
# anything only the tests use (the fake /dev/Vcodec, the harnesses).
#
#   tools/build.sh VENDOR_LIB_DIR stage && tools/bundle.sh [-o ../../../bin/techo5-vcodec-arm.tar.gz]
set -eu
D=$(cd "$(dirname "$0")/.." && pwd)
OUT=$D/../../../bin/techo5-vcodec-arm.tar.gz
[ "${1:-}" = -o ] && OUT=$2
S=$D/out/staging
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
R=$T/usr/local/lib/techo5-vcodec
mkdir -p "$R/system/bin" "$R/system/lib" "$R/shim" "$R/etc"
cp "$S/system/bin/linker" "$S/system/bin/vcodec_dec" "$S/system/bin/vcodec_enc" "$R/system/bin/"
for l in ld-android.so libc.so libm.so libdl.so libc++.so liblog.so libcutils.so libion.so libutils.so; do
	cp "$S/system/lib/$l" "$R/system/lib/"
done
# the stand-ins that take the place of the unit's own copies in /vendor/lib (searched before it)
for l in libion_mtk.so libm4u.so libbwc.so libmp4enc_sa.ca7.so libvc1dec_sa.ca7.so libvp8enc_sa.ca7.so \
	libhevce_sb.ca7.android.so; do
	cp "$S/vendor/lib/$l" "$R/shim/"
done
cp "$S/vendor/etc/vcodec.prop" "$R/etc/"
# what vcodec_dec decodes (its -c), for echod (hw_vcodec.go: a bundle without the file is H.264 only)
echo "h264 hevc" > "$R/etc/codecs"
mkdir -p "$(dirname "$OUT")"
tar -C "$T" -czf "$OUT" --owner=0 --group=0 usr
ls -l "$OUT"
