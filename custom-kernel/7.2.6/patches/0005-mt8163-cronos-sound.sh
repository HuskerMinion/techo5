#!/bin/sh
# Install the cronos sound card (patches/mt8163-cronos.c) into the tree. Idempotent.
set -e
S=${KDIR:-/src/linux-7.2.6}/sound/soc/mediatek
cp /patches/mt8163-cronos.c $S/mt8163/mt8163-cronos.c
grep -q mt8163-cronos $S/mt8163/Makefile || cat >> $S/mt8163/Makefile <<'M'

# Machine driver: Amazon Echo Show 5 (cronos)
snd-soc-mt8163-cronos-y := mt8163-cronos.o
obj-$(CONFIG_SND_SOC_MT8163_CRONOS) += snd-soc-mt8163-cronos.o
M
python3 - <<'PY'
import os, re
p=os.environ.get('KDIR', '/src/linux-7.2.6')+'/sound/soc/mediatek/Kconfig'; s=open(p).read()
anchor='config SND_SOC_MT8173\n'
add='''config SND_SOC_MT8163_CRONOS
	tristate "ASoC Audio driver for the Amazon Echo Show 5 (cronos) with TAS5805M"
	depends on SND_SOC_MT8163 && I2C
	select SND_SOC_TAS5805M
	help
	  The sound card of the Amazon Echo Show 5 (2021): the MT8163 AFE's I2S1
	  feeding a TAS5805M amplifier. The microphones are not on the AFE.

'''
if 'config SND_SOC_MT8163_CRONOS\n' in s:
    s = re.sub(r'config SND_SOC_MT8163_CRONOS\n.*?(?=config |\Z)', add, s, count=1, flags=re.S)
else:
    assert anchor in s
    s = s.replace(anchor, add + anchor, 1)
open(p, 'w').write(s)
PY
grep -n "cronos" $S/Kconfig $S/mt8163/Makefile | head -4
