#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check a kernel source tarball made by build.sh against a boot image it was built with.

    check-source.py [--config kernel.config] source.tar.xz boot.img

The tarball is what the GPL asks for with the boot image and the installer image: the whole tree that
built them. Checks: one top directory, linux-<version>-techo5-skylight, whose Makefile is that
version; no absolute paths, no "..", and no build output left in it (.config, objects, vmlinux); every
entry owned by 0:0 with one fixed time, as build.sh's tar writes it; TECHO5's board files there, the
same bytes as the ones beside this script, with the lines build.sh adds to the kernel's Makefiles and
Kconfig, and its boot logo; and techo5-skylight.config the configuration the boot image's kernel
carries (CONFIG_IKCONFIG), and the one given with --config (build.sh's kernel.config) when there is
one.

Prints "kver <version>" last, for release.ps1.
"""
import os
import re
import struct
import sys
import tarfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))

# Files TECHO5 adds to the tree, as they are in this directory.
OURS = {
    "arch/arm64/boot/dts/rockchip/rk3566-skylight-cal150.dts": "rk3566-skylight-cal150.dts",
    "drivers/gpu/drm/panel/panel-skylight-cal150.c": "panel-skylight-cal150.c",
}
# What build.sh adds to the kernel's own files.
MARKERS = {
    "arch/arm64/boot/dts/rockchip/Makefile": b"rk3566-skylight-cal150.dtb",
    "drivers/gpu/drm/panel/Makefile": b"obj-$(CONFIG_DRM_PANEL_SKYLIGHT_CAL150) += panel-skylight-cal150.o",
    "drivers/gpu/drm/panel/Kconfig": b"config DRM_PANEL_SKYLIGHT_CAL150",
}
# TECHO5's boot logo in place of the kernel's (mklogo.py): 1920x1064.
LOGO = "drivers/video/logo/logo_linux_clut224.ppm"
LOGO_HEAD = b"P3\n1920 1064\n255\n"
CONFIG = "techo5-skylight.config"
# What a build leaves and distclean takes away.
BUILT = re.compile(r"(^|/)(\.config|vmlinux|Image(\.gz)?|[^/]*\.(o|ko|a|dtb|dtbo|cmd))$")
TOP = re.compile(r"^linux-(\d+\.\d+\.\d+)-techo5-skylight$")


def kernel_of(img):
    """The kernel part of a header v2 boot image (pack-sky.py)."""
    if img[:8] != b"ANDROID!":
        raise ValueError("not an Android boot image")
    ksize, = struct.unpack_from("<I", img, 8)
    page, = struct.unpack_from("<I", img, 36)
    if page not in (2048, 4096) or page + ksize > len(img):
        raise ValueError("the boot image's header does not fit it")
    return img[page:page + ksize]


def embedded_config(img):
    """The configuration in the boot image's kernel (IKCONFIG), or None when it carries none. The
    Skylight's kernel is an uncompressed Image."""
    k = kernel_of(img)
    at = k.find(b"IKCFG_ST\x1f\x8b\x08")
    if at < 0:
        return None
    return zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(k[at + 8:])


def ours(rel):
    with open(os.path.join(HERE, rel), "rb") as f:
        return f.read().replace(b"\r\n", b"\n")


def check(source, img, config=None):
    """Errors found, and the kernel version."""
    errors = []
    found = {}
    top = kver = None
    times, owners = set(), set()
    wanted = set(MARKERS) | set(OURS) | {CONFIG, "Makefile", LOGO}
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
                    errors.append("top directory %s is not linux-<version>-techo5-skylight" % top)
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
            if m.isfile() and rest in wanted:
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
    for f, mark in MARKERS.items():
        if mark not in found.get(f, b""):
            errors.append("%s has no %r: not the tree build.sh set up" % (f, mark.decode()))
    for f, here in OURS.items():
        if f not in found:
            errors.append("%s is missing" % f)
        elif found[f] != ours(here):
            errors.append("%s is not this checkout's %s" % (f, here))
    if not found.get(LOGO, b"").startswith(LOGO_HEAD):
        errors.append("%s is not TECHO5's boot logo (mklogo.py)" % LOGO)
    cfg = found.get(CONFIG)
    if cfg is None:
        errors.append("%s is missing" % CONFIG)
    else:
        want = embedded_config(img)
        if want is not None and cfg != want:
            errors.append("%s is not the configuration the boot image's kernel was built with" % CONFIG)
        if config is not None and cfg != config:
            errors.append("%s is not the build's kernel.config" % CONFIG)
        if want is None:
            errors.append("the boot image's kernel carries no configuration (CONFIG_IKCONFIG, skylight.config)")
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
    except (tarfile.TarError, zlib.error, struct.error, ValueError, EOFError, OSError) as e:
        errors, kver = ["%s: %s" % (args[0], e)], None
    for e in errors:
        print("FAIL:", e)
    if errors:
        return 1
    print("OK: %s is linux-%s with TECHO5's Skylight board files, and the boot image's configuration"
          % (args[0], kver))
    print("kver", kver)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
