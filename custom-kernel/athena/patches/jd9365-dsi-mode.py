#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Give athena's JD9365 panels the DSI link they work on.

    jd9365-dsi-mode.py <linux tree>

panel/0004 leaves mode_flags unset in its five athena descriptions, so panel-jadard-jd9365da-h3
falls back to burst video without MIPI_DSI_MODE_LPM, and the init table goes out in high-speed mode.
On a unit with the BOE-new panel (jd936x_id=0x35) that left the screen dark with no DSI error. The
ILI9881C description sends its table in low-power mode, as MediaTek's own driver does; this gives all
five JD9365 descriptions burst video with LPM. The clock lane is LP while the table goes out and
continuous for video (mtk-dsi-cmd-clock-lp.py).

The BOE-new panel's line is the one the boot loader drives it with. Read from the DSI registers LK
leaves at the kernel's start, that line is 478 Mbps a lane with HSA/HBP/HFP of 56/112/236 bytes.
panel/0004's mode (20/20/30 pixels at 69.2 MHz) gave 415 Mbps. mediatek-dsi takes the D-PHY's own
cycles out of HBP and HFP, which left HFP at 34 bytes, and the picture flickered and moved. A 22-pixel
sync, 32-pixel back and 108-pixel front porch at 79.68 MHz gives 56/110/235 bytes at 478 Mbps, and a
stable picture across reboots and blank cycles. Amazon's kernel names the same 478 Mbps
(PLL_CLOCK 239) for all of athena's panels. The other four JD9365 descriptions keep panel/0004's
timing, untried on a unit.
Idempotent.
"""
import os
import re
import sys

FLAGS = "\t.mode_flags = MIPI_DSI_MODE_VIDEO | MIPI_DSI_MODE_VIDEO_BURST | MIPI_DSI_MODE_LPM,\n"
DESCS = ("athena_jd_kd_hsd_desc", "athena_jd_kd_hsd_new_desc", "athena_jd_boe_desc",
         "athena_jd_boe_new_desc", "athena_jd_inx_desc")
# The boot loader's line: pixel clock (kHz), front porch, sync, back porch.
LK_LINE = {"athena_jd_boe_new_desc": (79680, 108, 22, 32)}
CLOCK_RE = r"(\t\t\.clock\t\t= )\d+,\t/\*[^*]*\*/"
H_RE = (r"(\t\t\.hsync_start\t= 800 \+ )\d+(,\n\t\t\.hsync_end\t= 800 \+ )\d+ \+ \d+"
        r"(,\n\t\t\.htotal\t\t= 800 \+ )\d+ \+ \d+ \+ \d+,")


def desc(s, name):
    m = re.search(r"static const struct jadard_panel_desc %s = \{\n(.*?)\n\};\n" % name, s, re.S)
    assert m, "jd9365-dsi-mode: %s not found (panel/0004 not applied?)" % name
    return m


def main():
    path = os.path.join(sys.argv[1], "drivers/gpu/drm/panel/panel-jadard-jd9365da-h3.c")
    with open(path) as f:
        s = f.read()
    for name in DESCS:
        m = desc(s, name)
        body = m.group(1)
        if name in LK_LINE:
            clock, hfp, hsa, hbp = LK_LINE[name]
            body, n = re.subn(CLOCK_RE, lambda c: c.group(1) + "%d,\t/* the boot loader's line: 478 Mbps a lane */" % clock, body)
            assert n == 1, "jd9365-dsi-mode: %s clock" % name
            body, n = re.subn(H_RE, lambda h: "%s%d%s%d + %d%s%d + %d + %d," % (
                h.group(1), hfp, h.group(2), hfp, hsa, h.group(3), hfp, hsa, hbp), body)
            assert n == 1, "jd9365-dsi-mode: %s horizontal timing" % name
        if FLAGS.rstrip("\n") not in body:
            assert ".mode_flags" not in body, "jd9365-dsi-mode: %s already sets mode_flags" % name
            fmt = "\t.format = MIPI_DSI_FMT_RGB888,"
            assert body.count(fmt) == 1, "jd9365-dsi-mode: %s format line" % name
            body = body.replace(fmt, fmt + "\n" + FLAGS.rstrip("\n"))
        s = s[:m.start(1)] + body + s[m.end(1):]
    with open(path, "w") as f:
        f.write(s)


if __name__ == "__main__":
    main()
