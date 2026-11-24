#!/usr/bin/env python3
"""Install TECHO5 on a Lenovo ThinkSmart View (CD-18781Y, Qualcomm APQ8053) from its stock Android, in
one command, over the SoC's emergency download mode (EDL) with bkerler's edl.

    python3 tools/install-thinksmart.py --name Office
    python3 tools/install-thinksmart.py --name Office --wifi MyNetwork
    python3 tools/install-thinksmart.py --for-someone-else
    python3 tools/install-thinksmart.py --dry-run
    python3 tools/install-thinksmart.py --system system.img --lk2nd lk2nd.img --dry-run

Every one of Lenovo's partitions stays where it is. Android's userdata becomes two of TECHO5's: system
(TECHO5's image: lk2nd's boot files and the slot store, 2.24 GiB) and techo5-data (the rest, TECHO5's
state); Android's own system partition is renamed android-system, its contents untouched, so the
name system is TECHO5's. lk2nd goes into boot. See tools/linux/thinksmart/README.md.

The image carries only firmware anyone may pass on (the GPU's microcode and the Wi-Fi regulatory
database, their licenses beside them: tools/linux/thinksmart/firmware-licenses). The rest is the unit's
own and never leaves it except into the backup folder: the Wi-Fi, Bluetooth and audio firmware and the
GPU's zap shader are made at every boot from the modem and vendor partitions, which stay
(thinksmart-firmware), and the speaker amplifier's DSP table is taken here from the stock kernel in the
backup and written onto the unit with the settings.

It works only on what it knows: the stock partition table of a CD-18781Y (Lenovo's partitions, userdata
last, one free entry). Anything else stops it before anything is written. A unit this installer has
installed before is recognized, and installed again (TECHO5's data is erased) without touching the
partition table. Each step is checked before the next:

  1. checks    edl reaches a unit in EDL; its partition table, both copies, read and checked by CRC;
               the stock layout, or this installer's
  2. images    TECHO5's system image and lk2nd (the release's, checked against its signed manifest, or
               yours with --system and --lk2nd: tools/linux/thinksmart/lk2nd/build.sh builds it). The
               stock bootloader starts only a signed lk2nd without an appended QCDT, which lk2nd's own
               release images are not, so the image is checked for both; the new partition table, made
               from the unit's own
  3. backup    the whole eMMC (about 7.3 GiB), into backups/thinksmart-<serial>/ (the serial on the
               unit's label, read from its devinfo partition: every unit has the same disk id and
               partition table), its size and partition table checked against the unit. A backup is
               taken as this unit's only if its devinfo holds this serial. Without a verified backup
               nothing is written (--backup points at one made before)
  4. speaker   the amplifier's DSP table (Lenovo's tuning of this speaker), out of the stock kernel in
               the backup's boot partition, found by the registers it writes, into the backup folder; it
               goes back onto this unit only. Without one the speaker plays untuned, flat and quieter
  5. confirm   says exactly what is erased and how to go back, and asks once
  6. write     lk2nd into boot, the new partition table (both copies), TECHO5's system image into the
               new system, the settings at the start of techo5-data; each read back from the flash
  7. start     edl reset; the unit starts TECHO5, whose USB serial console says it is up

--for-someone-else is for a unit you are giving away: no Wi-Fi network, name or key is asked for or
written, and the unit starts its own setup when it is next powered on. --let-home-assistant-add writes a
name and Wi-Fi but no key: on its first start with a network the unit lets a Home Assistant add it.

--dry-run runs every check and prints every command that would write, and writes nothing.

Linux and macOS (edl needs raw USB access: its udev rules, or sudo; stop ModemManager on Linux, which
takes the device otherwise). Needs Python 3 and bkerler's edl, at the commit the docs name
(pip install git+https://github.com/bkerler/edl@1cda1a616748d608667b2d13b73a28d3197ceb45).
The way back is printed at the end, written into the
backup folder (restore/RESTORE.txt) and in docs/install-thinksmart.md.
"""
import argparse
import gzip
import hashlib
import io
import os
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import time
import uuid
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import (CONSOLE_TECHO5, LET_ADD_HELP, LET_ADD_MARKER, Release, ask_name, ask_wifi,  # noqa: E402
                       check_gift, check_let_add, check_let_add_release, confirm, default_dir,
                       fail, let_add_done, list_consoles, need, new_api_key, note, run_main, step, tool,
                       valid_api_key, wifi_conf, write_private)

REPO = 'HuskerMinion/techo5'
# bkerler's edl at the commit docs/install-thinksmart.md was done with.
EDL_COMMIT = '1cda1a616748d608667b2d13b73a28d3197ceb45'
BOARD_NAME = 'Lenovo ThinkSmart View (CD-18781Y)'
DEFAULT_NAME = 'ThinkSmart View'


SECTOR = 512
# TECHO5's image (tools/linux/thinksmart/mkimage.sh): an MBR, system1 (lk2nd's /boot, ext2) and system2
# (the store, ext4 labeled techo5-store), as the kernel's dm-mod.create maps them.
SYSTEM_SECTORS = 4694016
SYSTEM1 = (2048, 497664)
SYSTEM2 = (499712, 4194304)
STORE_LABEL = b'techo5-store'
# techo5-data has to hold the unit's state, logs and caches.
DATA_MIN_SECTORS = 1 << 21  # 1 GiB

# The GPT names: Lenovo's that the install relies on, and what it makes.
STOCK_NEEDED = ('modem', 'boot', 'recovery', 'system', 'vendor', 'persist', 'userdata')
ANDROID_SYSTEM = 'android-system'
LINUX_DATA = uuid.UUID('0fc63daf-8483-4772-8e79-3d69d8477de4')
# What the installer writes at the start of techo5-data: a tar archive, read by the store's /sbin/init
# before it formats the partition at the first boot.
SETTINGS_BYTES = 1 << 20

# The speaker amplifier's DSP table (tas5728m_dsp_lenovo_cd-18781y.bin, which the kernel's tas5782m
# driver loads): in the stock kernel, an array of {u32 register, u32 value} pairs, 15 of the driver's
# own setup and then the table. It is found by the registers it writes, in order, whose sha256 is this;
# the values are the unit's own tuning.
DSP_NAME = 'tas5728m_dsp_lenovo_cd-18781y.bin'
DSP_ENTRIES = 1522
DSP_REGS_SHA256 = '197a0026e1ef1250d943eb1c3dd2ee9cb7f2a9d3bd30d699bcb7619bd3a29812'

