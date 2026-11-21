#!/usr/bin/env python3
"""Offline tests for tools/linux/thinksmart/thinksmart-firmware, which makes a ThinkSmart View's
Wi-Fi, Bluetooth, GPU and speaker firmware from its own partitions at boot. No unit.

    python3 tools/test_thinksmart_firmware.py

It runs under busybox as test_lk2nd_ab.py runs lk2nd-ab (Docker's Alpine, or this machine's busybox).
The modem and vendor partitions are directories of made-up files. The two Wi-Fi containers are
checked against encoders written here from qca-swiss-army-knife's ath10k-fwencoder and
ath10k-bdencoder (the same layout, padding and field order), which on the real files give the bytes
postmarketOS's firmware for this board has.
"""
import os
import struct
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.dont_write_bytecode = True
import test_lk2nd_ab as h  # noqa: E402  (its busybox or Docker harness)

setUpModule = h.setUpModule
tearDownModule = h.tearDownModule

SCRIPT = os.path.join(HERE, 'linux', 'thinksmart', 'thinksmart-firmware')
VERSION = 'WLAN.NPL.1.6-00163-QCANPLSWPZ-1'
BOARD_NAME = b'bus=sdio,vendor=0271,device=0801,subsystem-vendor=0000,subsystem-device=0000,variant=WCBN3510A'
W = 'ath10k/QCA9379/hw1.0'
Z = 'qcom/msm8953/lenovo/cd-18781y'


def ie(kind, data, pad):
    return struct.pack('<II', kind, len(data)) + data + pad * ((4 - len(data) % 4) % 4)


def fw_container(otp, fw, timestamp):
    """ath10k-fwencoder --create --firmware-version=VERSION --otp --firmware --set-wmi-op-version=tlv
    --set-htt-op-version=tlv --set-fw-api=6 --features=wowlan,ignore-otp-result,mfp-support."""
    return (b'QCA-ATH10K\0w' + ie(0, VERSION.encode(), b'w') + ie(1, struct.pack('<I', timestamp), b'w') +
            ie(4, otp, b'w') + ie(3, fw, b'w') + ie(2, b'\xc0\x10', b'w') + ie(5, struct.pack('<I', 4), b'w') +
            ie(6, struct.pack('<I', 3), b'w'))


def board_container(board):
    """ath10k-bdencoder: one board, under this board's name."""
    inner = ie(0, BOARD_NAME, b'm') + ie(1, board, b'm')
    return b'QCA-ATH10K-BOARD\0mmm' + struct.pack('<II', 0, len(inner)) + inner


