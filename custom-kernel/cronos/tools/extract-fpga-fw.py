#!/usr/bin/env python3
"""Extract both microphone FPGA bitstreams from a locally supplied vendor boot image."""
import argparse
import struct
import zlib
from pathlib import Path


def extract(blob):
    offset = 0x400 if blob[0x400:0x408] == b"ANDROID!" else 0
    if blob[offset:offset + 8] != b"ANDROID!" or len(blob) < offset + 40:
        raise ValueError("Not an Android boot image")
    size, _, _, _, _, _, _, page = struct.unpack_from("<8I", blob, offset + 8)
    start = 0x800 if offset else page
    if page not in (2048, 4096, 8192, 16384) or not size or start + size > len(blob):
        raise ValueError("Invalid or truncated kernel payload")
    kernel = blob[start:start + size]
    gzip = kernel.find(b"\x1f\x8b\x08")
    if gzip < 0:
        raise ValueError("Vendor kernel has no gzip payload")
    image = zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(kernel[gzip:])
    base = 0xffffff8008080000
    result = {}
    for name in ("i2s_to_spi_4ch_v208.bin", "i2s_to_spi_4ch_v193.bin"):
        pos = image.find(name.encode() + b"\0")
        if pos < 0:
            raise ValueError("Missing FPGA firmware: " + name)
        pointer = struct.pack("<Q", base + pos)
        entry = image.find(pointer)
        while entry >= 0:
            if entry + 24 <= len(image):
                addr, length = struct.unpack_from("<QQ", image, entry + 8)
                off = addr - base
                if 0 < length < 200000 and 0 <= off <= len(image) - length and image[off:off+2] == b"\xff\x00":
                    result[name] = image[off:off+length]
                    break
            entry = image.find(pointer, entry + 1)
        if name not in result:
            raise ValueError("Invalid FPGA firmware entry: " + name)
    return result


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("boot", type=Path)
    ap.add_argument("output", type=Path)
    a = ap.parse_args()
    files = extract(a.boot.read_bytes())
    a.output.mkdir(parents=True, exist_ok=True)
    for name, data in files.items():
        (a.output / name).write_bytes(data)
