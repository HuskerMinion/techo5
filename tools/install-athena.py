#!/usr/bin/env python3
"""Install TECHO5 on an Echo Show 8 2nd gen (athena) from TWRP, in one command.

athena is not like the other Shows. There is no LineageOS for it. TECHO5 brings its own 64-bit
mainline Linux kernel, written to the `boot` partition, and the slot store, written to the `system`
partition: the boot and rescue environment at its root and the two root filesystem slots (a and b)
beside it. The kernel mounts `system` read-only as / and runs /init there. TECHO5's state goes into
one folder on userdata (userdata/techo5); nothing else of Fire OS's data is touched.

    python3 tools/install-athena.py --release v1.4.0
    python3 tools/install-athena.py --release v1.4.0 --name Kitchen --wifi MyNetwork
    python3 tools/install-athena.py --image system.img --boot boot.img --dry-run
    python3 tools/install-athena.py --kernel amazon --image system.img

The kernel's speaker amp needs Amazon's tuning for it (a TI TAS5805M register sequence). TECHO5
doesn't ship it: Amazon's kernel on the unit's own `boot` partition carries it. The installer reads it
out of the backup it makes, checks it against the known tunings by sha256, and puts it into a slot
reserved for it in TECHO5's boot image before it writes that image. The data stays on this computer
and on the unit.

`--kernel amazon` is the older path: Amazon's 32-bit kernel stays on `boot`, and only `system` is
written. The same store image works under both kernels.

The unit has to be unlocked (saiyaku-athena) and in TWRP, with adb working (`adb devices` says
recovery). Each step is checked before the next:

  1. checks    adb sees an athena in TWRP; its partition table is the one this installer knows (boot
               p20, system p21, userdata p23, the unlock's tee1_real and lk_real where they belong);
               for the mainline kernel, the screen is one it is tested on (ILI9881C with BOE
               glass, JD9365 with KD/HSD glass), or another it has tables for and --force-panel is
               given
  2. backup    every partition but cache and userdata, the boot0 and boot1 areas too, into
               backups/<serial>/partitions/, each checked against the unit's own sha256 (skipped when a
               backup is already there)
  3. images    the store image, and for the mainline kernel the boot image: yours (--image, --boot) or
               the release's, checked against its signed manifest
  4. amp       mainline only: the unit's speaker tuning, from Fire OS's boot in the backup, into the
               boot image's tuning slot; the boot image's id recomputed
  5. write     asked once; only then are system and boot unmounted if TWRP had them mounted, and
               written: system, then boot (mainline only), each read back from the flash and compared
               by sha256; the store on system is then grown to fill its partition, from TWRP (Amazon's
               kernel refuses to grow it once it runs: #192). Before this nothing on the unit changes:
               userdata is only mounted, and read-only when TWRP has not mounted it already
  6. settings  name, Home Assistant key, SSH key and Wi-Fi (added to the unit's saved networks) into
               userdata/techo5
  7. boot      restart into TECHO5; the first boot grows the store to fill the partition, then slot a
               starts; the install watches for the daemon over the USB serial console

Writes go to two partitions, system (p21) and boot (p20), and only after the partition table names
them so. Never tee1 or tee1_real (the unlock: writing them bricks the unit), lk, lk_real, lk2_real,
preloader, boot0, recovery (TWRP), vendor or anything else.

Windows, Linux and macOS alike; needs Python 3 and adb. The Home Assistant key is kept in
backups/<serial>/home-assistant.key; a unit that already has one keeps it, so Home Assistant keeps the
device. The way back to Fire OS is printed at the end, and in docs/install-athena.md.

`--show-amp-tables <stock boot image>` reads a Fire OS boot image on this computer and prints the amp
tunings found in it and their sha256, to add a firmware version this installer doesn't know yet. It
needs no unit.
"""
import argparse
import gzip
import hashlib
import os
import re
import shutil
import struct
import sys
import time
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from techo5lib import (CONSOLE_TECHO5, Adb, Console, Release, ask_name, ask_wifi, confirm,  # noqa: E402
                       default_dir, fail, need, new_api_key, note, pick_unit, run_main, step, valid_api_key,
                       wifi_conf, write_private)

REPO = 'HuskerMinion/techo5'
BOARD_NAME = 'Echo Show 8 2nd gen'

# The eMMC as TWRP's kernel names it (mmcblk1; TECHO5's mainline kernel calls it mmcblk0). Found again
# on the unit: the one whose p21 is named system.
MMC = 'mmcblk1'
MMC_CANDIDATES = ('mmcblk1', 'mmcblk0')

# The partitions this installer writes, by number and by the name the partition table gives them.
SYSTEM = 21
BOOT = 20
WRITABLE = {SYSTEM: 'system', BOOT: 'boot'}


def dev(n):
    return '/dev/block/%sp%d' % (MMC, n)


# The partition table of an unlocked athena, as far as this installer relies on it: the numbers it
# writes or mounts, and the unlock's own partitions, which prove the table is the one it was made for.
# The partition table itself names 3, 4, 18 and 19 tee1, tee2, lk and lk2; TWRP's by-name links call
# them tee1_real... and point tee1, lk... at /dev/null (seen on unit 1, 2026-10-15). Either name is
# the same partition, so either is accepted.
LAYOUT = {
    3: ('tee1', 'tee1_real'), 4: ('tee2', 'tee2_real'), 6: ('misc',), 7: ('recovery',), 12: ('vendor',),
    18: ('lk', 'lk_real'), 19: ('lk2', 'lk2_real'), 20: ('boot',), 21: ('system',), 22: ('cache',),
    23: ('userdata',),
}
USERDATA = 23

# Never written, whatever a table says: the unlock (tee1, and the original lk it replaces), the real
# bootloader and TEE it loads, the boot ROM's area, TWRP, the drivers.
PROTECTED = {'tee1', 'tee2', 'tee1_real', 'tee2_real', 'lk', 'lk2', 'lk_real', 'lk2_real', 'boot0', 'boot1',
             'preloader', 'recovery', 'vendor', 'kb', 'dkb', 'persist', 'misc', 'nvcfg', 'boot_para',
             'metadata', 'spmfw', 'sspm_1', 'userdata'}

# Not saved: cache is Fire OS's scratch space, userdata is not written (only one folder is added to it).
NOT_SAVED = {'cache', 'userdata'}

# What wpa_supplicant reads (the format boot.sh writes from Android's saved networks): the name and
# WPA's key, both as hex, so only the key ever reaches the unit, never the passphrase.
WIFI_CONF = 'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid=%s\n\tpsk=%s\n}\n'

EXT4_MAGIC = b'\x53\xef'
STORE_LABEL = b'techo5-store'

# ------------------------------------------------------------------------------------------ the amp tuning
#
# Amazon compiles two TAS5805M register sequences into its kernel's tas5805m driver and picks one with
# the device tree's speaker-config (0 = table A, 1 = table B). Each entry is {u32 reg, u32 val, u32 0},
# little endian; reg 0xfe means "wait val ms". Before the main table the driver sends a short preboot
# sequence (reset, deep sleep, HiZ). The mainline driver loads raw reg,val byte pairs and sends its own
# preboot, so the main table converts to that by dropping the delays and the zero word.
#
# Known tunings, by the sha256 of the converted table. A firmware version whose tables are not here
# stops the installer: add its hashes after checking them (--show-amp-tables, docs/install-athena.md).
KNOWN_AMP_TABLES = {
    # Fire OS PS7574.1.7080N (both tables of its kernel)
    '8c14a75c68e77d071cfc512f2fbeb002351eb2f26b0ee945bd94e87b10c45c49': ('A', 'Fire OS 7080, table A'),
    '5f3f4ddbcb081b6ac49a361c529ef99fcb010cee0100ae37088d831601ba9248': ('B', 'Fire OS 7080, table B'),
}
SPEAKER_CONFIG_TABLE = {0: 'A', 1: 'B'}

# The start of the preboot sequence (page 0, book 0, deep sleep, wait 2 ms, reset), the same in both
# tables. It marks where a main table ends; the preboot itself ends at its DEVICE_CTRL_2 = 2 write.
PREBOOT_START = ((0x00, 0x00), (0x7f, 0x00), (0x03, 0x02), (0xfe, 0x02), (0x01, 0x11))
PREBOOT_END = (0x02, 0x02)
MIN_TABLE = 100

