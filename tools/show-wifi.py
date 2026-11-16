#!/usr/bin/env python3
"""Give an Echo Show running TECHO5 a Wi-Fi network over its USB cable, and restart it to join.

For a Show that has no network it knows: installed from TWRP without --wifi, or moved to a house with
a different Wi-Fi. It talks to the unit's USB serial console, the same one the installer uses, so
nothing else has to work first.

    python3 tools/show-wifi.py MyNetwork
    python3 tools/show-wifi.py MyNetwork --serial <serial>

The passphrase is asked for here and turned into WPA's key on this computer; only the key reaches the
unit. The network replaces the ones the unit had.
"""
import argparse
import os
import re
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import (CONSOLE_TECHO5, Console, ask_wifi, check_serial_access, console_exchange, fail,  # noqa: E402
                       list_consoles, note, run_main, step, wait_for, wifi_conf)

CONF = '/data/techo5-linux/wpa_supplicant.conf'


def quote(s):
    return "'" + s.replace("'", "'\\''") + "'"


# CONF_FORMAT is the wpa_supplicant configuration as a printf format for the unit's shell, with the name and
# WPA's key (both hex) as its two arguments: the same form boot.sh writes from Android's saved networks.
# Sent as escapes rather than as the text itself, since a tab typed into the console's shell can be taken
# for completion.
CONF_FORMAT = r'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid=%s\n\tscan_ssid=1\n\tpsk=%s\n}\n'


def wifi_keys(lines):
    """The name and key, as hex, from wifi_conf's two lines."""
    kv = dict(line.split('=', 1) for line in lines.strip().split('\n'))
    return kv['ssid'], kv['psk']


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('network', help='the Wi-Fi network name')
    ap.add_argument('--serial', help="the unit's serial; found when only one Show is on USB")
    ap.add_argument('--passphrase-file', help='a file holding the passphrase, for running from a script')
    a = ap.parse_args()

    step('the unit')
    check_serial_access()
    serial = a.serial
    if not serial:
        ports = list_consoles(CONSOLE_TECHO5)
        if len(ports) != 1:
            fail('found %d Shows on USB; pass --serial' % len(ports))
        port, serial = ports[0]
        if not serial:
            # Windows does not always report the USB serial: the unit's kernel command line has it.
            serial = (console_exchange(port, "tr ' ' '\\n' < /proc/cmdline | sed -n 's/^androidboot.serialno=//p'", 8) or '').strip()
        if not serial:
            fail('the Show on %s did not say its serial; pass --serial' % port)
    console = Console(serial, CONSOLE_TECHO5)
    if 'UNIT-OK' not in (console.run('echo UNIT-OK', 8) or ''):
        fail('%s does not answer on its USB console: keep the cable in and try again' % serial)
    note('%s on %s' % (serial, console.port))

    step('Wi-Fi')
    if a.passphrase_file:
        with open(a.passphrase_file) as f:
            # Only the line's end goes: a passphrase may begin or end with spaces.
            ssid, psk = wifi_keys(wifi_conf(a.network, f.read().rstrip('\r\n')))
    else:
        ssid, psk = wifi_keys(ask_wifi(a.network))
    o = console.run("mkdir -p %s && (umask 077; printf '%s' %s %s > %s.tmp) && mv -f %s.tmp %s && sync && echo WIFI-OK"
                    % (os.path.dirname(CONF), CONF_FORMAT, ssid, psk, CONF, CONF, CONF), 15)
    if 'WIFI-OK' not in (o or ''):
        fail('writing the Wi-Fi network failed:\n%s' % o)
    note("'%s' saved (only its key)" % a.network)
    console.run('sync; (sleep 2; reboot || /bin/busybox.static reboot -f) >/dev/null 2>&1 &', 3)

    step('joining')
    note('restarting; this takes a minute or two')
    time.sleep(20)
    joined = {}

    def settled():
        o = console.run("ip -4 addr show wlan0 | sed -n 's/.*inet \\([0-9.]*\\).*/\\1/p'; "
                        "grep 'wifi: ' /run/boot.log | tail -1", 8) or ''
        lines = o.split('\n')
        if re.match(r'^\d+\.\d+\.\d+\.\d+$', lines[0].strip()):
            joined['ip'] = lines[0].strip()
            return True
        if 'not associated' in o:
            joined['why'] = lines[-1]
            return True
        return False
    wait_for("the unit to join '%s'" % a.network, 180, settled, 5)
    if 'ip' not in joined:
        fail("%s did not join '%s' (%s). A wrong passphrase is the usual reason: run this again and type it\n"
             '   carefully (it is case sensitive and not shown as you type).' % (serial, a.network, joined.get('why', '').strip()))
    print("\nJoined '%s' as %s. It asks to be added in Home Assistant, or shows the clock, within a minute."
          % (a.network, joined['ip']))


if __name__ == '__main__':
    run_main(main)
