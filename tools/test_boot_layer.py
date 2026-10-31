#!/usr/bin/env python3
"""Offline tests for the OS side of the boot layer: bootctl, slotctl, the command line checks in
techo5-lib.sh and boot.sh's trial check. No unit, no network.

    python3 tools/test_boot_layer.py

The scripts run under busybox, as on the units, in an Alpine container (Docker). Without Docker the
test is skipped and says so (SKIPPED, and a warning on GitHub Actions); in CI (CI set) with Docker
there, a run that cannot start fails instead. Each case runs twice where it matters: on a unit without the boot layer (no A/B record
in misc, no ab.slot= on the command line), where nothing may change, and on one with it.

The A/B record is checked here with Python's own decoding (struct, zlib.crc32), not with bootctl.
"""
import hashlib
import os
import shutil
import struct
import subprocess
import sys
import unittest
import uuid
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
LINUX = os.path.join(HERE, 'linux')
# Alpine 3.24.2, the release the rootfs is built from (tools/fetch-inputs.py), pinned by digest.
IMAGE = os.environ.get('TECHO5_TEST_ALPINE',
                       'alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6')
C = None  # the container


def skip(reason):
    msg = 'test_boot_layer: SKIPPED: ' + reason
    print(msg, file=sys.stderr)
    if os.environ.get('GITHUB_ACTIONS'):
        print('::warning::' + msg)
    raise unittest.SkipTest(reason)


def setUpModule():
    global C
    if not shutil.which('docker'):
        skip('needs Docker')
    r = subprocess.run(['docker', 'run', '-d', '--rm', IMAGE, 'sleep', '1800'], capture_output=True, text=True)
    if r.returncode != 0:
        why = 'docker run %s failed: %s' % (IMAGE, r.stderr.strip())
        if os.environ.get('CI'):
            raise RuntimeError(why)  # Docker is there in CI: a run that cannot start is a failure
        skip(why)
    C = r.stdout.strip()
    for name in ('bootctl', 'slotctl', 'techo5-lib.sh'):
        with open(os.path.join(LINUX, name), 'rb') as f:
            put('/src/' + name, f.read().replace(b'\r\n', b'\n'))
    with open(os.path.join(LINUX, 'rootfs', 'etc', 'techo5', 'boot.sh'), 'rb') as f:
        put('/src/boot.sh', f.read().replace(b'\r\n', b'\n'))


def tearDownModule():
    if C:
        subprocess.run(['docker', 'rm', '-f', C], capture_output=True)


def sh(script, stdin=None):
    """Run a busybox sh script in the container: (exit status, stdout and stderr as text)."""
    r = subprocess.run(['docker', 'exec', '-i', C, 'sh', '-c', script], input=stdin, capture_output=True)
    return r.returncode, (r.stdout + r.stderr).decode(errors='replace')


def put(path, data):
    rc, out = sh('mkdir -p "$(dirname "%s")" && cat > "%s"' % (path, path), stdin=data)
    assert rc == 0, out


def get(path):
    r = subprocess.run(['docker', 'exec', C, 'cat', path], capture_output=True)
    assert r.returncode == 0, r.stderr
    return r.stdout


def sha(path):
    return hashlib.sha256(get(path)).hexdigest()


# ------------------------------------------------------------------------------------------ the record

MAGIC = 0x42414342


def record(a, b, last=b'a'):
    """A valid A/B record (AOSP's struct bootloader_control), a and b as (priority, tries, successful)."""
    r = bytearray(32)
    r[0:2] = b'_' + last
    struct.pack_into('<I', r, 4, MAGIC)
    r[8] = 1
    r[9] = 2
    for i, (p, t, s) in enumerate((a, b)):
        r[12 + 2 * i] = p | t << 4 | s << 7
    struct.pack_into('<I', r, 28, zlib.crc32(bytes(r[:28])))
    return bytes(r)


def misc(rec=None):
    """A 512 KB misc partition with something in every area around the record."""
    m = bytearray(512 * 1024)
    m[0:13] = b'boot-recovery'
    m[4096:4110] = b'TECHO5 01 init'
    m[0x10000:0x10008] = b'snapshot'
    if rec:
        m[2048:2080] = rec
    return bytes(m)


def decode(m):
    """The record in misc as {slot: (priority, tries, successful)}, its CRC checked."""
    r = m[2048:2080]
    assert struct.unpack_from('<I', r, 4)[0] == MAGIC, 'no record'
    assert struct.unpack_from('<I', r, 28)[0] == zlib.crc32(r[:28]), 'bad CRC'
    assert r[20:28] == bytes(8), 'reserved1 not zero'
    return {s: (r[12 + 2 * i] & 15, r[12 + 2 * i] >> 4 & 7, r[12 + 2 * i] >> 7) for i, s in enumerate('ab')}


def outside(m):
    return m[:2048] + m[2080:]


# ------------------------------------------------------------------------------------------ bootctl

