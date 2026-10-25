#!/usr/bin/env python3
"""Make the Skylight's new GPT from its stock one: userdata (the last entry, 14) becomes techo5-store
(2 GiB) and techo5-data takes the rest of the same range as a new entry 15. Nothing else changes: the
disk GUID, the other 13 entries and userdata's own unique GUID are kept.

  mkgpt.py first-sectors.img total-sectors out-primary.bin out-backup.bin

first-sectors.img: the unit's first 34 sectors or more (protective MBR, header, entries), as read from
it or from its backup. total-sectors: the eMMC's size in 512-byte sectors (sysfs size). Every LBA comes
from these two; nothing of one unit is built in. out-primary: LBA 1..33 (header + entries); out-backup:
the last 33 LBAs (entries + header).

install-skylight.py imports this file and calls stock_layout() and make_gpt() on the GPT it reads from
the unit, so the bench and the installer make the same table."""
import struct
import sys
import uuid
import zlib

SECTOR = 512
# techo5-store's size: 2 GiB, the slot store (two slots and the rescue environment).
STORE_SECTORS = 4194304
# The smallest techo5-data this makes: TECHO5's state, logs and the photo cache need room.
MIN_DATA_SECTORS = 2097152
# Partitions start on 1 MiB boundaries in the stock table; the new ones keep to that.
ALIGN = 2048
LINUX_FS = uuid.UUID("0fc63daf-8483-4772-8e79-3d69d8477de4").bytes_le

# The stock partition table, in order, as Rockchip's parameter file for the D156 firmware lays it out.
STOCK_NAMES = ("security", "uboot", "trust", "misc", "dtbo", "vbmeta", "boot", "recovery", "backup",
               "cache", "metadata", "baseparameter", "super", "userdata")
# The same, after the install: what install.sh writes and what TECHO5's /init looks for.
TECHO5_NAMES = STOCK_NAMES[:-1] + ("techo5-store", "techo5-data")


class GPTError(Exception):
    """A table this tool will not work from, said plainly."""


def _crc(b):
    return zlib.crc32(bytes(b)) & 0xffffffff


def name_of(e):
    return bytes(e[56:128]).decode("utf-16le", "replace").split("\0")[0]


def parse(first, total=None):
    """The primary GPT in first (the disk's first sectors): (header bytes, entries bytes, info). The
    header's and the entries' CRCs are checked; with total, so is where the header says the backup is."""
    if len(first) < 34 * SECTOR:
        raise GPTError("need the disk's first 34 sectors, got %d bytes" % len(first))
    hdr = bytearray(first[SECTOR:2 * SECTOR])
    if hdr[:8] != b"EFI PART":
        raise GPTError("no GPT header at LBA 1")
    rev, hsz, hcrc, _r, cur, alt, first_lba, last_lba = struct.unpack_from("<IIIIQQQQ", hdr, 8)
    elba, n, esz, ecrc = struct.unpack_from("<QIII", hdr, 72)
    if hsz != 92 or esz != 128 or elba != 2 or cur != 1 or n * esz != 32 * SECTOR:
        raise GPTError("unexpected GPT header (size %d, entry size %d, entries at %d, %d entries)"
                       % (hsz, esz, elba, n))
    h0 = bytearray(hdr[:hsz])
    struct.pack_into("<I", h0, 16, 0)
    if _crc(h0) != hcrc:
        raise GPTError("the GPT header's CRC is wrong")
    ents = bytearray(first[elba * SECTOR:elba * SECTOR + n * esz])
    if _crc(ents) != ecrc:
        raise GPTError("the GPT entries' CRC is wrong")
    if total is not None and alt != total - 1:
        raise GPTError("the GPT says its backup is at LBA %d, but the disk's last LBA is %d" % (alt, total - 1))
    parts = []
    for i in range(n):
        e = ents[i * esz:(i + 1) * esz]
        if bytes(e[:16]) == bytes(16):
            continue
        fl, ll, attr = struct.unpack_from("<QQQ", e, 32)
        parts.append((i + 1, name_of(e), fl, ll))
    info = {"first_usable": first_lba, "last_usable": last_lba, "alt": alt, "entries": n,
            "entry_size": esz, "parts": parts}
    return hdr[:hsz], ents, info


def parse_backup(last33, total):
    """The backup GPT (the disk's last 33 sectors): (header bytes, entries bytes), CRCs checked."""
    if len(last33) != 33 * SECTOR:
        raise GPTError("need the disk's last 33 sectors, got %d bytes" % len(last33))
    hdr = bytearray(last33[32 * SECTOR:33 * SECTOR])
    if hdr[:8] != b"EFI PART":
        raise GPTError("no backup GPT header at the disk's last LBA")
    hsz, hcrc = struct.unpack_from("<II", hdr, 12)
    cur, alt = struct.unpack_from("<QQ", hdr, 24)
    elba, n, esz, ecrc = struct.unpack_from("<QIII", hdr, 72)
    if hsz != 92 or cur != total - 1 or alt != 1 or elba != total - 33 or n * esz != 32 * SECTOR:
        raise GPTError("unexpected backup GPT header")
    h0 = bytearray(hdr[:hsz])
    struct.pack_into("<I", h0, 16, 0)
    if _crc(h0) != hcrc:
        raise GPTError("the backup GPT header's CRC is wrong")
    ents = bytes(last33[:32 * SECTOR])
    if _crc(ents) != ecrc:
        raise GPTError("the backup GPT entries' CRC is wrong")
    return hdr[:hsz], ents


