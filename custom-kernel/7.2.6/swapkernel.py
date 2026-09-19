#!/usr/bin/env python3
"""Replace the kernel in an Android boot image (header v0), keeping its header, ramdisk and command line.

    swapkernel.py <in.img> <Image.gz> <cronos.dtb> <out.img> [--cmdline-append "..."]

The kernel blob LK expects is the gzip arm64 Image with the DTB appended (Image.gz-dtb), as LineageOS's.
"""
import hashlib, struct, sys

def main():
    args = sys.argv[1:]
    extra = ''
    if '--cmdline-append' in args:
        i = args.index('--cmdline-append'); extra = args[i + 1]; del args[i:i + 2]
    src, image, dtb, out = args
    b = open(src, 'rb').read()
    magic, ks, ka, rs, ra, ss, sa, ta, ps = struct.unpack('<8sIIIIIIII', b[:40])
    assert magic == b'ANDROID!', 'not a boot image'
    kernel = open(image, 'rb').read() + open(dtb, 'rb').read()
    pad = lambda n: (-n) % ps
    k_off = ps
    r_off = k_off + ks + pad(ks)
    s_off = r_off + rs + pad(rs)
    ramdisk = b[r_off:r_off + rs]
    second = b[s_off:s_off + ss] if ss else b''
    cmdline = b[64:64 + 512].split(b'\0')[0]
    if extra:
        cmdline = (cmdline.decode() + ' ' + extra).encode()
    assert len(cmdline) <= 512
    hdr = bytearray(b[:ps])
    struct.pack_into('<I', hdr, 8, len(kernel))
    hdr[64:64 + 512] = cmdline.ljust(512, b'\0')
    h = hashlib.sha1()
    for blob in (kernel, ramdisk, second):
        h.update(blob); h.update(struct.pack('<I', len(blob)))
    hdr[576:576 + 32] = h.digest().ljust(32, b'\0')
    with open(out, 'wb') as f:
        f.write(hdr)
        f.write(kernel); f.write(b'\0' * pad(len(kernel)))
        f.write(ramdisk); f.write(b'\0' * pad(rs))
        if ss:
            f.write(second); f.write(b'\0' * pad(ss))
    print('%s: kernel %d bytes (was %d), ramdisk %d, cmdline: %s' % (out, len(kernel), ks, rs, cmdline.decode()))

main()
