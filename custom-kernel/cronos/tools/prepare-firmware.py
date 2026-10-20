#!/usr/bin/env python3
"""Prepare local firmware inputs without adding binary firmware to the repository."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    root = Path(__file__).resolve().parents[1]
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--boot', required=True, type=Path, help='vendor boot image with FPGA firmware')
    ap.add_argument('--bluetooth', required=True, type=Path, help='locally supplied mt7668pr2h.bin')
    ap.add_argument('--regulatory', required=True, type=Path, help='directory with regulatory.db and its signature')
    ap.add_argument('--output', type=Path, default=root / 'out/firmware')
    a = ap.parse_args()
    files = {'amazon/' + name: data for name, data in load('extract-fpga-fw').extract(a.boot.read_bytes()).items()}
    files['mediatek/mt7668pr2h.bin'] = a.bluetooth.read_bytes()
    for name in ('regulatory.db', 'regulatory.db.p7s'):
        files[name] = (a.regulatory / name).read_bytes()
    source = root / 'sources/amazon-kernel/sound/soc/codecs/tas5805m_mono.h'
    files['tas5805m/tas5805m_dsp_default.bin'] = load('build-tas5805m-cfg').build(source.read_text())
    if any(not data for data in files.values()):
        raise ValueError('Firmware inputs must not be empty')
    for name, data in files.items():
        path = a.output / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
    manifest = {name: hashlib.sha256(data).hexdigest() for name, data in files.items()}
    (a.output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print('Prepared local firmware in', a.output)


if __name__ == '__main__':
    main()
