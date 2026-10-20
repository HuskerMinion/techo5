#!/bin/sh
# Run tpipe_test (or PROG=dpcap) on the unit under the Android 11 runtime the image already carries for the video
# filters (/usr/local/lib/techo5-vcodec), with this directory's stand-ins first and the unit's own
# /vendor/lib (libdpframework.so) last.
#   DIR=/tmp/isp-user tools/run.sh [-o OUTDIR] [field=value ...]
# DIR holds bin/tpipe_test and lib/*.so from out/. LOG=D shows the library's own log (default W).
D=${DIR:-$(cd "$(dirname "$0")/.." && pwd)}
V=${VCODEC_RUNTIME:-/usr/local/lib/techo5-vcodec}
export VCODEC_LOG=${LOG:-W}
export LD_LIBRARY_PATH="$D/lib:$V/system/lib:$V/shim:/vendor/lib"
exec "$V/system/bin/linker" "$D/bin/${PROG:-tpipe_test}" "$@"
