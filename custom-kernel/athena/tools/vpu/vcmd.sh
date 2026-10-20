#!/bin/sh
# vcmd.sh CMD [N=VALUE ...]: send the VPU a command (INFO01 = CMD, INFOn = VALUE), as MediaTek's driver
# does: the arguments, RUN_STALL off, CTL_XTENSA_INT; then wait for its interrupt and stall it again
cd /data/hw
C=0x80110
info() { echo $((0x80150 + 4 * $1)); }
cmd=$1; shift
for a in "$@"; do ./vr $(info ${a%%=*}) ${a#*=}; done
./vr $(info 1) $cmd
./vr 0x80114 1                                                   # the VPU last interrupt, cleared (write 1)
v=$(( $(./vr $C) )); ./vr $C $(( v & ~(1<<23) ))
./vr 0x80118 1                                                   # interrupt the VPU
i=0; while [ $i -lt 100 ]; do [ $(( $(./vr 0x80114) )) -ne 0 ] && break; sleep 0.05; i=$((i+1)); done
echo "cmd $cmd after $((i*50)) ms: INFO00 $(./vr $(info 0)) XTENSA_INT $(./vr 0x80114) PC $(./vr 0x801d0)"
./vr 0x80114 1
v=$(( $(./vr $C) )); ./vr $C $(( v | 1<<23 ))
