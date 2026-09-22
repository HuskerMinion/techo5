#!/usr/bin/env python3
"""Download the public build inputs into inputs/, for building TECHO5's images yourself. The installers
don't need this: they use the release.

    python3 tools/fetch-inputs.py --device show
    python3 tools/fetch-inputs.py --device spot --out ../techo5-spot/inputs
    python3 tools/fetch-inputs.py --device dot --dot ../techo5-dot --out ../techo5-dot/inputs

What it fetches, over HTTPS from Alpine's CDN and GitHub:
  alpine-minirootfs-3.24.1-armv7.tar.gz   Alpine's base image (pinned sha256)
  busybox.static                          from Alpine v3.24's busybox-static (armv7)
  apk.static                              apk-tools-static 2.14 (v3.22, x86_64), for the root filesystem build
  models/                                 okay_nabu, hey_jarvis, hey_mycroft, alexa, from esphome/micro-wake-word-models;
                                           computer, jarvis, hey_friday, glados, hal, terminator, marvin,
                                           home_assistant, from fwartner/home-assistant-wakewords-collection
                                           (both from a pinned commit, each file pinned to a sha256)
  show, spot: apks/, apks312/             tools/linux/packages.txt; the wpa_supplicant 2.9 set from Alpine v3.12
  spot:       apks/libgcc                 mkfs.ext4 needs it in the Spot's rescue initramfs
  dot:        apks-dot/, apks-bt-dot/,    techo5-dot's tools/linux/packages-rescue.txt, packages-bt.txt and
              apks-rootfs-dot/            packages-rootfs.txt (the last only unpacked into the root filesystem)

A package list names exact versions. Alpine keeps only the newest build of each package, so when a listed
one is gone the newest is taken and the script says so (docs/building.md, "Package versions"). Nothing is
kept unchecked: a package has to match the checksum Alpine's index gives for it and its own datahash, and
a model has to match the sha256 recorded here.
Windows, Linux and macOS alike; needs Python 3.
"""
import argparse
import base64
import hashlib
import io
import json
import os
import re
import sys
import tarfile
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import alpine, download, download_checked, fail, fetch, note, repo_root, run_main, step  # noqa: E402

MIRROR = 'https://dl-cdn.alpinelinux.org/alpine'
MODELS = ('okay_nabu', 'hey_jarvis', 'hey_mycroft', 'alexa')

# The models come from a commit, not from a branch: both repositories are other people's, and what
# main points at today is not what it points at tomorrow — these files end up inside a published image
# and in the directory the daemon parses as root, so what goes in is the bytes that were looked at.
# Each file's sha256 below is what that commit serves.
#
# Moving a pin: put the new commit here, run the script into an empty directory, and it stops at the
# first file whose sha256 is not the one listed, printing what the file now hashes to. Check the
# repository's history for what changed, then paste the new sums in.
MODELS_COMMIT = '05b65922cc433c9df13e98e32a7fe520758c837e'  # esphome/micro-wake-word-models, 2025-03-21
MODELS_REPO = 'https://raw.githubusercontent.com/esphome/micro-wake-word-models/%s/models/v2' % MODELS_COMMIT

# Wake words beyond esphome's built-in set, from the community collection at
# https://github.com/fwartner/home-assistant-wakewords-collection, which ships models with no manifest
# alongside them — the phrase is supplied here and written into one, the same shape a Home Assistant
# custom_wake_words offer arrives in (echod/internal/lib/wake).
EXTRA_MODELS = {
    'computer': ('en/computer/computer_v2.tflite', 'Computer'),
    'jarvis': ('en/jarvis/jarvis_v2.tflite', 'Jarvis'),
    'hey_friday': ('en/hey_friday/hey_Friday!.tflite', 'Hey Friday'),
    'glados': ('en/glados/glados.tflite', 'GLaDOS'),
    'hal': ('en/hal/hal_v2.tflite', 'HAL'),
    'terminator': ('en/terminator/Terminator.tflite', 'Terminator'),
    'marvin': ('en/marvin/marvin_v2.tflite', 'Marvin'),
    'home_assistant': ('en/home_assistant/Home_assistant.tflite', 'Home Assistant'),
}
EXTRA_MODELS_COMMIT = '8bcd2f20bb7b76c351b2eff871fa1ce873fe9be2'  # fwartner/home-assistant-wakewords-collection, 2026-01-13
EXTRA_MODELS_REPO = ('https://raw.githubusercontent.com/fwartner/home-assistant-wakewords-collection/%s'
                     % EXTRA_MODELS_COMMIT)

