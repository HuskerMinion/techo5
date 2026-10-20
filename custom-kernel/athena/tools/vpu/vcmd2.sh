#!/bin/sh
# vcmd2.sh CMD [N=VALUE ...]: send the VPU a command and wait for it as Fire OS does without the
# interrupt (research/vpu-vision-p6.md, "Loader and D2D protocol"): INFO00 cleared, the arguments,
# RUN_STALL off, CTL_XTENSA_INT; done when INFO00 is READY (1) again and DONE_ST shows PWAITMODE.
cd /data/hw
C=0x80110
info() { echo $((0x80150 + 4 * $1)); }
cmd=$1; shift
for a in "$@"; do ./vr $(info ${a%%=*}) ${a#*=}; done
./vr $(info 0) 0
./vr $(info 1) $cmd
./vr 0x80114 1
v=$(( $(./vr $C) )); ./vr $C $(( v & ~(1<<23) ))
./vr 0x80118 1
i=0; while [ $i -lt 200 ]; do
	[ $(( $(./vr $(info 0)) )) -eq 1 ] && [ $(( $(./vr 0x8010c) & 0x80 )) -ne 0 ] && break
	sleep 0.05; i=$((i+1))
done
echo "cmd $cmd after $((i*50)) ms: INFO00 $(./vr $(info 0)) DONE_ST $(./vr 0x8010c) INFO25 $(./vr $(info 25)) PC $(./vr 0x801e4) FAULT $(./vr 0x801ec)"
./vr 0x80114 1
v=$(( $(./vr $C) )); ./vr $C $(( v | 1<<23 ))
