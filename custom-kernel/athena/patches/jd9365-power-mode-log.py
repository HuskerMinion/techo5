#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Log what a JD9365 panel says about itself once its init table has gone out.

    jd9365-power-mode-log.py <linux tree>

After the init table, panel-jadard-jd9365da-h3 reads the panel's power mode (DCS 0x0A) and its ID
(DCS 0x04) back over DSI and logs both. A panel that took the table and left sleep reports 0x9c
(booster on, idle off, sleep out, display on); anything else, or a failed read, says the table did
not take, where a dark screen alone cannot tell that from a video link the panel does not follow.
Idempotent.
"""
import os
import sys

ANCHOR = """\tret = jadard->desc->init(jadard);
\tif (ret)
\t\treturn ret;

\treturn 0;
}
"""
LOG = """\tret = jadard->desc->init(jadard);
\tif (ret)
\t\treturn ret;

\t{
\t\tu8 mode = 0, id[3] = {};
\t\tint r = mipi_dsi_dcs_get_power_mode(jadard->dsi, &mode);
\t\tint n = mipi_dsi_dcs_read(jadard->dsi, MIPI_DCS_GET_DISPLAY_ID, id, sizeof(id));

\t\tdev_info(&jadard->dsi->dev, "panel power mode 0x%02x (%d), id %3phN (%d)\\n",
\t\t\t mode, r, id, n);
\t}

\treturn 0;
}
"""


def main():
    path = os.path.join(sys.argv[1], "drivers/gpu/drm/panel/panel-jadard-jd9365da-h3.c")
    with open(path) as f:
        s = f.read()
    if "panel power mode 0x%02x" in s:
        return
    assert s.count(ANCHOR) == 1, "jd9365-power-mode-log: jadard_prepare's end is not the one expected"
    with open(path, "w") as f:
        f.write(s.replace(ANCHOR, LOG))


if __name__ == "__main__":
    main()
