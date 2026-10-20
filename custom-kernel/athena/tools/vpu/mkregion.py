#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Build the VPU's firmware region image from a cam_vpu1 partition dump (research/vpu-vision-p6.md).

    mkregion.py CAM_VPU1.img OUT.bin

OUT.bin is the first 4 MiB of the no-map region at PA 0x7da00000 (patches/vpu.py): core 0's
segments of cam_vpu1 at their addresses, and at 0x7dd00000 the IRAM blob the firmware's SET_DBG
loads into the core's DMEM and IMEM (IRAM_DATA_HEADER {num; {ofst, dst, size}[20]}, then the data).
vboot.sh writes it to debugfs techo5-vpu/mem and boots the core.
"""
import struct
import sys

BASE = 0x7da00000
d = open(sys.argv[1], "rb").read()
h = 0x200
n, = struct.unpack_from("<I", d, h + 0x30)
img = bytearray(0x400000)
iram = []
for i in range(n):
    core, off, dst, length, _ = struct.unpack_from("<5I", d, h + 0x34 + i * 20)
    if core != 0x61:
        continue
    data = d[h + off:h + off + length]
    if dst >= 0x7ff00000:
        iram.append((dst, data))
    else:
        img[dst - BASE:dst - BASE + length] = data
hdr = bytearray(4 + 20 * 12)
struct.pack_into("<I", hdr, 0, len(iram))
blob = bytearray()
for k, (dst, data) in enumerate(iram):
    struct.pack_into("<3I", hdr, 4 + k * 12, 0x100 + len(blob), dst, len(data))
    blob += data + bytes((-len(data)) % 16)
ib = hdr.ljust(0x100, b"\0") + blob
img[0x300000:0x300000 + len(ib)] = ib
open(sys.argv[2], "wb").write(img)
print("%s: %d core 0 segments, IRAM blob %d bytes at %#x" % (sys.argv[2], n, len(ib), BASE + 0x300000))
