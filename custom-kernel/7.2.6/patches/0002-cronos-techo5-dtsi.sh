#!/bin/sh
# Install config/cronos-techo5.dtsi into the tree and include it from the cronos dts. Idempotent.
set -e
D=${KDIR:-/src/linux-7.2.6}/arch/arm64/boot/dts/mediatek
cp /cfg/cronos-techo5.dtsi $D/mt8163-amazon-cronos-techo5.dtsi
grep -q cronos-techo5 $D/mt8163-amazon-cronos.dts || sed -i 's|#include "mt8163-amazon-cronos.dtsi"|#include "mt8163-amazon-cronos.dtsi"\n#include "mt8163-amazon-cronos-techo5.dtsi"|' $D/mt8163-amazon-cronos.dts
grep -q 'interrupt-controller/irq.h' $D/mt8163-amazon-cronos.dts || sed -i 's|/dts-v1/;|/dts-v1/;\n#include <dt-bindings/interrupt-controller/irq.h>|' $D/mt8163-amazon-cronos.dts
