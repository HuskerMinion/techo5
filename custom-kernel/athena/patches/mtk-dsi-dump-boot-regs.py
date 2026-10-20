#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Diagnostic: log the DSI and MIPI TX registers the boot loader left, before mediatek-dsi changes them.

    mtk-dsi-dump-boot-regs.py <linux tree> [--revert]

LK drives athena's panel with MediaTek's own DSI driver and leaves it running when the kernel starts.
At mtk_dsi_probe the controller still holds LK's configuration (video mode, porches in bytes, D-PHY
timings, and in MIPI TX the lane rate), so this logs DSI 0x000-0x1ff and MIPI TX 0x000-0x0ff there,
to compare with what mediatek-dsi programs. Not part of a release build: build.sh does not run it;
--revert takes it out of the tree again.
"""
import os
import sys

ANCHOR = "\tdsi->regs = devm_platform_ioremap_resource(pdev, 0);\n"
DUMP = """\t/* mtk-dsi-dump-boot-regs */
\tif (!IS_ERR(dsi->regs)) {
\t\tvoid __iomem *tx = ioremap(0x11e50000, 0x100);
\t\tint i;

\t\tfor (i = 0; i < 0x200; i += 16)
\t\t\tdev_info(dev, "boot dsi %03x: %08x %08x %08x %08x\\n", i, readl(dsi->regs + i),
\t\t\t\t readl(dsi->regs + i + 4), readl(dsi->regs + i + 8), readl(dsi->regs + i + 12));
\t\tfor (i = 0; tx && i < 0x100; i += 16)
\t\t\tdev_info(dev, "boot mipitx %03x: %08x %08x %08x %08x\\n", i, readl(tx + i),
\t\t\t\t readl(tx + i + 4), readl(tx + i + 8), readl(tx + i + 12));
\t\tif (tx)
\t\t\tiounmap(tx);
\t}
\t/* end mtk-dsi-dump-boot-regs */
"""


def main():
    path = os.path.join(sys.argv[1], "drivers/gpu/drm/mediatek/mtk_dsi.c")
    with open(path) as f:
        s = f.read()
    if "--revert" in sys.argv:
        s = s.replace(DUMP, "")
    elif DUMP not in s:
        assert s.count(ANCHOR) == 1, "mtk-dsi-dump-boot-regs: mtk_dsi_probe is not the one expected"
        s = s.replace(ANCHOR, ANCHOR + DUMP)
    with open(path, "w") as f:
        f.write(s)


if __name__ == "__main__":
    main()
