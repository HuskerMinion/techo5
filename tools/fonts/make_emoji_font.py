#!/usr/bin/env python3
"""Makes the emoji font a device with a screen downloads the first time it has an emoji to draw (#639):
one file, techo5-noto-emoji.tar.gz, with NotoEmoji-Regular.ttf and its license, OFL.txt.

The fonts the screen offers have no emoji, so an emoji in a calendar event, a notification or a song
title showed as a box. A character none of them has is drawn from the device's own fonts folder
(echod/internal/lib/fonts, own.go), and the daemon fetches this archive into that folder
(feature/cjkfont, emoji.go), so nothing has to be put there by hand and no image carries it. It is
Noto Emoji, the black-and-white one, from google/fonts (SIL Open Font License 1.1), at the commit and
SHA-256 below, cut down to:

  - one static weight, 400: the daemon's font package does not apply a variable font's axes;
  - the glyphs its characters map to, and no layout features: the screen draws a character at a
    time, so the glyphs only a sequence reaches (a family, a flag, a skin tone on a hand) are never
    drawn, and each part of such a sequence is drawn on its own instead;
  - no hinting, as make_fonts.py does.

The same fontTools, the same source and this script give the same bytes, and the archive is written
the same way every time, so the size and SHA-256 feature/cjkfont pins can be checked by anyone:

    pip install fonttools==4.65.0
    python3 tools/fonts/make_emoji_font.py [out.tar.gz]

Fetches its source from GitHub; writes bin/techo5-noto-emoji.tar.gz by default.
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
COMMIT = "8b0a1d0f5983c89bc2b93f1b5fb55f9e252744b5"
BASE = "https://raw.githubusercontent.com/google/fonts/" + COMMIT + "/ofl/notoemoji/"
SOURCES = {
    "NotoEmoji%5Bwght%5D.ttf": "de6c18832938afc99caf132b39d6a30a19bac7f2e812e28db2535b4608d27551",
    "OFL.txt": "500bb1ccf43df7bbb522112f9133a52b16e1c35e809632f5d8609b179152de5b",
}
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")

# A fixed time for the font's head table and the archive's entries: 2026-11-18 00:00 UTC.
EPOCH = 1794960000
# The font's head table counts from 1904.
MAC_EPOCH = EPOCH + 2082844800


def fetch(name):
    with urllib.request.urlopen(BASE + name) as r:
        data = r.read()
    if hashlib.sha256(data).hexdigest() != SOURCES[name]:
        sys.exit("%s is not the file this script was written for" % name)
    return data


def main():
    if fontTools.version != FONTTOOLS:
        sys.exit("fontTools %s, not %s: the bytes would differ (pip install fonttools==%s)"
                 % (fontTools.version, FONTTOOLS, FONTTOOLS))
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "bin", "techo5-noto-emoji.tar.gz")
    font = TTFont(io.BytesIO(fetch("NotoEmoji%5Bwght%5D.ttf")), recalcTimestamp=False)
    license = fetch("OFL.txt")
    unicodes = sorted(font.getBestCmap())

    static = instancer.instantiateVariableFont(font, {"wght": 400}, updateFontNames=True)
    opts = subset.Options()
    opts.layout_features = []
    opts.hinting = False
    opts.notdef_outline = True
    opts.name_IDs = ["*"]
    opts.name_languages = ["*"]
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=unicodes)
    sub.subset(static)
    static["head"].created = MAC_EPOCH
    static["head"].modified = MAC_EPOCH
    static.recalcTimestamp = False
    ttf = io.BytesIO()
    static.save(ttf)
    ttf = ttf.getvalue()

    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.USTAR_FORMAT) as tar:
        for name, data, mode in [("NotoEmoji-Regular.ttf", ttf, 0o644), ("OFL.txt", license, 0o644)]:
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
    print("font: %d bytes, %d characters" % (len(ttf), len(unicodes)))
    print("built: %s %d bytes, %s" % (out, len(data), hashlib.sha256(data).hexdigest()))


if __name__ == "__main__":
    main()
