#!/usr/bin/env python3
"""Offline tests for what release.ps1 checks a Skylight release with: tools/check-image.py and the
Skylight kernel's check-boot.py and check-source.py. Nothing here builds a kernel: the images are
small made-up ones, packed by the real pack-sky.py.

    python3 tools/test_skylight_release.py
"""
import gzip
import hashlib
import importlib.util
import io
import os
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
KDIR = os.path.join(HERE, 'linux', 'skylight', 'kernel')


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


chk_img = load('check_image', os.path.join(HERE, 'check-image.py'))
chk_boot = load('check_boot', os.path.join(KDIR, 'check-boot.py'))
chk_src = load('check_source', os.path.join(KDIR, 'check-source.py'))

BOOT_CMDLINE = ('console=ttyS2,1500000n8 earlycon=uart8250,mmio32,0xfe660000 panic=10 loglevel=7 ro rootwait '
                'root=PARTLABEL=techo5-store init=/init vt.global_cursor_default=0')
INSTALLER_CMDLINE = 'console=ttyS2,1500000n8 panic=10 loglevel=7 rdinit=/init techo5.installer=1'
CONFIG = b'CONFIG_LOCALVERSION="-skylight"\nCONFIG_IKCONFIG=y\n'


def newc(entries):
    """A gzip'd newc cpio of [(name, mode, data)], as build.sh's cpio_gz makes it."""
    out = b''
    for i, (name, mode, data) in enumerate(entries + [('TRAILER!!!', 0, b'')]):
        n = name.encode() + b'\0'
        h = b'070701' + b''.join(b'%08X' % v for v in (i + 1, mode, 0, 0, 1, 0, len(data), 0, 0, 0, 0, len(n), 0))
        out += h + n
        out += bytes(-len(out) % 4) + data
        out += bytes(-len(out) % 4)
    return gzip.compress(out, mtime=0)


def ext4(label, size=1 << 16):
    b = bytearray(size)
    b[1024 + 56:1024 + 58] = b'\x53\xef'
    b[1024 + 120:1024 + 120 + len(label)] = label
    return bytes(b)


def kernel(config=CONFIG):
    k = bytearray(4096)
    k[0x38:0x3c] = b'ARM\x64'
    return bytes(k) + b'IKCFG_ST' + gzip.compress(config, mtime=0) + b'IKCFG_ED'


def dtb():
    body = b'skylight,cal150\0rockchip,rk3566\0' + bytes(64)
    return struct.pack('>II', 0xD00DFEED, 8 + len(body)) + body


def bmp(w=1920, h=1080):
    return b'BM' + bytes(16) + struct.pack('<ii', w, h) + bytes(64)


def installer_ramdisk(**over):
    data = ext4(b'techo5-data')
    files = {
        'init': (0o100755, b'#!/bin/busybox sh\n'),
        'SOURCES': (0o100644, b'bin/busybox: BusyBox v1.37.0 (made up)\n  Source: https://busybox.net/\n'),
        'bin/busybox': (0o100755, b'\x7fELF busybox'),
        'bin/reboot-normal': (0o100755, b'\x7fELF'),
        'installer/install.sh': (0o100755, b'#!/bin/sh\ncp firmware lib/firmware/brcm\n'),
        'installer/data.img.gz': (0o100644, gzip.compress(data, mtime=0)),
        'installer/data.img.info': (0o100644, b'%d %s\n' % (len(data), hashlib.sha256(data).hexdigest().encode())),
    }
    files.update(over)
    dirs = [('.', 0o40755, b''), ('bin', 0o40755, b''), ('installer', 0o40755, b'')]
    return newc(dirs + [(n, m, d) for n, (m, d) in sorted(files.items()) if d is not None])


class Packed:
    """Images packed by the real pack-sky.py from made-up parts."""

    def __init__(self, tmp):
        self.tmp = tmp
        self.write('Image', kernel())
        self.write('sky.dtb', dtb())
        self.write('logo.bmp', bmp())
        self.write('empty.cpio.gz', newc([('.', 0o40755, b'')]))

    def write(self, name, data):
        with open(os.path.join(self.tmp, name), 'wb') as f:
            f.write(data)
        return os.path.join(self.tmp, name)

    def pack(self, ramdisk, cmdline, out='boot.img', logo='logo.bmp'):
        p = lambda n: os.path.join(self.tmp, n)  # noqa: E731
        subprocess.run([sys.executable, '-I', os.path.join(KDIR, 'pack-sky.py'), '--kernel', p('Image'),
                        '--dtb', p('sky.dtb'), '--ramdisk', p(ramdisk), '--logo', p(logo), '--cmdline', cmdline,
                        '--out', p(out)], check=True, capture_output=True)
        with open(p(out), 'rb') as f:
            return f.read()

    def boot(self):
        return self.pack('empty.cpio.gz', BOOT_CMDLINE)

    def installer(self, **over):
        self.write('inst.cpio.gz', installer_ramdisk(**over))
        return self.pack('inst.cpio.gz', INSTALLER_CMDLINE, 'installer.img')


