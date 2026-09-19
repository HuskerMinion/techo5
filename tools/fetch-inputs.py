#!/usr/bin/env python3
"""Download the public build inputs into inputs/, for building TECHO5's images yourself. The installers
don't need this: they use the release.

    python3 tools/fetch-inputs.py --device show
    python3 tools/fetch-inputs.py --device spot --out ../techo5-spot/inputs
    python3 tools/fetch-inputs.py --device dot --dot ../techo5-dot --out ../techo5-dot/inputs

What it fetches, over HTTPS from Alpine's CDN and GitHub:
  alpine-minirootfs-3.24.2-armv7.tar.gz   Alpine's base image (pinned sha256)
  busybox.static                          from Alpine v3.24's busybox-static (armv7)
  apk.static                              apk-tools-static 2.14 (v3.22, x86_64), for the root filesystem build
  models/                                 okay_nabu, hey_jarvis, hey_mycroft, alexa, from esphome/micro-wake-word-models;
                                           computer, jarvis, hey_friday, glados, hal, terminator, marvin,
                                           home_assistant, from fwartner/home-assistant-wakewords-collection
  show, spot: apks/, apks312/             tools/linux/packages.txt; the wpa_supplicant 2.9 set from Alpine v3.12
  spot:       apks/libgcc                 mkfs.ext4 needs it in the Spot's rescue initramfs
  dot:        apks-dot/, apks-bt-dot/     techo5-dot's tools/linux/packages-rescue.txt and packages-bt.txt

A package list names exact versions. Alpine keeps only the newest build of each package, so when a listed
one is gone the newest is taken and the script says so (docs/building.md, "Package versions").
Windows, Linux and macOS alike; needs Python 3.
"""
import argparse
import io
import json
import os
import re
import sys
import tarfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import alpine, download, fail, fetch, note, repo_root, run_main, step  # noqa: E402

MIRROR = 'https://dl-cdn.alpinelinux.org/alpine'
MODELS = ('okay_nabu', 'hey_jarvis', 'hey_mycroft', 'alexa')

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
EXTRA_MODELS_REPO = 'https://raw.githubusercontent.com/fwartner/home-assistant-wakewords-collection/main'

_indexes = {}


def index(branch, repo, arch):
    key = (branch, repo, arch)
    if key not in _indexes:
        data = fetch('%s/%s/%s/%s/APKINDEX.tar.gz' % (MIRROR, branch, repo, arch))
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as t:
            text = t.extractfile('APKINDEX').read().decode('utf-8')
        versions = {}
        for block in text.split('\n\n'):
            p = re.search(r'^P:(.+)$', block, re.M)
            v = re.search(r'^V:(.+)$', block, re.M)
            if p and v:
                versions[p.group(1)] = v.group(1)
        _indexes[key] = versions
    return _indexes[key]


def get_apk(filename, dest, branch='v3.24', arch='armv7'):
    """A listed package file (name-version-rN.apk): that version when the mirror still has it, else the newest."""
    m = re.match(r'^(.+?)-(\d[^-]*-r\d+)\.apk$', filename)
    if not m:
        fail('not a package file name: %s' % filename)
    name, want = m.group(1), m.group(2)
    for repo in ('main', 'community'):
        have = index(branch, repo, arch).get(name)
        if not have:
            continue
        if have != want:
            note('%s %s is gone from Alpine %s; taking %s' % (name, want, branch, have))
        os.makedirs(dest, exist_ok=True)
        out = os.path.join(dest, '%s-%s.apk' % (name, have))
        if not os.path.exists(out):
            download('%s/%s/%s/%s/%s-%s.apk' % (MIRROR, branch, repo, arch, name, have), out)
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
            f = os.path.join(out, 'models', '%s.%s' % (m, ext))
            if not os.path.exists(f):
                download('https://raw.githubusercontent.com/esphome/micro-wake-word-models/main/models/v2/%s.%s' % (m, ext), f)
    for m, (path, phrase) in EXTRA_MODELS.items():
        tf = os.path.join(out, 'models', '%s.tflite' % m)
        if not os.path.exists(tf):
            download('%s/%s' % (EXTRA_MODELS_REPO, path), tf)
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
    for f in os.listdir(tmp):
        os.remove(os.path.join(tmp, f))
    os.rmdir(tmp)
    print('Inputs are in %s.' % out)


if __name__ == '__main__':
    run_main(main)
