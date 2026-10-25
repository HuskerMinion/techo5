#!/usr/bin/env python3
"""Install TECHO5 on a Skylight Calendar 15 (150-CAL, Rockchip RK3566) from its stock Android, in one
command, over adb.

The Skylight keeps its stock U-Boot, boot and super (Android itself). Android's userdata partition is
split in two of TECHO5's own: techo5-store (2 GiB, the slot store) and techo5-data (the rest, TECHO5's
state). TECHO5's kernel goes into the recovery partition, and "boot-recovery" at the start of misc
makes the stock U-Boot start it on every boot. See tools/linux/skylight/README.md.

    python3 tools/install-skylight.py --name Kitchen
    python3 tools/install-skylight.py --name Kitchen --wifi MyNetwork
    python3 tools/install-skylight.py --dry-run
    python3 tools/install-skylight.py --installer i.img --boot b.img --store s.img.gz --dry-run

It works only on what it knows: stock firmware D156.V1.3 (a userdebug build, where `adb root` works
without a prompt), the 150-CAL board with its 1920x1080 screen, a 16 GB eMMC and the stock partition
table. Anything else stops it before anything is written. Each step is checked before the next:

  1. checks    adb sees one Skylight running stock Android; the firmware is in KNOWN_FIRMWARE; adb root;
               the board (RK3566, the touch controller where the 150-CAL has it, the screen's size when
               the device tree shows it); the eMMC's size; the partition table is the stock one, both
               copies; the Wi-Fi and Bluetooth firmware are on the vendor partition
  2. images    the installer image, TECHO5's boot image and the store image: the release's, checked
               against its signed manifest, or yours (--installer, --boot, --store); the new partition
               table, made from the unit's own (tools/linux/skylight/install/mkgpt.py); room on /cache
               for the set
  3. backup    the whole eMMC (about 15.6 GB) and its two boot areas over `adb exec-out`, into
               backups/skylight-<serial>/, with the sha256 of the stream; every partition Android does
               not have mounted read-write is read again on the unit and must match. The backup notes
               the eMMC's CID and is only taken for that chip. Without a verified backup nothing is
               written (--backup points at one made before)
  4. firmware  the unit's own Wi-Fi and Bluetooth firmware, copied into the backup folder. It is the
               vendor's, so it never goes into a release or the repository: it goes back onto this unit
               only, into TECHO5's store, during the install
  5. confirm   says exactly what is erased and how to go back, and asks once
  6. stage     the set onto /cache/techo5-install, each file checked on the unit; the installer image
               into recovery, read back; then `adb reboot recovery`
  7. install   on the unit, by itself: the installer image runs install.sh, which writes the new table,
               the store, techo5-data, the firmware and settings, TECHO5's kernel into recovery and,
               last, boot-recovery into misc; each read back. Done, it restarts into TECHO5. Stopped
               before writing, it restarts into Android, and this says why

Written from Android: only /cache/techo5-install and the recovery partition. Everything else is
install.sh's, on the unit, after the confirm.

--resume picks up a set staged before (after a stop, or when the installer stopped and the unit came
back to Android): the staged files are checked again, nothing is downloaded or backed up again.
--dry-run runs every check and prints every command that would write, and writes nothing to the unit.

Windows, Linux and macOS alike; needs Python 3 and adb. The way back is printed at the end, written
into the backup folder (restore/RESTORE.txt) and in docs/install-skylight.md.
"""
import argparse
import gzip
import hashlib
import importlib.util
import os
import re
import shutil
import struct
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import (CONSOLE_TECHO5, Adb, Release, adb_devices, ask_name, ask_wifi, confirm,  # noqa: E402
                       default_dir, fail, list_consoles, need, new_api_key, note, pick_unit, run_main, step,
                       valid_api_key, wifi_conf, write_private)

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location('mkgpt', os.path.join(HERE, 'linux', 'skylight', 'install', 'mkgpt.py'))
mkgpt = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mkgpt)

REPO = 'HuskerMinion/techo5'
BOARD_NAME = 'Skylight Calendar 15 (150-CAL)'
DEFAULT_NAME = 'Skylight Calendar'

# The stock firmware this installer knows: ro.build.fingerprint up to its build type, and what to call
# it. One that is not here stops the installer before anything is written. Add one only once an
# install on it has been checked end to end; it must be a userdebug build, where adb root works.
KNOWN_FIRMWARE = (
    ('rockchip/D156/D156:12/AndroidS/D156.V1.3:userdebug/', 'D156.V1.3'),
)

# The board: Rockchip RK3566, the Goodix touch controller on I2C5 at 0x5d where the 150-CAL has it,
# and the 1920x1080 screen (when the stock device tree's DSI panel node shows its timing).
SOC = 'rockchip,rk3566'
TOUCH_NODE = '/sys/bus/i2c/devices/5-005d'
SCREEN = (1920, 1080)
# The eMMC: the 16 GB class, 14 to 15 GiB in 512-byte sectors.
EMMC_SECTORS = (14 << 21, 15 << 21)

# The unit's own Wi-Fi and Bluetooth firmware, on its stock vendor partition. Never published.
FIRMWARE_DIR = '/vendor/etc/firmware'
FIRMWARE = ('fw_bcm43456c5_ag.bin', 'nvram_ap6256.txt', 'BCM4345C5.hcd')

# Where the set waits for the installer image: Android's cache partition, which install.sh never
# writes. userdata cannot hold it: the install writes over it, and the installer cannot read it.
STAGE = '/cache/techo5-install'
STAGE_MARGIN = 16 << 20
# The installer image waits here before it goes into recovery (it is not needed on /cache).
RECOVERY_TMP = '/data/local/tmp/techo5-installer.img'
# Mounted read-write by Android while the backup runs: copied as they are, not compared.
LIVE = {'userdata', 'cache', 'metadata'}

EXT4_MAGIC = b'\x53\xef'
STORE_LABEL = b'techo5-store'
SECTOR = 512
GPT_BYTES = 33 * SECTOR

WIFI_CONF = 'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid=%s\n\tpsk=%s\n}\n'


def store_name(version):
    return 'techo5-store-skylight-%s.img.gz' % version


def boot_name(version):
    return 'techo5-boot-skylight-%s.img' % version


def installer_name(version):
    return 'techo5-installer-skylight-%s.img' % version


def quote(s):
    return "'" + s.replace("'", "'\\''") + "'"


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def first_word(text):
    return (text or '').split(' ')[0].strip()


