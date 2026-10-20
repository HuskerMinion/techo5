#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Turn on athena's Mali-G72 in the device tree.

    gpu-dts.py <mt8183-amazon-athena.dts>

The DTS wires the GPU's supplies the way the mainline MT8183 Chromebooks do (vgpu for the core,
vsram_gpu for the mfg_async domain, coupled within 100 mV) and leaves the node disabled, "not needed
to boot". panfrost drives it as it does theirs (mediatek,mt8183b-mali), scaling it between 300 and 800
MHz (simple_ondemand).

It is put under the die's thermal zone as well. mt8183.dtsi's cpu-thermal (one sensor, the power
allocator, throttling from 80 C) cools only the two CPU clusters, so a GPU at full clock would heat the
die with nothing slowing it. The GPU gets #cooling-cells, a power model (dynamic-power-coefficient, in
uW/MHz/V^2: no figure is published for this Mali-G72 MP3, so 1500, in the range Arm's Mali GPUs of its
size are given, which only weighs its share of the budget against the CPUs'), and a third cooling map
beside the CPUs'. Idempotent.
"""
import sys

OFF = """&gpu {
	mali-supply = <&mt6358_vgpu_reg>;
	status = "disabled";
};"""
ON = """&gpu {
	mali-supply = <&mt6358_vgpu_reg>;
	#cooling-cells = <2>;
	dynamic-power-coefficient = <1500>;
	status = "okay";
};

&cpu_thermal {
	cooling-maps {
		map2 {
			trip = <&target>;
			cooling-device = <&gpu THERMAL_NO_LIMIT THERMAL_NO_LIMIT>;
			contribution = <1024>;
		};
	};
};"""

path = sys.argv[1]
s = open(path).read()
if ON in s:
    sys.exit(0)
if s.count(OFF) != 1:
    sys.exit("gpu-dts: %s: the DTS's &gpu is not the one expected" % path)
open(path, "w").write(s.replace(OFF, ON))
