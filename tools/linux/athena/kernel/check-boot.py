#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check an athena boot image made by pack.py (build.sh runs this on its output).

    check-boot.py [--filled] boot.img

Checks: every header field against pack.py's constants, the SHA-1 id, the layout (header page,
Image.gz with the DTB right after the gzip stream, the ramdisk, zero padding), that the DTB is a
valid FDT, that the ramdisk is a gzip'd newc cpio holding exactly the expected firmware files under
lib/firmware and no init, that the DSP slot magic occurs exactly once in the whole image (inside
the DTB; not in the decompressed kernel or ramdisk) and that the slot is valid and empty (N == 0).
With --filled the slot may hold data instead (an image the installer has filled); its length and
SHA-256 are still checked.
"""
import os
import struct
import sys
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import dsp_slot  # noqa: E402
import pack  # noqa: E402

FDT_MAGIC = 0xD00DFEED

# What the ramdisk must hold (build.sh's RAMDISK_FILES), under lib/firmware
RAMDISK_FILES = {
    "LICENCE.mediatek",
    "mediatek/mt7663_n9_rebb.bin",
    "mediatek/mt7663_n9_v3.bin",
    "mediatek/mt7663pr2h.bin",
    "mediatek/mt7663pr2h_rebb.bin",
}
# The 7.2.11 build (custom-kernel/athena) adds the SCP's firmware.
RAMDISK_FILES_SCP = RAMDISK_FILES | {"mediatek/mt8183/scp.img"}


def gunzip_all(data):
    """Decompress one gzip member; return (decompressed, bytes after it)."""
    d = zlib.decompressobj(16 + zlib.MAX_WBITS)
    out = d.decompress(data)
    if not d.eof:
        raise ValueError("gzip stream does not end")
    return out, d.unused_data


def cpio_entries(cpio):
    """newc cpio -> {name: (mode, data)}. Raises ValueError on a malformed archive."""
    entries = {}
    p = 0
    while True:
        if cpio[p:p + 6] != b"070701":
            raise ValueError("bad cpio header at %d" % p)
        f = [int(cpio[p + 6 + 8 * i:p + 14 + 8 * i], 16) for i in range(13)]
        mode, filesize, namesize = f[1], f[6], f[11]
        name = cpio[p + 110:p + 110 + namesize - 1].decode()
        p += 110 + namesize
        p += -p % 4
        data = cpio[p:p + filesize]
        p += filesize
        p += -p % 4
        if name == "TRAILER!!!":
            break
        entries[name] = (mode, data)
    if any(cpio[p:]):
        raise ValueError("data after the cpio trailer")
    return entries


def check(img, filled=False):
    errors = []

    def want(cond, msg):
        if not cond:
            errors.append(msg)

    if len(img) < pack.PAGE_SIZE:
        return ["shorter than one page"], None
    f = struct.unpack_from("<8s10I16s512s32s1024sIQI", img, 0)
    (magic, ksize, kaddr, rsize, raddr, ssize, saddr, tags, page, hv, osv,
     name, cmd, digest, extra, dtbo_size, dtbo_off, hsize) = f
    want(magic == pack.BOOT_MAGIC, "magic %r" % magic)
    want(kaddr == pack.KERNEL_ADDR, "kernel_addr %#x" % kaddr)
    want(rsize > 0, "no ramdisk")
    want(raddr == pack.RAMDISK_ADDR, "ramdisk_addr %#x" % raddr)
    want(ssize == 0, "second_size %d" % ssize)
    want(saddr == pack.SECOND_ADDR, "second_addr %#x" % saddr)
    want(tags == pack.TAGS_ADDR, "tags_addr %#x" % tags)
    want(page == pack.PAGE_SIZE, "page_size %d" % page)
    want(hv == pack.HEADER_VERSION, "header_version %d" % hv)
    want(osv == pack.OS_VERSION, "os_version %#x" % osv)
    want(name == pack.BOARD_NAME.ljust(16, b"\0"), "name %r" % name)
    # pack.py's, or with cpuidle on (a build with MCDI's idle states, pack.py's CPUIDLE)
    cmdlines = {c.encode().ljust(512, b"\0") for c in (pack.CMDLINE, pack.CMDLINE.replace("cpuidle.off=1 ", ""))}
    want(cmd in cmdlines, "cmdline %r" % cmd.rstrip(b"\0"))
    want(extra == pack.EXTRA_CMDLINE.encode().ljust(1024, b"\0"), "extra_cmdline")
    want(dtbo_size == 0 and dtbo_off == 0, "recovery_dtbo %d@%d" % (dtbo_size, dtbo_off))
    want(hsize == pack.HEADER_SIZE, "header_size %d" % hsize)
    want(not any(img[pack.HEADER_SIZE:pack.PAGE_SIZE]), "header page padding is not zero")

    k_at = pack.PAGE_SIZE
    r_at = k_at + pack.pad(ksize)
    end = r_at + pack.pad(rsize)
    kernel = img[k_at:k_at + ksize]
    ramdisk = img[r_at:r_at + rsize]
    want(len(img) == end, "image size %d, expected %d" % (len(img), end))
    want(len(kernel) == ksize and len(ramdisk) == rsize, "image ends early")
    want(not any(img[k_at + ksize:r_at]), "padding after the kernel is not zero")
    want(not any(img[r_at + rsize:]), "padding after the ramdisk is not zero")
    want(digest == pack.image_id(kernel, ramdisk).ljust(32, b"\0"),
         "SHA-1 id does not match the contents")
    want(len(img) <= pack.BOOT_PARTITION_SIZE, "larger than the boot partition")

    # Image.gz, then the DTB
    try:
        vmlinux, dtb = gunzip_all(kernel)
    except (zlib.error, ValueError) as e:
        return errors + ["Image.gz: %s" % e], None
    gz_len = len(kernel) - len(dtb)
    want(vmlinux[56:60] == b"ARM\x64", "decompressed kernel is not an arm64 Image")
    want(len(dtb) >= 40 and struct.unpack_from(">I", dtb, 0)[0] == FDT_MAGIC, "no FDT after Image.gz")
    if len(dtb) >= 8:
        want(struct.unpack_from(">I", dtb, 4)[0] == len(dtb), "FDT totalsize does not match")
    want(dsp_slot.MAGIC not in vmlinux, "slot magic found in the decompressed kernel")

    # the ramdisk: firmware files only
    try:
        cpio, rest = gunzip_all(ramdisk)
        want(not rest, "data after the ramdisk's gzip stream")
        ents = cpio_entries(cpio)
        files = {n[len("lib/firmware/"):] for n, (m, _) in ents.items()
                 if m & 0o170000 == 0o100000 and n.startswith("lib/firmware/")}
        want(files in (RAMDISK_FILES, RAMDISK_FILES_SCP), "ramdisk files %s" % sorted(files))
        others = [n for n, (m, _) in ents.items()
                  if m & 0o170000 != 0o040000 and not n.startswith("lib/firmware/")]
        want(not others, "ramdisk has other entries: %s" % others)
        want("init" not in ents, "ramdisk has an /init")
        want(dsp_slot.MAGIC not in cpio, "slot magic found in the ramdisk")
    except (zlib.error, ValueError, UnicodeDecodeError) as e:
        errors.append("ramdisk: %s" % e)

    want(img.count(dsp_slot.MAGIC) == 1, "slot magic found %d times in the image, expected once"
         % img.count(dsp_slot.MAGIC))
    data = None
    try:
        off, slot = dsp_slot.find_slot(img)
        want(off >= k_at + gz_len and off + dsp_slot.SLOT_SIZE <= k_at + ksize,
             "slot is not inside the DTB")
        data = dsp_slot.parse_slot(slot)
        want(filled or not data, "slot is not empty (N=%d)" % len(data))
    except ValueError as e:
        errors.append("slot: %s" % e)
    return errors, data


def main(argv):
    args = argv[1:]
    filled = "--filled" in args
    args = [a for a in args if a != "--filled"]
    if len(args) != 1:
        print("usage: check-boot.py [--filled] boot.img", file=sys.stderr)
        return 2
    with open(args[0], "rb") as f:
        img = f.read()
    errors, data = check(img, filled)
    for e in errors:
        print("FAIL:", e)
    if errors:
        return 1
    print("OK: %s, %d bytes, header v%d, ramdisk %d bytes, slot %s" % (
        args[0], len(img), pack.HEADER_VERSION, struct.unpack_from("<I", img, 16)[0],
        "empty" if not data else "holds %d bytes" % len(data)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