def read_file(path):
    with open(path, 'rb') as f:
        return f.read()


class SkyAdb(Adb):
    def stream(self, command):
        """A command's raw output as a pipe, for reading as it comes."""
        return subprocess.Popen(self.cmd + ['exec-out', command], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)


class Writer:
    """Everything that writes to the unit goes through here. In a dry run it prints the command and
    does nothing."""

    def __init__(self, adb, dry_run):
        self.adb, self.dry_run = adb, dry_run

    def sh(self, command):
        if self.dry_run:
            print('   would run: adb shell %s' % command)
            return ''
        return self.adb.sh(command)

    def push(self, local, remote):
        if self.dry_run:
            print('   would run: adb push %s %s' % (local, remote))
            return
        self.adb.push(local, remote)


# ------------------------------------------------------------------------------------------ the unit

def read_bytes(adb, command, work, name='skylight-read.bin'):
    """A command's output, byte for byte (adb shell's text output would mangle binary data)."""
    tmp = os.path.join(work, name)
    adb.exec_out_to_file(command, tmp)
    try:
        return read_file(tmp)
    finally:
        os.remove(tmp)


def known_firmware(fingerprint):
    for prefix, name in KNOWN_FIRMWARE:
        if fingerprint.startswith(prefix):
            return name
    return None


def check_firmware(adb):
    """The stock firmware, by its fingerprint: one this installer knows, a userdebug build."""
    fp = adb.sh('getprop ro.build.fingerprint')
    name = known_firmware(fp)
    if not name:
        fail("this unit's firmware is '%s', which this installer doesn't know. It knows: %s.\n"
             '   Nothing was written. A newer Skylight update may have changed what the install relies on\n'
             '   (adb root, the partition table); see "Firmware the installer doesn\'t know" in\n'
             '   docs/install-skylight.md.' % (fp or 'unknown', ', '.join(n for _, n in KNOWN_FIRMWARE)))
    btype = adb.sh('getprop ro.build.type')
    if btype != 'userdebug':
        fail("the firmware's build type is '%s', not userdebug: adb root does not work on it" % btype)
    return name


def check_root(adb):
    if adb.sh('id').split('(')[0] == 'uid=0':
        return
    adb.root()
    if adb.sh('id').split('(')[0] != 'uid=0':
        fail('adb root did not give a root shell on this unit. The installer needs it to read and write\n'
             '   the eMMC; on the firmware it knows, adb root works without a prompt.')


def check_board(adb, work):
    """RK3566, the 150-CAL's touch controller, and the screen's size when the device tree has it.
    Returns what to say about the screen."""
    compat = [c for c in read_bytes(adb, 'cat /proc/device-tree/compatible 2>/dev/null', work)
              .decode('ascii', 'replace').split('\0') if c]
    if SOC not in compat:
        fail("this unit's device tree says it is %s, not an RK3566: not a Skylight Calendar 15"
             % (' '.join(compat) or 'nothing'))
    if adb.sh('[ -e %s ] && echo YES' % TOUCH_NODE) != 'YES':
        fail('no touch controller at %s, where the 150-CAL has its Goodix: this is not the board this\n'
             '   installer knows' % TOUCH_NODE)
    found = adb.sh('ls -d /proc/device-tree/dsi@*/panel*/display-timings/*/hactive 2>/dev/null')
    sizes = []
    for path in found.split('\n'):
        path = path.strip()
        if not path.endswith('/hactive'):
            continue
        h = read_bytes(adb, 'cat %s' % quote(path), work)
        v = read_bytes(adb, 'cat %s' % quote(path[:-len('hactive')] + 'vactive'), work)
        if len(h) == 4 and len(v) == 4:
            sizes.append((int.from_bytes(h, 'big'), int.from_bytes(v, 'big')))
    if not sizes:
        return 'screen: its timing is not in the device tree; going by the firmware and the board'
    if SCREEN not in sizes:
        fail("the screen in this unit's device tree is %s, not %dx%d: not the panel TECHO5's kernel drives"
             % (', '.join('%dx%d' % s for s in sizes), SCREEN[0], SCREEN[1]))
    return 'screen: %dx%d, the panel TECHO5\'s kernel drives' % SCREEN


def find_disk(adb):
    """(eMMC name, its size in sectors), from the misc partition's link."""
    link = adb.sh('readlink -f /dev/block/by-name/misc')
    m = re.fullmatch(r'/dev/block/(mmcblk\d+)p(\d+)', link)
    if not m:
        fail("misc is '%s', not a partition of the eMMC" % link)
    disk = m.group(1)
    size = adb.sh('cat /sys/class/block/%s/size' % disk)
    if not size.isdigit():
        fail('could not read the size of %s' % disk)
    total = int(size)
    lo, hi = EMMC_SECTORS
    if not lo <= total <= hi:
        fail('the eMMC is %d MiB; the installer knows the 16 GB one (%d to %d MiB)'
             % (total >> 11, lo >> 11, hi >> 11))
    return disk, total


def read_cid(adb, disk):
    """The eMMC's CID: the chip's own id, which ties a backup to this unit."""
    cid = adb.sh('cat /sys/class/block/%s/device/cid 2>/dev/null' % disk).strip().lower()
    if not re.fullmatch(r'[0-9a-f]{32}', cid):
        fail('could not read the CID of %s (the eMMC chip\'s id)' % disk)
    return cid


def read_table(adb, disk, total, work):
    """The disk's first 34 sectors and last 33: the stock GPT, both copies."""
    first = read_bytes(adb, 'dd if=/dev/block/%s bs=512 count=34 2>/dev/null' % disk, work)
    last = read_bytes(adb, 'dd if=/dev/block/%s bs=512 skip=%d count=33 2>/dev/null' % (disk, total - 33), work)
    if len(first) != 34 * SECTOR or len(last) != 33 * SECTOR:
        fail('reading the partition table gave %d and %d bytes' % (len(first), len(last)))
    return first, last


def check_table(first, last, total):
    try:
        layout = mkgpt.stock_layout(first, total, last)
    except mkgpt.GPTError as e:
        fail('the partition table is not the stock one this installer knows: %s.\n'
             '   Nothing was written.' % e)
    return layout


def dev(disk, n):
    return '/dev/block/%sp%d' % (disk, n)


