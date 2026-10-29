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

    def test_the_scripts_parse_under_busybox(self):
        for f in ('bootctl', 'slotctl', 'techo5-lib.sh', 'boot.sh'):
            self.assertEqual(sh('sh -n /src/' + f), (0, ''), f)
        for f in ('init', os.path.join('athena', 'init')):
            with open(os.path.join(LINUX, f), 'rb') as fh:
                put('/src/init.check', fh.read().replace(b'\r\n', b'\n'))
            self.assertEqual(sh('sh -n /src/init.check'), (0, ''), f)


if __name__ == '__main__':
    unittest.main()