class Bootctl(unittest.TestCase):
    def setUp(self):
        self.d = '/t/bootctl-' + self._testMethodName
        sh('rm -rf %s' % self.d)

    def bc(self, cmdline, args):
        put(self.d + '/cmdline', cmdline.encode() + b'\n')
        return sh('BOOTCTL_MISC={d}/misc BOOTCTL_CMDLINE={d}/cmdline BOOTCTL_CONF=/nonexistent '
                  'sh /src/bootctl {a}'.format(d=self.d, a=args))

    def test_no_record_changes_nothing(self):
        put(self.d + '/misc', misc())
        before = sha(self.d + '/misc')
        for c in ('status', 'set-active a', 'mark-good b', 'mark-bad a', 'set a priority=1'):
            rc, out = self.bc('ab.slot=a', c)
            self.assertEqual(rc, 3, c + ': ' + out)
            self.assertIn('no A/B record', out)
        rc, out = self.bc('', '-q mark-good a')
        self.assertEqual((rc, out), (3, ''))
        self.assertEqual(sha(self.d + '/misc'), before)

    def test_init_writes_the_default_record_into_an_empty_area_only(self):
        put(self.d + '/misc', misc())
        rc, out = self.bc('', 'init')
        self.assertEqual(rc, 0, out)
        m = get(self.d + '/misc')
        self.assertEqual(m[2048:2080].hex(), '5f61000042434142010200003f003e000000000000000000000000005a0fd7c0')
        self.assertEqual(decode(m), {'a': (15, 3, 0), 'b': (14, 3, 0)})
        self.assertEqual(outside(m), outside(misc()))
        o = bytearray(misc())
        o[3000] = 1
        put(self.d + '/misc', bytes(o))
        rc, out = self.bc('', 'init')
        self.assertEqual(rc, 1, out)
        self.assertEqual(get(self.d + '/misc'), bytes(o))

    def test_edits(self):
        put(self.d + '/misc', misc(record((15, 2, 1), (14, 0, 1))))
        rc, out = self.bc('ab.slot=a', 'status')
        self.assertEqual(rc, 0, out)
        self.assertIn('slot a: priority 15, tries 2, successful 1 (good)', out)
        self.assertIn('booted: a', out)
        steps = [
            ('ab.slot=a', 'set-active b', {'a': (14, 2, 1), 'b': (15, 3, 0)}),
            ('ab.slot=b ab.kernel=b', 'mark-good', {'a': (14, 2, 1), 'b': (15, 3, 1)}),
            ('ab.slot=b', 'mark-bad a', {'a': (0, 0, 0), 'b': (15, 3, 1)}),
            ('', 'set a priority=14 tries=3', {'a': (14, 3, 0), 'b': (15, 3, 1)}),
        ]
        for cmdline, args, want in steps:
            rc, out = self.bc(cmdline, args)
            self.assertEqual(rc, 0, args + ': ' + out)
            m = get(self.d + '/misc')
            self.assertEqual(decode(m), want, args)
            self.assertEqual(outside(m), outside(misc()), args)
        before = sha(self.d + '/misc')
        rc, out = self.bc('ab.slot=b', 'mark-good')
        self.assertEqual(rc, 0, out)
        self.assertEqual(sha(self.d + '/misc'), before, 'an edit that changes nothing wrote')
        rc, out = self.bc('', 'mark-good')
        self.assertEqual(rc, 1, 'mark-good with no slot booted: ' + out)
        rc, out = self.bc('', 'set a priority=16')
        self.assertEqual(rc, 2, out)

    def test_a_broken_record_is_not_written(self):
        m = bytearray(misc(record((15, 2, 1), (14, 0, 1))))
        m[2060] ^= 0xff
        put(self.d + '/misc', bytes(m))
        rc, out = self.bc('', 'set-active b')
        self.assertEqual(rc, 3, out)
        self.assertEqual(get(self.d + '/misc'), bytes(m))

    def test_current_is_ab_slot_only(self):
        for cmdline, want in (('console=ttyS0 ab.slot=b ab.kernel=a', (0, 'b\n')),
                              ('console=ttyS0 root=/dev/mmcblk0p12', (1, '')),
                              ('ab.kernel=rescue', (1, ''))):
            self.assertEqual(self.bc(cmdline, 'current'), want, cmdline)


# ------------------------------------------------------------------------------------------ bootctl on systemd-boot

ENTRY = 'title Slot %s (%s) 2026-10-30 12:00\nsort-key slot-%s\nlinux /%s/vmlinuz\noptions root=PARTUUID=x rw ab.slot=%s ab.kernel=%s\n'


