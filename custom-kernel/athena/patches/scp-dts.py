#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Run athena's SCP with the public MT8183 firmware, for the MDP3.

    scp-dts.py <mt8183-amazon-athena.dts>

The SCP (a Cortex-M4 at 0x10500000) is the MT8183's media co-processor: the MDP3 scaler/converter
(mtk-mdp3) is planned on it, as are the H.264 codecs (their registers reached through the AP, scp-mmproxy.py: the
SCP cannot reach them on this unit, README.md), as on the Chromebooks. Fire OS runs Amazon's own SCP firmware (a sensor hub, on the
SCP's own I2C and SPI); the DTS left the SCP off rather than load anything else. Mainline loads
linux-firmware's mediatek/mt8183/scp.img (the kukui SCP, redistributable under LICENCE.mediatek),
resetting the core first, so nothing of Amazon's runs. That firmware is a ChromeOS EC as well: the
cros-ec-rpmsg child gives its console in debugfs (cros_scp/console_log).

That firmware maps its DRAM at the AP's 0x50000000 (kukui's scp_mem_reserved, 41 MB): the pool is
put back at the same place. It is plain RAM here: LK loads the kernel at 0x40200000, the tags at
0x54000000 and the ramdisk at 0x55000000, and reserves nothing below 0x54400000. The SCP's UART pins
(kukui's scp_pins, GPIO110/112) are not claimed: nothing on athena listens there.
Idempotent.
"""
import sys

OFF = """&scp {
	/* mt8183.dtsi points this at a board-defined pool; none here */
	/delete-property/ memory-region;
	status = "disabled";
};"""
ON = """&scp {
	firmware-name = "mediatek/mt8183/scp.img";
	status = "okay";

	/* The firmware is a ChromeOS EC: its console and host commands, over rpmsg */
	cros-ec-rpmsg {
		compatible = "google,cros-ec-rpmsg";
		mediatek,rpmsg-name = "cros-ec-rpmsg";
	};
};"""
POOL_AFTER = "\t\tafe_dma_mem: audio-dma-pool@60000000 {"
POOL = """\t\t/* The SCP firmware's DRAM, where it expects it (scp-dts.py) */
		scp_mem_reserved: memory@50000000 {
			compatible = "shared-dma-pool";
			reg = <0 0x50000000 0 0x2900000>;
			no-map;
		};

"""

path = sys.argv[1]
s = open(path).read()
if ON in s:
    sys.exit(0)
if s.count(OFF) != 1 or s.count(POOL_AFTER) != 1:
    sys.exit("scp-dts: %s: the DTS's &scp or reserved memory is not the one expected" % path)
s = s.replace(OFF, ON).replace(POOL_AFTER, POOL + POOL_AFTER)
open(path, "w").write(s)
