#!/usr/bin/env python3
"""Offline tests for tools/install-thinksmart.py. No unit.

    python3 tools/test_install_thinksmart.py

The unit is a sparse disk image with a partition table shaped like a ThinkSmart View's stock one
(Lenovo's partitions at the same entries, userdata last, one free entry), smaller, and edl is a
stand-in that reads and writes it (rs, ws, rf, reset) and writes down what it was asked. The stock
kernel is a made-up Android boot image holding a made-up speaker table.
"""
import gzip
import hashlib
import importlib.util
import io
import os
import shutil
import struct
import sys
import tarfile
import tempfile
import unittest
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.dont_write_bytecode = True
_spec = importlib.util.spec_from_file_location('install_thinksmart', os.path.join(HERE, 'install-thinksmart.py'))
it = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(it)
from techo5lib import Fail  # noqa: E402

S = it.SECTOR
SYSTEM_TYPE = uuid.UUID('97d7b011-54da-4835-b3c4-917ad6e73d74')
BASIC = uuid.UUID('ebd0a0a2-b9e5-4433-87c0-68b6b72699c7')
DISK = uuid.UUID('98101b32-bbe2-4bf2-a06e-2bb33d000c20')

# Lenovo's partitions the install looks at, at their stock entries, packed small; the rest of the 52
# entries are filler, entry 51 free.
NAMED = {0: 'modem', 20: 'boot', 21: 'recovery', 22: 'devinfo', 23: 'system', 24: 'vendor', 26: 'persist',
         50: 'userdata'}
SIZES = {'modem': 2048, 'boot': 8192, 'recovery': 8192, 'system': 4096, 'vendor': 4096, 'persist': 2048}
USERDATA = it.SYSTEM_SECTORS + it.DATA_MIN_SECTORS + 4096


def stock_table():
    """(first 34 sectors, last 33, total sectors) of a stock-shaped GPT."""
    entries, lba = [], 34 + 2014  # start at 1 MiB
    for i in range(52):
        e = it.Entry(bytes(128), i)
        if i == 51:
            entries.append(e)
            continue
        name = NAMED.get(i, 'part%d' % i)
        size = SIZES.get(name, 8)
        if name == 'userdata':
            lba = (lba + 7) // 8 * 8
            size = USERDATA
        e.type = SYSTEM_TYPE if name in ('system', 'vendor') else BASIC
        e.guid = uuid.UUID(int=0x1000 + i)
        e.first, e.last, e.attrs, e.name = lba, lba + size - 1, (1 << 60) if i < 24 else 0, name
        entries.append(e)
        lba += size
    total = lba + 33
    g = it.GPT.__new__(it.GPT)
    g.mbr = b'\0' * 446 + struct.pack('<4xB3xII', 0xee, 1, total - 1) + bytes(48) + b'\x55\xaa'
    g.rev, g.alt, g.first_lba, g.last_lba = 0x10000, total - 1, 34, total - 34
    g.disk_guid, g.count, g.esize, g.total, g.entries = DISK, 52, 128, total, entries
    first, last = g.build()
    return first, last, total


def boot_image(kernel):
    """An Android boot image (header v0) holding kernel gzip'd."""
    gz = gzip.compress(kernel, mtime=0)
    hdr = bytearray(2048)
    hdr[:8] = b'ANDROID!'
    struct.pack_into('<II', hdr, 8, len(gz), 0x8000)
    struct.pack_into('<I', hdr, 36, 2048)
    return bytes(hdr) + gz + bytes(-len(gz) % 2048)


def lk2nd_image(signed=True, qcdt=0):
    """An lk2nd image shaped as build.sh's: a boot image (header v0, 2048-byte pages) for this board, then
    the AVB1 signature (a DER SEQUENCE) and SEANDROIDENFORCE. qcdt: an appended QCDT's size."""
    kernel = b'lk2nd 23.1 lenovo,cd-18781y' + b'\x11' * 5000
    hdr = bytearray(2048)
    hdr[:8] = b'ANDROID!'
    struct.pack_into('<9I', hdr, 8, len(kernel), 0x8000, 0, 0, 0, 0, 0x100, 2048, qcdt)
    img = bytes(hdr) + kernel + bytes(-len(kernel) % 2048) + b'Q' * qcdt + bytes(-qcdt % 2048)
    if signed:
        img += b'\x30\x82\x01\x00' + b'\x5a' * 256
    return img + b'SEANDROIDENFORCE'


