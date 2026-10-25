#!/usr/bin/env python3
"""Pack a Skylight 150-CAL boot image the stock Rockchip U-Boot accepts.

Android boot image header v2 with the stock load addresses, our DTB in the v2 dtb section, and a
Rockchip resource image ("second") whose DTB entries (rk-kernel.dtb, d156-mipi.dtb, d156-edp.dtb)
are all our DTB, so U-Boot cannot pick a stock tree. Logo entries are copied from the stock
resource image when one is given.

  pack-sky.py --kernel Image --dtb sky.dtb --ramdisk initrd.cpio.gz --cmdline "..." \
              [--stock-resource resource.img] --out boot-sky.img
"""
import argparse, hashlib, struct

PAGE = 2048
# From the unit's stock boot image header.
KERNEL_ADDR, RAMDISK_ADDR, SECOND_ADDR = 0x10008000, 0x11000000, 0x10F00000
TAGS_ADDR, DTB_ADDR = 0x10000100, 0x11F00000
OS_VERSION = 0x18000163  # stock header value (Android 12, 2022-03)
BLK = 512
DTB_NAMES = ("rk-kernel.dtb", "d156-mipi.dtb", "d156-edp.dtb")
KEEP_FROM_STOCK = ("logo.bmp", "logo_kernel.bmp")


def pad(b, n):
    return b + bytes(-len(b) % n)


def read_resource(path):
    """Return {name: bytes} from a Rockchip RSCE resource image."""
    r = open(path, "rb").read()
    assert r[:4] == b"RSCE"
    hdr_blks, tbl_blks, count = r[8], r[10], struct.unpack_from("<I", r, 12)[0]
    out = {}
    for i in range(count):
        e = r[(hdr_blks + i * tbl_blks) * BLK:][:BLK]
        assert e[:4] == b"ENTR"
        name = e[4:224].split(b"\0")[0].decode()
        off, size = struct.unpack_from("<II", e, 260)
        out.setdefault(name, r[off * BLK:off * BLK + size])
    return out


def make_resource(entries):
    """entries: list of (name, bytes). Layout as stock: header block, one block per entry, data."""
    n = len(entries)
    hdr = pad(b"RSCE" + struct.pack("<HHBBBBI", 0, 0, 1, 1, 1, 0, n), BLK)
    tbl, data = b"", b""
    off = 1 + n
    for name, blob in entries:
        sha1 = hashlib.sha1(blob).digest()
        e = b"ENTR" + name.encode().ljust(220, b"\0") + sha1.ljust(32, b"\0")
        e += struct.pack("<III", 20, off + len(data) // BLK, len(blob))
        tbl += pad(e, BLK)
        data += pad(blob, BLK)
    return hdr + tbl + data


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--kernel", required=True)
    ap.add_argument("--dtb", required=True)
    ap.add_argument("--ramdisk", required=True)
    ap.add_argument("--cmdline", default="")
    ap.add_argument("--stock-resource")
    ap.add_argument("--logo", help="a 1920x1080 BMP to show as U-Boot's boot logo (logo.bmp)")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()

    kernel = open(a.kernel, "rb").read()
    dtb = open(a.dtb, "rb").read()
    ramdisk = open(a.ramdisk, "rb").read()
    assert dtb[:4] == b"\xd0\x0d\xfe\xed", "not a DTB"
    assert kernel[0x38:0x3c] == b"ARM\x64", "not an arm64 Image"

    entries = [(n, dtb) for n in DTB_NAMES]
    if a.logo:
        logo = open(a.logo, "rb").read()
        assert logo[:2] == b"BM", "the logo is not a BMP"
        entries.append(("logo.bmp", logo))
    elif a.stock_resource:
        stock = read_resource(a.stock_resource)
        entries += [(n, stock[n]) for n in KEEP_FROM_STOCK if n in stock]
    second = make_resource(entries)

    cmd = a.cmdline.encode()
    assert len(cmd) < 512
    header_size = 1660
    h = b"ANDROID!"
    h += struct.pack("<10I", len(kernel), KERNEL_ADDR, len(ramdisk), RAMDISK_ADDR,
                     len(second), SECOND_ADDR, TAGS_ADDR, PAGE, 2, OS_VERSION)
    h += bytes(16)                      # name
    h += cmd.ljust(512, b"\0")          # cmdline
    h += bytes(32)                      # id
    h += bytes(1024)                    # extra_cmdline
    h += struct.pack("<IQI", 0, 0, header_size)   # recovery_dtbo size/offset, header_size
    h += struct.pack("<IQ", len(dtb), DTB_ADDR)
    assert len(h) == header_size, len(h)

    # id = SHA-1 over the parts, as mkbootimg does (U-Boot may check it).
    s = hashlib.sha1()
    for blob in (kernel, ramdisk, second):
        s.update(blob)
        s.update(struct.pack("<I", len(blob)))
    s.update(b"")
    s.update(struct.pack("<I", 0))      # recovery_dtbo
    s.update(dtb)
    s.update(struct.pack("<I", len(dtb)))
    h = h[:576] + s.digest().ljust(32, b"\0") + h[608:]

    img = pad(h, PAGE) + pad(kernel, PAGE) + pad(ramdisk, PAGE) + pad(second, PAGE) + pad(dtb, PAGE)
    open(a.out, "wb").write(img)
    print("%s: %d bytes (kernel %d, ramdisk %d, second %d, dtb %d)"
          % (a.out, len(img), len(kernel), len(ramdisk), len(second), len(dtb)))


if __name__ == "__main__":
    main()
