#!/usr/bin/env python3
"""Generate the amplifier configuration from the pinned vendor source."""
import argparse
import re
from pathlib import Path


def build(source):
    text = re.sub(r"/\*.*?\*/|//[^\n]*", "", source, flags=re.S)
    match = re.search(r"tas5805m_init_mono_mini\[\]\s*=\s*\{(.*?)\};", text, re.S)
    if not match:
        raise ValueError("MONO_MINI table is missing")
    pairs = re.findall(r"\{\s*(0x[0-9a-fA-F]+)\s*,\s*(0x[0-9a-fA-F]+)\s*\}", match[1])
    if len(pairs) < 10:
        raise ValueError("MONO_MINI table is incomplete")
    ops = [(int(a, 16), int(b, 16)) for a, b in pairs]
    # The mainline driver supplies reset and HiZ before loading the remaining table.
    if ops[6:9] != [(0, 0), (0x7f, 0), (3, 2)]:
        raise ValueError("Unexpected amplifier startup sequence")
    return bytes(v for pair in ops[9:] for v in pair)


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("source", type=Path)
    ap.add_argument("output", type=Path)
    a = ap.parse_args()
    a.output.parent.mkdir(parents=True, exist_ok=True)
    a.output.write_bytes(build(a.source.read_text()))
