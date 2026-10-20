#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Point the athena device tree at another of athena's panels, and add the camera.

    panel-dts.py <mt8183-amazon-athena.dts> <panel>

The DTS drives the ILI9881C on BOE glass, the panel upstream tested. The other athena panels' init
tables are in tools/linux/athena/kernel/patches/panel, and the DTS's comment at &dsi0 says what using
one takes: its compatible, and for the JD9365 ones the jadard driver's two supplies, vccio-supply =
<&mt6358_vcama2_reg> and vdd-supply = <&lcd_avee>, in place of the ILI9881C driver's power-supply.

<panel> is the part of the compatible after "amazon,athena-": ili9881c-boe (no change),
jd9365-boe-new, and so on. The boot loader names the unit's panel with jd936x_id= on its command line
(docs/install-athena.md, "Which screen"); its low nibble is the variant:
"""
import sys

# The panel the boot loader reports by the low nibble of jd936x_id, from the LK panel names in
# patches/panel (only the ones whose nibble the patches state).
NIBBLES = {4: "jd9365-boe", 5: "jd9365-boe-new", 6: "ili9881c-boe", 7: "jd9365-inx"}
JD9365 = ("kd-hsd", "kd-hsd-new", "boe", "boe-new", "inx")
ILI9881C_LINE = '\t\tcompatible = "amazon,athena-ili9881c-boe", "ilitek,ili9881c";\n'
SUPPLY_LINE = "\t\tpower-supply = <&lcd_avee>;\n"
PWM_LINE = "\t\tpwms = <&pwm0 0 500000>;\n"
# Units whose LED driver does not follow the display PWM at the DTS's 2 kHz (a 500 us period, kukui's):
# on the BOE-new unit 4% and 100% duty looked the same at 2 kHz and were plainly different at 25 kHz.
# Amazon's lcd-backlight node (led_mode 5, the display PWM) sets a divider for 6 to 25 kHz on every
# board revision. That unit has no LM36274/KTZ8864A backlight chip on any I2C bus.
PWM_PERIOD_NS = {"jd9365-boe-new": 40000}

__doc__ += "".join("    nibble %d: %s\n" % kv for kv in sorted(NIBBLES.items()))
__doc__ += """
The camera (every panel) is the node patches/mt8183-athena-camera.c drives. The wiring is Amazon's, from
the athena-pvt device tree in the stock boot image: the OV13855 at 0x36 on I2C2 (with its EEPROM at
0x54, unused), reset GPIO102 and power-down GPIO177 (cam0_rst/cam0_pnd, low while off), MCLK on
CMMCLK0 (GPIO99 function 1), and vcama1 2.8 V, vcamd 1.2 V, vcamio 1.8 V. It takes CSI0 with four
lanes, as MT8183 main cameras do, and CAMSV1 (interrupt 258, as stock's camsv1 node says).
"""
CAMERA = """
/* OV13855 camera: patches/panel-dts.py, driven by patches/mt8183-athena-camera.c */
&mt6358_vcama1_reg {
	regulator-min-microvolt = <2800000>;
	regulator-max-microvolt = <2800000>;
};

&mt6358_vcamd_reg {
	regulator-min-microvolt = <1200000>;
	regulator-max-microvolt = <1200000>;
};

&mt6358_vcamio_reg {
	regulator-min-microvolt = <1800000>;
	regulator-max-microvolt = <1800000>;
};

&pio {
	camera_pins: camera-pins {
		pins-mclk {
			pinmux = <PINMUX_GPIO99__FUNC_CMMCLK0>;
		};
		pins-control {
			pinmux = <PINMUX_GPIO102__FUNC_GPIO102>,
				 <PINMUX_GPIO177__FUNC_GPIO177>;
			output-low;
		};
	};
};

/ {
	camera@1a040000 {
		compatible = "amazon,athena-camera";
		reg = <0 0x1a040000 0 0x8000>,	/* seninf */
		      <0 0x1a050000 0 0x1000>,	/* CAMSV1 */
		      <0 0x11c80000 0 0x2000>;	/* MIPI CSI0 A and B */
		reg-names = "seninf", "camsv", "csi";
		interrupts = <GIC_SPI 258 IRQ_TYPE_LEVEL_LOW>;
		clocks = <&camsys CLK_CAM_CAM>, <&camsys CLK_CAM_CAMTG>,
			 <&camsys CLK_CAM_SENINF>, <&camsys CLK_CAM_CAMSV0>,
			 <&topckgen CLK_TOP_MUX_SENINF>, <&topckgen CLK_TOP_MUX_CAMTG>;
		clock-names = "cam", "camtg", "seninf", "camsv", "seninf-top", "mclk";
		/* 24 MHz MCLK */
		assigned-clocks = <&topckgen CLK_TOP_MUX_CAMTG>;
		assigned-clock-parents = <&topckgen CLK_TOP_UNIVP_192M_D8>;
		power-domains = <&spm MT8183_POWER_DOMAIN_CAM>;
		/* larb6; which port the CAMSV writes through is not known, so all candidates */
		iommus = <&iommu M4U_PORT_CAM_IMGO>, <&iommu M4U_PORT_CAM_SOCO>,
			 <&iommu M4U_PORT_CAM_SOC1>, <&iommu M4U_PORT_CAM_SOC2>;
		pinctrl-names = "default";
		pinctrl-0 = <&camera_pins>;
		dovdd-supply = <&mt6358_vcamio_reg>;
		avdd-supply = <&mt6358_vcama1_reg>;
		dvdd-supply = <&mt6358_vcamd_reg>;
		reset-gpios = <&pio 102 GPIO_ACTIVE_LOW>;
		powerdown-gpios = <&pio 177 GPIO_ACTIVE_LOW>;
		sensor-bus = <&i2c2_gpio>;
	};
};
"""


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    path, panel = sys.argv[1], sys.argv[2]
    with open(path) as f:
        s = f.read()
    assert "amazon,athena-camera" not in s, "panel-dts: the DTS already has the camera"
    # The DTS's own camera section (the seninf/CAMSV chain tools/linux/athena's kernel drives) is the
    # last in the file. This kernel drives the same sensor, pins and registers through the node below,
    # and the OV13855's address on I2C2 is taken by whichever node holds it first, so that section
    # goes: the two do not run together.
    banner = s.find("/* ======================= Camera ======================= */")
    if banner >= 0:
        s = s[:banner].rstrip("\n") + "\n"
    assert "i2c2_gpio: i2c-gpio-2" in s, "panel-dts: the DTS's bit-banged I2C2 is not the one expected"
    s += CAMERA
    if panel == "ili9881c-boe":
        with open(path, "w") as f:
            f.write(s)
        return
    kind, _, variant = panel.partition("-")
    if kind != "jd9365" or variant not in JD9365:
        sys.exit("panel-dts: %s is not one of ili9881c-boe, %s" % (panel, ", ".join("jd9365-" + v for v in JD9365)))
    assert s.count(ILI9881C_LINE) == 1, "panel-dts: the DTS's panel compatible is not the one expected"
    assert s.count(SUPPLY_LINE) == 1, "panel-dts: the DTS's panel supply is not the one expected"
    s = s.replace(ILI9881C_LINE, '\t\tcompatible = "amazon,athena-%s", "jadard,jd9365da-h3";\n' % panel)
    # The DTS gives the panel both drivers' supplies since it learned to pick the panel at boot
    # (panel-athena-select.c); an older one has only the ILI9881C's, which the JD9365's replace.
    if "vccio-supply = <&mt6358_vcama2_reg>;" not in s:
        s = s.replace(SUPPLY_LINE, "\t\tvccio-supply = <&mt6358_vcama2_reg>;\n\t\tvdd-supply = <&lcd_avee>;\n")
    if panel in PWM_PERIOD_NS:
        assert s.count(PWM_LINE) == 1, "panel-dts: the DTS's backlight PWM is not the one expected"
        s = s.replace(PWM_LINE, "\t\tpwms = <&pwm0 0 %d>;\n" % PWM_PERIOD_NS[panel])
    with open(path, "w") as f:
        f.write(s)
    print("panel-dts: amazon,athena-%s" % panel)


if __name__ == "__main__":
    main()
