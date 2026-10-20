import importlib.util
import hashlib
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


recovery = load('kernel-recovery')
fpga = load('extract-fpga-fw')


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.image = self.root / 'boot.img'
        data = bytearray(6144)
        data[:8] = data[1024:1032] = b'ANDROID!'
        struct.pack_into('<8I', data, 1032, 16, 0x40080000, 16, 0x69000000,
                         0, 0, 0x48000000, 2048)
        checksum = hashlib.sha1()
        for payload in (bytes(16), bytes(16), b''):
            checksum.update(payload)
            checksum.update(struct.pack('<I', len(payload)))
        data[1600:1620] = checksum.digest()
        self.image.write_bytes(data)
        self.bundle = self.root / 'recovery'
        recovery.prepare(self.image, self.bundle)

    def test_corrupt_backup_prevents_flash(self):
        saved = self.bundle / 'boot-known-good.img'
        data = bytearray(saved.read_bytes())
        data[-1] ^= 1
        saved.write_bytes(data)
        with patch.object(recovery.subprocess, 'run') as run:
            with self.assertRaises(ValueError):
                recovery.flash(self.image, self.bundle, 'device')
            run.assert_not_called()

    def test_incompatible_image_prevents_flash(self):
        data = bytearray(self.image.read_bytes())
        struct.pack_into('<I', data, 1036, 0x41000000)
        self.image.write_bytes(data)
        with patch.object(recovery.subprocess, 'run') as run:
            with self.assertRaises(ValueError):
                recovery.flash(self.image, self.bundle, 'device')
            run.assert_not_called()

    def test_truncated_or_oversize_image_rejected(self):
        for data in (self.image.read_bytes()[:2048], bytes(recovery.LIMIT + 1)):
            self.image.write_bytes(data)
            with self.assertRaises(ValueError):
                recovery.image_info(self.image)

    def test_existing_backup_is_not_overwritten(self):
        with self.assertRaises(FileExistsError):
            recovery.prepare(self.image, self.bundle)

    def test_corrupt_candidate_prevents_flash(self):
        data = bytearray(self.image.read_bytes())
        data[2048] ^= 1
        self.image.write_bytes(data)
        with patch.object(recovery.subprocess, 'run') as run:
            with self.assertRaises(ValueError):
                recovery.flash(self.image, self.bundle, 'device')
            run.assert_not_called()

    def test_dry_run_has_no_device_access(self):
        with patch.object(recovery.subprocess, 'run') as run:
            commands = recovery.flash(self.image, self.bundle, 'device', dry_run=True)
            run.assert_not_called()
        self.assertEqual(commands[0][1:5], ['-s', 'device', 'flash', 'boot'])
        self.assertEqual(commands[1], ['fastboot', '-s', 'device', 'reboot'])

    def test_wrong_device_is_not_flashed(self):
        with patch.object(recovery.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, 'other\tfastboot\n')) as run:
            with self.assertRaises(ValueError):
                recovery.flash(self.image, self.bundle, 'device')
            self.assertEqual(run.call_count, 1)

    def test_flash_failure_does_not_reboot(self):
        results = [subprocess.CompletedProcess([], 0, 'device\tfastboot\n'),
                   subprocess.CalledProcessError(1, 'fastboot')]
        with patch.object(recovery.subprocess, 'run', side_effect=results) as run:
            with self.assertRaises(subprocess.CalledProcessError):
                recovery.flash(self.image, self.bundle, 'device')
            self.assertEqual(run.call_count, 2)

    def test_invalid_vendor_image_fails_extraction(self):
        for data in (b'', b'ANDROID!', self.image.read_bytes()):
            with self.assertRaises(ValueError):
                fpga.extract(data)


if __name__ == '__main__':
    unittest.main()
