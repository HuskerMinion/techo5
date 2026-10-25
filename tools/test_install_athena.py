#!/usr/bin/env python3
"""Offline tests for install-athena.py: no unit, no network.

    python3 tools/test_install_athena.py

The installer runs against a simulated athena in TWRP: its shell commands run in a real POSIX shell
(Git Bash on Windows) with /dev, /sys, /proc, /tmp and /data moved into a scratch folder, so the
partitions are small files there. Stock boot images for those runs are made up here, with made-up amp
tables.

Fire OS's real boot images are not in the repository. To check the extraction on them too, name them
(and optionally the mainline blob to compare table B with) in the environment:

    TECHO5_STOCK_BOOTS=<p20-boot.img>[<os.pathsep><another>]  TECHO5_DSP_REF=<tas5805m_dsp_athena.bin>

They are read in place; nothing is copied.
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
from contextlib import redirect_stdout, redirect_stderr

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('install_athena', os.path.join(HERE, 'install-athena.py'))
inst = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inst)
Fail = sys.modules['techo5lib'].Fail

PAGE = 2048


# ------------------------------------------------------------------------------------------ made-up images

def ref_id(kernel, ramdisk, second, hv):
    """The header id as TECHO5's boot image packer computes it (kernel, ramdisk, second, each followed by
    its size; then a zero size for header version 1 and later), written out separately from the
    installer's own code."""
    h = hashlib.sha1()
    for blob in (kernel, ramdisk, second):
        h.update(blob)
        h.update(struct.pack('<I', len(blob)))
    if hv > 0:
        h.update(struct.pack('<I', 0))
    return h.digest().ljust(32, b'\0')


def boot_image(kernel, ramdisk=b'', hv=1, kernel_addr=0x40200000, set_id=True):
    pad = lambda b: b + b'\0' * (-len(b) % PAGE)  # noqa: E731
    hdr = bytearray(PAGE)
    hdr[:8] = b'ANDROID!'
    struct.pack_into('<10I', hdr, 8, len(kernel), kernel_addr, len(ramdisk), 0x45000000, 0, 0, 0x44000000,
                     PAGE, hv, 0)
    hdr[64:64 + 7] = b'bootopt'
    if hv >= 1:
        struct.pack_into('<IQI', hdr, 1632, 0, 0, 1648)
    if set_id:
        hdr[576:608] = ref_id(kernel, ramdisk, b'', hv)
    return bytes(hdr) + pad(kernel) + (pad(ramdisk) if ramdisk else b'')


def empty_slot():
    return inst.SLOT_MAGIC + b'\0' * (inst.SLOT_SIZE - len(inst.SLOT_MAGIC))


def techo5_boot(slots=1, n=0):
    """A made-up TECHO5 mainline boot image: an 'Image.gz' with the empty tuning slot, then a DTB."""
    slot = bytearray(empty_slot())
    struct.pack_into('<I', slot, 8, n)
    kernel = b'\x1f\x8b\x08\x00' + b'\x5a' * 3000 + bytes(slot) * slots + fdt({'compatible': 'amazon,athena'}) + b'\x33' * 100
    return boot_image(kernel)


def fdt(tree):
    """A flattened device tree from {'prop': value, 'node@1': {...}}; values are str, int or bytes."""
    strings = bytearray()
    names = {}
    body = bytearray()

    def name_off(n):
        if n not in names:
            names[n] = len(strings)
            strings.extend(n.encode() + b'\0')
        return names[n]

    def node(name, d):
        body.extend(struct.pack('>I', 1) + name.encode() + b'\0')
        body.extend(b'\0' * (-len(body) % 4))
        for k, v in d.items():
            if isinstance(v, dict):
                continue
            if isinstance(v, int):
                v = struct.pack('>I', v)
            elif isinstance(v, str):
                v = v.encode() + b'\0'
            elif isinstance(v, list):
                v = b''.join(s.encode() + b'\0' for s in v)
            body.extend(struct.pack('>III', 3, len(v), name_off(k)) + v)
            body.extend(b'\0' * (-len(body) % 4))
        for k, v in d.items():
            if isinstance(v, dict):
                node(k, v)
        body.extend(struct.pack('>I', 2))

    node('', tree)
    body.extend(struct.pack('>I', 9))
    off_rsv = 40
    off_struct = off_rsv + 16
    off_strings = off_struct + len(body)
    total = off_strings + len(strings)
    head = struct.pack('>10I', 0xd00dfeed, total, off_struct, off_strings, off_rsv, 17, 16, 0, len(strings), len(body))
    return head + b'\0' * 16 + bytes(body) + bytes(strings)


def athena_dtb(compat, cfg, status='okay'):
    amp = {'compatible': 'ti,tas5805m', 'reg': 0x2d, 'status': status}
    if cfg is not None:
        amp['speaker-config'] = cfg
    return fdt({'model': 'MT8183', 'compatible': compat, 'i2c@11008000': {'tas5805m@2d': amp}})


PREBOOT = [(0, 0), (0x7f, 0), (3, 2), (0xfe, 2), (1, 0x11), (0xfe, 20), (0, 0), (0x7f, 0), (3, 2), (0xfe, 2),
           (3, 0), (0xfe, 20), (0, 0), (0x7f, 0), (0x46, 1), (0, 0), (0x7f, 0), (3, 2), (0xfe, 2), (2, 2)]


def made_up_table(seed, n=300):
    t = [(0, 0), (0x7f, 0), (0x78, 0x80), (0x54, seed)]
    t += [((i * 7 + seed) % 0x7e + 1, (i * 13 + seed) & 0xff) for i in range(n)]
    t += [(0xfe, 5), (3, 3), (0x78, 0x80)]
    return t


