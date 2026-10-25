#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Patch a mainline 6.18 tree for athena's real display: MediaTek DRM + DSI + the ILI9881C/JD9365 panels.

What it does:
  1. Applies the panel patches from panel/ (written 2026-10-14 against 6.18.56, still apply cleanly):
       0001/0002  ilitek,ili9881c binding + driver: 5 "amazon,athena-ili9881c-*" panels
                  (unit 1 = amazon,athena-ili9881c-boe)
       0003/0004  jadard,jd9365da-h3 binding + driver: 5 "amazon,athena-jd9365-*" panels (other units)
       0005       the JD9365 BOE (new) panel (0x35) on Amazon's DSI link settings (#187)
  2. mediatek-drm (mtk_drm_drv.c): evict the bootloader's simple-framebuffer when the DRM device
     binds (athena: evict-simplefb). Upstream mediatek-drm never does this (ChromeOS has no simplefb),
     so without it simplefb keeps /dev/fb0 and the DRM fbdev emulation lands on /dev/fb1, while the
     daemon draws to fb0. With it, simplefb is unregistered first and the DRM fbdev takes fb0; if
     mediatek-drm never binds, simplefb stays and the screen works as before. Same call as
     rockchip/sun4i/meson make.
  3. Adds src/panel/panel-athena-select.c (athena: panel select): at boot it sets the panel node's
     compatible to the unit's own screen, by the LCM name LK leaves in /chosen atag,videolfb (or the
     jd936x_id nibble), so one boot image drives every athena screen. The device tree lists unit 1's.
  4. panel-jadard-jd9365da-h3 takes its reset line deasserted at probe (athena: reset deasserted at
     probe), as panel-ilitek-ili9881c does, so LK's picture stays up until the first modeset.

Usage: patch-display.py KTREE   (idempotent: each step is skipped when its marker is present)
"""
import os
import subprocess
import sys

tree = sys.argv[1]
here = os.path.dirname(os.path.abspath(__file__))
panel = os.path.join(here, "panel")


def read(path):
    with open(os.path.join(tree, path)) as f:
        return f.read()


def apply_patch(name, path, marker):
    """Apply panel/<name> unless <path> already holds <marker>."""
    if marker in read(path):
        return
    subprocess.run(["patch", "-p1", "-N", "-s", "--no-backup-if-mismatch",
                    "-i", os.path.join(panel, name)], cwd=tree, check=True)
    assert marker in read(path), (name, "marker missing after patch")


def edit(path, mark, pairs):
    p = os.path.join(tree, path)
    s = open(p).read()
    if mark in s:
        return
    for a, b in pairs:
        assert s.count(a) == 1, (path, a[:70], s.count(a))
        s = s.replace(a, b)
    open(p, "w").write(s)


BIND = "Documentation/devicetree/bindings/display/panel/"
apply_patch("0001-dt-bindings-ili9881c-add-amazon-athena-panels.patch",
            BIND + "ilitek,ili9881c.yaml", "amazon,athena-ili9881c-boe")
# mediatek-dsi finds its panel only through the OF graph (dsi port@1 -> panel port), which the
# ili9881c binding does not list yet (jd9365da-h3 does): allow it, or dtbs_check flags the node.
edit(BIND + "ilitek,ili9881c.yaml", "  port: true\n", [
    ("  backlight: true\n  power-supply: true\n",
     "  backlight: true\n  port: true\n  power-supply: true\n"),
])
# athena's one panel node carries the JD9365 driver's supplies too (panel-athena-select.c picks the
# driver at boot): allow them on the ili9881c binding as well.
edit(BIND + "ilitek,ili9881c.yaml", "  vccio-supply: true\n", [
    ("  port: true\n  power-supply: true\n",
     "  port: true\n  power-supply: true\n  vccio-supply: true\n  vdd-supply: true\n"),
])
apply_patch("0002-drm-panel-ili9881c-add-amazon-athena-panels.patch",
            "drivers/gpu/drm/panel/panel-ilitek-ili9881c.c", "amazon,athena-ili9881c-boe")
apply_patch("0003-dt-bindings-jd9365da-add-amazon-athena-panels.patch",
            BIND + "jadard,jd9365da-h3.yaml", "amazon,athena-jd9365-boe")
apply_patch("0004-drm-panel-jd9365da-add-amazon-athena-panels.patch",
            "drivers/gpu/drm/panel/panel-jadard-jd9365da-h3.c", "amazon,athena-jd9365-boe")
# athena: reset deasserted at probe. The driver takes its reset line asserted, which puts the panel
# in reset as soon as mediatek-dsi adds it: LK's picture goes, and stays gone if mediatek-drm never
# binds. Taken deasserted, as panel-ilitek-ili9881c does, the picture stays until the first modeset;
# prepare pulses the line itself either way.
edit("drivers/gpu/drm/panel/panel-jadard-jd9365da-h3.c", "athena: reset deasserted at probe", [
    ("""	jadard->reset = devm_gpiod_get(dev, "reset", GPIOD_OUT_HIGH);
""",
     """	/* athena: reset deasserted at probe, so LK's picture stays until prepare */
	jadard->reset = devm_gpiod_get(dev, "reset", GPIOD_OUT_LOW);
"""),
])
# athena: boe-new link. The 0x35 panel on Amazon's DSI link settings, without which it flickers
# (#187): clock lane non-continuous, 478 Mbps per lane, EoT packets on.
apply_patch("0005-drm-panel-jd9365da-athena-boe-new-link.patch",
            "drivers/gpu/drm/panel/panel-jadard-jd9365da-h3.c", "athena: boe-new link")

edit("drivers/gpu/drm/mediatek/mtk_drm_drv.c", "athena: evict-simplefb", [
    ("#include <linux/component.h>\n",
     "#include <linux/aperture.h>\n"
     "#include <linux/component.h>\n"),
    ("""	if (!mtk_drm_get_all_drm_priv(dev)) {
		put_device(private->mutex_dev);
		return 0;
	}

	drm = drm_dev_alloc(&mtk_drm_driver, dev);
""",
     """	if (!mtk_drm_get_all_drm_priv(dev)) {
		put_device(private->mutex_dev);
		return 0;
	}

	/*
	 * athena: evict-simplefb. The bootloader's picture is shown through a
	 * simple-framebuffer until now; unregister it so that the fbdev
	 * emulation below takes its place (and /dev/fb0).
	 */
	ret = aperture_remove_all_conflicting_devices(mtk_drm_driver.name);
	if (ret)
		goto err_put_dev;

	drm = drm_dev_alloc(&mtk_drm_driver, dev);
"""),
])
src = os.path.join(here, "..", "src", "panel", "panel-athena-select.c")
dst = os.path.join(tree, "drivers/gpu/drm/panel/panel-athena-select.c")
with open(src, "rb") as f:
    new = f.read()
if not os.path.exists(dst) or open(dst, "rb").read() != new:
    with open(dst, "wb") as f:
        f.write(new)
edit("drivers/gpu/drm/panel/Makefile", "panel-athena-select.o", [
    ("obj-$(CONFIG_DRM_PANEL_ILITEK_ILI9881C) += panel-ilitek-ili9881c.o\n",
     "obj-$(CONFIG_DRM_PANEL_ILITEK_ILI9881C) += panel-ilitek-ili9881c.o\n"
     "# athena: panel select. Built in only: it runs before the DSI host adds the panel.\n"
     "ifeq ($(CONFIG_DRM_PANEL_ILITEK_ILI9881C),y)\n"
     "obj-y += panel-athena-select.o\n"
     "endif\n"),
])
print("patched")
