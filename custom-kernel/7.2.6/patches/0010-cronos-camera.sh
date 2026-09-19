#!/bin/sh
set -eu
D=${KDIR:-/src/linux-7.2.6}/drivers/media/platform/mediatek
cp /patches/mt8163-cronos-camera.c "$D/mt8163-cronos-camera.c"
python3 /patches/port-ov02b10.py /host/amazon-kernel/drivers/misc/mediatek/imgsensor/src/mt8163/ov02b10_mipi_raw/ov02bmipiraw_Sensor.c "$D/ov02b10-init.h"
grep -q mt8163-cronos-camera "$D/Makefile" || printf '\nobj-$(CONFIG_VIDEO_CRONOS_CAMERA) += mt8163-cronos-camera.o\n' >> "$D/Makefile"
grep -q VIDEO_CRONOS_CAMERA "$D/Kconfig" || cat >> "$D/Kconfig" <<'K'

config VIDEO_CRONOS_CAMERA
	bool "Echo Show 5 OV02B10 capture"
	depends on VIDEO_DEV && I2C && GPIOLIB && OF
	select VIDEOBUF2_VMALLOC
	select VIDEOBUF2_V4L2
	help
	  Capture packed RAW10 frames from the fixed OV02B10 and MT8163 pipeline.
K
