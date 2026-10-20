#!/bin/sh
# Develop one packed RAW10 1600x1200 frame (RGGB) on ISP pass 2, through the isp2test module:
# IMGI -> UNP -> CFA (demosaic) -> G2C (BT.601, full range) -> C42 -> IMG2O, three tiles from a tile
# table (isp2cmp tdr), YUYV out with signed chroma. Run on the unit with the capture stopped
# (touch /run/techo5/hold; kill $(pidof techo5)) and isp2test loaded:
#
#   tools/pass2.sh in.raw tdr.bin out.yuv
#
# The register recipe is the one found in stage 2 (research/mt8163-isp-pass2.md).
set -eu
RAW=$1 TDR=$2 OUT=$3
D=/sys/kernel/debug/isp2test
mountpoint -q /sys/kernel/debug || mount -t debugfs none /sys/kernel/debug
[ -d $D ] || { echo "isp2test is not loaded" >&2; exit 1; }

cat "$RAW" > $D/imgi
dd if="$TDR" of=$D/tdr conv=notrunc 2>/dev/null
echo "fill img2o 0xaa" > $D/ctl
echo clear > $D/ctl
# One command per write: the first that fails stops the rest.
while read -r cmd; do
	case "$cmd" in ''|'#'*) continue ;; esac
	echo "$cmd" > $D/ctl
done <<'EOF'
on
reset
# start clear, internal clocks on, the TG (pass 1) off
w 0x000 0
w 0x150 0x1ffff
w 0x410 0 0
# interrupt status enables: the status bits latch only when enabled
w 0x020 0x5fbbf41f
# every DMA base not used points at the scratch buffer; the command queues at nothing mapped
w 0x26c @aux
w 0x300 @aux
w 0x36c @aux
w 0x388 @aux
w 0x0a8 0 0 0 0 0 0
# FMT_SEL: scenario 3 (memory to memory), CAM_IN_FMT 1 (RAW10); the TG1 bits as pass 1 left them
w 0x010 0x00011103
# EN1: UNP (20) and CFA (21); EN2: G2C (0) and C42 (1); DMA_EN: IMGI (7) and IMG2O (10)
w 0x004 0x00300000
w 0x008 0x00000003
w 0x00c 0x00000480
w 0x01c 0
w 0x078 0
# IMGI: base, offset, xsize, ysize, stride (the tile table overrides offset and sizes per tile)
w 0x230 @imgi 0 1999 1199 2000
# IMG2O: base, offset, xsize, ysize, stride
w 0x320 @img2o 0 3199 1199 3200
# G2C: Y, Cb, Cr offsets 0 (the reset values; chroma comes out signed)
w 0xa04 0x0000003a
w 0xa0c 0x00000100
w 0xa14 0x000007d6
# 0xa1c bit 0 changes the chroma slightly (by about 1 code, rounding?); clear
w 0xa1c 0
# the tile table: TDRI base, offset, size; TCM_EN.TDR_EN (31); SEL bit 6 loads the tiles' sizes
w 0x050 0x0000032f
w 0x204 @tdr 0 71
w 0x054 0x80000000
w 0x018 0x00000040
run 0x000 1 500 0x4000
reset
EOF
cat $D/log
head -c 3840000 $D/img2o > "$OUT"
