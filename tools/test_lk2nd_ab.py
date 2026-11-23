#!/usr/bin/env python3
"""Offline tests for the ThinkSmart View's boot layer, tools/linux/thinksmart/lk2nd-ab. No unit.

    python3 tools/test_lk2nd_ab.py

lk2nd-ab and bootctl run under busybox, as on the units: in an Alpine container (Docker) as
test_boot_layer.py runs them, else under this machine's own busybox where there is one (every
applet linked into a directory of its own, which is the whole PATH), else the test is skipped and
says so. The units' system1, store, recovery partition and command line are a directory, files
and a stand-in for rebootto that writes down how it was asked to restart.

The boot images are kernel/pack.py's, around a made-up arm64 Image header (its image_size chosen
per test) and a made-up device tree, so the checks lk2nd-ab makes run on the real format.
"""
import gzip
import os
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
LINUX = os.path.join(HERE, 'linux')
TS = os.path.join(LINUX, 'thinksmart')
sys.path.insert(0, os.path.join(TS, 'kernel'))
sys.dont_write_bytecode = True  # no __pycache__ beside pack.py
import pack  # noqa: E402

IMAGE = os.environ.get('TECHO5_TEST_ALPINE',
                       'alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6')
RUN = None  # how scripts run: ('docker', container) or ('busybox', bin directory)
TMP = None  # the files the scripts see, at /t in the container or a temporary directory here
CMDLINE = ('consoleblank=0 video=DSI-1:800x1280,rotate=180 dm-mod.create="system1,,,rw,0 497664 linear '
           'PARTLABEL=system 2048" root=/dev/dm-1 ro rootwait init=/sbin/init')


def skip(reason):
    msg = 'test_lk2nd_ab: SKIPPED: ' + reason
    print(msg, file=sys.stderr)
    if os.environ.get('GITHUB_ACTIONS'):
        print('::warning::' + msg)
    raise unittest.SkipTest(reason)


def setUpModule():
    global RUN, TMP
    if shutil.which('docker'):
        r = subprocess.run(['docker', 'run', '-d', '--rm', IMAGE, 'sleep', '1800'], capture_output=True, text=True)
        if r.returncode == 0:
            RUN = ('docker', r.stdout.strip())
            TMP = '/t'
        elif os.environ.get('CI'):
            raise RuntimeError('docker run %s failed: %s' % (IMAGE, r.stderr.strip()))
    if RUN is None:
        bb = shutil.which('busybox')
        if not bb:
            skip('needs Docker or busybox')
        d = tempfile.mkdtemp(prefix='lk2nd-ab-bin-')
        applets = subprocess.run([bb, '--list'], capture_output=True, text=True).stdout.split()
        for a in applets:
            os.symlink(bb, os.path.join(d, a))
        RUN = ('busybox', d)
        TMP = tempfile.mkdtemp(prefix='lk2nd-ab-')


def tearDownModule():
    if RUN and RUN[0] == 'docker':
        subprocess.run(['docker', 'rm', '-f', RUN[1]], capture_output=True)
    elif RUN:
        shutil.rmtree(RUN[1], ignore_errors=True)
        shutil.rmtree(TMP, ignore_errors=True)


def sh3(script):
    """Run a busybox sh script: (exit status, stdout, stderr)."""
    if RUN[0] == 'docker':
        r = subprocess.run(['docker', 'exec', '-i', RUN[1], 'sh', '-c', script], capture_output=True)
    else:
        r = subprocess.run([os.path.join(RUN[1], 'sh'), '-c', script], capture_output=True,
                           env={'PATH': RUN[1], 'HOME': TMP})
    return r.returncode, r.stdout.decode(errors='replace'), r.stderr.decode(errors='replace')


def sh(script):
    """Run a busybox sh script: (exit status, its output and messages)."""
    st, out, err = sh3(script)
    return st, out + err


