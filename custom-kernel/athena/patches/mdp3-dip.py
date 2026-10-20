#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Add the MT8183's ISP pass 2 (DIP) driver to the kernel tree, and its nodes to athena's DTS.

    mdp3-dip.py KTREE DTS

patches/mdp3-dip/mtk-mdp3-dip.c goes in beside the MDP3 driver whose GCE path runs its jobs
(VIDEO_MEDIATEK_DIP, drivers/media/platform/mediatek/mdp3). The device tree gets, as the ChromeOS
5.10 kernel's mt8183.dtsi has them:

  - the DIP (0x15022000, in the ISP's power domain, behind larb5), for that driver: its clocks, its
    IOMMU ports (the DIP's own DMAs on larb5) and the SCP;
  - the MDP3 components patches/mdp3-mt8183-isp.py makes the MDP3 driver find: the ISP head
    (IMGI, its registers from 0x15020000 through GCE subsys SUBSYS_1502XXXX) and the two direct
    links from the ISP into the MDP (CAMIN, CAMIN2) with their MMSYS clocks. Idempotent.
"""
import os
import shutil
import sys

tree, dts = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
d = os.path.join(tree, "drivers/media/platform/mediatek/mdp3")
shutil.copyfile(os.path.join(here, "mdp3-dip", "mtk-mdp3-dip.c"), os.path.join(d, "mtk-mdp3-dip.c"))

p = os.path.join(d, "Makefile")
s = open(p).read()
if "mtk-mdp3-dip.o" not in s:
    open(p, "a").write("obj-$(CONFIG_VIDEO_MEDIATEK_DIP) += mtk-mdp3-dip.o\n")
p = os.path.join(d, "Kconfig")
s = open(p).read()
if "VIDEO_MEDIATEK_DIP" not in s:
    open(p, "a").write("""
config VIDEO_MEDIATEK_DIP
	bool "MediaTek MT8183 ISP pass 2 (DIP)"
	depends on VIDEO_MEDIATEK_MDP3=y
	help
	  The MT8183's ISP pass 2 as a memory-to-memory device: packed 10-bit Bayer in, developed
	  and scaled YUV out, composed by the SCP and run by the MDP3 driver's GCE path.
""")

NODES = """
/* The ISP's pass 2 (patches/mdp3-dip.py) */
&{/soc} {
	dip: dip@15022000 {
		compatible = "mediatek,mt8183-dip";
		reg = <0 0x15022000 0 0x6000>;
		interrupts = <GIC_SPI 268 IRQ_TYPE_LEVEL_LOW>;
		clocks = <&imgsys CLK_IMG_LARB5>, <&imgsys CLK_IMG_DIP>;
		clock-names = "larb5", "dip";
		power-domains = <&spm MT8183_POWER_DOMAIN_ISP>;
		iommus = <&iommu M4U_PORT_CAM_IMGI>, <&iommu M4U_PORT_CAM_IMG2O>,
			 <&iommu M4U_PORT_CAM_IMG3O>, <&iommu M4U_PORT_CAM_VIPI>,
			 <&iommu M4U_PORT_CAM_LCEI>, <&iommu M4U_PORT_CAM_SMXI>,
			 <&iommu M4U_PORT_CAM_SMXO>;
		mediatek,scp = <&scp>;
	};

	mdp3-imgi@15020000 {
		compatible = "mediatek,mt8183-mdp3-imgi";
		reg = <0 0x15020000 0 0x1000>;
		mediatek,gce-client-reg = <&gce SUBSYS_1502XXXX 0 0x1000>;
	};

	mdp3-camin@14000000 {
		compatible = "mediatek,mt8183-mdp3-dl";
		reg = <0 0x14000000 0 0x1000>;
		mediatek,gce-client-reg = <&gce SUBSYS_1400XXXX 0 0x1000>;
		clocks = <&mmsys CLK_MM_MDP_DL_TXCK>, <&mmsys CLK_MM_MDP_DL_RX>;
	};

	mdp3-camin2@14000000 {
		compatible = "mediatek,mt8183-mdp3-dl";
		reg = <0 0x14000000 0 0x1000>;
		mediatek,gce-client-reg = <&gce SUBSYS_1400XXXX 0 0x1000>;
		clocks = <&mmsys CLK_MM_IPU_DL_TXCK>, <&mmsys CLK_MM_IPU_DL_RX>;
	};
};
"""
s = open(dts).read()
if "mediatek,mt8183-dip" not in s:
    if "dt-bindings/gce/mt8183-gce.h" not in s:
        s = s.replace('#include "mt8183.dtsi"', '#include <dt-bindings/gce/mt8183-gce.h>\n#include "mt8183.dtsi"', 1)
    open(dts, "w").write(s + NODES)
print("dip: in place")