# sha256 of every model file fetched, keyed by the name it is written under. Recorded from what the two
# pinned commits serve; see the note above MODELS_COMMIT for moving a pin.
MODEL_SHA256 = {
    'okay_nabu.tflite': '0689abe1912a95a3318a0d8cb2e67bad0cbcfe3e24dd6e050c75debddfb6f891',
    'okay_nabu.json': '6dd65604f70fe5ea9d1af73a7bf239529d1fbabc363807f45d2b22ce464ddbed',
    'hey_jarvis.tflite': '21a7976add39ee24ec96c63d96b7aaa18e24d1d9824b963e451da8feb4b78b77',
    'hey_jarvis.json': 'b153867d818675d8abcc9dace474afe7f83551ae0d5a9b1d71a98681320185af',
    'hey_mycroft.tflite': 'c2a9b6ed51182db72e014781d5a4ece1929dc232a40b5b4be384f0295f0e1571',
    'hey_mycroft.json': '57b2b06fe5fdbbe834a242fabc7af31e4194a550fc382b2c88636a6d62d0d57e',
    'alexa.tflite': '9011a8155b04de858c48038529235cbc0e42e9fca05a55bf588cb80a653a723b',
    'alexa.json': '1d999798b35b1fe2606465b75ab840be51c1811d2909d5e620cefb6e96f8abd0',
    'computer.tflite': '411db364955bf7b7a13a50d732a8b59c129e2fbe130a54f9eb3c20ca183bc4d0',
    'jarvis.tflite': 'cb2102fc9a76d4e02a740760d5ba2060978d766869489000b3565c8c4f8493a5',
    'hey_friday.tflite': 'eb127d82d884a1ef4167b455ec67682bf362b9de3e232c3f8d544a5e6ab4cb8b',
    'glados.tflite': '7564b95e5deed29cecfd55fd34cac70da5307c0d86108a7f87bfc610c9724dec',
    'hal.tflite': '8ddbdc859eed8fbd648f4b5fe137f82e5e8480c1756fd12257e3ef5645275117',
    'terminator.tflite': '7feb69397a56a6933d3248ecca3ca6aaca7b6bf2c36f5beb9b63bb3d17647686',
    'marvin.tflite': 'ed91c4d83e28bcc0af1cdebe3ab4f2a4f2d4c9908101ad8476d2e1b9b77f4e0c',
    'home_assistant.tflite': '9f54305884abde30d484f18dbff582ed6e2f1a141b93d6fa5f7feb505a0c94aa',
}

_indexes = {}


def index(branch, repo, arch):
    """name -> (version, checksum) from the branch's package index. The checksum is the index's own
    C: field, which apk writes as Q1 (sha1) or Q2 (sha256) of the package's control segment."""
    key = (branch, repo, arch)
    if key not in _indexes:
        data = fetch('%s/%s/%s/%s/APKINDEX.tar.gz' % (MIRROR, branch, repo, arch))
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as t:
            text = t.extractfile('APKINDEX').read().decode('utf-8')
        versions = {}
        for block in text.split('\n\n'):
            p = re.search(r'^P:(.+)$', block, re.M)
            v = re.search(r'^V:(.+)$', block, re.M)
            c = re.search(r'^C:(.+)$', block, re.M)
            if p and v:
                versions[p.group(1)] = (v.group(1), c.group(1) if c else None)
        _indexes[key] = versions
    return _indexes[key]


