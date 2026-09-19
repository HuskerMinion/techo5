#!/usr/bin/env python3
"""Carve the audio FPGA bitstreams (i2s_to_spi_4ch_v208.bin, v193) out of a 4.9 Echo Show 5 boot image.
Amazon builds them into the kernel with CONFIG_EXTRA_FIRMWARE, so the unit's own LineageOS/FireOS boot
partition is the source, as for the rest of the vendor tree.

    extract-fpga-fw.py boot.img outdir
"""
import struct, sys, zlib, os
b = open(sys.argv[1], 'rb').read()
off = 0x400 if b[0x400:0x408] == b'ANDROID!' else 0          # this unit: microloader first
ks, ka, rs, ra, ss, sa, ta, ps = struct.unpack('<IIIIIIII', b[off + 8:off + 40])
k = b[0x800:0x800 + ks] if off else b[ps:ps + ks]
out = zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(k[k.find(b'\x1f\x8b\x08'):])
base = 0xffffff8008080000                                     # 4.9 arm64: image VA at file offset 0
os.makedirs(sys.argv[2], exist_ok=True)
for name in (b'i2s_to_spi_4ch_v208.bin', b'i2s_to_spi_4ch_v193.bin'):
    va = struct.pack('<Q', base + out.find(name))
    j = out.find(va)
    while j != -1:
        dptr, size = struct.unpack('<QQ', out[j + 8:j + 24])
        doff = dptr - base
        if 0 < size < 200000 and 0 <= doff < len(out) and out[doff:doff + 2] == b'\xff\x00':
            open(os.path.join(sys.argv[2], name.decode()), 'wb').write(out[doff:doff + size])
            print('%s: %d bytes' % (name.decode(), size))
            break
        j = out.find(va, j + 1)