def stock_layout(first, total, last33=None, store_sectors=None, min_data=None):
    """Checks that the table is the stock one this tool knows and returns {name: (number, first, last)}.
    The names must be STOCK_NAMES, numbered 1..14 in that order; userdata must be the last partition,
    1 MiB aligned, inside the usable range and large enough for techo5-store and techo5-data; with
    last33, the backup table must hold the same entries."""
    store_sectors = store_sectors or STORE_SECTORS
    min_data = min_data or MIN_DATA_SECTORS
    _, ents, info = parse(first, total)
    names = tuple(p[1] for p in info["parts"])
    if names != STOCK_NAMES or [p[0] for p in info["parts"]] != list(range(1, len(STOCK_NAMES) + 1)):
        if names == TECHO5_NAMES:
            raise GPTError("the table already holds techo5-store and techo5-data: TECHO5 is installed")
        raise GPTError("the partitions are %s, not the stock %s" % (", ".join(names), ", ".join(STOCK_NAMES)))
    if last33 is not None:
        _, bents = parse_backup(last33, total)
        if bents != bytes(ents):
            raise GPTError("the backup GPT's entries differ from the primary's")
    layout = {p[1]: (p[0], p[2], p[3]) for p in info["parts"]}
    prev_end = 0
    for num, name, fl, ll in info["parts"]:
        if fl <= prev_end or ll < fl:
            raise GPTError("partition %d (%s) is out of order or empty" % (num, name))
        prev_end = ll
    _, fl, ll = layout["userdata"]
    if fl < info["first_usable"] or ll > info["last_usable"]:
        raise GPTError("userdata (%d..%d) is outside the usable range %d..%d"
                       % (fl, ll, info["first_usable"], info["last_usable"]))
    if fl % ALIGN or store_sectors % ALIGN:
        raise GPTError("userdata starts at LBA %d, not on a 1 MiB boundary" % fl)
    if ll - fl + 1 < store_sectors + min_data:
        raise GPTError("userdata is %d MiB; techo5-store and techo5-data need %d MiB"
                       % ((ll - fl + 1) // 2048, (store_sectors + min_data) // 2048))
    return layout


def _entry(esz, first_lba, last_lba, label, unique):
    x = bytearray(esz)
    x[0:16] = LINUX_FS
    x[16:32] = unique
    struct.pack_into("<QQQ", x, 32, first_lba, last_lba, 0)
    x[56:56 + 72] = label.encode("utf-16le").ljust(72, b"\0")
    return x


def make_gpt(first, total, last33=None, store_sectors=None, min_data=None, data_guid=None):
    """(primary LBA 1..33, backup last 33 LBAs, geometry) for the new table, from the stock one.
    geometry: total, store_first, store_last, data_first, data_last, backup_lba (where the backup goes).
    techo5-data's unique GUID, unless given, is made from the disk's GUID and userdata's: the same
    unit always gets the same table (so a staged install can be checked again), two units never do."""
    store_sectors = store_sectors or STORE_SECTORS
    stock_layout(first, total, last33, store_sectors, min_data)
    hdr, ents, info = parse(first, total)
    esz, n = info["entry_size"], info["entries"]
    i = len(STOCK_NAMES) - 1                     # userdata, entry 14
    e = ents[i * esz:(i + 1) * esz]
    fl, ll = struct.unpack_from("<QQ", e, 32)
    store_end = fl + store_sectors - 1
    if any(ents[(i + 1) * esz:(i + 2) * esz]):
        raise GPTError("entry 15 is not free")
    ents[i * esz:(i + 1) * esz] = _entry(esz, fl, store_end, "techo5-store", bytes(e[16:32]))
    if data_guid is None:
        seed = "techo5-data:" + (bytes(hdr[56:72]) + bytes(e[16:32])).hex()
        data_guid = uuid.uuid5(uuid.NAMESPACE_OID, seed).bytes_le
    ents[(i + 1) * esz:(i + 2) * esz] = _entry(esz, store_end + 1, ll, "techo5-data", data_guid)
    ecrc = _crc(ents)

    def header(cur_lba, alt_lba, ent_lba):
        h = bytearray(hdr)
        struct.pack_into("<QQ", h, 24, cur_lba, alt_lba)
        struct.pack_into("<Q", h, 72, ent_lba)
        struct.pack_into("<I", h, 88, ecrc)
        struct.pack_into("<I", h, 16, 0)
        struct.pack_into("<I", h, 16, _crc(h))
        return bytes(h).ljust(SECTOR, b"\0")

    nsec = n * esz // SECTOR
    prim = header(1, total - 1, 2) + bytes(ents)
    back = bytes(ents) + header(total - 1, 1, total - 1 - nsec)
    assert len(prim) == (1 + nsec) * SECTOR and len(back) == (nsec + 1) * SECTOR
    geo = {"total": total, "store_first": fl, "store_last": store_end, "data_first": store_end + 1,
           "data_last": ll, "backup_lba": total - 1 - nsec}
    return prim, back, geo


def main(argv):
    if len(argv) != 4:
        print(__doc__, file=sys.stderr)
        return 2
    src, total, outp, outb = argv[0], int(argv[1]), argv[2], argv[3]
    with open(src, "rb") as f:
        d = f.read()
    try:
        _, _, info = parse(d, total)
        for num, name, fl, ll in info["parts"]:
            print(num, name, (fl, ll))
        prim, back, geo = make_gpt(d, total)
    except GPTError as e:
        print("mkgpt: %s" % e, file=sys.stderr)
        return 1
    with open(outp, "wb") as f:
        f.write(prim)
    with open(outb, "wb") as f:
        f.write(back)
    print("new: 14 techo5-store", geo["store_first"], geo["store_last"],
          "| 15 techo5-data", geo["data_first"], geo["data_last"])
    print("primary", len(prim), "bytes at LBA 1; backup", len(back), "bytes at LBA", geo["backup_lba"])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
