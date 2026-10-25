#!/usr/bin/env python3
"""The Skylight's boot pictures, made from logo/TECHO5_logo.png:

  mklogo.py <TECHO5_logo.png> <kernel-logo.ppm> [<uboot-logo.bmp>]

kernel-logo.ppm  the kernel's fbcon logo (drivers/video/logo/logo_linux_clut224.ppm): 1920x1064 (fbcon
                 refuses a logo as tall as the screen; centered, it leaves 8 rows top and bottom),
                 224 colors, the mark on its own navy
uboot-logo.bmp   1920x1080, 8-bit, for the boot image's resource (logo.bmp)

Both draw the mark exactly where the daemon's splash does on a 1920x1080 panel (echod feature/display
splash.go: logoScale 1.25 times the panel over the Show 5's 960, centered), so the screen does not
change between the kernel's logo and the splash. Needs Pillow.
"""
import sys

from PIL import Image

W, H = 1920, 1080
KERNEL_H = 1064
NAVY = (28, 31, 36)          # the mark's own background (splash.go navy)
SCALE = 1.25 * W / 960       # splash.go splashScale on the Skylight


def main():
    src = Image.open(sys.argv[1]).convert("RGB")
    sw, sh = int(src.size[0] * SCALE), int(src.size[1] * SCALE)
    mark = src.resize((sw, sh), Image.BICUBIC)
    ax, ay = (W - sw) // 2, (H - sh) // 2

    k = Image.new("RGB", (W, KERNEL_H), NAVY)
    k.paste(mark, (ax, ay - (H - KERNEL_H) // 2))
    q = k.quantize(colors=224, method=Image.MEDIANCUT).convert("RGB")
    px = q.load()
    with open(sys.argv[2], "w", newline="\n") as f:
        f.write("P3\n%d %d\n255\n" % (W, KERNEL_H))
        for y in range(KERNEL_H):
            f.write("\n".join("%d %d %d" % px[x, y] for x in range(W)) + "\n")

    if len(sys.argv) > 3:
        full = Image.new("RGB", (W, H), NAVY)
        full.paste(mark, (ax, ay))
        full.quantize(colors=256, method=Image.MEDIANCUT).save(sys.argv[3])


if __name__ == "__main__":
    main()
