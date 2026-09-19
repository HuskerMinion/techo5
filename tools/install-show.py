#!/usr/bin/env python3
"""Install TECHO5 on an Echo Show 5 (2nd gen, cronos) running LineageOS 18.1, in one command.

The 1st gen (checkers) is experimental: it runs the same kernel commit on LineageOS 18.1 from
2026-09-04 on, and installs with --boot, a boot image built for it (its kernel with Bluetooth), since
releases carry the 2nd gen's only.

    python3 tools/install-show.py --serial <serial> --name Kitchen --dry-run
    python3 tools/install-show.py --serial <serial> --name Kitchen

Windows, Linux and macOS alike; needs Python 3, adb and fastboot. Nothing is built: the release's boot
image (LineageOS's kernel rebuilt with Bluetooth, and TECHO5's rescue environment, with no SSH key) and
root filesystem are downloaded and checked. Each step is checked before the next:

  1. checks    adb sees the unit as cronos on the LineageOS kernel TECHO5's is built from
  2. backup    with Rooted debugging on, LineageOS's boot image into backups/<serial>/ (the way back)
  3. release   the boot image and root filesystem, checked against their checksums
  4. push      the root filesystem onto the unit's storage, checked by md5
  5. flash     the boot image, from the bootloader's fastboot
  6. store     over the USB serial console: this unit's own vendor tree (Wi-Fi and Bluetooth drivers,
               firmware) is kept, LineageOS's system partition becomes the slot store (THIS ERASES
               LINEAGEOS), the root filesystem goes into slot a, and the name, the Home Assistant key and
               an SSH key (--ssh-key) are written
  7. watch     the first boot from slot a to a running daemon

The Home Assistant key is kept in backups/<serial>/home-assistant.key (api.psk on a unit installed
before that name) and reused on a later run, so Home Assistant keeps the device. Undo: TWRP stays in
recovery; flash the LineageOS zip and the LineageOS boot image.
"""
import argparse
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import (CONSOLE_TECHO5, Adb, Console, Fastboot, Release, default_dir, fail, fetch_json,  # noqa: E402
                       md5, need, new_api_key, note, run_main, step, valid_api_key, wait_for)

REPO = 'HuskerMinion/techo5'
# The LineageOS kernel commit TECHO5's kernel is rebuilt from: the vendor modules only load on it.
KERNEL_RELEASE = '4.9.337-g8d928c5176cc'
WIFI_MODULE = 'vendor/lib/modules/mt76x8_wlan.ko'


def quote(s):
    return "'" + s.replace("'", "'\\''") + "'"


def default_key_file(backup):
    """backups/<serial>/home-assistant.key, as on the Dot; a unit installed while it was api.psk keeps
    that file, so a later run reuses the key Home Assistant already has."""
    new = os.path.join(backup, 'home-assistant.key')
    old = os.path.join(backup, 'api.psk')
    return old if os.path.exists(old) and not os.path.exists(new) else new


def show_version(tag):
    m = re.match(r'^v(\d+)\.(\d+)\.(\d+)$', tag)
    return tuple(int(x) for x in m.groups()) if m else None


