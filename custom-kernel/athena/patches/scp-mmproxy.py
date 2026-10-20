#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Serve the SCP's register accesses to the media blocks from the AP (MT8183, athena).

    scp-mmproxy.py KTREE DTS

patches/scp-mmproxy/mtk_scp_mmproxy.c (its header says how) goes into the SCP driver (mtk_scp), and
scp_ipi_send tells it while a message is out, when the firmware is busiest with the blocks. The
firmware's side is patches/scp-vdec-proxy/proxy.c, linked in by scp-vdec-proxy-fw.py. The DTS gets
the no-map page the proxy keeps its trail of accesses in (mode 3), which survives a watchdog reset.
Idempotent.
"""
import os
import shutil
import sys

tree, dts = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
rp = os.path.join(tree, "drivers/remoteproc")
shutil.copyfile(os.path.join(here, "scp-mmproxy", "mtk_scp_mmproxy.c"), os.path.join(rp, "mtk_scp_mmproxy.c"))


def edit(path, old, new, mark):
    s = open(path).read()
    if mark in s:
        return
    if s.count(old) != 1:
        sys.exit("scp-mmproxy: %s: %r is not there once" % (path, old))
    open(path, "w").write(s.replace(old, new))


edit(os.path.join(rp, "Makefile"),
     "obj-$(CONFIG_MTK_SCP)\t\t\t+= mtk_scp.o mtk_scp_ipi.o\n",
     "obj-$(CONFIG_MTK_SCP)\t\t\t+= mtk_scp.o mtk_scp_ipi.o mtk_scp_mmproxy.o\n", "mtk_scp_mmproxy.o")
edit(os.path.join(tree, "include/linux/remoteproc/mtk_scp.h"),
     "int scp_ipi_send(",
     "/* athena: the AP serves the SCP's accesses to the media blocks (mtk_scp_mmproxy.c) */\n"
     "void mtk_scp_mmproxy_busy(bool busy);\n\nint scp_ipi_send(", "mtk_scp_mmproxy_busy")
ipi = os.path.join(rp, "mtk_scp_ipi.c")
edit(ipi,
     "\tmutex_lock(&scp->send_lock);\n\n\t /* Wait until SCP receives the last command */",
     "\tmutex_lock(&scp->send_lock);\n\tmtk_scp_mmproxy_busy(true);\t/* athena: its accesses to the blocks */\n\n"
     "\t /* Wait until SCP receives the last command */", "mtk_scp_mmproxy_busy(true)")
edit(ipi,
     "unlock_mutex:\n\tmutex_unlock(&scp->send_lock);",
     "unlock_mutex:\n\tmtk_scp_mmproxy_busy(false);\n\tmutex_unlock(&scp->send_lock);", "mtk_scp_mmproxy_busy(false)")

NODE = """
/* The SCP's accesses to the media blocks: the proxy's trail (patches/scp-mmproxy.py) */
&{/reserved-memory} {
	scp-mmproxy-trail@52901000 {
		compatible = "techo5,scp-mmproxy-trail";
		reg = <0 0x52901000 0 0x1000>;
		no-map;
	};
};
"""
s = open(dts).read()
if "techo5,scp-mmproxy-trail" not in s:
    open(dts, "w").write(s + NODE)
print("scp-mmproxy: in place")