def reid(img):
    """The header id made again after a test changed a part."""
    ksize, rsize, ssize = struct.unpack_from('<I', img, 8)[0], struct.unpack_from('<I', img, 16)[0], \
        struct.unpack_from('<I', img, 24)[0]
    dsize = struct.unpack_from('<I', img, 1648)[0]
    pad = lambda n: (n + 2047) // 2048 * 2048  # noqa: E731
    k = 2048
    r = k + pad(ksize)
    s = r + pad(rsize)
    d = s + pad(ssize)
    h = hashlib.sha1()
    for blob in (img[k:k + ksize], img[r:r + rsize], img[s:s + ssize], b'', img[d:d + dsize]):
        h.update(blob)
        h.update(struct.pack('<I', len(blob)))
    return img[:576] + h.digest().ljust(32, b'\0') + img[608:]


class CheckBoot(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.TemporaryDirectory()
        self.p = Packed(self.d.name)

    def tearDown(self):
        self.d.cleanup()

    def test_the_boot_image_passes(self):
        self.assertEqual(chk_boot.check(self.p.boot()), [])

    def test_the_installer_image_passes_as_the_installer_only(self):
        img = self.p.installer()
        self.assertEqual(chk_boot.check(img, installer=True), [])
        self.assertTrue(chk_boot.check(img))
        self.assertTrue(chk_boot.check(self.p.boot(), installer=True))

    def test_a_damaged_image_fails(self):
        img = bytearray(self.p.boot())
        img[2048 + 100] ^= 1
        self.assertTrue(any('SHA-1' in e for e in chk_boot.check(bytes(img))))

    def test_a_boot_ramdisk_with_files_fails(self):
        self.p.write('r.cpio.gz', newc([('.', 0o40755, b''), ('init', 0o100755, b'x')]))
        img = self.p.pack('r.cpio.gz', BOOT_CMDLINE)
        self.assertTrue(any('not empty' in e for e in chk_boot.check(img)))

    def test_installer_ramdisk_contents(self):
        cases = {
            'a key': dict(**{'root/.ssh/authorized_keys': (0o100600, b'ssh-ed25519 AAAAC3Nz test')}),
            'firmware': dict(**{'lib/firmware/brcm/BCM4345C5.hcd': (0o100644, b'fw')}),
            'a missing file': dict(SOURCES=(0, None)),
            'a data image unlike its info': {'installer/data.img.info': (0o100644, b'1 00\n')},
            'an init that does not run': {'init': (0o100644, b'#!/bin/busybox sh\n')},
        }
        for name, over in cases.items():
            with self.subTest(name):
                self.assertTrue(chk_boot.check(self.p.installer(**over), installer=True), name)

    def test_the_command_lines(self):
        img = self.p.pack('empty.cpio.gz', BOOT_CMDLINE + ' techo5.installer=1')
        self.assertTrue(chk_boot.check(img))
        img = self.p.pack('empty.cpio.gz', BOOT_CMDLINE.replace('root=PARTLABEL=techo5-store', 'root=/dev/mmcblk0p14'))
        self.assertTrue(chk_boot.check(img))

    def test_another_board_or_logo_fails(self):
        self.p.write('sky.dtb', dtb().replace(b'skylight,cal150', b'pine64,pinetab2'))
        self.assertTrue(any('skylight,cal150' in e for e in chk_boot.check(self.p.boot())))
        self.p.write('sky.dtb', dtb())
        self.p.write('logo.bmp', bmp(800, 1280))
        self.assertTrue(any('logo.bmp' in e for e in chk_boot.check(self.p.boot())))

    def test_main(self):
        path = self.p.write('ok.img', self.p.boot())
        inst = self.p.write('inst.img', self.p.installer())
        out = io.StringIO()
        sys.stdout, old = out, sys.stdout
        try:
            self.assertEqual(chk_boot.main(['check-boot.py', path]), 0)
            self.assertEqual(chk_boot.main(['check-boot.py', '--installer', path]), 1)
            self.assertEqual(chk_boot.main(['check-boot.py', '--installer', '--kernel-of', path, inst]), 0)
            # Another kernel in the boot image: the installer's is not its.
            self.p.write('Image', kernel(b'CONFIG_OTHER=y\n'))
            other = self.p.write('other.img', self.p.boot())
            self.assertEqual(chk_boot.main(['check-boot.py', '--installer', '--kernel-of', other, inst]), 1)
            self.assertIn("not %s's" % other, out.getvalue())
        finally:
            sys.stdout = old


def source_tar(path, kver='6.18.56', top=None, mtime=1760659200, owner=0, extra=(), drop=(), config=CONFIG,
               dts=None):
    top = top or 'linux-%s-techo5-skylight' % kver
    a, b, c = kver.split('.')

    def ours(name):
        with open(os.path.join(KDIR, name), 'rb') as f:
            return f.read().replace(b'\r\n', b'\n')
    files = {
        'Makefile': b'VERSION = %s\nPATCHLEVEL = %s\nSUBLEVEL = %s\n' % (a.encode(), b.encode(), c.encode()),
        'arch/arm64/boot/dts/rockchip/rk3566-skylight-cal150.dts': dts if dts is not None else ours('rk3566-skylight-cal150.dts'),
        'drivers/gpu/drm/panel/panel-skylight-cal150.c': ours('panel-skylight-cal150.c'),
        'arch/arm64/boot/dts/rockchip/Makefile': b'dtb-$(CONFIG_ARCH_ROCKCHIP) += rk3566-skylight-cal150.dtb\n',
        'drivers/gpu/drm/panel/Makefile': b'obj-$(CONFIG_DRM_PANEL_SKYLIGHT_CAL150) += panel-skylight-cal150.o\n',
        'drivers/gpu/drm/panel/Kconfig': b'config DRM_PANEL_SKYLIGHT_CAL150\n',
        'drivers/video/logo/logo_linux_clut224.ppm': b'P3\n1920 1064\n255\n0 0 0\n',
        'techo5-skylight.config': config,
    }
    for n in drop:
        del files[n]
    files.update(dict(extra))
    with tarfile.open(path, 'w:xz') as t:
        for n, data in sorted(files.items()):
            ti = tarfile.TarInfo('%s/%s' % (top, n))
            ti.size, ti.mtime, ti.uid, ti.gid = len(data), mtime, owner, owner
            t.addfile(ti, io.BytesIO(data))


class CheckSource(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.TemporaryDirectory()
        self.p = Packed(self.d.name)
        self.img = self.p.boot()
        self.tar = os.path.join(self.d.name, 'src.tar.xz')

    def tearDown(self):
        self.d.cleanup()

    def test_the_tree_passes_and_names_its_version(self):
        source_tar(self.tar)
        errors, kver = chk_src.check(self.tar, self.img, CONFIG)
        self.assertEqual((errors, kver), ([], '6.18.56'))

    def test_the_installer_image_has_the_same_source(self):
        source_tar(self.tar)
        self.assertEqual(chk_src.check(self.tar, self.p.installer())[0], [])

    def test_failures(self):
        cases = {
            'another configuration': dict(config=b'CONFIG_OTHER=y\n'),
            'build output': dict(extra=[('vmlinux', b'x')]),
            'a changed board file': dict(dts=b'/dts-v1/; /* not ours */\n'),
            'a missing panel driver': dict(drop=['drivers/gpu/drm/panel/panel-skylight-cal150.c']),
            'no Kconfig entry': dict(extra=[('drivers/gpu/drm/panel/Kconfig', b'menu\n')]),
            'the stock logo': dict(extra=[('drivers/video/logo/logo_linux_clut224.ppm', b'P3\n80 80\n255\n')]),
            'owned by a user': dict(owner=1000),
            'another top directory': dict(top='linux-6.18.56'),
            'a home directory': dict(extra=[('home/someone/notes', b'x')]),
        }
        for name, kw in cases.items():
            with self.subTest(name):
                source_tar(self.tar, **kw)
                self.assertTrue(chk_src.check(self.tar, self.img)[0], name)

    def test_a_kernel_without_its_configuration_fails(self):
        self.p.write('Image', bytes(0x38) + b'ARM\x64' + bytes(4096))
        img = self.p.boot()
        source_tar(self.tar)
        self.assertTrue(any('carries no configuration' in e for e in chk_src.check(self.tar, img)[0]))

    def test_a_build_config_that_differs(self):
        source_tar(self.tar)
        self.assertTrue(chk_src.check(self.tar, self.img, b'CONFIG_X=y\n')[0])


class CheckImage(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.d.cleanup()

    def path(self, name, data, gz=True):
        p = os.path.join(self.d.name, name)
        with open(p, 'wb') as f:
            f.write(gzip.compress(data, mtime=0) if gz else data)
        return p

    @staticmethod
    def dirent(name, kind=1):
        n = name.encode()
        return struct.pack('<IHBB', 12, 8 + len(n), len(n), kind) + n

    def tar(self, files):
        p = os.path.join(self.d.name, 'rootfs.tar.gz')
        with tarfile.open(p, 'w:gz') as t:
            for n, data in files.items():
                ti = tarfile.TarInfo(n)
                ti.size = len(data)
                t.addfile(ti, io.BytesIO(data))
        return p

    def test_a_clean_image(self):
        img = bytes(5000) + self.dirent('okay_nabu.tflite') + b'github.com/Someone/techo5 ' + bytes(5000)
        self.assertEqual(chk_img.scan(self.path('a.img.gz', img), deny=['someone'], no_file=['BCM4345C5.hcd'],
                                      models=['okay_nabu.tflite']), [])

    def test_keys(self):
        for key in (b'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5', b'ecdsa-sha2-nistp256 AAAAE2Vj',
                    b'-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA'):
            with self.subTest(key[:12]):
                self.assertTrue(chk_img.scan(self.path('k.img', bytes(100) + key + bytes(100), gz=False)))

    def test_a_key_across_two_chunks(self):
        old = chk_img.CHUNK
        chk_img.CHUNK = 1000
        try:
            img = bytes(990) + b'ssh-rsa AAAAB3Nza' + bytes(3000)
            self.assertEqual(len(chk_img.scan(self.path('k.img.gz', img))), 1)
            self.assertIn('(1)', chk_img.scan(self.path('k.img.gz', img))[0])
        finally:
            chk_img.CHUNK = old

    def test_denied_words(self):
        img = b'built in /home/Someone/techo5 by someone ' + bytes(100)
        found = chk_img.scan(self.path('d.img.gz', img), deny=['someone', 'zz'])
        self.assertEqual(found, ["'someone' (2)"])
        # Inside a longer word it is another word.
        self.assertEqual(chk_img.scan(self.path('e.img.gz', b'awesomeoneday'), deny=['someone']), [])

    def test_files_and_models(self):
        img = bytes(64) + self.dirent('brcmfmac43456-sdio.bin') + self.dirent('i_am_private.tflite') + bytes(64)
        found = chk_img.scan(self.path('f.img.gz', img), no_file=['brcmfmac43456-sdio.bin'],
                             models=['okay_nabu.tflite'])
        self.assertEqual(len(found), 2)
        self.assertTrue(any('brcmfmac43456-sdio.bin' in f for f in found))
        self.assertTrue(any('i_am_private.tflite' in f for f in found))

    def test_a_tarball(self):
        ok = self.tar({'./usr/share/techo5/models/okay_nabu.tflite': b'm', './root/.ssh/authorized_keys': b''})
        self.assertEqual(chk_img.scan(ok, no_file=['BCM4345C5.hcd'], models=['okay_nabu.tflite'],
                                      empty_keys=True), [])
        bad = self.tar({'./lib/firmware/brcm/BCM4345C5.hcd': b'fw', './root/.ssh/authorized_keys': b'x',
                        './usr/share/techo5/models/other.tflite': b'm'})
        found = chk_img.scan(bad, no_file=['BCM4345C5.hcd'], models=['okay_nabu.tflite'], empty_keys=True)
        self.assertEqual(len(found), 3, found)

    def test_main(self):
        p = self.path('m.img.gz', bytes(100))
        out = io.StringIO()
        sys.stdout, old = out, sys.stdout
        try:
            self.assertEqual(chk_img.main(['check-image.py', '--models', 'a.tflite', p]), 0)
            p = self.path('n.img.gz', b'ssh-dss AAAAB3')
            self.assertEqual(chk_img.main(['check-image.py', p]), 1)
        finally:
            sys.stdout = old


if __name__ == '__main__':
    unittest.main()
