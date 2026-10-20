#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Link patches/scp-vdec-proxy/proxy.c into the MT8183 SCP firmware, for the media blocks it drives.

    scp-vdec-proxy-fw.py SCP.IMG

proxy.c (see its header) makes the firmware reach the media blocks' registers through the AP: a trap
on them, and a mailbox the kernel serves (scp-mmproxy.py). This compiles it for the SCP's
Cortex-M4 (arm-none-eabi-gcc, in the build image) at SCP address 0x104fe000, which is the AP's
0x504fe000 (the SCP's cached view of its DRAM, kukui_scp's 0x10000000 = 0x50000000), the last two
pages of the 5 MiB the kernel gives the firmware (mtk_scp's max_dram_size; the firmware's own DRAM
image ends at 0x5009b7d0), and adds it to scp.img as a third loadable segment, the mailbox page after
it zero-filled. Then two pointers change:

  - the MemManage vector (SCP address 0x10) becomes proxy.c's mm_entry;
  - the IPI handler table's entries (at 0x42508, its unused entries 0x50d) for the blocks served,
    SCP_IPI_VDEC_H264 (1), SCP_IPI_VENC_H264 (4), SCP_IPI_DIP (9) and SCP_IPI_FD_CMD (12), become
    proxy.c's ipi_trampoline_N, which arm the trap and call the original handler, recorded in its
    ipi_original[N]. The trap is armed by whichever comes first, so every block is served from its
    first message on.

Idempotent.
"""
import os
import struct
import subprocess
import sys
import tempfile

TEXT_FILE_OFFSET = 0x10000
VECTOR_MEMMANAGE = 0x10
IPI_TABLE = 0x42508
# the IPIs routed through proxy.c, and their handlers in kukui_scp_v2.0.13324
HANDLERS = {1: 0x25f97, 4: 0x2a841, 9: 0x25f59, 12: 0x2a88d}
DEFAULT_HANDLER = 0xb49
LOAD_SCP, LOAD_AP = 0x104fe000, 0x504fe000
PAGE = 0x1000

here = os.path.dirname(os.path.abspath(__file__))
path = sys.argv[1]
b = bytearray(open(path, "rb").read())


def word(off):
    return struct.unpack_from("<I", b, TEXT_FILE_OFFSET + off)[0]


if word(IPI_TABLE + 4) & ~0xfff == LOAD_SCP:
    print("scp-vdec-proxy-fw: already patched")
    sys.exit(0)
for ipi, handler in HANDLERS.items():
    if word(IPI_TABLE + 4 * ipi) != handler:
        sys.exit("scp-vdec-proxy-fw: %s: IPI entry %d is %#x, not %#x"
                 % (path, ipi, word(IPI_TABLE + 4 * ipi), handler))
if word(VECTOR_MEMMANAGE) != DEFAULT_HANDLER:
    sys.exit("scp-vdec-proxy-fw: %s: the MemManage vector is not the default handler" % path)

# The ELF: 32-bit LSB ARM, program headers right after the header with room before 0x10000
e_phoff, = struct.unpack_from("<I", b, 0x1c)
e_phentsize, e_phnum = struct.unpack_from("<HH", b, 0x2a)
if b[:4] != b"\x7fELF" or e_phentsize != 32 or e_phoff != 52 or e_phoff + 32 * (e_phnum + 1) > TEXT_FILE_OFFSET:
    sys.exit("scp-vdec-proxy-fw: %s: not the ELF layout expected" % path)
if any(b[e_phoff + 32 * e_phnum:e_phoff + 32 * (e_phnum + 1)]):
    sys.exit("scp-vdec-proxy-fw: %s: no free room for another program header" % path)

with tempfile.TemporaryDirectory() as t:
    ld = os.path.join(t, "proxy.ld")
    open(ld, "w").write("""
MEMORY { DRAM (rwx) : ORIGIN = %#x, LENGTH = %#x }
SECTIONS {
	.text : { *(.text*) *(.rodata*) *(.data*) *(.bss*) *(COMMON) . = ALIGN(4); } > DRAM
	/DISCARD/ : { *(.ARM.*) *(.comment) }
}
""" % (LOAD_SCP, PAGE))
    elf = os.path.join(t, "proxy.elf")
    subprocess.run(["arm-none-eabi-gcc", "-mcpu=cortex-m4", "-mthumb", "-mfloat-abi=soft",
                    "-Os", "-ffreestanding", "-fno-pic", "-fno-common", "-nostdlib",
                    "-Wall", "-Werror", "-Wl,--no-warn-rwx-segments", "-T", ld, "-o", elf,
                    os.path.join(here, "scp-vdec-proxy", "proxy.c")], check=True)
    blob = os.path.join(t, "proxy.bin")
    subprocess.run(["arm-none-eabi-objcopy", "-O", "binary", "-j", ".text", elf, blob], check=True)
    code = open(blob, "rb").read()
    syms = {}
    for line in subprocess.run(["arm-none-eabi-nm", elf], check=True, capture_output=True,
                               text=True).stdout.splitlines():
        f = line.split()
        if len(f) == 3:
            syms[f[2]] = int(f[0], 16)
if len(code) > PAGE:
    sys.exit("scp-vdec-proxy-fw: proxy.c is %d bytes, more than its page" % len(code))
names = ["mm_entry", "ipi_original"] + ["ipi_trampoline_%d" % i for i in HANDLERS]
for s in names:
    if not LOAD_SCP <= syms.get(s, 0) < LOAD_SCP + PAGE:
        sys.exit("scp-vdec-proxy-fw: %s not where it should be" % s)

code = bytearray(code)
for ipi, handler in HANDLERS.items():
    struct.pack_into("<I", code, syms["ipi_original"] - LOAD_SCP + 4 * ipi, handler)

# The segment: code page, then the mailbox page, both zero-filled beyond the code
seg = code + bytes(2 * PAGE - len(code))
p_offset = (len(b) + 0xfff) & ~0xfff
b += bytes(p_offset - len(b)) + seg
struct.pack_into("<8I", b, e_phoff + 32 * e_phnum,
                 1, p_offset, LOAD_SCP, LOAD_AP, len(seg), len(seg), 7, 0x1000)
struct.pack_into("<H", b, 0x2c, e_phnum + 1)
struct.pack_into("<I", b, TEXT_FILE_OFFSET + VECTOR_MEMMANAGE, syms["mm_entry"] | 1)
for ipi in HANDLERS:
    struct.pack_into("<I", b, TEXT_FILE_OFFSET + IPI_TABLE + 4 * ipi, syms["ipi_trampoline_%d" % ipi] | 1)
open(path, "wb").write(b)
print("scp-vdec-proxy-fw: linked %d bytes at %#x (mm_entry %#x; IPIs %s through proxy.c)"
      % (len(code), LOAD_SCP, syms["mm_entry"], ", ".join(str(i) for i in HANDLERS)))