TABLE_A, TABLE_B = made_up_table(0x0a), made_up_table(0x13)


def entries_bytes(t):
    return b''.join(struct.pack('<3I', r, v, 0) for r, v in t)


def stock_boot(cfgs=((['amzn,athena-pvt', 'amzn,athena-dvt', 'amzn,athena'], 1),
                     (['amzn,athena-evt', 'amzn,athena'], 1),
                     (['amzn,theia-proto', 'amzn,theia'], 0)),
               tables=(TABLE_A, TABLE_B)):
    """A made-up Fire OS boot image: a 32-bit zImage-like kernel with a gzip payload holding the tables
    as Amazon's kernel lays them out (A, preboot, B, preboot), then one DTB per board revision."""
    raw = b'\xff' * (1 << 20) + b''.join(entries_bytes(t) + entries_bytes(PREBOOT) for t in tables) + b'\xff' * 4096
    z = io.BytesIO()
    with gzip.GzipFile(fileobj=z, mode='wb', mtime=0) as g:
        g.write(raw)
    head = bytearray(b'\x00\x00\xa0\xe1' * 9 + b'\x18\x28\x6f\x01' + b'\0' * 64)
    kernel = bytes(head) + b'\x11' * 500 + z.getvalue() + b''.join(athena_dtb(c, v) for c, v in cfgs)
    return boot_image(kernel, kernel_addr=0x40008000)


def known_for_made_up():
    return {hashlib.sha256(inst.mainline_table(TABLE_A)).hexdigest(): ('A', 'made-up A'),
            hashlib.sha256(inst.mainline_table(TABLE_B)).hexdigest(): ('B', 'made-up B')}


def store_image(size=1 << 20):
    img = bytearray(size)
    img[1024 + 56:1024 + 58] = inst.EXT4_MAGIC
    img[1024 + 120:1024 + 120 + len(inst.STORE_LABEL)] = inst.STORE_LABEL
    img[4096:4096 + 16] = b'TECHO5 STORE....'
    return bytes(img)


# ------------------------------------------------------------------------------------------ simulated TWRP

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

# The partition table's own names, as unit 1 reported them in TWRP (2026-10-15). TWRP's by-name links
# call 3, 4, 18 and 19 tee1_real, tee2_real, lk_real and lk2_real instead; the installer accepts both.
LAYOUT = {1: 'kb', 2: 'dkb', 3: 'tee1', 4: 'tee2', 5: 'persist', 6: 'misc', 7: 'recovery',
          12: 'vendor', 18: 'lk', 19: 'lk2', 20: 'boot', 21: 'system', 22: 'cache', 23: 'userdata'}
SIZES = {20: 2 << 20, 21: 4 << 20, 22: 64 << 10, 23: 64 << 10}


