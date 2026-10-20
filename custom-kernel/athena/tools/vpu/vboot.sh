#!/bin/sh
# vboot.sh: load cam_vpu1's core 0 into the region and boot the VPU (MT6771's vpu_hw_boot_sequence)
set -e
cd /data/hw
D=/sys/kernel/debug/techo5-vpu
mount | grep -q debugfs || mount -t debugfs none /sys/kernel/debug
dd if=vpu-region.bin of=$D/mem bs=1M conv=notrunc 2>/dev/null
echo 1 > $D/power
C=0x80110
ctl() { echo $(( $(./vr $C) )); }
echo "CTRL before $(./vr $C)"
./vr 0x801f8 0x7da00000                                   # ALTRESETVEC
v=$(ctl); v=$(( (v | 1<<31 | 1<<26 | 1<<19 | 1<<23 | 1<<17) & ~(7<<27) )); ./vr $C $v
r=$(( $(./vr 0x8000c) )); ./vr 0x8000c $(( r | 1<<12 )); ./vr 0x8000c $r       # OCDHALTONRESET pulse
./vr 0x8000c $(( r | 1<<4 | 1<<8 )); ./vr 0x8000c $r                            # B_RST, D_RST pulse
v=$(ctl); ./vr $C $(( v & ~(1<<17) ))                                            # PIF ungated
# AXI_DEFAULT0/1: keep their reset values (AXI_DEFAULT1 holds the iDMA's write attributes; zeroed, an
# iDMA write to DDR never completes), the M4U bypassed (bits 22, 27 and 4, 9 clear), pre-ultra on
a0=$(( $(./vr 0x8013c) )); a1=$(( $(./vr 0x80140) )); echo "AXI_DEFAULT reset $(printf %#x $a0) $(printf %#x $a1)"
./vr 0x8013c $(( (a0 & ~(1<<22 | 1<<27)) | 1<<28 )); ./vr 0x80140 $(( a1 & ~(1<<4 | 1<<9) ))
echo "CTRL ready  $(./vr $C)  ALTRESET $(./vr 0x801f8)"
echo "DMEM0 word  $(./vr 0)"
v=$(ctl); ./vr $C $(( v & ~(1<<23) ))                                            # run
i=0; while [ $i -lt 50 ]; do s=$(( $(./vr 0x80150) )); [ $s -ne 0 ] && break; sleep 0.1; i=$((i+1)); done
echo "after $((i*100)) ms: INFO00 $(./vr 0x80150) XTENSA_INT $(./vr 0x80114) CTRL $(./vr $C)"
for k in 0 1 2 3 4 5 6 7 8 9 10 11; do printf "DEBUG_INFO%02d %s\n" $k $(./vr $((0x801d0 + 4*k))); done
v=$(ctl); ./vr $C $(( v | 1<<23 ))                                               # stall again