def check_names(adb, disk, layout):
    """The kernel's view agrees with the table: each partition the install relies on, by name."""
    for name in ('misc', 'recovery', 'cache', 'userdata'):
        n = layout[name][0]
        link = adb.sh('readlink -f /dev/block/by-name/%s' % name)
        got = adb.sh('cat /sys/class/block/%sp%d/uevent' % (disk, n))
        if link != dev(disk, n) or ('PARTNAME=%s' % name) not in got.split('\n'):
            fail("partition %d is not %s as the table says (by-name: '%s')" % (n, name, link))


def check_vendor_firmware(adb):
    sizes = {}
    for f in FIRMWARE:
        out = adb.sh('stat -c %%s %s/%s 2>/dev/null' % (FIRMWARE_DIR, f))
        if not out.isdigit() or int(out) == 0:
            fail('%s/%s is not on this unit. TECHO5 needs the unit\'s own Wi-Fi and Bluetooth firmware\n'
                 '   from there; this is not the firmware the installer knows.' % (FIRMWARE_DIR, f))
        sizes[f] = int(out)
    return sizes


def cache_free(adb):
    """Bytes free on /cache, which has to be Android's cache partition, mounted."""
    if not re.search(r'\s/cache\s', adb.sh('cat /proc/mounts')):
        fail('/cache is not mounted on this unit; the installer stages its files there')
    out = adb.sh('df -k /cache').split('\n')
    f = out[-1].split() if out else []
    if len(f) < 4 or not f[3].isdigit():
        fail('could not read the free space on /cache')
    return int(f[3]) * 1024


# ------------------------------------------------------------------------------------------ backup

def backup_name(serial):
    return 'skylight-' + re.sub(r'[^A-Za-z0-9._-]', '_', serial)


def stream_to_file(adb, command, path, size, ranges=(), what='', every=30):
    """A device read into a file, hashed as it arrives: (sha256 of the whole stream, {name: sha256}
    of each byte range in ranges [(name, start, end)])."""
    whole = hashlib.sha256()
    hs = {name: hashlib.sha256() for name, _, _ in ranges}
    p = adb.stream(command)
    got, said, start = 0, time.time(), time.time()
    with open(path + '.partial', 'wb') as f:
        while True:
            chunk = p.stdout.read(4 << 20)
            if not chunk:
                break
            f.write(chunk)
            whole.update(chunk)
            end = got + len(chunk)
            for name, a, b in ranges:
                if a < end and b > got:
                    hs[name].update(chunk[max(a, got) - got:min(b, end) - got])
            got = end
            if what and time.time() - said >= every:
                rate = got / max(time.time() - start, 1)
                note('%s: %d of %d MiB (%.0f MiB/s, about %d min left)'
                     % (what, got >> 20, size >> 20, rate / (1 << 20), (size - got) / max(rate, 1) / 60))
                said = time.time()
    p.wait()
    if got != size:
        os.remove(path + '.partial')
        fail('reading %s gave %d bytes, not %d; the backup is not complete. Check the USB cable and run '
             'this again.' % (what or command, got, size))
    os.replace(path + '.partial', path)
    return whole.hexdigest(), {k: h.hexdigest() for k, h in hs.items()}


def make_backup(adb, disk, total, layout, first, folder, cid):
    """The whole eMMC and its boot areas into folder, verified. Read only."""
    os.makedirs(folder, exist_ok=True)
    size = total * SECTOR
    free = shutil.disk_usage(folder).free
    if free < size + (1 << 30):
        fail('the backup needs %d MiB free in %s, and there are %d MiB' % ((size >> 20) + 1024, folder, free >> 20))
    ranges = [('first34', 0, 34 * SECTOR), ('last33', size - GPT_BYTES, size)]
    ranges += [(name, a * SECTOR, (b + 1) * SECTOR) for name, (_, a, b) in layout.items() if name not in LIVE]
    note('reading the whole eMMC (%d MiB) over USB; this takes a while' % (size >> 20))
    img = os.path.join(folder, 'emmc.img')
    whole, got = stream_to_file(adb, 'dd if=/dev/block/%s bs=4194304 2>/dev/null' % disk, img, size, ranges,
                                'the eMMC')
    if got['first34'] != hashlib.sha256(first).hexdigest():
        fail('the backup\'s partition table is not the one the unit showed a moment ago; run this again')
    lines = ['# the eMMC of this unit, %d sectors; sha256 on the unit of each partition, checked against '
             'the backup' % total]
    for name, (n, a, b) in sorted(layout.items(), key=lambda kv: kv[1][0]):
        if name in LIVE:
            lines.append('p%-2d %-14s %10d %10d  mounted by Android: copied as it was, not compared' % (n, name, a, b))
            continue
        theirs = first_word(adb.sh('sha256sum %s' % dev(disk, n)))
        if theirs != got[name]:
            fail('the backup of p%d (%s) is not what the unit reads (%s, the unit: %s); run this again'
                 % (n, name, got[name][:16], theirs[:16]))
        lines.append('p%-2d %-14s %10d %10d  %s' % (n, name, a, b, theirs))
    last = read_bytes(adb, 'dd if=/dev/block/%s bs=512 skip=%d count=33 2>/dev/null' % (disk, total - 33), folder)
    if hashlib.sha256(last).hexdigest() != got['last33']:
        fail('the backup\'s last 33 sectors (the backup partition table) are not what the unit reads')
    sums = ['%s  emmc.img' % whole]
    for b in ('boot0', 'boot1'):
        n = adb.sh('cat /sys/class/block/%s%s/size 2>/dev/null' % (disk, b))
        if not n.isdigit() or not int(n):
            continue
        name = 'emmc-%s.img' % b
        h, _ = stream_to_file(adb, 'dd if=/dev/block/%s%s bs=1048576 2>/dev/null' % (disk, b),
                              os.path.join(folder, name), int(n) * SECTOR)
        if h != first_word(adb.sh('sha256sum /dev/block/%s%s' % (disk, b))):
            fail('the backup of %s%s is not what the unit reads' % (disk, b))
        sums.append('%s  %s' % (h, name))
    with open(os.path.join(folder, 'partitions.txt'), 'w', newline='\n') as f:
        f.write('\n'.join(lines) + '\n')
    with open(os.path.join(folder, 'SHA256SUMS'), 'w', newline='\n') as f:
        f.write('\n'.join(sums) + '\n')
    write_restore(folder, total, layout, first, last, img)
    # Last, so a backup that stopped anywhere above is never taken as verified.
    with open(os.path.join(folder, 'verified'), 'w', newline='\n') as f:
        f.write('verified %s sectors=%d sha256=%s cid=%s\n'
                % (time.strftime('%Y-%m-%dT%H:%M:%S'), total, whole, cid))
    return img


