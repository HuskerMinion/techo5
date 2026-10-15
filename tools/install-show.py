#!/usr/bin/env python3
"""Install TECHO5 on an Echo Show running LineageOS 18.1, or straight from TWRP, in one command.

Three boards install the same way and run the same daemon, which tells them apart at run time from the
panel the bootloader names in the kernel command line: the Echo Show 5 2nd gen (cronos), the Show 5
1st gen (checkers) and the Show 8 1st gen (crown). All three run the same kernel commit, and the
partitions this script writes are numbered the same on each. Each board takes its own boot image from
the release, since the kernel configuration and the device trees differ.

    python3 tools/install-show.py
    python3 tools/install-show.py --serial <serial> --name Kitchen --dry-run
    python3 tools/install-show.py --serial <serial> --name Kitchen --force
    python3 tools/install-show.py --lineage-zip lineage-18.1-...-cronos.zip --wifi MyNetwork

From TWRP (--lineage-zip), a unit fresh from its unlock never has to start LineageOS: the installer
formats userdata, installs the LineageOS zip from TWRP only for the drivers on its system partition, and
goes on from there. Wi-Fi is then given with --wifi, or chosen on the Show's own screen afterward.

Run with nothing, it finds the unit (asking which, when there are several), asks for a name, and asks
once before anything is erased. Every question has a switch, for running it from a script.

Windows, Linux and macOS alike; needs Python 3, adb and fastboot. Nothing is built: the release's boot
image (LineageOS's kernel rebuilt with Bluetooth, and TECHO5's rescue environment, with no SSH key) and
root filesystem are downloaded and checked. Each step is checked before the next:

  1. checks    adb sees the unit as one of the three boards, on the LineageOS kernel TECHO5's is built from
  2. backup    with Rooted debugging on, LineageOS's boot image into backups/<serial>/ (the way back)
  3. release   the boot image and root filesystem, checked against the release's signed manifest
  4. push      the root filesystem onto the unit's storage, checked by md5
  5. flash     the boot image, from the bootloader's fastboot
  6. store     over the USB serial console: this unit's own vendor tree (Wi-Fi and Bluetooth drivers,
               firmware) is kept, LineageOS's system partition becomes the slot store (THIS ERASES
               LINEAGEOS), the root filesystem goes into slot a, and the name, the Home Assistant key and
               an SSH key (--ssh-key) are written
  7. watch     the first boot from slot a to a running daemon

From TWRP the steps before the flash differ: the unit's small partitions are saved into
backups/<serial>/partitions/ first, and after the one question userdata is formatted, LineageOS's zip is
installed and its Wi-Fi driver checked against the kernel TECHO5's is built from. On a Show 5 2nd gen or
a Show 8 the TECHO5 logo then replaces Amazon's at boot (--amazon-logo keeps Amazon's); see put_logo.

The Home Assistant key is kept in backups/<serial>/home-assistant.key (api.psk on a unit installed
before that name) and reused on a later run, so Home Assistant keeps the device. Undo: TWRP stays in
recovery; flash the LineageOS zip and the LineageOS boot image.
"""
import argparse
import hashlib
import os
import re
import sys
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import (CONSOLE_TECHO5, Adb, Console, Fastboot, Release, ask_name, ask_wifi,  # noqa: E402
                       check_serial_access, confirm, console_hint, default_dir, fail, fetch_json, md5, need,
                       new_api_key, note, pick_unit, run_main, step, valid_api_key, wait_for, wifi_conf,
                       write_private)

REPO = 'HuskerMinion/techo5'
# The LineageOS kernel commit TECHO5's kernel is rebuilt from: the vendor modules only load on it.
# All three boards run this same commit, which is why one daemon and one installer serve them.
KERNEL_RELEASE = '4.9.337-g8d928c5176cc'
# The LineageOS release with that kernel, per board: the 2026-09-04 build. A later one is built from a
# later commit, with another kernel configuration, and its vendor modules do not fit TECHO5's kernel.
LINEAGE_RELEASE = {
    'cronos': 'lineage-18.1-cronos-v0.4',
    'checkers': 'lineage-18.1-checkers-v0.7',
    'crown': 'lineage-18.1-crown-v0.5',
}
LINEAGE_DATE = '20260904'
LINEAGE_ZIP = 'lineage-18.1-' + LINEAGE_DATE + '-UNOFFICIAL-%s.zip'
WIFI_MODULE = 'vendor/lib/modules/mt76x8_wlan.ko'