class SystemdBoot(unittest.TestCase):
    """bootctl's systemd-boot backend (the Surface RT): the record kept as loader entries with boot
    counters and loader.conf's default and preferred, on a copy of an ESP."""

    def setUp(self):
        self.d = '/t/sdboot-' + self._testMethodName
        self.n = 0
        sh('rm -rf %s; mkdir -p %s/esp/loader/entries %s/efivars' % (self.d, self.d, self.d))

    def esp(self, entries, default, preferred=None):
        """entries: {file name: word in its title}."""
        for name, word in entries.items():
            s = name[len('slot-')]
            put('%s/esp/loader/entries/%s' % (self.d, name), (ENTRY % (s.upper(), word, s, s.upper(), s, s)).encode())
        put(self.d + '/esp/loader/loader.conf', ('timeout 5\nauto-entries no\ndefault %s\n' % default +
                                                 ('preferred %s\n' % preferred if preferred else '')).encode())

    def bc(self, cmdline, args, env='BOOTCTL_ESP={d}/esp'):
        self.n += 1
        put('%s/cmdline.%d' % (self.d, self.n), cmdline.encode() + b'\n')
        return sh(('BOOTCTL_CMDLINE={d}/cmdline.{n} BOOTCTL_CONF=/nonexistent BOOTCTL_EFIVARS={d}/efivars ' + env +
                   ' sh /src/bootctl {a}').format(d=self.d, n=self.n, a=args))

    def entries(self):
        rc, out = sh('ls %s/esp/loader/entries' % self.d)
        return out.split()

    def loader(self):
        """loader.conf's default and preferred, the rest checked to be kept."""
        lines = get(self.d + '/esp/loader/loader.conf').decode().splitlines()
        self.assertEqual(lines[:2], ['timeout 5', 'auto-entries no'])
        kv = dict(l.split(None, 1) for l in lines[2:])
        return kv.get('default'), kv.get('preferred')

    def title(self, name):
        return get('%s/esp/loader/entries/%s' % (self.d, name)).decode().splitlines()[0]

    def tree(self):
        return sh('cd %s/esp && find . | sort | while read f; do [ -f "$f" ] && sha256sum "$f" || echo "$f"; done'
                  % self.d)[1]

    def try_boot(self, name):
        """What systemd-boot does when it boots a counted entry: one try, by renaming it."""
        stem, c = name[:-len('.conf')].rsplit('+', 1)
        left, done = (c.split('-') + ['0'])[:2]
        new = '%s+%d-%d.conf' % (stem, max(int(left) - 1, 0), int(done) + 1)
        sh('cd %s/esp/loader/entries && mv %s %s' % (self.d, name, new))
        return new

    def test_status_reads_the_entries_as_the_record(self):
        self.esp({'slot-a.conf': 'good', 'slot-b+2-1.conf': 'trial'}, 'slot-a.conf', 'slot-b.conf')
        rc, out = self.bc('console=ttyS0 ab.slot=b ab.kernel=b', 'status')
        self.assertEqual(rc, 0, out)
        self.assertIn('slot a: priority 14, tries 0, successful 1 (good)', out)
        self.assertIn('slot b: priority 15, tries 2, successful 0 (trial)', out)
        self.assertIn('booted: b (kernel b)', out)
        # tries used up: systemd-boot skips the preferred entry, the default boots
        sh('mv {d}/esp/loader/entries/slot-b+2-1.conf {d}/esp/loader/entries/slot-b+0-3.conf'.format(d=self.d))
        rc, out = self.bc('ab.slot=a ab.kernel=a', 'status')
        self.assertIn('slot a: priority 15, tries 0, successful 1 (good)', out)
        self.assertIn('slot b: priority 0, tries 0, successful 0 (bad)', out)
        sh('rm %s/esp/loader/entries/slot-b+0-3.conf' % self.d)
        rc, out = self.bc('', 'status')
        self.assertIn('slot b: priority 0, tries 0, successful 0, no entry (bad)', out)
        self.assertIn('booted: - (kernel )', out)

    def test_the_os_side_of_a_slot_update(self):
        """SPEC.md's "What the OS does", step by step, as systemd-boot's files."""
        self.esp({'slot-a.conf': 'good', 'slot-b.conf': 'good'}, 'slot-a.conf')
        steps = [
            # installing into b (a runs): marked bad before its files change, then on trial
            ('ab.slot=a ab.kernel=a', 'mark-bad b', ['slot-a.conf', 'slot-b+0.conf'], ('slot-a.conf', None)),
            ('ab.slot=a ab.kernel=a', 'set-active b', ['slot-a.conf', 'slot-b+3.conf'], ('slot-a.conf', 'slot-b.conf')),
        ]
        for cmdline, args, ents, ldr in steps:
            rc, out = self.bc(cmdline, args)
            self.assertEqual(rc, 0, args + ': ' + out)
            self.assertEqual((self.entries(), self.loader()), (ents, ldr), args)
        self.assertEqual(self.title('slot-b+3.conf'), 'title Slot B (trial) 2026-10-30 12:00')
        self.try_boot('slot-b+3.conf')  # b boots: systemd-boot takes a try
        rc, out = self.bc('ab.slot=b ab.kernel=b', 'status')
        self.assertIn('slot b: priority 15, tries 2, successful 0 (trial)', out)
        # the OS's health check: b works
        rc, out = self.bc('ab.slot=b ab.kernel=b', 'mark-good')
        self.assertEqual(rc, 0, out)
        self.assertEqual((self.entries(), self.loader()), (['slot-a.conf', 'slot-b.conf'], ('slot-b.conf', None)))
        self.assertEqual(self.title('slot-b.conf'), 'title Slot B (good) 2026-10-30 12:00')
        before = self.tree()
        rc, out = self.bc('ab.slot=b ab.kernel=b', 'mark-good')
        self.assertEqual((rc, out), (0, 'bootctl: slot b already so\n'))
        self.assertEqual(self.tree(), before, 'an edit that changes nothing wrote')
        # the user wants the other slot: on trial again
        self.bc('ab.slot=b ab.kernel=b', 'set-active a')
        self.assertEqual((self.entries(), self.loader()), (['slot-a+3.conf', 'slot-b.conf'], ('slot-b.conf', 'slot-a.conf')))
        self.try_boot('slot-a+3.conf')
        # the running slot is known broken: the other boots next
        rc, out = self.bc('ab.slot=a ab.kernel=a', 'mark-bad a')
        self.assertEqual(rc, 0, out)
        self.assertEqual((self.entries(), self.loader()), (['slot-a+0-1.conf', 'slot-b.conf'], ('slot-b.conf', None)))
        self.assertEqual(self.title('slot-a+0-1.conf'), 'title Slot A (bad) 2026-10-30 12:00')
        # slotctl install --keep-active: on trial, not first
        rc, out = self.bc('ab.slot=b ab.kernel=b', 'set a priority=14 tries=3 successful=0')
        self.assertEqual(rc, 0, out)
        self.assertEqual((self.entries(), self.loader()), (['slot-a+3-1.conf', 'slot-b.conf'], ('slot-b.conf', None)))
        self.assertIn('slot a: priority 14, tries 3, successful 0 (trial)', out)
        rc, out = self.bc('', 'mark-good')
        self.assertEqual(rc, 1, 'mark-good with no slot booted: ' + out)
        self.assertEqual(self.bc('', 'set a priority=16')[0], 2)

    def test_a_trial_without_a_good_slot_has_no_preferred(self):
        self.esp({'slot-a+0-3.conf': 'trial', 'slot-b.conf': 'good'}, 'slot-b.conf')
        rc, out = self.bc('ab.slot=b ab.kernel=b', 'mark-bad b')
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.loader(), ('slot-b.conf', None))
        rc, out = self.bc('ab.slot=b ab.kernel=b', 'set-active a')
        self.assertEqual((self.entries(), self.loader()), (['slot-a+3.conf', 'slot-b+0.conf'], ('slot-a.conf', None)))

    def test_entries_it_cannot_read_are_left_alone(self):
        self.esp({'slot-a.conf': 'good', 'slot-a+1.conf': 'trial', 'slot-b.conf': 'good'}, 'slot-a.conf')
        before = self.tree()
        for args in ('status', 'mark-bad b', 'set-active a'):
            rc, out = self.bc('ab.slot=a', args)
            self.assertEqual(rc, 1, args + ': ' + out)
            self.assertIn('slot a has 2 entries', out)
        self.assertEqual(self.tree(), before)
        rc, out = self.bc('ab.slot=a', 'set-active b', env='BOOTCTL_BACKEND=systemd-boot BOOTCTL_ESP=%s/nowhere' % self.d)
        self.assertEqual(rc, 3, out)
        self.assertIn('no slot-a or slot-b entries', out)

    def test_which_backend(self):
        """The record where misc holds one, systemd-boot's entries where only the ESP has them, as
        configured where /etc/bootctl.conf says; a unit with neither stays as it was."""
        self.esp({'slot-a.conf': 'good', 'slot-b.conf': 'good'}, 'slot-a.conf')
        put(self.d + '/misc', misc(record((15, 2, 1), (14, 0, 1))))
        esp = 'BOOTCTL_ESP=%s/esp ' % self.d
        rc, out = self.bc('ab.slot=a', 'status', env=esp + 'BOOTCTL_MISC=%s/misc' % self.d)
        self.assertIn('misc: ', out)
        rc, out = self.bc('ab.slot=a', 'status', env=esp)
        self.assertIn('esp: %s/esp (systemd-boot' % self.d, out)
        rc, out = self.bc('ab.slot=a', '-e %s/esp status' % self.d, env='')
        self.assertIn('esp: ', out)
        put(self.d + '/bootctl.conf', b'BACKEND=misc\n')
        rc, out = sh('BOOTCTL_CONF={d}/bootctl.conf BOOTCTL_ESP={d}/esp sh /src/bootctl status'.format(d=self.d))
        self.assertEqual(rc, 3, out)
        self.assertIn('no partition named', out)
        put(self.d + '/bootctl.conf', ('BACKEND=systemd-boot\nESP=%s/esp\n' % self.d).encode())
        rc, out = sh('BOOTCTL_CONF={d}/bootctl.conf BOOTCTL_CMDLINE=/nonexistent sh /src/bootctl status'.format(d=self.d))
        self.assertEqual(rc, 0, out)
        rc, out = self.bc('ab.slot=a', 'status', env='BOOTCTL_ESP=%s/nowhere' % self.d)
        self.assertEqual(rc, 3, out)
        self.assertIn('no partition named', out)

    def test_init_writes_nothing(self):
        self.esp({'slot-a.conf': 'good', 'slot-b+1.conf': 'trial'}, 'slot-a.conf', 'slot-b.conf')
        before = self.tree()
        rc, out = self.bc('', 'init')
        self.assertEqual(rc, 0, out)
        self.assertIn('already holds', out)
        self.assertEqual(self.tree(), before)

    def test_current_is_ab_slot_else_the_entry_systemd_boot_booted(self):
        self.esp({'slot-a.conf': 'good', 'slot-b.conf': 'good'}, 'slot-a.conf')
        self.assertEqual(self.bc('console=ttyS0 ab.slot=b ab.kernel=b', 'current'), (0, 'b\n'))
        self.assertEqual(self.bc('console=ttyS0', 'current'), (1, ''))
        # LoaderEntrySelected: 4 bytes of attributes, then the id in UTF-16, NUL ended
        put(self.d + '/efivars/LoaderEntrySelected-4a67b082-0a4c-41cf-b6c7-440b29bb8c4f',
            b'\x06\0\0\0' + 'slot-a.conf\0'.encode('utf-16-le'))
        self.assertEqual(self.bc('console=ttyS0', 'current'), (0, 'a\n'))