def backup_cid(folder):
    """The CID a finished backup in folder was made of: None without one, '' when it does not say."""
    try:
        marker = read_file(os.path.join(folder, 'verified')).decode('ascii', 'replace')
    except OSError:
        return None
    c = re.search(r'\bcid=([0-9a-f]{32})\b', marker)
    return c.group(1) if c else ''


def backup_verified(folder, total, first, cid, rehash=True):
    """Whether folder holds a verified backup of this unit as it is now: made and checked by this
    installer, of this eMMC chip (its CID), the eMMC's size, the same partition table, and (rehash) the
    image still matching its sha256. Returns '' when it does, else why not."""
    try:
        marker = read_file(os.path.join(folder, 'verified')).decode('ascii', 'replace')
        sums = read_file(os.path.join(folder, 'SHA256SUMS')).decode('ascii', 'replace')
    except OSError:
        return 'no verified backup in %s' % folder
    m = re.search(r'sectors=(\d+) sha256=([0-9a-f]{64})', marker)
    if not m or int(m.group(1)) != total:
        return 'the backup in %s is of another eMMC' % folder
    c = backup_cid(folder)
    if not c:
        return ('the backup in %s does not say which eMMC chip it is of (made before the installer wrote the '
                'CID down), so it cannot be tied to this unit. Move that folder aside and run this again for '
                'a new backup' % folder)
    if c != cid:
        return 'the backup in %s is of another unit (another eMMC chip)' % folder
    if ('%s  emmc.img' % m.group(2)) not in sums.splitlines():
        return 'the backup in %s does not agree with its SHA256SUMS' % folder
    img = os.path.join(folder, 'emmc.img')
    try:
        if os.path.getsize(img) != total * SECTOR:
            return '%s is not the size of the eMMC' % img
        with open(img, 'rb') as f:
            if f.read(34 * SECTOR) != first:
                return 'the backup in %s holds another partition table than the unit has now' % folder
    except OSError:
        return 'no emmc.img in %s' % folder
    if rehash:
        note('checking the backup in %s against its sha256' % folder)
        if sha256_file(img) != m.group(2):
            return '%s does not match its sha256: the copy is damaged' % img
    return ''


def write_restore(folder, total, layout, first, last, img):
    """restore/: the pieces of the backup the install changes, and RESTORE.txt, the way back with
    rkdeveloptool in Loader mode, with this unit's own numbers."""
    r = os.path.join(folder, 'restore')
    os.makedirs(r, exist_ok=True)
    with open(os.path.join(r, 'lba0-33.bin'), 'wb') as f:
        f.write(first)
    with open(os.path.join(r, 'last33.bin'), 'wb') as f:
        f.write(last)
    with open(img, 'rb') as src:
        for name in ('recovery', 'misc'):
            _, a, b = layout[name]
            src.seek(a * SECTOR)
            with open(os.path.join(r, name + '.img'), 'wb') as f:
                f.write(src.read((b - a + 1) * SECTOR))
    with open(os.path.join(r, 'zero-1MiB.bin'), 'wb') as f:
        f.write(bytes(1 << 20))
    with open(os.path.join(r, 'RESTORE.txt'), 'w') as f:
        f.write(restore_text(total, layout))


def restore_text(total, layout):
    return '\n'.join([
        'The way back to stock Android for this unit (docs/install-skylight.md, "The way back").',
        '',
        'Loader mode: unplug the power, hold Volume down and plug the power back in.',
        '`rkdeveloptool ld` lists the unit as Loader. In this folder\'s parent (the backup):',
        '',
        'A. The unit as it came, the whole eMMC (writes %d MiB; slow):' % (total >> 11),
        '',
        '    rkdeveloptool wl 0 emmc.img',
        '    rkdeveloptool rd',
        '',
        'B. Only what the install changed: the partition table, recovery and misc, and the start of',
        '   userdata cleared. Android then starts as a new unit, with a fresh setup:',
        '',
        '    rkdeveloptool wl 0 restore/lba0-33.bin',
        '    rkdeveloptool wl %d restore/last33.bin' % (total - 33),
        '    rkdeveloptool wl %d restore/recovery.img' % layout['recovery'][1],
        '    rkdeveloptool wl %d restore/misc.img' % layout['misc'][1],
        '    rkdeveloptool wl %d restore/zero-1MiB.bin' % layout['userdata'][1],
        '    rkdeveloptool rd',
        '',
        'Never write anything else, and never write over uboot or trust from anything but this backup.',
        ''])


# ------------------------------------------------------------------------------------------ firmware

def pull_firmware(adb, folder):
    """The unit's own Wi-Fi and Bluetooth firmware into folder/firmware, each checked by sha256."""
    out = os.path.join(folder, 'firmware')
    os.makedirs(out, exist_ok=True)
    sums = []
    for f in FIRMWARE:
        local = os.path.join(out, f)
        if not adb.pull('%s/%s' % (FIRMWARE_DIR, f), local):
            fail('could not copy %s/%s from the unit' % (FIRMWARE_DIR, f))
        h = sha256_file(local)
        if h != first_word(adb.sh('sha256sum %s/%s' % (FIRMWARE_DIR, f))):
            fail('the copy of %s is not what the unit reads' % f)
        sums.append('%s  %s' % (h, f))
    with open(os.path.join(out, 'SHA256SUMS'), 'w', newline='\n') as f:
        f.write('\n'.join(sums) + '\n')
    return out


def firmware_ok(folder):
    out = os.path.join(folder, 'firmware')
    try:
        sums = read_file(os.path.join(out, 'SHA256SUMS')).decode('ascii').splitlines()
    except OSError:
        return False
    want = dict(reversed(line.split('  ', 1)) for line in sums if '  ' in line)
    return all(f in want and os.path.exists(os.path.join(out, f)) and sha256_file(os.path.join(out, f)) == want[f]
               for f in FIRMWARE)


def inside(path, root):
    path, root = os.path.normcase(os.path.abspath(path)), os.path.normcase(os.path.abspath(root))
    return path == root or path.startswith(root.rstrip(os.sep) + os.sep)