# The boards this installs on, as `getprop ro.product.device` reports them. They share the SoC, the
# kernel commit, the partition numbers this script writes (MISC p8, boot p9, system p12) and the
# daemon binary; they differ in the panel, the microphone array, the speaker codec and the mute
# driver, which the daemon settles at run time from the board name in the kernel command line.
BOARDS = {
    'cronos': 'Echo Show 5 2nd gen',
    'checkers': 'Echo Show 5 1st gen',
    'crown': 'Echo Show 8 1st gen',
}


def quote(s):
    return "'" + s.replace("'", "'\\''") + "'"


# The partitions saved from a unit in TWRP before anything is written: the bootloader chain, the logo,
# the vendor's small ones, boot and recovery, persist and metadata. Everything the store, userdata and
# cache are not. By number, because TWRP's by-name links differ from LineageOS's.
SMALL_PARTITIONS = (1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 14, 15)

# The TECHO5 logo in place of Amazon's at boot. What the bootloader paints is compiled into kaeru, the
# unlock's bootloader, which lives in the expdb partition: the wordmark is a zlib bundle, and the Show 5
# 2nd gen's and the Show 8's hold the same Amazon picture in the same 6105-byte slot, at different
# places. boot-logo/cronos-wordmark.bin is that slot with the TECHO5 wordmark in it, made by
# tools/linux/patch-lk-logo.py --in-place --colors 16; it has run in both boards' kaeru. It is only ever
# written over the kaeru it was made for (stock), in place, so the header, the size and every pointer
# stay as they are; anything else keeps Amazon's logo. Never into lk (the stock bootloader): changed,
# it relocks the unit. And expdb only this way: a unit whose kaeru is broken does not start at all.
LOGO_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'boot-logo', 'cronos-wordmark.bin')
LOGO_SHA = '1354914c8a2498a7f69a8ebc4a23782ff3369f9e4f980de04f7b9b77734c3403'
LOGO_SIZE = 6105
# Per board: where the slot is in kaeru, kaeru's length, and kaeru's sha256 without and with the logo.
LOGO_KAERU = {
    'cronos': dict(offset=335736, length=399440,  # amonet-cronos v2.0.1
                   stock='a9fb5acb08ee99ab5562da612b329249157f96dabc5702fad6e14ec6cf5a415b',
                   logo='8f062790f7d150928bfcf4967039e38926739f10f22325c5f64a471b29e765cd'),
    'crown': dict(offset=288716, length=481792,  # amonet-crown
                  stock='6a717f0f8bd3502df2e2a811f1a90a2ec83e58f151874e11c2920a9a63dc4fa1',
                  logo='926bc8449b1a9e0ce894c0df1bc66f549c2b294247bc73da9e8998b5e9344f64'),
}


def lineage_zip_date(zip_path):
    """The build date in a LineageOS zip's file name (lineage-18.1-YYYYMMDD-...), or None for a name
    that carries none."""
    m = re.search(r'(20\d{6})', os.path.basename(zip_path))
    return m.group(1) if m else None


def lineage_board(zip_path):
    """The board a LineageOS zip is built for, from its metadata's pre-device line."""
    try:
        with zipfile.ZipFile(zip_path) as z:
            meta = z.read('META-INF/com/android/metadata').decode('utf-8', 'replace')
    except (OSError, KeyError, zipfile.BadZipFile) as e:
        fail('%s is not a LineageOS zip: %s' % (zip_path, e))
    m = re.search(r'^pre-device=(\S+)', meta, re.M)
    if not m:
        fail('%s names no device in its metadata' % zip_path)
    return m.group(1)


# WIFI_CONF_FORMAT is the wpa_supplicant configuration as a printf format for the unit's shell, with the
# name and WPA's key (both hex) as its two arguments: the same form boot.sh writes from Android's saved
# networks. Sent as escapes rather than as the text itself, since a tab typed into the console's shell can
# be taken for completion.
WIFI_CONF_FORMAT = r'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid=%s\n\tpsk=%s\n}\n'


