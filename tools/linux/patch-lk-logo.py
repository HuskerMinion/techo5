#!/usr/bin/env python3
"""Replace the boot logo compiled into the cronos LK (bootloader) image.

The `logo` partition on the Echo Show 5 is empty; what LK paints is compiled
into LK itself as five zlib bundles (u32 count=1, u32 total, u32 offset=12,
then zlib of a raw 32-bit BGRA image): the "amazon" wordmark (315×170, drawn
centred on black), "Booting...", two battery pictures and the full-screen
over-temperature thermometer (960×480). LK knows each image's size from code,
not from the bundle, so a replacement must decompress to exactly the same
number of bytes. LK's data table holds a pointer to each bundle as a load
address (0x4BD00000 + file offset − 512).

This appends a new bundle after the LK image (≈384 KB used of a 1 MB
partition), repoints the wordmark's pointer at it and grows the MTK header's
size so the preloader loads the appended bytes. Nothing else in LK changes.

    python3 patch-lk-logo.py lk-amonet-cronos.img lk-techo5.img logo.png [--check]

The image is fitted into 315×170 on black, landscape as LK stores it (LK turns
it onto the portrait panel itself). Flashing LK is NOT recoverable with a
fastboot command if LK stops working — fastboot lives in LK; amonet's bootrom
path is the fallback. Use --check first.
"""
import argparse
import struct
import sys
import zlib

from PIL import Image

BASE = 0x4BD00000
HDR = 512
MAGIC = 0x58881688

# The wordmark bundle in the amonet LK for cronos (lk-amonet-cronos.img): file offset of its
# header and the image size LK draws it at. Verified by decoding it.
WORDMARK_OFFSET = 335736
WORDMARK_SIZE = (315, 170)


def bundles(d):
    """Find single-image logo bundles: (file offset, total, raw size)."""
    out = []
    i = HDR
    while i < len(d) - 16:
        c, t, o = struct.unpack_from("<III", d, i)
        if c == 1 and o == 12 and 16 < t < 1 << 20 and i + t <= len(d):
            try:
                raw = zlib.decompress(d[i + 12 : i + t])
                out.append((i, t, len(raw)))
                i += t
                continue
            except zlib.error:
                pass
        i += 4
    return out


def pointer_sites(d, off):
    want = struct.pack("<I", BASE + off - HDR)
    return [i for i in range(HDR, len(d) - 4, 4) if d[i : i + 4] == want]


def key_out(img, tolerance):
    """Make pixels close to the corner color transparent: LK paints on black, and a mark drawn on
    its own dark ground would otherwise sit in a visible rectangle."""
    img = img.convert("RGBA")
    bg = img.getpixel((0, 0))[:3]
    px = img.load()
    for y in range(img.height):
        for x in range(img.width):
            r, g, b, a = px[x, y]
            dist = max(abs(r - bg[0]), abs(g - bg[1]), abs(b - bg[2]))
            if dist <= tolerance:
                px[x, y] = (0, 0, 0, 0)
            elif dist <= 2 * tolerance:
                # soften the edge: partly transparent towards black
                k = (dist - tolerance) / tolerance
                px[x, y] = (int(r * k), int(g * k), int(b * k), a)
    return img