def put(path, data, mode=0o644):
    if RUN[0] == 'docker':
        sh('mkdir -p "$(dirname %s)"' % path)
        subprocess.run(['docker', 'exec', '-i', RUN[1], 'sh', '-c', 'cat > %s; chmod %o %s' % (path, mode, path)],
                       input=data, check=True)
    else:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'wb') as f:
            f.write(data)
        os.chmod(path, mode)


def get(path):
    if RUN[0] == 'docker':
        r = subprocess.run(['docker', 'exec', RUN[1], 'cat', path], capture_output=True)
        return r.stdout if r.returncode == 0 else None
    try:
        with open(path, 'rb') as f:
            return f.read()
    except OSError:
        return None


def boot_image(tag, image_size=28 << 20):
    """A boot image as build.sh makes it: a gzip'd arm64 Image header taking image_size in memory,
    told apart by tag, and a device tree."""
    hdr = bytearray(64)
    struct.pack_into('<Q', hdr, 16, image_size)
    hdr[56:60] = b'ARM\x64'
    gz = gzip.compress(bytes(hdr) + tag.encode() * 500, mtime=0)
    dtb = bytearray(b'\xd0\x0d\xfe\xed' + struct.pack('>I', 2000) + bytes(1992))
    dtb[100:100 + len(tag)] = tag.encode()
    limit = pack.KERNEL_MAX
    pack.KERNEL_MAX = max(limit, image_size)  # an image pack.py itself would refuse, for the test
    try:
        return pack.pack(gz, bytes(dtb), CMDLINE), gz, bytes(dtb)
    finally:
        pack.KERNEL_MAX = limit