def wifi_keys(lines):
    """The name and key, as hex, from wifi_conf's two lines."""
    kv = dict(line.split('=', 1) for line in lines.strip().split('\n'))
    return kv['ssid'], kv['psk']


def save_partitions(adb, folder):
    """The unit's small partitions into folder, each checked against its size, with SHA256SUMS: a copy of
    this unit's own bootloader chain and logo, which is what turns a bad flash into a short recovery."""
    os.makedirs(folder, exist_ok=True)
    sums = []
    for n in SMALL_PARTITIONS:
        name = adb.sh('grep PARTNAME /sys/class/block/mmcblk0p%d/uevent | cut -d= -f2' % n) or 'part'
        size = int(adb.sh('cat /sys/class/block/mmcblk0p%d/size' % n) or 0) * 512
        out = os.path.join(folder, 'p%d-%s.img' % (n, name))
        adb.exec_out_to_file('dd if=/dev/block/mmcblk0p%d bs=4096 2>/dev/null' % n, out)
        if not size or os.path.getsize(out) != size:
            fail('saving partition %d gave %d bytes, not %d' % (n, os.path.getsize(out), size))
        with open(out, 'rb') as f:
            sums.append('%s  %s' % (hashlib.sha256(f.read()).hexdigest(), os.path.basename(out)))
    for b in ('boot0', 'boot1'):
        out = os.path.join(folder, 'mmcblk0%s.img' % b)
        size = int(adb.sh('cat /sys/class/block/mmcblk0%s/size' % b) or 0) * 512
        adb.exec_out_to_file('dd if=/dev/block/mmcblk0%s bs=4096 2>/dev/null' % b, out)
        if not size or os.path.getsize(out) != size:
            fail('saving %s gave %d bytes, not %d' % (b, os.path.getsize(out), size))
    with open(os.path.join(folder, 'SHA256SUMS'), 'w') as f:
        f.write('\n'.join(sums) + '\n')
    return len(sums)


def sha256(b):
    return hashlib.sha256(b).hexdigest()


def expdb_partition(adb):
    """expdb's partition number, by its name: TWRP numbers some boards' partitions differently from
    Linux (the Show 8's recovery is p11 in TWRP and p10 in LineageOS), so no number is assumed."""
    found = adb.sh('for f in /sys/class/block/mmcblk0p*/uevent; do grep -qx PARTNAME=expdb "$f" && echo "$f"; done')
    nums = re.findall(r'mmcblk0p(\d+)/uevent', found)
    return int(nums[0]) if len(nums) == 1 else 0