WIFI_CONF = 'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid=%s\n\tscan_ssid=1\n\tpsk=%s\n}\n'


def system_name(version):
    return 'techo5-system-thinksmart-%s.img.gz' % version


def lk2nd_name(version):
    return 'techo5-lk2nd-thinksmart-%s.img' % version


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def read_file(path):
    with open(path, 'rb') as f:
        return f.read()


def write_bytes(path, data):
    with open(path, 'wb') as f:
        f.write(data.encode('utf-8') if isinstance(data, str) else data)


# ------------------------------------------------------------------------------------------ edl

class Edl:
    """bkerler's edl, one command per call. Each call finds the unit again; the first after the unit
    entered EDL loads the firehose programmer (edl knows Lenovo's), the rest find it running."""

    def __init__(self, exe, extra=()):
        self.cmd = tool(exe) + list(extra)

    def run(self, *args, what=''):
        r = subprocess.run(self.cmd + [str(a) for a in args], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        out = r.stdout.decode('utf-8', 'replace')
        if r.returncode != 0 or re.search(r'(?i)\b(error|failed)\b', out.split('Progress')[-1][-400:]):
            tail = '\n   '.join(l for l in out.replace('\r', '\n').splitlines()[-6:] if l.strip())
            fail('edl %s failed%s:\n   %s' % (args[0], (' (%s)' % what) if what else '', tail))
        return out

    def read(self, start, count, path):
        self.run('rs', start, count, path, what='reading sectors %d to %d' % (start, start + count - 1))
        if not os.path.exists(path) or os.path.getsize(path) != count * SECTOR:
            got = os.path.getsize(path) if os.path.exists(path) else 0
            fail('edl read %d bytes of sectors %d+%d, not %d' % (got, start, count, count * SECTOR))
        return read_file(path)

    def write(self, start, path):
        self.run('ws', start, path, what='writing %s at sector %d' % (os.path.basename(path), start))

    def dump(self, path):
        self.run('rf', path, what='reading the whole eMMC')

    def reset(self):
        # The unit drops off USB as it restarts, and edl often reports that as a USBError: the reset
        # happened.
        r = subprocess.run(self.cmd + ['reset'], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        out = r.stdout.decode('utf-8', 'replace')
        if 'USBError' in out:
            return out
        if r.returncode != 0 or re.search(r'(?i)\b(error|failed)\b', out[-400:]):
            tail = '\n   '.join(l for l in out.replace('\r', '\n').splitlines()[-6:] if l.strip())
            fail('edl reset failed:\n   %s' % tail)
        return out


class Writer:
    """Everything that writes to the unit goes through here. In a dry run it prints the command and does
    nothing."""

    def __init__(self, edl, dry_run):
        self.edl, self.dry_run = edl, dry_run

    def write(self, start, path):
        if self.dry_run:
            print('   would run: edl ws %d %s' % (start, path))
            return
        self.edl.write(start, path)

    def reset(self):
        if self.dry_run:
            print('   would run: edl reset')
            return
        self.edl.reset()


def write_checked(w, edl, start, path, work, what):
    """A write, then the same sectors read back and compared."""
    w.write(start, path)
    if w.dry_run:
        return
    size = os.path.getsize(path)
    back = edl.read(start, size // SECTOR, os.path.join(work, 'readback.bin'))
    os.remove(os.path.join(work, 'readback.bin'))
    if back != read_file(path):
        fail('%s did not read back as written (sector %d)' % (what, start))


# ------------------------------------------------------------------------------------------ GPT

class GPTError(Exception):
    pass


class Entry:
    def __init__(self, raw, index):
        self.index = index
        self.type = uuid.UUID(bytes_le=raw[0:16])
        self.guid = uuid.UUID(bytes_le=raw[16:32])
        self.first, self.last, self.attrs = struct.unpack_from('<QQQ', raw, 32)
        self.name = raw[56:128].decode('utf-16-le', 'replace').split('\0')[0]

    @property
    def free(self):
        return self.type.int == 0

    @property
    def sectors(self):
        return self.last - self.first + 1

    def pack(self):
        name = self.name.encode('utf-16-le')
        if len(name) > 72:
            raise GPTError('partition name %r is too long' % self.name)
        return (self.type.bytes_le + self.guid.bytes_le + struct.pack('<QQQ', self.first, self.last, self.attrs) +
                name.ljust(72, b'\0'))


HEADER = struct.Struct('<8sIIIIQQQQ16sQIII')


class GPT:
    """A GUID partition table, from its primary copy (the first 34 sectors of the disk) and checked
    against its backup copy (the last 33)."""

    def __init__(self, first, last, check_backup=True):
        if len(first) != 34 * SECTOR or len(last) != 33 * SECTOR:
            raise GPTError('the partition table reads are %d and %d bytes' % (len(first), len(last)))
        self.mbr = first[:SECTOR]
        h = first[SECTOR:2 * SECTOR]
        (sig, self.rev, hsize, crc, _, cur, self.alt, self.first_lba, self.last_lba, guid, elba, self.count,
         self.esize, ecrc) = HEADER.unpack_from(h)
        if sig != b'EFI PART' or hsize != 92 or cur != 1:
            raise GPTError('no GPT header at sector 1')
        if zlib.crc32(h[:16] + b'\0\0\0\0' + h[20:92]) & 0xffffffff != crc:
            raise GPTError("the primary GPT header's CRC is wrong")
        if elba != 2 or self.esize != 128 or self.count * self.esize > 32 * SECTOR or self.first_lba != 34:
            raise GPTError('the GPT has an unusual shape (entries at %d, %d of %d bytes, first usable %d)'
                           % (elba, self.count, self.esize, self.first_lba))
        raw = first[2 * SECTOR:2 * SECTOR + self.count * self.esize]
        if zlib.crc32(raw) & 0xffffffff != ecrc:
            raise GPTError("the primary GPT's partition entries do not match their CRC")
        self.disk_guid = uuid.UUID(bytes_le=guid)
        self.total = self.alt + 1
        self.entries = [Entry(raw[i * 128:(i + 1) * 128], i) for i in range(self.count)]
        if not check_backup:
            return
        # The backup copy: the same entries, its header in the last sector naming them 32 sectors before.
        bh = last[32 * SECTOR:]
        b = HEADER.unpack_from(bh)
        if b[0] != b'EFI PART' or b[5] != self.alt or b[6] != 1 or b[10] != self.alt - 32:
            raise GPTError('the backup GPT header is not where the primary says, or not the same table')
        if zlib.crc32(bh[:16] + b'\0\0\0\0' + bh[20:92]) & 0xffffffff != b[3]:
            raise GPTError("the backup GPT header's CRC is wrong")
        if last[:self.count * self.esize] != raw:
            raise GPTError('the backup GPT does not hold the same partitions as the primary')

    def named(self, name):
        found = [e for e in self.entries if not e.free and e.name == name]
        return found[0] if len(found) == 1 else None

    def build(self):
        """(the first 34 sectors, the last 33) of this table as it is now."""
        raw = b''.join(e.pack() for e in self.entries).ljust(32 * SECTOR, b'\0')
        ecrc = zlib.crc32(raw[:self.count * self.esize]) & 0xffffffff

        def header(cur, other, elba):
            h = bytearray(HEADER.pack(b'EFI PART', self.rev, 92, 0, 0, cur, other, self.first_lba, self.last_lba,
                                      self.disk_guid.bytes_le, elba, self.count, self.esize, ecrc))
            struct.pack_into('<I', h, 16, zlib.crc32(bytes(h)) & 0xffffffff)
            return bytes(h).ljust(SECTOR, b'\0')
        first = self.mbr + header(1, self.alt, 2) + raw
        last = raw + header(self.alt, 1, self.alt - 32)
        return first, last


def layout_of(gpt):
    """'stock', 'techo5' (this installer's), or why it is neither."""
    names = [e.name for e in gpt.entries if not e.free]
    if len(set(names)) != len(names):
        return 'two partitions share a name'
    sysp, data = gpt.named('system'), gpt.named('techo5-data')
    if gpt.named(ANDROID_SYSTEM) and sysp and data:
        if sysp.sectors != SYSTEM_SECTORS or sysp.type != LINUX_DATA or data.first != sysp.last + 1:
            return 'it has TECHO5 partitions, but not as this installer makes them'
        return 'techo5'
    if data:
        return ('it has a techo5-data partition, from installing by hand (docs/install-thinksmart.md); '
                'this installer does not change such a table: use edl w system as that page says')
    missing = [n for n in STOCK_NEEDED if not gpt.named(n)]
    if missing:
        return 'it has no %s partition' % ', '.join(missing)
    ud = gpt.named('userdata')
    if max(e.last for e in gpt.entries if not e.free) != ud.last or ud.last != gpt.last_lba:
        return 'userdata is not the last partition, to the end of the eMMC'
    if ud.sectors < SYSTEM_SECTORS + DATA_MIN_SECTORS:
        return 'userdata is %d MiB, too small for TECHO5 (%d MiB)' % (ud.sectors >> 11,
                                                                     (SYSTEM_SECTORS + DATA_MIN_SECTORS) >> 11)
    if ud.first % 8:
        return 'userdata does not start on a 4 KiB boundary'
    if not [e for e in gpt.entries if e.free]:
        return 'the table has no free entry for techo5-data'
    return 'stock'


def new_layout(gpt, guids=None):
    """The table TECHO5 needs, from the unit's stock one: system renamed android-system, userdata made
    TECHO5's system, and the first free entry techo5-data, from there to the end. guids: the two new
    partitions' ids (random by default)."""
    guids = guids or (uuid.uuid4(), uuid.uuid4())
    g = GPT.__new__(GPT)
    g.__dict__.update(gpt.__dict__)
    g.entries = [Entry(e.pack(), e.index) for e in gpt.entries]
    old_system, ud = g.named('system'), g.named('userdata')
    free = [e for e in g.entries if e.free][0]
    old_system.name = ANDROID_SYSTEM
    data_first, data_last = ud.first + SYSTEM_SECTORS, ud.last
    ud.name, ud.type, ud.guid, ud.attrs = 'system', LINUX_DATA, guids[0], 0
    ud.last = ud.first + SYSTEM_SECTORS - 1
    free.name, free.type, free.guid, free.attrs = 'techo5-data', LINUX_DATA, guids[1], 0
    free.first, free.last = data_first, data_last
    return g


def read_table(first, last):
    """(the unit's table, the new table a run before left half written, or None). The table is read
    from both copies. The one other state let through is a run stopped between its two writes: the
    backup copy written (it goes first) and the primary still the stock table. Then the backup copy is
    exactly new_layout of the primary, with its own two new partition ids, and this run writes it
    again, both copies. Anything else that does not match is a GPTError, as before."""
    try:
        return GPT(first, last), None
    except GPTError as e:
        why = e
    try:
        primary = GPT(first, last, check_backup=False)
    except GPTError:
        raise why
    if layout_of(primary) != 'stock':
        raise why
    theirs = [Entry(last[i * 128:(i + 1) * 128], i) for i in range(primary.count)]
    ids = {e.name: e.guid for e in theirs if not e.free}
    if 'system' not in ids or 'techo5-data' not in ids:
        raise why
    pending = new_layout(primary, (ids['system'], ids['techo5-data']))
    if pending.build()[1] != last:
        raise why
    return primary, pending


# ------------------------------------------------------------------------------------------ images

def check_system_image(path):
    """TECHO5's system image: its size, its MBR's two partitions and their file systems."""
    if os.path.getsize(path) != SYSTEM_SECTORS * SECTOR:
        fail('%s is %d bytes, not %d: not a ThinkSmart View system image (mkimage.sh)'
             % (path, os.path.getsize(path), SYSTEM_SECTORS * SECTOR))
    with open(path, 'rb') as f:
        mbr = f.read(SECTOR)
        parts = [struct.unpack_from('<4xB3xII', mbr, 446 + 16 * i) for i in range(2)]
        f.seek(SYSTEM1[0] * SECTOR + 1024)
        sb1 = f.read(1024)
        f.seek(SYSTEM2[0] * SECTOR + 1024)
        sb2 = f.read(1024)
    if mbr[510:512] != b'\x55\xaa' or [(p[1], p[2]) for p in parts] != [SYSTEM1, SYSTEM2]:
        fail('%s does not have the two partitions of a ThinkSmart View system image' % path)
    if sb1[56:58] != b'\x53\xef' or sb2[56:58] != b'\x53\xef':
        fail('%s: its partitions do not hold ext2/ext4 file systems' % path)
    if sb2[120:136].rstrip(b'\0') != STORE_LABEL:
        fail("%s: its second partition is labeled '%s', not techo5-store"
             % (path, sb2[120:136].rstrip(b'\0').decode('ascii', 'replace')))


def unpack_system(path, work):
    """A raw system image: the one given, or a .gz unpacked into the work folder."""
    if not path.endswith('.gz'):
        return path
    out = os.path.join(work, os.path.basename(path)[:-3])
    if os.path.exists(out) and os.path.getsize(out) == SYSTEM_SECTORS * SECTOR and \
            os.path.getmtime(out) >= os.path.getmtime(path):
        return out
    note('unpacking %s' % os.path.basename(path))
    with gzip.open(path, 'rb') as src, open(out + '.partial', 'wb') as dst:
        shutil.copyfileobj(src, dst, 4 << 20)
    os.replace(out + '.partial', out)
    return out


def boot_image_end(data):
    """Where an Android boot image (header version 0, lk2nd's) ends: the header page, then the kernel,
    ramdisk, second stage and QCDT, each padded to the page size."""
    kernel, _, ramdisk, _, second, _, _, page, dt = struct.unpack_from('<9I', data, 8)
    if page not in (2048, 4096):
        return None
    pages = lambda n: -(-n // page) * page
    return page + pages(kernel) + pages(ramdisk) + pages(second) + pages(dt), dt


def check_lk2nd(path):
    """The stock bootloader of this board (locked, and it crashes when unlocked) starts only an AVB1
    signed boot image (any key) without an appended QCDT. lk2nd's release images are neither, and the
    stock bootloader then crashes to a black screen. tools/linux/thinksmart/lk2nd/build.sh builds one."""
    data = read_file(path)
    if data[:8] != b'ANDROID!' or b'lk2nd' not in data:
        fail('%s is not an lk2nd boot image' % path)
    if b'lenovo,cd-18781y' not in data:
        fail('%s does not know this board (lenovo,cd-18781y): an lk2nd for msm8953 from 20.0 on does' % path)
    end = boot_image_end(data)
    if end is None or end[0] > len(data):
        fail('%s is not a whole Android boot image' % path)
    end, dt = end
    if dt:
        fail('%s has a QCDT appended, which this board\'s bootloader does not start (it crashes):\n'
             '   build lk2nd with LK2ND_QCDTBS="" (tools/linux/thinksmart/lk2nd/build.sh)' % path)
    # bootsignature.py appends a DER SEQUENCE (the AVB1 signature) right after the image.
    if data[end:end + 2] != b'\x30\x82':
        fail('%s is not signed, and this board\'s bootloader starts only a signed boot image (it crashes\n'
             '   otherwise): build lk2nd with SIGN_BOOTIMG=1 (tools/linux/thinksmart/lk2nd/build.sh)' % path)
    return data


# ------------------------------------------------------------------------------------------ backup

# What tells one unit from another. Not the GPT's disk id, nor any partition's UUID, nor persist's
# filesystem UUID: Lenovo's factory image gives every unit the same ones. devinfo holds the unit's
# serial (the one on its label) at byte 228, after aboot's "ANDROID-BOOT!" magic, and nothing TECHO5
# runs writes devinfo.
DEVINFO_MAGIC = b'ANDROID-BOOT!'
SERIAL_AT = 228
SERIAL_RE = re.compile(rb'[A-Z0-9]{6,16}')


# The model aboot writes after the serial, as on the unit's label: ZA69 is the ThinkSmart View
# (CD-18781Y); another Qualcomm device in EDL with a table shaped like this one is not taken for one.
MODEL_PREFIX = 'ZA69'
MODEL_RE = re.compile(rb'[A-Z0-9]{6,16}')


def model_of(devinfo):
    """The unit's model from the first sector of its devinfo partition (after the serial), or None."""
    if devinfo[:len(DEVINFO_MAGIC)] != DEVINFO_MAGIC:
        return None
    fields = devinfo[SERIAL_AT:SERIAL_AT + 64].split(b'\0')
    m = fields[1] if len(fields) > 1 else b''
    return m.decode('ascii') if MODEL_RE.fullmatch(m) else None


def masked(serial):
    """A serial as it is printed: enough to tell units apart at a glance, not the whole label."""
    return serial[:3] + '*' * max(len(serial) - 5, 3) + serial[-2:]


def serial_of(devinfo):
    """The unit's serial from the first sector of its devinfo partition, or None."""
    if devinfo[:len(DEVINFO_MAGIC)] != DEVINFO_MAGIC:
        return None
    s = devinfo[SERIAL_AT:SERIAL_AT + 17].split(b'\0', 1)[0]
    return s.decode('ascii') if SERIAL_RE.fullmatch(s) else None


def unit_serial(edl, gpt, work):
    dv = gpt.named('devinfo')
    if dv is None:
        fail('this unit has no devinfo partition, so it cannot be told apart from another. Nothing was written.')
    sector = edl.read(dv.first, 1, os.path.join(work, 'thinksmart-devinfo.bin'))
    os.remove(os.path.join(work, 'thinksmart-devinfo.bin'))
    serial = serial_of(sector)
    if not serial:
        fail("this unit's devinfo holds no serial, so it cannot be told apart from another unit and its "
             "backup\n   cannot be named. Nothing was written.")
    model = model_of(sector)
    if not model or not model.startswith(MODEL_PREFIX):
        fail("this unit's devinfo says model %s, not a %s (%s...). Nothing was written."
             % (model or 'none', BOARD_NAME, MODEL_PREFIX))
    return serial, model


def backup_name(serial):
    return 'thinksmart-%s' % serial


def backup_verified(folder, gpt, serial):
    """'' when folder holds a verified backup of this unit's eMMC (its size, the table the unit had
    when it was made: the stock one, or this table, and this unit's serial in the backup's own devinfo),
    else why not."""
    try:
        marker = read_file(os.path.join(folder, 'verified')).decode('ascii', 'replace')
    except OSError:
        return 'no verified backup in %s' % folder
    m = re.search(r'sectors=(\d+) sha256=([0-9a-f]{64}) disk=([0-9a-f-]{36})', marker)
    if not m or int(m.group(1)) != gpt.total or m.group(3) != str(gpt.disk_guid):
        return 'the backup in %s is of another eMMC' % folder
    img = os.path.join(folder, 'emmc.img')
    try:
        if os.path.getsize(img) != gpt.total * SECTOR:
            return '%s is not the size of the eMMC' % img
        with open(img, 'rb') as f:
            f.seek(gpt.named('devinfo').first * SECTOR)
            theirs = serial_of(f.read(SECTOR))
    except OSError:
        return 'no emmc.img in %s' % folder
    if theirs != serial:
        return 'the backup in %s is of another unit (serial %s, and this one is %s)' % (folder, theirs, serial)
    return ''


def make_backup(edl, gpt, first, last, folder):
    os.makedirs(folder, exist_ok=True)
    size = gpt.total * SECTOR
    free = shutil.disk_usage(folder).free
    if free < size + (1 << 30):
        # edl's rf stops short, without an error, when the disk fills.
        fail('the backup needs %d MiB free in %s, and there are %d MiB' % ((size >> 20) + 1024, folder, free >> 20))
    img = os.path.join(folder, 'emmc.img')
    note('reading the whole eMMC (%d MiB) over USB; edl shows no progress here, and it takes several minutes'
         % (size >> 20))
    edl.dump(img + '.partial')
    got = os.path.getsize(img + '.partial') if os.path.exists(img + '.partial') else 0
    if got != size:
        fail('the backup is %d bytes, not %d: it is not complete. Check the USB cable and the free space, '
             'and run this again.' % (got, size))
    with open(img + '.partial', 'rb') as f:
        head = f.read(34 * SECTOR)
        f.seek(size - 33 * SECTOR)
        tail = f.read(33 * SECTOR)
    if head != first or tail != last:
        fail("the backup's partition table is not the one the unit showed a moment ago; run this again")
    os.replace(img + '.partial', img)
    note('hashing the backup')
    whole = sha256_file(img)
    write_bytes(os.path.join(folder, 'SHA256SUMS'), '%s  emmc.img\n' % whole)
    if write_restore(folder, gpt, img):
        note("warning: boot already holds lk2nd, not Lenovo's kernel; restore/RESTORE.txt says what that means\n"
             "   for the way back")
    # Last, so a backup that stopped anywhere above is never taken as verified.
    write_bytes(os.path.join(folder, 'verified'), 'verified %s sectors=%d sha256=%s disk=%s\n'
                % (time.strftime('%Y-%m-%dT%H:%M:%S'), gpt.total, whole, gpt.disk_guid))
    return img


def write_restore(folder, gpt, img):
    """restore/: the pieces of the eMMC the install changes, and RESTORE.txt, the way back over EDL with
    this unit's own numbers."""
    r = os.path.join(folder, 'restore')
    os.makedirs(r, exist_ok=True)
    with open(img, 'rb') as src:
        def piece(name, first, count):
            src.seek(first * SECTOR)
            write_bytes(os.path.join(r, name), src.read(count * SECTOR))
        piece('gpt-primary.bin', 0, 34)
        piece('gpt-backup.bin', gpt.total - 33, 33)
        for name in ('boot', 'recovery'):
            e = gpt.named(name)
            piece(name + '.img', e.first, e.sectors)
    write_bytes(os.path.join(r, 'zero-1MiB.bin'), bytes(1 << 20))
    lk2nd_there = holds_lk2nd(read_file(os.path.join(r, 'boot.img')))
    write_bytes(os.path.join(r, 'RESTORE.txt'), restore_text(gpt, lk2nd_there))
    return lk2nd_there


def holds_lk2nd(boot):
    """Whether a boot partition holds lk2nd rather than an Android kernel: someone put it there by hand
    before this backup was made."""
    return boot[:8] == b'ANDROID!' and b'lk2nd' in boot[:4 << 20]


def restore_text(gpt, lk2nd_there=False):
    b, rc, ud = gpt.named('boot'), gpt.named('recovery'), gpt.named('userdata')
    lk2nd_note = [
        'NOTE: when this backup was made, boot already held lk2nd, not Lenovo\'s Android kernel (it was put',
        'there before this installer ran). Both A. and B. put that lk2nd back; Android\'s own boot partition',
        'has to come from an older backup of this unit or from Lenovo\'s firmware.',
        ''] if lk2nd_there else []
    return '\n'.join(lk2nd_note + [
        "The way back to Lenovo's Android for this unit (docs/install-thinksmart.md, \"The way back\").",
        '',
        'EDL: unplug the power, hold Vol+ and Vol- together, plug the power back in, release after 5 to 10',
        'seconds. In this folder\'s parent (the backup):',
        '',
        'A. The unit as it came, the whole eMMC (writes %d MiB):' % (gpt.total >> 11),
        '',
        '    edl wf emmc.img',
        '    edl reset',
        '',
        'B. Only what the install changed: the partition table (both copies), boot and recovery, and the',
        '   start of userdata cleared. Android then starts as a new unit, with a fresh setup:',
        '',
        '    edl ws 0 restore/gpt-primary.bin',
        '    edl ws %d restore/gpt-backup.bin' % (gpt.total - 33),
        '    edl ws %d restore/boot.img' % b.first,
        '    edl ws %d restore/recovery.img' % rc.first,
        '    edl ws %d restore/zero-1MiB.bin' % ud.first,
        '    edl reset',
        '',
        'Never write anything else from here.',
        ''])


# ------------------------------------------------------------------------------------------ speaker

def boot_kernel(img):
    """The kernel inside an Android boot image (header v0 to v2), decompressed."""
    if img[:8] != b'ANDROID!':
        return None
    ksize, page = struct.unpack_from('<I', img, 8)[0], struct.unpack_from('<I', img, 36)[0]
    k = img[page:page + ksize]
    i = k.find(b'\x1f\x8b\x08')
    if i < 0:
        return k
    try:
        return zlib.decompressobj(31).decompress(k[i:])
    except zlib.error:
        return None


def find_dsp_table(kernel, regs_sha=None):
    """The speaker's DSP table in a stock kernel, as the driver's firmware file wants it ({register,
    value} bytes), or None. It is a run of aligned {u32, u32} pairs below 256 holding DSP_ENTRIES pairs
    whose registers have the sha256 DSP_REGS_SHA256."""
    regs_sha = regs_sha or DSP_REGS_SHA256
    words = len(kernel) // 8
    run = 0
    for o in range(0, words * 8, 8):
        r, v = struct.unpack_from('<II', kernel, o)
        if r < 256 and v < 256:
            run += 1
            continue
        if run >= DSP_ENTRIES:
            start = o - run * 8
            pairs = [struct.unpack_from('<II', kernel, start + 8 * i) for i in range(run)]
            for skip in range(run - DSP_ENTRIES + 1):
                regs = bytes(p[0] for p in pairs[skip:skip + DSP_ENTRIES])
                if hashlib.sha256(regs).hexdigest() == regs_sha:
                    return bytes(x for p in pairs[skip:skip + DSP_ENTRIES] for x in p)
        run = 0
    return None


def extract_dsp(img_path, gpt, out):
    """The DSP table from the stock kernel in the backup (boot, else recovery) into out."""
    with open(img_path, 'rb') as f:
        for name in ('boot', 'recovery'):
            e = gpt.named(name)
            f.seek(e.first * SECTOR)
            kernel = boot_kernel(f.read(e.sectors * SECTOR))
            table = find_dsp_table(kernel) if kernel else None
            if table:
                write_bytes(out, table)
                return name
    return None


# ------------------------------------------------------------------------------------------ settings

def settings_files(gift, name, psk, wifi, let_add=False):
    """What the store's /sbin/init puts into the unit's state directory at the first boot, as name ->
    text: as tools/install-skylight.py stages them."""
    if gift:
        return {'firstrun': ''}
    files = {'name': name + '\n'}
    if let_add:
        files[LET_ADD_MARKER] = ''
    else:
        files['psk'] = psk + '\n'
    if wifi:
        files['wpa_supplicant.conf'] = wifi
    return files


def settings_image(files, dsp):
    """The tar archive init reads from the start of techo5-data, zero-padded to SETTINGS_BYTES."""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode='w', format=tarfile.USTAR_FORMAT) as t:
        def add(name, data, mode):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), mode, 0
            t.addfile(info, io.BytesIO(data))
        for n, text in sorted(files.items()):
            add('settings/' + n, text.encode('utf-8'), 0o600)
        if dsp is not None:
            add('firmware/' + DSP_NAME, dsp, 0o644)
    data = buf.getvalue()
    if len(data) > SETTINGS_BYTES:
        fail('the settings come to %d bytes, more than the %d the first boot reads' % (len(data), SETTINGS_BYTES))
    return data.ljust(SETTINGS_BYTES, b'\0')


# ------------------------------------------------------------------------------------------ after

def watch(known, minutes=10, sleep=time.sleep, clock=time.time, consoles=None):
    """After edl reset: TECHO5's USB serial console appears when the store's init is up. Only consoles
    that were not there before count (known): another unit on this computer is not this one."""
    consoles = consoles or list_consoles
    known = set(known)
    start, said = clock(), clock()
    while clock() - start < minutes * 60:
        if [p for p in consoles(CONSOLE_TECHO5) if p not in known]:
            return True
        if clock() - said >= 30:
            note('still waiting (%d s): the unit\'s screen shows its start' % (clock() - start))
            said = clock()
        sleep(5)
    return False


# ------------------------------------------------------------------------------------------ main

def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--release', default='latest', help='a release tag, or latest, whose signed manifest names %s'
                    % system_name('<v>'))
    ap.add_argument('--system', help='a system image you built (tools/linux/thinksmart/mkimage.sh, from a store '
                    'made with -k none), .img or .img.gz')
    ap.add_argument('--lk2nd', help='an lk2nd image of your own (tools/linux/thinksmart/lk2nd/build.sh: signed, no QCDT), in place '
                    'of the release\'s; needed with --system')
    ap.add_argument('--backup', help='a verified backup of this unit made before by this installer (its folder)')
    ap.add_argument('--name', help='the name Home Assistant shows, e.g. Office')
    ap.add_argument('--key-file', help='where the Home Assistant key is kept (default: in the backup folder)')
    ap.add_argument('--wifi', help='a Wi-Fi network to join (its passphrase is asked for); otherwise pick one '
                    'on the screen')
    ap.add_argument('--wifi-passphrase-file', help='a file holding the --wifi passphrase, for running from a script')
    ap.add_argument('--for-someone-else', action='store_true',
                    help='a gift install: no Wi-Fi, name or key is asked for or written; the unit starts its own '
                    'setup when next powered on')
    ap.add_argument('--let-home-assistant-add', action='store_true', help=LET_ADD_HELP)
    ap.add_argument('--dry-run', action='store_true', help='run every check, print every command that would write; '
                    'write nothing')
    ap.add_argument('--force', action='store_true', help='do not ask before writing')
    ap.add_argument('--no-watch', action='store_true', help='start the unit and stop; do not wait for it')
    ap.add_argument('--backups', default=default_dir('TECHO5_BACKUPS', 'backups'))
    ap.add_argument('--work', default=default_dir('TECHO5_WORK', 'build'))
    ap.add_argument('--edl', default='edl', help="bkerler's edl (default: edl on the PATH)")
    ap.add_argument('--edl-arg', action='append', default=[], help='an argument for every edl call, e.g. '
                    '--edl-arg=--loader=prog.mbn (repeat for more)')
    a = ap.parse_args(argv)
    check_gift(a, **{'--name': a.name, '--wifi': a.wifi, '--wifi-passphrase-file': a.wifi_passphrase_file,
                     '--key-file': a.key_file})
    check_let_add(a, **{'--key-file': a.key_file, '--for-someone-else': a.for_someone_else})
    gift, let_add = a.for_someone_else, a.let_home_assistant_add

    # ------------------------------------------------------------------------------------ 1. checks
    step('checks')
    need(a.edl, "install bkerler's edl, at the commit the docs use: "
         "pip install git+https://github.com/bkerler/edl@%s" % EDL_COMMIT)
    for path in [x for x in (a.system, a.lk2nd, a.backup) if x]:
        if not os.path.exists(path):
            fail('nothing at %s' % path)
    os.makedirs(a.work, exist_ok=True)
    edl = Edl(a.edl, a.edl_arg)
    note('looking for the unit in EDL (Vol+ and Vol- held while the power is plugged in)')
    first = edl.read(0, 34, os.path.join(a.work, 'thinksmart-gpt-primary.bin'))
    try:
        hdr = HEADER.unpack_from(first, SECTOR)
        if hdr[0] != b'EFI PART':
            raise GPTError('no GPT header at sector 1')
        total = hdr[6] + 1
        last = edl.read(total - 33, 33, os.path.join(a.work, 'thinksmart-gpt-backup.bin'))
        gpt, pending = read_table(first, last)
    except GPTError as e:
        fail("this unit's partition table cannot be read: %s. Nothing was written." % e)
    if pending:
        note('a run before stopped between writing the two copies of the new partition table (the backup\n'
             '   copy is new, the primary still stock); this run writes the same table again, both copies')
    layout = layout_of(gpt)
    if layout not in ('stock', 'techo5'):
        fail('the partition table is not the stock one of a %s, nor one this installer made: %s.\n'
             '   Nothing was written.' % (BOARD_NAME, layout))
    ud = gpt.named('userdata') if layout == 'stock' else None
    note('eMMC: %d MiB, disk %s; %s' % (gpt.total >> 11, gpt.disk_guid,
                                        'the stock partition table; userdata is %d MiB' % (ud.sectors >> 11)
                                        if ud else 'installed by this installer before: installing again'))
    serial, model = unit_serial(edl, gpt, a.work)
    note('unit: %s, serial %s' % (model, masked(serial)))
    folder = os.path.abspath(a.backup or os.path.join(a.backups, backup_name(serial)))
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
    if gift:
        note('gift install: no name, Wi-Fi network or Home Assistant key is asked for or written')
    else:
        a.name = ask_name(a.name, DEFAULT_NAME)
    as_name = "as '%s'" % a.name if a.name else 'as a gift'

    # ------------------------------------------------------------------------------------ 2. images
    step('the images')
    rel = None
    if a.system:
        version = 'your image'
        check_let_add_release(a, None)
        system = unpack_system(os.path.abspath(a.system), a.work)
    else:
        rel = Release(REPO, a.release, a.work)
        version = rel.version
        check_let_add_release(a, version)
        system = unpack_system(rel.asset(system_name(version)), a.work)
    check_system_image(system)
    note('system image: %s (%s)' % (os.path.basename(system), version))
    if a.lk2nd:
        lk2nd = os.path.abspath(a.lk2nd)
    elif rel:
        lk2nd = rel.asset(lk2nd_name(version))
    else:
        fail('with --system, pass --lk2nd too: an lk2nd built by tools/linux/thinksmart/lk2nd/build.sh')
    lk2nd_data = check_lk2nd(lk2nd)
    boot = gpt.named('boot')
    if len(lk2nd_data) > boot.sectors * SECTOR:
        fail('%s is larger than the boot partition' % lk2nd)
    note('lk2nd: %s, %d KiB' % (os.path.basename(lk2nd), len(lk2nd_data) >> 10))
    # lk2nd's image padded to whole sectors, for the write and the read back.
    lk2nd_padded = os.path.join(a.work, 'thinksmart-lk2nd.bin')
    write_bytes(lk2nd_padded, lk2nd_data.ljust(-(-len(lk2nd_data) // SECTOR) * SECTOR, b'\0'))
    if layout == 'stock':
        new = pending or new_layout(gpt)
        new_first, new_last = new.build()
        GPT(new_first, new_last)  # the table about to be written reads back as a table
    else:
        new = gpt
    sysp, data = new.named('system'), new.named('techo5-data')
    note('partitions: system %d..%d (%d MiB), techo5-data %d..%d (%d MiB)%s'
         % (sysp.first, sysp.last, sysp.sectors >> 11, data.first, data.last, data.sectors >> 11,
            '; android-system is Android\'s system, renamed' if layout == 'stock' else ''))

    # ------------------------------------------------------------------------------------ 3. backup
    step('backup')
    why = backup_verified(folder, gpt, serial)
    if not why:
        note('a verified backup of this unit is there already: %s' % folder)
        img = os.path.join(folder, 'emmc.img')
    elif a.backup:
        fail('--backup: %s. Without a verified backup nothing is written.' % why)
    elif a.dry_run:
        note('dry run: %s; the real run reads the whole eMMC (%d MiB) into %s first, with:'
             % (why, gpt.total >> 11, folder))
        print('   would read: edl rf %s' % os.path.join(folder, 'emmc.img'))
        img = None
    elif layout == 'techo5':
        fail('%s, and this unit has TECHO5 on it already, so a backup made now would not be of its stock\n'
             '   firmware. Pass --backup with the folder the first install made.' % why)
    elif pending:
        fail('%s, and a run before already wrote half of the new partition table, so a backup made now\n'
             '   would not be of the stock table. Pass --backup with the folder that run made.' % why)
    else:
        img = make_backup(edl, gpt, first, last, folder)
        note('the whole eMMC is in %s, checked against the unit\'s partition table' % folder)

    # ------------------------------------------------------------------------------------ 4. speaker
    step("the speaker's DSP table")
    dsp_path = os.path.join(folder, 'firmware', DSP_NAME)
    dsp = None
    if os.path.exists(dsp_path):
        dsp = read_file(dsp_path)
        note('already taken: %s' % dsp_path)
    elif img:
        os.makedirs(os.path.dirname(dsp_path), exist_ok=True)
        where = extract_dsp(img, gpt, dsp_path)
        if where:
            dsp = read_file(dsp_path)
            note('taken from the stock kernel in %s (%d bytes); it goes back onto this unit only' % (where, len(dsp)))
        else:
            note("warning: the stock kernel in the backup has no speaker tuning this installer recognizes. The\n"
                 "   speaker plays without it: flat and 4.5 dB quieter. Going on.")
    else:
        note('dry run: the real run takes it from the stock kernel in the backup')

    psk = None
    if not (gift or let_add):
        if os.path.exists(key_file):
            psk = read_file(key_file).decode('ascii', 'replace').strip()
            if not valid_api_key(psk):
                fail('the key in %s is not 32 bytes of base64' % key_file)
        else:
            psk = new_api_key()
    # The settings hold the Home Assistant key and the Wi-Fi key: kept in memory until the write, then
    # in a file only its owner can read, removed once it is on the unit.
    settings_data = settings_image(settings_files(gift, a.name or '', psk or '', wifi, let_add), dsp)
    settings = os.path.join(a.work, 'thinksmart-settings.bin')

    if a.dry_run:
        w = Writer(edl, True)
        print('\nDry run: every check passed. A real run would then do this (after the confirm):')
        w.write(boot.first, lk2nd_padded)
        if layout == 'stock':
            print('   would run: edl ws 0 <the new partition table> and edl ws %d <its backup copy>' % (gpt.total - 33))
        w.write(sysp.first, system)
        w.write(data.first, settings)
        w.reset()
        print('\nNothing was written to the unit.')
        return

    # ------------------------------------------------------------------------------------ 5. confirm
    mib = lambda n: n >> 11  # noqa: E731
    lines = ["About to install TECHO5 (%s) on this %s %s." % (version, BOARD_NAME, as_name)]
    if layout == 'stock':
        lines += [
            'This ERASES Android\'s userdata: the unit\'s apps, accounts and settings. On the eMMC it changes:',
            '  - boot (%d MiB): Android\'s kernel is replaced by lk2nd' % mib(boot.sectors),
            '  - the partition table: userdata (%d MiB) becomes TECHO5\'s system (%d MiB) and techo5-data (%d MiB);'
            % (mib(ud.sectors), mib(sysp.sectors), mib(data.sectors)),
            '    Android\'s system is renamed android-system, its contents untouched',
            '  - recovery: from the first update on, TECHO5 starts new kernels from it',
            'Every other partition, Android\'s system and vendor among them, is not written.']
    else:
        lines += ['TECHO5 is on this unit already. This ERASES its data (settings, logs) and writes its system',
                  'partition and lk2nd again; the partition table is not changed.']
    lines += ['The way back: the backup in %s, written back over EDL with edl' % folder,
              '(restore/RESTORE.txt there, and docs/install-thinksmart.md, "The way back").']
    confirm(a.force, lines)

    # ------------------------------------------------------------------------------------ 6. write
    step('write')
    w = Writer(edl, False)
    if gift:
        pass
    elif let_add:
        note('Home Assistant key: none; Home Assistant sets one when it adds the unit')
    elif not os.path.exists(key_file):
        os.makedirs(os.path.dirname(key_file) or '.', exist_ok=True)
        write_private(key_file, psk)
        note('Home Assistant key: new, in %s' % key_file)
    else:
        note('Home Assistant key: the one in %s' % key_file)
    write_checked(w, edl, boot.first, lk2nd_padded, a.work, 'lk2nd in boot')
    note('lk2nd is in boot, read back from the flash')
    if layout == 'stock':
        p, b = os.path.join(a.work, 'thinksmart-new-gpt-primary.bin'), os.path.join(a.work, 'thinksmart-new-gpt-backup.bin')
        write_bytes(p, new_first)
        write_bytes(b, new_last)
        # The backup copy first: a stop between the two leaves a primary that still says stock and a
        # backup copy that is exactly the new table, which a run again recognizes (read_table) and
        # finishes, rather than a new primary and a stale backup.
        write_checked(w, edl, gpt.total - 33, b, a.work, 'the backup partition table')
        write_checked(w, edl, 0, p, a.work, 'the partition table')
        note('the new partition table is on the unit, both copies, read back')
    note('writing TECHO5\'s system image (%d MiB); edl shows no progress here, and it takes a few minutes'
         % mib(sysp.sectors))
    w.write(sysp.first, system)
    # Read back: lk2nd's boot files and the start of the store, then the image's last MiB.
    head = (SYSTEM2[0] + 8192) * SECTOR
    with open(system, 'rb') as f:
        want_head = f.read(head)
        f.seek(SYSTEM_SECTORS * SECTOR - (1 << 20))
        want_tail = f.read()
    rb = os.path.join(a.work, 'readback.bin')
    if edl.read(sysp.first, head // SECTOR, rb) != want_head or \
            edl.read(sysp.first + SYSTEM_SECTORS - 2048, 2048, rb) != want_tail:
        fail('the system image did not read back as written; run this again (the backup is kept)')
    os.remove(rb)
    note('the system image is in system, its boot files and the start and end of the store read back')
    write_private(settings, settings_data)
    try:
        write_checked(w, edl, data.first, settings, a.work, 'the settings')
    finally:
        os.remove(settings)
    note('the settings%s are at the start of techo5-data' % (' and the speaker table' if dsp else ''))

    # ------------------------------------------------------------------------------------ 7. start
    step('start')
    known = [] if a.no_watch else list_consoles(CONSOLE_TECHO5)
    w.reset()
    note('the unit restarts into TECHO5. The first start makes techo5-data; the kernel\'s log is on the screen')
    if not a.no_watch:
        if watch(known):
            note("TECHO5's USB serial console is up: the unit has started")
        else:
            note('no USB console from the unit within 10 minutes. That may only be this computer not seeing it:\n'
                 "   look at the unit's screen, and for it in Home Assistant. See \"If it does not start\" in\n"
                 '   docs/install-thinksmart.md.')
    if gift:
        print('\nTECHO5 is installed. This unit starts its own setup when it is next powered on;')
        print("its owner's Home Assistant can add it with nothing typed.")
    elif let_add:
        print("\nTECHO5 starts as '%s'." % a.name)
        if not wifi:
            print("Pick the Wi-Fi network on the unit's screen.")
        print(let_add_done(a.name))
    else:
        print("\nTECHO5 starts as '%s'." % a.name)
        if not wifi:
            print("Pick the Wi-Fi network on the unit's screen.")
        print('Home Assistant finds it as an ESPHome device (Settings, Devices & services, Discovered). When it\n'
              'asks for the encryption key, paste:\n\n    %s\n\n(kept in %s)' % (psk, key_file))
    print("\nThe way back to Android is in %s\n(restore/RESTORE.txt) and in docs/install-thinksmart.md." % folder)


if __name__ == '__main__':
    run_main(main)