def apk_segments(data):
    """An apk is three gzip streams one after another — the signature, the control data (.PKGINFO) and
    the files — so gzip's own reader, which stops at the end of the first, cannot tell them apart."""
    out = []
    off = 0
    while off < len(data):
        d = zlib.decompressobj(16 + zlib.MAX_WBITS)
        d.decompress(data[off:])
        end = len(data) - len(d.unused_data)
        if end <= off:
            break
        out.append(data[off:end])
        off = end
    return out


def apk_mismatch(path, csum):
    """What apk itself checks short of the signature, and returns why not when it does not hold: the
    control segment against the checksum the index carries, and the files against the datahash inside
    that control segment. Nothing downloaded here is signature-checked — that would need an RSA
    implementation this script does not have, and apk does it properly when the packages are installed
    (tools/linux/mkrootfs.sh). This catches a mirror, a proxy or a half-finished transfer handing back
    something other than the package the index describes, which matters because these files are
    unpacked into images by tools/linux/mkimage.py without apk ever seeing them.
    """
    if not csum:
        return 'the package index carries no checksum for it'
    algo = {'Q1': 'sha1', 'Q2': 'sha256'}.get(csum[:2])
    if not algo:
        return 'the index checksum %s is in a form this script does not know' % csum
    with open(path, 'rb') as f:
        data = f.read()
    # A file that is damaged rather than swapped fails here, in the middle of a gzip stream, so the
    # reading is done where it can be reported the same way as a checksum that does not match.
    try:
        segments = apk_segments(data)
        if len(segments) < 3:
            return 'not an apk: %d gzip streams, wanted 3' % len(segments)
        want = base64.b64decode(csum[2:])
        got = hashlib.new(algo, segments[1]).digest()
        if got != want:
            return 'its control data is %s, the index says %s' % (got.hex(), want.hex())
        with tarfile.open(fileobj=io.BytesIO(segments[1]), mode='r:gz') as t:
            info = t.extractfile('.PKGINFO').read().decode('utf-8', 'replace')
    except Exception as e:
        return 'it does not read as an apk: %s' % e
    m = re.search(r'^datahash = (\w+)$', info, re.M)
    if not m:
        return 'its control data names no datahash'
    got = hashlib.sha256(segments[2]).hexdigest()
    if got != m.group(1):
        return 'its files hash to %s, its own control data says %s' % (got, m.group(1))
    return None


def get_apk(filename, dest, branch='v3.24', arch='armv7'):
    """A listed package file (name-version-rN.apk): that version when the mirror still has it, else the newest."""
    m = re.match(r'^(.+?)-(\d[^-]*-r\d+)\.apk$', filename)
    if not m:
        fail('not a package file name: %s' % filename)
    name, want = m.group(1), m.group(2)
    for repo in ('main', 'community'):
        entry = index(branch, repo, arch).get(name)
        if not entry:
            continue
        have, csum = entry
        if have != want:
            note('%s %s is gone from Alpine %s; taking %s' % (name, want, branch, have))
        os.makedirs(dest, exist_ok=True)
        out = os.path.join(dest, '%s-%s.apk' % (name, have))
        if os.path.exists(out):
            # One fetched earlier is checked again rather than trusted: it is the same few milliseconds,
            # and an inputs tree that went wrong once should not keep feeding images.
            bad = apk_mismatch(out, csum)
            if bad:
                fail('%s does not match Alpine %s: %s. Delete it and run this again.' % (out, branch, bad))
            return out
        url = '%s/%s/%s/%s/%s-%s.apk' % (MIRROR, branch, repo, arch, name, have)
        unchecked = out + '.unchecked'
        download(url, unchecked)
        bad = apk_mismatch(unchecked, csum)
        if bad:
            os.remove(unchecked)
            fail('%s does not match the package index: %s' % (url, bad))
        os.replace(unchecked, out)
        return out
    fail('%s is in neither main nor community of Alpine %s' % (name, branch))