def put_logo(adb, parts, board):
    """The TECHO5 logo into kaeru's wordmark slot (see LOGO_FILE), from TWRP, and read back from the
    flash. What it did, as a line for the log; a unit it does not fit keeps Amazon's logo. A read back
    that is wrong puts the saved expdb back at once, before anything restarts the unit."""
    k = LOGO_KAERU[board]
    n = expdb_partition(adb)
    if not n:
        return "kept Amazon's boot logo: no single partition named expdb"
    part = '/dev/block/mmcblk0p%d' % n
    expdb_backup = os.path.join(parts, 'p%d-expdb.img' % n)
    if not os.path.exists(expdb_backup):
        return "kept Amazon's boot logo: expdb (p%d) was not among the partitions saved" % n
    with open(expdb_backup, 'rb') as f:
        saved = f.read()
    with open(LOGO_FILE, 'rb') as f:
        logo = f.read()
    kaeru = saved[:k['length']]
    if sha256(kaeru) == k['logo']:
        return 'the TECHO5 boot logo is already there'
    if sha256(logo) != LOGO_SHA or len(logo) != LOGO_SIZE:
        return "kept Amazon's boot logo: %s is not the one this installer knows" % LOGO_FILE
    if sha256(kaeru) != k['stock']:
        return "kept Amazon's boot logo: this unit's bootloader (kaeru, in expdb) is not the one the logo was made for"
    at = k['offset']
    if sha256(kaeru[:at] + logo + kaeru[at + LOGO_SIZE:]) != k['logo']:
        return "kept Amazon's boot logo: the patched bootloader would not be the one that has been run"
    # The copy on this computer is the one that goes back if anything reads back wrong: it has to be
    # what the unit holds now.
    if adb.sh('sha256sum %s' % part).split(' ')[0] != sha256(saved):
        return "kept Amazon's boot logo: expdb on the unit is not the copy saved in %s" % expdb_backup

    def flash_kaeru():
        # From the flash rather than the page cache, which would only give back what was just written.
        path = expdb_backup + '.readback'
        adb.exec_out_to_file('sync; echo 3 > /proc/sys/vm/drop_caches; dd if=%s bs=4096 count=%d 2>/dev/null'
                             % (part, (k['length'] + 4095) // 4096), path)
        with open(path, 'rb') as f:
            got = f.read()[:k['length']]
        os.remove(path)
        return sha256(got)

    adb.push(LOGO_FILE, '/tmp/techo5-logo.bin')
    if adb.sh('sha256sum /tmp/techo5-logo.bin').split(' ')[0] != LOGO_SHA:
        adb.sh('rm -f /tmp/techo5-logo.bin')
        return "kept Amazon's boot logo: the logo changed on its way to the unit"
    adb.sh('dd if=/tmp/techo5-logo.bin of=%s bs=1 seek=%d count=%d conv=notrunc 2>/dev/null; sync; '
           'rm -f /tmp/techo5-logo.bin' % (part, at, LOGO_SIZE))
    if flash_kaeru() == k['logo']:
        return 'the TECHO5 boot logo written into expdb (p%d) and read back from the flash' % n

    # Wrong on the flash: the saved copy goes back whole, now, while TWRP still runs from RAM.
    adb.push(expdb_backup, '/tmp/expdb.img')
    adb.sh('dd if=/tmp/expdb.img of=%s bs=4096 2>/dev/null; sync; rm -f /tmp/expdb.img' % part)
    if flash_kaeru() == k['stock']:
        return "kept Amazon's boot logo: writing the logo did not read back right, so the saved expdb was put back"
    fail('expdb (p%d) did not read back right after the boot logo, nor after putting the saved copy back.\n'
         '   Do not restart the unit. Write the saved copy again from TWRP:\n'
         '     adb push %s /tmp/expdb.img\n'
         '     adb shell "dd if=/tmp/expdb.img of=%s bs=4096; sync"\n'
         '   and check it with: adb shell sha256sum %s' % (n, expdb_backup, part, part))


def install_lineage(adb, zip_path):
    """Format userdata and install the LineageOS zip from TWRP, without ever starting LineageOS: its
    system partition is where this unit's drivers come from. Fire OS's userdata is encrypted, so it is
    formatted first, and TWRP is restarted for /data to be usable again."""
    out = adb.sh('twrp format data')
    if 'Done' not in out:
        fail('formatting userdata in TWRP failed:\n%s' % out)
    adb.reboot('recovery')
    wait_for('TWRP after formatting userdata', 180,
             lambda: adb.state() == 'recovery' and ' /data ' in adb.sh('mount'), 3)
    note('userdata formatted')
    adb.push(zip_path, '/data/lineage.zip', ready=lambda: ' /data ' in adb.sh('mount'))
    with open(zip_path, 'rb') as f:
        want = hashlib.sha256(f.read()).hexdigest()
    if adb.sh('sha256sum /data/lineage.zip').split(' ')[0] != want:
        fail('the LineageOS zip changed on its way to the unit')
    out = adb.sh('twrp install /data/lineage.zip')
    adb.sh('rm -f /data/lineage.zip')
    if 'succeeded' not in out:
        fail('installing LineageOS from TWRP failed:\n%s' % out)
    note('LineageOS installed from TWRP (not started)')


def lineage_hint(board):
    """Which LineageOS build TECHO5 needs on this board, and where to get it."""
    tag = LINEAGE_RELEASE[board]
    return ('TECHO5 needs LineageOS %s (%s), whose kernel its own is built from; a newer LineageOS is not '
            'supported yet. Get it from https://github.com/amazon-oss/releases/releases/tag/%s and run '
            'this again from TWRP with --lineage-zip %s, or install it in TWRP and start it once'
            % (tag.rsplit('-', 1)[1], LINEAGE_ZIP % board, tag, LINEAGE_ZIP % board))


def check_lineage_driver(adb, board):
    """The Wi-Fi driver on the LineageOS system partition is built for the kernel TECHO5's is rebuilt
    from, which is what the running-kernel check says on a unit that started LineageOS."""
    out = adb.sh('mkdir -p /tmp/t5sys && mount -o ro /dev/block/mmcblk0p12 /tmp/t5sys && '
                 'grep -a -o "vermagic=[^ ]*" /tmp/t5sys/system/%s | head -1; umount /tmp/t5sys' % WIFI_MODULE)
    if 'vermagic=%s' % KERNEL_RELEASE not in out:
        fail('the LineageOS system this zip installed has a Wi-Fi driver for %s, not %s. %s'
             % (out.strip() or 'no kernel it names', KERNEL_RELEASE, lineage_hint(board)))
    note('Wi-Fi driver built for kernel %s' % KERNEL_RELEASE)


def default_key_file(backup):
    """backups/<serial>/home-assistant.key, as on the Dot; a unit installed while it was api.psk keeps
    that file, so a later run reuses the key Home Assistant already has."""
    new = os.path.join(backup, 'home-assistant.key')
    old = os.path.join(backup, 'api.psk')
    return old if os.path.exists(old) and not os.path.exists(new) else new


def show_version(tag):
    m = re.match(r'^v(\d+)\.(\d+)\.(\d+)$', tag)
    return tuple(int(x) for x in m.groups()) if m else None


def boot_name(dev, tag):
    """What a release calls the boot image for this board: each has its own kernel configuration and
    device trees, so a 1st gen Show 5 or a Show 8 needs the one built for it. The 2nd gen Show 5 is
    the unqualified name, because it was the first."""
    return 'techo5-boot-%s.img' % tag if dev == 'cronos' else 'techo5-boot-%s-%s.img' % (dev, tag)


def boot_image(rel, work, dev):
    """The release's boot image for this generation, or, when it carries none (the boot image changes
    rarely, so most releases don't), the one from the newest earlier Show release that does."""
    name = boot_name(dev, rel.version)
    if rel.signed(name):
        return rel.asset(name)
    want = show_version(rel.version)
    try:
        releases = fetch_json('https://api.github.com/repos/%s/releases?per_page=100' % REPO)
    except Exception as e:
        fail('release %s has no boot image, and the list of earlier releases could not be read: %s'
             % (rel.version, e))
    for r in releases:
        v = show_version(r['tag_name'])
        name = boot_name(dev, r['tag_name'])
        if not (v and want and v < want and any(x['name'] == name for x in r['assets'])):
            continue
        # An image is only usable when that release's signed manifest names it. A checksum in
        # SHA256SUMS is not enough: nothing signs that file, so it proves only that whoever served the
        # image also served the list. Releases made before the manifest covered boot images are passed
        # over here rather than trusted, which is why the search keeps going.
        earlier = Release(REPO, r['tag_name'], work)
        if not earlier.signed(name):
            note('release %s has %s but does not name it in its signed manifest; looking further back'
                 % (r['tag_name'], name))
            continue
        note('release %s has no boot image of its own; using %s\'s' % (rel.version, r['tag_name']))
        return earlier.asset(name)
    fail('no release up to %s names a boot image for the %s in its signed manifest, so this computer '
         'has no way to tell the real one from a substitute. Releases before the manifest covered boot '
         'images carry one only in SHA256SUMS, which nothing signs. Install from a newer release, or '
         'build the boot image yourself (docs/building.md) and pass it with --boot.'
         % (rel.version, dev))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--serial', help="the unit's adb serial (adb devices); found, or asked for, when missing")
    ap.add_argument('--name', help='the name Home Assistant shows, e.g. Kitchen; asked for when missing')
    ap.add_argument('--release', default='latest', help='a release tag, or latest')
    ap.add_argument('--key-file', help='where the Home Assistant key is kept (default backups/<serial>/home-assistant.key)')
    ap.add_argument('--ssh-key', help='an SSH public key the unit accepts from the start (SSH is switched on)')
    ap.add_argument('--boot', help='a boot image you built (docs/building.md) instead of the release\'s')
    ap.add_argument('--rootfs', help='a root filesystem you built instead of the release\'s')
    ap.add_argument('--lineage-zip', help='install from TWRP: the LineageOS 18.1 zip for this board, installed only for its drivers')
    ap.add_argument('--wifi', help="a Wi-Fi network to join (its passphrase is asked for); otherwise LineageOS's saved one, or the Show's screen")
    ap.add_argument('--wifi-passphrase-file', help='a file holding the --wifi passphrase, for running from a script')
    ap.add_argument('--amazon-logo', action='store_true',
                    help="from TWRP on a Show 5 2nd gen or a Show 8: keep Amazon's logo at boot rather than put TECHO5's in")
    ap.add_argument('--dry-run', action='store_true', help='download and check the release; write nothing')
    ap.add_argument('--force', action='store_true', help='do not ask before erasing LineageOS')
    ap.add_argument('--backups', default=default_dir('TECHO5_BACKUPS', 'backups'))
    ap.add_argument('--work', default=default_dir('TECHO5_WORK', 'build'))
    ap.add_argument('--adb', default='adb')
    ap.add_argument('--fastboot', default='fastboot')
    a = ap.parse_args()

    # ------------------------------------------------------------------------------------ 1. checks
    step('checks')
    need(a.adb, 'install the Android platform tools (adb and fastboot)')
    need(a.fastboot, 'install the Android platform tools (adb and fastboot)')
    twrp = bool(a.lineage_zip)
    if twrp and not os.path.exists(a.lineage_zip):
        fail('no file at %s' % a.lineage_zip)
    a.serial = pick_unit(a.serial, a.adb, ('recovery',) if twrp else ('device',), 'Echo Show', consoles=(CONSOLE_TECHO5,))
    backup = os.path.join(a.backups, a.serial)
    key_file = a.key_file or default_key_file(backup)
    adb = Adb(a.serial, a.adb)
    fastboot = Fastboot(a.serial, a.fastboot)
    console = Console(a.serial, CONSOLE_TECHO5)
    if a.name is not None:
        a.name = ask_name(a.name, '')
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
    if twrp and state != 'recovery':
        fail("adb does not see %s in TWRP (state '%s'): --lineage-zip installs from TWRP; on LineageOS leave it out"
             % (a.serial, state))
    if not twrp and state != 'device':
        fail("adb does not see %s running LineageOS (state '%s'): turn on USB debugging and accept this computer, "
             "or install from TWRP with --lineage-zip%s" % (a.serial, state, console_hint((CONSOLE_TECHO5,))))
    dev = adb.sh('getprop ro.product.device')
    if dev not in BOARDS:
        fail("%s reports '%s', which is none of: %s"
             % (a.serial, dev, ', '.join('%s (%s)' % (b, n) for b, n in BOARDS.items())))
    if twrp:
        zdev = lineage_board(a.lineage_zip)
        if zdev != dev:
            fail('%s is a LineageOS build for %s, and this unit is a %s' % (a.lineage_zip, zdev, dev))
        # A later build's drivers do not fit TECHO5's kernel, which check_lineage_driver only finds out
        # once it is installed; its name says so before anything is written.
        zdate = lineage_zip_date(a.lineage_zip)
        if zdate and zdate != LINEAGE_DATE:
            fail('%s is the %s build of LineageOS. %s' % (a.lineage_zip, zdate, lineage_hint(dev)))
        note('%s in TWRP; LineageOS zip for %s' % (dev, zdev))
    else:
        kr = adb.sh('uname -r')
        if kr != KERNEL_RELEASE:
            fail('%s runs kernel %s, not %s. %s' % (a.serial, kr, KERNEL_RELEASE, lineage_hint(dev)))
        note('%s, LineageOS kernel %s' % (dev, kr))
    wifi = None
    if a.wifi:
        if a.wifi_passphrase_file:
            with open(a.wifi_passphrase_file) as f:
                # Only the line's end goes: a passphrase may begin or end with spaces.
                wifi = wifi_keys(wifi_conf(a.wifi, f.read().rstrip('\r\n')))
        else:
            wifi = wifi_keys(ask_wifi(a.wifi))
        note("Wi-Fi: '%s' (only its key goes to the unit)" % a.wifi)
    a.name = ask_name(a.name, BOARDS[dev])
    note("name in Home Assistant: '%s'" % a.name)
    if not a.dry_run:
        check_serial_access()

    # ------------------------------------------------------------------------------------ 2. backup
    step('backup')
    os.makedirs(a.work, exist_ok=True)
    los_boot = os.path.join(backup, 'boot-lineage.img')
    parts = os.path.join(backup, 'partitions')
    if twrp:
        if os.path.exists(os.path.join(parts, 'SHA256SUMS')):
            note('partitions already saved: %s' % parts)
        elif a.dry_run:
            note('dry run: the partitions are saved by the real run')
        else:
            note('%d partitions saved, each checked against its size: %s' % (save_partitions(adb, parts), parts))
    elif os.path.exists(los_boot):
        note('LineageOS boot image already kept: %s' % los_boot)
    elif a.dry_run:
        # A dry run changes nothing: not adb's mode on the unit (adb root restarts adbd) and not this
        # computer's backups. The real run takes the backup.
        note('dry run: the LineageOS boot image backup is taken by the real run')
    else:
        os.makedirs(backup, exist_ok=True)
        adb.root()
        if adb.sh('id').startswith('uid=0'):
            if not adb.pull('/dev/block/mmcblk0p9', los_boot + '.partial'):
                fail('reading the LineageOS boot partition failed')
            want = adb.sh('md5sum /dev/block/mmcblk0p9').split(' ')[0]
            if md5(los_boot + '.partial') != want:
                fail('LineageOS boot image md5 mismatch')
            os.replace(los_boot + '.partial', los_boot)
            note('LineageOS boot image: %s' % los_boot)
            saved = adb.sh('grep -c SSID /data/misc/apexdata/com.android.wifi/WifiConfigStore.xml 2>/dev/null')
            if saved in ('', '0') and not wifi:
                fail('LineageOS has no saved Wi-Fi network: join one first (TECHO5 uses it), or give one with --wifi')
            note('a saved Wi-Fi network' if saved not in ('', '0') else 'no saved Wi-Fi network; --wifi is used')
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
        boot = boot_image(rel, a.work, dev)
        note('boot image %s checked' % os.path.basename(boot))
    if a.dry_run:
        print('\nDry run: TECHO5 %s downloaded and checked in %s; nothing written to the unit.' % (version, rel.dir))
        return

    if twrp:
        # From TWRP the first thing written is userdata, so the one question comes before it.
        confirm(a.force, [
            "About to install TECHO5 %s on %s (%s) as '%s', from TWRP." % (version, a.serial, BOARDS[dev], a.name),
            "This formats userdata (Fire OS's data), installs LineageOS for its drivers without starting it,",
            "flashes TECHO5's boot image, then erases the system partition (mmcblk0p12) to make the slot store.",
        ])
        step('LineageOS, for its drivers')
        install_lineage(adb, a.lineage_zip)
        check_lineage_driver(adb, dev)
        if not os.path.exists(los_boot):
            # Through a .partial kept only when whole, as on the LineageOS path: this is the image the way
            # back flashes, and a short one kept for good would be skipped on every later run.
            os.makedirs(backup, exist_ok=True)
            size = int(adb.sh('cat /sys/class/block/mmcblk0p9/size') or 0) * 512
            adb.exec_out_to_file('dd if=/dev/block/mmcblk0p9 bs=4096 2>/dev/null', los_boot + '.partial')
            if not size or os.path.getsize(los_boot + '.partial') != size:
                fail('the LineageOS boot image came back %d bytes, not %d' % (os.path.getsize(los_boot + '.partial'), size))
            os.replace(los_boot + '.partial', los_boot)
            note('LineageOS boot image: %s' % los_boot)
        if dev not in LOGO_KAERU:
            note("boot logo: Amazon's is kept (TECHO5's is made for the Show 5 2nd gen and the Show 8)")
        elif a.amazon_logo:
            note("boot logo: Amazon's is kept (--amazon-logo)")
        else:
            note('boot logo: ' + put_logo(adb, parts, dev))

    # ------------------------------------------------------------------------------------ 4. push
    step('root filesystem onto the unit')
    name = os.path.basename(rootfs)
    # TWRP's /sdcard is not always userdata's media folder, so from TWRP the file goes to the path the
    # rescue environment reads it from.
    remote = ('/data/media/0/Download/' if twrp else '/sdcard/Download/') + name
    if twrp:
        adb.sh('mkdir -p /data/media/0/Download')
    adb.push(rootfs, remote, ready=(lambda: ' /data ' in adb.sh('mount')) if twrp else None)
    if adb.sh('md5sum ' + remote).split(' ')[0] != md5(rootfs):
        fail('md5 mismatch after pushing ' + name)
    note('%s ok' % remote)
    os.makedirs(os.path.dirname(key_file) or '.', exist_ok=True)
    if os.path.exists(key_file):
        with open(key_file) as f:
            psk = f.read().strip()
        if not valid_api_key(psk):
            fail('the key in %s is not 32 bytes of base64' % key_file)
        note('Home Assistant key: the existing one in %s' % key_file)
    else:
        psk = new_api_key()
        write_private(key_file, psk)
        note('Home Assistant key: new, in %s' % key_file)

    # ------------------------------------------------------------------------------------ 5. flash
    # Asked here, once, rather than halfway through the store: the flash is where the unit stops being
    # a LineageOS unit, so this is the last moment a "no" leaves it as it was.
    if not twrp:
        confirm(a.force, [
            "About to install TECHO5 %s on %s (%s) as '%s'." % (version, a.serial, BOARDS[dev], a.name),
            "This flashes TECHO5's boot image, then erases LineageOS's system partition (mmcblk0p12) to",
            'make the slot store. The way back is in docs/install.md.',
        ])
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
    wait_for('the rescue console on USB', 300, rescue_up, hint=console.waiting_hint)
    note('rescue console on %s' % console.port)

    # ------------------------------------------------------------------------------------ 6. store
    step('slot store')
    # A store already on the unit means this is not a fresh install, and mkstore would reformat it.
    # That is the one step here with nothing behind it: the vendor tree in the store is this unit's
    # own, LineageOS is gone by now, and vendor.tar was deleted once it reached slot a - so erasing
    # the store leaves the unit with no way to bring its Wi-Fi up and nothing to rebuild it from.
    # Asked of the unit rather than assumed from how far this run has got, and not something --force
    # can wave through: --force is there to skip a prompt, not to erase a store somebody is using.
    if 'HAVE-STORE' in (console.run('test -e /store/.techo5-store && echo HAVE-STORE', 10) or ''):
        fail('this unit already has a slot store, so it is already installed or part-installed.\n'
             '   Erasing it would take this unit\'s vendor tree with it, and LineageOS is no longer\n'
             '   there to rebuild it from. What the unit has:\n%s\n'
             '   To update it, use Home Assistant or deploy-rootfs.sh --install.\n'
             '   To start over from LineageOS, flash the backup at %s first.'
             % (console.run('STORE=/store slotctl status', 30) or '   (slotctl status did not answer)',
                los_boot))
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
    if wifi:
        prov += (" && mkdir -p /data/techo5-linux && (umask 077; printf '%s' %s %s > /data/techo5-linux/wpa_supplicant.conf)"
                 % (WIFI_CONF_FORMAT, wifi[0], wifi[1]))
    o = console.run(prov + ' && sync && echo PROV-OK', 15)
    if 'PROV-OK' not in (o or ''):
        fail('provisioning failed:\n%s' % o)
    note("name '%s' and Home Assistant key%s%s written" % (a.name, ', SSH key' if pub else '', ', Wi-Fi' if wifi else ''))
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
    wait_for('slot a with the daemon running', 300, slot_up, hint=console.waiting_hint)
    o = console.run('ip -4 addr show wlan0 | sed -n "s/.*inet \\([0-9.]*\\).*/\\1/p"; cat /etc/techo5-release', 8) or ''
    for line in o.split('\n'):
        note(line)

    print("\nDone. '%s' runs TECHO5 %s from slot a; the slot commits itself after five healthy minutes." % (a.name, version))
    print('Home Assistant finds it as an ESPHome device. When it asks for the encryption key, paste:\n\n    %s\n\n(kept in %s)' % (psk, key_file))
    if twrp and not wifi:
        print('It has no Wi-Fi yet: on the Show, swipe down for Settings, then Connections, then Wi-Fi.')
    print("Later versions arrive through Home Assistant's update card.")


if __name__ == '__main__':
    run_main(main)