# TECHO5's boot image reserves this slot for the tuning: magic, u32 N, sha256 of the data, the data,
# zeros to the end of the slot. It ships with N = 0.
SLOT_MAGIC = b'T5DSPCF1'
SLOT_SIZE = 8192
SLOT_HEAD = 44
SLOT_MAX = SLOT_SIZE - SLOT_HEAD

# The screens TECHO5's mainline kernel has tables for, by LK's name for each: (jd936x_id vendor nibble,
# or None where both controllers use it; what to call it). The kernel picks the unit's own at boot
# (panel-athena-select.c). Only the tested ones are installed without --force-panel.
PANELS = {
    'ili9881c_wxga_dsi_vdo_st_boe': (6, 'ILI9881C, BOE glass'),
    'ili9881c_wxga_dsi_vdo_st_kd_hsd': (None, 'ILI9881C, KD/HSD glass'),
    'ili9881c_wxga_dsi_vdo_st_inx': (8, 'ILI9881C, INX glass'),
    'ili9881c_wxga_dsi_vdo_st_kd_hsd_onyx': (9, 'ILI9881C, KD/HSD glass (onyx)'),
    'ili9881c_wxga_dsi_vdo_st_kd_inx_o': (0xa, 'ILI9881C, KD/INX glass'),
    'jd936x_wxga_dsi_vdo_athena_st_kd_hsd': (1, 'JD9365, KD/HSD glass'),
    'jd936x_wxga_dsi_vdo_athena_st_kd_hsd_new': (2, 'JD9365, KD/HSD glass (new)'),
    'jd936x_wxga_dsi_vdo_athena_st_kd_hsd_proto': (None, 'JD9365, KD/HSD glass (proto)'),
    'jd936x_wxga_dsi_vdo_athena_st_boe': (4, 'JD9365, BOE glass'),
    'jd936x_wxga_dsi_vdo_athena_st_boe_new': (5, 'JD9365, BOE glass (new)'),
    'jd936x_wxga_dsi_vdo_athena_st_inx': (7, 'JD9365, INX glass'),
}
# The screens tested on units: ILI9881C with BOE glass on ours, JD9365 with KD/HSD glass by a user (#187).
PANELS_TESTED = ('ili9881c_wxga_dsi_vdo_st_boe', 'jd936x_wxga_dsi_vdo_athena_st_kd_hsd')


def image_name(version):
    """What a release calls athena's store image."""
    return 'techo5-system-athena-%s.img.gz' % version


def boot_name(version):
    """What a release calls athena's mainline boot image."""
    return 'techo5-boot-athena-%s.img' % version


def quote(s):
    return "'" + s.replace("'", "'\\''") + "'"


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def first_word(text):
    return (text or '').split(' ')[0].strip()


# ------------------------------------------------------------------------------------------ boot images

class BootImage:
    """An Android boot image, header version 0 to 2, as LK reads it."""

    def __init__(self, data, what='the boot image'):
        if len(data) < 1648 or data[:8] != b'ANDROID!':
            fail('%s is not an Android boot image' % what)
        (self.kernel_size, _, self.ramdisk_size, _, self.second_size, _, _, self.page,
         self.version) = struct.unpack('<9I', data[8:44])
        if self.page not in (2048, 4096, 8192, 16384):
            fail('%s has a page size of %d' % (what, self.page))
        if self.version > 2:
            fail('%s has header version %d; this installer knows 0 to 2' % (what, self.version))
        self.data = data
        self.what = what
        pad = lambda n: (n + self.page - 1) // self.page * self.page  # noqa: E731
        self.kernel_at = self.page
        self.ramdisk_at = self.kernel_at + pad(self.kernel_size)
        self.second_at = self.ramdisk_at + pad(self.ramdisk_size)
        end = self.second_at + self.second_size
        self.dtbo = self.dtb = b''
        if self.version >= 1:
            size, off = struct.unpack('<IQ', data[1632:1644])
            if size:
                self.dtbo = data[off:off + size]
                if len(self.dtbo) != size:
                    fail('%s ends inside its recovery dtbo' % what)
        if self.version >= 2:
            size = struct.unpack('<I', data[1648:1652])[0]
            at = self.second_at + pad(self.second_size) + pad(len(self.dtbo))
            self.dtb = data[at:at + size]
            if len(self.dtb) != size:
                fail('%s ends inside its dtb' % what)
        if end > len(data):
            fail('%s is %d bytes, shorter than its header says (%d)' % (what, len(data), end))

    def kernel(self):
        return self.data[self.kernel_at:self.kernel_at + self.kernel_size]

    def ramdisk(self):
        return self.data[self.ramdisk_at:self.ramdisk_at + self.ramdisk_size]

    def second(self):
        return self.data[self.second_at:self.second_at + self.second_size]

    def stored_id(self):
        return bytes(self.data[576:608])

    def computed_id(self):
        """The header's id the way mkbootimg (and TECHO5's packer) make it: SHA-1 over each part followed
        by its size, the recovery dtbo from version 1, the dtb from version 2; padded to 32 bytes."""
        h = hashlib.sha1()
        parts = [self.kernel(), self.ramdisk(), self.second()]
        if self.version >= 1:
            parts.append(self.dtbo)
        if self.version >= 2:
            parts.append(self.dtb)
        for blob in parts:
            h.update(blob)
            h.update(struct.pack('<I', len(blob)))
        return h.digest().ljust(32, b'\0')


def slot_offset(data, what='the boot image'):
    """Where the tuning slot is: the magic exactly once, with a whole slot after it."""
    n = data.count(SLOT_MAGIC)
    if n != 1:
        fail('%s has %d amp tuning slots, not one: it is not a TECHO5 athena boot image this installer '
             'can fill' % (what, n))
    at = data.find(SLOT_MAGIC)
    if at + SLOT_SIZE > len(data):
        fail('the amp tuning slot in %s runs past its end' % what)
    return at


def is_techo5_boot(data):
    return SLOT_MAGIC in data


def check_techo5_boot(data, what='the boot image'):
    """A TECHO5 mainline boot image as shipped: an Android boot image whose id is right, with one empty
    tuning slot inside the part LK loads."""
    img = BootImage(data, what)
    if img.stored_id() != img.computed_id():
        fail('%s: its header id does not match its contents; it is damaged, or not made the way this '
             'installer can re-sign' % what)
    at = slot_offset(data, what)
    if not (img.kernel_at <= at and at + SLOT_SIZE <= img.second_at + img.second_size):
        fail('the amp tuning slot in %s is not inside its kernel, ramdisk or second stage' % what)
    n = struct.unpack('<I', data[at + 8:at + 12])[0]
    if n != 0:
        fail('the amp tuning slot in %s is already filled (%d bytes); use the image as released' % (what, n))
    if any(data[at + SLOT_HEAD:at + SLOT_SIZE]):
        fail('the amp tuning slot in %s is not empty' % what)
    return img


def fill_slot(data, table, what='the boot image'):
    """The boot image with the tuning in its slot and its header id recomputed; the same size."""
    check_techo5_boot(bytes(data), what)
    if not table or len(table) > SLOT_MAX or len(table) % 2:
        fail('the amp tuning is %d bytes; the slot takes an even number up to %d' % (len(table), SLOT_MAX))
    out = bytearray(data)
    at = slot_offset(out, what)
    out[at + 8:at + 12] = struct.pack('<I', len(table))
    out[at + 12:at + 44] = hashlib.sha256(table).digest()
    out[at + SLOT_HEAD:at + SLOT_HEAD + len(table)] = table
    out[576:608] = BootImage(bytes(out), what).computed_id()
    return bytes(out)


def read_slot(data):
    """The tuning in a filled slot, checked against its own sha256."""
    at = slot_offset(data)
    n = struct.unpack('<I', data[at + 8:at + 12])[0]
    blob = data[at + SLOT_HEAD:at + SLOT_HEAD + n]
    if n == 0 or n > SLOT_MAX or hashlib.sha256(blob).digest() != data[at + 12:at + 44]:
        return None
    return blob


# ------------------------------------------------------------------------------------------ Amazon's kernel

