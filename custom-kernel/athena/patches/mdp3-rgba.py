#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Let the MT8183's MDP3 write RGBA (bytes R, G, B, A): V4L2_PIX_FMT_RGBA32.

    mdp3-rgba.py KTREE

The GPU helper takes video frames as RGBA textures (echod's hardware/screen), and the video player has
the MDP3 make them out of the hardware decoder's pictures (feature/video). The MDP3's color set has
RGBA8888 (mtk-mdp3-regs.h: its names give the bytes in memory, as V4L2_PIX_FMT_ABGR32, bytes B G R A,
is MDP_COLOR_BGRA8888), but mainline's MT8183 table offers only the BGRA and ARGB orders. This adds
RGBA32 next to them, for capture and output alike. Idempotent.
"""
import sys

p = sys.argv[1] + "/drivers/media/platform/mediatek/mdp3/mdp_cfg_data.c"
s = open(p).read()
start = s.index("static const struct mdp_format mt8183_formats[] = {")
end = s.index("\n};", start)
table = s[start:end]
if "V4L2_PIX_FMT_RGBA32" in table:
    print(p + ": already patched")
    sys.exit(0)
OLD = """		.pixelformat	= V4L2_PIX_FMT_ARGB32,
		.mdp_color	= MDP_COLOR_ARGB8888,
		.depth		= { 32 },
		.row_depth	= { 32 },
		.num_planes	= 1,
		.flags		= MDP_FMT_FLAG_OUTPUT | MDP_FMT_FLAG_CAPTURE,
	}, {"""
NEW = OLD + """
		/* athena: RGBA, the GPU helper's texture layout (patches/mdp3-rgba.py) */
		.pixelformat	= V4L2_PIX_FMT_RGBA32,
		.mdp_color	= MDP_COLOR_RGBA8888,
		.depth		= { 32 },
		.row_depth	= { 32 },
		.num_planes	= 1,
		.flags		= MDP_FMT_FLAG_OUTPUT | MDP_FMT_FLAG_CAPTURE,
	}, {"""
if table.count(OLD) != 1:
    sys.exit("mdp3-rgba: %s: the MT8183 table's ARGB32 entry is not the one expected" % p)
s = s[:start] + table.replace(OLD, NEW) + s[end:]
open(p, "w").write(s)
print("patched")
