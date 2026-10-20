#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Give the MT7663 on SDIO the radio temperature the PCIe MT7615 already reports.

    mt7663s-temperature.py <linux tree>

mt76 asks for the temperature with the extended command THERMAL_CTRL and reads the reply's first
32-bit word, and registers that as a hwmon device (mt7615_phy0, temp1_input) on PCIe only. Two things
keep the MT7663 on SDIO from reporting it:

  - nothing registers the hwmon device on the SDIO and USB path; this does, as pci_init.c does;
  - the MT7663's firmware puts the temperature in the reply's second word, the first being 0 (as
    reported for an MT7663E on linux-wireless); on an MT7663 the second word is read.

Amazon's own query for the family, the legacy CMD_ID_GET_TEMPERATURE (0x8c), is no use here: this
firmware answers it with an empty event 0xfd (read on the 2nd gen Show 8, 2026-10-16).
Idempotent.
"""
import os
import sys

MT76 = "drivers/net/wireless/mediatek/mt76/mt7615"

EDITS = [
    ("mcu.c",
     """\t} else if (cmd == MCU_EXT_CMD(THERMAL_CTRL)) {
\t\tskb_pull(skb, sizeof(*rxd));
\t\tret = le32_to_cpu(*(__le32 *)skb->data);
""",
     """\t} else if (cmd == MCU_EXT_CMD(THERMAL_CTRL)) {
\t\tskb_pull(skb, sizeof(*rxd));
\t\t/* the MT7663's firmware leaves the first word 0 and puts the temperature in the second */
\t\tret = le32_to_cpu(*(__le32 *)&skb->data[is_mt7663(mdev) ? 4 : 0]);
"""),
    ("usb_sdio.c",
     """\tmt7615_init_txpower(dev, &dev->mphy.sband_5g.sband);

\treturn mt7615_init_debugfs(dev);
""",
     """\tmt7615_init_txpower(dev, &dev->mphy.sband_5g.sband);

\terr = mt7615_thermal_init(dev);
\tif (err)
\t\treturn err;

\treturn mt7615_init_debugfs(dev);
"""),
]

for name, anchor, patched in EDITS:
    path = os.path.join(sys.argv[1], MT76, name)
    src = open(path).read()
    if patched in src:
        continue
    if src.count(anchor) != 1:
        sys.exit("%s: not where mt7663s-temperature.py expects it" % path)
    open(path, "w").write(src.replace(anchor, patched))
