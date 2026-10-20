#!/usr/bin/env python3
"""Assemble an amonet boot image with its microloader, kernel, DTB and ramdisk.

The first 0x400 bytes come from a compatible locally supplied boot image.
The Android boot header follows at offset 0x400, and the kernel at 0x800.
Load addresses and the header command line are preserved from that input.
"""
import argparse
import hashlib
import struct

MAGIC = b'ANDROID!'
ML = 0x400  # the microloader, and the offset the real header is moved to


def header(b, at=0):
    ks, ka, rs, ra, ss, sa, ta, ps = struct.unpack('<IIIIIIII', b[at + 8:at + 40])
    return dict(kernel_size=ks, kernel_addr=ka, ramdisk_size=rs, ramdisk_addr=ra,
                second_size=ss, second_addr=sa, tags_addr=ta, page_size=ps)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--lineage', required=True, help="compatible boot image containing an amonet microloader")
    ap.add_argument('--ramdisk-from', help='a boot image to take the initramfs from')
    ap.add_argument('--ramdisk', help='a raw initramfs file to use instead')
    ap.add_argument('--kernel', required=True)
    ap.add_argument('--dtb', required=True)
    ap.add_argument('--cmdline-append', default='')
    ap.add_argument('-o', '--out', required=True)
    a = ap.parse_args()

    los = open(a.lineage, 'rb').read()
    if los[:8] != MAGIC:
        raise SystemExit('%s does not start with ANDROID!: not a boot image' % a.lineage)
    if los[ML:ML + 8] != MAGIC:
        raise SystemExit('%s carries no microloader (no ANDROID! at 0x400), so there is nothing to take.\n'
                         'Dump the running boot partition instead: adb pull /dev/block/mmcblk0p9' % a.lineage)
    micro = los[:ML]
    h = header(los, ML)
    ps = h['page_size']
    print('microloader: %d bytes; kernel load address 0x%08x, ramdisk at 0x%08x, tags at 0x%08x, page %d'
          % (ML, h['kernel_addr'], h['ramdisk_addr'], h['tags_addr'], ps))

    if a.ramdisk:
        ramdisk = open(a.ramdisk, 'rb').read()
    else:
        ramdisk = None
    # The rescue initramfs, from the release image (a plain, uninjected boot image).
    rel = open(a.ramdisk_from, 'rb').read() if a.ramdisk_from else b''
    if ramdisk is None:
        if rel[:8] != MAGIC:
            raise SystemExit('%s is not a boot image' % a.ramdisk_from)
        r = header(rel)
        pad = lambda n: (-n) % r['page_size']
        r_off = r['page_size'] + r['kernel_size'] + pad(r['kernel_size'])
        ramdisk = rel[r_off:r_off + r['ramdisk_size']]
        if len(ramdisk) != r['ramdisk_size']:
            raise SystemExit('%s is truncated' % a.ramdisk_from)

    kernel = open(a.kernel, 'rb').read() + open(a.dtb, 'rb').read()

    # The real header: preserved load addresses, with updated sizes. Only its first 0x400 bytes survive the layout,
    # which covers everything through the sha1 id; extra_cmdline past that is dropped, as the payload's
    # own injection drops it too.
    hdr = bytearray(los[ML:ML + ML])
    struct.pack_into('<I', hdr, 8, len(kernel))
    struct.pack_into('<I', hdr, 16, len(ramdisk))
    struct.pack_into('<I', hdr, 24, 0)          # no second stage
    cmdline = hdr[64:64 + 512].split(b'\0')[0]
    if a.cmdline_append:
        cmdline = cmdline + b' ' + a.cmdline_append.encode()
    if len(cmdline) > 511:
        raise SystemExit('the command line is too long (%d bytes)' % len(cmdline))
    hdr[64:64 + 512] = cmdline.ljust(512, b'\0')
    sha = hashlib.sha1()
    for blob in (kernel, ramdisk, b''):
        sha.update(blob)
        sha.update(struct.pack('<I', len(blob)))
    hdr[576:576 + 32] = sha.digest().ljust(32, b'\0')

    out = bytearray()
    out += micro
    out += hdr
    assert len(out) == ps, 'page size %d does not match the 0x800 layout the microloader needs' % ps
    out += kernel + b'\0' * ((-len(kernel)) % ps)
    out += ramdisk + b'\0' * ((-len(ramdisk)) % ps)

    if len(out) > 16 * 1024 * 1024:
        raise SystemExit('boot image exceeds the 16 MiB boot partition')
    open(a.out, 'wb').write(out)
    print('%s: %d bytes, kernel %d, ramdisk %d' % (a.out, len(out), len(kernel), len(ramdisk)))
    print('command line: %s' % cmdline.decode())
    fits = 'fits' if len(out) <= 16 * 1024 * 1024 else 'DOES NOT FIT'
    print('%s the 16 MB boot partition' % fits)


main()
