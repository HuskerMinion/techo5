#!/usr/bin/env python3
"""repack-init.py BOOT.img INIT OUT.img: the same amonet boot image with the initramfs's /init replaced."""
import sys, lzma, struct, hashlib
img = bytearray(open(sys.argv[1], 'rb').read())
newinit = open(sys.argv[2], 'rb').read()
H = 0x400
ks, ka, rs = struct.unpack_from('<III', img, H + 8)
ps = struct.unpack_from('<I', img, H + 36)[0]
koff = 0x800
roff = koff + ks + (-ks) % ps
kernel = bytes(img[koff:koff + ks]); rd = bytes(img[roff:roff + rs])
d = lzma.LZMADecompressor(); cpio = d.decompress(rd); assert d.eof
out = bytearray(); i = 0; done = False
while i < len(cpio):
    if cpio[i:i + 6] != b'070701':
        out += cpio[i:]; break  # padding after the trailer
    f = [int(cpio[i + 6 + 8 * k:i + 14 + 8 * k], 16) for k in range(13)]
    namesize, filesize = f[11], f[6]
    nstart = i + 110; name = cpio[nstart:nstart + namesize - 1]
    dstart = nstart + namesize; dstart += (-dstart) % 4
    data = cpio[dstart:dstart + filesize]
    nxt = dstart + filesize; nxt += (-nxt) % 4
    hdr = bytearray(cpio[i:i + 110])
    if name == b'init':
        data = newinit; hdr[6 + 8 * 6:14 + 8 * 6] = b'%08X' % len(data); done = True
    rec = bytes(hdr) + cpio[nstart:nstart + namesize]
    rec += b'\0' * ((-len(rec)) % 4) + data
    rec += b'\0' * ((-len(rec)) % 4)
    out += rec; i = nxt
    if name == b'TRAILER!!!':
        out += b'\0' * ((-len(out)) % 512); break
assert done, 'no /init'
newrd = lzma.compress(bytes(out), format=lzma.FORMAT_XZ, check=lzma.CHECK_CRC32, filters=[{"id": lzma.FILTER_LZMA2, "preset": 9, "dict_size": 1 << 20}])
struct.pack_into('<I', img, H + 16, len(newrd))
cl = bytes(img[H + 64:H + 576]).split(b'\0')[0]
if b'techo5.testslot=1' not in cl:
    cl += b' techo5.testslot=1'
assert len(cl) < 512
img[H + 64:H + 576] = cl.ljust(512, b'\0')
sha = hashlib.sha1()
for blob in (kernel, newrd, b''):
    sha.update(blob); sha.update(struct.pack('<I', len(blob)))
img[H + 576:H + 608] = sha.digest().ljust(32, b'\0')
res = bytes(img[:roff]) + newrd + b'\0' * ((-len(newrd)) % ps)
assert len(res) <= 16 << 20
open(sys.argv[3], 'wb').write(res)
print('%s: %d bytes, ramdisk %d -> %d' % (sys.argv[3], len(res), rs, len(newrd)))
