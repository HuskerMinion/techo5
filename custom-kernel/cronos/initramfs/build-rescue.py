#!/usr/bin/env python3
"""Assemble the mainline rescue initramfs without per-device data."""
import hashlib
import importlib.util
import lzma
import os
import re
from pathlib import Path

root = Path(__file__).resolve().parents[1]
repo = root.parent.parent
inputs = Path(os.environ.get("TECHO5_INPUTS", repo / "inputs"))
spec = importlib.util.spec_from_file_location("mkimage", repo / "tools/linux/mkimage.py")
mkimage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mkimage)

# The Alpine base tools/fetch-inputs.py fetches, by the name and sha256 tools/techo5lib.py pins.
pins = (repo / "tools/techo5lib.py").read_text()
base = inputs / re.search(r"^ALPINE_URL = '.*/([^/']+)'", pins, re.M)[1]
assert hashlib.sha256(base.read_bytes()).hexdigest() == re.search(r"^ALPINE_SHA256 = '([0-9a-f]{64})'", pins, re.M)[1]
c = mkimage.Cpio()
for name in ("dev", "proc", "sys", "run", "data", "store", "android", "newroot"):
    c.dir(name)
c.dir("tmp", 0o1777)
for name, major, minor in (("console", 5, 1), ("null", 1, 3), ("kmsg", 1, 11)):
    c.chardev("dev/" + name, major, minor)
for part in (8, 12, 16):
    c.blockdev(f"dev/mmcblk0p{part}", 179, part)
c.add_tar(base)
for directory in ("apks", "apks312"):
    for package in sorted((inputs / directory).glob("*.apk")):
        c.add_tar(package, skip_dotfiles=True)
c.add_tar(root / "out/kernel-rootfs.tar.gz")
for src, dest, mode in (
    (inputs / "busybox.static", "bin/busybox.static", 0o755),
    (repo / "bin/fbprobe-arm", "usr/local/bin/fbprobe", 0o755),
    (repo / "tools/linux/init", "init", 0o755),
    (repo / "tools/linux/slotctl", "usr/local/sbin/slotctl", 0o755),
    (repo / "tools/linux/techo5-lib.sh", "lib/techo5-lib.sh", 0o644),
):
    c.add_parents(dest)
    c.file(dest, src.read_bytes(), mode)
raw = c.finish()
names = mkimage.verify_cpio(raw)
assert {"init", "bin/busybox.static", "usr/local/sbin/slotctl"} <= set(names)
packed = lzma.compress(raw, check=lzma.CHECK_CRC32, preset=6)
out = root / "out/initramfs-rescue.xz"
out.write_bytes(packed)
print(f"Rescue image: {len(names)} entries, {len(packed)} bytes")
