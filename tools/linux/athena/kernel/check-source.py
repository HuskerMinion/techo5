#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check a kernel source tarball made by build.sh against the boot image it was built with.

    check-source.py [--config kernel.config] source.tar.xz boot.img

The tarball is what the GPL asks for with the boot image: the whole patched tree that built it.
Checks: one top directory, linux-<version>-techo5-athena, whose Makefile is that version; no
absolute paths, no "..", and no build output left in it (.config, objects, vmlinux); every entry
owned by 0:0 with one fixed time, as build.sh's tar writes it; TECHO5's own files and the patches'
markers there; and techo5-athena.config the configuration the boot image's kernel carries
(CONFIG_IKCONFIG), and the one given with --config (build.sh's kernel.config) when there is one.
A kernel without its configuration in it is checked by the markers and --config alone.

Prints "kver <version>" last, for release.ps1.
"""
import re
import struct
import sys
import tarfile
import zlib

# What the patches leave in the tree: a marker each, in the file it patches (patches/*.py).
MARKERS = {
    "drivers/devfreq/mtk-cci-devfreq.c": b"athena: sram limits",
    "drivers/thermal/mediatek/auxadc_thermal.c": b"athena: devinfo calibration",
    "sound/soc/mediatek/mt8183/mt8183-afe-pcm.c": b"leave cache-only mode before the clocks are enabled",
    "drivers/phy/mediatek/phy-mtk-mipi-csi-0-5.c": b"athena: camera",
    "drivers/media/platform/mediatek/isp/mtk_camsv_hw.c": b"athena: no error interrupts",
    "drivers/gpu/drm/panel/Makefile": b"athena: panel select",
    "drivers/gpu/drm/panel/panel-jadard-jd9365da-h3.c": (b"athena: reset deasserted at probe", b"athena: boe-new link"),
}
# Files TECHO5 adds to the tree.
OURS = [
    "arch/arm64/boot/dts/mediatek/mt8183-amazon-athena.dts",
    "arch/arm64/boot/dts/mediatek/mt8183-amazon-athena-dsp-slot.bin",
    "sound/soc/mediatek/mt8183/mt8183-athena.c",
    "drivers/gpu/drm/panel/panel-athena-select.c",
    "drivers/media/i2c/ov13855.c",
    "drivers/media/i2c/ov13855-tables.inc",
    "drivers/media/platform/mediatek/isp/mtk_seninf.c",
    "drivers/media/platform/mediatek/isp/mtk_camsv_video.c",
]
CONFIG = "techo5-athena.config"
# What a build leaves and distclean takes away.
BUILT = re.compile(r"(^|/)(\.config|vmlinux|Image(\.gz)?|[^/]*\.(o|ko|a|dtb|cmd))$")
TOP = re.compile(r"^linux-(\d+\.\d+\.\d+)-techo5-athena$")


def embedded_config(img):
    """The configuration in the boot image's kernel (IKCONFIG), or None when it carries none."""
    ksize, = struct.unpack_from("<I", img, 8)
    page, = struct.unpack_from("<I", img, 36)
    vmlinux = zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(img[page:page + ksize])
    at = vmlinux.find(b"IKCFG_ST\x1f\x8b\x08")
    if at < 0:
        return None
    return zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(vmlinux[at + 8:])


def check(source, img, config=None):
    """Errors found, and the kernel version."""
    errors = []
    found = {}
    top = kver = None
    times, owners = set(), set()
    with tarfile.open(source, "r|xz") as t:
        for m in t:
            name = m.name
            if name.startswith("/") or ".." in name.split("/"):
                errors.append("unsafe path: %s" % name)
                continue
            first, _, rest = name.partition("/")
            if top is None:
                top = first
                mt = TOP.match(top)
                if not mt:
                    errors.append("top directory %s is not linux-<version>-techo5-athena" % top)
                else:
                    kver = mt.group(1)
            elif first != top:
                errors.append("a second top directory: %s" % first)
                continue
            times.add(m.mtime)
            owners.add((m.uid, m.gid))
            if "/home/" in "/" + name:
                errors.append("a home directory in a path: %s" % name)
            if m.isfile() and BUILT.search(rest):
                errors.append("build output: %s" % name)
            if m.isfile() and (rest in MARKERS or rest in OURS or rest in (CONFIG, "Makefile")):
                found[rest] = t.extractfile(m).read()
    if top is None:
        return ["empty archive"], None
    if len(times) != 1:
        errors.append("entries carry %d different times, not one fixed time" % len(times))
    if owners != {(0, 0)}:
        errors.append("entries are owned by %s, not 0:0" % sorted(owners))
    mk = found.get("Makefile", b"")
    v = [re.search(rb"^%s = (\d+)$" % k, mk, re.M) for k in (b"VERSION", b"PATCHLEVEL", b"SUBLEVEL")]
    if kver and (not all(v) or ".".join(x.group(1).decode() for x in v) != kver):
        errors.append("the Makefile is not linux-%s" % kver)
    for f, marks in MARKERS.items():
        for mark in marks if isinstance(marks, tuple) else (marks,):
            if mark not in found.get(f, b""):
                errors.append("%s has no %r: not the patched tree" % (f, mark.decode()))
    for f in OURS:
        if f not in found:
            errors.append("%s is missing" % f)
    cfg = found.get(CONFIG)
    if cfg is None:
        errors.append("%s is missing" % CONFIG)
    else:
        want = embedded_config(img)
        if want is not None and cfg != want:
            errors.append("%s is not the configuration the boot image's kernel was built with" % CONFIG)
        if config is not None and cfg != config:
            errors.append("%s is not the build's kernel.config" % CONFIG)
        if want is None and config is None:
            print("note: the boot image's kernel carries no configuration and none was given; "
                  "checked by the patch markers only")
    return errors, kver


def main(argv):
    args = argv[1:]
    config = None
    if len(args) >= 2 and args[0] == "--config":
        with open(args[1], "rb") as f:
            config = f.read()
        args = args[2:]
    if len(args) != 2:
        print("usage: check-source.py [--config kernel.config] source.tar.xz boot.img", file=sys.stderr)
        return 2
    with open(args[1], "rb") as f:
        img = f.read()
    try:
        errors, kver = check(args[0], img, config)
    except (tarfile.TarError, zlib.error, struct.error, EOFError, OSError) as e:
        errors, kver = ["%s: %s" % (args[0], e)], None
    for e in errors:
        print("FAIL:", e)
    if errors:
        return 1
    print("OK: %s is the patched linux-%s tree, with the boot image's configuration" % (args[0], kver))
    print("kver", kver)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
