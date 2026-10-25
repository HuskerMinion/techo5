#!/usr/bin/env python3
"""Offline tests for install-skylight.py, mkgpt.py and install.sh: no unit, no network.

    python3 tools/test_install_skylight.py

install-skylight.py runs against a simulated Skylight running stock Android: a small made-up eMMC (32
MiB, with a stock-shaped partition table built here, independently of mkgpt.py) behind a fake adb that
knows each command the installer sends and refuses any other, so a new command cannot slip in untested.
The sizes are scaled down for that: techo5-store is 4 MiB here, not 2 GiB.

install.sh runs on a simulated eMMC too, in a real POSIX shell (Git Bash on Windows) with /sys, /dev,
/proc, /mnt and /tmp in a scratch folder (INSTALL_ROOT), on the very set the installer staged.

The geometry tests use the real sizes: stock tables of several 16 GB eMMCs.
"""
import gzip
import hashlib
import importlib.util
import io
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
import uuid
import zlib
from contextlib import redirect_stdout, redirect_stderr

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('install_skylight', os.path.join(HERE, 'install-skylight.py'))
inst = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inst)
mkgpt = inst.mkgpt
Fail = sys.modules['techo5lib'].Fail
SKY = os.path.join(HERE, 'linux', 'skylight')
INSTALL_SH = os.path.join(SKY, 'install', 'install.sh')
PACK = os.path.join(SKY, 'kernel', 'pack-sky.py')

S = 512
FP = 'rockchip/D156/D156:12/AndroidS/D156.V1.3:userdebug/test-keys'
NAMES = ('security', 'uboot', 'trust', 'misc', 'dtbo', 'vbmeta', 'boot', 'recovery', 'backup', 'cache',
         'metadata', 'baseparameter', 'super', 'userdata')
BASIC = uuid.UUID('ebd0a0a2-b9e5-4433-87c0-68b6b72699c7').bytes_le


# ------------------------------------------------------------------------------------------ made-up tables

def crc(b):
    return zlib.crc32(bytes(b)) & 0xffffffff


def gpt(total, parts, disk_guid=None):
    """(first 34 sectors, last 33 sectors) of a GPT with parts [(name, first, last)], numbered from 1,
    written out here from the UEFI layout, not from mkgpt.py."""
    ents = bytearray(128 * 128)
    for i, (name, a, b) in enumerate(parts):
        e = bytearray(128)
        e[0:16] = BASIC
        e[16:32] = hashlib.md5(('%d-%s' % (i, name)).encode()).digest()
        struct.pack_into('<QQQ', e, 32, a, b, 0)
        e[56:56 + 72] = name.encode('utf-16le').ljust(72, b'\0')
        ents[i * 128:(i + 1) * 128] = e
    ecrc = crc(ents)
    guid = disk_guid or hashlib.md5(b'disk').digest()

    def header(cur, alt, elba):
        h = bytearray(92)
        h[:8] = b'EFI PART'
        struct.pack_into('<IIIIQQQQ', h, 8, 0x10000, 92, 0, 0, cur, alt, 34, total - 34)
        h[56:72] = guid
        struct.pack_into('<QIII', h, 72, elba, 128, 128, ecrc)
        struct.pack_into('<I', h, 16, crc(h))
        return bytes(h).ljust(S, b'\0')

    mbr = bytearray(S)
    mbr[446 + 4] = 0xee
    mbr[510:512] = b'\x55\xaa'
    first = bytes(mbr) + header(1, total - 1, 2) + bytes(ents)
    last = bytes(ents) + header(total - 1, 1, total - 33)
    return first, last


def stock_parts(total, fixed=1024, recovery=4096, super_=2048, start=2048):
    """The stock table's shape: 13 fixed partitions, then userdata to 65 sectors before the end (as on
    the unit: the last usable LBA rounded down to 32), starting on a 1 MiB boundary."""
    parts, at = [], start
    for name in NAMES[:-1]:
        size = {'recovery': recovery, 'super': super_}.get(name, fixed)
        parts.append((name, at, at + size - 1))
        at += size
    at = (at + 2047) // 2048 * 2048
    parts.append(('userdata', at, total - 66 + 1))
    return parts


# Real-sized tables: 4 MiB fixed partitions, a 128 MiB recovery and a large super, as Rockchip's
# parameter files lay them out, on 16 GB eMMCs of several makers' sizes.
REAL_SIZES = dict(fixed=8192, recovery=262144, super_=4718592, start=8192)
REAL_TOTALS = (30539776, 30310400, 30777344, 31116288, 29360128)


def entries(blob, at=S):
    return [blob[at + i * 128:at + (i + 1) * 128] for i in range(128)]


def entry_fields(e):
    a, b = struct.unpack_from('<QQ', e, 32)
    return bytes(e[56:128]).decode('utf-16le').split('\0')[0], a, b, bytes(e[16:32])


# ------------------------------------------------------------------------------------------ made-up images

def boot_image(tmp, name, cmdline):
    """A boot image made by the real packer (pack-sky.py) from a made-up kernel and device tree."""
    kernel = bytearray(64 * 1024)
    kernel[0x38:0x3c] = b'ARM\x64'
    kernel[0x100:0x110] = name.encode().ljust(16, b'.')
    dtb = b'\xd0\x0d\xfe\xed' + bytes(1020)
    paths = {}
    for k, v in (('kernel', bytes(kernel)), ('dtb', dtb), ('ramdisk', gzip.compress(b'ramdisk ' + name.encode(), mtime=0))):
        paths[k] = os.path.join(tmp, '%s.%s' % (name, k))
        with open(paths[k], 'wb') as f:
            f.write(v)
    out = os.path.join(tmp, name + '.img')
    subprocess.run([sys.executable, '-I', PACK, '--kernel', paths['kernel'], '--dtb', paths['dtb'], '--ramdisk',
                    paths['ramdisk'], '--cmdline', cmdline, '--out', out], check=True, stdout=subprocess.DEVNULL)
    return out


INSTALLER_CMDLINE = 'console=ttyS2,1500000n8 panic=10 rdinit=/init techo5.installer=1'
BOOT_CMDLINE = 'console=ttyS2,1500000n8 panic=10 ro rootwait root=PARTLABEL=techo5-store init=/init'


def ext4_image(label, size=1 << 20, fill=b'STORE'):
    img = bytearray(size)
    img[1024 + 56:1024 + 58] = b'\x53\xef'
    img[1024 + 120:1024 + 120 + len(label)] = label
    img[8192:8192 + 64] = (fill * 64)[:64]
    return bytes(img)


FIRMWARE = {'fw_bcm43456c5_ag.bin': b'MADE-UP-WIFI-FIRMWARE-' * 50,
            'nvram_ap6256.txt': b'boardtype=made-up\nmacaddr=00:00:00:00:00:00\n' * 10,
            'BCM4345C5.hcd': b'MADE-UP-BT-PATCH-' * 40}


# ------------------------------------------------------------------------------------------ simulated unit

TOTAL = 65536


