#!/bin/sh
# Install the MT8163 SODI cpuidle driver (patches/mt8163-sodi.c). Idempotent. It only registers
# when enabled at run time: /sys/module/mt8163_sodi/parameters/enable.
set -e
D=${KDIR:-/src/linux-7.2.11}/drivers/cpuidle
cp /patches/mt8163-sodi.c $D/mt8163-sodi.c
grep -q mt8163-sodi $D/Makefile || printf '\n# MT8163 screen-on deep idle (TECHO5)\nobj-$(CONFIG_ARCH_MEDIATEK) += mt8163-sodi.o\n' >> $D/Makefile
grep -n mt8163-sodi $D/Makefile
