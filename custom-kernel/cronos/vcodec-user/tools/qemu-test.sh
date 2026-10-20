#!/bin/sh
# Offline test of the staging tree on a dev machine. The tree becomes the
# root filesystem of a linux/arm/v7 container, which Docker runs under binfmt
# qemu-arm. That is equivalent to the device chroot: the same
# /system/bin/linker, the same default search paths, and no ld.config.
# There is no /dev/Vcodec here, so allocation tests use VCODEC_SHIM_FAKE=1
# (the default below).
#
# Two qemu-only workarounds:
#  - seccomp=unconfined: the linker calls personality(0xffffffff) at start-up,
#    and Docker's default seccomp profile rejects that.
#  - qemu-user on a host without AArch32 (Apple silicon, many arm64 servers)
#    cannot do personality(PER_LINUX32), and the Android 11 linker aborts if
#    that fails (__libc_init_AT_SECURE). In a copy of the linker used only
#    here, the "beq <fatal>" after that call is patched to a NOP. The device
#    (an AArch32-capable CPU) uses the unmodified linker.
#
#   tools/qemu-test.sh [cmd args...]   (default: smoke_test)
#   QEMU_DOCKER_ARGS="-v $PWD/data:/data" tools/qemu-test.sh vdec_test ...
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
ST=$HERE/out/staging
[ -x "$ST/system/bin/linker" ] || { echo "run tools/build.sh first"; exit 1; }

rm -rf "$HERE/out/qemu-root"
cp -R "$ST" "$HERE/out/qemu-root"
python3 "$HERE/tools/qemu-patch-linker.py" "$HERE/out/qemu-root/system/bin/linker"
printf 'FROM scratch\nCOPY qemu-root/ /\n' > "$HERE/out/Dockerfile.qemu"
docker build -q --platform linux/arm/v7 -t vcodec-user-qemu \
	-f "$HERE/out/Dockerfile.qemu" "$HERE/out" >/dev/null

[ $# -gt 0 ] || set -- smoke_test
cmd=$1; shift
exec docker run --rm --platform linux/arm/v7 --security-opt seccomp=unconfined \
	--ulimit core=0 \
	-e VCODEC_SHIM_FAKE="${VCODEC_SHIM_FAKE:-1}" -e VCODEC_SHIM_DEBUG="${VCODEC_SHIM_DEBUG:-1}" \
	-e VCODEC_LOG="${VCODEC_LOG:-I}" ${QEMU_DOCKER_ARGS:-} \
	vcodec-user-qemu /system/bin/$cmd "$@"