def lz4_legacy(data):
    """LZ4's legacy frame (what the kernel's lz4 compression writes), decoded."""
    out = bytearray()
    p = 4
    while p + 4 <= len(data):
        size = struct.unpack('<I', data[p:p + 4])[0]
        if size == 0x184c2102 or size == 0 or p + 4 + size > len(data):
            break
        blk = data[p + 4:p + 4 + size]
        p += 4 + size
        i = 0
        while i < len(blk):
            tok = blk[i]
            i += 1
            lit = tok >> 4
            if lit == 15:
                while True:
                    b = blk[i]
                    i += 1
                    lit += b
                    if b != 255:
                        break
            out += blk[i:i + lit]
            i += lit
            if i >= len(blk):
                break
            off = blk[i] | blk[i + 1] << 8
            i += 2
            ml = tok & 15
            if ml == 15:
                while True:
                    b = blk[i]
                    i += 1
                    ml += b
                    if b != 255:
                        break
            ml += 4
            if off == 0 or off > len(out):
                raise ValueError('bad lz4 match')
            for _ in range(ml):
                out.append(out[-off])
    return bytes(out)


def kernel_image(kernel):
    """The uncompressed kernel inside a boot image's kernel part: a 32-bit zImage with a gzip or LZ4
    payload, an Image.gz, or an uncompressed arm64 Image."""
    if kernel[0x38:0x3c] == b'ARMd':
        return kernel
    tries = []
    for magic in (b'\x1f\x8b\x08', b'\x02\x21\x4c\x18'):
        at = kernel.find(magic)
        while at >= 0 and len(tries) < 64:
            tries.append(at)
            at = kernel.find(magic, at + 1)
    for at in sorted(tries):
        try:
            if kernel[at] == 0x1f:
                raw = zlib.decompressobj(16 + zlib.MAX_WBITS).decompress(kernel[at:])
            else:
                raw = lz4_legacy(kernel[at:])
        except (zlib.error, ValueError, IndexError):
            continue
        if len(raw) >= 1 << 20:
            return raw
    fail("no compressed kernel found in Fire OS's boot image")


def fdt_props(blob):
    """[(path, name, value)] of a flattened device tree; minimal, for reading a few properties."""
    magic, total, off_struct, off_strings, _, version = struct.unpack('>6I', blob[:24])
    if magic != 0xd00dfeed or version < 16:
        raise ValueError('not a device tree')
    p, path, out = off_struct, [], []
    while p + 4 <= total:
        tok = struct.unpack('>I', blob[p:p + 4])[0]
        p += 4
        if tok == 1:
            e = blob.index(b'\0', p)
            path.append(blob[p:e].decode('ascii', 'replace'))
            p = (e + 4) & ~3
        elif tok == 2:
            path.pop()
        elif tok == 3:
            size, name_at = struct.unpack('>II', blob[p:p + 8])
            p += 8
            s = off_strings + name_at
            out.append(('/'.join(path) or '/', blob[s:blob.index(b'\0', s)].decode('ascii', 'replace'),
                        blob[p:p + size]))
            p = (p + size + 3) & ~3
        elif tok == 4:
            continue
        elif tok == 9:
            return out
        else:
            raise ValueError('bad device tree token %d' % tok)
    raise ValueError('device tree has no end')


def find_dtbs(blob):
    """Every well-formed device tree in blob (Amazon appends one per board revision to its kernel)."""
    found, at = [], blob.find(b'\xd0\x0d\xfe\xed')
    while at >= 0:
        if at + 40 <= len(blob):
            total = struct.unpack('>I', blob[at + 4:at + 8])[0]
            if 40 < total <= len(blob) - at:
                try:
                    found.append(fdt_props(blob[at:at + total]))
                except (ValueError, IndexError, struct.error):
                    pass
        at = blob.find(b'\xd0\x0d\xfe\xed', at + 4)
    return found


def strings(value):
    return [s.decode('ascii', 'replace') for s in value.split(b'\0') if s]


def dtb_compatible(props):
    for path, name, value in props:
        if path == '/' and name == 'compatible':
            return strings(value)
    return []


def dtb_speaker_configs(props):
    """speaker-config of each enabled tas5805m node in one device tree."""
    nodes = {}
    for path, name, value in props:
        nodes.setdefault(path, {})[name] = value
    out = []
    for path, p in sorted(nodes.items()):
        if 'ti,tas5805m' not in strings(p.get('compatible', b'')):
            continue
        status = strings(p.get('status', b'okay'))
        if status and status[0] not in ('okay', 'ok'):
            continue
        if len(p.get('speaker-config', b'')) == 4:
            out.append(struct.unpack('>I', p['speaker-config'])[0])
    return out


def speaker_config(dtbs, live_compatible=None):
    """The speaker-config Amazon's kernel uses on this unit: from the device tree LK picks for it (the
    one whose compatible list is the unit's), else the one every athena device tree agrees on."""
    if live_compatible:
        mine = [d for d in dtbs if dtb_compatible(d) == live_compatible]
        values = sorted({v for d in mine for v in dtb_speaker_configs(d)})
        if len(values) == 1:
            return values[0], 'device tree %s' % live_compatible[0]
    athena = [d for d in dtbs if 'amzn,athena' in dtb_compatible(d)]
    values = sorted({v for d in athena for v in dtb_speaker_configs(d)})
    if len(values) == 1:
        return values[0], 'all %d athena device trees' % len(athena)
    if not values:
        fail("Fire OS's boot image has no athena device tree with an enabled tas5805m speaker-config")
    fail("Fire OS's athena device trees disagree on speaker-config (%s) and none is this unit's (%s)"
         % (values, ' '.join(live_compatible or ['?'])))


def amp_entry_ok(reg, val, zero):
    return zero == 0 and reg < 0x100 and val < 0x100


def amp_tables(raw):
    """Every main table of TAS5805M writes in a decompressed kernel, found by structure rather than by
    offset: a run of 12-byte entries {reg, val, 0} (reg and val below 0x100), split at each copy of the
    preboot sequence. Returns [(offset, [(reg, val), ...])]."""
    sig = b''.join(struct.pack('<3I', r, v, 0) for r, v in PREBOOT_START)
    tables = []
    seen_runs = set()
    at = raw.find(sig)
    while at >= 0:
        if at % 4 == 0:
            start = at
            while start >= 12 and amp_entry_ok(*struct.unpack('<3I', raw[start - 12:start])):
                start -= 12
            if start not in seen_runs:
                seen_runs.add(start)
                end = at
                while end + 12 <= len(raw) and amp_entry_ok(*struct.unpack('<3I', raw[end:end + 12])):
                    end += 12
                entries = [struct.unpack('<3I', raw[o:o + 12])[:2] for o in range(start, end, 12)]
                tables += split_run(entries, start)
        at = raw.find(sig, at + 1)
    return tables


def split_run(entries, base):
    """A run of entries cut into the main tables between preboot sequences."""
    out, seg = [], 0
    i = 0
    n = len(PREBOOT_START)
    while i < len(entries):
        if tuple(entries[i:i + n]) == PREBOOT_START:
            if i - seg >= MIN_TABLE:
                out.append((base + 12 * seg, entries[seg:i]))
            j = i + n
            while j < len(entries) and entries[j] != PREBOOT_END:
                j += 1
            i = seg = j + 1
            continue
        i += 1
    if len(entries) - seg >= MIN_TABLE:
        out.append((base + 12 * seg, entries[seg:]))
    return out


def mainline_table(entries):
    """A main table as mainline's tas5805m loads it: reg,val byte pairs, the 0xfe delays left out."""
    return bytes(b for reg, val in entries if reg != 0xfe for b in (reg, val))


def stock_amp_tuning(boot_data, live_compatible=None, what="Fire OS's boot image"):
    """(table bytes, letter, description, speaker-config, where it came from) for this unit, from
    Amazon's boot image, checked against the known tunings."""
    if is_techo5_boot(boot_data):
        fail('%s is TECHO5\'s, not Fire OS\'s' % what)
    img = BootImage(boot_data, what)
    kernel = img.kernel()
    raw = kernel_image(kernel)
    dtbs = find_dtbs(kernel) + find_dtbs(img.second()) + find_dtbs(img.dtb)
    if not dtbs:
        fail('%s carries no device tree' % what)
    cfg, source = speaker_config(dtbs, live_compatible)
    if cfg not in SPEAKER_CONFIG_TABLE:
        fail('%s says speaker-config %d; this installer knows 0 and 1' % (what, cfg))
    want = SPEAKER_CONFIG_TABLE[cfg]
    found = []
    for _, entries in amp_tables(raw):
        blob = mainline_table(entries)
        h = hashlib.sha256(blob).hexdigest()
        found.append(h)
        known = KNOWN_AMP_TABLES.get(h)
        if known and known[0] == want:
            return blob, want, known[1], cfg, source
    fail('this unit uses amp table %s (speaker-config %d, from %s), and its Fire OS kernel holds no table\n'
         '   this installer knows (found: %s). That Fire OS version is not known yet, so TECHO5\'s kernel\n'
         '   cannot be given its speaker tuning. Use --kernel amazon, or see "A Fire OS version the installer\n'
         '   doesn\'t know" in docs/install-athena.md.'
         % (want, cfg, source, ', '.join(h[:16] for h in found) or 'none'))


