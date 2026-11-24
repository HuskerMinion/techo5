#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""pack.py: the ThinkSmart View's boot image, boot-thinksmart.img, and its check.

    pack.py pack <Image.gz> <dtb> <cmdline> <out.img>
    pack.py check <boot-thinksmart.img>
    pack.py cmdline <boot-thinksmart.img>     system1's command line: the image's, trial word off

An Android boot image, header version 0, as mkbootimg makes it with the ThinkSmart View's stock
addresses (--base 0x80000000 --kernel_offset 0x8000 --ramdisk_offset 0x01000000 --tags_offset 0x100,
page size 2048): the kernel part is Image.gz with the device tree after it, the ramdisk an empty
cpio archive (uncompressed, so the image does not depend on a zlib), no second stage. lk2nd starts
it from the recovery partition in recovery mode, which is how a slot on trial is started once with
its kernel (tools/linux/thinksmart/lk2nd-ab). The command line ends in techo5.kernel=trial, which
tells that boot from one of system1's; system1's command line is the same without it (mkimage.sh and
the update both take it from here). Deterministic: the same inputs give the same bytes.
"""
import hashlib
import struct
import sys
import zlib

PAGE = 2048
BASE = 0x80000000
KERNEL_ADDR = BASE + 0x00008000
RAMDISK_ADDR = BASE + 0x01000000
SECOND_ADDR = 0  # no second stage: 0, as mkbootimg 1.0 writes it
TAGS_ADDR = BASE + 0x00000100
TRIAL = "techo5.kernel=trial"
# lk2nd's extlinux loader (the units have 20.0, January 2025) takes a kernel of up to 32 MiB and puts
# the device tree right after that, so the kernel's whole size in memory (the arm64 header's
# image_size, its BSS included) must stay under it. Newer lk2nd allows 48 MiB; the units' does not.
KERNEL_MAX = 32 << 20
FDT_MAGIC = b"\xd0\x0d\xfe\xed"


def empty_cpio():
    name = b"TRAILER!!!\0"
    h = b"070701" + b"".join(b"%08X" % v for v in (0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, len(name), 0)) + name
    h += b"\0" * (-len(h) % 4)
    return h + b"\0" * (-len(h) % 512)


def pad(b):
    return b + b"\0" * (-len(b) % PAGE)


def image_id(*parts):
    h = hashlib.sha1()
    for p in parts:
        h.update(p)
        h.update(struct.pack("<I", len(p)))
    return h.digest() + b"\0" * 12


def image_size(gz):
    """The arm64 Image header's image_size, from the start of Image.gz."""
    head = zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(gz, 64)
    if len(head) < 64 or head[56:60] != b"ARM\x64":
        raise ValueError("Image.gz does not hold an arm64 Image")
    return struct.unpack("<Q", head[16:24])[0]


def pack(gz, dtb, cmdline):
    if gz[:2] != b"\x1f\x8b":
        raise SystemExit("pack: the kernel is not Image.gz")
    if image_size(gz) > KERNEL_MAX:
        raise SystemExit("pack: the kernel takes %d bytes in memory; lk2nd takes %d" % (image_size(gz), KERNEL_MAX))
    if dtb[:4] != FDT_MAGIC or struct.unpack(">I", dtb[4:8])[0] != len(dtb):
        raise SystemExit("pack: the device tree is not one whole flattened tree")
    cmdline = cmdline.strip() + " " + TRIAL
    if len(cmdline) >= 512:
        raise SystemExit("pack: the command line is %d bytes; the header holds 511" % len(cmdline))
    kernel, ramdisk, second = gz + dtb, empty_cpio(), b""
    hdr = b"ANDROID!" + struct.pack("<10I", len(kernel), KERNEL_ADDR, len(ramdisk), RAMDISK_ADDR,
                                     len(second), SECOND_ADDR, TAGS_ADDR, PAGE, 0, 0)
    hdr += b"\0" * 16 + cmdline.encode().ljust(512, b"\0") + image_id(kernel, ramdisk, second) + b"\0" * 1024
    return pad(hdr) + pad(kernel) + pad(ramdisk)


def parse(img):
    """The image's kernel, device tree and command line, checked as pack makes them."""
    if img[:8] != b"ANDROID!":
        raise ValueError("not an Android boot image")
    ksz, _, rsz, _, ssz, _, _, page, version, _ = struct.unpack("<10I", img[8:48])
    if page != PAGE or version != 0 or ssz != 0:
        raise ValueError("page %d, header version %d, second stage %d: not a ThinkSmart image" % (page, version, ssz))
    k_at = PAGE
    r_at = k_at + ksz + (-ksz % PAGE)
    end = r_at + rsz + (-rsz % PAGE)
    if end != len(img):
        raise ValueError("%d bytes, the header says %d" % (len(img), end))
    kernel, ramdisk = img[k_at:k_at + ksz], img[r_at:r_at + rsz]
    if img[576:608] != image_id(kernel, ramdisk, b""):
        raise ValueError("the header id does not match the contents")
    at = kernel.rfind(FDT_MAGIC)
    while at > 0 and struct.unpack(">I", kernel[at + 4:at + 8])[0] != len(kernel) - at:
        at = kernel.rfind(FDT_MAGIC, 0, at)
    if at <= 0 or kernel[:2] != b"\x1f\x8b":
        raise ValueError("the kernel part is not Image.gz with a device tree after it")
    if image_size(kernel[:at]) > KERNEL_MAX:
        raise ValueError("the kernel takes %d bytes in memory; lk2nd takes %d" % (image_size(kernel[:at]), KERNEL_MAX))
    cmdline = img[64:576].split(b"\0", 1)[0].decode()
    if not cmdline.endswith(" " + TRIAL) or "root=/dev/dm-1" not in cmdline.split():
        raise ValueError("the command line does not have the store as its root and %s last" % TRIAL)
    return kernel[:at], kernel[at:], cmdline[:-len(" " + TRIAL)]


def main(argv):
    if len(argv) == 6 and argv[1] == "pack":
        gz, dtb = open(argv[2], "rb").read(), open(argv[3], "rb").read()
        with open(argv[5], "wb") as f:
            f.write(pack(gz, dtb, argv[4]))
        return 0
    if len(argv) == 3 and argv[1] in ("check", "cmdline"):
        try:
            _, _, cmdline = parse(open(argv[2], "rb").read())
        except ValueError as e:
            print("%s: %s" % (argv[2], e), file=sys.stderr)
            return 1
        print(cmdline if argv[1] == "cmdline" else "OK: %s is a ThinkSmart boot image" % argv[2])
        return 0
    print(__doc__.split("\n\n")[0], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
