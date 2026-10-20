#!/bin/sh
# Build kmod/isp2test.ko and kmod/mdptest.ko against the kernel tree in the techo5-k6 volume
# (/src/linux-7.2.11, its out/ already configured and built), in the techo5-kbuild container, as
# k6.sh does. The modules are built in a copy outside the repository; the .ko files land in out/kmod/.
#   tools/kmod.sh [KDIR]
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
KDIR=${1:-/src/linux-7.2.11}
mkdir -p "$HERE/out/kmod"
docker run --rm --platform linux/arm64 -v techo5-k6:/src -v "$HERE/kmod:/m:ro" -v "$HERE/out/kmod:/o" \
	techo5-kbuild sh -euc "
rm -rf /tmp/b && cp -r /m /tmp/b
make -s -C $KDIR O=out M=/tmp/b ARCH=arm64 modules
cp /tmp/b/*.ko /o/
for k in /o/*.ko; do modinfo \$k | grep -E 'vermagic|license'; done
"
