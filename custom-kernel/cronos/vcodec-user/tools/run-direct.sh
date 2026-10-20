#!/bin/sh
# Run a test without chroot, straight from the Alpine rootfs: invoke the
# bionic linker explicitly and give it the library dirs through
# LD_LIBRARY_PATH. (Tested under qemu with an Alpine armv7 userland.)
#   ./run-direct.sh vdec_test -o /tmp/out.nv12 /tmp/in.h264
R=$(cd "$(dirname "$0")" && pwd)
cmd=$1; shift
export LD_LIBRARY_PATH="$R/system/lib:$R/vendor/lib"
export VCODEC_PROP_FILE="${VCODEC_PROP_FILE:-$R/vendor/etc/vcodec.prop}"
exec "$R/system/bin/linker" "$R/system/bin/$cmd" "$@"
