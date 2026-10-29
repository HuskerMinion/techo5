#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# A copy of tools/pack-uboot.py from github.com/nuno407/u-boot-amazon-echo (commit 8f780fb): build.sh checks
# that it is the same but for these two lines. install-athena.py --boot-layer packs U-Boot with it.
"""Pack U-Boot as the kernel of a device's own boot image, in that image's shape (the Echo boards).

    pack-uboot.py --boot BOOT.img --uboot u-boot.bin --out OUT.img [options]
    pack-uboot.py --boot BOOT.img --id         (print BOOT.img's header id, 40 hex digits)

BOOT.img is the device's boot partition as read from it (or a backup of it). Everything but the
kernel is taken from it unchanged: whatever comes before the header (--header-at: on the Echo
Show 5 2nd gen, amonet's microloader, which this repository does not carry), the header (load
addresses, page size, os_version, command line), the device tree appended to its kernel (with the
device's own data in it) and its ramdisk. The kernel becomes U-Boot, gzip'd (as the kernels are)
or, with --raw, as it is, with that same device tree appended. So the bootloader patches and hands
U-Boot exactly the tree, command line and ramdisk it hands the kernel today, and abboot (SPEC.md)
finds the packed tree again to tell the bootloader's additions from it.

Options, per board (each board's README.md):
  --header-at N      the Android header at byte N (0; 0x400 behind amonet's microloader), the
                     kernel still at the first page boundary; bytes 0..N-1 come from
                     --copy-stub-from (default: BOOT.img itself)
  --min-kernel N     pad the kernel area (gzip'd payload + DTB) to at least N bytes with random,
                     incompressible bytes after U-Boot (default 0x210000, 2 MB + 64 KB). On the
                     Echo boards the bootloader did not start images whose kernel area was small
                     (black-box tests, "The 2 MB rule" in the Echo Show 8 board's README.md).
                     0: no padding (for repeating those tests)
  --marker STR       refuse a u-boot.bin that does not carry STR (CONFIG_AB_SELF_MARKER)
  --max-size N       the partition size (default 16 MiB)

The output carries the device's own data (from BOOT.img): keep it with your backups and do not
share it. Only the Python standard library is used.
"""
import argparse
import gzip
import hashlib
import io
import random
import struct
import sys
import zlib

PAGE = 2048
PARTITION = 16 * 1024 * 1024    # the Echo boards' boot and recovery partitions
FDT_MAGIC = b'\xd0\x0d\xfe\xed'
MIN_KERNEL = 0x210000
HDR_SIZE = {0: 1632, 1: 1648}   # the v0 and v1 headers (v1 adds the recovery DTBO fields)


def pad(n, page=PAGE):
    return (n + page - 1) // page * page


def image_id(kernel, ramdisk, version=0):
    """mkbootimg's id: kernel, ramdisk and an empty second stage, each with its length; v1 also a
    zero recovery DTBO size."""
    sha = hashlib.sha1()
    for blob in (kernel, ramdisk, b''):
        sha.update(blob)
        sha.update(struct.pack('<I', len(blob)))
    if version == 1:
        sha.update(struct.pack('<I', 0))
    return sha.digest()


def header(img, at=0):
    """The header fields abboot and the bootloader use: kernel_size, ramdisk_size, second_size, page_size,
    header_version, the id."""
    if img[at:at + 8] != b'ANDROID!':
        raise SystemExit('no Android boot image header at %#x' % at)
    ks, _, rs, _, ss = struct.unpack_from('<5I', img, at + 8)
    ps, ver = struct.unpack_from('<II', img, at + 36)
    return ks, rs, ss, ps, ver, img[at + 576:at + 596]


def split(img, at=0):
    """(kernel gzip without the DTB, DTB, ramdisk) of a boot image with its header at at."""
    if at and img[:8] != b'ANDROID!':
        raise SystemExit('not an amonet boot image (no microloader before the header at %#x)' % at)
    ks, rs, ss, ps, ver, hid = header(img, at)
    if ps != PAGE or ss or ver not in HDR_SIZE:
        raise SystemExit('unexpected page size %d, a second stage, or header version %d' % (ps, ver))
    kernel = img[PAGE:PAGE + ks]
    ramdisk = img[PAGE + pad(ks):PAGE + pad(ks) + rs]
    if len(kernel) != ks or len(ramdisk) != rs:
        raise SystemExit('the boot image is truncated')
    if image_id(kernel, ramdisk, ver) != hid:
        raise SystemExit('the boot image header id does not match its contents')
    d = zlib.decompressobj(16 + zlib.MAX_WBITS)
    d.decompress(kernel)
    if not d.eof:
        raise SystemExit('the kernel is not one gzip stream')
    dtb = d.unused_data
    if dtb[:4] != FDT_MAGIC or struct.unpack('>I', dtb[4:8])[0] != len(dtb):
        raise SystemExit('no device tree right after the kernel gzip stream')
    return kernel[:ks - len(dtb)], dtb, ramdisk


def gz(data):
    """Deterministic gzip (no name, mtime 0), as the kernel's Image.gz."""
    b = io.BytesIO()
    with gzip.GzipFile(filename='', mode='wb', fileobj=b, mtime=0, compresslevel=9) as g:
        g.write(data)
    out = bytearray(b.getvalue())
    out[9] = 3  # OS: Unix, as GNU gzip -n writes it (Python writes 255); not covered by the CRC
    return bytes(out)


