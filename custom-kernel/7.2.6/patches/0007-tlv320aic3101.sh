#!/bin/sh
# Install the ported TLV320AIC3101 ADC codec driver (codec-work/, from patches/port-aic3101.py). Idempotent.
set -e
S=${KDIR:-/src/linux-7.2.6}/sound/soc/codecs
cp /host/codec-work/tlv320aic3101.c /host/codec-work/tlv320aic3101.h $S/
grep -q tlv320aic3101 $S/Makefile || cat >> $S/Makefile <<'M'

# Amazon's TLV320AIC3101 ADC driver (Echo Show 5)
snd-soc-tlv320aic3101-y := tlv320aic3101.o
obj-$(CONFIG_SND_SOC_TLV320AIC3101) += snd-soc-tlv320aic3101.o
M
grep -q SND_SOC_TLV320AIC3101 $S/Kconfig || python3 - <<'PY'
import os
p=os.environ.get('KDIR', '/src/linux-7.2.6')+'/sound/soc/codecs/Kconfig'; s=open(p).read()
anchor='config SND_SOC_TLV320AIC31XX\n'
add='''config SND_SOC_TLV320AIC3101
	tristate "Texas Instruments TLV320AIC3101 ADC (Amazon Echo Show 5)"
	depends on I2C
	help
	  Amazon's driver for the TLV320AIC3101 microphone ADC of the Echo Show 5,
	  whose I2S output feeds the audio FPGA rather than the SoC.

'''
assert anchor in s
s=s.replace(anchor, add+anchor, 1); open(p,'w').write(s)
PY