def fit(img, size):
    """Scale img to fit size, centered on black, as RGBA."""
    w, h = size
    img = img.convert("RGBA")
    scale = min(w / img.width, h / img.height)
    if scale != 1:
        img = img.resize((max(1, round(img.width * scale)), max(1, round(img.height * scale))), Image.LANCZOS)
    out = Image.new("RGBA", size, (0, 0, 0, 255))
    out.paste(img, ((w - img.width) // 2, (h - img.height) // 2), img)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("lk_in")
    ap.add_argument("lk_out")
    ap.add_argument("image")
    ap.add_argument("--bundle", type=lambda v: int(v, 0), default=WORDMARK_OFFSET,
                    help="file offset of the bundle to replace (default: the wordmark)")
    ap.add_argument("--size", default="%dx%d" % WORDMARK_SIZE, help="image size LK draws that bundle at")
    ap.add_argument("--check", action="store_true", help="only report what would be patched")
    ap.add_argument("--extract", metavar="PNG", help="also write the bundle's current image here")
    ap.add_argument("--key", type=int, default=0, metavar="TOL",
                    help="key the image's corner color (within TOL per channel) to black first")
    ap.add_argument("--preview", metavar="PNG", help="write the image as LK will draw it")
    ap.add_argument("--colors", type=int, default=0, metavar="N",
                    help="quantize to N colors first (flat colors compress far better)")
    ap.add_argument("--in-place", action="store_true",
                    help="overwrite the old bundle in its own slot instead of appending; the new "
                         "bundle must fit. Required for the kaeru copy in expdb, whose stage-2 code "
                         "follows the LK payload and whose header size must not change")
    a = ap.parse_args()
    w, h = (int(v) for v in a.size.split("x"))

    d = bytearray(open(a.lk_in, "rb").read())
    magic, size = struct.unpack_from("<II", d, 0)
    if magic != MAGIC or d[8:10] != b"LK":
        sys.exit("not an MTK LK image")
    end = HDR + size
    found = {b[0]: b for b in bundles(d)}
    if a.bundle not in found:
        sys.exit(f"no bundle at {a.bundle}; found {sorted(found)}")
    off, total, rawlen = found[a.bundle]
    if rawlen != w * h * 4:
        sys.exit(f"bundle at {off} decompresses to {rawlen} bytes, not {w}x{h}x4")
    sites = pointer_sites(d, off)
    if len(sites) != 1:
        sys.exit(f"expected one pointer to the bundle at {off}, found {sites}")
    print(f"LK payload {size} bytes (ends at {end}); bundle at {off} ({total} bytes, {w}x{h}), pointer at {sites[0]}")

    if a.extract:
        raw = zlib.decompress(d[off + 12 : off + total])
        Image.frombuffer("RGBA", (w, h), raw, "raw", "BGRA", 0, 1).convert("RGB").save(a.extract)
        print(f"current image written to {a.extract}")

    src = Image.open(a.image)
    if a.key:
        src = key_out(src, a.key)
    img = fit(src, (w, h))
    if a.colors:
        img = img.convert("RGB").quantize(a.colors, dither=Image.NONE).convert("RGBA")
    if a.preview:
        img.convert("RGB").save(a.preview)
    raw = img.tobytes("raw", "BGRA")
    assert len(raw) == rawlen
    z = zlib.compress(raw, 9)
    bundle = struct.pack("<III", 1, 12 + len(z), 12) + z
    if a.in_place:
        if len(bundle) > total:
            sys.exit(f"new bundle is {len(bundle)} bytes, old slot holds {total}: shrink the image "
                     f"or lower --colors")
        print(f"new image: {len(raw)} raw -> {len(z)} zlib; bundle {len(bundle)} bytes in place of "
              f"{total} at {off}")
        if a.check:
            return
        d[off : off + total] = bundle + bytes(total - len(bundle))
        open(a.lk_out, "wb").write(d)
        print(f"wrote {a.lk_out}: header and pointer unchanged")
        return
    new_off = (end + 15) & ~15
    if new_off + len(bundle) > len(d):
        sys.exit("new bundle does not fit the partition")
    print(f"new image: {len(raw)} raw -> {len(z)} zlib; bundle {len(bundle)} bytes at {new_off}")
    if a.check:
        return

    d[new_off : new_off + len(bundle)] = bundle
    struct.pack_into("<I", d, sites[0], BASE + new_off - HDR)
    struct.pack_into("<I", d, 4, new_off + len(bundle) - HDR)
    open(a.lk_out, "wb").write(d)
    print(f"wrote {a.lk_out}: header size {new_off + len(bundle) - HDR}, pointer -> {BASE + new_off - HDR:#x}")


if __name__ == "__main__":
    main()
