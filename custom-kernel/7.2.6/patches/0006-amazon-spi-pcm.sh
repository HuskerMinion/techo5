#!/bin/sh
# Install the microphone FPGA SPI capture driver (patches/amazon-spi-pcm.c). Idempotent.
set -e
S=${KDIR:-/src/linux-7.2.6}/sound/soc/mediatek
cp /patches/amazon-spi-pcm.c $S/mt8163/amazon-spi-pcm.c
grep -q amazon-spi-pcm $S/mt8163/Makefile || cat >> $S/mt8163/Makefile <<'M'

# Amazon Echo Show 5: the microphone FPGA on SPI
snd-soc-amazon-spi-pcm-y := amazon-spi-pcm.o
obj-$(CONFIG_SND_SOC_AMAZON_SPI_PCM) += snd-soc-amazon-spi-pcm.o
M
grep -q SND_SOC_AMAZON_SPI_PCM $S/Kconfig || python3 - <<'PY'
import os
p=os.environ.get('KDIR', '/src/linux-7.2.6')+'/sound/soc/mediatek/Kconfig'; s=open(p).read()
anchor='config SND_SOC_MT8173\n'
add='''config SND_SOC_AMAZON_SPI_PCM
	tristate "Amazon Echo Show 5 microphone FPGA on SPI"
	depends on SPI
	help
	  Capture from the iCE40 FPGA that carries the Echo Show 5's two microphones
	  and the playback loopback over SPI, as 4-channel 24-bit frames.

'''
assert anchor in s
s=s.replace(anchor, add+anchor, 1); open(p,'w').write(s)
PY