class Lk2ndAb(unittest.TestCase):
    def setUp(self):
        sh('rm -rf %s/u' % TMP)
        self.u = TMP + '/u'
        for name in ('bootctl',):
            with open(os.path.join(LINUX, name), 'rb') as f:
                put(self.u + '/bin/' + name, f.read().replace(b'\r\n', b'\n'), 0o755)
        with open(os.path.join(TS, 'lk2nd-ab'), 'rb') as f:
            put(self.u + '/bin/lk2nd-ab', f.read().replace(b'\r\n', b'\n'), 0o755)
        put(self.u + '/bin/rebootto', b'#!/bin/sh\necho "restart ${1:-normal}" >> %s/restarts\n' % self.u.encode(), 0o755)
        put(self.u + '/boot/techo5-ab', bytes(4096))
        put(self.u + '/recovery', bytes(4 << 20))
        sh('mkdir -p %s/store/slots/a/boot %s/store/slots/b/boot' % (self.u, self.u))
        self.cmdline('')

    def env(self):
        return ('BOOT={u}/boot STORE={u}/store RECOVERY={u}/recovery CMDLINE={u}/cmdline '
                'REBOOT={u}/bin/rebootto BOOTCTL_CONF=/dev/null BOOTCTL_CMDLINE={u}/cmdline').format(u=self.u)

    def run_ab(self, *args):
        return sh('%s sh %s/bin/lk2nd-ab %s' % (self.env(), self.u, ' '.join(args)))

    def boot(self):
        """lk2nd-ab boot: (exit status, the slot it says to start, its messages)."""
        st, out, err = sh3('%s sh %s/bin/lk2nd-ab boot' % (self.env(), self.u))
        return st, out.strip(), err

    def bootctl(self, *args):
        return sh('%s BOOTCTL_MISC=%s/boot/techo5-ab sh %s/bin/bootctl %s' % (self.env(), self.u, self.u, ' '.join(args)))

    def cmdline(self, words):
        put(self.u + '/cmdline', (CMDLINE + ' ' + words).strip().encode() + b'\n')

    def kernel(self, slot, tag, image_size=28 << 20):
        img, gz, dtb = boot_image(tag, image_size)
        put('%s/store/slots/%s/boot/boot.img' % (self.u, slot), img)
        return img, gz, dtb

    def restarts(self):
        r = get(self.u + '/restarts')
        return r.decode().split('\n')[:-1] if r else []

    def record(self):
        return self.bootctl('-q', 'status')[1]

    def extlinux(self):
        r = get(self.u + '/boot/extlinux/extlinux.conf')
        return r.decode() if r else ''

    def new_unit(self):
        """A unit as mkimage.sh leaves it: slot a good, its kernel on system1."""
        self.kernel('a', 'A')
        self.assertEqual(self.bootctl('init')[0], 0)
        self.assertEqual(self.bootctl('mark-good', 'a')[0], 0)
        st, out = self.run_ab('install', 'a')
        self.assertEqual(st, 0, out)
        self.cmdline('ab.slot=a')

    def test_without_a_record_it_does_nothing(self):
        put(self.u + '/boot/techo5-ab', bytes(4096))
        self.kernel('a', 'A')
        st, out = self.run_ab('boot')
        self.assertEqual(st, 3, out)
        self.assertEqual(self.restarts(), [])
        self.assertEqual(self.extlinux(), '')

    def test_install_splits_the_image_for_lk2nd(self):
        img, gz, dtb = self.kernel('a', 'A')
        self.bootctl('init')
        st, out = self.run_ab('install', 'a')
        self.assertEqual(st, 0, out)
        conf = self.extlinux()
        k = [l.split()[1] for l in conf.splitlines() if l.strip().startswith('kernel ')][0]
        d = k.rsplit('/', 1)[0]
        self.assertEqual(get(self.u + '/boot' + k), gz)
        self.assertEqual(get(self.u + '/boot' + d + '/apq8053-lenovo-cd-18781y.dtb'), dtb)
        self.assertIn('\tappend ' + CMDLINE + ' ab.slot=a\n', conf)
        self.assertNotIn(pack.TRIAL, conf)
        self.assertEqual(conf.count('label '), 1)

    def test_a_good_slot_with_its_kernel_on_system1_boots_at_once(self):
        self.new_unit()
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.assertEqual(self.restarts(), [])

    def test_a_slot_on_trial_is_started_once_from_recovery_and_takes_a_try(self):
        self.new_unit()
        img, _, _ = self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        st, out = self.run_ab('boot')
        self.assertEqual(self.restarts(), ['restart recovery'], out)
        self.assertIn('slot b: priority 15, tries 2, successful 0', self.record())
        rec = get(self.u + '/recovery')
        self.assertEqual(rec[:64], img[:64])
        self.assertEqual(rec[576:len(img)], img[576:])
        cl = rec[64:576].rstrip(b'\0').decode()
        self.assertEqual(cl, CMDLINE + ' ' + pack.TRIAL + ' ab.slot=b')
        # system1 still starts slot a's kernel
        self.assertIn('ab.slot=a', self.extlinux())

    def test_the_kernel_on_trial_starts_its_slot_without_another_try(self):
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        self.bootctl('set', 'b', 'tries=2')
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'b'), out)
        self.assertEqual(self.restarts(), [])
        self.assertIn('slot b: priority 15, tries 2, successful 0', self.record())

    def test_a_trial_that_never_settles_falls_back_to_the_good_slot(self):
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        for left in (2, 1, 0):
            st, out = self.run_ab('boot')
            self.assertEqual(self.restarts()[-1], 'restart recovery', out)
            self.assertIn('slot b: priority 15, tries %d, successful 0' % left, self.record())
        # Out of tries: system1's own slot, with no restart.
        n = len(self.restarts())
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.assertEqual(len(self.restarts()), n)
        self.assertIn('ab.slot=a', self.extlinux())

    def test_a_committed_slot_moves_its_kernel_to_system1(self):
        self.new_unit()
        _, gz, _ = self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        self.run_ab('boot')
        # boot.sh committed slot b on its trial boot (bootctl mark-good, as slotctl commit does).
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        self.assertEqual(self.bootctl('mark-good', 'b')[0], 0)
        # The next start is system1's, with slot a's kernel: slot b's goes there, then a restart.
        self.cmdline('ab.slot=a')
        st, out = self.run_ab('boot')
        self.assertEqual(self.restarts(), ['restart recovery', 'restart normal'], out)
        conf = self.extlinux()
        self.assertIn('ab.slot=b', conf)
        k = [l.split()[1] for l in conf.splitlines() if l.strip().startswith('kernel ')][0]
        self.assertEqual(get(self.u + '/boot' + k), gz)
        self.assertEqual(sh('ls -d %s/boot/k-* | wc -l' % self.u)[1].strip(), '1')
        # And that start boots it with no more restarts.
        self.cmdline('ab.slot=b')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'b'), out)
        self.assertEqual(len(self.restarts()), 2)

    def test_a_kernel_too_big_for_lk2nd_is_marked_bad_before_its_trial(self):
        self.new_unit()
        self.kernel('b', 'B', image_size=46 << 20)
        self.bootctl('set-active', 'b')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.assertIn('lk2nd starts one of', out)
        self.assertEqual(self.restarts(), [])
        self.assertIn('slot b: priority 0, tries 0, successful 0', self.record())

    def test_a_rollback_to_the_good_slot_puts_its_kernel_back(self):
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        self.bootctl('mark-good', 'b')
        self.run_ab('boot')                     # b's kernel to system1, restart
        self.cmdline('ab.slot=b')
        self.bootctl('set', 'a', 'priority=15')
        self.bootctl('set', 'b', 'priority=14')  # slotctl switch a, a slot already good
        st, out = self.run_ab('boot')
        self.assertEqual(self.restarts()[-1], 'restart normal', out)
        self.assertIn('ab.slot=a', self.extlinux())

    def test_the_record_mkimage_writes(self):
        """mkimage.sh's record (its python, taken from the script): slot a good, slot b empty."""
        with open(os.path.join(TS, 'mkimage.sh')) as f:
            src = f.read()
        code = src.split("<<'PY'\n", 1)[1].split('\nPY\n', 1)[0]
        out = os.path.join(tempfile.mkdtemp(prefix='lk2nd-ab-rec-'), 'techo5-ab')
        subprocess.run([sys.executable, '-I', '-c', code, out], check=True)
        with open(out, 'rb') as f:
            put(self.u + '/boot/techo5-ab', f.read())
        shutil.rmtree(os.path.dirname(out))
        rec = self.record()
        self.assertIn('slot a: priority 15, tries 3, successful 1', rec)
        self.assertIn('slot b: priority 0, tries 0, successful 0', rec)
        self.kernel('a', 'A')
        self.run_ab('install', 'a')
        self.cmdline('ab.slot=a')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)

    def committed_b(self):
        """Slot b tried and committed, and its kernel moved to system1 at the next start."""
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        self.run_ab('boot')
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        self.assertEqual(self.bootctl('mark-good', 'b')[0], 0)
        self.cmdline('ab.slot=a')
        st, out = self.run_ab('boot')
        self.assertEqual(self.restarts(), ['restart recovery', 'restart normal'], out)
        return out

    def test_a_commit_leaves_the_old_kernel_in_recovery_to_fall_back_to(self):
        out = self.committed_b()
        self.assertIn('ab.slot=b', self.extlinux())
        img = get(self.u + '/store/slots/a/boot/boot.img')
        rec = get(self.u + '/recovery')
        self.assertEqual(rec[:64], img[:64])
        self.assertEqual(rec[576:len(img)], img[576:])
        cl = rec[64:576].rstrip(b'\0').decode()
        self.assertEqual(cl, CMDLINE + ' ab.slot=a techo5.kernel=fallback')
        self.assertNotIn(pack.TRIAL, cl)
        self.assertIn("recovery: slot a's kernel", out)

    def test_the_fallback_kernel_undoes_the_commit(self):
        self.committed_b()
        # slot b's kernel fails from system1; started from recovery by hand, slot a's.
        self.cmdline('ab.slot=a techo5.kernel=fallback')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.assertEqual(len(self.restarts()), 2)
        rec = self.record()
        self.assertIn('slot b: priority 0, tries 0, successful 0', rec)
        self.assertIn('slot a: priority 14, tries 3, successful 1', rec)
        self.assertIn('ab.slot=a', self.extlinux())
        # And system1 then boots slot a with no restart.
        self.cmdline('ab.slot=a')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.assertEqual(len(self.restarts()), 2)

    def test_a_fallback_kernel_for_a_slot_no_longer_good_boots_nothing(self):
        self.committed_b()
        self.bootctl('mark-bad', 'a')
        self.cmdline('ab.slot=a techo5.kernel=fallback')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (1, ''), out)
        self.assertEqual(self.restarts()[-1], 'restart normal')
        self.assertIn('slot b: priority 15, tries 2, successful 1', self.record())

    def test_a_stale_trial_kernel_boots_nothing(self):
        # Recovery still holds slot b's trial image after b was committed; started again by hand.
        self.committed_b()
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (1, ''), out)
        self.assertIn('stale', out)
        self.assertEqual(self.restarts()[-1], 'restart normal')
        # Nor for a slot marked bad.
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('mark-bad', 'b')
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        n = len(self.restarts())
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (1, ''), out)
        self.assertEqual(self.restarts()[n:], ['restart normal'])

    def test_a_trial_kernel_starts_only_once(self):
        # Every try started; once it runs, recovery holds system1's good kernel instead.
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        for left in (2, 1, 0):
            self.cmdline('ab.slot=a')
            st, out = self.run_ab('boot')
            self.assertEqual(self.restarts()[-1], 'restart recovery', out)
            self.cmdline(pack.TRIAL + ' ab.slot=b')
            st, slot, out = self.boot()
            self.assertEqual((st, slot), (0, 'b'), out)
            cl = get(self.u + '/recovery')[64:576].rstrip(b'\0').decode()
            self.assertEqual(cl, CMDLINE + ' ab.slot=a techo5.kernel=fallback')
        self.assertIn('recovery: slot a\'s kernel, to fall back to', self.run_ab('status')[1])
        # Out of tries, back on a; Recovery from lk2nd's menu now starts slot a's kernel, which
        # gives up on b rather than settling it.
        self.cmdline('ab.slot=a')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.cmdline('ab.slot=a techo5.kernel=fallback')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'a'), out)
        self.assertIn('slot b: priority 0, tries 0, successful 0', self.record())

    def test_a_trial_kernel_with_no_fallback_clears_recovery(self):
        # Slot a good but its kernel gone from the store: recovery is cleared instead.
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        self.run_ab('boot')
        sh('rm -f %s/store/slots/a/boot/boot.img' % self.u)
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        st, slot, out = self.boot()
        self.assertEqual((st, slot), (0, 'b'), out)
        self.assertIn('recovery cleared', out)
        self.assertEqual(get(self.u + '/recovery')[:8], bytes(8))
        self.assertIn('recovery: nothing', self.run_ab('status')[1])

    def test_a_commit_that_cannot_write_the_fallback_says_so(self):
        self.new_unit()
        self.kernel('b', 'B')
        self.bootctl('set-active', 'b')
        self.run_ab('boot')
        self.cmdline(pack.TRIAL + ' ab.slot=b')
        self.boot()
        self.assertEqual(self.bootctl('mark-good', 'b')[0], 0)
        sh('rm -f %s/store/slots/a/boot/boot.img' % self.u)
        self.cmdline('ab.slot=a')
        st, out = self.run_ab('boot')
        self.assertEqual(self.restarts()[-1], 'restart normal', out)
        self.assertIn('ab.slot=b', self.extlinux())
        self.assertIn('NO WAY BACK', out)
        self.assertEqual(get(self.u + '/recovery')[:8], bytes(8))

    def test_no_bootable_slot_is_rescue(self):
        self.new_unit()
        self.bootctl('mark-bad', 'a')
        st, out = self.run_ab('boot')
        self.assertEqual(st, 1, out)
        self.assertEqual(self.restarts(), [])


if __name__ == '__main__':
    unittest.main()
