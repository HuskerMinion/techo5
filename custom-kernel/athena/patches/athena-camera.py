#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Add athena's camera driver (mt8183-athena-camera.c, next to this script) to the tree.

    athena-camera.py <linux tree>

The driver runs the fixed OV13855 -> CSI0 -> seninf -> CAMSV1 path and hands packed 10-bit frames
to V4L2, as the Echo Show 5's cronos-camera does on MT8163. CONFIG_VIDEO_ATHENA_CAMERA builds it.
Idempotent.
"""
import os
import shutil
import sys

KCONFIG = """
config VIDEO_ATHENA_CAMERA
	tristate "Echo Show 8 2nd gen OV13855 capture"
	depends on VIDEO_DEV && I2C && GPIOLIB && OF && HAS_DMA
	select VIDEOBUF2_VMALLOC
	select VIDEOBUF2_V4L2
	help
	  Capture packed RAW10 frames from the Echo Show 8 2nd gen's OV13855 through the MT8183
	  seninf and CAMSV1.
"""


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    d = os.path.join(sys.argv[1], "drivers/media/platform/mediatek")
    shutil.copyfile(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mt8183-athena-camera.c"),
                    os.path.join(d, "mt8183-athena-camera.c"))
    p = os.path.join(d, "Makefile")
    s = open(p).read()
    if "mt8183-athena-camera" not in s:
        open(p, "a").write("obj-$(CONFIG_VIDEO_ATHENA_CAMERA) += mt8183-athena-camera.o\n")
    p = os.path.join(d, "Kconfig")
    s = open(p).read()
    if "VIDEO_ATHENA_CAMERA" not in s:
        open(p, "a").write(KCONFIG)


if __name__ == "__main__":
    main()