def boot_image(rel, work):
    """The release's boot image, or, when it carries none (the boot image changes rarely, so most
    releases don't), the one from the newest earlier Show release that does."""
    name = 'techo5-boot-%s.img' % rel.version
    if name in rel.sums:
        return rel.asset(name)
    want = show_version(rel.version)
    try:
        releases = fetch_json('https://api.github.com/repos/%s/releases?per_page=100' % REPO)
    except Exception as e:
        fail('release %s has no boot image, and the list of earlier releases could not be read: %s'
             % (rel.version, e))
    for r in releases:
        v = show_version(r['tag_name'])
        name = 'techo5-boot-%s.img' % r['tag_name']
        if v and want and v < want and any(x['name'] == name for x in r['assets']):
            note('release %s has no boot image of its own; using %s\'s' % (rel.version, r['tag_name']))
            return Release(REPO, r['tag_name'], work).asset(name)
    fail('no release up to %s has a boot image' % rel.version)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--serial', required=True, help="the unit's adb serial (adb devices)")
    ap.add_argument('--name', required=True, help='the name Home Assistant shows, e.g. Kitchen')
    ap.add_argument('--release', default='latest', help='a release tag, or latest')
    ap.add_argument('--key-file', help='where the Home Assistant key is kept (default backups/<serial>/home-assistant.key)')
    ap.add_argument('--ssh-key', help='an SSH public key the unit accepts from the start (SSH is switched on)')
    ap.add_argument('--boot', help='a boot image you built (docs/building.md) instead of the release\'s')
    ap.add_argument('--rootfs', help='a root filesystem you built instead of the release\'s')
    ap.add_argument('--dry-run', action='store_true', help='download and check the release; write nothing')
    ap.add_argument('--force', action='store_true', help='do not ask before erasing LineageOS')
    ap.add_argument('--backups', default=default_dir('TECHO5_BACKUPS', 'backups'))
    ap.add_argument('--work', default=default_dir('TECHO5_WORK', 'build'))
    ap.add_argument('--adb', default='adb')
    ap.add_argument('--fastboot', default='fastboot')
    a = ap.parse_args()

    backup = os.path.join(a.backups, a.serial)
    key_file = a.key_file or default_key_file(backup)
    adb = Adb(a.serial, a.adb)
    fastboot = Fastboot(a.serial, a.fastboot)
    console = Console(a.serial, CONSOLE_TECHO5)

    # ------------------------------------------------------------------------------------ 1. checks
    step('checks')
    need(a.adb, 'install the Android platform tools (adb and fastboot)')
    need(a.fastboot, 'install the Android platform tools (adb and fastboot)')
    if '\n' in a.name or len(a.name) > 31:
        fail('the name must be one line of at most 31 characters')
    pub = None
    if a.ssh_key:
        with open(os.path.expanduser(a.ssh_key)) as f:
            pub = f.read().strip()
        if not pub.startswith(('ssh-', 'ecdsa-', 'sk-')):
            fail('%s does not look like an SSH public key' % a.ssh_key)
    for f in (a.boot, a.rootfs):
        if f and not os.path.exists(f):
            fail('no file at %s' % f)
    state = adb.state()
    if state != 'device':
        fail("adb does not see %s running LineageOS (state '%s'): turn on USB debugging and accept this computer" % (a.serial, state))
    dev = adb.sh('getprop ro.product.device')
    if dev not in ('cronos', 'checkers'):
        fail("%s reports '%s', not an Echo Show 5 (cronos, 2nd gen; checkers, 1st gen)" % (a.serial, dev))
    if dev == 'checkers' and not a.boot:
        # A release's boot image carries the 2nd gen's kernel and device trees: on a 1st gen it would
        # not bring the screen or the Wi-Fi up. Only a boot image built for it may go on.
        fail('the Echo Show 5 1st gen is experimental: give --boot the boot image built for it')
    kr = adb.sh('uname -r')
    if kr != KERNEL_RELEASE:
        fail('%s runs kernel %s, not %s: install the LineageOS 18.1 build the getting started guide links' % (a.serial, kr, KERNEL_RELEASE))
    note('%s, LineageOS kernel %s' % (dev, kr))

    # ------------------------------------------------------------------------------------ 2. backup
    step('backup')
    os.makedirs(backup, exist_ok=True)
    os.makedirs(a.work, exist_ok=True)
    los_boot = os.path.join(backup, 'boot-lineage.img')
    if os.path.exists(los_boot):
        note('LineageOS boot image already kept: %s' % los_boot)
    else:
        adb.root()
        if adb.sh('id').startswith('uid=0'):
            if not adb.pull('/dev/block/mmcblk0p9', los_boot + '.partial'):
                fail('reading the LineageOS boot partition failed')
            want = adb.sh('md5sum /dev/block/mmcblk0p9').split(' ')[0]
            if md5(los_boot + '.partial') != want:
                fail('LineageOS boot image md5 mismatch')
            os.replace(los_boot + '.partial', los_boot)
            note('LineageOS boot image: %s' % los_boot)
            wifi = adb.sh('grep -c SSID /data/misc/apexdata/com.android.wifi/WifiConfigStore.xml 2>/dev/null')
            if wifi in ('', '0'):
                fail('LineageOS has no saved Wi-Fi network: join one first (TECHO5 uses it)')
            note('a saved Wi-Fi network')
        else:
            note('adb is not root (Rooted debugging off): no boot image backup; the LineageOS zip has one')
            note('make sure LineageOS is on your Wi-Fi: TECHO5 joins the network it saved')

    # ------------------------------------------------------------------------------------ 3. release
    step('the release')
    rel = Release(REPO, a.release, a.work)
    version = rel.version
    if a.rootfs:
        rootfs = os.path.abspath(a.rootfs)
        note('root filesystem: your own, %s' % rootfs)
    else:
        rootfs = rel.rootfs('arm')
        note('root filesystem %s checked against the signed manifest' % os.path.basename(rootfs))
    if a.boot:
        boot = os.path.abspath(a.boot)
        note('boot image: your own, %s' % boot)
    else:
        boot = boot_image(rel, a.work)
        note('boot image %s checked' % os.path.basename(boot))
    if a.dry_run:
        print('\nDry run: TECHO5 %s downloaded and checked in %s; nothing written to the unit.' % (version, rel.dir))
        return

    # ------------------------------------------------------------------------------------ 4. push
    step('root filesystem onto the unit')
    name = os.path.basename(rootfs)
    adb.push(rootfs, '/sdcard/Download/' + name)
    if adb.sh('md5sum /sdcard/Download/' + name).split(' ')[0] != md5(rootfs):
        fail('md5 mismatch after pushing ' + name)
    note('/sdcard/Download/%s ok' % name)
    os.makedirs(os.path.dirname(key_file) or '.', exist_ok=True)
    if os.path.exists(key_file):
        with open(key_file) as f:
            psk = f.read().strip()
        if not valid_api_key(psk):
            fail('the key in %s is not 32 bytes of base64' % key_file)
        note('Home Assistant key: the existing one in %s' % key_file)
    else:
        psk = new_api_key()
        with open(key_file, 'w') as f:
            f.write(psk)
        note('Home Assistant key: new, in %s' % key_file)

    # ------------------------------------------------------------------------------------ 5. flash
    step('flash the boot image')
    adb.reboot('bootloader')
    wait_for('fastboot', 90, fastboot.present, 3)
    code, out = fastboot.run('flash', 'boot', boot)
    if code != 0:
        fail('fastboot flash boot failed: ' + out)
    fastboot.run('continue')
    note('booting the rescue environment (no slot store yet)')
    nudged = [False]

    def rescue_up():
        if 'RESCUE-UP' in (console.run('test -e /run/techo5/slot || echo RESCUE-UP', 4) or ''):
            return True
        # A leftover bootloader request can stop the boot at "hacked fastboot" once; continue it.
        if not nudged[0] and fastboot.present():
            fastboot.run('continue')
            nudged[0] = True
        return False
    wait_for('the rescue console on USB', 300, rescue_up)
    note('rescue console on %s' % console.port)

    # ------------------------------------------------------------------------------------ 6. store
    step('slot store')
    if not a.force:
        print("   Next: LineageOS's system partition (mmcblk0p12) is erased and becomes the slot store.")
        if input('   Type ERASE to go on: ').strip() != 'ERASE':
            fail('stopped before erasing; the unit stays in rescue (flash the LineageOS boot image to go back)')
    tar = '/data/media/0/Download/' + name
    # The vendor tree is this unit's own, from LineageOS: releases don't carry it. Kept on userdata
    # before the system partition is erased, then in the store.
    o = console.run('mkdir -p /data/techo5-linux && tar -cf /data/techo5-linux/vendor.tar -C /android/system vendor && '
                    'tar -tf /data/techo5-linux/vendor.tar %s >/dev/null && echo VENDOR-SAVED' % WIFI_MODULE, 180)
    if 'VENDOR-SAVED' not in (o or ''):
        fail("saving LineageOS's vendor tree failed (nothing was erased):\n%s" % o)
    o = console.run('umount /android 2>/dev/null; slotctl mkstore /dev/mmcblk0p12 --i-know-this-erases-it >/tmp/mkstore.log 2>&1 '
                    '&& echo MKSTORE-OK; tail -3 /tmp/mkstore.log', 300)
    if 'MKSTORE-OK' not in (o or ''):
        fail('mkstore failed:\n%s' % o)
    o = console.run('tar -xf /data/techo5-linux/vendor.tar -C /store && [ -e /store/%s ] && echo VENDOR-OK' % WIFI_MODULE, 180)
    if 'VENDOR-OK' not in (o or ''):
        fail('putting the vendor tree into the store failed (it is kept in /data/techo5-linux/vendor.tar):\n%s' % o)
    o = console.run('STORE=/store slotctl install %s >/tmp/install.log 2>&1 && echo INSTALL-OK; tail -2 /tmp/install.log; '
                    '[ -e /store/slots/a/%s ] || { mkdir -p /store/slots/a/vendor && cp -a /store/vendor/. /store/slots/a/vendor/; }; '
                    '[ -e /store/slots/a/%s ] && rm -f /data/techo5-linux/vendor.tar && echo SLOT-VENDOR-OK; STORE=/store slotctl status'
                    % (tar, WIFI_MODULE, WIFI_MODULE), 900)
    if 'INSTALL-OK' not in (o or ''):
        fail('slot install failed:\n%s' % o)
    if 'SLOT-VENDOR-OK' not in o:
        fail('the vendor tree did not reach slot a:\n%s' % o)
    for line in o.split('\n'):
        if line.startswith('slot a'):
            note(line)
    prov = ("mkdir -p /data/misc/techo5 && printf '%%s\\n' %s > /data/misc/techo5/name && "
            "(umask 077; printf '%%s\\n' %s > /data/misc/techo5/psk)" % (quote(a.name), quote(psk)))
    if pub:
        prov += (" && mkdir -p -m 700 /data/misc/techo5/ssh && (umask 077; printf '%%s\\n' %s > /data/misc/techo5/ssh/authorized_keys)"
                 " && { [ -e /data/misc/techo5/state.json ] || printf '{\"security\":{\"ssh\":true}}\\n' > /data/misc/techo5/state.json; }"
                 % quote(pub))
    o = console.run(prov + ' && sync && echo PROV-OK', 15)
    if 'PROV-OK' not in (o or ''):
        fail('provisioning failed:\n%s' % o)
    note("name '%s' and Home Assistant key%s written" % (a.name, ', SSH key' if pub else ''))
    console.run('sync; (sleep 2; /bin/busybox.static reboot -f) >/dev/null 2>&1 &', 3)

    # ------------------------------------------------------------------------------------ 7. watch
    step('first boot')
    nudged[0] = False

    def slot_up():
        if re.search(r'slot=a- daemon=\d', console.run('echo slot=$(cat /run/techo5/slot 2>/dev/null)- daemon=$(pidof techo5)-', 4) or ''):
            return True
        if not nudged[0] and fastboot.present():
            fastboot.run('continue')
            nudged[0] = True
        return False
    wait_for('slot a with the daemon running', 300, slot_up)
    o = console.run('ip -4 addr show wlan0 | sed -n "s/.*inet \\([0-9.]*\\).*/\\1/p"; cat /etc/techo5-release', 8) or ''
    for line in o.split('\n'):
        note(line)

    print("\nDone. '%s' runs TECHO5 %s from slot a; the slot commits itself after five healthy minutes." % (a.name, version))
    print('Home Assistant finds it as an ESPHome device. When it asks for the encryption key, paste:\n\n    %s\n\n(kept in %s)' % (psk, key_file))
    print("Later versions arrive through Home Assistant's update card.")


if __name__ == '__main__':
    run_main(main)
