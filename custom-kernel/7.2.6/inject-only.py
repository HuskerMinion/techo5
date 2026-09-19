#!/usr/bin/env python3
"""Pre-inject an amonet microloader into an existing boot image, changing nothing else.

    inject-only.py <lineage-dump> <boot.img> <out.img>

Same transformation the amonet payload performs when it flashes: the first 0x400 bytes of the header
move to 0x400 and the microloader takes their place. Used to put the stock TECHO5 release image on a
unit whose fallback fastboot cannot do the injection itself.
"""
import sys

ML = 0x400
src, img, out = sys.argv[1], sys.argv[2], sys.argv[3]
los = open(src, 'rb').read()
b = open(img, 'rb').read()
if los[:8] != b'ANDROID!' or los[ML:ML + 8] != b'ANDROID!':
    raise SystemExit('%s carries no microloader' % src)
if b[:8] != b'ANDROID!':
    raise SystemExit('%s is not a boot image' % img)
if b[ML:ML + 8] == b'ANDROID!':
    raise SystemExit('%s is already injected' % img)
res = los[:ML] + b[:ML] + b[0x800:]
assert len(res) == len(b)
open(out, 'wb').write(res)
print('%s: %d bytes, microloader from %s' % (out, len(res), src))
