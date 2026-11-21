#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check a kernel source tarball made by build.sh against a boot image it was built with.

    check-source.py [--config kernel.config] source.tar.xz boot-thinksmart.img

The tarball is what the GPL asks for with the boot image a ThinkSmart View rootfs carries: the whole
tree that built it. Checks: one top directory, linux-<version>-techo5-thinksmart, whose Makefile is
that version; no absolute paths, no "..", and no build output left in it (.config, objects, vmlinux);
every entry owned by 0:0 with one fixed time, as build.sh's tar writes it; the drivers in src/ there,
the same bytes as the ones beside this script, with the lines build.sh adds to the kernel's Makefiles
and Kconfig; every line each of patches/*.patch adds, in the file it adds it to; and
techo5-thinksmart.config the configuration the boot image's kernel carries (CONFIG_IKCONFIG), and the
one given with --config (build.sh's kernel.config) when there is one.

Prints "kver <version>" last, for release.ps1.
"""
import glob
import os
import re
import struct
import sys
import tarfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))

# Files TECHO5 adds to the tree, as they are in src/.
OURS = {
    "sound/soc/codecs/tas5782m.c": "src/tas5782m.c",
    "sound/soc/codecs/tas5782m.h": "src/tas5782m.h",
    "sound/soc/codecs/tas5782m_priv.h": "src/tas5782m_priv.h",
    "sound/soc/codecs/tas5782m_tables.c": "src/tas5782m_tables.c",
    "drivers/media/i2c/s5kc505a.c": "src/s5kc505a.c",
}
# What build.sh adds to the kernel's own files.
MARKERS = {
    "sound/soc/codecs/Makefile": b"obj-$(CONFIG_SND_SOC_TAS5782M) += snd-soc-tas5782m.o",
    "sound/soc/codecs/Kconfig": b"config SND_SOC_TAS5782M",
    "drivers/media/i2c/Makefile": b"obj-$(CONFIG_VIDEO_S5KC505A) += s5kc505a.o",
    "drivers/media/i2c/Kconfig": b"config VIDEO_S5KC505A",
}
CONFIG = "techo5-thinksmart.config"
# What a build leaves and distclean takes away.
BUILT = re.compile(r"(^|/)(\.config|vmlinux|Image(\.gz)?|[^/]*\.(o|ko|a|dtb|dtbo|cmd))$")
TOP = re.compile(r"^linux-(\d+\.\d+\.\d+)-techo5-thinksmart$")


def kernel_of(img):
    """The kernel inside a header v0 boot image (pack.py): Image.gz with the device tree after it,
    decompressed."""
    if img[:8] != b"ANDROID!":
        raise ValueError("not an Android boot image")
    ksize, = struct.unpack_from("<I", img, 8)
    page, = struct.unpack_from("<I", img, 36)
    if page not in (2048, 4096) or page + ksize > len(img):
        raise ValueError("the boot image's header does not fit it")
    k = img[page:page + ksize]
    if k[:2] != b"\x1f\x8b":
        raise ValueError("the boot image's kernel is not Image.gz")
    # The gzip stream ends where the device tree starts; decompressobj stops there by itself.
    return zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(k)


def embedded_config(img):
    """The configuration in the boot image's kernel (IKCONFIG), or None when it carries none."""
    k = kernel_of(img)
    at = k.find(b"IKCFG_ST\x1f\x8b\x08")
    if at < 0:
        return None
    return zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(k[at + 8:])


def ours(rel):
    with open(os.path.join(HERE, rel), "rb") as f:
        return f.read().replace(b"\r\n", b"\n")


def patch_lines():
    """{file in the tree: [(patch, line it adds)]} for patches/*.patch."""
    want = {}
    for p in sorted(glob.glob(os.path.join(HERE, "patches", "*.patch"))):
        target = None
        for line in ours(os.path.relpath(p, HERE)).split(b"\n"):
            if line.startswith(b"+++ "):
                path = line[4:].split(b"\t")[0].decode()
                target = path[2:] if path.startswith("b/") else None
            elif line.startswith(b"--- ") or line.startswith(b"diff "):
                continue
            elif target and line.startswith(b"+") and line[1:].strip():
                want.setdefault(target, []).append((os.path.basename(p), line[1:]))
    return want


def check(source, img, config=None):
    """Errors found, and the kernel version."""
    errors = []
    found = {}
    top = kver = None
    times, owners = set(), set()
    patched = patch_lines()
    wanted = set(MARKERS) | set(OURS) | set(patched) | {CONFIG, "Makefile"}
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
                    errors.append("top directory %s is not linux-<version>-techo5-thinksmart" % top)
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
    for f, lines in patched.items():
        text = found.get(f)
        if text is None:
            errors.append("%s, which %s changes, is missing" % (f, lines[0][0]))
            continue
        have = {l.rstrip() for l in text.split(b"\n")}
        for p in sorted({p for p, line in lines if line.rstrip() not in have}):
            errors.append("%s does not have what %s adds to it" % (f, p))
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
            errors.append("the boot image's kernel carries no configuration (CONFIG_IKCONFIG)")
    return errors, kver


def main(argv):
    args = argv[1:]
    config = None
    if len(args) >= 2 and args[0] == "--config":
        with open(args[1], "rb") as f:
            config = f.read()
        args = args[2:]
    if len(args) != 2:
        print("usage: check-source.py [--config kernel.config] source.tar.xz boot-thinksmart.img", file=sys.stderr)
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
    print("OK: %s is linux-%s with TECHO5's ThinkSmart drivers and patches, and the boot image's "
          "configuration" % (args[0], kver))
    print("kver", kver)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
