#!/usr/bin/env python3
"""Keep a verified recovery image and flash only the shared boot partition."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import struct
import subprocess

LIMIT = 16 * 1024 * 1024


def image_info(path):
    if not 2048 <= path.stat().st_size <= LIMIT:
        raise ValueError('Boot image must fit the 16 MiB boot partition')
    data = path.read_bytes()
    if not 2048 <= len(data) <= LIMIT:
        raise ValueError('Boot image must fit the 16 MiB boot partition')
    if data[:8] != b'ANDROID!' or data[1024:1032] != b'ANDROID!':
        raise ValueError('Expected an amonet-injected Android boot image')
    kernel, ka, ramdisk, ra, second, sa, tags, page = struct.unpack_from('<8I', data, 1032)
    if page != 2048 or not kernel or not ramdisk or second:
        raise ValueError('Unsupported boot image layout')
    required = page + ((kernel + page - 1) // page + (ramdisk + page - 1) // page) * page
    if required > len(data):
        raise ValueError('Truncated boot image')
    ramdisk_offset = page + (kernel + page - 1) // page * page
    checksum = hashlib.sha1()
    for payload in (data[page:page + kernel], data[ramdisk_offset:ramdisk_offset + ramdisk], b''):
        checksum.update(payload)
        checksum.update(struct.pack('<I', len(payload)))
    if data[1600:1620] != checksum.digest():
        raise ValueError('Boot image payload checksum does not match its header')
    return {'sha256': hashlib.sha256(data).hexdigest(), 'size': len(data),
            'loader': hashlib.sha256(data[:1024]).hexdigest(),
            'addresses': [ka, ra, sa, tags], 'page_size': page}


def prepare(image, directory):
    info = image_info(image)
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    target = directory / 'boot-known-good.img'
    shutil.copyfile(image, target)
    target.chmod(0o600)
    manifest = directory / 'manifest.json'
    manifest.write_text(json.dumps({'format': 1, 'image': info}, indent=2) + '\n')
    manifest.chmod(0o600)
    check(directory)


def check(directory):
    manifest = json.loads((directory / 'manifest.json').read_text())
    image = directory / 'boot-known-good.img'
    info = image_info(image)
    if not isinstance(manifest, dict) or manifest.get('format') != 1 or manifest.get('image') != info:
        raise ValueError('Recovery image does not match its manifest')
    return image, info


def flash(image, directory, serial, executable='fastboot', dry_run=False):
    _, known = check(directory)
    candidate = image_info(image)
    if any(candidate[key] != known[key] for key in ('loader', 'addresses', 'page_size')):
        raise ValueError('Image does not match the recovery image bootloader layout')
    commands = [[executable, '-s', serial, 'flash', 'boot', str(image.resolve())],
                [executable, '-s', serial, 'reboot']]
    if dry_run:
        return commands
    devices = subprocess.run([executable, 'devices'], check=True, capture_output=True,
                             text=True, timeout=10).stdout
    if not any(line.split() == [serial, 'fastboot'] for line in devices.splitlines()):
        raise ValueError('Selected device is not in payload fastboot')
    for command in commands:
        subprocess.run(command, check=True, timeout=120)
    return commands


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest='command', required=True)
    p = sub.add_parser('prepare', help='save a boot image already verified on this unit')
    p.add_argument('--known-good', required=True, type=Path)
    p.add_argument('--directory', required=True, type=Path)
    p = sub.add_parser('check', help='verify the saved recovery image')
    p.add_argument('--directory', required=True, type=Path)
    for action in ('flash', 'restore'):
        p = sub.add_parser(action)
        p.add_argument('--directory', required=True, type=Path)
        p.add_argument('--serial', required=True)
        p.add_argument('--fastboot', default='fastboot')
        p.add_argument('--dry-run', action='store_true')
        if action == 'flash':
            p.add_argument('--image', required=True, type=Path)
    a = ap.parse_args()
    try:
        if a.command == 'prepare':
            prepare(a.known_good, a.directory)
            print('Recovery image saved and verified')
        elif a.command == 'check':
            check(a.directory)
            print('Recovery image verified')
        else:
            image = a.image if a.command == 'flash' else check(a.directory)[0]
            commands = flash(image, a.directory, a.serial, a.fastboot, a.dry_run)
            if a.dry_run:
                for command in commands:
                    print(json.dumps(command))
    except (OSError, ValueError, subprocess.SubprocessError) as e:
        ap.exit(1, str(e) + '\n')


if __name__ == '__main__':
    main()