def devinfo(serial, model='ZA690000US'):
    """devinfo's first sector as aboot writes it: the magic, the serial at 228, the model after it."""
    d = bytearray(512)
    d[:13] = b'ANDROID-BOOT!'
    struct.pack_into('<I', d, 224, 1)
    d[228:228 + len(serial)] = serial.encode()
    at = 228 + len(serial) + 1
    d[at:at + len(model)] = model.encode()
    return bytes(d)


SERIAL = 'TEST0001'


def dsp_table():
    """A made-up speaker table: (its bytes, its registers' sha256)."""
    pairs = [((i * 7) % 128, (i * 13) % 256) for i in range(it.DSP_ENTRIES)]
    return bytes(x for p in pairs for x in p), hashlib.sha256(bytes(p[0] for p in pairs)).hexdigest()


def stock_kernel(table):
    """Code, the driver's 15 setup pairs and the table as {u32, u32}, more code."""
    pairs = [(0, 0)] * 3 + [(127, 0), (2, 17)] + [(0, 0)] * 10
    pairs += [(table[i], table[i + 1]) for i in range(0, len(table), 2)]
    return b'\xff\xee' * 4000 + b''.join(struct.pack('<II', r, v) for r, v in pairs) + b'\xff\xff\xff\xff' * 999


FAKE_EDL = r'''
import os, sys, shutil, subprocess
disk = os.environ['FAKE_DISK']
args = [a for a in sys.argv[1:] if not a.startswith('--')]
with open(os.environ['FAKE_LOG'], 'a') as log:
    log.write(' '.join(args) + '\n')
if os.environ.get('FAKE_NO_UNIT'):
    print('main - Waiting for the device'); sys.exit(1)
cmd = args[0]
if cmd == 'rs':
    start, count, out = int(args[1]), int(args[2]), args[3]
    with open(disk, 'rb') as f:
        f.seek(start * 512); data = f.read(count * 512)
    open(out, 'wb').write(data)
elif cmd == 'ws':
    start, src = int(args[1]), args[2]
    if os.environ.get('FAKE_STOP_AT') == str(start):
        print('main - Waiting for the device'); sys.exit(1)  # the cable pulled before this write
    with open(disk, 'r+b') as d, open(src, 'rb') as s:
        d.seek(start * 512)
        while True:
            c = s.read(4 << 20)
            if not c: break
            if c.strip(b'\0'): d.write(c)
            else: d.seek(len(c), 1)
elif cmd == 'rf':
    subprocess.run(['cp', '--sparse=always', disk, args[1]], check=True)
    short = os.environ.get('FAKE_SHORT_RF')
    if short:
        os.truncate(args[1], os.path.getsize(args[1]) - int(short))
elif cmd == 'reset':
    if os.environ.get('FAKE_RESET_USBERROR'):
        print('DeviceClass - USBError(5, \'Input/Output Error\')'); sys.exit(1)
print('ok')
'''


