#!/bin/sh
# Install patches/mt8163-vcodec.c, /dev/Vcodec for MediaTek's codec libraries, into drivers/misc. Idempotent.
set -eu
D=${KDIR:-/src/linux-7.2.11}/drivers/misc
cp /patches/mt8163-vcodec.c "$D/mt8163-vcodec.c"
grep -q mt8163-vcodec "$D/Makefile" || printf '\nobj-$(CONFIG_MT8163_VCODEC) += mt8163-vcodec.o\n' >> "$D/Makefile"
grep -q 'config MT8163_VCODEC' "$D/Kconfig" || sed -i 's|^endmenu$|config MT8163_VCODEC\n\ttristate "MT8163 video codec for MediaTek'"'"'s codec libraries (/dev/Vcodec)"\n\tdepends on ARCH_MEDIATEK \&\& OF \&\& COMPAT\n\thelp\n\t  Powers the MT8163'"'"'s H.264 decoder and encoder and gives MediaTek'"'"'s 32-bit\n\t  codec libraries their registers, interrupts and buffers.\n\nendmenu|' "$D/Kconfig"
