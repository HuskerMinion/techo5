#!/usr/bin/env python3
"""Offline tests for install-show.py: no unit, no network.

    python3 tools/test_install_show.py

The unlock check before the flash (#168) and what the wait for the rescue console says about where
the unit is (#166), with adb and fastboot stood in for.
"""
import contextlib
import hashlib
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


class FakeRelease:
    def __init__(self, version, names):
        self.version = version
        self.names = names

    def signed(self, name):
        return {'sha256': 'x'} if name in self.names else None

    def asset(self, name):
        return '/work/' + name


class BootImage(unittest.TestCase):
    def setUp(self):
        self.saved = (inst.fetch_json, inst.Release)
        self.pages = {}
        self.urls = []

        def fetch_json(url):
            self.urls.append(url)
            page = int(url.split('&page=')[1])
            if isinstance(self.pages.get(page), Exception):
                raise self.pages[page]
            return self.pages.get(page, [])
        inst.fetch_json = fetch_json
        inst.Release = lambda repo, tag, work: FakeRelease(tag, ['techo5-boot-crown-%s.img' % tag])

    def tearDown(self):
        inst.fetch_json, inst.Release = self.saved

    def test_walks_past_the_first_page(self):
        self.pages[1] = [{'tag_name': 'v1.2.%d' % n, 'assets': []} for n in range(30, 10, -1)]
        self.pages[2] = [{'tag_name': 'v1.2.10', 'assets': [{'name': 'techo5-boot-crown-v1.2.10.img'}]}]
        got = inst.boot_image(FakeRelease('v1.2.40', []), '/work', 'crown')
        self.assertEqual(got, '/work/techo5-boot-crown-v1.2.10.img')
        self.assertEqual(len(self.urls), 3)

    def test_own_image_needs_no_list(self):
        rel = FakeRelease('v1.2.40', ['techo5-boot-crown-v1.2.40.img'])
        self.assertEqual(inst.boot_image(rel, '/work', 'crown'), '/work/techo5-boot-crown-v1.2.40.img')
        self.assertEqual(self.urls, [])

    def test_unreadable_list_says_so(self):
        self.pages[1] = lib.urllib.error.HTTPError('u', 403, 'rate limit exceeded', {}, None)
        with self.assertRaises(lib.Fail) as c:
            inst.boot_image(FakeRelease('v1.2.40', []), '/work', 'crown')
        self.assertIn('could not be read', str(c.exception))
        self.assertIn('60 requests an hour', str(c.exception))
        self.assertIn('--boot', str(c.exception))


class LockedBootloader(unittest.TestCase):
    def test_the_text_fastboot_prints(self):
        self.assertTrue(inst.locked_bootloader("FAILED (remote: 'the command you input is restricted on locked hw')"))
        self.assertFalse(inst.locked_bootloader('FAILED (remote: image is not a boot image)'))
        self.assertFalse(inst.locked_bootloader(None))

    def test_help_points_to_the_unlock(self):
        text = inst.locked_help('Echo Show 5 1st gen')
        self.assertIn('unlock is not done, or did not take', text)
        self.assertIn('docs/install.md#unlock-the-bootloader-first', text)
        self.assertIn('Nothing has been written', text)


class FakeConsole:
    """A console stood in for: answers is what each run gives in turn (None for no answer), gone what
    the first run leaves in console.gone."""
    waiting_hint = staticmethod(lambda: '')
    blocked = ''

    def __init__(self, answers, gone=''):
        self.answers, self.asked, self.gone = list(answers), [], gone

    def run(self, command, wait=8):
        self.asked.append(command)
        self.gone = ''
        return self.answers.pop(0) if self.answers else None


class StoreFailed(unittest.TestCase):
    def fail_text(self, console, erased, o=None, seconds=180):
        with tempfile.TemporaryDirectory() as d, contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(lib.Fail) as c:
                inst.store_failed(console, d, 'making the slot store', o, erased, 'WAY-BACK', seconds)
            text = str(c.exception)
            saved = os.path.exists(os.path.join(d, 'slot-store-restart.txt'))
        return text, saved

    def test_a_restart_is_said_as_one_and_the_evidence_saved(self):
        console = FakeConsole(['pstore lines\n----- rescue.log\nlog'], gone='port went away')
        text, saved = self.fail_text(console, 'yes')
        self.assertIn('the unit restarted, or its USB went away, while making the slot store (port went away)', text)
        self.assertIn('is erased', text)
        self.assertIn('#if-the-install-stops-at-the-slot-store', text)
        self.assertIn('slot-store-restart.txt', text)
        self.assertNotIn('None', text)
        self.assertTrue(saved)
        self.assertIn('console-ramoops', console.asked[0])
        self.assertIn('/data/techo5-linux/rescue.log', console.asked[0])

    def test_nothing_erased_says_how_to_go_back(self):
        text, _ = self.fail_text(FakeConsole([], gone='gone'), '', seconds=0)
        self.assertIn('Nothing was erased yet', text)
        self.assertIn('WAY-BACK', text)
        self.assertNotIn('finish the install by hand', text)

    def test_the_erasing_step_may_have_erased(self):
        text, _ = self.fail_text(FakeConsole([], gone='gone'), 'maybe', seconds=0)
        self.assertIn('may already be erased', text)

    def test_a_unit_that_never_comes_back_gets_the_by_hand_commands(self):
        text, saved = self.fail_text(FakeConsole([], gone='gone'), 'yes', seconds=0)
        self.assertIn('did not come back', text)
        self.assertIn('mount -t pstore', text)
        self.assertFalse(saved)

    def test_a_failure_without_a_restart_shows_the_output(self):
        text, _ = self.fail_text(FakeConsole([]), 'yes', o='mkfs said no')
        self.assertIn('making the slot store failed:\nmkfs said no', text)
        self.assertIn('finish the install by hand', text)