class Parts(unittest.TestCase):
    def setUp(self):
        self.first, self.last, self.total = stock_table()
        self.gpt = it.GPT(self.first, self.last)

    def test_stock_table_reads_as_stock(self):
        self.assertEqual(it.layout_of(self.gpt), 'stock')

    def test_new_layout(self):
        g1, g2 = uuid.UUID(int=1), uuid.UUID(int=2)
        new = it.new_layout(self.gpt, (g1, g2))
        first, last = new.build()
        back = it.GPT(first, last)  # CRCs and both copies right
        self.assertEqual(it.layout_of(back), 'techo5')
        self.assertEqual(first[:S], self.first[:S], 'the protective MBR is kept')
        self.assertEqual(back.disk_guid, DISK)
        ud = self.gpt.named('userdata')
        sysp, data = back.named('system'), back.named('techo5-data')
        self.assertEqual((sysp.index, sysp.first, sysp.sectors, sysp.type, sysp.guid), (50, ud.first, it.SYSTEM_SECTORS,
                                                                                     it.LINUX_DATA, g1))
        self.assertEqual((data.index, data.first, data.last, data.type, data.guid),
                         (51, ud.first + it.SYSTEM_SECTORS, ud.last, it.LINUX_DATA, g2))
        andr = back.named(it.ANDROID_SYSTEM)
        old = self.gpt.entries[23]
        self.assertEqual((andr.index, andr.first, andr.last, andr.type, andr.guid, andr.attrs),
                         (23, old.first, old.last, old.type, old.guid, old.attrs))
        for i in range(52):
            if i not in (23, 50, 51):
                self.assertEqual(back.entries[i].pack(), self.gpt.entries[i].pack(), 'entry %d changed' % i)

    def test_refuses_what_it_does_not_know(self):
        def changed(fn):
            g = it.GPT(self.first, self.last)
            fn(g)
            f, l = g.build()
            return it.layout_of(it.GPT(f, l))
        self.assertIn('no userdata', changed(lambda g: setattr(g.entries[50], 'name', 'data')))
        self.assertIn('no free entry', changed(lambda g: setattr(g.entries[51], 'type', BASIC) or
                                               setattr(g.entries[51], 'name', 'extra')))
        self.assertIn('share a name', changed(lambda g: setattr(g.entries[24], 'name', 'system')))
        self.assertIn('too small', changed(lambda g: setattr(g.entries[50], 'first', g.entries[50].last - 1000)))

        def by_hand(g):  # docs/install-thinksmart.md's repartition.sh
            ud = g.entries[50]
            g.entries[51].type, g.entries[51].name = it.LINUX_DATA, 'techo5-data'
            g.entries[51].first, g.entries[51].last = ud.first + it.SYSTEM_SECTORS, ud.last
            ud.last = ud.first + it.SYSTEM_SECTORS - 1
        self.assertIn('by hand', changed(by_hand))

    def test_a_half_written_table_is_read_as_the_run_that_wrote_it(self):
        g1, g2 = uuid.UUID(int=1), uuid.UUID(int=2)
        new_last = it.new_layout(self.gpt, (g1, g2)).build()[1]
        gpt, pending = it.read_table(self.first, new_last)
        self.assertEqual(it.layout_of(gpt), 'stock')
        self.assertEqual((pending.named('system').guid, pending.named('techo5-data').guid), (g1, g2))
        self.assertEqual(pending.build()[1], new_last)
        self.assertIsNone(it.read_table(self.first, self.last)[1])
        # A backup copy that is the new table but not of this primary (another userdata end) is not let
        # through, nor one that is anything else.
        other = it.GPT(self.first, self.last)
        other.named('userdata').last -= 8
        with self.assertRaisesRegex(it.GPTError, 'same partitions'):
            it.read_table(self.first, it.new_layout(other, (g1, g2)).build()[1])
        bad = bytearray(new_last)
        bad[100] ^= 1
        with self.assertRaises(it.GPTError):
            it.read_table(self.first, bytes(bad))

    def test_model_from_devinfo(self):
        self.assertEqual(it.model_of(devinfo('TEST0003')), 'ZA690000US')
        self.assertEqual(it.model_of(devinfo('R58M12345678', 'SM-T510')), None)
        self.assertEqual(it.model_of(devinfo('R58M12345678', 'SMT510XX')), 'SMT510XX')
        self.assertIsNone(it.model_of(bytes(512)))
        self.assertEqual(it.masked('TEST0003'), 'TES***03')

    def test_damaged_tables(self):
        bad = bytearray(self.first)
        bad[2 * S + 60] ^= 1
        with self.assertRaisesRegex(it.GPTError, 'CRC'):
            it.GPT(bytes(bad), self.last)
        other = bytearray(self.last)
        other[100] ^= 1
        with self.assertRaisesRegex(it.GPTError, 'same partitions'):
            it.GPT(self.first, bytes(other))


class Speaker(unittest.TestCase):
    def test_found_by_its_registers(self):
        table, regs = dsp_table()
        self.assertEqual(it.find_dsp_table(stock_kernel(table), regs), table)

    def test_other_registers_are_not_it(self):
        table, regs = dsp_table()
        self.assertIsNone(it.find_dsp_table(stock_kernel(table), 'ab' * 32))
        self.assertIsNone(it.find_dsp_table(stock_kernel(table[:-200]), regs))

    def test_from_the_boot_partition(self):
        table, regs = dsp_table()
        img = boot_image(stock_kernel(table))
        self.assertEqual(it.find_dsp_table(it.boot_kernel(img), regs), table)