class Firmware(unittest.TestCase):
    def setUp(self):
        h.sh('rm -rf %s/f' % h.TMP)
        self.f = h.TMP + '/f'
        with open(SCRIPT, 'rb') as f:
            h.put(self.f + '/bin/thinksmart-firmware', f.read().replace(b'\r\n', b'\n'), 0o755)
        # Odd sizes, so every field needs its padding.
        self.otp = bytes(range(256)) * 9 + b'\x01'
        self.fw = b'\xaa\x55' * 4097 + b'\x07'
        self.board = b'\x5a' * 8122 + b'BD'
        img = self.f + '/modem/image/'
        for name, data in (('otp30.bin', self.otp), ('qwlan30.bin', self.fw), ('bdwlan30.bin', self.board),
                           ('btfwnpla.tlv', b'rampatch'), ('btnvnpla.bin', b'nvm')):
            h.put(img + name, data)
        h.put(self.f + '/modem/verinfo/ver_info.txt',
              ('{\n    "btfm": "BTFM.NPL.1.0.4-00002-QCABTFMSWPZ-1", \n    "wlan": "%s", \n}\n' % VERSION).encode())
        h.put(self.f + '/vendor/firmware/a506_zap.mdt', b'mdt')
        h.put(self.f + '/vendor/firmware/a506_zap.b02', b'b02')
        h.put(self.f + '/data/tas.bin', b'\x00\x00\x7f\x00')
        h.sh('mkdir -p %s/store %s/target' % (self.f, self.f))

    def run_it(self, vendor=True, env=''):
        st, out = h.sh('cd %s && PATH=$PATH:%s/bin FIRMWARE=%s/store DSP=%s/data/tas.bin TMPDIR=%s %s '
                       'thinksmart-firmware %s/target %s/modem %s'
                       % (self.f, self.f, self.f, self.f, self.f, env, self.f, self.f,
                          self.f + '/vendor' if vendor else ''))
        self.assertEqual(st, 0, out)
        return out

    def target(self, name):
        return h.get('%s/target/%s' % (self.f, name))

    def test_containers_are_ath10k_tools_bytes(self):
        out = self.run_it(env='TIMESTAMP=1713283196')
        self.assertIn('firmware-sdio-6.bin: made, ' + VERSION, out)
        self.assertEqual(self.target(W + '/firmware-sdio-6.bin'), fw_container(self.otp, self.fw, 1713283196))
        self.assertEqual(self.target(W + '/board-2.bin'), board_container(self.board))

    def test_timestamp_defaults_to_zero(self):
        self.run_it()
        self.assertEqual(self.target(W + '/firmware-sdio-6.bin'), fw_container(self.otp, self.fw, 0))

    def test_links(self):
        self.run_it()
        for name, want in (('qca/rampatch_00150100.bin', b'rampatch'), ('qca/nvm_00150100.bin', b'nvm'),
                           (Z + '/a506_zap.mdt', b'mdt'), (Z + '/a506_zap.b02', b'b02'),
                           ('tas5728m_dsp_lenovo_cd-18781y.bin', b'\x00\x00\x7f\x00')):
            self.assertEqual(self.target(name), want, name)
            st, _ = h.sh('[ -L %s/target/%s ]' % (self.f, name))
            self.assertEqual(st, 0, '%s is a link, not a copy' % name)

    def test_the_stores_own_files_win(self):
        for name in ('qca/rampatch_00150100.bin', 'qca/nvm_00150100.bin', W + '/firmware-sdio-6.bin',
                     W + '/board-2.bin', Z + '/a506_zap.mdt', 'tas5728m_dsp_lenovo_cd-18781y.bin'):
            h.put('%s/store/%s' % (self.f, name), b'the store')
        out = self.run_it()
        self.assertEqual(out, '')
        st, listing = h.sh('find %s/target -type f -o -type l' % self.f)
        self.assertEqual(listing.strip(), '')

    def test_nothing_to_make_it_from(self):
        h.sh('rm -rf %s/modem/image/* %s/modem/verinfo %s/data/tas.bin' % (self.f, self.f, self.f))
        out = self.run_it(vendor=False)
        for name in ('qca/rampatch_00150100.bin', 'firmware-sdio-6.bin', 'board-2.bin', 'a506_zap.mdt',
                     'tas5728m_dsp_lenovo_cd-18781y.bin'):
            self.assertIn(name, out)
        self.assertEqual(out.count('not made'), 6, out)
        st, listing = h.sh('find %s/target -type f -o -type l' % self.f)
        self.assertEqual(listing.strip(), '')

    def test_no_wlan_version_no_firmware(self):
        h.put(self.f + '/modem/verinfo/ver_info.txt', b'{ "btfm": "x" }\n')
        out = self.run_it()
        self.assertIn('firmware-sdio-6.bin: not made', out)
        self.assertIsNone(self.target(W + '/firmware-sdio-6.bin'))
        self.assertEqual(self.target(W + '/board-2.bin'), board_container(self.board))

    def test_leaves_no_temporary_files(self):
        self.run_it()
        st, listing = h.sh('ls -a %s | grep thinksmart-firmware' % self.f)
        self.assertEqual(listing.strip(), '')


if __name__ == '__main__':
    unittest.main()
