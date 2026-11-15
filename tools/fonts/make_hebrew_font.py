#!/usr/bin/env python3
"""Makes the Hebrew font a device downloads the first time its screen draws Hebrew: one file,
techo5-noto-sans-hebrew.tar.gz, with NotoSansHebrew-Regular.ttf and its license, OFL.txt.

The fonts the screen offers draw Latin only (make_fonts.py); a character none of them has is drawn
from the device's own fonts folder (echod/internal/lib/fonts, own.go). For Hebrew, the daemon fetches
this archive into that folder (feature/cjkfont), so Hebrew draws under whichever screen font is chosen,
nothing has to be put there by hand, and no image carries it. It is Noto Sans Hebrew from google/fonts
(SIL Open Font License 1.1), at the commit and SHA-256 below, cut down to:

  - one static instance, weight 400 and width 100: the daemon's font package does not apply a variable
    font's axes;
  - the Hebrew block (letters, nikud, cantillation marks, maqaf, geresh, gershayim), the Hebrew
    presentation forms, the new shekel sign and the dotted circle marks are shown on alone;
  - no layout features but 'kern', 'mark' and 'mkmk' (nikud placed on its letter), no hinting.

The same fontTools, the same source and this script give the same bytes, and the archive is written
the same way every time, so the size and SHA-256 feature/cjkfont pins can be checked by anyone:

    pip install fonttools==4.65.0
    python3 tools/fonts/make_hebrew_font.py [out.tar.gz]

Fetches its source from GitHub; writes bin/techo5-noto-sans-hebrew.tar.gz by default.
"""
import gzip
import hashlib
import io
import os
import sys
import tarfile
import urllib.request

import fontTools
from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

FONTTOOLS = "4.65.0"
COMMIT = "e7db6ca603fd4862010383ff17ffd6cc4ffac1c0"
BASE = "https://raw.githubusercontent.com/google/fonts/" + COMMIT + "/ofl/notosanshebrew/"
SOURCES = {
    "NotoSansHebrew%5Bwdth,wght%5D.ttf": "7ef36a2c3593758cdb622e1bdef4f84523e92fbc3ccc667438dd80ff54c2de88",
    "OFL.txt": "9b9fe028b5ba74d231659a1bbaf0ed09b11e759d1ca6a070999e16d151616b47",
}
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")

# A fixed time for the font's head table and the archive's entries: 2026-10-26 00:00 UTC.
EPOCH = 1792972800
# The font's head table counts from 1904.
MAC_EPOCH = EPOCH + 2082844800


def fetch(name):
    with urllib.request.urlopen(BASE + name) as r:
        data = r.read()
    if hashlib.sha256(data).hexdigest() != SOURCES[name]:
        sys.exit("%s is not the file this script was written for" % name)
    return data


def unicodes():
    u = set()
    for lo, hi in [
        (0x0590, 0x05FF),  # Hebrew: letters, nikud, cantillation marks, punctuation
        (0xFB1D, 0xFB4F),  # Hebrew presentation forms
    ]:
        u.update(range(lo, hi + 1))
    u.update([0x20AA, 0x25CC])  # new shekel sign, dotted circle
    return sorted(u)


def main():
    if fontTools.version != FONTTOOLS:
        sys.exit("fontTools %s, not %s: the bytes would differ (pip install fonttools==%s)"
                 % (fontTools.version, FONTTOOLS, FONTTOOLS))
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "bin", "techo5-noto-sans-hebrew.tar.gz")
    font = TTFont(io.BytesIO(fetch("NotoSansHebrew%5Bwdth,wght%5D.ttf")), recalcTimestamp=False)
    license = fetch("OFL.txt")

    static = instancer.instantiateVariableFont(font, {"wght": 400, "wdth": 100}, updateFontNames=True)
    opts = subset.Options()
    opts.layout_features = ["kern", "mark", "mkmk"]
    opts.hinting = False
    opts.notdef_outline = True
    opts.name_IDs = ["*"]
    opts.name_languages = ["*"]
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=unicodes())
    sub.subset(static)
    static["head"].created = MAC_EPOCH
    static["head"].modified = MAC_EPOCH
    static.recalcTimestamp = False
    ttf = io.BytesIO()
    static.save(ttf)
    ttf = ttf.getvalue()

    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.USTAR_FORMAT) as tar:
        for name, data, mode in [("NotoSansHebrew-Regular.ttf", ttf, 0o644), ("OFL.txt", license, 0o644)]:
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), mode, EPOCH
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            tar.addfile(info, io.BytesIO(data))
    os.makedirs(os.path.dirname(os.path.abspath(out)), exist_ok=True)
    with open(out, "wb") as f:
        with gzip.GzipFile(filename="", mode="wb", fileobj=f, mtime=0, compresslevel=9) as gz:
            gz.write(buf.getvalue())
    data = open(out, "rb").read()
    print("font: %d bytes, %d characters" % (len(ttf), len(unicodes())))
    print("built: %s %d bytes, %s" % (out, len(data), hashlib.sha256(data).hexdigest()))


if __name__ == "__main__":
    main()