def show_amp_tables(path):
    """--show-amp-tables: what a Fire OS boot image holds, for adding a firmware version."""
    with open(path, 'rb') as f:
        data = f.read()
    img = BootImage(data, path)
    print('%s: sha256 %s' % (path, hashlib.sha256(data).hexdigest()))
    kernel = img.kernel()
    raw = kernel_image(kernel)
    print('kernel: %d bytes compressed, %d uncompressed' % (len(kernel), len(raw)))
    for d in find_dtbs(kernel) + find_dtbs(img.second()) + find_dtbs(img.dtb):
        print('device tree %-60s speaker-config %s' % (' '.join(dtb_compatible(d)), dtb_speaker_configs(d) or '-'))
    for off, entries in amp_tables(raw):
        blob = mainline_table(entries)
        h = hashlib.sha256(blob).hexdigest()
        known = KNOWN_AMP_TABLES.get(h)
        print('table at 0x%x: %d entries, %d delays, %d bytes for mainline, sha256 %s %s'
              % (off, len(entries), sum(1 for r, _ in entries if r == 0xfe), len(blob), h,
                 '(%s)' % known[1] if known else '(not known)'))


# ------------------------------------------------------------------------------------------ the unit

def find_mmc(adb):
    """The eMMC's name in TWRP: the device whose p21 is named system."""
    for m in MMC_CANDIDATES:
        if adb.sh('sed -n "s/^PARTNAME=//p" /sys/class/block/%sp%d/uevent 2>/dev/null' % (m, SYSTEM)) == 'system':
            return m
    fail('no eMMC with a partition 21 named system: this is not the partition table of an unlocked athena')


def partitions(adb):
    """{number: (name, bytes)} for the unit's eMMC, from sysfs. TWRP's grep crashes on this unit, so
    sed reads the names."""
    out = adb.sh('for d in /sys/class/block/%sp*; do n=${d##*%sp}; '
                 'echo "$n $(sed -n "s/^PARTNAME=//p" $d/uevent) $(cat $d/size)"; done' % (MMC, MMC))
    parts = {}
    for line in out.split('\n'):
        f = line.split()
        if len(f) == 3 and f[0].isdigit() and f[2].isdigit():
            parts[int(f[0])] = (f[1], int(f[2]) * 512)
    return parts


def is_athena(adb):
    """What the unit says it is: TWRP's build properties, and the device tree's compatible list."""
    props = ' '.join(adb.sh('getprop %s' % p) for p in
                     ('ro.product.device', 'ro.build.product', 'ro.product.vendor.device', 'ro.product.board'))
    compat = adb.sh("cat /proc/device-tree/compatible 2>/dev/null | tr '\\0' ' '")
    return 'athena' in props.split() or 'amzn,athena' in compat.split(), (props.strip(), compat.strip())


def check_layout(parts):
    for n, names in sorted(LAYOUT.items()):
        got = parts.get(n, ('(none)', 0))[0]
        if got not in names:
            fail('partition %d on this unit is %s, not %s: this is not the partition table of an unlocked athena\n'
                 '   this installer knows, so nothing will be written. (Is this an Echo Show 8 2nd gen unlocked\n'
                 '   with saiyaku-athena?)' % (n, got, ' or '.join(names)))


def panel_of(adb, work):
    """(jd936x_id or None, LK's panel name or None): what LK told the kernel TWRP runs, on its command
    line and in the framebuffer tag it leaves in the device tree."""
    cmdline = adb.sh('cat /proc/cmdline 2>/dev/null') + ' ' + adb.sh(
        "cat /proc/device-tree/chosen/bootargs 2>/dev/null | tr '\\0' ' '")
    m = re.search(r'(?:^|\s)jd936x_id=(0x[0-9a-fA-F]+|\d+)(?:\s|$)', cmdline)
    panel_id = int(m.group(1), 0) if m else None
    tmp = os.path.join(work, 'athena-videolfb.bin')
    adb.exec_out_to_file('cat /proc/device-tree/chosen/atag,videolfb 2>/dev/null', tmp)
    try:
        with open(tmp, 'rb') as f:
            m = re.search(rb'[a-z0-9_]+_dsi_[a-z0-9_]+', f.read())
    finally:
        os.remove(tmp)
    return panel_id, m.group(0).decode() if m else None


def check_panel(panel_id, lk_name, force):
    """Refuses the mainline kernel on a screen it isn't tested on, unless forced. Returns what to say about it."""
    said = 'jd936x_id=%s, LK panel %s' % ('0x%02x' % panel_id if panel_id is not None else '?', lk_name or '?')
    which, doubt = panel_which(panel_id, lk_name)
    if which in PANELS_TESTED and not doubt:
        return 'screen: %s (%s), one TECHO5\'s kernel is tested on' % (PANELS[which][1], said)
    reach = ('give --wifi and --ssh-key too, so the unit can be reached if the screen stays dark; "Which '
             'screen" in\n   docs/install-athena.md has the way back')
    if which:
        why = 'this unit\'s screen is the %s (%s)' % (PANELS[which][1], said)
        if doubt:
            why += ': %s, and TECHO5\'s kernel would pick that one' % doubt
        else:
            why += ': TECHO5\'s kernel has its tables, but they have not been tested on a unit yet'
        if force:
            return why + '; going on (--force-panel): if the screen stays dark, see "Which screen"'
        fail(why + '.\n   To try it, run this again with --force-panel; ' + reach + '. Or use --kernel amazon '
             '(Amazon\'s kernel\n   drives every screen).')
    if panel_id is None and lk_name is None:
        why = 'TWRP does not show which screen this unit has (no jd936x_id on its command line, no LK panel name)'
    else:
        why = 'this unit\'s screen is not one TECHO5\'s mainline kernel knows (%s)' % said
    if force:
        return why + '; going on anyway (--force-panel): the screen may stay dark'
    fail(why + '.\n   With TECHO5\'s kernel the screen would stay dark. Use --kernel amazon (Amazon\'s kernel '
         'drives every\n   screen), or --force-panel if you know better (and ' + reach + ').')


def panel_which(panel_id, lk_name):
    """(LK's name for the screen TECHO5's kernel would pick, or None; why that pick is in doubt, or None).
    As the kernel picks: by LK's name, else by the id's vendor nibble where only one screen has it."""
    nibble = panel_id & 0xf if panel_id is not None else None
    if lk_name in PANELS:
        want = PANELS[lk_name][0]
        if nibble is not None and want is not None and want != nibble:
            return lk_name, 'LK\'s name and jd936x_id disagree'
        return lk_name, None
    named = [n for n, (nib, _) in PANELS.items() if nibble is not None and nib == nibble]
    if len(named) != 1:
        return None, None
    if lk_name is not None:
        return named[0], 'LK names a screen this installer does not know, so this is by jd936x_id'
    return named[0], None


def live_speaker_config(adb):
    """speaker-config in the device tree TWRP runs with, when it has one (the same board's tree)."""
    out = adb.sh('for f in /proc/device-tree/*/tas5805m@*/speaker-config '
                 '/proc/device-tree/*/*/tas5805m@*/speaker-config; do [ -f "$f" ] && od -An -tx1 "$f"; done')
    values = set()
    for line in out.split('\n'):
        b = line.split()
        if len(b) == 4 and all(re.fullmatch('[0-9a-f]{2}', x) for x in b):
            values.add(int(''.join(b), 16))
    return values


