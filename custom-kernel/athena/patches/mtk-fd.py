#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Add the MT8183's face detector (FDVT) driver to the kernel tree, and its node to athena's DTS.

    mtk-fd.py KTREE DTS

patches/mtk-fd/ is the ChromeOS 5.10 kernel's mtk-isp/fd driver (mtk_fd_40.c, mtk_fd.h), brought to
7.2: a V4L2 memory-to-memory device (/dev/video* "mtk-fd-4.0") that takes a YUV picture and gives back
the faces in it. The SCP plans each detection and programs the FDVT; on this board its register
accesses reach the FDVT through the AP (patches/scp-vdec-proxy, the same trap as the decoder's). The
node is ChromeOS's mt8183.dtsi's. Idempotent.
"""
import os
import shutil
import sys

tree, dts = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
d = os.path.join(tree, "drivers/media/platform/mediatek/fd")
os.makedirs(d, exist_ok=True)
for f in ("mtk_fd.h", "mtk_fd_40.c"):
    shutil.copyfile(os.path.join(here, "mtk-fd", f), os.path.join(d, f))
open(os.path.join(d, "Makefile"), "w").write(
    "# SPDX-License-Identifier: GPL-2.0\nmtk-fd-objs += mtk_fd_40.o\nobj-$(CONFIG_VIDEO_MEDIATEK_FD) += mtk-fd.o\n")
open(os.path.join(d, "Kconfig"), "w").write("""# SPDX-License-Identifier: GPL-2.0
config VIDEO_MEDIATEK_FD
	tristate "MediaTek MT8183 face detection (FDVT)"
	depends on VIDEO_DEV && MTK_SCP
	depends on ARCH_MEDIATEK || COMPILE_TEST
	select VIDEOBUF2_DMA_CONTIG
	select V4L2_MEM2MEM_DEV
	select MEDIA_CONTROLLER
	help
	  The MT8183's face detector as a memory-to-memory device: a YUV picture in, the faces in
	  it out, planned by the SCP.
""")
p = os.path.join(tree, "drivers/media/platform/mediatek/Kconfig")
s = open(p).read()
if "mediatek/fd/Kconfig" not in s:
    open(p, "a").write('source "drivers/media/platform/mediatek/fd/Kconfig"\n')
p = os.path.join(tree, "drivers/media/platform/mediatek/Makefile")
s = open(p).read()
if "fd/" not in s:
    open(p, "a").write("obj-y += fd/\n")

NODE = """
/* The face detector (patches/mtk-fd.py), as ChromeOS's mt8183.dtsi has it */
&{/soc} {
	fd: fd@1502b000 {
		compatible = "mediatek,mt8183-fd";
		mediatek,scp = <&scp>;
		iommus = <&iommu M4U_PORT_CAM_FDVT_RP>,
			 <&iommu M4U_PORT_CAM_FDVT_WR>,
			 <&iommu M4U_PORT_CAM_FDVT_RB>;
		reg = <0 0x1502b000 0 0x1000>;
		interrupts = <GIC_SPI 269 IRQ_TYPE_LEVEL_LOW>;
		clocks = <&imgsys CLK_IMG_FDVT>;
		clock-names = "fd";
		power-domains = <&spm MT8183_POWER_DOMAIN_ISP>;
	};
};
"""
s = open(dts).read()
if "mediatek,mt8183-fd" not in s:
    open(dts, "w").write(s + NODE)
print("fd: in place")