class FakeTWRP:
    """An athena in TWRP: adb's calls, run in a scratch folder that stands for the unit's root."""

    def __init__(self, root, mmc='mmcblk1', cmdline='jd936x_id=0x36', lk_panel='ili9881c_wxga_dsi_vdo_st_boe',
                 boot=None, live_cfg=1, compat=('amzn,athena-pvt', 'amzn,athena-dvt', 'amzn,athena')):
        self.root = root
        self.mmc = mmc
        self.log = []
        self.readies = {}
        self.rebooted = False
        self.sh_root = subprocess.run([BASH, '-c', 'cygpath -u "$1" 2>/dev/null || echo "$1"', '_', root],
                                      stdout=subprocess.PIPE).stdout.decode().strip() if os.name == 'nt' else root
        for d in ('dev/block', 'sys/class/block', 'proc/sys/vm', 'proc/device-tree/chosen', 'tmp', 'data'):
            os.makedirs(os.path.join(root, d), exist_ok=True)
        for n, name in LAYOUT.items():
            size = SIZES.get(n, 16 << 10)
            content = (hashlib.sha256(name.encode()).digest() * (size // 32 + 1))[:size]
            if n == 20:
                content = (boot or b'').ljust(size, b'\0')
            self.put('dev/block/%sp%d' % (mmc, n), content)
            self.put('sys/class/block/%sp%d/uevent' % (mmc, n), 'MAJOR=179\nPARTN=%d\nPARTNAME=%s\n' % (n, name))
            self.put('sys/class/block/%sp%d/size' % (mmc, n), '%d\n' % (size // 512))
        for b in ('boot0', 'boot1'):
            self.put('dev/block/%s%s' % (mmc, b), b'\x5a' * 8192)
            self.put('sys/class/block/%s%s/size' % (mmc, b), '16\n')
        # As the shell sees it: the paths in it are in the scratch folder too.
        self.put('proc/mounts', 'rootfs / rootfs rw 0 0\n%s/dev/block/%sp23 %s/data ext4 rw 0 0\n'
                 % (self.sh_root, mmc, self.sh_root))
        self.put('proc/cmdline', 'console=tty0 androidboot.hardware=mt8183 %s gpt=1\n' % cmdline)
        self.put('proc/device-tree/compatible', b''.join(c.encode() + b'\0' for c in compat))
        self.put('proc/device-tree/chosen/bootargs', b'console=tty0\0')
        self.put('proc/device-tree/chosen/atag,videolfb',
                 b'\x00\x00\x25\x53\x01\x00\x00\x00' + (lk_panel.encode() + b'\0' if lk_panel else b''))
        if live_cfg is not None:
            self.put('proc/device-tree/i2c@11008000/tas5805m@2d/speaker-config', struct.pack('>I', live_cfg))
        self.put('proc/sys/vm/drop_caches', '')
        self.before = self.snapshot()

    def put(self, rel, content):
        p = os.path.join(self.root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, 'wb') as f:
            f.write(content.encode() if isinstance(content, str) else content)

    def get(self, rel):
        with open(os.path.join(self.root, rel), 'rb') as f:
            return f.read()

    def part(self, n):
        return self.get('dev/block/%sp%d' % (self.mmc, n))

    def snapshot(self):
        return {n: hashlib.sha256(self.part(n)).hexdigest() for n in LAYOUT}

    def changed(self):
        now = self.snapshot()
        return sorted(n for n in LAYOUT if now[n] != self.before[n])

    # Paths on the unit, moved into the scratch folder; /dev/null stays.
    PATH_RE = re.compile(r"(?<![\w.~])/(?!dev/null)(dev|sys|proc|tmp|data)(?=/|\s|'|\"|$)")

    def translate(self, cmd):
        return self.PATH_RE.sub(lambda m: self.sh_root + '/' + m.group(1), cmd).replace('[ -b ', '[ -f ')

    def prelude(self):
        return ('id() { echo "uid=0(root) gid=0(root)"; }\n'
                'getprop() { case "$1" in ro.product.device) echo athena;; *) echo;; esac; }\n'
                'dd() { command dd "$@" conv=notrunc; }\n'
                'mount() { echo "mount not simulated" >&2; return 1; }\n'
                'umount() { :; }\n')

    def run(self, cmd, stdout=subprocess.PIPE):
        self.log.append(cmd)
        return subprocess.run([BASH, '-c', self.prelude() + self.translate(cmd)], stdout=stdout,
                              stderr=subprocess.DEVNULL)

    # adb's interface, as install-athena.py uses it
    def state(self):
        return 'recovery'

    def sh(self, cmd):
        out = self.run(cmd).stdout.decode('utf-8', 'replace').replace('\r\n', '\n').strip()
        return out.replace(self.sh_root, '')

    def push(self, local, remote, tries=4, ready=None):
        self.log.append('push %s' % remote)
        self.readies[remote] = ready
        shutil.copyfile(local, os.path.join(self.root, remote.lstrip('/')))

    def exec_out_to_file(self, cmd, path):
        with open(path, 'wb') as f:
            return self.run(cmd, stdout=f).returncode

    def reboot(self, target=None):
        self.log.append('reboot')
        self.rebooted = True

    def writes(self):
        """The partitions written, in order, from the dd commands run."""
        return [int(m.group(1)) for c in self.log for m in [re.search(r'of=/dev/block/mmcblk\dp(\d+)', c)] if m]


def run_installer(unit, args, tmp):
    """install-athena.py's main against the simulated unit. Returns (output, error or None)."""
    inst.Adb = lambda serial, exe='adb': unit
    inst.need = lambda *a, **k: None
    out, err = io.StringIO(), io.StringIO()
    error = None
    with redirect_stdout(out), redirect_stderr(err):
        try:
            inst.main(['--serial', 'SIMULATED', '--backups', os.path.join(tmp, 'backups'),
                       '--work', os.path.join(tmp, 'work'), '--name', 'Kitchen', '--force', '--no-watch'] + args)
        except Fail as e:
            error = str(e)
    return out.getvalue(), error


# ------------------------------------------------------------------------------------------ tests

class SlotAndHeader(unittest.TestCase):
    def test_fill(self):
        shipped = techo5_boot()
        table = inst.mainline_table(TABLE_B)
        filled = inst.fill_slot(shipped, table)
        self.assertEqual(len(filled), len(shipped))
        at = shipped.find(inst.SLOT_MAGIC)
        self.assertEqual(filled[at:at + 8], inst.SLOT_MAGIC)
        self.assertEqual(struct.unpack('<I', filled[at + 8:at + 12])[0], len(table))
        self.assertEqual(filled[at + 12:at + 44], hashlib.sha256(table).digest())
        self.assertEqual(filled[at + 44:at + 44 + len(table)], table)
        self.assertFalse(any(filled[at + 44 + len(table):at + inst.SLOT_SIZE]))
        # Nothing outside the slot and the id changed.
        diff = [i for i in range(len(shipped)) if shipped[i] != filled[i]]
        self.assertTrue(all(576 <= i < 608 or at <= i < at + inst.SLOT_SIZE for i in diff))
        self.assertEqual(inst.read_slot(filled), table)

    def test_header_id_matches_reference(self):
        filled = inst.fill_slot(techo5_boot(), inst.mainline_table(TABLE_A))
        ksize = struct.unpack('<I', filled[8:12])[0]
        kernel = filled[PAGE:PAGE + ksize]
        self.assertEqual(filled[576:608], ref_id(kernel, b'', b'', 1))
        self.assertNotEqual(filled[576:608], techo5_boot()[576:608])
        for hv in (0, 1, 2):
            img = boot_image(b'kernel' * 1000, ramdisk=b'rd' * 300, hv=hv)
            if hv == 2:  # version 2 adds the (here empty) dtb's size
                h = hashlib.sha1()
                for blob in (b'kernel' * 1000, b'rd' * 300, b'', b'', b''):
                    h.update(blob)
                    h.update(struct.pack('<I', len(blob)))
                img = img[:576] + h.digest().ljust(32, b'\0') + img[608:]
            b = inst.BootImage(img)
            self.assertEqual(b.computed_id(), b.stored_id(), 'header version %d' % hv)

    def test_refuses_filled_slot(self):
        with self.assertRaisesRegex(Fail, 'already filled'):
            inst.fill_slot(techo5_boot(n=10), b'\x01\x02')

    def test_refuses_magic_count(self):
        with self.assertRaisesRegex(Fail, '0 amp tuning slots'):
            inst.fill_slot(boot_image(b'\x1f\x8b\x08' + b'\0' * 9000), b'\x01\x02')
        with self.assertRaisesRegex(Fail, '2 amp tuning slots'):
            inst.fill_slot(techo5_boot(slots=2), b'\x01\x02')

    def test_refuses_wrong_id_and_dirty_slot(self):
        bad = bytearray(techo5_boot())
        bad[600] ^= 1
        with self.assertRaisesRegex(Fail, 'header id'):
            inst.fill_slot(bytes(bad), b'\x01\x02')
        dirty = bytearray(techo5_boot())
        at = dirty.find(inst.SLOT_MAGIC)
        dirty[at + 100] = 1
        dirty = boot_image(bytes(dirty[PAGE:PAGE + struct.unpack('<I', dirty[8:12])[0]]))
        with self.assertRaisesRegex(Fail, 'not empty'):
            inst.fill_slot(dirty, b'\x01\x02')

    def test_refuses_oversize_table(self):
        with self.assertRaisesRegex(Fail, 'slot takes'):
            inst.fill_slot(techo5_boot(), b'\x01\x02' * 5000)


class MadeUpStock(unittest.TestCase):
    def setUp(self):
        self.saved = dict(inst.KNOWN_AMP_TABLES)
        inst.KNOWN_AMP_TABLES.update(known_for_made_up())

    def tearDown(self):
        inst.KNOWN_AMP_TABLES.clear()
        inst.KNOWN_AMP_TABLES.update(self.saved)

    def test_extracts_by_structure(self):
        raw = inst.kernel_image(inst.BootImage(stock_boot()).kernel())
        tables = inst.amp_tables(raw)
        self.assertEqual([len(t) for _, t in tables], [len(TABLE_A), len(TABLE_B)])
        blob, letter, _, cfg, _ = inst.stock_amp_tuning(stock_boot(), ['amzn,athena-pvt', 'amzn,athena-dvt', 'amzn,athena'])
        self.assertEqual((letter, cfg), ('B', 1))
        self.assertEqual(blob, inst.mainline_table(TABLE_B))
        self.assertNotIn(b'\xfe\x05', blob[::1])

    def test_table_order_does_not_matter(self):
        blob, letter, _, _, _ = inst.stock_amp_tuning(stock_boot(tables=(TABLE_B, TABLE_A)), None)
        self.assertEqual((letter, blob), ('B', inst.mainline_table(TABLE_B)))

    def test_config_zero_picks_a(self):
        img = stock_boot(cfgs=((['amzn,athena-pvt', 'amzn,athena'], 0),))
        blob, letter, _, cfg, _ = inst.stock_amp_tuning(img, ['amzn,athena-pvt', 'amzn,athena'])
        self.assertEqual((letter, cfg, blob), ('A', 0, inst.mainline_table(TABLE_A)))

    def test_disagreeing_trees(self):
        img = stock_boot(cfgs=((['amzn,athena-pvt', 'amzn,athena'], 0), (['amzn,athena-evt', 'amzn,athena'], 1)))
        with self.assertRaisesRegex(Fail, 'disagree'):
            inst.stock_amp_tuning(img, ['amzn,athena-xyz', 'amzn,athena'])
        _, letter, _, _, _ = inst.stock_amp_tuning(img, ['amzn,athena-evt', 'amzn,athena'])
        self.assertEqual(letter, 'B')

    def test_unknown_table(self):
        inst.KNOWN_AMP_TABLES.clear()
        with self.assertRaisesRegex(Fail, 'not known yet'):
            inst.stock_amp_tuning(stock_boot(), None)

    def test_refuses_techo5_boot_as_stock(self):
        with self.assertRaisesRegex(Fail, "TECHO5's"):
            inst.stock_amp_tuning(techo5_boot(), None)


class Panel(unittest.TestCase):
    def test_boe(self):
        self.assertIn('ILI9881C', inst.check_panel(0x36, 'ili9881c_wxga_dsi_vdo_st_boe', False))
        self.assertIn('ILI9881C', inst.check_panel(0x36, None, False))
        self.assertIn('ILI9881C', inst.check_panel(None, 'ili9881c_wxga_dsi_vdo_st_boe', False))
        # The JD9365 with KD/HSD glass, tested by a user (#187), goes on by itself too.
        self.assertIn('tested on', inst.check_panel(0x31, None, False))
        self.assertIn('JD9365, KD/HSD glass', inst.check_panel(0x31, 'jd936x_wxga_dsi_vdo_athena_st_kd_hsd', False))

    def test_others_refused(self):
        for pid, name in ((0x32, None), (0x34, 'jd936x_wxga_dsi_vdo_athena_st_boe'), (0x38, None),
                          (0x36, 'ili9881c_wxga_dsi_vdo_st_inx'), (None, None)):
            with self.assertRaisesRegex(Fail, 'kernel amazon'):
                inst.check_panel(pid, name, False)
            self.assertIn('--force-panel', inst.check_panel(pid, name, True))

    def test_untested_known(self):
        with self.assertRaisesRegex(Fail, 'JD9365, BOE glass \\(new\\).*not been tested'):
            inst.check_panel(0x35, 'jd936x_wxga_dsi_vdo_athena_st_boe_new', False)
        said = inst.check_panel(0x35, 'jd936x_wxga_dsi_vdo_athena_st_boe_new', True)
        self.assertIn('JD9365, BOE glass (new)', said)
        self.assertIn('--force-panel', said)
        self.assertIn('JD9365, KD/HSD glass (new)', inst.check_panel(0x32, None, True))
        self.assertIn('ILI9881C, KD/HSD glass', inst.check_panel(0x33, 'ili9881c_wxga_dsi_vdo_st_kd_hsd', True))

    def test_which(self):
        boe_new = 'jd936x_wxga_dsi_vdo_athena_st_boe_new'
        inx = 'ili9881c_wxga_dsi_vdo_st_inx'
        self.assertEqual(inst.panel_which(0x35, None), (boe_new, None))
        self.assertEqual(inst.panel_which(None, inx), (inx, None))
        self.assertEqual(inst.panel_which(0x33, None), (None, None), 'nibble 3 is on both controllers')
        which, doubt = inst.panel_which(0x36, inx)
        self.assertEqual(which, inx, 'the kernel goes by the name')
        self.assertIn('disagree', doubt)
        which, doubt = inst.panel_which(0x36, 'some_other_dsi_panel')
        self.assertEqual(which, 'ili9881c_wxga_dsi_vdo_st_boe', 'the kernel falls back to the nibble')
        self.assertIn('does not know', doubt)
        self.assertEqual(inst.panel_which(0x3b, None), (None, None))
        self.assertEqual(inst.panel_which(None, None), (None, None))

    def test_doubt_needs_force(self):
        with self.assertRaisesRegex(Fail, 'disagree'):
            inst.check_panel(0x36, 'ili9881c_wxga_dsi_vdo_st_inx', False)
        with self.assertRaisesRegex(Fail, 'does not know'):
            inst.check_panel(0x36, 'some_other_dsi_panel', False)
        self.assertIn('--force-panel', inst.check_panel(0x36, 'some_other_dsi_panel', True))
        with self.assertRaisesRegex(Fail, '--ssh-key'):
            inst.check_panel(0x35, None, False)

    def test_same_table_as_the_kernel(self):
        src = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                           'linux', 'athena', 'kernel', 'src', 'panel', 'panel-athena-select.c')
        with open(src) as f:
            rows = re.findall(r'ATHENA_(ILI|JD)\("(\w+)", (-1|0x[0-9a-f]+|\d+), "[\w-]+"\)', f.read())
        prefix = {'ILI': 'ili9881c_wxga_dsi_vdo_st_', 'JD': 'jd936x_wxga_dsi_vdo_athena_st_'}
        kernel = {prefix[ic] + name: (None if nib == '-1' else int(nib, 0)) for ic, name, nib in rows}
        self.assertEqual(kernel, {n: nib for n, (nib, _) in inst.PANELS.items()})
        self.assertEqual(rows[0][:2], ('ILI', 'boe'), "the kernel's first row is the device tree's own")


@unittest.skipUnless(BASH, 'needs a POSIX shell (Git Bash on Windows)')
class SimulatedTWRP(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix='t5-athena-test-')
        self.saved = dict(inst.KNOWN_AMP_TABLES)
        inst.KNOWN_AMP_TABLES.update(known_for_made_up())
        self.store = os.path.join(self.tmp, 'system.img')
        with open(self.store, 'wb') as f:
            f.write(store_image())
        self.boot = os.path.join(self.tmp, 'boot.img')
        with open(self.boot, 'wb') as f:
            f.write(techo5_boot())

    def tearDown(self):
        inst.KNOWN_AMP_TABLES.clear()
        inst.KNOWN_AMP_TABLES.update(self.saved)
        shutil.rmtree(self.tmp, ignore_errors=True)

    def unit(self, **kw):
        kw.setdefault('boot', stock_boot())
        return FakeTWRP(os.path.join(self.tmp, 'unit'), **kw)

    def install(self, unit, *extra):
        return run_installer(unit, list(extra), self.tmp)

    def mainline_args(self):
        return ['--image', self.store, '--boot', self.boot]

    def test_store_grown_after_written(self):
        # #192: the store is grown from TWRP once written and read back, before the boot partition
        # and the reboot; a grow that cannot be done says so and leaves it to the first boot.
        u = self.unit()
        out, err = self.install(u, *self.mainline_args())
        self.assertIsNone(err, out)
        system = next(i for i, c in enumerate(u.log) if 'of=/dev/block/mmcblk1p21' in c)
        boot = next(i for i, c in enumerate(u.log) if 'of=/dev/block/mmcblk1p20' in c)
        grow = [i for i, c in enumerate(u.log) if 'resize2fs /dev/block/mmcblk1p21' in c]
        self.assertTrue(grow and system < grow[0] < boot, u.log)
        # The simulated partition holds no real ext4: the grow is not done, and that is said.
        self.assertIn('the store was not grown here', out)
        self.assertTrue(u.rebooted)

    def test_mainline_install(self):
        u = self.unit()
        out, err = self.install(u, *self.mainline_args())
        self.assertIsNone(err, out)
        # Backups first, then system, then boot; nothing else written.
        self.assertEqual(u.writes(), [21, 20])
        first_write = next(i for i, c in enumerate(u.log) if 'of=/dev/block' in c)
        backup_reads = [i for i, c in enumerate(u.log) if re.search(r'dd if=/dev/block/mmcblk1p\d+ bs', c)]
        self.assertTrue(backup_reads and max(backup_reads) < first_write)
        self.assertEqual(u.changed(), [20, 21])
        folder = os.path.join(self.tmp, 'backups', 'SIMULATED', 'partitions')
        saved = sorted(os.listdir(folder))
        self.assertIn('p20-boot.img', saved)
        self.assertIn('p21-system.img', saved)
        self.assertIn('mmcblk1boot0.img', saved)
        self.assertNotIn('p22-cache.img', saved)
        self.assertNotIn('p23-userdata.img', saved)
        with open(os.path.join(folder, 'p20-boot.img'), 'rb') as f:
            self.assertEqual(hashlib.sha256(f.read()).hexdigest(), u.before[20])
        # boot holds the shipped image with the unit's table B in its slot, its id right.
        with open(self.boot, 'rb') as f:
            shipped = f.read()
        boot_now = u.part(20)[:len(shipped)]
        self.assertEqual(boot_now, inst.fill_slot(shipped, inst.mainline_table(TABLE_B)))
        self.assertEqual(inst.read_slot(boot_now), inst.mainline_table(TABLE_B))
        ksize = struct.unpack('<I', boot_now[8:12])[0]
        self.assertEqual(boot_now[576:608], ref_id(boot_now[PAGE:PAGE + ksize], b'', b'', 1))
        self.assertEqual(u.part(21)[:len(store_image())], store_image())
        self.assertEqual(u.get('data/techo5/misc/techo5/name'), b'Kitchen\n')
        self.assertTrue(u.rebooted)
        self.assertIn('p20-boot.img', out)

        # Again: the backup is kept (its boot is Fire OS's) while boot now holds TECHO5's.
        u2_before = u.snapshot()
        out, err = self.install(u, *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertIn("boot now holds TECHO5's", out)
        self.assertEqual(u.snapshot(), u2_before)

    def test_dry_run_writes_nothing(self):
        u = self.unit()
        out, err = self.install(u, '--dry-run', *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertEqual(u.writes(), [])
        self.assertEqual(u.changed(), [])
        self.assertIn('table B', out)
        self.assertFalse(os.path.exists(os.path.join(self.tmp, 'backups', 'SIMULATED', 'partitions')))

    def test_mmcblk0(self):
        u = self.unit(mmc='mmcblk0')
        out, err = self.install(u, *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertEqual(u.changed(), [20, 21])

    def test_other_panel_refused(self):
        for kw in ({'cmdline': 'jd936x_id=0x32', 'lk_panel': 'jd936x_wxga_dsi_vdo_athena_st_kd_hsd_new'},
                   {'cmdline': '', 'lk_panel': None}):
            u = self.unit(**kw)
            out, err = self.install(u, *self.mainline_args())
            self.assertIn('--kernel amazon', err or '')
            self.assertEqual(u.changed(), [])
            self.assertFalse(any('dd if=' in c for c in u.log), 'nothing read before the panel check')
            shutil.rmtree(os.path.join(self.tmp, 'unit'))
        u = self.unit(cmdline='jd936x_id=0x32', lk_panel=None)
        out, err = self.install(u, '--force-panel', '--dry-run', *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertIn('--force-panel', out)

    def test_techo5_boot_without_stock_backup(self):
        filled = inst.fill_slot(techo5_boot(), inst.mainline_table(TABLE_B))
        u = self.unit(boot=filled)
        out, err = self.install(u, *self.mainline_args())
        self.assertIn('--stock-boot', err or '')
        self.assertEqual(u.writes(), [])
        stock = os.path.join(self.tmp, 'stock-boot.img')
        with open(stock, 'wb') as f:
            f.write(stock_boot())
        out, err = self.install(u, '--stock-boot', stock, *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertEqual(u.writes(), [21, 20])
        # The backup was made with TECHO5 on boot: said plainly, and no restore lines that would not
        # bring Fire OS back.
        self.assertIn('NOT a Fire OS backup', out)
        self.assertNotIn('adb exec-in', out)

    def test_unknown_table_stops_before_writing(self):
        inst.KNOWN_AMP_TABLES.clear()
        u = self.unit()
        out, err = self.install(u, *self.mainline_args())
        self.assertIn('not known yet', err or '')
        self.assertEqual(u.writes(), [])
        self.assertEqual(u.changed(), [])

    def test_live_speaker_config_disagrees(self):
        u = self.unit(live_cfg=0)
        out, err = self.install(u, *self.mainline_args())
        self.assertIn('not guessing', err or '')
        self.assertEqual(u.writes(), [])

    def test_shipped_boot_already_filled(self):
        with open(self.boot, 'wb') as f:
            f.write(inst.fill_slot(techo5_boot(), b'\x01\x02'))
        u = self.unit()
        out, err = self.install(u, *self.mainline_args())
        self.assertIn('already filled', err or '')
        self.assertEqual(u.writes(), [])

    def test_amazon_kernel(self):
        u = self.unit(cmdline='jd936x_id=0x31', lk_panel=None)  # any screen
        out, err = self.install(u, '--kernel', 'amazon', '--image', self.store)
        self.assertIsNone(err, out)
        self.assertEqual(u.writes(), [21])
        self.assertEqual(u.changed(), [21])

    def test_amazon_kernel_refused_over_techo5_boot(self):
        u = self.unit(boot=inst.fill_slot(techo5_boot(), inst.mainline_table(TABLE_B)))
        out, err = self.install(u, '--kernel', 'amazon', '--image', self.store)
        self.assertIn("Amazon's kernel on boot", err or '')
        self.assertEqual(u.writes(), [])

    def test_wrong_partition_table(self):
        u = self.unit()
        u.put('sys/class/block/mmcblk1p3/uevent', 'PARTNAME=frp\n')
        out, err = self.install(u, *self.mainline_args())
        self.assertIn('saiyaku-athena', err or '')
        self.assertEqual(u.writes(), [])

    def test_fire_os_backup_gets_restore_lines(self):
        u = self.unit()
        out, err = self.install(u, *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertNotIn('NOT a Fire OS backup', out)
        self.assertIn('adb exec-in', out)

    def test_settings_pushes_wait_for_userdata(self):
        u = self.unit()
        pub = os.path.join(self.tmp, 'id.pub')
        with open(pub, 'w') as f:
            f.write('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample made-up\n')
        out, err = self.install(u, '--ssh-key', pub, *self.mainline_args())
        self.assertIsNone(err, out)
        settings = {r: f for r, f in u.readies.items() if r.startswith('/data/')}
        self.assertEqual(len(settings), 4, settings)  # name, psk, authorized_keys, state.json
        for remote, ready in settings.items():
            self.assertIsNotNone(ready, remote)
            self.assertTrue(ready(), remote)

    def test_wifi_added_to_saved_networks(self):
        u = self.unit()
        conf = 'data/techo5/techo5-linux/wpa_supplicant.conf'
        home = 'Home'.encode().hex()
        u.put(conf, 'ctrl_interface=/run/wpa\nupdate_config=0\n'
                    'network={\n\tssid=%s\n\tpsk=%s\n}\n'
                    'network={\n\tssid="Home"\n\tpsk="an old one"\n}\n'
                    'network={\n\tssid=%s\n\tpsk=%s\n}\n' % ('aa' * 4, 'bb' * 32, home, 'cc' * 32))
        passfile = os.path.join(self.tmp, 'pass.txt')
        with open(passfile, 'wb') as f:
            f.write(b'\xef\xbb\xbfcorrect horse\r\n')  # as Notepad saves it
        out, err = self.install(u, '--wifi', 'Home', '--wifi-passphrase-file', passfile, *self.mainline_args())
        self.assertIsNone(err, out)
        got = u.get(conf).decode()
        want_psk = hashlib.pbkdf2_hmac('sha1', b'correct horse', b'Home', 4096, 32).hex()
        self.assertEqual(got, 'ctrl_interface=/run/wpa\nupdate_config=0\n'
                              'network={\n\tssid=%s\n\tpsk=%s\n}\n'
                              'network={\n\tssid=%s\n\tpsk=%s\n}\n' % ('aa' * 4, 'bb' * 32, home, want_psk))

    def test_nothing_unmounted_before_confirm(self):
        u = self.unit()
        u.put('proc/mounts', u.get('proc/mounts').decode() + '%s/dev/block/mmcblk1p21 /system_root ext4 ro 0 0\n'
              % u.sh_root)
        asked = []

        def no(force, lines):
            asked.append(list(u.log))
            raise Fail('not confirmed')
        saved, inst.confirm = inst.confirm, no
        try:
            out, err = self.install(u, *self.mainline_args())
        finally:
            inst.confirm = saved
        self.assertEqual(err, 'not confirmed')
        self.assertFalse(any('umount' in c or 'mount -' in c for c in asked[0]), asked[0])
        self.assertIn('unmounted once you confirm', out)
        self.assertEqual(u.changed(), [])

    def test_readback_mismatch_twice_stops(self):
        u = self.unit()
        real_sh = u.sh
        u.sh = lambda cmd: '0' * 64 + '  -' if 'drop_caches' in cmd and 'p21' in cmd else real_sh(cmd)
        out, err = self.install(u, *self.mainline_args())
        self.assertIn('did not read back as written, twice', err or '')
        self.assertEqual(u.writes(), [21, 21])  # system twice, boot never
        self.assertNotIn(20, u.changed())
        self.assertFalse(u.rebooted)
        self.assertIn('try 1 of 2', out)

    def test_readback_mismatch_once_retries(self):
        u = self.unit()
        real_sh = u.sh
        bad = [1]

        def sh(cmd):
            if 'drop_caches' in cmd and 'p21' in cmd and bad:
                bad.pop()
                return '0' * 64 + '  -'
            return real_sh(cmd)
        u.sh = sh
        out, err = self.install(u, *self.mainline_args())
        self.assertIsNone(err, out)
        self.assertEqual(u.writes(), [21, 21, 20])
        self.assertIn('second try', out)


class TargetCheck(unittest.TestCase):
    """target_ok, the check run right before every write."""

    class Answers:
        def __init__(self, out):
            self.out = out

        def sh(self, cmd):
            return self.out

    PARTS = {21: ('system', 4 << 20), 20: ('boot', 2 << 20), 23: ('userdata', 64 << 10)}

    def setUp(self):
        self.mmc, inst.MMC = inst.MMC, 'mmcblk1'

    def tearDown(self):
        inst.MMC = self.mmc

    def test_the_partition(self):
        a = self.Answers('BLOCK\n/dev/block/mmcblk1p21\nsystem')
        self.assertEqual(inst.target_ok(a, self.PARTS, 21), 4 << 20)

    def test_link_to_another_node(self):
        # The node is a link: readlink -f names where it really goes.
        for out in ('BLOCK\n/dev/block/mmcblk1p23\nsystem', 'BLOCK\n/dev/block/mmcblk1p3\nsystem',
                    '\n/dev/null\nsystem', 'BLOCK\n/dev/block/mmcblk1p21\nuserdata', 'BLOCK'):
            with self.assertRaisesRegex(Fail, 'refusing to write'):
                inst.target_ok(self.Answers(out), self.PARTS, 21)

    def test_never_other_partitions(self):
        with self.assertRaisesRegex(Fail, 'refusing to write'):
            inst.target_ok(self.Answers('BLOCK\n/dev/block/mmcblk1p23\nuserdata'), self.PARTS, 23)

    def test_symlinked_node_on_the_simulated_unit(self):
        tmp = tempfile.mkdtemp(prefix='t5-athena-link-')
        try:
            u = FakeTWRP(os.path.join(tmp, 'unit'), boot=stock_boot())
            node = os.path.join(u.root, 'dev', 'block', 'mmcblk1p21')
            os.remove(node)
            try:
                os.symlink(os.path.join(u.root, 'dev', 'block', 'mmcblk1p23'), node)
            except (OSError, NotImplementedError) as e:
                self.skipTest('no symlinks here: %s' % e)
            with self.assertRaisesRegex(Fail, 'refusing to write'):
                inst.target_ok(u, {21: ('system', 4 << 20)}, 21)
        finally:
            shutil.rmtree(tmp, ignore_errors=True)


class MergeWifi(unittest.TestCase):
    NEW = ('4e6577', 'ab' * 32)

    def test_no_configuration(self):
        for existing in (None, '', '\n'):
            self.assertEqual(inst.merge_wifi(existing, *self.NEW), inst.WIFI_CONF % self.NEW)

    def test_header_only(self):
        self.assertEqual(inst.merge_wifi('ctrl_interface=/run/wpa\nupdate_config=0', *self.NEW),
                         inst.WIFI_CONF % self.NEW)

    def test_keeps_others_replaces_same_name(self):
        existing = ('ctrl_interface=/run/wpa\r\nupdate_config=0\r\nnetwork={\r\n\tssid="New"\r\n\tpsk="x"\r\n}\r\n'
                    'network={\n\tssid=4f6c64\n\tkey_mgmt=NONE\n}\nnetwork={\n\tssid=4E6577\n\tpsk=%s\n}' % ('cd' * 32))
        self.assertEqual(inst.merge_wifi(existing, *self.NEW),
                         'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid=4f6c64\n\tkey_mgmt=NONE\n}\n'
                         'network={\n\tssid=4e6577\n\tpsk=%s\n}\n' % ('ab' * 32))

    def test_indented_braces(self):
        # An indented closing brace still closes its block: the next block is not swallowed with it.
        existing = ('ctrl_interface=/run/wpa\n  network={\n\tssid="New"\n\tpsk="x"\n\t}\n'
                    'network={\n\tssid="Other"\n\tpsk="y"\n  }\n')
        self.assertEqual(inst.merge_wifi(existing, *self.NEW),
                         'ctrl_interface=/run/wpa\nnetwork={\n\tssid="Other"\n\tpsk="y"\n  }\n'
                         'network={\n\tssid=4e6577\n\tpsk=%s\n}\n' % ('ab' * 32))

    def test_first_of_two_same_name(self):
        existing = ('# saved by hand\nnetwork={\n\tssid=4e6577\n\tpsk="old"\n}\n'
                    'network={\n\tssid="Next"\n\tkey_mgmt=NONE\n}\n')
        self.assertEqual(inst.merge_wifi(existing, *self.NEW),
                         '# saved by hand\nnetwork={\n\tssid="Next"\n\tkey_mgmt=NONE\n}\n'
                         'network={\n\tssid=4e6577\n\tpsk=%s\n}\n' % ('ab' * 32))

    def test_p_ssid(self):
        for ssid in ('P"New"', 'P"N\\x65w"', 'P"\\x4e\\x65\\x77"'):
            existing = 'update_config=0\nnetwork={\n\tssid=%s\n\tpsk="old"\n}\n' % ssid
            self.assertEqual(inst.merge_wifi(existing, *self.NEW), 'update_config=0\n'
                             'network={\n\tssid=4e6577\n\tpsk=%s\n}\n' % ('ab' * 32), ssid)
        self.assertEqual(inst.wifi_ssid_hex(['\tssid=P"a\\tb\\\\"']), b'a\tb\\'.hex())
        self.assertEqual(inst.wifi_ssid_hex(['\tssid="a"b"']), b'a"b'.hex())

    def test_only_block_removed_leaves_no_blank_line(self):
        existing = 'ctrl_interface=/run/wpa\nupdate_config=0\n\nnetwork={\n\tssid="New"\n\tpsk="x"\n}\n\n'
        self.assertEqual(inst.merge_wifi(existing, *self.NEW), inst.WIFI_CONF % self.NEW)


STOCK = [p for p in os.environ.get('TECHO5_STOCK_BOOTS', '').split(os.pathsep) if p]


@unittest.skipUnless(STOCK, 'set TECHO5_STOCK_BOOTS to Fire OS boot images to check the extraction on them')
class RealStockBoot(unittest.TestCase):
    """Fire OS 7080's boot image, read in place."""

    def test_extraction(self):
        ref = os.environ.get('TECHO5_DSP_REF')
        for path in STOCK:
            with open(path, 'rb') as f:
                data = f.read()
            img = inst.BootImage(data, path)
            self.assertEqual(img.computed_id(), img.stored_id(), 'the header id method on Amazon\'s image')
            hashes = {hashlib.sha256(inst.mainline_table(t)).hexdigest()
                      for _, t in inst.amp_tables(inst.kernel_image(img.kernel()))}
            self.assertEqual(hashes, set(inst.KNOWN_AMP_TABLES), path)
            blob, letter, _, cfg, _ = inst.stock_amp_tuning(
                data, ['amzn,athena-pvt', 'amzn,athena-dvt', 'amzn,athena'], path)
            self.assertEqual((letter, cfg), ('B', 1))
            blob2, letter2, _, _, _ = inst.stock_amp_tuning(data, None, path)
            self.assertEqual((blob2, letter2), (blob, letter))
            if ref:
                with open(ref, 'rb') as f:
                    self.assertEqual(blob, f.read(), 'table B as the mainline blob')
            filled = inst.fill_slot(techo5_boot(), blob)
            self.assertEqual(inst.read_slot(filled), blob)


if __name__ == '__main__':
    unittest.main(verbosity=2)