# ------------------------------------------------------------------------------------------ slotctl

class Slotctl(unittest.TestCase):
    """slotctl on a store with a (good) and b (good), b active."""

    KERNEL = bytes(range(256)) * 300

    def setUp(self):
        self.d = '/t/slotctl-' + self._testMethodName
        self.n = 0
        rc, out = sh('''set -e; d={d}; rm -rf $d; mkdir -p $d/store/slots/a/store $d/store/slots/b/store
            echo board=athena > $d/store/.techo5-store; echo b > $d/store/active
            echo good > $d/store/slots/a.state; echo good > $d/store/slots/b.state
            mkdir -p $d/bin; cp /src/slotctl /src/bootctl $d/bin/'''.format(d=self.d))
        self.assertEqual(rc, 0, out)
        self.tarball('rootfs.tar.gz')

    def tarball(self, name, kernel=KERNEL, header_args=None, init=True):
        r = self.d + '/root-' + name
        if kernel is not None:
            put(r + '/usr/share/techo5/kernel/boot-athena.img', kernel)
        if header_args:
            put(r + '/usr/share/techo5/kernel/boot-header-args', header_args.encode() + b'\n')
        rc, out = sh('''set -e; r={r}; mkdir -p $r/sbin $r/usr/local/bin $r/etc/techo5 $r/store
            {init}: > $r/usr/local/bin/techo5; : > $r/etc/techo5/boot.sh; : > $r/etc/inittab
            echo T5_BOARD=athena > $r/etc/techo5/device.conf
            echo "techo5 rootfs v1.2.19 built 2026-10-08T20:15Z, daemon echod version v1.2.19" > $r/etc/techo5-release
            cd $r && tar -czf {d}/{name} .'''.format(r=r, d=self.d, name=name, init=': > $r/sbin/init; ' if init else ''))
        self.assertEqual(rc, 0, out)
        return self.d + '/' + name

    def slotctl(self, booted, args, cmdline=None, env='', ok=True, bootctl=True):
        """slotctl <args> with the slot <booted> running, booted by the boot layer unless cmdline says."""
        self.n += 1
        if cmdline is None:
            cmdline = 'ab.slot=%s ab.kernel=%s' % (booted, booted)
        put('%s/cmdline.%d' % (self.d, self.n), b'console=ttyS0 ' + cmdline.encode() + b'\n')
        rc, out = sh('''mkdir -p /run/techo5; echo {b} > /run/techo5/slot; {nobc}
            {env} BOOTCTL_CMDLINE={d}/cmdline.{n} BOOTCTL_CONF=/nonexistent STORE={d}/store SLOTCTL_MISC={d}/misc \
            sh {d}/bin/slotctl {a}'''.format(b=booted, d=self.d, n=self.n, a=args, env=env,
                                             nobc='' if bootctl else 'rm -f %s/bin/bootctl;' % self.d))
        if ok:
            self.assertEqual(rc, 0, 'slotctl %s: %s' % (args, out))
        return rc, out

    def state(self, s):
        return get('%s/store/slots/%s.state' % (self.d, s)).decode().strip()

    def active(self):
        return get(self.d + '/store/active').decode().strip()

    def field(self):
        return decode(get(self.d + '/misc'))

    def tree(self):
        """Every file of the store with its contents' hash, and the outputs: what slotctl left."""
        rc, out = sh('cd %s/store && find . | sort | while read f; do [ -f "$f" ] && sha256sum "$f" || echo "$f"; done'
                     % self.d)
        return out

    def run_sequence(self, **kw):
        outs = []
        for booted, args in (('b', 'install %s/rootfs.tar.gz' % self.d), ('a', 'commit'), ('a', 'commit'),
                             ('a', 'switch b'), ('b', 'rollback'), ('a', 'install --keep-active %s/rootfs.tar.gz' % self.d),
                             ('a', 'status')):
            outs.append(self.slotctl(booted, args, **kw)[1])
        return outs

    # --- Without the boot layer.

    def test_without_the_boot_layer_slotctl_works_on_its_files_alone(self):
        put(self.d + '/misc', misc())
        before = sha(self.d + '/misc')
        outs = self.run_sequence(cmdline='root=/dev/mmcblk0p12')
        self.assertEqual(sha(self.d + '/misc'), before, 'misc written without a record')
        self.assertEqual((self.state('a'), self.state('b'), self.active()), ('good', 'trial 3', 'a'))
        rc, out = sh('ls -d %s/store/slots/*/boot 2>&1' % self.d)
        self.assertNotEqual(rc, 0, 'boot files made without the boot layer: ' + out)
        for o in outs:
            self.assertNotIn('bootctl', o)
            self.assertNotIn('boot layer', o)

    def test_without_a_record_bootctl_makes_no_difference(self):
        """The same steps with bootctl beside slotctl (and no record) and with no bootctl at all."""
        put(self.d + '/misc', misc())
        outs = self.run_sequence(cmdline='root=/dev/mmcblk0p12')
        with_bootctl = self.tree()
        self.setUp()
        put(self.d + '/misc', misc())
        outs2 = self.run_sequence(cmdline='root=/dev/mmcblk0p12', bootctl=False)
        self.assertEqual(outs, outs2)
        self.assertEqual(with_bootctl, self.tree())

    # --- With the boot layer: a record in misc.

    def test_with_a_record_slotctl_tells_the_boot_layer(self):
        put(self.d + '/misc', misc(record((14, 0, 1), (15, 0, 1), last=b'b')))
        self.slotctl('b', 'install %s/rootfs.tar.gz' % self.d)  # into a: on trial, first
        self.assertEqual(self.field(), {'a': (15, 3, 0), 'b': (14, 0, 1)})
        self.assertEqual(get(self.d + '/store/slots/a/boot/label'), b'8 Oct 2026\n')
        rc, out = sh('stat -c %%i %s/store/slots/a/boot/boot.img %s/store/slots/a/usr/share/techo5/kernel/boot-athena.img'
                     % (self.d, self.d))
        self.assertEqual(len(set(out.split())), 1, 'boot/boot.img is not a hard link to the slot\'s kernel: ' + out)
        rc, out = self.slotctl('a', 'status')
        self.assertIn('boot layer', out)
        self.assertIn('slot a: priority 15, tries 3, successful 0 (trial)', out)
        self.slotctl('a', 'commit')
        self.assertEqual(self.field(), {'a': (15, 3, 1), 'b': (14, 0, 1)})
        before = sha(self.d + '/misc')
        self.slotctl('a', 'commit')
        self.assertEqual(sha(self.d + '/misc'), before, 'a second commit wrote misc')
        self.slotctl('a', 'switch b')
        self.assertEqual(self.field(), {'a': (14, 3, 1), 'b': (15, 3, 0)})
        self.slotctl('b', 'rollback')
        self.assertEqual(self.field(), {'a': (14, 3, 1), 'b': (0, 0, 0)})
        self.slotctl('a', 'install --keep-active %s/rootfs.tar.gz' % self.d)  # into b, a stays first
        self.assertEqual(self.field(), {'a': (14, 3, 1), 'b': (14, 3, 0)})
        m = get(self.d + '/misc')
        self.assertEqual(outside(m), outside(misc()), 'misc changed outside the record')

    def test_a_rootfs_with_its_own_kernel_brings_it(self):
        put(self.d + '/misc', misc(record((14, 0, 1), (15, 0, 1), last=b'b')))
        put(self.d + '/store/slots/b/boot/boot.img', b'the kernel b runs with')
        self.slotctl('b', 'install %s/rootfs.tar.gz' % self.d)
        self.assertEqual(get(self.d + '/store/slots/a/boot/boot.img'), self.KERNEL)

    def test_a_rootfs_without_a_kernel_keeps_the_one_running_now(self):
        put(self.d + '/misc', misc(record((14, 0, 1), (15, 0, 1), last=b'b')))
        put(self.d + '/store/slots/b/boot/boot.img', b'the kernel b runs with')
        tar = self.tarball('nokernel.tar.gz', kernel=None)
        rc, out = self.slotctl('b', 'install ' + tar)
        self.assertIn("boot/boot.img: slot b's", out)
        self.assertEqual(get(self.d + '/store/slots/a/boot/boot.img'), b'the kernel b runs with')
        self.assertEqual(get(self.d + '/store/slots/a/boot/label'), b'8 Oct 2026\n')
        self.assertEqual(self.field(), {'a': (15, 3, 0), 'b': (14, 0, 1)})

    def test_no_kernel_anywhere_says_so_and_breaks_nothing(self):
        put(self.d + '/misc', misc(record((14, 0, 1), (15, 0, 1), last=b'b')))
        tar = self.tarball('nokernel.tar.gz', kernel=None)
        rc, out = self.slotctl('b', 'install ' + tar)
        self.assertIn('no kernel in this rootfs or in the other slot', out)
        rc, ls = sh('ls %s/store/slots/a/boot/boot.img' % self.d)
        self.assertNotEqual(rc, 0, 'a boot.img out of nowhere')
        self.assertEqual((self.state('a'), self.state('b')), ('trial 3', 'good'))
        self.assertEqual(self.field()['b'], (14, 0, 1))

    def test_an_install_marks_the_slot_bad_before_changing_it(self):
        put(self.d + '/misc', misc(record((14, 0, 1), (15, 0, 1), last=b'b')))
        bad = self.tarball('broken.tar.gz', init=False)
        rc, out = self.slotctl('b', 'install ' + bad, ok=False)
        self.assertNotEqual(rc, 0)
        self.assertEqual(self.field(), {'a': (0, 0, 0), 'b': (15, 0, 1)})

    def test_commit_on_the_rescue_kernel_leaves_the_record_alone(self):
        put(self.d + '/misc', misc(record((15, 0, 0), (14, 0, 0))))
        put(self.d + '/store/slots/a.state', b'trial 2\n')
        before = sha(self.d + '/misc')
        self.slotctl('a', 'commit', cmdline='ab.kernel=rescue')
        self.assertEqual(self.state('a'), 'good')
        self.assertEqual(sha(self.d + '/misc'), before)

    def test_follow_after_the_boot_layer_fell_back(self):
        """b was on trial and used up its tries; the boot layer booted a. The store follows, and the
        next install goes into b instead of being refused as the running root."""
        put(self.d + '/misc', misc(record((14, 3, 1), (0, 0, 0))))
        put(self.d + '/store/slots/b.state', b'trial 1\n')
        before = sha(self.d + '/misc')
        rc, out = self.slotctl('a', 'follow')
        self.assertIn('marks slot b bad', out)
        self.assertEqual((self.active(), self.state('a'), self.state('b')), ('a', 'good', 'bad'))
        self.assertEqual(sha(self.d + '/misc'), before, 'follow wrote misc')
        self.slotctl('a', 'install %s/rootfs.tar.gz' % self.d)
        self.assertEqual((self.active(), self.state('b')), ('b', 'trial 3'))

    def test_follow_after_a_slot_chosen_in_the_menu(self):
        """The person picked a in the boot menu while b, good, was active: a is named active, b stays good."""
        put(self.d + '/misc', misc(record((14, 3, 1), (15, 3, 1))))
        self.slotctl('a', 'follow')
        self.assertEqual((self.active(), self.state('b')), ('a', 'good'))

    def test_follow_changes_nothing_otherwise(self):
        """Without the boot layer, when the store agrees, or on the rescue kernel, the store is left as it is."""
        put(self.d + '/misc', misc())
        before = self.tree()
        self.slotctl('a', 'follow', cmdline='root=/dev/mmcblk0p12')
        self.assertEqual(self.tree(), before)
        put(self.d + '/misc', misc(record((14, 3, 1), (15, 3, 1), last=b'b')))
        self.slotctl('b', 'follow')
        self.slotctl('a', 'follow', cmdline='ab.kernel=rescue')
        self.assertEqual(self.tree(), before)

    def test_a_broken_record_is_left_alone(self):
        m = bytearray(misc(record((14, 0, 1), (15, 0, 1))))
        m[2060] ^= 0xff
        put(self.d + '/misc', bytes(m))
        self.slotctl('b', 'switch a')
        self.assertEqual(get(self.d + '/misc'), bytes(m))
        self.assertEqual(self.active(), 'a')

    def test_header_args_put_the_root_in_the_slots_boot_image(self):
        """A rootfs asking for header words: boot/boot.img is a copy of its kernel with them, the store's
        GUID read from the disk's GPT (a disk image here, the store partition 3); the rootfs's own image
        is left as released. A slot installed without them gets them when it commits."""
        guid = uuid.UUID('46f0c0bb-f227-4eb6-b82f-66408e13e36d')
        disk = bytearray(34 * 512)
        disk[512:520] = b'EFI PART'
        struct.pack_into('<Q', disk, 512 + 72, 2)    # the entries at LBA 2
        struct.pack_into('<I', disk, 512 + 84, 128)  # 128 bytes each
        disk[1024 + 2 * 128 + 16:1024 + 2 * 128 + 32] = guid.bytes_le
        put(self.d + '/disk', bytes(disk))
        img = bytearray(70000)
        img[0:8] = b'ANDROID!'
        img[64:90] = b'bootopt=64S3 console=ttyS0'
        img[2048:2056] = b'payload!'
        tar = self.tarball('args.tar.gz', kernel=bytes(img),
                           header_args='skip_initramfs rootwait ro init=/init root=PARTUUID=@STORE_PARTUUID@')
        put(self.d + '/misc', misc(record((14, 0, 1), (15, 0, 1), last=b'b')))
        env = 'SLOTCTL_STORE_DISK=%s/disk SLOTCTL_STORE_PARTN=3' % self.d
        self.slotctl('b', 'install ' + tar, env=env)
        want = ('bootopt=64S3 console=ttyS0 skip_initramfs rootwait ro init=/init root=PARTUUID=%s' % guid).encode()
        got = get(self.d + '/store/slots/a/boot/boot.img')
        self.assertEqual(got[64:576].rstrip(b'\0'), want)
        self.assertEqual(got[:64] + got[576:], bytes(img[:64] + img[576:]))
        self.assertEqual(get(self.d + '/store/slots/a/usr/share/techo5/kernel/boot-athena.img'), bytes(img))
        # installed before the words (a hard link to the image as released): commit puts them in
        sh('cd {d}/store/slots/a && ln -f usr/share/techo5/kernel/boot-athena.img boot/boot.img && echo "trial 3" > ../a.state'
           .format(d=self.d))
        self.slotctl('a', 'commit', env=env)
        self.assertEqual(get(self.d + '/store/slots/a/boot/boot.img')[64:576].rstrip(b'\0'), want)


