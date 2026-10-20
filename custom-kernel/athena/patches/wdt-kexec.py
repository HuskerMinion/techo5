#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Keep the MediaTek watchdog running through a kexec (athena).

    wdt-kexec.py KTREE

A test kernel is started from the running one with kexec, so that one which hangs is reset by the
hardware watchdog into the boot partition's kernel. kexec runs the reboot notifiers, and the
watchdog core stops a watchdog registered with watchdog_stop_on_reboot() there (SYS_DOWN is what a
restart sends), which would leave a hung test kernel hung. The watchdog is left running instead:
the new kernel's mtk-wdt takes it over at probe (it finds it enabled and keeps pinging it), a
reboot goes through this watchdog's own restart handler anyway, and a halt is reset 31 s later.
Idempotent.
"""
import sys

MARK = "athena: keep running through a kexec"
p = sys.argv[1] + "/drivers/watchdog/mtk_wdt.c"
s = open(p).read()
if MARK in s:
    print(p + ": already patched")
    sys.exit(0)
OLD = "\twatchdog_stop_on_reboot(&mtk_wdt->wdt_dev);\n"
if s.count(OLD) != 1:
    sys.exit("wdt-kexec: %s: watchdog_stop_on_reboot is not where expected" % p)
s = s.replace(OLD, "\t/* %s: a hung kernel it started is reset (patches/wdt-kexec.py) */\n" % MARK)
open(p, "w").write(s)
print("patched")
