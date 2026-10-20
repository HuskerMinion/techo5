#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""The AP's side of the MT8183's MCDI on athena: patches/mcdi/athena-mcdi.c (drivers/soc/mediatek).

    mcdi.py KTREE DTS [CPUS]

The driver goes in built in (on with ARCH_MEDIATEK, a no-op on other machines). CPUS, a list like
"7" or "4,5,6,7", lets those cores use the idle states. With all eight (the build's default) the DTS
keeps mainline's: CPU_SLEEP (PSCI 0x00010001) and the cluster's CLUSTER_SLEEPn (0x01010001) on every
core, which is what every image so far has run (research/mcdi-cpuidle.md, "Cluster level"). With
fewer, every core gets CPU_SLEEP alone (the PSCI cpuidle driver takes a state on all or none) and the
driver holds the others in WFI (MCDI_IDLE_CPUS). Without CPUS the DTS is left alone and the command
line's cpuidle.off=1 keeps the cores in WFI. Idempotent.
"""
import os
import sys

tree, dts = sys.argv[1], sys.argv[2]
cpus = [int(c) for c in sys.argv[3].split(",")] if len(sys.argv) > 3 and sys.argv[3] else None
here = os.path.dirname(os.path.abspath(__file__))
d = os.path.join(tree, "drivers/soc/mediatek")
src = open(os.path.join(here, "mcdi", "athena-mcdi.c")).read()
if cpus is not None:
    mask = sum(1 << c for c in cpus)
    src = src.replace("#ifndef MCDI_IDLE_CPUS", "#define MCDI_IDLE_CPUS\t\t%#x\n#ifndef MCDI_IDLE_CPUS" % mask, 1)
open(os.path.join(d, "athena-mcdi.c"), "w").write(src)
p = os.path.join(d, "Makefile")
if "athena-mcdi.o" not in open(p).read():
    open(p, "a").write("obj-$(CONFIG_ARCH_MEDIATEK) += athena-mcdi.o\n")
# The marker is the appended comment itself: the DTS's own comments name this script too.
MARK = "CPU-level idle only (mcdi.py)"
if cpus is not None and sorted(set(cpus)) != list(range(8)):
    s = open(dts).read()
    if MARK not in s:
        node = "\n/* %s, used on cores %s */\n" % (MARK, ",".join(map(str, cpus)))
        for c in range(8):
            node += "&cpu%d { cpu-idle-states = <&CPU_SLEEP>; };\n" % c
        open(dts, "w").write(s + node)
print("mcdi: in place" + (" (idle on CPUs %s)" % cpus if cpus is not None else ""))
