#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Patch mainline mt8183 AFE for athena's echo reference: DL1 into UL2 (the AWB memif).

UL2's interconnect mixers (O05/O06) already have DL1_CH1/DL1_CH2 switches (I05/I06), but no route
reaches them, so the switches are never created as controls and the path never powers. This adds
the two routes, so "UL2_CH1 DL1_CH1" and "UL2_CH2 DL1_CH2" appear and the machine driver's
DL1_AWB_Record link (hw:0,1) records what DL1 sends I2S1, as Amazon's kernel's DL1_AWB_Record does.

The routes have to be the AFE's own: its platform probe creates its widgets' controls
(snd_soc_dapm_new_widgets in mtk-afe-platform-driver.c) before the card adds its routes, so a
route the machine driver adds to one of these mixers gets no control and stays disconnected.

Usage: patch-afe-awb-dl1.py KTREE   (idempotent)
"""
import sys

p = sys.argv[1] + "/sound/soc/mediatek/mt8183/mt8183-afe-pcm.c"
MARK = "athena: DL1 into AWB"
s = open(p).read()
if MARK not in s:
    a = '''	{"UL2_CH1", "I2S2_CH1", "I2S2"},
	{"UL2_CH2", "I2S2_CH2", "I2S2"},
'''
    assert s.count(a) == 1, "UL2 routes"
    s = s.replace(a, a + '''	/* athena: DL1 into AWB, the echo reference (DL1_AWB_Record) */
	{"UL2_CH1", "DL1_CH1", "DL1"},
	{"UL2_CH2", "DL1_CH2", "DL1"},
''')
    open(p, "w").write(s)
print("patched")
