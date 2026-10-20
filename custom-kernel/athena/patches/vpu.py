#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""The MT8183's VPU (Vision P6 core 0) for user space: patches/vpu/techo5_vpu.c (/dev/techo5-vpu).

    vpu.py KTREE DTS

The driver goes into drivers/misc (TECHO5_VPU, built in; TECHO5_VPU_DEBUG adds its debugfs files for
research), and the DTS gets its node: the core's power domain and every clock Fire OS turns on for
core 0 (custom-kernel/athena/research/vpu-vision-p6.md), and a no-map region at 0x7da00000 for the
firmware, which the core reaches at the same address with the M4U bypassed. Idempotent.
"""
import os
import shutil
import sys

tree, dts = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
d = os.path.join(tree, "drivers/misc")
for f in ("techo5_vpu.c", "techo5_vpu_uapi.h"):
    shutil.copyfile(os.path.join(here, "vpu", f), os.path.join(d, f))
p = os.path.join(d, "Makefile")
if "CONFIG_TECHO5_VPU)" not in open(p).read():
    open(p, "a").write("obj-$(CONFIG_TECHO5_VPU) += techo5_vpu.o\n")
p = os.path.join(d, "Kconfig")
s = open(p).read()
if "config TECHO5_VPU\n" not in s:
    s = s.replace("\nendmenu", """
config TECHO5_VPU
	bool "MT8183 VPU core 0 for user space (athena)"
	depends on ARCH_MEDIATEK && ARM64
	help
	  /dev/techo5-vpu: boots the unit's own VPU firmware on the MT8183's Vision P6 core 0, loads
	  one of its algorithms and runs jobs on a data area user space maps. The core is powered
	  only while the device is open.

config TECHO5_VPU_DEBUG
	bool "MT8183 VPU: registers and memory in debugfs, for research"
	depends on TECHO5_VPU && DEBUG_FS
	help
	  debugfs techo5-vpu/{power,regs,mem,log}: the core's power, its register window, its
	  memory region and the firmware's log, as custom-kernel/athena/tools/vpu uses them.

endmenu""", 1)
# the cache maintenance on the data area is arm64's (trees patched before it said so)
s = s.replace("""(athena)"
	depends on ARCH_MEDIATEK
""", """(athena)"
	depends on ARCH_MEDIATEK && ARM64
""", 1)
open(p, "w").write(s)

NODE = """
/* The VPU (patches/vpu.py) */
&{/reserved-memory} {
	vpu_fw: vpu-firmware@7da00000 {
		reg = <0 0x7da00000 0 0x1600000>;
		no-map;
	};
};

&{/soc} {
	vpu@19100000 {
		compatible = "techo5,mt8183-vpu";
		power-domains = <&spm MT8183_POWER_DOMAIN_VPU_CORE0>;
		memory-region = <&vpu_fw>;
		interrupts = <GIC_SPI 292 IRQ_TYPE_LEVEL_LOW>;
		clocks = <&topckgen CLK_TOP_MUX_DSP>, <&topckgen CLK_TOP_MUX_DSP1>,
			 <&topckgen CLK_TOP_MUX_DSP2>, <&topckgen CLK_TOP_MUX_IPU_IF>,
			 <&ipu_core0 CLK_IPU_CORE0_JTAG>, <&ipu_core0 CLK_IPU_CORE0_AXI>,
			 <&ipu_core0 CLK_IPU_CORE0_IPU>, <&ipu_adl CLK_IPU_ADL_CABGEN>,
			 <&ipu_conn CLK_IPU_CONN_DAP_RX>, <&ipu_conn CLK_IPU_CONN_APB2AXI>,
			 <&ipu_conn CLK_IPU_CONN_APB2AHB>, <&ipu_conn CLK_IPU_CONN_IPU_CAB1TO2>,
			 <&ipu_conn CLK_IPU_CONN_IPU1_CAB1TO2>, <&ipu_conn CLK_IPU_CONN_IPU2_CAB1TO2>,
			 <&ipu_conn CLK_IPU_CONN_CAB3TO3>, <&ipu_conn CLK_IPU_CONN_CAB2TO1>,
			 <&ipu_conn CLK_IPU_CONN_CAB3TO1_SLICE>, <&ipu_conn CLK_IPU_CONN_IPU>,
			 <&ipu_conn CLK_IPU_CONN_AHB>, <&ipu_conn CLK_IPU_CONN_AXI>,
			 <&ipu_conn CLK_IPU_CONN_ISP>, <&ipu_conn CLK_IPU_CONN_CAM_ADL>,
			 <&ipu_conn CLK_IPU_CONN_IMG_ADL>, <&mmsys CLK_MM_GALS_IPU2MM>,
			 <&mmsys CLK_MM_GALS_IPU12MM>, <&mmsys CLK_MM_GALS_COMM0>,
			 <&mmsys CLK_MM_GALS_COMM1>, <&mmsys CLK_MM_SMI_COMMON>;
	};
};
"""
s = open(dts).read()
if "techo5,mt8183-vpu" not in s:
    open(dts, "w").write(s + NODE)
print("vpu: in place")
