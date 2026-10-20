#!/bin/sh
# Run a test inside this staging tree as a chroot (as root on the device).
#   ./run-chroot.sh vdec_test /data/in.h264 /data/out ...
# Puts the device's /dev (for /dev/Vcodec) and a /proc at the expected places.
# /data is bind-mounted from $DATA (default: <staging>/data) so the input and
# output files are easy to reach.
set -eu
R=$(cd "$(dirname "$0")" && pwd)
DATA=${DATA:-$R/data}
mountpoint -q "$R/proc" || mount -t proc proc "$R/proc"
mountpoint -q "$R/dev" || mount --bind /dev "$R/dev"
[ "$DATA" = "$R/data" ] || mountpoint -q "$R/data" || mount --bind "$DATA" "$R/data"
cmd=$1; shift
exec chroot "$R" /system/bin/$cmd "$@"