def check_firmware_place(folder, work):
    """The unit's firmware stays in its backup folder: never in a release download, the work folder
    the release files are kept in, or the repository's tracked tree."""
    from techo5lib import repo_root
    fw = os.path.join(folder, 'firmware')
    if inside(fw, work):
        fail('the backup folder %s is inside the work folder %s, where release files are kept; the unit\'s\n'
             '   firmware must not go there. Pass --backups somewhere else.' % (folder, work))
    root = repo_root()
    if inside(fw, root) and not inside(fw, os.path.join(root, 'backups')):
        fail('the backup folder %s is inside the repository but not under backups/ (which git ignores);\n'
             '   the unit\'s firmware must never be committed. Pass --backups somewhere else.' % folder)


# ------------------------------------------------------------------------------------------ the images

class BootImage:
    """An Android boot image of header version 2, as the Skylight's U-Boot and pack-sky.py make it."""

    def __init__(self, data, what):
        if len(data) < 1660 or data[:8] != b'ANDROID!':
            fail('%s is not an Android boot image' % what)
        f = struct.unpack('<10I', data[8:48])
        ksize, rsize, ssize, page, version = f[0], f[2], f[4], f[7], f[8]
        if version != 2 or page != 2048:
            fail('%s has header version %d, page %d; the Skylight\'s are version 2, page 2048' % (what, version, page))
        self.cmdline = data[64:64 + 512].split(b'\0')[0].decode('ascii', 'replace')
        pad = lambda n: (n + page - 1) // page * page  # noqa: E731
        k = page
        r = k + pad(ksize)
        s = r + pad(rsize)
        dsize = struct.unpack('<I', data[1648:1652])[0]
        d = s + pad(ssize)
        if d + dsize > len(data):
            fail('%s is shorter than its header says' % what)
        h = hashlib.sha1()
        for blob in (data[k:k + ksize], data[r:r + rsize], data[s:s + ssize], b'', data[d:d + dsize]):
            h.update(blob)
            h.update(struct.pack('<I', len(blob)))
        if h.digest().ljust(32, b'\0') != data[576:608]:
            fail('%s: its header id does not match its contents; it is damaged' % what)


def check_boot(data, what, installer):
    img = BootImage(data, what)
    words = img.cmdline.split()
    if installer:
        if 'techo5.installer=1' not in words or 'rdinit=/init' not in words:
            fail('%s is not the TECHO5 Skylight installer image (kernel/build.sh INSTALLER=1)' % what)
    elif 'root=PARTLABEL=techo5-store' not in words or 'techo5.installer=1' in words:
        fail('%s is not TECHO5\'s Skylight boot image (kernel/build.sh: root=PARTLABEL=techo5-store)' % what)
    return img


def prepare_store(path, work):
    """(the store as .gz, its size and sha256 uncompressed): ext4 labeled techo5-store, whole 4 KiB
    blocks. A raw image is gzipped here; install.sh unpacks it on the unit."""
    h = hashlib.sha256()
    size = 0
    head = b''
    opener = gzip.open if path.endswith('.gz') else open
    try:
        with opener(path, 'rb') as f:
            for chunk in iter(lambda: f.read(1 << 20), b''):
                if len(head) < 2048:
                    head += chunk[:2048]
                h.update(chunk)
                size += len(chunk)
    except (OSError, EOFError) as e:
        fail('%s could not be read: %s' % (path, e))
    sb = head[1024:2048]
    if len(sb) < 136 or sb[56:58] != EXT4_MAGIC:
        fail('%s is not an ext4 image' % path)
    label = sb[120:136].rstrip(b'\0')
    if label != STORE_LABEL:
        fail("%s is labeled '%s', not 'techo5-store': not a Skylight store image"
             % (path, label.decode('ascii', 'replace')))
    if size % 4096 or size > mkgpt.STORE_SECTORS * SECTOR:
        fail('%s is %d bytes: not whole blocks, or larger than techo5-store' % (path, size))
    if path.endswith('.gz'):
        return path, size, h.hexdigest()
    out = os.path.join(work, 'skylight-store.img.gz')
    with open(path, 'rb') as src, gzip.GzipFile(out + '.partial', 'wb', mtime=0) as dst:
        shutil.copyfileobj(src, dst, 1 << 20)
    os.replace(out + '.partial', out)
    return out, size, h.hexdigest()


def install_conf(geo, first, last, store_bytes, store_sha):
    keys = [('TOTAL', geo['total']), ('STORE_FIRST', geo['store_first']), ('STORE_LAST', geo['store_last']),
            ('DATA_FIRST', geo['data_first']), ('DATA_LAST', geo['data_last']), ('BACKUP_LBA', geo['backup_lba']),
            ('STOCK_PRIMARY_SHA', hashlib.sha256(first[SECTOR:34 * SECTOR]).hexdigest()),
            ('STOCK_BACKUP_SHA', hashlib.sha256(last).hexdigest()),
            ('STORE_BYTES', store_bytes), ('STORE_SHA', store_sha)]
    return '# made by install-skylight.py from this unit\'s own partition table\n' + \
        ''.join('%s=%s\n' % kv for kv in keys)


def write_bytes(path, data):
    with open(path, 'wb') as f:
        f.write(data.encode('utf-8') if isinstance(data, str) else data)


# ------------------------------------------------------------------------------------------ staging

def staged_status(adb):
    return adb.sh('cat %s/status 2>/dev/null' % STAGE)


def stage_set(adb, w, files):
    """files: [(local path, name under STAGE)]. Each pushed and checked on the unit by sha256."""
    w.sh('rm -rf %s && mkdir -p %s/firmware && chmod 700 %s' % (STAGE, STAGE, STAGE))
    for local, name in files:
        w.push(local, '%s/%s' % (STAGE, name))
        if w.dry_run:
            continue
        if first_word(adb.sh('sha256sum %s/%s' % (STAGE, name))) != sha256_file(local):
            fail('%s changed on its way to the unit; nothing was installed' % name)


def check_staged(adb, files):
    """For --resume: the staged set on the unit is the one these files say."""
    for local, name in files:
        if first_word(adb.sh('sha256sum %s/%s 2>/dev/null' % (STAGE, name))) != sha256_file(local):
            return name
    return None