# ------------------------------------------------------------------------------------------ the command line

class CommandLine(unittest.TestCase):
    """techo5-lib.sh's t5_ab_slot and t5_boot_layer (the inits' and boot.sh's switch), t5_watchdog, and
    boot.sh's on_trial."""

    def lib(self, cmdline, call):
        put('/t/cmdline', cmdline.encode() + b'\n')
        return sh('log() { echo "log: $*"; }; . /src/techo5-lib.sh; T5_CMDLINE=/t/cmdline; ' + call)

    def test_ab_slot(self):
        for cmdline, want in (('console=ttyS0 lcm=cronos root=/dev/ram', (1, '')),
                              ('console=ttyS0 ab.slot=b ab.kernel=b watchdog.open_timeout=120', (0, 'b\n')),
                              ('ab.slot=a', (0, 'a\n')),
                              ('ab.kernel=rescue', (1, '')),
                              ('ab.slot=c xab.slot=a', (1, ''))):
            self.assertEqual(self.lib(cmdline, 't5_ab_slot'), want, cmdline)

    def test_boot_layer(self):
        for cmdline, want in (('console=ttyS0 lcm=cronos root=/dev/ram', 1),
                              ('ab.slot=b ab.kernel=b', 0),
                              ('ab.kernel=rescue', 0)):
            self.assertEqual(self.lib(cmdline, 't5_boot_layer')[0], want, cmdline)

    def test_watchdog_without_a_device_does_nothing(self):
        rc, out = self.lib('ab.slot=a', 't5_watchdog; echo rc=$?; pidof watchdog || echo none')
        self.assertEqual(out, 'log: watchdog: no /dev/watchdog\nrc=0\nnone\n')

    def on_trial(self, state, cmdline, rec):
        put('/t/misc', misc(rec))
        put('/store/slots/a.state', state.encode() + b'\n')
        put('/t/cmdline', cmdline.encode() + b'\n')
        rc, out = sh('''mkdir -p /t/bin; cp /src/bootctl /t/bin/bootctl; chmod 755 /t/bin/bootctl
            export PATH=/t/bin:$PATH BOOTCTL_MISC=/t/misc BOOTCTL_CMDLINE=/t/cmdline BOOTCTL_CONF=/nonexistent
            log() { :; }; . /src/techo5-lib.sh; T5_CMDLINE=/t/cmdline
            eval "$(sed -n '/^on_trial() {/,/^}/p' /src/boot.sh)"
            on_trial a''')
        return rc

    def test_on_trial(self):
        good = record((15, 3, 1), (14, 0, 1))
        trial = record((15, 2, 0), (14, 0, 1))
        self.assertEqual(self.on_trial('trial 2', 'root=/dev/ram', None), 0)
        self.assertEqual(self.on_trial('good', 'root=/dev/ram', None), 1, 'without the boot layer: as before')
        self.assertEqual(self.on_trial('good', 'root=/dev/ram', trial), 1, 'not booted by the boot layer')
        self.assertEqual(self.on_trial('good', 'ab.slot=a ab.kernel=a', trial), 0)
        self.assertEqual(self.on_trial('good', 'ab.slot=a ab.kernel=a', good), 1)

    def test_on_trial_on_systemd_boot(self):
        """boot.sh's check, unchanged, on a board with no store whose bootctl reads systemd-boot's entries."""
        def check(cmdline, entry):
            put('/t/cmdline', cmdline.encode() + b'\n')
            rc, out = sh('''rm -rf /t/esp /store/slots/a.state; mkdir -p /t/esp/loader/entries /t/bin
                printf 'default slot-b.conf\\npreferred slot-a.conf\\n' > /t/esp/loader/loader.conf
                : > /t/esp/loader/entries/{e}; : > /t/esp/loader/entries/slot-b.conf
                cp /src/bootctl /t/bin/bootctl; chmod 755 /t/bin/bootctl
                export PATH=/t/bin:$PATH BOOTCTL_ESP=/t/esp BOOTCTL_CMDLINE=/t/cmdline BOOTCTL_CONF=/nonexistent
                log() {{ :; }}; . /src/techo5-lib.sh; T5_CMDLINE=/t/cmdline
                eval "$(sed -n '/^on_trial() {{/,/^}}/p' /src/boot.sh)"
                on_trial a'''.format(e=entry))
            return rc
        self.assertEqual(check('ab.slot=a ab.kernel=a', 'slot-a+2-1.conf'), 0)
        self.assertEqual(check('ab.slot=a ab.kernel=a', 'slot-a.conf'), 1)
        self.assertEqual(check('root=PARTUUID=x', 'slot-a+2-1.conf'), 1, 'not booted by the boot layer')

    def test_the_scripts_parse_under_busybox(self):
        for f in ('bootctl', 'slotctl', 'techo5-lib.sh', 'boot.sh'):
            self.assertEqual(sh('sh -n /src/' + f), (0, ''), f)
        for f in ('init', os.path.join('athena', 'init')):
            with open(os.path.join(LINUX, f), 'rb') as fh:
                put('/src/init.check', fh.read().replace(b'\r\n', b'\n'))
            self.assertEqual(sh('sh -n /src/init.check'), (0, ''), f)


if __name__ == '__main__':
    unittest.main()
