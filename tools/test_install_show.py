#!/usr/bin/env python3
"""Offline tests for install-show.py: no unit, no network.

    python3 tools/test_install_show.py

The unlock check before the flash (#168) and what the wait for the rescue console says about where
the unit is (#166), with adb and fastboot stood in for.
"""
import contextlib
import importlib.util
import io
import os
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('install_show', os.path.join(HERE, 'install-show.py'))
inst = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inst)
lib = sys.modules.get('techo5lib') or __import__('techo5lib')

IDS = ('1d6b', '0104')


class Getvar(unittest.TestCase):
    def test_answer(self):
        out = 'kaeru-version: 2.0.0\nFinished. Total time: 0.001s'
        self.assertEqual(inst.getvar(out, 'kaeru-version'), '2.0.0')

    def test_bootloader_prefix(self):
        self.assertEqual(inst.getvar('(bootloader) unlock_status: true\nOKAY', 'unlock_status'), 'true')

    def test_no_answer(self):
        out = "getvar:kaeru-version FAILED (remote: 'GetVar Variable Not found')\nFinished. Total time: 0.001s"
        self.assertEqual(inst.getvar(out, 'kaeru-version'), '')
        self.assertEqual(inst.getvar('kaeru-version:\nFinished.', 'kaeru-version'), '')


class ScriptFastboot:
    """A fastboot stood in for by a short Python script, run as the real one would be."""
    serial = 'SERIAL1'

    def __init__(self, script):
        self.base = [sys.executable, '-c', script]


class FastbootGetvar(unittest.TestCase):
    def test_answer(self):
        fb = ScriptFastboot('import sys; print("kaeru-version: 2.0.0"); print("Finished.")')
        self.assertEqual(inst.fastboot_getvar(fb, 'kaeru-version'), '2.0.0')

    def test_asked_of_the_unit(self):
        fb = ScriptFastboot('import sys; print("unlock_status: " + " ".join(sys.argv[1:]))')
        self.assertEqual(inst.fastboot_getvar(fb, 'unlock_status'), '-s SERIAL1 getvar unlock_status')

    def test_no_answer_in_time_is_not_reported(self):
        # A bootloader that sits on the question must not hold up the install for good.
        fb = ScriptFastboot('import time; time.sleep(30)')
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(inst.fastboot_getvar(fb, 'kaeru-version', seconds=1), '')
        self.assertIn('no answer', out.getvalue())


class FastbootNudge(unittest.TestCase):
    """A continue sent to a unit that left fastboot between the check and the command waits for it
    forever; the nudge gives up after its time and the install carries on (#257)."""

    def test_a_continue_nobody_answers_gives_up(self):
        fb = lib.Fastboot.__new__(lib.Fastboot)
        fb.serial, fb.base = 'SERIAL1', [sys.executable, '-c', 'import time; time.sleep(30)']
        start = time.time()
        code, out = fb.run('continue', timeout=1)
        self.assertEqual(code, -1)
        self.assertIn('no answer', out)
        self.assertLess(time.time() - start, 10)

    def test_an_answer_comes_back(self):
        fb = lib.Fastboot.__new__(lib.Fastboot)
        fb.serial, fb.base = 'SERIAL1', [sys.executable, '-c', 'print("resuming boot")']
        self.assertEqual(fb.run('continue', timeout=10), (0, 'resuming boot'))


class UnlockProblem(unittest.TestCase):
    def test_tested_unlock_passes(self):
        self.assertEqual(inst.unlock_problem('cronos', '2.0.0', 'true'), '')

    def test_no_answer_passes(self):
        # The tested unlock not naming itself is no reason to stop.
        self.assertEqual(inst.unlock_problem('cronos', '', ''), '')
        self.assertEqual(inst.unlock_problem('cronos', '', 'true'), '')

    def test_tested_unlock_with_a_build_tag_passes(self):
        # The genuine amonet-cronos v2.0.1 reports its version with a build tag (#257).
        self.assertEqual(inst.unlock_problem('cronos', '2.0.0-g7b11500', 'true'), '')
        self.assertIn('kaeru-version 2.0.01', inst.unlock_problem('cronos', '2.0.01', 'true'))

    def test_other_kaeru_stops(self):
        self.assertIn('kaeru-version 1.3', inst.unlock_problem('cronos', '1.3', 'true'))

    def test_old_unlock_stops(self):
        self.assertIn('unlock_status: false', inst.unlock_problem('cronos', '', 'false'))

    def test_help_says_what_was_written(self):
        self.assertIn('Nothing has been written', inst.unlock_help('S'))
        self.assertIn('refused the image', inst.unlock_help('S', refused=True))
        self.assertIn('fastboot -s S continue', inst.unlock_help('S'))

    def test_other_boards_not_checked(self):
        for dev in ('checkers', 'crown'):
            self.assertEqual(inst.unlock_problem(dev, '1.3', 'false'), '')


class FakeAdb:
    def __init__(self, state):
        self._state = state

    def state(self):
        return self._state


class FakeFastboot:
    serial = 'SERIAL1'

    def __init__(self, present):
        self._present = present

    def present(self):
        return self._present


def usb_tree(root, name, ids, tty=False):
    d = os.path.join(root, name)
    os.makedirs(d)
    for f, v in zip(('idVendor', 'idProduct'), ids):
        with open(os.path.join(d, f), 'w') as fh:
            fh.write(v + '\n')
    if tty:
        os.makedirs(os.path.join(d, name + '-1.0', 'tty', 'ttyACM0'))


class Whereabouts(unittest.TestCase):
    def test_twrp(self):
        self.assertIn('TWRP', inst.unit_whereabouts(FakeAdb('recovery'), FakeFastboot(False), IDS))

    def test_android(self):
        self.assertIn('LineageOS', inst.unit_whereabouts(FakeAdb('device'), FakeFastboot(False), IDS))

    def test_fastboot(self):
        h = inst.unit_whereabouts(FakeAdb(''), FakeFastboot(True), IDS)
        self.assertIn('fastboot -s SERIAL1 continue', h)

    @unittest.skipUnless(sys.platform.startswith('linux'), 'reads /sys/bus/usb on Linux')
    def test_nothing(self):
        if inst.usb_without_port(IDS):
            self.skipTest('a console without a port is plugged into this computer')
        self.assertEqual(inst.unit_whereabouts(FakeAdb(''), FakeFastboot(False), IDS), '')

    def test_usb_without_port(self):
        with tempfile.TemporaryDirectory() as root:
            self.assertFalse(inst.usb_without_port(IDS, root))
            usb_tree(root, '1-1', ('18d1', '4ee7'))
            self.assertFalse(inst.usb_without_port(IDS, root))
            usb_tree(root, '1-2', IDS, tty=True)
            self.assertFalse(inst.usb_without_port(IDS, root))
            usb_tree(root, '1-3', IDS)
            self.assertTrue(inst.usb_without_port(IDS, root))

    def test_screen_hint_covers_both_screens(self):
        self.assertIn('color bars', inst.SCREEN_HINT)
        self.assertIn('RESCUE', inst.SCREEN_HINT)


if __name__ == '__main__':
    unittest.main(verbosity=2)