def mounts_of(adb, block):
    """Where a block device is mounted, by whatever name /proc/mounts gives it (TWRP mounts by-name)."""
    out = adb.sh('awk \'{print $1, $2}\' /proc/mounts | while read d m; do '
                 '[ "$(readlink -f "$d" 2>/dev/null)" = "%s" ] && echo "$m"; done' % block)
    return [m for m in out.split('\n') if m.strip()]


def target_ok(adb, parts, n):
    """A write target, checked again right before every write: one of the two this installer writes,
    the node exists, is a block device, is the partition so named, and is not a link to anything else."""
    want = WRITABLE.get(n)
    name, size = parts[n]
    if want is None or name != want or name in PROTECTED:
        fail('refusing to write: partition %d is %s' % (n, name))
    d = dev(n)
    out = adb.sh('[ -b {d} ] && echo BLOCK; readlink -f {d}; sed -n "s/^PARTNAME=//p" /sys/class/block/{m}p{n}/uevent'
                 .format(d=d, m=MMC, n=n)).split('\n')
    if len(out) < 3 or out[0] != 'BLOCK' or out[1] != d or out[2] != want:
        fail('refusing to write: %s is not the %s partition as expected (%s)' % (d, want, ' / '.join(out)))
    return size


OWN_USERDATA = '/tmp/t5-userdata'


def userdata_dir(adb, dry_run):
    """Where userdata is mounted in TWRP, mounting it when TWRP has not. Its root is never encrypted, so
    TECHO5's folder there is readable however TWRP left Fire OS's own. Mounted here, it is read-only
    until the install is confirmed (userdata_writable)."""
    block = dev(USERDATA)
    for m in mounts_of(adb, block):
        if adb.sh('[ -d %s ] && echo YES' % quote(m)) == 'YES':
            return m
    if dry_run:
        return None
    out = adb.sh('mkdir -p %s && mount -t ext4 -o ro %s %s && echo MOUNTED' % (OWN_USERDATA, block, OWN_USERDATA))
    if 'MOUNTED' not in out:
        fail('userdata (%s) could not be mounted in TWRP: %s\n'
             '   TECHO5 keeps its settings there. Mount Data in TWRP (Mount menu) and run this again.' % (block, out))
    return OWN_USERDATA


def userdata_writable(adb, ud):
    """userdata read-write for the settings, once the install is confirmed. TWRP's own mount is left as
    TWRP made it; the one this installer made read-only is remounted read-write."""
    if ud != OWN_USERDATA:
        return
    out = adb.sh('mount -o remount,rw %s && echo REMOUNTED' % OWN_USERDATA)
    if 'REMOUNTED' not in out:
        fail('userdata (%s) could not be made writable for the settings: %s\n'
             '   system and boot are written. Mount Data in TWRP (Mount menu) and run this again: it keeps\n'
             '   the backup and writes the same images and the settings.' % (dev(USERDATA), out))


def userdata_ready(adb, ud):
    """For Adb.push's ready: userdata mounted where the settings go, mounted again (read-write) where
    this installer had mounted it. TWRP may have restarted since, and a push before then would land in
    its RAM disk instead."""
    def ready():
        if ud in mounts_of(adb, dev(USERDATA)):
            return True
        if ud == OWN_USERDATA:
            adb.sh('mkdir -p %s && mount -t ext4 %s %s' % (OWN_USERDATA, dev(USERDATA), OWN_USERDATA))
            return ud in mounts_of(adb, dev(USERDATA))
        return False
    return ready


def read_partition(adb, n, size, path):
    """A partition into a file here, compared with the sha256 the unit computes of its own."""
    adb.exec_out_to_file('dd if=%s bs=1048576 2>/dev/null' % dev(n), path + '.partial')
    if os.path.getsize(path + '.partial') != size:
        fail('reading %s gave %d bytes, not %d' % (dev(n), os.path.getsize(path + '.partial'), size))
    if sha256_file(path + '.partial') != first_word(adb.sh('sha256sum %s' % dev(n))):
        fail('reading %s: the copy here is not what the unit reads' % dev(n))
    os.replace(path + '.partial', path)
    return path


# ------------------------------------------------------------------------------------------ backup

def save_partitions(adb, parts, folder):
    """Every partition but cache and userdata, and the boot areas, into folder, each compared with the
    sha256 the unit computes of its own. Read only; nothing on the unit changes."""
    os.makedirs(folder, exist_ok=True)
    sums = []
    todo = [(dev(n), 'p%d-%s.img' % (n, name), size)
            for n, (name, size) in sorted(parts.items()) if name not in NOT_SAVED]
    for b in ('boot0', 'boot1'):
        size = int(adb.sh('cat /sys/class/block/%s%s/size 2>/dev/null' % (MMC, b)) or 0) * 512
        # TWRP links mmcblk1boot0 (the preloader's area) to /dev/null, as it does tee1 and lk, so it
        # cannot be read, or written, from TWRP at all (unit 1, 2026-10-15). Nothing here writes it.
        if size and adb.sh('readlink /dev/block/%s%s 2>/dev/null' % (MMC, b)).strip() == '/dev/null':
            note('%s%s is not readable from TWRP (it points at /dev/null); not saved, and never written'
                 % (MMC, b))
            continue
        if size:
            todo.append(('/dev/block/%s%s' % (MMC, b), '%s%s.img' % (MMC, b), size))
    for d, name, size in todo:
        out = os.path.join(folder, name)
        if size >= 64 << 20:
            note('saving %s (%d MiB)...' % (name, size >> 20))
        adb.exec_out_to_file('dd if=%s bs=1048576 2>/dev/null' % d, out + '.partial')
        got = os.path.getsize(out + '.partial')
        if got != size:
            fail('saving %s gave %d bytes, not %d' % (d, got, size))
        mine = sha256_file(out + '.partial')
        theirs = first_word(adb.sh('sha256sum %s' % d))
        if mine != theirs:
            fail('saving %s: the copy here (%s) is not what the unit reads (%s)' % (d, mine[:16], theirs[:16]))
        os.replace(out + '.partial', out)
        sums.append('%s  %s' % (mine, name))
    with open(os.path.join(folder, 'SHA256SUMS'), 'w') as f:
        f.write('\n'.join(sums) + '\n')
    return len(sums)


def backup_done(folder):
    try:
        with open(os.path.join(folder, 'SHA256SUMS')) as f:
            names = f.read()
    except OSError:
        return False
    return all(re.search(r'\sp%d-%s\.img$' % (n, LAYOUT[n][0]), names, re.M) and
               os.path.exists(os.path.join(folder, 'p%d-%s.img' % (n, LAYOUT[n][0]))) for n in (BOOT, SYSTEM))


# ------------------------------------------------------------------------------------------ the images

def raw_image(path, work):
    """The image as a raw file: a .gz is unpacked into work first."""
    if not path.endswith('.gz'):
        return path
    out = os.path.join(work, os.path.basename(path)[:-3])
    with gzip.open(path, 'rb') as src, open(out + '.partial', 'wb') as dst:
        shutil.copyfileobj(src, dst, 1 << 20)
    os.replace(out + '.partial', out)
    return out


def check_image(path):
    """A TECHO5 athena store: ext4, labeled techo5-store, a whole number of 4 KiB blocks."""
    size = os.path.getsize(path)
    with open(path, 'rb') as f:
        sb = f.read(2048)[1024:]
    if len(sb) < 136 or sb[56:58] != EXT4_MAGIC:
        fail('%s is not an ext4 image' % path)
    label = sb[120:136].rstrip(b'\0')
    if label != STORE_LABEL:
        fail("%s is labeled '%s', not '%s': not an athena store image (tools/linux/athena/mkstore.sh)"
             % (path, label.decode('ascii', 'replace'), STORE_LABEL.decode()))
    if size % 4096:
        fail('%s is %d bytes, not a whole number of blocks' % (path, size))
    return size


def unmount(adb, n):
    """TWRP may have a partition mounted; it has to be free before it is written."""
    for m in mounts_of(adb, dev(n)):
        adb.sh('umount %s' % quote(m))
    still = mounts_of(adb, dev(n))
    if still:
        fail('the %s partition is mounted at %s and would not unmount; unmount it in TWRP (Mount menu)\n'
             '   and run this again' % (WRITABLE[n], ', '.join(still)))


# The marker grow_store's script prints when the store was grown and checked clean.
GROWN = 'T5-STORE-GROWN'


