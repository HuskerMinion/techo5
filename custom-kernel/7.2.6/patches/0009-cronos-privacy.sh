#!/bin/sh
set -eu
D=${KDIR:-/src/linux-7.2.6}/drivers/input/misc
cp /patches/gpio-privacy-cronos.c "$D/gpio-privacy-cronos.c"
grep -q gpio-privacy-cronos "$D/Makefile" || printf '\nobj-$(CONFIG_INPUT_CRONOS_PRIVACY) += gpio-privacy-cronos.o\n' >> "$D/Makefile"
grep -q INPUT_CRONOS_PRIVACY "$D/Kconfig" || cat >> "$D/Kconfig" <<'K'

config INPUT_CRONOS_PRIVACY
	tristate "Echo Show 5 hardware privacy latch"
	depends on OF && GPIOLIB
	help
	  Handle the physical privacy button and expose the mute latch state.
K