def write_recovery(adb, w, disk, layout, image, want):
    """The installer image into recovery, read back from the flash. The target is checked by name right
    before the write."""
    n, a, b = layout['recovery']
    size = os.path.getsize(image)
    if size > (b - a + 1) * SECTOR:
        fail('the installer image (%d MiB) is larger than recovery' % (size >> 20))
    target = dev(disk, n)
    if adb.sh('readlink -f /dev/block/by-name/recovery') != target or \
            'PARTNAME=recovery' not in adb.sh('cat /sys/class/block/%sp%d/uevent' % (disk, n)).split('\n'):
        fail('refusing to write: %s is not the recovery partition' % target)
    w.push(image, RECOVERY_TMP)
    if not w.dry_run and first_word(adb.sh('sha256sum %s' % RECOVERY_TMP)) != want:
        fail('the installer image changed on its way to the unit; nothing was written')
    w.sh('dd if=%s of=%s bs=1048576 conv=fsync 2>/dev/null; sync' % (RECOVERY_TMP, target))
    if w.dry_run:
        return
    got = first_word(adb.sh('sync; echo 3 > /proc/sys/vm/drop_caches; head -c %d %s | sha256sum' % (size, target)))
    adb.sh('rm -f %s' % RECOVERY_TMP)
    if got != want:
        fail('recovery did not read back as written. Android is untouched: run this again. Android starts\n'
             '   normally meanwhile (recovery is only used when asked for).')


# ------------------------------------------------------------------------------------------ after

def watch(serial, exe, adb, minutes=15, sleep=time.sleep, clock=time.time, devices=None, consoles=None,
          known=()):
    """After `adb reboot recovery`: the unit leaves adb, installs, and either starts TECHO5 (its USB
    console appears) or comes back to Android (the install stopped before writing). Returns 'techo5',
    'android' or 'unknown'; a console from the installer itself means it stopped partway.

    Every TECHO5 unit has the same USB ids, so only consoles that were not there before the reboot
    (known, as consoles() listed them then) count: another unit on this computer is not this one."""
    devices = devices or adb_devices
    consoles = consoles or list_consoles
    known = set(known)
    start = clock()
    gone, said = False, start
    while clock() - start < minutes * 60:
        states = {s: st for s, st, _ in devices(exe)}
        if serial not in states:
            gone = True
        elif gone and states[serial] == 'device':
            return 'android'
        ports = [p for p in consoles(CONSOLE_TECHO5) if p not in known]
        if any(usb.lower() == 'techo5-installer' for _, usb in ports):
            fail('the installer stopped partway; it says why on the unit\'s screen, and its USB serial console\n'
                 '   is up (%s). Leave the unit plugged in and see "If the install stops" in\n'
                 '   docs/install-skylight.md.' % ', '.join(p for p, _ in ports))
        if gone and ports:
            return 'techo5'
        if clock() - said >= 30:
            note('still waiting (%d s): the unit\'s screen shows what the installer is doing' % (clock() - start))
            said = clock()
        sleep(5)
    return 'unknown'


def came_back(adb):
    """The unit is back in Android after the installer: what it left in /cache."""
    adb.root()
    status = staged_status(adb)
    log = adb.sh('tail -5 %s/install.log 2>/dev/null' % STAGE)
    return status, log


# ------------------------------------------------------------------------------------------ main