def grow_store(adb):
    """Grow the store just written to fill its partition, here in TWRP, with nothing mounted. The first
    boot grows it online otherwise, but Amazon's 4.4 kernel flags the filesystem with errors as it
    mounts it, and then refuses the online grow at every boot: a unit left at the image's 200 MiB could
    never take an update (#192). Not done (TWRP without the tools, or the resize failing) leaves it to
    the first boot as before."""
    d = dev(SYSTEM)
    out = adb.sh('command -v e2fsck >/dev/null && command -v resize2fs >/dev/null || { echo no-tools; exit; }; '
                 'e2fsck -fy %s >/dev/null 2>&1; [ $? -le 1 ] || { echo e2fsck-failed; exit; }; '
                 'resize2fs %s >/dev/null 2>&1 || { echo resize-failed; exit; }; '
                 'e2fsck -fn %s >/dev/null 2>&1 && echo %s' % (d, d, d, GROWN))
    if GROWN in out:
        note('store grown to fill %s, and checked clean' % d)
    else:
        note('the store was not grown here (%s); the first boot grows it' % (out.strip() or 'no answer'))


def write_partition(adb, parts, n, image, want):
    """An image onto partition n, through TWRP's RAM disk, read back from the flash."""
    size = os.path.getsize(image)
    psize = target_ok(adb, parts, n)
    name = WRITABLE[n]
    if size > psize:
        fail('the %s image (%d MiB) is larger than the partition (%d MiB)' % (name, size >> 20, psize >> 20))
    unmount(adb, n)
    free = adb.sh("df -k /tmp | awk 'NR==2 {print $4}'")
    if free.isdigit() and int(free) * 1024 < size + (32 << 20):
        fail("TWRP's /tmp has %d MiB free, and the image needs %d MiB" % (int(free) >> 10, size >> 20))
    remote = '/tmp/techo5-%s.img' % name
    adb.push(image, remote)
    if first_word(adb.sh('sha256sum ' + remote)) != want:
        adb.sh('rm -f ' + remote)
        fail('the %s image changed on its way to the unit; nothing was written' % name)

    def readback():
        # From the flash rather than the page cache, which would only give back what was just written.
        return first_word(adb.sh('sync; echo 3 > /proc/sys/vm/drop_caches; head -c %d %s | sha256sum'
                                 % (size, dev(n))))

    for attempt in (1, 2):
        target_ok(adb, parts, n)
        adb.sh('dd if=%s of=%s bs=1048576 2>/dev/null; sync' % (remote, dev(n)))
        if readback() == want:
            adb.sh('rm -f ' + remote)
            return attempt
        note('the %s partition did not read back as written (try %d of 2)' % (name, attempt))
    fail('the %s partition did not read back as written, twice. Do not restart the unit from here.\n'
         '   The image is still in TWRP\'s /tmp; try again with this installer, or put Fire OS back\n'
         '   (see the way back in docs/install-athena.md).' % name)


# ------------------------------------------------------------------------------------------ settings

def unit_file(adb, path):
    out = adb.sh('[ -f %s ] && cat %s' % (quote(path), quote(path)))
    return out.strip() or None


def put_file(adb, local_dir, text, remote, mode, ready=None):
    """A small file onto the unit through adb push (no shell quoting of its contents), mode set after.
    Written as bytes, so Windows does not turn its line ends into CRLF on the way: wpa_supplicant and
    the daemon read these exactly. Readable only by its owner here, as write_private makes it. ready is
    Adb.push's: what must be true again before a retry, after TWRP resets USB."""
    tmp = os.path.join(local_dir, 'athena-setting.tmp')
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, 'O_BINARY', 0), 0o600)
    with os.fdopen(fd, 'wb') as f:
        f.write(text.encode('utf-8'))
    try:
        adb.push(tmp, remote, ready=ready)
    finally:
        os.remove(tmp)
    adb.sh('chmod %s %s' % (mode, quote(remote)))


def merge_wifi(existing, ssid_hex, psk_hex):
    """The unit's wpa_supplicant configuration (existing, or None) with the --wifi network added: its
    saved networks kept as they are, except one of the same name, which this one replaces. With no
    configuration yet, the one the unit writes, with this network in it."""
    text = (existing or '').replace('\r\n', '\n')
    if not text.strip():
        return WIFI_CONF % (ssid_hex, psk_hex)
    # A line at a time, as echod's wifi package reads it (lib/wifi blocks): a trimmed "network={" opens
    # a block and a trimmed "}" closes it, so indenting either changes nothing. Lines outside blocks,
    # comments included, are kept as they are.
    kept, block = [], None
    for line in text.split('\n'):
        if block is None:
            if line.strip() == 'network={':
                block = [line]
            else:
                kept.append(line)
            continue
        block.append(line)
        if line.strip() == '}':
            if wifi_ssid_hex(block) != ssid_hex.lower():
                kept.extend(block)
            block = None
    if block is not None:  # never closed: kept as it was
        kept.extend(block)
    while kept and not kept[-1].strip():
        kept.pop()
    return '\n'.join(kept) + '\n' + 'network={\n\tssid=%s\n\tpsk=%s\n}\n' % (ssid_hex, psk_hex)


def wifi_ssid_hex(block):
    """A network block's name as lowercase hex, or None: hex as written, "text" (what lies between the
    first quote and the last, as the supplicant reads it), or P"text" with its printf escapes."""
    for line in block:
        key, eq, v = line.strip().partition('=')
        if not eq or key != 'ssid':
            continue
        if v.startswith('P"') and v.endswith('"') and len(v) >= 3:
            out, s, i = bytearray(), v[2:-1], 0
            while i < len(s):
                if s[i] == '\\' and i + 1 < len(s):
                    c = s[i + 1]
                    if c == 'x' and re.fullmatch(r'[0-9a-fA-F]{2}', s[i + 2:i + 4]):
                        out.append(int(s[i + 2:i + 4], 16))
                        i += 4
                        continue
                    out += {'n': b'\n', 'r': b'\r', 't': b'\t', 'e': b'\x1b'}.get(c, c.encode('utf-8'))
                    i += 2
                    continue
                out += s[i].encode('utf-8')
                i += 1
            return out.hex()
        if v.startswith('"'):
            end = v.rfind('"')
            return v[1:end].encode('utf-8').hex() if end > 0 else None
        if re.fullmatch(r'(?:[0-9a-fA-F]{2})+', v):
            return v.lower()
        return None
    return None