class Settings(unittest.TestCase):
    def members(self, image):
        self.assertEqual(len(image), it.SETTINGS_BYTES)
        self.assertEqual(image[257:262], b'ustar')  # what the store's init looks for
        with tarfile.open(fileobj=io.BytesIO(image)) as t:
            return {m.name: t.extractfile(m).read() for m in t.getmembers()}

    def test_name_key_wifi_and_speaker(self):
        m = self.members(it.settings_image(it.settings_files(False, 'Office', 'K' * 44, 'wifi\n'), b'dsp'))
        self.assertEqual(m, {'settings/name': b'Office\n', 'settings/psk': b'K' * 44 + b'\n',
                             'settings/wpa_supplicant.conf': b'wifi\n', 'firmware/' + it.DSP_NAME: b'dsp'})

    def test_gift_and_let_add(self):
        self.assertEqual(set(self.members(it.settings_image(it.settings_files(True, '', '', None), b'd'))),
                         {'settings/firstrun', 'firmware/' + it.DSP_NAME})
        self.assertEqual(set(self.members(it.settings_image(it.settings_files(False, 'O', '', None, True), None))),
                         {'settings/name', 'settings/' + it.LET_ADD_MARKER})


class Install(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp(prefix='install-thinksmart-')
        self.disk = os.path.join(self.d, 'emmc')
        self.first, self.last, self.total = stock_table()
        self.gpt = it.GPT(self.first, self.last)
        with open(self.disk, 'wb') as f:
            f.truncate(self.total * S)
            f.write(self.first)
            f.seek((self.total - 33) * S)
            f.write(self.last)
            self.table, regs = dsp_table()
            f.seek(self.gpt.named('boot').first * S)
            f.write(boot_image(stock_kernel(self.table)))
            f.seek(self.gpt.named('devinfo').first * S)
            f.write(devinfo(SERIAL))
        self.regs = regs
        self.edl = os.path.join(self.d, 'edl.py')
        with open(self.edl, 'w') as f:
            f.write(FAKE_EDL)
        self.log = os.path.join(self.d, 'edl.log')
        self.system = os.path.join(self.d, 'system.img')
        with open(self.system, 'wb') as f:
            f.truncate(it.SYSTEM_SECTORS * S)
            f.write(b'\0' * 446 + struct.pack('<4xB3xII', 0x83, *it.SYSTEM1) + struct.pack('<4xB3xII', 0x83, *it.SYSTEM2)
                    + bytes(32) + b'\x55\xaa')
            for start, label in ((it.SYSTEM1[0], b''), (it.SYSTEM2[0], it.STORE_LABEL)):
                sb = bytearray(1024)
                sb[56:58] = b'\x53\xef'
                sb[120:120 + len(label)] = label
                f.seek(start * S + 1024)
                f.write(sb)
            f.seek(it.SYSTEM2[0] * S + (1 << 20))
            f.write(b'the store')
            f.seek(it.SYSTEM_SECTORS * S - 100)
            f.write(b'the end')
        self.lk2nd = os.path.join(self.d, 'lk2nd.img')
        with open(self.lk2nd, 'wb') as f:
            f.write(lk2nd_image())
        self.env = dict(os.environ, FAKE_DISK=self.disk, FAKE_LOG=self.log)
        self.saved = it.DSP_REGS_SHA256
        it.DSP_REGS_SHA256 = self.regs

    def tearDown(self):
        it.DSP_REGS_SHA256 = self.saved
        shutil.rmtree(self.d, ignore_errors=True)

    def install(self, *extra, env=None, lk2nd=True):
        old = dict(os.environ)
        os.environ.update(env or self.env)
        out = io.StringIO()
        try:
            sys.stdout, saved = out, sys.stdout
            try:
                it.main(['--system', self.system] + (['--lk2nd', self.lk2nd] if lk2nd else []) +
                        ['--edl', self.edl, '--force', '--no-watch',
                         '--backups', os.path.join(self.d, 'backups'), '--work', os.path.join(self.d, 'work')]
                        + list(extra))
            finally:
                sys.stdout = saved
        finally:
            os.environ.clear()
            os.environ.update(old)
        return out.getvalue()

    def read(self, start, count):
        with open(self.disk, 'rb') as f:
            f.seek(start * S)
            return f.read(count * S)

    def writes(self):
        with open(self.log) as f:
            return [l.split() for l in f if l.startswith('ws ')]

    def test_from_stock(self):
        out = self.install('--name', 'Office', '--wifi', 'Home', '--wifi-passphrase-file', self.passphrase())
        new = it.GPT(self.read(0, 34), self.read(self.total - 33, 33))
        self.assertEqual(it.layout_of(new), 'techo5')
        boot = new.named('boot')
        with open(self.lk2nd, 'rb') as f:
            want = f.read()
        self.assertEqual(self.read(boot.first, -(-len(want) // S))[:len(want)], want)
        sysp, data = new.named('system'), new.named('techo5-data')
        self.assertEqual(self.read(sysp.first + it.SYSTEM2[0], 4)[1024 + 120:1024 + 132], it.STORE_LABEL)
        with open(self.system, 'rb') as f:
            f.seek(it.SYSTEM_SECTORS * S - S)
            self.assertEqual(self.read(sysp.first + it.SYSTEM_SECTORS - 1, 1), f.read())
        with tarfile.open(fileobj=io.BytesIO(self.read(data.first, it.SETTINGS_BYTES // S))) as t:
            m = {x.name: t.extractfile(x).read() for x in t.getmembers()}
        self.assertEqual(m['settings/name'], b'Office\n')
        self.assertEqual(m['firmware/' + it.DSP_NAME], self.table)
        self.assertIn(b'network={', m['settings/wpa_supplicant.conf'])
        self.assertNotIn(b'secret passphrase', m['settings/wpa_supplicant.conf'])
        key = os.path.join(self.d, 'backups', 'thinksmart-' + SERIAL, 'home-assistant.key')
        with open(key) as f:
            self.assertEqual(m['settings/psk'].decode().strip(), f.read().strip())
        self.assertIn(m['settings/psk'].decode().strip(), out)
        self.assertIn('unit: ZA690000US, serial TES***01', out)
        self.assertNotIn('serial ' + SERIAL, out)
        for left in ('thinksmart-settings.bin', 'thinksmart-devinfo.bin', 'readback.bin'):
            self.assertFalse(os.path.exists(os.path.join(self.d, 'work', left)), left)
        # The order: lk2nd, the backup table before the primary, the image, the settings; then reset.
        ws = self.writes()
        self.assertEqual([int(w[1]) for w in ws], [boot.first, self.total - 33, 0, sysp.first, data.first])
        with open(self.log) as f:
            self.assertEqual(f.read().split('\n')[-2], 'reset')
        # The backup: verified, the unit as it was, the way back.
        b = os.path.join(self.d, 'backups', 'thinksmart-' + SERIAL)
        self.assertEqual(it.backup_verified(b, self.gpt, SERIAL), '')
        with open(os.path.join(b, 'emmc.img'), 'rb') as f:
            self.assertEqual(f.read(34 * S), self.first)
        with open(os.path.join(b, 'firmware', it.DSP_NAME), 'rb') as f:
            self.assertEqual(f.read(), self.table)
        with open(os.path.join(b, 'restore', 'RESTORE.txt')) as f:
            r = f.read()
        self.assertIn('edl ws %d restore/gpt-backup.bin' % (self.total - 33), r)
        self.assertIn('edl ws %d restore/boot.img' % self.gpt.named('boot').first, r)
        self.assertIn('edl ws %d restore/zero-1MiB.bin' % self.gpt.named('userdata').first, r)
        self.assertNotIn('already held lk2nd', r)
        with open(os.path.join(b, 'restore', 'boot.img'), 'rb') as f:
            self.assertEqual(f.read(8), b'ANDROID!')

    def passphrase(self):
        p = os.path.join(self.d, 'pass')
        with open(p, 'w') as f:
            f.write('secret passphrase\n')
        return p

    def test_dry_run_writes_nothing(self):
        before = self.read(0, 34) + self.read(self.gpt.named('boot').first, 16)
        out = self.install('--name', 'Office', '--dry-run')
        self.assertIn('Nothing was written', out)
        self.assertEqual(self.writes(), [])
        self.assertEqual(self.read(0, 34) + self.read(self.gpt.named('boot').first, 16), before)
        self.assertFalse(os.path.exists(os.path.join(self.d, 'backups', 'thinksmart-' + SERIAL, 'emmc.img')))
        self.assertFalse(os.path.exists(os.path.join(self.d, 'work', 'thinksmart-settings.bin')))

    def test_again_on_its_own_install(self):
        self.install('--name', 'Office')
        table = self.read(0, 34)
        open(self.log, 'w').close()
        self.install('--name', 'Kitchen')
        self.assertEqual(self.read(0, 34), table, 'the partition table is not written again')
        new = it.GPT(self.read(0, 34), self.read(self.total - 33, 33))
        self.assertEqual([int(w[1]) for w in self.writes()],
                         [new.named('boot').first, new.named('system').first, new.named('techo5-data').first])
        with open(self.log) as f:
            self.assertNotIn('rf', f.read().split(), 'no second backup')

    def test_again_needs_the_first_backup(self):
        self.install('--name', 'Office')
        shutil.rmtree(os.path.join(self.d, 'backups'))
        with self.assertRaisesRegex(Fail, 'Pass --backup'):
            self.install('--name', 'Kitchen')

    def test_short_backup_writes_nothing(self):
        env = dict(self.env, FAKE_SHORT_RF=str(4096))
        with self.assertRaisesRegex(Fail, 'not complete'):
            self.install('--name', 'Office', env=env)
        self.assertEqual(self.writes(), [])

    def test_unknown_table_writes_nothing(self):
        g = it.GPT(self.first, self.last)
        g.entries[50].name = 'data'
        f, l = g.build()
        with open(self.disk, 'r+b') as d:
            d.write(f)
            d.seek((self.total - 33) * S)
            d.write(l)
        with self.assertRaisesRegex(Fail, 'no userdata'):
            self.install('--name', 'Office')
        self.assertEqual(self.writes(), [])

    def test_no_speaker_table_installs_without_it(self):
        it.DSP_REGS_SHA256 = 'cd' * 32
        out = self.install('--name', 'Office')
        self.assertIn('no speaker tuning', out)
        new = it.GPT(self.read(0, 34), self.read(self.total - 33, 33))
        with tarfile.open(fileobj=io.BytesIO(self.read(new.named('techo5-data').first, it.SETTINGS_BYTES // S))) as t:
            names = t.getnames()
        self.assertIn('settings/name', names)
        self.assertNotIn('firmware/' + it.DSP_NAME, names)

    def test_no_unit(self):
        with self.assertRaisesRegex(Fail, 'edl rs failed'):
            self.install('--name', 'Office', env=dict(self.env, FAKE_NO_UNIT='1'))

    def test_bad_system_image(self):
        with open(self.system, 'r+b') as f:
            f.seek(it.SYSTEM2[0] * S + 1024 + 120)
            f.write(b'other-label\0\0\0\0\0')
        with self.assertRaisesRegex(Fail, 'not techo5-store'):
            self.install('--name', 'Office')
        self.assertEqual(self.writes(), [])

    def test_lk2nd_the_stock_bootloader_will_not_start(self):
        # lk2nd's own release images: unsigned (all), a QCDT appended (21.0 on). The stock bootloader
        # crashes on either, so neither may reach boot.
        for img, why in ((lk2nd_image(signed=False), 'not signed'), (lk2nd_image(qcdt=8192), 'QCDT')):
            with open(self.lk2nd, 'wb') as f:
                f.write(img)
            with self.assertRaisesRegex(Fail, why):
                self.install('--name', 'Office')
            self.assertEqual(self.writes(), [])

    def test_lk2nd_for_another_board(self):
        with open(self.lk2nd, 'wb') as f:
            f.write(lk2nd_image().replace(b'lenovo,cd-18781y', b'lenovo,other-one'))
        with self.assertRaisesRegex(Fail, 'does not know this board'):
            self.install('--name', 'Office')

    def test_own_system_needs_own_lk2nd(self):
        with self.assertRaisesRegex(Fail, 'pass --lk2nd'):
            self.install('--name', 'Office', lk2nd=False)
        self.assertEqual(self.writes(), [])

    def test_reset_usb_error_is_the_unit_restarting(self):
        out = self.install('--name', 'Office', env=dict(self.env, FAKE_RESET_USBERROR='1'))
        self.assertEqual(len(self.writes()), 5)
        self.assertNotIn('failed', out)


    def test_units_are_told_apart_by_serial_not_by_the_factory_disk_id(self):
        # Every unit has the same disk id and partition table from Lenovo's factory image: a second
        # unit must get a backup and speaker table of its own, not the first unit's.
        self.install('--name', 'Office')
        first = os.path.join(self.d, 'backups', 'thinksmart-' + SERIAL)
        self.assertEqual(it.backup_verified(first, self.gpt, SERIAL), '')
        self.assertIn('another unit', it.backup_verified(first, self.gpt, 'TEST0002'))
        # The second unit: stock again, the same table and disk id, another serial and speaker table.
        with open(self.disk, 'r+b') as f:
            f.write(self.first)
            f.seek((self.total - 33) * S)
            f.write(self.last)
            f.seek(self.gpt.named('devinfo').first * S)
            f.write(devinfo('TEST0002'))
        os.remove(self.log)
        out = self.install('--name', 'Kitchen')
        second = os.path.join(self.d, 'backups', 'thinksmart-TEST0002')
        self.assertIn('unit: ZA690000US, serial TES***02', out)
        self.assertNotIn('there already', out)
        self.assertEqual(it.backup_verified(second, self.gpt, 'TEST0002'), '')
        with open(self.log) as f:
            self.assertTrue(any(l.split()[0] == 'rf' for l in f))

    def test_backup_of_another_unit_is_refused(self):
        self.install('--name', 'Office')
        first = os.path.join(self.d, 'backups', 'thinksmart-' + SERIAL)
        with open(self.disk, 'r+b') as f:
            f.write(self.first)
            f.seek((self.total - 33) * S)
            f.write(self.last)
            f.seek(self.gpt.named('devinfo').first * S)
            f.write(devinfo('TEST0002'))
        os.remove(self.log)
        with self.assertRaisesRegex(Fail, 'another unit'):
            self.install('--name', 'Kitchen', '--backup', first)
        self.assertEqual(self.writes(), [])

    def test_no_serial_writes_nothing(self):
        with open(self.disk, 'r+b') as f:
            f.seek(self.gpt.named('devinfo').first * S)
            f.write(bytes(512))
        with self.assertRaisesRegex(Fail, 'cannot be told apart'):
            self.install('--name', 'Office')
        self.assertEqual(self.writes(), [])

    def test_another_model_writes_nothing(self):
        with open(self.disk, 'r+b') as f:
            f.seek(self.gpt.named('devinfo').first * S)
            f.write(devinfo(SERIAL, 'SM8250XX'))
        with self.assertRaisesRegex(Fail, 'model SM8250XX, not a Lenovo ThinkSmart View'):
            self.install('--name', 'Office')
        self.assertEqual(self.writes(), [])

    def test_a_stop_between_the_two_table_writes_is_finished(self):
        # The first run stops as it goes to write the primary table: the backup copy is new, the
        # primary still stock. The next run sees that, keeps the first run's table and backup, and
        # finishes; nothing else is let through as a half-written table.
        env = dict(self.env, FAKE_STOP_AT='0')
        with self.assertRaisesRegex(Fail, 'edl ws failed'):
            self.install('--name', 'Office', env=env)
        half = self.read(self.total - 33, 33)
        self.assertEqual(self.read(0, 34), self.first, 'the primary is still stock')
        gpt, pending = it.read_table(self.first, half)
        self.assertIsNotNone(pending)
        os.remove(self.log)
        out = self.install('--name', 'Office')
        self.assertIn('stopped between writing the two copies', out)
        new = it.GPT(self.read(0, 34), self.read(self.total - 33, 33))
        self.assertEqual(it.layout_of(new), 'techo5')
        self.assertEqual(self.read(self.total - 33, 33), half, "the first run's table, its ids too")
        with open(self.log) as f:
            self.assertNotIn('rf', [l.split()[0] for l in f], 'the first run\'s backup, not a second one')
        self.assertIn(0, [int(w[1]) for w in self.writes()])

    def test_a_half_written_table_with_no_backup_writes_nothing(self):
        with self.assertRaises(Fail):
            self.install('--name', 'Office', env=dict(self.env, FAKE_STOP_AT='0'))
        shutil.rmtree(os.path.join(self.d, 'backups'))
        os.remove(self.log)
        with self.assertRaisesRegex(Fail, 'half of the new partition table'):
            self.install('--name', 'Office')
        self.assertEqual(self.writes(), [])

    def test_lk2nd_in_boot_before_the_backup_is_said_in_restore(self):
        with open(self.disk, 'r+b') as f:
            f.seek(self.gpt.named('boot').first * S)
            f.write(lk2nd_image())
        out = self.install('--name', 'Office')
        self.assertIn("boot already holds lk2nd", out)
        with open(os.path.join(self.d, 'backups', 'thinksmart-' + SERIAL, 'restore', 'RESTORE.txt')) as f:
            self.assertIn('already held lk2nd', f.read())


if __name__ == '__main__':
    unittest.main()