def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--serial', help="the unit's adb serial (adb devices); found when only one is connected")
    ap.add_argument('--release', default='latest', help='a release tag, or latest, whose signed manifest names %s, '
                    '%s and %s' % (installer_name('<v>'), boot_name('<v>'), store_name('<v>')))
    ap.add_argument('--installer', help='an installer image you built (kernel/build.sh INSTALLER=1)')
    ap.add_argument('--boot', help="a TECHO5 Skylight boot image you built (kernel/build.sh's boot-skylight.img)")
    ap.add_argument('--store', help='a store image you built (tools/linux/skylight/mkstore.sh -k none, no -F), '
                    '.img or .img.gz')
    ap.add_argument('--backup', help='a verified backup of this unit made before by this installer (its folder)')
    ap.add_argument('--name', help='the name Home Assistant shows, e.g. Kitchen')
    ap.add_argument('--key-file', help='where the Home Assistant key is kept (default: in the backup folder)')
    ap.add_argument('--wifi', help='a Wi-Fi network to join (its passphrase is asked for); otherwise pick one '
                    'on the screen')
    ap.add_argument('--wifi-passphrase-file', help='a file holding the --wifi passphrase, for running from a script')
    ap.add_argument('--dry-run', action='store_true',
                    help='run every check, print every command that would write; write nothing')
    ap.add_argument('--resume', action='store_true', help='use the set staged on the unit before')
    ap.add_argument('--force', action='store_true', help='do not ask before writing')
    ap.add_argument('--no-watch', action='store_true', help='restart into the installer and stop; do not wait for it')
    ap.add_argument('--backups', default=default_dir('TECHO5_BACKUPS', 'backups'))
    ap.add_argument('--work', default=default_dir('TECHO5_WORK', 'build'))
    ap.add_argument('--adb', default='adb')
    a = ap.parse_args(argv)

    # ------------------------------------------------------------------------------------ 1. checks
    step('checks')
    need(a.adb, 'install the Android platform tools (adb)')
    given = [x for x in (a.installer, a.boot, a.store) if x]
    if given and len(given) != 3:
        fail('give all three of --installer, --boot and --store (images you built), or none (the release\'s)')
    for path in given + ([a.backup] if a.backup else []):
        if not os.path.exists(path):
            fail('nothing at %s' % path)
    a.serial = pick_unit(a.serial, a.adb, ('device',), 'Skylight running Android')
    adb = SkyAdb(a.serial, a.adb)
    if adb.state() != 'device':
        fail("adb does not see %s running Android (state '%s')" % (a.serial, adb.state()))
    fw_name = check_firmware(adb)
    check_root(adb)
    os.makedirs(a.work, exist_ok=True)
    note('%s: stock firmware %s, adb root' % (a.serial, fw_name))
    note(check_board(adb, a.work))
    disk, total = find_disk(adb)
    cid = read_cid(adb, disk)
    first, last = read_table(adb, disk, total, a.work)
    layout = check_table(first, last, total)
    check_names(adb, disk, layout)
    ud = layout['userdata']
    note('eMMC %s: %d MiB; the stock partition table; userdata is p%d, %d MiB'
         % (disk, total >> 11, ud[0], (ud[2] - ud[1] + 1) >> 11))
    fw_sizes = check_vendor_firmware(adb)
    note('Wi-Fi and Bluetooth firmware on the vendor partition: %s' % ', '.join(FIRMWARE))
    folder = os.path.abspath(a.backup or os.path.join(a.backups, backup_name(a.serial)))
    check_firmware_place(folder, os.path.abspath(a.work))
    key_file = a.key_file or os.path.join(folder, 'home-assistant.key')
    wifi = None
    if a.wifi:
        if a.wifi_passphrase_file:
            with open(a.wifi_passphrase_file, encoding='utf-8-sig') as f:
                wifi = wifi_conf(a.wifi, f.read().rstrip('\r\n'))
        else:
            wifi = ask_wifi(a.wifi)
        kv = dict(line.split('=', 1) for line in wifi.strip().split('\n'))
        wifi = WIFI_CONF % (kv['ssid'], kv['psk'])
        note("Wi-Fi: '%s' (only its key goes to the unit)" % a.wifi)
    a.name = ask_name(a.name, DEFAULT_NAME)
    status = staged_status(adb)
    if status:
        note('a set is staged on the unit already (status: %s)' % status)
        log = adb.sh('tail -3 %s/install.log 2>/dev/null' % STAGE)
        if log:
            for line in log.split('\n'):
                note('  ' + line)
    if a.resume and not status:
        fail('--resume: nothing is staged on this unit; run without --resume')

    # ------------------------------------------------------------------------------------ 2. images
    step('the images')
    if given:
        version = 'your images'
        installer, boot, store = (os.path.abspath(x) for x in given)
        note('your own images: %s, %s, %s' % (installer, boot, store))
    else:
        rel = Release(REPO, a.release, a.work)
        version = rel.version
        installer = rel.asset(installer_name(version))
        boot = rel.asset(boot_name(version))
        store = rel.asset(store_name(version))
        note('installer, boot and store images of %s, checked against the signed manifest' % version)
    for path, inst_img in ((installer, True), (boot, False)):
        check_boot(read_file(path), os.path.basename(path), inst_img)
        if os.path.getsize(path) > (layout['recovery'][2] - layout['recovery'][1] + 1) * SECTOR:
            fail('%s is larger than the recovery partition' % os.path.basename(path))
    store_gz, store_bytes, store_sha = prepare_store(store, a.work)
    note('store: %d MiB, sha256 %s' % (store_bytes >> 20, store_sha))
    try:
        prim, back, geo = mkgpt.make_gpt(first, total, last)
    except mkgpt.GPTError as e:
        fail('the new partition table could not be made: %s' % e)
    note('new partition table: p%d techo5-store %d..%d (%d MiB), p%d techo5-data %d..%d (%d MiB)'
         % (ud[0], geo['store_first'], geo['store_last'], (geo['store_last'] - geo['store_first'] + 1) >> 11,
            ud[0] + 1, geo['data_first'], geo['data_last'], (geo['data_last'] - geo['data_first'] + 1) >> 11))

    # Room on /cache for the set, checked now, before the long backup.
    need_bytes = (sum(os.path.getsize(p) for p in (installer, boot, store_gz)) + sum(fw_sizes.values()) +
                  (64 << 10) + STAGE_MARGIN)
    free = cache_free(adb)
    if a.resume:
        free += int((adb.sh('du -sk %s 2>/dev/null' % STAGE).split() or ['0'])[0]) * 1024
    if free < need_bytes:
        fail('/cache has %d MiB free, and the install set needs %d MiB. Nothing was written.'
             % (free >> 20, need_bytes >> 20))
    note('/cache: %d MiB free, the set needs %d MiB' % (free >> 20, need_bytes >> 20))

    # ------------------------------------------------------------------------------------ 3. backup
    step('backup')
    if a.backup:
        why = backup_verified(folder, total, first, cid, rehash=not a.dry_run)
        if why:
            fail('--backup: %s. Without a verified backup nothing is written.' % why)
        note('the backup in %s checks out' % folder)
    else:
        why = backup_verified(folder, total, first, cid, rehash=not (a.dry_run or a.resume))
        if not why:
            note('a verified backup of this unit is there already: %s' % folder)
        elif a.dry_run:
            note('dry run: %s; the real run reads the whole eMMC (%d MiB) into %s first, with:'
                 % (why, total >> 11, folder))
            print('   would read: adb exec-out "dd if=/dev/block/%s bs=4194304" > %s'
                  % (disk, os.path.join(folder, 'emmc.img')))
        elif a.resume:
            fail('--resume needs the verified backup the first run made: %s' % why)
        elif backup_cid(folder) not in (None, cid):
            # A finished backup that is not of this chip, or does not say: never written over.
            fail('%s. Nothing was written, and that backup is left as it is.' % why)
        else:
            make_backup(adb, disk, total, layout, first, folder, cid)
            note('the whole eMMC is in %s, checked by sha256 against what the unit reads' % folder)

    # ------------------------------------------------------------------------------------ 4. firmware
    step("the unit's firmware")
    if firmware_ok(folder):
        note('already copied: %s' % os.path.join(folder, 'firmware'))
    elif a.dry_run:
        for f in FIRMWARE:
            print('   would read: adb pull %s/%s %s' % (FIRMWARE_DIR, f, os.path.join(folder, 'firmware', f)))
    else:
        pull_firmware(adb, folder)
        note('copied into %s (%d bytes); it goes back onto this unit only' % (os.path.join(folder, 'firmware'),
                                                                                sum(fw_sizes.values())))

    # The set, as install.sh reads it.
    setdir = os.path.join(a.work, 'skylight-set-%s' % backup_name(a.serial))
    psk = None
    if not a.dry_run:
        shutil.rmtree(setdir, ignore_errors=True)
        os.makedirs(os.path.join(setdir, 'settings'))
        write_bytes(os.path.join(setdir, 'install.conf'), install_conf(geo, first, last, store_bytes, store_sha))
        write_bytes(os.path.join(setdir, 'gpt-primary.bin'), prim)
        write_bytes(os.path.join(setdir, 'gpt-backup.bin'), back)
        if os.path.exists(key_file):
            psk = read_file(key_file).decode('ascii', 'replace').strip()
            if not valid_api_key(psk):
                fail('the key in %s is not 32 bytes of base64' % key_file)
        else:
            psk = new_api_key()
        write_bytes(os.path.join(setdir, 'settings', 'name'), a.name + '\n')
        write_bytes(os.path.join(setdir, 'settings', 'psk'), psk + '\n')
        if wifi:
            write_bytes(os.path.join(setdir, 'settings', 'wpa_supplicant.conf'), wifi)
    fwdir = os.path.join(folder, 'firmware')
    plain = [(os.path.join(setdir, n), n) for n in ('install.conf', 'gpt-primary.bin', 'gpt-backup.bin')]
    plain += [(store_gz, 'store.img.gz'), (boot, 'boot.img')]
    plain += [(os.path.join(fwdir, f), 'firmware/' + f) for f in FIRMWARE]
    settings = [(os.path.join(setdir, 'settings', n), 'settings/' + n)
                for n in ('name', 'psk', 'wpa_supplicant.conf') if wifi or n != 'wpa_supplicant.conf']
    sums = os.path.join(setdir, 'SHA256SUMS')
    if not a.dry_run:
        write_bytes(sums, ''.join('%s  %s\n' % (sha256_file(p), n) for p, n in plain))
    files = plain + [(sums, 'SHA256SUMS')] + settings + [(installer, 'installer.img')]
    installer_sha = sha256_file(installer)

    if a.dry_run:
        w = Writer(adb, True)
        print('\nDry run: every check passed. A real run would then do this (after the confirm):')
        stage_set(adb, w, files)
        write_recovery(adb, w, disk, layout, installer, installer_sha)
        print('   would run: adb push <a file saying staged> %s/status' % STAGE)
        print('   would run: adb reboot recovery')
        print('   and on the unit, by itself: install.sh writes the new partition table, techo5-store at LBA %d,\n'
              '   techo5-data at LBA %d, the firmware and settings, TECHO5\'s kernel into recovery (p%d) and\n'
              '   boot-recovery into misc (p%d).' % (geo['store_first'], geo['data_first'], layout['recovery'][0],
                                                     layout['misc'][0]))
        print('\nNothing was written to the unit.')
        return

    # ------------------------------------------------------------------------------------ 5. confirm
    mib = lambda n: (n + 1) >> 11  # noqa: E731
    confirm(a.force, [
        "About to install TECHO5 (%s) on %s (%s, firmware %s) as '%s'."
        % (version, a.serial, BOARD_NAME, fw_name, a.name),
        'This ERASES Android\'s userdata: the Skylight app, its account, calendars, photos and settings on',
        'the unit. On the eMMC it changes:',
        '  - the partition table: userdata (p%d, %d MiB) becomes techo5-store (%d MiB) and techo5-data (%d MiB)'
        % (ud[0], mib(ud[2] - ud[1]), mib(geo['store_last'] - geo['store_first']),
           mib(geo['data_last'] - geo['data_first'])),
        "  - recovery (p%d): Android's recovery is replaced, by the installer, then by TECHO5's kernel"
        % layout['recovery'][0],
        '  - misc (p%d), its first 2048 bytes: "boot-recovery", so every start is TECHO5' % layout['misc'][0],
        '  - cache (p%d): a techo5-install folder' % layout['cache'][0],
        'uboot, trust, boot, super (Android itself) and the rest are not written.',
        'The way back: the backup in %s, written back in Loader mode with rkdeveloptool' % folder,
        '(restore/RESTORE.txt there, and docs/install-skylight.md, "The way back"). Clearing misc\'s first',
        '2048 bytes (zeros) from TECHO5 is meant to start Android again with a fresh setup; it has not been',
        'tried yet, so the backup is the way back to rely on.',
    ])

    # ------------------------------------------------------------------------------------ 6. stage
    step('stage')
    w = Writer(adb, False)
    if not os.path.exists(key_file):
        os.makedirs(os.path.dirname(key_file) or '.', exist_ok=True)
        write_private(key_file, psk)
        note('Home Assistant key: new, in %s' % key_file)
    else:
        note('Home Assistant key: the one in %s' % key_file)
    if a.resume:
        # The installer removes the staged settings (the key) once it has run; they go again.
        bad = check_staged(adb, plain + [(sums, 'SHA256SUMS')])
        if bad:
            fail('--resume: the staged %s is not what this run would stage; run without --resume' % bad)
        w.sh('rm -rf %s/settings' % STAGE)
        for local, name in settings + [(installer, 'installer.img')]:
            if first_word(adb.sh('sha256sum %s/%s 2>/dev/null' % (STAGE, name))) != sha256_file(local):
                w.push(local, '%s/%s' % (STAGE, name))
                if first_word(adb.sh('sha256sum %s/%s' % (STAGE, name))) != sha256_file(local):
                    fail('%s changed on its way to the unit; nothing was installed' % name)
        note('the staged set on the unit checks out')
    else:
        stage_set(adb, w, files)
        note('the set is on %s, each file checked' % STAGE)
    write_recovery(adb, w, disk, layout, installer, installer_sha)
    note('recovery holds the installer image, read back from the flash')
    status_file = os.path.join(setdir, 'status')
    write_bytes(status_file, 'staged\n')
    w.push(status_file, '%s/status' % STAGE)
    if staged_status(adb) != 'staged':
        fail('the staged set could not be marked ready')
    adb.sh('sync')

    # ------------------------------------------------------------------------------------ 7. install
    step('install')
    # The TECHO5 consoles already here (another unit), before this one could add its own.
    known = [] if a.no_watch else list_consoles(CONSOLE_TECHO5)
    adb.reboot('recovery')
    note('the unit restarts into the installer; its screen says what it is doing. Do not unplug it.')
    if a.no_watch:
        print('\nThe installer runs on the unit now. When it is done the unit starts TECHO5 by itself.')
    else:
        how = watch(a.serial, a.adb, adb, known=known)
        if how == 'android':
            status, log = came_back(adb)
            fail('the unit came back to Android: the installer stopped before writing anything (status: %s).\n'
                 '   %s\n   Android is as it was. Fix what it says, then run this again with --resume.'
                 % (status or 'none', log.replace('\n', '\n   ')))
        if how == 'techo5':
            note("TECHO5's USB serial console is up: the install is done")
        else:
            note('no word from the unit within 15 minutes; its screen says what happened')
    print("\nTECHO5 starts as '%s'. On the first boot it grows its partitions; that takes a minute." % a.name)
    if not wifi:
        print('Pick the Wi-Fi network on the unit\'s screen.')
    print('Home Assistant finds it as an ESPHome device (Settings, Devices & services, Discovered). When it\n'
          'asks for the encryption key, paste:\n\n    %s\n\n(kept in %s)' % (psk, key_file))
    print('\nThe way back to Android is in %s\n(restore/RESTORE.txt) and in docs/install-skylight.md.' % folder)


if __name__ == '__main__':
    run_main(main)