def read_file(path):
    with open(path, 'rb') as f:
        return f.read()


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--serial', help="the unit's adb serial (adb devices); found, or asked for, when missing")
    ap.add_argument('--kernel', choices=('mainline', 'amazon'), default='mainline',
                    help="mainline (default): TECHO5's 64-bit kernel on boot; amazon: keep Amazon's kernel, "
                    'write system only')
    ap.add_argument('--image', help='a store image you built (tools/linux/athena/mkstore.sh), .img or .img.gz')
    ap.add_argument('--boot', help='with --image: a TECHO5 mainline boot image you built (its amp slot empty)')
    ap.add_argument('--release', default='latest', help="without --image: a release tag, or latest, whose "
                    "signed manifest names %s and %s" % (image_name('<version>'), boot_name('<version>')))
    ap.add_argument('--stock-boot', help="Fire OS's boot image, when the backup's boot is already TECHO5's "
                    '(the amp tuning is read from it)')
    ap.add_argument('--force-panel', action='store_true',
                    help='install the mainline kernel on a screen it is not tested on, or one it does not know')
    ap.add_argument('--name', help='the name Home Assistant shows, e.g. Kitchen; a unit that has one keeps it')
    ap.add_argument('--key-file', help='where the Home Assistant key is kept (default backups/<serial>/home-assistant.key)')
    ap.add_argument('--ssh-key', help='an SSH public key the unit accepts from the start (SSH is switched on)')
    ap.add_argument('--wifi', help="a Wi-Fi network to join (its passphrase is asked for); otherwise Fire OS's "
                    'saved one, or the Show\'s screen')
    ap.add_argument('--wifi-passphrase-file', help='a file holding the --wifi passphrase, for running from a script')
    ap.add_argument('--dry-run', action='store_true', help='check the unit and the images; write nothing')
    ap.add_argument('--force', action='store_true', help='do not ask before writing')
    ap.add_argument('--no-watch', action='store_true', help='restart the unit and stop; do not watch the first boot')
    ap.add_argument('--show-amp-tables', metavar='BOOT_IMG',
                    help="print the amp tunings in a Fire OS boot image and their sha256; needs no unit")
    ap.add_argument('--backups', default=default_dir('TECHO5_BACKUPS', 'backups'))
    ap.add_argument('--work', default=default_dir('TECHO5_WORK', 'build'))
    ap.add_argument('--adb', default='adb')
    a = ap.parse_args(argv)
    global MMC
    mainline = a.kernel == 'mainline'

    if a.show_amp_tables:
        show_amp_tables(a.show_amp_tables)
        return

    # ------------------------------------------------------------------------------------ 1. checks
    step('checks')
    need(a.adb, 'install the Android platform tools (adb)')
    for opt in ('image', 'boot', 'stock_boot'):
        path = getattr(a, opt)
        if path and not os.path.exists(path):
            fail('no file at %s' % path)
    if a.boot and not mainline:
        fail('--boot is for the mainline kernel; --kernel amazon leaves boot as it is')
    if mainline and bool(a.image) != bool(a.boot):
        fail('give both --image and --boot (images you built), or neither (the release\'s): a store and a '
             'kernel from different builds may not fit')
    a.serial = pick_unit(a.serial, a.adb, ('recovery',), 'Echo Show 8 in TWRP')
    adb = Adb(a.serial, a.adb)
    state = adb.state()
    if state != 'recovery':
        fail("adb does not see %s in TWRP (state '%s'): start TWRP (hold Volume Up while it powers on) and "
             'run this again' % (a.serial, state))
    if adb.sh('id').split('(')[0] != 'uid=0':
        fail('adb in TWRP is not root on %s' % a.serial)
    athena, (props, compat) = is_athena(adb)
    if not athena:
        fail("%s does not say it is an athena (properties '%s', device tree '%s')" % (a.serial, props, compat))
    MMC = find_mmc(adb)
    parts = partitions(adb)
    check_layout(parts)
    note('%s: an athena in TWRP; boot is %s, system %s (%d MiB)' % (a.serial, dev(BOOT), dev(SYSTEM),
                                                                    parts[SYSTEM][1] >> 20))
    os.makedirs(a.work, exist_ok=True)
    if mainline:
        note(check_panel(*panel_of(adb, a.work), force=a.force_panel))
    backup = os.path.join(a.backups, a.serial)
    key_file = a.key_file or os.path.join(backup, 'home-assistant.key')
    pub = None
    if a.ssh_key:
        with open(os.path.expanduser(a.ssh_key)) as f:
            pub = f.read().strip()
        if not pub.startswith(('ssh-', 'ecdsa-', 'sk-')) or '\n' in pub:
            fail('%s does not look like one SSH public key' % a.ssh_key)
    wifi = None
    if a.wifi:
        if a.wifi_passphrase_file:
            with open(a.wifi_passphrase_file, encoding='utf-8-sig') as f:
                wifi = wifi_conf(a.wifi, f.read().rstrip('\r\n'))
        else:
            wifi = ask_wifi(a.wifi)
        kv = dict(line.split('=', 1) for line in wifi.strip().split('\n'))
        wifi = (kv['ssid'], kv['psk'])
        note("Wi-Fi: '%s' (only its key goes to the unit)" % a.wifi)
    # Nothing is unmounted or written before the install is confirmed. userdata is only mounted, and
    # read-only when this installer mounts it.
    targets = (SYSTEM, BOOT) if mainline else (SYSTEM,)
    for n in targets:
        mounted = mounts_of(adb, dev(n))
        if mounted:
            note('the %s partition is mounted at %s; it is unmounted once you confirm'
                 % (WRITABLE[n], ', '.join(mounted)))
    ud = userdata_dir(adb, a.dry_run)
    if ud:
        note('userdata mounted at %s%s; TECHO5 keeps its settings in its techo5 folder'
             % (ud, ' (read-only until you confirm)' if ud == OWN_USERDATA else ''))
    else:
        note('dry run: userdata is not mounted in TWRP; the real run mounts it')
    state_dir = ud + '/techo5/misc/techo5' if ud else None
    unit_name = unit_file(adb, state_dir + '/name') if ud else None
    unit_psk = unit_file(adb, state_dir + '/psk') if ud else None
    if a.name is not None:
        a.name = ask_name(a.name, '')
    elif unit_name:
        a.name = unit_name
        note("name in Home Assistant: '%s' (already on the unit)" % a.name)
    else:
        a.name = ask_name(None, BOARD_NAME)

    # ------------------------------------------------------------------------------------ 2. backup
    step('backup')
    folder = os.path.join(backup, 'partitions')
    saved_boot = os.path.join(folder, 'p%d-boot.img' % BOOT)
    if backup_done(folder):
        note('partitions already saved: %s' % folder)
    elif a.dry_run:
        note('dry run: the partitions are saved by the real run')
    else:
        note('this reads about %d MiB over USB; system alone is %d MiB'
             % (sum(s for n, s in parts.values() if n not in NOT_SAVED) >> 20, parts[SYSTEM][1] >> 20))
        note('%d partitions saved and checked by sha256: %s' % (save_partitions(adb, parts, folder), folder))
    # What boot holds now, read fresh (the backup may be older than the last install).
    current_boot = read_file(read_partition(adb, BOOT, parts[BOOT][1], os.path.join(a.work, 'athena-boot-now.img')))
    os.remove(os.path.join(a.work, 'athena-boot-now.img'))
    boot_is_techo5 = is_techo5_boot(current_boot)
    note('boot now holds %s' % ("TECHO5's mainline kernel" if boot_is_techo5 else "Amazon's kernel"))
    # A backup made with TECHO5 already on the unit holds TECHO5, not Fire OS.
    backup_not_fire_os = os.path.exists(saved_boot) and is_techo5_boot(read_file(saved_boot))
    if backup_not_fire_os:
        note('WARNING: the backup in %s is NOT a Fire OS backup.\n'
             '   It was made with TECHO5 already installed: its p%d-boot.img holds TECHO5\'s kernel, and\n'
             '   writing it back does not bring Fire OS back. Keep the backup made before TECHO5 was first\n'
             '   installed: that one is the way back to Fire OS.' % (folder, BOOT))
    if not mainline and boot_is_techo5:
        fail("--kernel amazon needs Amazon's kernel on boot, and boot holds TECHO5's. Put Fire OS's boot back\n"
             '   first (the way back in docs/install-athena.md: %s to boot), then run this again.' % saved_boot)

    # ------------------------------------------------------------------------------------ 3. images
    step('the images')
    if a.image:
        src = os.path.abspath(a.image)
        boot_src = os.path.abspath(a.boot) if a.boot else None
        version = 'your images'
        note('store image: your own, %s' % src)
        if boot_src:
            note('boot image: your own, %s' % boot_src)
    else:
        rel = Release(REPO, a.release, a.work)
        version = rel.version
        src = rel.asset(image_name(rel.version))
        note('store image %s checked against the signed manifest' % os.path.basename(src))
        boot_src = None
        if mainline:
            boot_src = rel.asset(boot_name(rel.version))
            note('boot image %s checked against the signed manifest' % os.path.basename(boot_src))
    image = raw_image(src, a.work)
    size = check_image(image)
    want = sha256_file(image)
    if size > parts[SYSTEM][1]:
        fail('the image (%d MiB) is larger than the system partition (%d MiB)' % (size >> 20, parts[SYSTEM][1] >> 20))
    note('%s: %d MiB, sha256 %s' % (os.path.basename(image), size >> 20, want))
    boot_image = None
    if mainline:
        shipped = read_file(boot_src)
        check_techo5_boot(shipped, os.path.basename(boot_src))
        if len(shipped) > parts[BOOT][1]:
            fail('the boot image (%d bytes) is larger than the boot partition (%d)' % (len(shipped), parts[BOOT][1]))
        note('%s: %d bytes, header id right, amp tuning slot empty' % (os.path.basename(boot_src), len(shipped)))

        # -------------------------------------------------------------------------------- 4. amp
        step('the speaker tuning')
        if a.stock_boot:
            stock_path, what = os.path.abspath(a.stock_boot), 'the boot image in --stock-boot'
        elif os.path.exists(saved_boot):
            stock_path, what = saved_boot, "the backup's boot (%s)" % saved_boot
        else:
            stock_path, what = None, "Fire OS's boot on the unit"
        stock = read_file(stock_path) if stock_path else current_boot
        if is_techo5_boot(stock):
            fail("%s is TECHO5's, not Fire OS's, so it has no speaker tuning to read. Pass Fire OS's boot\n"
                 '   with --stock-boot (the p20-boot.img of the backup made before TECHO5 was first installed).' % what)
        live = compat.split()
        table, letter, desc, cfg, source = stock_amp_tuning(stock, live, what)
        live_cfg = live_speaker_config(adb)
        if live_cfg and live_cfg != {cfg}:
            fail('TWRP\'s device tree says speaker-config %s, and Fire OS\'s says %d; not guessing which is right'
                 % (sorted(live_cfg), cfg))
        note('speaker-config %d (%s): table %s, %s, %d bytes, sha256 %s'
             % (cfg, source, letter, desc, len(table), hashlib.sha256(table).hexdigest()[:16]))
        filled = fill_slot(shipped, table, os.path.basename(boot_src))
        if read_slot(filled) != table or BootImage(filled).stored_id() != BootImage(filled).computed_id():
            fail('the filled boot image does not check out; nothing was written')
        boot_image = os.path.join(a.work, 'athena-boot-filled.img')
        with open(boot_image, 'wb') as f:
            f.write(filled)
        boot_want = hashlib.sha256(filled).hexdigest()
        note('tuning in the boot image\'s slot, header id recomputed: sha256 %s' % boot_want)

    if a.dry_run:
        print('\nDry run: the unit and the images check out; nothing was written to the unit.')
        return

    lines = ["About to install TECHO5 (%s) on %s (%s) as '%s', from TWRP." % (version, a.serial, BOARD_NAME, a.name),
             'This writes the system partition (%s), which replaces Fire OS (or the TECHO5 there now).' % dev(SYSTEM)]
    if mainline:
        lines += ["It also writes the boot partition (%s) with TECHO5's mainline kernel." % dev(BOOT),
                  'vendor, recovery (TWRP) and the unlock are not touched; userdata only gains settings in its']
    else:
        lines += ['boot, vendor, recovery (TWRP) and the unlock are not touched; userdata only gains settings in its']
    lines += ['techo5 folder. The backup is in %s.' % folder]
    confirm(a.force, lines)
    for n in targets:
        mounted = mounts_of(adb, dev(n))
        if mounted:
            unmount(adb, n)
            note('%s partition unmounted (TWRP had it at %s)' % (WRITABLE[n], ', '.join(mounted)))

    # ------------------------------------------------------------------------------------ 5. write
    # system first: a unit left with TECHO5's store and Amazon's kernel still starts TECHO5 (the store
    # works under both); one left with TECHO5's kernel and Fire OS's system would not.
    step('write the system partition')
    tries = write_partition(adb, parts, SYSTEM, image, want)
    note('%s written and read back from the flash (sha256 matches%s)' % (dev(SYSTEM), '' if tries == 1 else ', second try'))
    grow_store(adb)
    if mainline:
        step('write the boot partition')
        tries = write_partition(adb, parts, BOOT, boot_image, boot_want)
        note('%s written and read back from the flash (sha256 matches%s)' % (dev(BOOT), '' if tries == 1 else ', second try'))

    # ------------------------------------------------------------------------------------ 6. settings
    step('settings')
    os.makedirs(os.path.dirname(key_file) or '.', exist_ok=True)
    local_psk = None
    if os.path.exists(key_file):
        with open(key_file) as f:
            local_psk = f.read().strip()
        if not valid_api_key(local_psk):
            fail('the key in %s is not 32 bytes of base64' % key_file)
    if unit_psk and valid_api_key(unit_psk):
        # Home Assistant knows this unit by the key it has; keep that one.
        psk = unit_psk
        if local_psk != psk:
            if local_psk:
                os.replace(key_file, key_file + '.old')
                note('the key in %s differed from the unit\'s; kept as %s.old' % (key_file, key_file))
            write_private(key_file, psk)
        note('Home Assistant key: the one already on the unit (copy in %s)' % key_file)
    elif local_psk:
        psk = local_psk
        note('Home Assistant key: the existing one in %s' % key_file)
    else:
        psk = new_api_key()
        write_private(key_file, psk)
        note('Home Assistant key: new, in %s' % key_file)
    userdata_writable(adb, ud)
    ready = userdata_ready(adb, ud)
    linux_dir = ud + '/techo5/techo5-linux'
    adb.sh('mkdir -p %s %s && chmod 700 %s' % (quote(state_dir), quote(linux_dir), quote(state_dir)))
    put_file(adb, a.work, a.name + '\n', state_dir + '/name', '644', ready)
    put_file(adb, a.work, psk + '\n', state_dir + '/psk', '600', ready)
    done = ['name', 'Home Assistant key']
    if pub:
        adb.sh('mkdir -p %s/ssh && chmod 700 %s/ssh' % (quote(state_dir), quote(state_dir)))
        put_file(adb, a.work, pub + '\n', state_dir + '/ssh/authorized_keys', '600', ready)
        if not unit_file(adb, state_dir + '/state.json'):
            put_file(adb, a.work, '{"security":{"ssh":true}}\n', state_dir + '/state.json', '600', ready)
        done.append('SSH key')
    if wifi:
        # Added to the networks the unit has saved, not in place of them.
        conf = linux_dir + '/wpa_supplicant.conf'
        put_file(adb, a.work, merge_wifi(unit_file(adb, conf), *wifi), conf, '600', ready)
        done.append('Wi-Fi')
    if ud == OWN_USERDATA:
        adb.sh('sync; umount /tmp/t5-userdata')
    else:
        adb.sh('sync')
    note('%s written to userdata/techo5' % ', '.join(done))

    # ------------------------------------------------------------------------------------ 7. boot
    step('first boot')
    adb.reboot()
    note('restarting into TECHO5; the first boot grows the store to fill the partition, then starts slot a')
    if not a.no_watch:
        console = Console(a.serial, CONSOLE_TECHO5)
        deadline = time.time() + 300
        seen = ''
        while time.time() < deadline:
            seen = console.run('echo slot=$(cat /run/techo5/slot 2>/dev/null)- daemon=$(pidof techo5)-', 4) or ''
            if re.search(r'slot=a- daemon=\d', seen):
                break
            time.sleep(5)
        if re.search(r'slot=a- daemon=\d', seen):
            o = console.run('cat /etc/techo5-release; STORE=/store slotctl status | head -1; '
                            'tail -1 /data/techo5-linux/grow.log 2>/dev/null', 8) or ''
            for line in o.split('\n'):
                note(line)
        else:
            note('the USB serial console did not show slot a with the daemon within 5 minutes. That may only be\n'
                 '   this computer not seeing the console: the Show\'s screen says what it is doing (RESCUE if\n'
                 '   no slot started). /data/techo5-linux/ on the unit has the boot logs.')

    print("\nDone. '%s' runs TECHO5 from slot a; the slot commits itself after five healthy minutes." % a.name)
    print('Home Assistant finds it as an ESPHome device. When it asks for the encryption key, paste:\n\n    %s\n\n'
          '(kept in %s)' % (psk, key_file))
    if backup_not_fire_os:
        print('\nWARNING: the backup in %s is NOT a Fire OS backup. It was made with TECHO5 already\n'
              'installed, so writing it back does not return the unit to Fire OS. The way back is the backup\n'
              'made before TECHO5 was first installed (docs/install-athena.md, "The way back to Fire OS").'
              % folder)
        return
    restore = [('system', SYSTEM)] + ([('boot', BOOT)] if mainline else [])
    print('\nThe way back to Fire OS (TWRP stays in recovery; %s and the unlock were never changed):'
          % ('vendor' if mainline else 'boot, vendor'))
    print('  from TWRP (adb in recovery), streaming the backup straight to each partition:')
    for name, n in restore:
        print('      adb exec-in "dd of=%s bs=1048576" < %s' % (dev(n), os.path.join(folder, 'p%d-%s.img' % (n, name))))
    print('      (PowerShell has no "<": run each line through cmd /c "...")')
    print('  Fire OS then replaces TWRP with Amazon\'s recovery on its first start: write p7-recovery.img back')
    print('  with mtkclient (mtk w recovery ...) if you want TWRP again. Never write tee1, lk or the preloader.')


if __name__ == '__main__':
    run_main(main)
