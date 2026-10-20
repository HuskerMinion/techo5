#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""mediatek-dsi: the clock lane idles in LP while commands go out, and runs continuously for video.

    mtk-dsi-cmd-clock-lp.py <linux tree>

mtk_dsi sets HSTX_CKLP_EN (the clock lane drops to LP when no HS data is going) from the panel's
MIPI_DSI_CLOCK_NON_CONTINUOUS, once, for commands and video alike, and turns the clock lane's HS
transmitter on before the panel's init table goes out. On athena's JD9365 BOE-new panel, a continuous
clock while the table goes out leaves it not taken at most boots (no reply to a DCS read, a dark
screen), and a non-continuous clock in video flickered with mediatek-dsi's line. LK keeps the clock
non-continuous in video too, with the clock's HS word count (DSI_HSTX_CKL_WC) covering the front
porch as well, which mediatek-dsi does not set; a continuous clock in video needs no such count.

So the lanes come up with HSTX_CKLP_EN set, and mtk_output_dsi_enable clears it before video starts
unless the panel asks for a non-continuous clock (athena's ILI9881C does, and is unchanged). Each
power-on writes DSI_TXRX_CTRL again (mtk_dsi_lane_ready), so every prepare sends its table with the
clock in LP. This kernel is built for athena only.
Idempotent.
"""
import os
import sys

RXTX_OLD = """\tif (dsi->mode_flags & MIPI_DSI_CLOCK_NON_CONTINUOUS)
\t\tregval |= HSTX_CKLP_EN;
"""
RXTX_NEW = """\t/* athena: LP clock for commands; mtk_output_dsi_enable makes it continuous for video */
\tregval |= HSTX_CKLP_EN;
"""
ENABLE_OLD = """\tif (dsi->enabled)
\t\treturn;

\tmtk_dsi_set_mode(dsi);
\tmtk_dsi_start(dsi);
"""
ENABLE_NEW = """\tif (dsi->enabled)
\t\treturn;

\tif (!(dsi->mode_flags & MIPI_DSI_CLOCK_NON_CONTINUOUS))
\t\tmtk_dsi_mask(dsi, DSI_TXRX_CTRL, HSTX_CKLP_EN, 0);
\tmtk_dsi_set_mode(dsi);
\tmtk_dsi_start(dsi);
"""
PARTS = ((RXTX_OLD, RXTX_NEW), (ENABLE_OLD, ENABLE_NEW))


def main():
    path = os.path.join(sys.argv[1], "drivers/gpu/drm/mediatek/mtk_dsi.c")
    with open(path) as f:
        s = f.read()
    for old, new in PARTS:
        if new in s:
            continue
        assert s.count(old) == 1, "mtk-dsi-cmd-clock-lp: mtk_dsi.c is not the one expected (%r)" % old[:40]
        s = s.replace(old, new)
    with open(path, "w") as f:
        f.write(s)


if __name__ == "__main__":
    main()