def get_list(path, dest, out_root):
    with open(path) as f:
        for line in f:
            a = line.split('#', 1)[0].strip()
            if not a or a.startswith('busybox-static-'):
                continue
            # The Wi-Fi drivers need wpa_supplicant 2.9, which lives in Alpine 3.12 with its libraries.
            if re.match(r'^(wpa_supplicant-2\.9|libssl1\.1|libcrypto1\.1|libnl3-3\.5)', a):
                get_apk(a, os.path.join(out_root, 'apks312'), 'v3.12')
            else:
                get_apk(a, dest)


def from_apk(apk, member, out):
    with tarfile.open(apk, 'r:gz') as t:
        data = t.extractfile(member).read()
    with open(out, 'wb') as f:
        f.write(data)
    os.chmod(out, 0o755)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--device', choices=('show', 'spot', 'dot'), default='show')
    ap.add_argument('--out', default=os.environ.get('TECHO5_INPUTS') or os.path.join(repo_root(), 'inputs'))
    ap.add_argument('--dot', default=os.path.join(os.path.dirname(repo_root()), 'techo5-dot'),
                    help='a techo5-dot checkout, for its package lists (default ../techo5-dot)')
    a = ap.parse_args()
    out = os.path.abspath(a.out)
    tmp = os.path.join(out, '.fetch')
    os.makedirs(tmp, exist_ok=True)

    step('Alpine base')
    alpine(out)
    step('busybox.static, apk.static')
    from_apk(get_apk('busybox-static-1.37.0-r31.apk', tmp), 'bin/busybox.static', os.path.join(out, 'busybox.static'))
    from_apk(get_apk('apk-tools-static-2.14.12-r0.apk', tmp, 'v3.22', 'x86_64'), 'sbin/apk.static', os.path.join(out, 'apk.static'))
    step('wake word models')
    os.makedirs(os.path.join(out, 'models'), exist_ok=True)
    for m in MODELS:
        for ext in ('tflite', 'json'):
            name = '%s.%s' % (m, ext)
            download_checked('%s/%s' % (MODELS_REPO, name), os.path.join(out, 'models', name), MODEL_SHA256[name])
    for m, (path, phrase) in EXTRA_MODELS.items():
        name = '%s.tflite' % m
        download_checked('%s/%s' % (EXTRA_MODELS_REPO, path), os.path.join(out, 'models', name), MODEL_SHA256[name])
        js = os.path.join(out, 'models', '%s.json' % m)
        if not os.path.exists(js):
            with open(js, 'w') as f:
                json.dump({'wake_word': phrase, 'model': '%s.tflite' % m, 'trained_languages': ['en']}, f, indent=2)
    step('packages for the %s' % a.device)
    if a.device in ('show', 'spot'):
        get_list(os.path.join(repo_root(), 'tools', 'linux', 'packages.txt'), os.path.join(out, 'apks'), out)
    if a.device == 'spot':
        get_apk('libgcc-15.2.0-r5.apk', os.path.join(out, 'apks'))
    if a.device == 'dot':
        lists = os.path.join(a.dot, 'tools', 'linux')
        if not os.path.exists(os.path.join(lists, 'packages-rescue.txt')):
            fail('no techo5-dot checkout at %s: pass --dot' % a.dot)
        get_list(os.path.join(lists, 'packages-rescue.txt'), os.path.join(out, 'apks-dot'), out)
        get_list(os.path.join(lists, 'packages-bt.txt'), os.path.join(out, 'apks-bt-dot'), out)
        # Kept apart from apks-dot: the rescue initramfs unpacks everything in that directory and is
        # flashed to a partition of a fixed size, so what only the running system needs goes here.
        get_list(os.path.join(lists, 'packages-rootfs.txt'), os.path.join(out, 'apks-rootfs-dot'), out)
    for f in os.listdir(tmp):
        os.remove(os.path.join(tmp, f))
    os.rmdir(tmp)
    print('Inputs are in %s.' % out)


if __name__ == '__main__':
    run_main(main)
