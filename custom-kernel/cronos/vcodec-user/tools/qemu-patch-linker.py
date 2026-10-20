#!/usr/bin/env python3
"""qemu test only: make the Android 11 linker tolerate a failing
personality(PER_LINUX32). See tools/qemu-test.sh. Never stage this on the
device."""
import hashlib
import sys

p = sys.argv[1]
d = bytearray(open(p, 'rb').read())
PINNED = '3d715adaad7f2c793e01ffd785ebdb0e76f7fa1e2fac836f5cc1d450934562f5'
if hashlib.sha256(d).hexdigest() != PINNED:
    sys.exit('linker is not the pinned redroid 11 one; find the new offset of the '
             'beq.n after the 2nd personality() call in __libc_init_AT_SECURE')
OFF = 0x86c7a  # vaddr 0x87c7a (text segment: file offset = vaddr - 0x1000)
assert d[OFF:OFF + 2] == b'\x13\xd0', d[OFF:OFF + 2].hex()  # beq.n <fatal>
d[OFF:OFF + 2] = b'\x00\xbf'                                 # nop
open(p, 'wb').write(d)