def assemble(src, at, stub, kernel, ramdisk):
    """The output image: src's first page with the new kernel size and id (and stub before the
    header), the kernel and the ramdisk, each padded to the page."""
    ver = header(src, at)[4]
    page = bytearray(src[:PAGE])
    page[:at] = stub[:at]
    if not at:
        page[HDR_SIZE[ver]:] = bytes(PAGE - HDR_SIZE[ver])  # past the header: zero
    struct.pack_into('<I', page, at + 8, len(kernel))
    struct.pack_into('<I', page, at + 16, len(ramdisk))
    page[at + 576:at + 608] = image_id(kernel, ramdisk, ver).ljust(32, b'\0')
    return bytes(page) + kernel.ljust(pad(len(kernel)), b'\0') + ramdisk.ljust(pad(len(ramdisk)), b'\0')


def image_size(ub, keep):
    """The arm64 Image header's image_size: the payload and room for U-Boot's pre-relocation stack
    and early malloc (SYS_INIT_SP_BSS_OFFSET 0x80000 + SYS_MALLOC_F_LEN 0x10000), 64 KB aligned, at
    least 2 MB, as a kernel's. A loader may use it to place things after the image."""
    if keep:
        return ub
    size = struct.unpack_from('<Q', ub, 16)[0]
    size = max(0x200000, (max(size, len(ub)) + 0x90000 + 0xffff) & ~0xffff)
    print('image_size set to %#x' % size)
    return ub[:16] + struct.pack('<Q', size) + ub[24:]


def main():
    ap = argparse.ArgumentParser()
    num = lambda v: int(v, 0)  # noqa: E731
    ap.add_argument('--boot', required=True)
    ap.add_argument('--uboot')
    ap.add_argument('--out')
    ap.add_argument('--id', action='store_true', help="print BOOT's header id and stop")
    ap.add_argument('--header-at', type=num, default=0, help='the header offset (0x400 behind a microloader)')
    ap.add_argument('--copy-stub-from', help='the bytes before the header from this image (default: BOOT)')
    ap.add_argument('--min-kernel', type=num, default=MIN_KERNEL, help='the smallest kernel area (0: none)')
    ap.add_argument('--no-min-size', action='store_true', help='the same as --min-kernel 0')
    ap.add_argument('--marker', help='u-boot.bin must carry this string')
    ap.add_argument('--max-size', type=num, default=PARTITION, help='the partition size')
    ap.add_argument('--raw', action='store_true', help='U-Boot uncompressed (default: gzip)')
    ap.add_argument('--pad-random', type=num, default=0,
                    help='append this many random (seeded, incompressible) bytes to U-Boot inside '
                         'the payload, so the compressed kernel is as big as a real one')
    ap.add_argument('--keep-image-size', action='store_true',
                    help="leave the arm64 Image header's image_size as built")
    a = ap.parse_args()

    at = a.header_at
    src = open(a.boot, 'rb').read()
    _, dtb, ramdisk = split(src, at)
    if a.id:
        print(src[at + 576:at + 596].hex())
        return 0
    if not a.uboot or not a.out:
        ap.error('--uboot and --out are needed')
    stub = open(a.copy_stub_from, 'rb').read()[:at] if a.copy_stub_from else src[:at]
    if len(stub) != at or (at and stub[:8] != b'ANDROID!'):
        raise SystemExit('the stub before the header is not %#x bytes of a boot image' % at)
    minimum = 0 if a.no_min_size else a.min_kernel

    ub = open(a.uboot, 'rb').read()
    if ub[0x38:0x3c] != b'ARM\x64':
        raise SystemExit('%s has no arm64 Image header (CONFIG_LINUX_KERNEL_IMAGE_HEADER)' % a.uboot)
    if a.marker and a.marker.encode() not in ub:
        raise SystemExit('%s does not carry %s (CONFIG_AB_SELF_MARKER)' % (a.uboot, a.marker))
    if a.pad_random:
        ub = ub + random.Random(1).randbytes(a.pad_random)
    ub = image_size(ub, a.keep_image_size)
    kernel = (ub if a.raw else gz(ub)) + dtb
    if len(kernel) < minimum:
        # Random, incompressible bytes after U-Boot inside the payload (after its image, where
        # nothing of U-Boot's lives), until the kernel area is large enough.
        ub = ub + random.Random(1).randbytes(minimum - len(kernel) + 0x1000)
        if not a.keep_image_size:
            size = max(struct.unpack_from('<Q', ub, 16)[0], (len(ub) + 0x90000 + 0xffff) & ~0xffff)
            ub = ub[:16] + struct.pack('<Q', size) + ub[24:]
            print('image_size set to %#x' % size)
        kernel = (ub if a.raw else gz(ub)) + dtb
        print('payload padded to %d bytes, kernel area %d' % (len(ub), len(kernel)))
        if len(kernel) < minimum:
            raise SystemExit('kernel area %d bytes: under the %d asked for' % (len(kernel), minimum))

    out = assemble(src, at, stub, kernel, ramdisk)
    if len(out) > a.max_size:
        raise SystemExit('%d bytes: larger than the %d-byte partition' % (len(out), a.max_size))
    with open(a.out, 'wb') as o:
        o.write(out)
    print('%s: %d bytes of %d; payload %d (image_size %#x), kernel area %d%s, device tree %d, '
          'ramdisk %d; header at %#x, id %s; from %s' % (
              a.out, len(out), a.max_size, len(ub), struct.unpack_from('<Q', ub, 16)[0], len(kernel),
              '' if a.raw else ' (gzip %d)' % (len(kernel) - len(dtb)), len(dtb), len(ramdisk), at,
              out[at + 576:at + 596].hex(), src[at + 576:at + 596].hex()))
    print('command line: %s' % out[at + 64:at + 576].split(b'\0')[0].decode())
    print('sha256 %s' % hashlib.sha256(out).hexdigest())
    return 0


if __name__ == '__main__':
    sys.exit(main())
