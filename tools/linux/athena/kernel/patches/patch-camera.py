#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""The front camera of the Echo Show 8 2nd gen: OV13855 -> CSI-2 D-PHY -> seninf -> CAMSV -> memory.

Mainline 6.18 has none of MediaTek's camera receive path, and the MT8183 ISP proper (P1) is only
ever programmed by the SCP firmware, in every public driver. CAMSV bypasses the ISP: it writes the
sensor's raw Bayer frames straight to memory, and echod does the rest in software, as on the
Echo Show 5. This adds:

  - src/camera/isp/: the seninf (CSI-2 receiver) and CAMSV (raw DMA) drivers, BayLibre's MT8365
    "ISP 3.0" v8 series with MT8183 support, as published in github.com/vpavlin/mt8183-camera
    (commit c00ad16, GPL-2.0, original copyright and SPDX headers kept), which runs them on an
    MT8183 Chromebook. Built in as drivers/media/platform/mediatek/isp. Four changes to them,
    each marked "athena:": v4l2_get_link_freq() takes the sensor's pad since 6.13 (and its result
    is checked); CAMSV writes packed 10-bit by default (out_mode=1), MediaTek's LSB-first packing
    that echod reads; CAMSV's error interrupts stay off (one stays asserted on MT8183 with nothing
    wrong); and userspace may ask for cached buffers (the MT8183 is not DMA-coherent).
  - the "mediatek,mt8183-csi-rx" compatible in the mainline MIPI CSI-2 D-PHY driver
    (phy-mtk-mipi-csi-0-5), the same block as MT8365's.
  - src/camera/ov13855.c: a sensor driver (register tables from Rockchip's GPL driver), as
    drivers/media/i2c/ov13855.c, VIDEO_OV13855.

Usage: patch-camera.py KTREE   (idempotent)
"""
import os
import shutil
import sys

tree = sys.argv[1]
here = os.path.dirname(os.path.abspath(__file__))
src = os.path.join(here, "..", "src", "camera")
MARK = "athena: camera"


def copy(name, rel):
    with open(os.path.join(src, name), "rb") as f:
        data = f.read().replace(b"\r\n", b"\n")
    dst = os.path.join(tree, rel)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    if not os.path.exists(dst) or open(dst, "rb").read() != data:
        with open(dst, "wb") as f:
            f.write(data)


def edit(rel, pairs):
    p = os.path.join(tree, rel)
    s = open(p).read()
    if MARK in s:
        return
    for a, b in pairs:
        assert s.count(a) == 1, (rel, a[:70], s.count(a))
        s = s.replace(a, b)
    assert MARK in s, rel
    open(p, "w").write(s)


for name in sorted(os.listdir(os.path.join(src, "isp"))):
    copy(os.path.join("isp", name), "drivers/media/platform/mediatek/isp/" + name)
copy("ov13855.c", "drivers/media/i2c/ov13855.c")
copy("ov13855-tables.inc", "drivers/media/i2c/ov13855-tables.inc")

edit("drivers/media/platform/mediatek/Kconfig", [
    ('source "drivers/media/platform/mediatek/jpeg/Kconfig"\n',
     '# athena: camera (seninf + CAMSV)\nsource "drivers/media/platform/mediatek/isp/Kconfig"\n'
     'source "drivers/media/platform/mediatek/jpeg/Kconfig"\n'),
])
edit("drivers/media/platform/mediatek/Makefile", [
    ("obj-y += jpeg/\n", "# athena: camera (seninf + CAMSV)\nobj-y += isp/\nobj-y += jpeg/\n"),
])
edit("drivers/phy/mediatek/phy-mtk-mipi-csi-0-5.c", [
    ('\t{ .compatible = "mediatek,mt8365-csi-rx" },\n',
     '\t{ .compatible = "mediatek,mt8365-csi-rx" },\n'
     '\t/* athena: camera. MT8183 has the same CSI-2 receive PHY */\n'
     '\t{ .compatible = "mediatek,mt8183-csi-rx" },\n'),
])
edit("drivers/media/i2c/Kconfig", [
    ('config VIDEO_OV13B10\n',
     'config VIDEO_OV13855\n'
     '\ttristate "OmniVision OV13855 sensor support"\n'
     '\tselect V4L2_CCI_I2C\n'
     '\thelp\n'
     '\t  This is a Video4Linux2 sensor driver for the OmniVision\n'
     '\t  OV13855 camera (athena: camera).\n'
     '\n'
     'config VIDEO_OV13B10\n'),
])
edit("drivers/media/i2c/Makefile", [
    ("obj-$(CONFIG_VIDEO_OV13858) += ov13858.o\n",
     "obj-$(CONFIG_VIDEO_OV13858) += ov13858.o\n"
     "# athena: camera\nobj-$(CONFIG_VIDEO_OV13855) += ov13855.o\n"),
])