class NoFastboot:
    @staticmethod
    def present():
        return False


class FirstBoot(unittest.TestCase):
    def watch(self, answers):
        console = FakeConsole(answers)
        with contextlib.redirect_stdout(io.StringIO()):
            return inst.first_boot(console, NoFastboot, [False], seconds=1, every=0)

    def test_daemon_seen_is_up(self):
        self.assertFalse(self.watch(['slot=- daemon=-', 'slot=a- daemon=123-']))

    def test_slot_started_then_console_gone_is_probably_up(self):
        self.assertTrue(self.watch(['slot=- daemon=-', 'slot=a- daemon=-']))

    def test_back_in_rescue_is_a_failure(self):
        with self.assertRaises(lib.Fail):
            self.watch(['slot=a- daemon=-', 'slot=- daemon=-'])

    def test_never_seen_is_a_failure(self):
        with self.assertRaises(lib.Fail):
            self.watch([])

    def test_slot_a_without_its_daemon_to_the_end_is_a_failure(self):
        # The console answers to the end, so it did not go away: the daemon never started.
        class Stuck(FakeConsole):
            def run(self, command, wait=8):
                return 'slot=a- daemon=-'
        with self.assertRaises(lib.Fail), contextlib.redirect_stdout(io.StringIO()):
            inst.first_boot(Stuck([]), NoFastboot, [False], seconds=1, every=0)


class DownloadChecked(unittest.TestCase):
    """A download that comes out wrong is tried once more, and the error says how much arrived (#393)."""

    def fetch_with(self, bodies, want):
        served = list(bodies)
        old = lib._open
        lib._open = lambda url: contextlib.closing(io.BytesIO(served.pop(0)))
        self.addCleanup(setattr, lib, '_open', old)
        out = os.path.join(tempfile.mkdtemp(), 'image.gz')
        with contextlib.redirect_stdout(io.StringIO()):
            lib.download_checked('https://example.invalid/image.gz', out, hashlib.sha256(want).hexdigest())
        return out

    def test_a_second_try_that_comes_out_right_is_kept(self):
        out = self.fetch_with([b'cut', b'whole image'], b'whole image')
        with open(out, 'rb') as f:
            self.assertEqual(f.read(), b'whole image')

    def test_two_wrong_says_how_much_arrived(self):
        with self.assertRaises(lib.Fail) as e:
            self.fetch_with([b'cut', b'cut'], b'whole image')
        self.assertIn('3 bytes arrived', str(e.exception))


class GiftInstall(unittest.TestCase):
    def run_main(self, *argv):
        old = sys.argv
        sys.argv = ['install-show.py'] + list(argv)
        try:
            inst.main()
        except lib.Fail as e:
            return str(e)
        finally:
            sys.argv = old

    def test_cannot_be_combined_with_name_wifi_or_key(self):
        for extra in (['--name', 'Kitchen'], ['--wifi', 'Net'], ['--wifi-passphrase-file', 'p.txt'],
                      ['--key-file', 'k.key']):
            err = self.run_main('--for-someone-else', *extra)
            self.assertIn('--for-someone-else', err or '', extra)
            self.assertIn(extra[0], err, extra)

    def test_gift_writes_the_marker_and_no_name_key_or_wifi(self):
        cmd = inst.provision_command(True, None, None, None, None)
        self.assertIn('/data/misc/techo5/firstrun', cmd)   # echod's layout.StateDir on the Shows
        # an earlier install's name, key and networks are removed; the configuration holds no network
        self.assertIn('rm -f /data/misc/techo5/name /data/misc/techo5/psk /data/techo5-linux/wifi.conf', cmd)
        self.assertIn('/data/techo5-linux/wpa_supplicant.conf', cmd)
        self.assertIn('ctrl_interface=/run/wpa update_config=0', cmd)
        self.assertNotIn('network={', cmd)
        self.assertNotIn('> /data/misc/techo5/name', cmd)
        self.assertNotIn('> /data/misc/techo5/psk', cmd)

    def test_usual_install_writes_name_and_key_and_no_marker(self):
        cmd = inst.provision_command(False, 'Kitchen', 'KEY', None, None)
        self.assertIn('/data/misc/techo5/name', cmd)
        self.assertIn('/data/misc/techo5/psk', cmd)
        self.assertNotIn('firstrun', cmd)

    def test_gift_still_takes_an_ssh_key(self):
        cmd = inst.provision_command(True, None, None, 'ssh-ed25519 AAAA', None)
        self.assertIn('firstrun', cmd)
        self.assertIn('authorized_keys', cmd)


if __name__ == '__main__':
    unittest.main(verbosity=2)