class FakeSkylight:
    """A Skylight running stock Android, as install-skylight.py sees it over adb. Every command the
    installer sends is recognized here; anything else is an error."""

    DISK = 'mmcblk2'

    def __init__(self, total=TOTAL, fingerprint=FP, btype='userdebug', root_ok=True, compat=None, touch=True,
                 timing=(1920, 1080), firmware=True, cache_free=64 << 20, cache=True, parts=None, last=None):
        self.total = total
        self.fingerprint, self.btype, self.root_ok = fingerprint, btype, root_ok
        self.compat = compat if compat is not None else ['rockchip,rk3566-evb', 'rockchip,rk3566']
        self.touch, self.timing, self.firmware = touch, timing, firmware
        self.cache_free, self.cache = cache_free, cache
        self.rooted = False
        self.parts = parts or stock_parts(total)
        first, back = gpt(total, self.parts)
        self.disk = bytearray(total * S)
        for i, (name, a, b) in enumerate(self.parts):
            n = (b - a + 1) * S
            self.disk[a * S:(b + 1) * S] = (hashlib.sha256(name.encode()).digest() * (n // 32 + 1))[:n]
        self.disk[:34 * S] = first
        self.disk[(total - 33) * S:] = last if last is not None else back
        self.boot = {'boot0': b'\x5a' * 8192, 'boot1': b'\xa5' * 8192}
        self.files = {}          # /cache/... and /data/local/tmp/... files pushed
        self.log, self.writes, self.pushes, self.streams = [], [], [], []
        self.rebooted = None
        self.corrupt_stream = None   # (offset, byte): what the stream gets wrong
        self.short_stream = False
        self.before = bytes(self.disk)
        self.cid = 'ce0000c0ffee0000000000000000aa00'   # made up

    # -------------------------------------------------------------------- helpers
    def part(self, name):
        for i, (n, a, b) in enumerate(self.parts):
            if n == name:
                return i + 1, a, b
        raise KeyError(name)

    def node_bytes(self, node):
        m = re.fullmatch(r'/dev/block/%sp(\d+)' % self.DISK, node)
        if m:
            _, a, b = self.parts[int(m.group(1)) - 1]
            return bytes(self.disk[a * S:(b + 1) * S])
        m = re.fullmatch(r'/dev/block/%s(boot[01])' % self.DISK, node)
        if m:
            return self.boot[m.group(1)]
        if node == '/dev/block/' + self.DISK:
            return bytes(self.disk)
        if node.startswith(inst.FIRMWARE_DIR + '/') and self.firmware:
            return FIRMWARE.get(node.rsplit('/', 1)[1])
        return self.files.get(node)

    def changed(self):
        """Ranges of the disk that differ from how it started, as (first LBA, last LBA)."""
        out, cur = [], None
        for lba in range(self.total):
            same = self.disk[lba * S:(lba + 1) * S] == self.before[lba * S:(lba + 1) * S]
            if not same and cur is None:
                cur = lba
            if same and cur is not None:
                out.append((cur, lba - 1))
                cur = None
        if cur is not None:
            out.append((cur, self.total - 1))
        return out

    # -------------------------------------------------------------------- adb
    def state(self):
        return 'device'

    def root(self):
        self.log.append('root')
        self.rooted = self.root_ok

    def sh(self, cmd):
        self.log.append(cmd)
        out = self._sh(cmd)
        if out is None:
            raise AssertionError('the installer sent a command the simulated unit does not know: %r' % cmd)
        return out.strip()

    def _sh(self, cmd):
        D = self.DISK
        m = re.fullmatch(r'getprop (\S+)', cmd)
        if m:
            return {'ro.build.fingerprint': self.fingerprint, 'ro.build.type': self.btype}.get(m.group(1), '')
        if cmd == 'id':
            return 'uid=0(root) gid=0(root)' if self.rooted else 'uid=2000(shell) gid=2000(shell)'
        if cmd == '[ -e %s ] && echo YES' % inst.TOUCH_NODE:
            return 'YES' if self.touch else ''
        if cmd == 'ls -d /proc/device-tree/dsi@*/panel*/display-timings/*/hactive 2>/dev/null':
            return '/proc/device-tree/dsi@fe060000/panel@0/display-timings/timing0/hactive' if self.timing else ''
        m = re.fullmatch(r'readlink -f /dev/block/by-name/(\S+)', cmd)
        if m:
            try:
                return '/dev/block/%sp%d' % (D, self.part(m.group(1))[0])
            except KeyError:
                return '/dev/block/by-name/' + m.group(1)
        if cmd == 'cat /sys/class/block/%s/size' % D:
            return str(self.total)
        if cmd == 'cat /sys/class/block/%s/device/cid 2>/dev/null' % D:
            return self.cid + '\n'
        m = re.fullmatch(r'cat /sys/class/block/%sp(\d+)/uevent' % D, cmd)
        if m:
            n = int(m.group(1))
            return 'MAJOR=179\nPARTN=%d\nPARTNAME=%s\n' % (n, self.parts[n - 1][0]) if n <= len(self.parts) else ''
        m = re.fullmatch(r'cat /sys/class/block/%s(boot[01])/size 2>/dev/null' % D, cmd)
        if m:
            return str(len(self.boot[m.group(1)]) // S)
        m = re.fullmatch(r'stat -c %%s %s/(\S+) 2>/dev/null' % re.escape(inst.FIRMWARE_DIR), cmd)
        if m:
            b = FIRMWARE.get(m.group(1)) if self.firmware else None
            return str(len(b)) if b else ''
        if cmd == 'cat /proc/mounts':
            return ('/dev/block/dm-5 /data ext4 rw 0 0\n' +
                    ('/dev/block/by-name/cache /cache ext4 rw,nosuid 0 0\n' if self.cache else ''))
        if cmd == 'df -k /cache':
            return 'Filesystem 1K-blocks Used Available Use%% Mounted on\n/dev/block/by-name/cache 380000 1000 %d 1%% /cache' \
                % (self.cache_free // 1024)
        m = re.fullmatch(r'sha256sum (\S+)( 2>/dev/null)?', cmd)
        if m:
            b = self.node_bytes(m.group(1))
            return '' if b is None else '%s  %s' % (hashlib.sha256(b).hexdigest(), m.group(1))
        m = re.fullmatch(r'cat %s/status 2>/dev/null' % inst.STAGE, cmd)
        if m:
            return (self.files.get(inst.STAGE + '/status') or b'').decode()
        m = re.fullmatch(r'tail -\d %s/install.log 2>/dev/null' % inst.STAGE, cmd)
        if m:
            return (self.files.get(inst.STAGE + '/install.log') or b'').decode()
        if cmd == 'du -sk %s 2>/dev/null' % inst.STAGE:
            n = sum(len(v) for k, v in self.files.items() if k.startswith(inst.STAGE + '/'))
            return '%d\t%s' % (n // 1024, inst.STAGE)
        if cmd == 'rm -rf {0} && mkdir -p {0}/firmware && chmod 700 {0}'.format(inst.STAGE):
            self.writes.append('clear stage')
            self.files = {k: v for k, v in self.files.items() if not k.startswith(inst.STAGE + '/')}
            return ''
        if cmd == 'rm -rf %s/settings' % inst.STAGE:
            self.files = {k: v for k, v in self.files.items() if not k.startswith(inst.STAGE + '/settings/')}
            return ''
        m = re.fullmatch(r'dd if=(\S+) of=/dev/block/%sp(\d+) bs=1048576 conv=fsync 2>/dev/null; sync' % D, cmd)
        if m:
            src, n = self.files[m.group(1)], int(m.group(2))
            _, a, b = self.parts[n - 1]
            assert len(src) <= (b - a + 1) * S
            self.disk[a * S:a * S + len(src)] = src
            self.writes.append('p%d' % n)
            return ''
        m = re.fullmatch(r'sync; echo 3 > /proc/sys/vm/drop_caches; head -c (\d+) (\S+) \| sha256sum', cmd)
        if m:
            return '%s  -' % hashlib.sha256(self.node_bytes(m.group(2))[:int(m.group(1))]).hexdigest()
        m = re.fullmatch(r'rm -f (\S+)', cmd)
        if m:
            self.files.pop(m.group(1), None)
            return ''
        if cmd == 'sync':
            return ''
        return None

    def exec_out_to_file(self, cmd, path):
        self.log.append('exec-out ' + cmd)
        D = self.DISK
        if cmd == 'cat /proc/device-tree/compatible 2>/dev/null':
            data = b''.join(c.encode() + b'\0' for c in self.compat)
        elif re.fullmatch(r"cat '/proc/device-tree/dsi@fe060000/panel@0/display-timings/timing0/(h|v)active'", cmd):
            data = struct.pack('>I', self.timing[0 if '/hactive' in cmd else 1])
        elif cmd == 'dd if=/dev/block/%s bs=512 count=34 2>/dev/null' % D:
            data = bytes(self.disk[:34 * S])
        else:
            m = re.fullmatch(r'dd if=/dev/block/%s bs=512 skip=(\d+) count=33 2>/dev/null' % D, cmd)
            if not m:
                raise AssertionError('unknown exec-out command %r' % cmd)
            a = int(m.group(1))
            data = bytes(self.disk[a * S:(a + 33) * S])
        with open(path, 'wb') as f:
            f.write(data)
        return 0

    def stream(self, cmd):
        self.log.append('stream ' + cmd)
        self.streams.append(cmd)
        m = re.fullmatch(r'dd if=(/dev/block/\S+) bs=\d+ 2>/dev/null', cmd)
        if not m:
            raise AssertionError('unknown stream command %r' % cmd)
        data = bytearray(self.node_bytes(m.group(1)))
        if m.group(1) == '/dev/block/' + self.DISK:
            if self.corrupt_stream:
                at, v = self.corrupt_stream
                data[at] = v
            if self.short_stream:
                data = data[:-S]

        class P:
            stdout = io.BytesIO(bytes(data))

            def wait(self):
                return 0
        return P()

    def pull(self, remote, local):
        self.log.append('pull ' + remote)
        b = self.node_bytes(remote)
        if b is None:
            return False
        with open(local, 'wb') as f:
            f.write(b)
        return True

    def push(self, local, remote, tries=4, ready=None):
        self.log.append('push ' + remote)
        self.pushes.append(remote)
        assert remote.startswith((inst.STAGE + '/', inst.RECOVERY_TMP)), remote
        with open(local, 'rb') as f:
            self.files[remote] = f.read()

    def reboot(self, target=None):
        self.log.append('reboot %s' % target)
        self.rebooted = target


class Scaled(unittest.TestCase):
    """The installer with the sizes scaled to the simulated unit."""

    def setUp(self):
        self.saved = (inst.EMMC_SECTORS, mkgpt.STORE_SECTORS, mkgpt.MIN_DATA_SECTORS, inst.STAGE_MARGIN)
        inst.EMMC_SECTORS = (TOTAL // 2, TOTAL * 2)
        mkgpt.STORE_SECTORS = 8192
        mkgpt.MIN_DATA_SECTORS = 2048
        inst.STAGE_MARGIN = 64 << 10
        self.tmp = tempfile.mkdtemp(prefix='t5-skylight-test-')
        self.installer = boot_image(self.tmp, 'installer', INSTALLER_CMDLINE)
        self.boot = boot_image(self.tmp, 'boot', BOOT_CMDLINE)
        self.store = os.path.join(self.tmp, 'store.img.gz')
        with gzip.open(self.store, 'wb') as f:
            f.write(ext4_image(b'techo5-store'))
        self.backups = os.path.join(self.tmp, 'backups')
        self.work = os.path.join(self.tmp, 'work')

    def tearDown(self):
        inst.EMMC_SECTORS, mkgpt.STORE_SECTORS, mkgpt.MIN_DATA_SECTORS, inst.STAGE_MARGIN = self.saved
        shutil.rmtree(self.tmp, ignore_errors=True)

    def images(self):
        return ['--installer', self.installer, '--boot', self.boot, '--store', self.store]

    def run_installer(self, unit, *args, images=True, name=True):
        inst.SkyAdb = lambda serial, exe='adb': unit
        inst.need = lambda *a, **k: None
        out, err = io.StringIO(), io.StringIO()
        error = None
        argv = ['--serial', 'SIM-1', '--backups', self.backups, '--work', self.work, '--force', '--no-watch']
        argv += (['--name', 'Kitchen'] if name else []) + (self.images() if images else []) + list(args)
        with redirect_stdout(out), redirect_stderr(err):
            try:
                inst.main(argv)
            except Fail as e:
                error = str(e)
        return out.getvalue(), error

    def folder(self):
        return os.path.join(self.backups, 'skylight-SIM-1')

    def assert_nothing_written(self, u):
        self.assertEqual(u.changed(), [])
        self.assertEqual(u.writes, [])
        self.assertEqual(u.pushes, [])
        self.assertIsNone(u.rebooted)


# ------------------------------------------------------------------------------------------ tests

class Geometry(unittest.TestCase):
    """mkgpt.py on stock tables of real 16 GB eMMCs: every LBA from the unit's own table and size."""

    def test_real_sizes(self):
        for total in REAL_TOTALS:
            parts = stock_parts(total, **REAL_SIZES)
            first, last = gpt(total, parts)
            prim, back, geo = mkgpt.make_gpt(first, total, last)
            ud = parts[-1]
            self.assertEqual(geo['store_first'], ud[1], total)
            self.assertEqual(geo['store_last'] - geo['store_first'] + 1, 4194304)
            self.assertEqual(geo['data_first'], geo['store_last'] + 1)
            self.assertEqual(geo['data_last'], ud[2])
            self.assertEqual(geo['backup_lba'], total - 33)
            self.assertEqual(geo['store_first'] % 2048, 0)
            self.assertEqual(geo['data_first'] % 2048, 0)
            # The new table, read back as the UEFI layout says (not with mkgpt.py's own parser).
            hdr = prim[:92]
            self.assertEqual(hdr[:8], b'EFI PART')
            h0 = bytearray(hdr)
            struct.pack_into('<I', h0, 16, 0)
            self.assertEqual(crc(h0), struct.unpack_from('<I', hdr, 16)[0])
            self.assertEqual(struct.unpack_from('<QQ', hdr, 24), (1, total - 1))
            self.assertEqual(crc(prim[S:]), struct.unpack_from('<I', hdr, 88)[0])
            bh = back[32 * S:32 * S + 92]
            self.assertEqual(struct.unpack_from('<QQ', bh, 24), (total - 1, 1))
            self.assertEqual(struct.unpack_from('<Q', bh, 72)[0], total - 33)
            self.assertEqual(back[:32 * S], prim[S:])
            new, old = entries(prim, S), entries(first, 2 * S)
            self.assertEqual(new[:13], old[:13], 'the fixed partitions are untouched')
            self.assertEqual(entry_fields(new[13])[:3], ('techo5-store', ud[1], ud[1] + 4194303))
            self.assertEqual(entry_fields(new[13])[3], entry_fields(old[13])[3], "userdata's unique GUID is kept")
            self.assertEqual(entry_fields(new[14])[:3], ('techo5-data', ud[1] + 4194304, ud[2]))
            self.assertEqual(new[15:], old[15:])
            self.assertEqual(prim[56:72], first[S + 56:S + 72], 'the disk GUID is kept')
            # The new table is TECHO5's, which the stock check refuses: an install cannot run twice.
            with self.assertRaisesRegex(mkgpt.GPTError, 'already holds techo5'):
                mkgpt.stock_layout(bytes(S) + prim, total)

    def test_data_guid_per_unit(self):
        total = REAL_TOTALS[0]
        parts = stock_parts(total, **REAL_SIZES)
        first, last = gpt(total, parts)
        a = mkgpt.make_gpt(first, total, last)[0]
        self.assertEqual(mkgpt.make_gpt(first, total, last)[0], a, 'the same unit, the same table')
        other = gpt(total, parts, disk_guid=hashlib.md5(b'another unit').digest())[0]
        b = mkgpt.make_gpt(other, total)[0]
        guid = lambda p: entry_fields(entries(p, S)[14])[3]  # noqa: E731
        self.assertNotEqual(guid(a), guid(b))
        self.assertNotEqual(guid(a), entry_fields(entries(a, S)[13])[3])

    def test_cli(self):
        tmp = tempfile.mkdtemp(prefix='t5-mkgpt-')
        try:
            total = REAL_TOTALS[2]
            first, _ = gpt(total, stock_parts(total, **REAL_SIZES))
            src = os.path.join(tmp, 'first.img')
            with open(src, 'wb') as f:
                f.write(first + bytes(1 << 20))
            r = subprocess.run([sys.executable, '-I', os.path.join(SKY, 'install', 'mkgpt.py'), src, str(total),
                                os.path.join(tmp, 'p.bin'), os.path.join(tmp, 'b.bin')], stdout=subprocess.PIPE)
            self.assertEqual(r.returncode, 0)
            self.assertEqual(os.path.getsize(os.path.join(tmp, 'p.bin')), 33 * S)
            self.assertIn(b'techo5-data', r.stdout)
        finally:
            shutil.rmtree(tmp)

    def test_refusals(self):
        total = REAL_TOTALS[0]
        parts = stock_parts(total, **REAL_SIZES)
        first, last = gpt(total, parts)
        with self.assertRaisesRegex(mkgpt.GPTError, 'backup is at LBA'):
            mkgpt.make_gpt(first, total + 2048)
        bad = bytearray(first)
        bad[S * 2 + 60] ^= 1
        with self.assertRaisesRegex(mkgpt.GPTError, "entries' CRC"):
            mkgpt.make_gpt(bytes(bad), total)
        renamed = [(('data' if n == 'userdata' else n), a, b) for n, a, b in parts]
        with self.assertRaisesRegex(mkgpt.GPTError, 'not the stock'):
            mkgpt.make_gpt(gpt(total, renamed)[0], total)
        extra = parts + [('extra', total - 64, total - 40)]
        with self.assertRaisesRegex(mkgpt.GPTError, 'not the stock'):
            mkgpt.make_gpt(gpt(total, extra)[0], total)
        small = parts[:-1] + [('userdata', parts[-1][1], parts[-1][1] + 4194304 + 1000)]
        with self.assertRaisesRegex(mkgpt.GPTError, 'need'):
            mkgpt.make_gpt(gpt(total, small)[0], total)
        odd = parts[:-1] + [('userdata', parts[-1][1] + 8, parts[-1][2])]
        with self.assertRaisesRegex(mkgpt.GPTError, '1 MiB boundary'):
            mkgpt.make_gpt(gpt(total, odd)[0], total)
        other_last = gpt(total, parts[:-1] + [('userdata', parts[-1][1], parts[-1][2] - 2048)])[1]
        with self.assertRaisesRegex(mkgpt.GPTError, "backup GPT's entries differ"):
            mkgpt.make_gpt(first, total, other_last)
        with self.assertRaisesRegex(mkgpt.GPTError, 'backup GPT header'):
            mkgpt.make_gpt(first, total, bytes(33 * S))


class Allowlist(unittest.TestCase):
    def test_known(self):
        self.assertEqual(inst.known_firmware(FP), 'D156.V1.3')
        self.assertEqual(inst.known_firmware('rockchip/D156/D156:12/AndroidS/D156.V1.3:userdebug/release-keys'),
                         'D156.V1.3')

    def test_unknown(self):
        for fp in ('rockchip/D156/D156:12/AndroidS/D156.V1.4:userdebug/test-keys',
                   'rockchip/D156/D156:12/AndroidS/D156.V1.3:user/release-keys',
                   'rockchip/D156/D156:13/AndroidT/D156.V1.3:userdebug/test-keys',
                   'rockchip/D156/D156:12/AndroidS/D156.V1.30:userdebug/test-keys',
                   'rockchip/OTHER/D156:12/AndroidS/D156.V1.3:userdebug/test-keys', ''):
            self.assertIsNone(inst.known_firmware(fp), fp)

    def test_one_table(self):
        for prefix, name in inst.KNOWN_FIRMWARE:
            self.assertTrue(prefix.endswith(':userdebug/'), 'only userdebug builds: adb root needs one')
            self.assertTrue(name)


class Preflight(Scaled):
    """Each refusal stops the installer before anything is written, and says why plainly."""

    def refused(self, pattern, **kw):
        u = FakeSkylight(**kw)
        out, err = self.run_installer(u)
        self.assertIsNotNone(err, out)
        self.assertRegex(err, pattern)
        self.assert_nothing_written(u)
        self.assertEqual(u.streams, [], 'no backup read before the checks pass')
        return u, err

    def test_unknown_firmware(self):
        _, err = self.refused("doesn't know", fingerprint='rockchip/D156/D156:12/AndroidS/D156.V1.4:userdebug/test-keys')
        self.assertIn('Nothing was written', err)

    def test_user_build(self):
        self.refused("doesn't know", fingerprint='rockchip/D156/D156:12/AndroidS/D156.V1.3:user/release-keys')
        self.refused('not userdebug', btype='user')

    def test_no_root(self):
        self.refused('adb root did not give a root shell', root_ok=False)

    def test_not_rk3566(self):
        self.refused('not an RK3566', compat=['rockchip,rk3568-evb', 'rockchip,rk3568'])

    def test_no_touch(self):
        self.refused('no touch controller', touch=False)

    def test_other_screen(self):
        self.refused('not 1920x1080', timing=(1280, 800))

    def test_screen_not_in_tree_is_fine(self):
        u = FakeSkylight(timing=None)
        out, err = self.run_installer(u, '--dry-run')
        self.assertIsNone(err, out)
        self.assertIn('timing is not in the device tree', out)

    def test_emmc_size(self):
        inst.EMMC_SECTORS = (TOTAL * 2, TOTAL * 4)
        self.refused('the eMMC is 32 MiB')

    def test_installed_already(self):
        parts = stock_parts(TOTAL)
        ud = parts[-1]
        techo5 = parts[:-1] + [('techo5-store', ud[1], ud[1] + 8191), ('techo5-data', ud[1] + 8192, ud[2])]
        self.refused('TECHO5 is installed', parts=techo5)

    def test_other_table(self):
        parts = stock_parts(TOTAL)
        self.refused('not the stock one', parts=[('custom' if n == 'backup' else n, a, b) for n, a, b in parts])

    def test_backup_table_differs(self):
        parts = stock_parts(TOTAL)
        _, last = gpt(TOTAL, parts[:-1] + [('userdata', parts[-1][1], parts[-1][2] - 4096)])
        self.refused("backup GPT's entries differ", last=last)

    def test_no_vendor_firmware(self):
        self.refused('fw_bcm43456c5_ag.bin is not on this unit', firmware=False)

    def test_no_cache(self):
        self.refused('/cache is not mounted', cache=False)

    def test_cache_too_small(self):
        self.refused('/cache has 0 MiB free', cache_free=100 << 10)

    def test_images_together(self):
        u = FakeSkylight()
        out, err = self.run_installer(u, '--installer', self.installer, images=False)
        self.assertRegex(err, 'all three')
        self.assert_nothing_written(u)

    def test_boot_images_swapped(self):
        u = FakeSkylight()
        out, err = self.run_installer(u, '--installer', self.boot, '--boot', self.boot, '--store', self.store,
                                      images=False)
        self.assertRegex(err, 'not the TECHO5 Skylight installer image')
        self.assert_nothing_written(u)
        out, err = self.run_installer(u, '--installer', self.installer, '--boot', self.installer,
                                      '--store', self.store, images=False)
        self.assertRegex(err, "not TECHO5's Skylight boot image")
        self.assert_nothing_written(u)

    def test_damaged_boot_image(self):
        bad = os.path.join(self.tmp, 'bad.img')
        data = bytearray(open(self.boot, 'rb').read())
        data[2048 + 0x100] ^= 1
        with open(bad, 'wb') as f:
            f.write(data)
        u = FakeSkylight()
        out, err = self.run_installer(u, '--installer', self.installer, '--boot', bad, '--store', self.store,
                                      images=False)
        self.assertRegex(err, 'header id does not match')
        self.assert_nothing_written(u)

    def test_store_label(self):
        other = os.path.join(self.tmp, 'other.img')
        with open(other, 'wb') as f:
            f.write(ext4_image(b'system'))
        u = FakeSkylight()
        out, err = self.run_installer(u, '--installer', self.installer, '--boot', self.boot, '--store', other,
                                      images=False)
        self.assertRegex(err, "not 'techo5-store'")
        self.assert_nothing_written(u)


class DryRun(Scaled):
    def test_writes_nothing(self):
        u = FakeSkylight()
        out, err = self.run_installer(u, '--dry-run')
        self.assertIsNone(err, out)
        self.assert_nothing_written(u)
        self.assertEqual(u.streams, [])
        self.assertFalse(os.path.exists(self.folder()), 'no backup folder in a dry run')
        self.assertIn('would read: adb exec-out "dd if=/dev/block/mmcblk2 bs=4194304"', out)
        self.assertIn('would run: adb push', out)
        self.assertRegex(out, r'would run: adb shell dd if=/data/local/tmp/techo5-installer.img of=/dev/block/mmcblk2p8')
        self.assertIn('would run: adb reboot recovery', out)
        self.assertIn('Nothing was written to the unit.', out)
        for f in inst.FIRMWARE:
            self.assertIn('would read: adb pull %s/%s' % (inst.FIRMWARE_DIR, f), out)
        # Every command the dry run sent reads: none of them writes.
        for c in u.log:
            self.assertNotRegex(c, r'of=/dev|rm -|mkdir|chmod|^push|> /cache|reboot', c)


class Install(Scaled):
    def full(self, *extra):
        u = FakeSkylight()
        out, err = self.run_installer(u, *extra)
        self.assertIsNone(err, out)
        return u, out

    def test_install(self):
        u, out = self.full()
        folder = self.folder()
        # The backup: the whole eMMC as it was, and both boot areas, before anything was written.
        with open(os.path.join(folder, 'emmc.img'), 'rb') as f:
            self.assertEqual(f.read(), u.before)
        self.assertTrue(os.path.exists(os.path.join(folder, 'verified')))
        with open(os.path.join(folder, 'emmc-boot0.img'), 'rb') as f:
            self.assertEqual(f.read(), u.boot['boot0'])
        first_write = next(i for i, c in enumerate(u.log) if c.startswith('push') or 'of=/dev' in c)
        last_read = max(i for i, c in enumerate(u.log) if c.startswith('stream '))
        self.assertLess(last_read, first_write)
        with open(os.path.join(folder, 'partitions.txt')) as f:
            text = f.read()
        self.assertIn('mounted by Android', text)
        self.assertRegex(text, r'p8  recovery .* [0-9a-f]{64}')
        # The firmware: in the backup folder, and staged for this unit.
        for name, data in FIRMWARE.items():
            with open(os.path.join(folder, 'firmware', name), 'rb') as f:
                self.assertEqual(f.read(), data)
            self.assertEqual(u.files[inst.STAGE + '/firmware/' + name], data)
        # Written from Android: only recovery (the installer image) and the staged set.
        self.assertEqual([w for w in u.writes if w.startswith('p')], ['p8'])
        n, a, b = u.part('recovery')
        with open(self.installer, 'rb') as f:
            want = f.read()
        self.assertEqual(bytes(u.disk[a * S:a * S + len(want)]), want)
        self.assertTrue(all(a <= x and y <= b for x, y in u.changed()), u.changed())
        self.assertEqual(u.files[inst.STAGE + '/status'], b'staged\n')
        self.assertEqual(u.rebooted, 'recovery')
        self.assertNotIn(inst.RECOVERY_TMP, u.files)
        staged = sorted(k[len(inst.STAGE) + 1:] for k in u.files if k.startswith(inst.STAGE + '/'))
        self.assertEqual(staged, sorted(['install.conf', 'gpt-primary.bin', 'gpt-backup.bin', 'store.img.gz', 'boot.img',
                                         'SHA256SUMS', 'installer.img', 'status', 'settings/name', 'settings/psk']
                                        + ['firmware/' + f for f in FIRMWARE]))
        conf = u.files[inst.STAGE + '/install.conf'].decode()
        ud = u.part('userdata')
        self.assertIn('TOTAL=%d\n' % TOTAL, conf)
        self.assertIn('STORE_FIRST=%d\n' % ud[1], conf)
        self.assertIn('DATA_LAST=%d\n' % ud[2], conf)
        self.assertIn('BACKUP_LBA=%d\n' % (TOTAL - 33), conf)
        self.assertEqual(u.files[inst.STAGE + '/settings/name'], b'Kitchen\n')
        key = open(os.path.join(folder, 'home-assistant.key')).read().strip()
        self.assertEqual(u.files[inst.STAGE + '/settings/psk'], (key + '\n').encode())
        self.assertIn(key, out)
        # The way back, with this unit's numbers.
        with open(os.path.join(folder, 'restore', 'RESTORE.txt')) as f:
            restore = f.read()
        self.assertIn('rkdeveloptool wl %d restore/recovery.img' % a, restore)
        self.assertIn('rkdeveloptool wl %d restore/misc.img' % u.part('misc')[1], restore)
        with open(os.path.join(folder, 'restore', 'recovery.img'), 'rb') as f:
            self.assertEqual(f.read(), u.before[a * S:(b + 1) * S])

        # Again, from the start: the backup is there and verified, so it is not read again.
        u2 = FakeSkylight()
        out, err = self.run_installer(u2)
        self.assertIsNone(err, out)
        self.assertEqual(u2.streams, [])
        self.assertIn('verified backup of this unit is there already', out)

    def test_wifi(self):
        pw = os.path.join(self.tmp, 'pw')
        with open(pw, 'w') as f:
            f.write('correct horse battery\n')
        u, out = self.full('--wifi', 'Home', '--wifi-passphrase-file', pw)
        conf = u.files[inst.STAGE + '/settings/wpa_supplicant.conf'].decode()
        self.assertIn('ssid=%s' % b'Home'.hex(), conf)
        self.assertNotIn('correct horse', conf)

    def test_firmware_never_in_release_paths(self):
        u, _ = self.full()
        markers = [d[:16] for d in FIRMWARE.values()]
        for root in (self.work, os.path.dirname(self.installer)):
            for dirpath, _, names in os.walk(root):
                if os.path.abspath(dirpath).startswith(os.path.abspath(self.backups)):
                    continue
                for n in names:
                    with open(os.path.join(dirpath, n), 'rb') as f:
                        data = f.read()
                    for mk in markers:
                        self.assertNotIn(mk, data, os.path.join(dirpath, n))
        # Not inside the store image either: the firmware is staged beside it, never in it.
        self.assertNotIn(markers[0], gzip.decompress(u.files[inst.STAGE + '/store.img.gz']))
        self.assertNotIn(markers[0], u.files[inst.STAGE + '/boot.img'])
        self.assertNotIn(markers[0], u.files[inst.STAGE + '/installer.img'])

    def test_backups_inside_work_refused(self):
        u = FakeSkylight()
        out, err = self.run_installer(u, '--backups', os.path.join(self.work, 'b'))
        self.assertRegex(err, 'inside the work folder')
        self.assert_nothing_written(u)
        self.assertFalse(os.path.exists(os.path.join(self.work, 'b')))

    def test_backups_in_repo_refused(self):
        u = FakeSkylight()
        out, err = self.run_installer(u, '--backups', os.path.join(HERE, 'linux'))
        self.assertRegex(err, 'never be committed')
        self.assert_nothing_written(u)

    def test_resume(self):
        u, _ = self.full()
        # The installer stopped before writing and the unit came back to Android: it removed the
        # settings and left its status.
        u.files = {k: v for k, v in u.files.items() if '/settings/' not in k}
        u.files[inst.STAGE + '/status'] = b'failed 2\n'
        u.files[inst.STAGE + '/install.log'] = b'install: STOPPED: the set does not match its SHA256SUMS\n'
        u.pushes, u.streams, u.rebooted, u.writes = [], [], None, []
        out, err = self.run_installer(u, '--resume')
        self.assertIsNone(err, out)
        self.assertIn('failed 2', out)
        self.assertIn('STOPPED', out)
        self.assertEqual(u.streams, [])
        self.assertNotIn('clear stage', u.writes)
        self.assertIn(inst.STAGE + '/settings/psk', u.pushes)
        self.assertNotIn(inst.STAGE + '/store.img.gz', u.pushes)
        self.assertEqual(u.files[inst.STAGE + '/status'], b'staged\n')
        self.assertEqual(u.rebooted, 'recovery')

    def test_resume_other_set(self):
        u, _ = self.full()
        u.files[inst.STAGE + '/store.img.gz'] = b'something else'
        u.pushes, u.rebooted = [], None
        out, err = self.run_installer(u, '--resume')
        self.assertRegex(err, 'store.img.gz is not what this run would stage')
        self.assertIsNone(u.rebooted)

    def test_resume_nothing_staged(self):
        u = FakeSkylight()
        out, err = self.run_installer(u, '--resume')
        self.assertRegex(err, 'nothing is staged')
        self.assert_nothing_written(u)

    def test_recovery_readback_mismatch(self):
        u = FakeSkylight()
        real = u._sh

        def lying(cmd):
            if cmd.startswith('sync; echo 3 > /proc/sys/vm/drop_caches; head -c'):
                return '0' * 64 + '  -'
            return real(cmd)
        u._sh = lying
        out, err = self.run_installer(u)
        self.assertRegex(err, 'recovery did not read back')
        self.assertIsNone(u.rebooted)
        self.assertNotIn(inst.STAGE + '/status', u.files)


class Backup(Scaled):
    def test_stream_differs_from_the_unit(self):
        u = FakeSkylight()
        _, a, _ = u.part('boot')
        u.corrupt_stream = (a * S + 100, 0x00)
        out, err = self.run_installer(u)
        self.assertRegex(err, r'backup of p7 \(boot\) is not what the unit reads')
        self.assert_nothing_written(u)
        self.assertFalse(os.path.exists(os.path.join(self.folder(), 'verified')))
        # Run again: there is no verified backup, so it is read again.
        u2 = FakeSkylight()
        out, err = self.run_installer(u2)
        self.assertIsNone(err, out)
        self.assertEqual(len([s for s in u2.streams if s.startswith('dd if=/dev/block/mmcblk2 ')]), 1)

    def test_live_partitions_not_compared(self):
        u = FakeSkylight()
        _, a, _ = u.part('userdata')
        u.corrupt_stream = (a * S + 100, 0x00)   # Android wrote to userdata during the read
        out, err = self.run_installer(u)
        self.assertIsNone(err, out)

    def test_short_stream(self):
        u = FakeSkylight()
        u.short_stream = True
        out, err = self.run_installer(u)
        self.assertRegex(err, 'the backup is not complete')
        self.assert_nothing_written(u)
        self.assertFalse(os.path.exists(os.path.join(self.folder(), 'emmc.img')))

    def test_existing_backup(self):
        u = FakeSkylight()
        self.assertIsNone(self.run_installer(u)[1])
        elsewhere = os.path.join(self.tmp, 'kept')
        shutil.move(self.folder(), elsewhere)
        u2 = FakeSkylight()
        out, err = self.run_installer(u2, '--backup', elsewhere)
        self.assertIsNone(err, out)
        self.assertEqual(u2.streams, [])
        self.assertIn('checks out', out)

    def test_damaged_backup_refused(self):
        u = FakeSkylight()
        self.assertIsNone(self.run_installer(u)[1])
        img = os.path.join(self.folder(), 'emmc.img')
        with open(img, 'r+b') as f:
            f.seek(40 * S)
            f.write(b'X')
        u2 = FakeSkylight()
        out, err = self.run_installer(u2, '--backup', self.folder())
        self.assertRegex(err, 'does not match its sha256')
        self.assert_nothing_written(u2)

    def test_backup_of_another_table_refused(self):
        u = FakeSkylight()
        self.assertIsNone(self.run_installer(u)[1])
        parts = stock_parts(TOTAL)
        other = FakeSkylight(parts=parts[:-1] + [('userdata', parts[-1][1], parts[-1][2] - 2048)],
                             last=gpt(TOTAL, parts[:-1] + [('userdata', parts[-1][1], parts[-1][2] - 2048)])[1])
        out, err = self.run_installer(other, '--backup', self.folder())
        self.assertRegex(err, 'another partition table')
        self.assert_nothing_written(other)

    def test_verified_needs_marker(self):
        u = FakeSkylight()
        self.assertIsNone(self.run_installer(u)[1])
        os.remove(os.path.join(self.folder(), 'verified'))
        self.assertIn('no verified backup', inst.backup_verified(self.folder(), TOTAL, u.before[:34 * S], u.cid))

    def test_backup_of_another_chip_refused(self):
        u = FakeSkylight()
        self.assertIsNone(self.run_installer(u)[1])
        with open(os.path.join(self.folder(), 'verified')) as f:
            self.assertIn('cid=' + u.cid, f.read())
        self.assertEqual(inst.backup_verified(self.folder(), TOTAL, u.before[:34 * S], u.cid), '')
        # Another unit with the same table and size, given this one's backup.
        v = FakeSkylight()
        v.cid = '11' * 16
        out, err = self.run_installer(v, '--backup', self.folder())
        self.assertIn('of another unit', err)
        self.assert_nothing_written(v)

    def test_backup_without_cid_refused(self):
        u = FakeSkylight()
        self.assertIsNone(self.run_installer(u)[1])
        marker = os.path.join(self.folder(), 'verified')
        with open(marker) as f:
            old = re.sub(r' cid=\S+', '', f.read())
        with open(marker, 'w', newline='\n') as f:
            f.write(old)
        v = FakeSkylight()
        out, err = self.run_installer(v, '--backup', self.folder())
        self.assertIn('does not say which eMMC chip', err)
        self.assert_nothing_written(v)
        # Found in its usual place, it is not backed up over either.
        v = FakeSkylight()
        out, err = self.run_installer(v)
        self.assertIn('that backup is left as it is', err)
        self.assertEqual(v.streams, [])
        self.assert_nothing_written(v)
        with open(marker) as f:
            self.assertEqual(f.read(), old)

    def test_unreadable_cid(self):
        u = FakeSkylight()
        u.cid = ''
        out, err = self.run_installer(u)
        self.assertIn('could not read the CID', err)
        self.assert_nothing_written(u)


class Watch(unittest.TestCase):
    def timeline(self, steps):
        """steps: [(adb devices, consoles)] one per poll."""
        it = iter(steps)
        state = {'now': 0, 'cur': steps[0]}

        def devices(exe):
            state['cur'] = next(it, steps[-1])
            return state['cur'][0]

        def consoles(ids):
            return state['cur'][1]

        def sleep(n):
            state['now'] += n
        return dict(sleep=sleep, clock=lambda: state['now'], devices=devices, consoles=consoles)

    def test_techo5(self):
        t = self.timeline([([('S', 'device', '')], []), ([], []), ([], []), ([], [('COM5', 'techo5')])])
        with redirect_stdout(io.StringIO()):
            self.assertEqual(inst.watch('S', 'adb', None, **t), 'techo5')

    def test_back_in_android(self):
        t = self.timeline([([('S', 'device', '')], []), ([], []), ([('S', 'device', '')], [])])
        with redirect_stdout(io.StringIO()):
            self.assertEqual(inst.watch('S', 'adb', None, **t), 'android')

    def test_installer_console(self):
        t = self.timeline([([], []), ([], [('COM7', 'techo5-installer')])])
        with redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(Fail, 'stopped partway'):
                inst.watch('S', 'adb', None, **t)

    def test_nothing(self):
        t = self.timeline([([], [])])
        with redirect_stdout(io.StringIO()):
            self.assertEqual(inst.watch('S', 'adb', None, minutes=1, **t), 'unknown')

    def test_other_unit_console(self):
        # Another TECHO5 unit on this computer: its console was there before the reboot.
        other = ('COM3', 'techo5')
        t = self.timeline([([('S', 'device', '')], [other]), ([], [other])])
        with redirect_stdout(io.StringIO()):
            self.assertEqual(inst.watch('S', 'adb', None, minutes=1, known=[other], **t), 'unknown')
        t = self.timeline([([('S', 'device', '')], [other]), ([], [other]), ([], [other, ('COM5', 'techo5')])])
        with redirect_stdout(io.StringIO()):
            self.assertEqual(inst.watch('S', 'adb', None, known=[other], **t), 'techo5')


class WatchAfterInstall(Scaled):
    """The whole run, watched: the consoles are listed before `adb reboot recovery`."""

    def run_watched(self, unit, appears):
        other = ('COM3', 'techo5')
        saved = (inst.list_consoles, inst.adb_devices, inst.watch)
        self.addCleanup(lambda: setattr(inst, 'list_consoles', saved[0]) or setattr(inst, 'adb_devices', saved[1])
                        or setattr(inst, 'watch', saved[2]))
        clock = {'now': 0}

        def consoles(ids):
            return [other] + ([('COM5', 'techo5')] if appears and unit.rebooted and clock['now'] >= 20 else [])

        def sleep(n):
            clock['now'] += n
        inst.SkyAdb = lambda serial, exe='adb': unit
        inst.need = lambda *a, **k: None
        inst.list_consoles = consoles
        inst.adb_devices = lambda exe: [] if unit.rebooted else [('SIM-1', 'device', '')]
        real = saved[2]
        inst.watch = lambda *a, **k: real(*a, minutes=1, sleep=sleep, clock=lambda: clock['now'], **k)
        argv = ['--serial', 'SIM-1', '--backups', self.backups, '--work', self.work, '--force', '--name', 'Kitchen']
        out = io.StringIO()
        with redirect_stdout(out), redirect_stderr(io.StringIO()):
            inst.main(argv + self.images())
        return out.getvalue()

    def test_other_unit_is_not_this_one(self):
        out = self.run_watched(FakeSkylight(), appears=False)
        self.assertNotIn('the install is done', out)
        self.assertIn('no word from the unit', out)

    def test_this_unit(self):
        out = self.run_watched(FakeSkylight(), appears=True)
        self.assertIn('the install is done', out)


# ------------------------------------------------------------------------------------------ install.sh

def find_bash():
    if os.name != 'nt':
        return shutil.which('bash')
    git = shutil.which('git')
    if git:
        base = os.path.dirname(os.path.dirname(os.path.realpath(git)))
        for rel in ('bin/bash.exe', 'usr/bin/bash.exe', '../bin/bash.exe'):
            p = os.path.normpath(os.path.join(base, rel))
            if os.path.exists(p):
                return p
    return None


BASH = find_bash()

# blockdev --rereadpt on the simulated unit: sysfs made again from the table on the disk file.
SYSFS = r'''
import os, struct, sys
root, disk = sys.argv[1], sys.argv[2]
d = open(os.path.join(root, 'dev', disk), 'rb').read(34 * 512)
cls = os.path.join(root, 'sys', 'class', 'block')
for n in os.listdir(cls):
    if n.startswith(disk + 'p') and n[len(disk) + 1:] not in ('4', '8'):
        for f in os.listdir(os.path.join(cls, n)):
            os.remove(os.path.join(cls, n, f))
        os.rmdir(os.path.join(cls, n))
for i in range(128):
    e = d[1024 + i * 128:1024 + (i + 1) * 128]
    if e[:16] == bytes(16):
        continue
    a, b = struct.unpack_from('<QQ', e, 32)
    name = e[56:128].decode('utf-16le').split('\0')[0]
    p = os.path.join(cls, '%sp%d' % (disk, i + 1))
    os.makedirs(p, exist_ok=True)
    for f, v in (('uevent', 'MAJOR=179\nPARTN=%d\nPARTNAME=%s\n' % (i + 1, name)), ('start', '%d\n' % a), ('size', '%d\n' % (b - a + 1))):
        with open(os.path.join(p, f), 'w', newline='\n') as fh:
            fh.write(v)
'''


@unittest.skipUnless(BASH, 'needs a POSIX shell (Git Bash on Windows)')
class InstallSh(Scaled):
    """install.sh on a simulated eMMC, on the set install-skylight.py staged."""

    def setUp(self):
        super().setUp()
        self.unit = FakeSkylight()
        out, err = self.run_installer(self.unit)
        self.assertIsNone(err, out)
        r = os.path.join(self.tmp, 'root')
        self.root = r
        for d in ('dev', 'sys/class/block', 'proc/sys/vm', 'mnt', 'tmp', 'bin', 'set/firmware', 'set/settings'):
            os.makedirs(os.path.join(r, d), exist_ok=True)
        self.disk = os.path.join(r, 'dev', 'mmcblk0')
        with open(self.disk, 'wb') as f:
            f.write(self.unit.before)   # the eMMC as the installer image finds it: still stock
        # misc and recovery are their own nodes here (the simulated disk does not alias them).
        _, a, b = self.unit.part('misc')
        self.misc_before = b'boot-recovery'.ljust(2048, b'\0') + self.unit.before[a * S + 2048:(b + 1) * S]
        self.misc = os.path.join(r, 'dev', 'mmcblk0p4')
        with open(self.misc, 'wb') as f:
            f.write(self.misc_before)
        _, a, b = self.unit.part('recovery')
        self.recovery = os.path.join(r, 'dev', 'mmcblk0p8')
        with open(self.recovery, 'wb') as f:
            f.write(self.unit.disk[a * S:(b + 1) * S])   # the installer image, written from Android
        os.makedirs(os.path.join(r, 'sys', 'class', 'block', 'mmcblk0'), exist_ok=True)
        with open(os.path.join(r, 'sys', 'class', 'block', 'mmcblk0', 'size'), 'w', newline='\n') as f:
            f.write('%d\n' % TOTAL)
        with open(os.path.join(r, 'proc', 'sys', 'vm', 'drop_caches'), 'w') as f:
            f.write('')
        gen = os.path.join(self.tmp, 'sysfs.py')
        with open(gen, 'w') as f:
            f.write(SYSFS)
        self.run_py([gen, r, 'mmcblk0'])
        # The set, as the installer image's /init copies it: what was staged, and its own techo5-data.
        for k, v in self.unit.files.items():
            if k.startswith(inst.STAGE + '/') and not k.endswith(('/status', '/installer.img')):
                with open(os.path.join(r, 'set', k[len(inst.STAGE) + 1:]), 'wb') as f:
                    f.write(v)
        data = ext4_image(b'techo5-data', 512 << 10, b'DATA!')
        self.data = data
        with open(os.path.join(r, 'set', 'data.img.gz'), 'wb') as f:
            f.write(gzip.compress(data, mtime=0))
        with open(os.path.join(r, 'set', 'data.img.info'), 'w', newline='\n') as f:
            f.write('%d %s\n' % (len(data), hashlib.sha256(data).hexdigest()))
        # The commands only the unit has, or that would act on this computer.
        py = sys.executable.replace('\\', '/')
        stubs = {
            'blockdev': '#!/bin/sh\n[ "$1" = --rereadpt ] && exec "%s" -I "%s" "$INSTALL_ROOT" "$(basename "$2")"\n'
                        % (py, gen.replace('\\', '/')),
            'mount': '#!/bin/sh\nfor a; do last=$a; done\nmkdir -p "$last"\necho "mount $*" >> "$INSTALL_ROOT/mounts.log"\n',
            'umount': '#!/bin/sh\necho "umount $*" >> "$INSTALL_ROOT/mounts.log"\n',
            'mdev': '#!/bin/sh\nexit 0\n',
            # Regular files stand for block devices here, which a plain dd would truncate.
            'dd': '#!/bin/sh\nexec /usr/bin/dd "$@" conv=notrunc\n',
        }
        for name, text in stubs.items():
            with open(os.path.join(r, 'bin', name), 'w', newline='\n') as f:
                f.write(text)

    def run_py(self, args):
        subprocess.run([sys.executable, '-I'] + args, check=True)

    def sh_path(self, p):
        if os.name != 'nt':
            return p
        return subprocess.run([BASH, '-c', 'cygpath -u "$1"', '_', p], stdout=subprocess.PIPE).stdout.decode().strip()

    def install_sh(self, attempt=1):
        r = self.sh_path(self.root)
        cmd = ('export INSTALL_ROOT="%s" INSTALL_ATTEMPT=%d; export PATH="%s/bin:$PATH"; chmod +x "%s"/bin/*; '
               'sh "%s" "%s/set"') % (r, attempt, r, r, self.sh_path(INSTALL_SH), r)
        p = subprocess.run([BASH, '-c', cmd], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        return p.returncode, p.stdout.decode('utf-8', 'replace')

    def read(self, path):
        with open(path, 'rb') as f:
            return f.read()

    def test_install(self):
        rc, out = self.install_sh()
        self.assertEqual(rc, 0, out)
        disk = self.read(self.disk)
        ud = self.unit.part('userdata')
        conf = dict(line.split('=', 1) for line in self.unit.files[inst.STAGE + '/install.conf'].decode().split('\n')
                    if '=' in line)
        sf, df, bl = int(conf['STORE_FIRST']), int(conf['DATA_FIRST']), int(conf['BACKUP_LBA'])
        self.assertEqual((sf, bl), (ud[1], TOTAL - 33))
        store = gzip.decompress(self.unit.files[inst.STAGE + '/store.img.gz'])
        self.assertEqual(disk[S:34 * S], self.unit.files[inst.STAGE + '/gpt-primary.bin'])
        self.assertEqual(disk[bl * S:], self.unit.files[inst.STAGE + '/gpt-backup.bin'])
        self.assertEqual(disk[sf * S:sf * S + len(store)], store)
        self.assertEqual(disk[df * S:df * S + len(self.data)], self.data)
        # Nothing else of the disk changed: not the protective MBR, not the 13 stock partitions, not the
        # rest of the two new partitions.
        before = self.unit.before
        written = [(1, 33), (sf, sf + len(store) // S - 1), (df, df + len(self.data) // S - 1), (bl, TOTAL - 1)]
        for lba in range(TOTAL):
            if not any(a <= lba <= b for a, b in written):
                if disk[lba * S:(lba + 1) * S] != before[lba * S:(lba + 1) * S]:
                    self.fail('LBA %d changed' % lba)
        # recovery: TECHO5's boot image; misc: boot-recovery, the rest of it as it was.
        boot = self.unit.files[inst.STAGE + '/boot.img']
        self.assertEqual(self.read(self.recovery)[:len(boot)], boot)
        misc = self.read(self.misc)
        self.assertEqual(misc[:13], b'boot-recovery')
        self.assertEqual(misc[13:], self.misc_before[13:])
        # The kernel's view after the new table: techo5-store and techo5-data where they were written.
        blk = os.path.join(self.root, 'sys', 'class', 'block')
        self.assertIn(b'PARTNAME=techo5-store', self.read(os.path.join(blk, 'mmcblk0p14', 'uevent')))
        self.assertEqual(self.read(os.path.join(blk, 'mmcblk0p15', 'start')).strip(), str(df).encode())
        # The unit's firmware into the store, under the drivers' names; the settings into techo5-data.
        brcm = os.path.join(self.root, 'mnt', 'store', 'lib', 'firmware', 'brcm')
        self.assertEqual(self.read(os.path.join(brcm, 'brcmfmac43456-sdio.bin')), FIRMWARE['fw_bcm43456c5_ag.bin'])
        self.assertEqual(self.read(os.path.join(brcm, 'brcmfmac43456-sdio.txt')), FIRMWARE['nvram_ap6256.txt'])
        self.assertEqual(self.read(os.path.join(brcm, 'BCM4345C5.hcd')), FIRMWARE['BCM4345C5.hcd'])
        state = os.path.join(self.root, 'mnt', 'data', 'techo5', 'misc', 'techo5')
        self.assertEqual(self.read(os.path.join(state, 'name')), b'Kitchen\n')
        mounts = self.read(os.path.join(self.root, 'mounts.log')).decode()
        self.assertIn('mmcblk0p14', mounts)
        self.assertIn('done: misc says boot-recovery', out)

        # Again on the same set (an interrupted run, power lost): the same bytes, still done.
        rc, out = self.install_sh()
        self.assertEqual(rc, 0, out)
        self.assertIn('already (partly) the new one', out)
        self.assertEqual(self.read(self.disk), disk)

    def test_other_table_stops_before_writing(self):
        with open(self.disk, 'r+b') as f:
            f.seek(2 * S + 100)
            f.write(b'\x01')
        before = self.read(self.disk)
        rc, out = self.install_sh()
        self.assertEqual(rc, 2, out)
        self.assertIn('neither the stock one', out)
        self.assertEqual(self.read(self.disk), before)
        self.assertEqual(self.read(self.misc), self.misc_before)

    def tear_primary(self):
        # Power lost while the new primary table went down: its header new, the entries stock.
        new = self.unit.files[inst.STAGE + '/gpt-primary.bin']
        with open(self.disk, 'r+b') as f:
            f.seek(S)
            f.write(new[:S])

    def test_torn_primary_on_the_second_run(self):
        rc, out = self.install_sh()
        self.assertEqual(rc, 0, out)
        done = self.read(self.disk)
        with open(self.disk, 'wb') as f:
            f.write(self.unit.before)
        self.tear_primary()
        rc, out = self.install_sh(attempt=2)
        self.assertEqual(rc, 0, out)
        self.assertIn('cut off writing it', out)
        self.assertEqual(self.read(self.disk), done)

    def test_torn_primary_on_a_first_run_writes_nothing(self):
        self.tear_primary()
        before = self.read(self.disk)
        rc, out = self.install_sh()
        self.assertEqual(rc, 2, out)
        self.assertIn('neither the stock one', out)
        self.assertEqual(self.read(self.disk), before)
        self.assertEqual(self.read(self.misc), self.misc_before)

    def test_damaged_set_stops_before_writing(self):
        p = os.path.join(self.root, 'set', 'store.img.gz')
        with open(p, 'ab') as f:
            f.write(b'x')
        rc, out = self.install_sh()
        self.assertEqual(rc, 2, out)
        self.assertIn('does not match its SHA256SUMS', out)
        self.assertEqual(self.read(self.disk), self.unit.before)

    def test_missing_firmware_stops_before_writing(self):
        os.remove(os.path.join(self.root, 'set', 'firmware', 'BCM4345C5.hcd'))
        rc, out = self.install_sh()
        self.assertEqual(rc, 2, out)
        self.assertIn('BCM4345C5.hcd is missing', out)
        self.assertEqual(self.read(self.disk), self.unit.before)

    def test_other_disk_size_stops_before_writing(self):
        with open(os.path.join(self.root, 'sys', 'class', 'block', 'mmcblk0', 'size'), 'w', newline='\n') as f:
            f.write('%d\n' % (TOTAL + 2048))
        rc, out = self.install_sh()
        self.assertEqual(rc, 2, out)
        self.assertEqual(self.read(self.disk), self.unit.before)

    def test_conf_injection_refused(self):
        p = os.path.join(self.root, 'set', 'install.conf')
        with open(p, 'ab') as f:
            f.write(b'TOTAL=1; rm -rf /\n')
        # SHA256SUMS would catch it first; make the sums agree so the conf check itself is what stops it.
        sums = os.path.join(self.root, 'set', 'SHA256SUMS')
        lines = [ln for ln in self.read(sums).decode().split('\n') if ln and not ln.endswith('  install.conf')]
        lines.append('%s  install.conf' % hashlib.sha256(self.read(p)).hexdigest())
        with open(sums, 'w', newline='\n') as f:
            f.write('\n'.join(lines) + '\n')
        rc, out = self.install_sh()
        self.assertEqual(rc, 2, out)
        self.assertIn('holds lines it should not', out)


# ------------------------------------------------------------------------------------------ /init

@unittest.skipUnless(BASH, 'needs a POSIX shell (Git Bash on Windows)')
class KernelRollback(unittest.TestCase):
    """The store /init's kernel_rollback, taken out of the file and run on a scratch recovery and
    kernel state directory: a kernel on trial that never reached a slot is put back from there."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp, True)
        with open(os.path.join(SKY, 'init'), newline='') as f:
            text = f.read()
        m = re.search(r'^kernel_rollback\(\) \{\n.*?^\}\n', text, re.S | re.M)
        self.assertIsNotNone(m)
        self.fn = m.group(0)
        self.kdir = os.path.join(self.tmp, 'kernel')
        os.makedirs(self.kdir)
        self.rec = os.path.join(self.tmp, 'recovery')
        self.prev = b'ANDROID!' + b'previous kernel' * 300
        self.new = b'ANDROID!' + b'kernel on trial' * 500
        with open(self.rec, 'wb') as f:
            f.write(self.new + bytes(4096 * 8 - len(self.new)))
        self.put('prev.img', self.prev)
        self.put('prev.sha256', hashlib.sha256(self.prev).hexdigest() + '\n')
        self.shipped = 'ab' * 32
        self.put('state', 'trial 0 %s %d %s\n' % (hashlib.sha256(self.new).hexdigest(), len(self.new), self.shipped))

    def put(self, name, data):
        with open(os.path.join(self.kdir, name), 'wb') as f:
            f.write(data.encode() if isinstance(data, str) else data)

    def get(self, name):
        p = os.path.join(self.kdir, name)
        if not os.path.exists(p):
            return None
        with open(p, 'rb') as f:
            return f.read().decode()

    def rollback(self):
        sp = InstallSh.sh_path
        script = ('BB=; crumb() { echo "crumb: $*"; }; part() { echo "%s"; }; dd() { command dd "$@" conv=notrunc; }\n'
                  '%sKDIR="%s"\nkernel_rollback; echo "rc=$?"\n') % (sp(self, self.rec), self.fn, sp(self, self.kdir))
        p = subprocess.run([BASH, '-c', script], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        out = p.stdout.decode('utf-8', 'replace')
        return out.strip().split('rc=')[-1], out

    def recovery(self):
        with open(self.rec, 'rb') as f:
            return f.read()

    def test_put_back(self):
        rc, out = self.rollback()
        self.assertEqual(rc, '0', out)
        self.assertEqual(self.recovery()[:len(self.prev)], self.prev)
        self.assertEqual(len(self.recovery()), 4096 * 8)
        self.assertIsNone(self.get('state'))
        self.assertEqual(self.get('bad'), self.shipped + '\n')

    def test_no_trial(self):
        self.put('state', 'good %s %d %s\n' % (hashlib.sha256(self.new).hexdigest(), len(self.new), self.shipped))
        rc, out = self.rollback()
        self.assertEqual(rc, '1', out)
        self.assertEqual(self.recovery()[:len(self.new)], self.new)

    def test_recovery_not_the_trial(self):
        with open(self.rec, 'r+b') as f:
            f.write(b'ANDROID?')
        rc, out = self.rollback()
        self.assertEqual(rc, '1', out)
        self.assertIn('trial dropped', out)
        self.assertIsNone(self.get('state'))
        self.assertIsNone(self.get('bad'))

    def test_prev_damaged(self):
        self.put('prev.img', self.prev[:-1] + b'x')
        before = self.recovery()
        rc, out = self.rollback()
        self.assertEqual(rc, '1', out)
        self.assertIn('does not match its sum', out)
        self.assertEqual(self.recovery(), before)
        self.assertEqual(self.get('state'), 'failed %s\n' % self.shipped)

    def test_prev_not_a_boot_image(self):
        prev = b'NOTABOOT' + self.prev[8:]
        self.put('prev.img', prev)
        self.put('prev.sha256', hashlib.sha256(prev).hexdigest() + '\n')
        before = self.recovery()
        rc, out = self.rollback()
        self.assertEqual(rc, '1', out)
        self.assertEqual(self.recovery(), before)
        self.assertEqual(self.get('state'), 'failed %s\n' % self.shipped)


if __name__ == '__main__':
    unittest.main(verbosity=2)
