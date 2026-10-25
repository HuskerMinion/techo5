#!/usr/bin/env python3
"""Makes the screen's font choices, echod/internal/lib/fonts/files/<name>/Regular.ttf and Bold.ttf.

Each comes from the font's own files in google/fonts (https://github.com/google/fonts, ofl/<name>),
all under the SIL Open Font License 1.1, whose OFL.txt is copied beside them. Two changes are made:

  - A variable font is cut to two static instances, weight 400 and 700, with its other axes at their
    defaults (width 100, upright, no rounding; optical size 14 for Inter and 18 for Google Sans Flex,
    their text sizes). The daemon's font package (golang.org/x/image/font/opentype) does not apply
    a variable font's axes, so it can only draw a static one.
  - Each is cut down to the characters the screen draws: Latin (with Latin-1, Extended-A and -B),
    punctuation, currency, arrows, math, shapes and a few symbols. Anything else falls back to Go,
    which the daemon draws with when a font has no glyph for a character (lib/fonts). Only the
    'kern' layout feature is kept (the daemon applies GPOS kerning and nothing else) and the hinting
    instructions are dropped (the daemon does not run them).

Needs fontTools (pip install fonttools). Give it the folder that holds google/fonts' ofl/ folders,
each one as downloaded (inter/, exo/, ...):

    python3 tools/fonts/make_fonts.py path/to/ofl [name ...]

Names after the folder make only those (antonio, inter, ...); with none, every one.
"""
import os
import shutil
import sys

from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "echod", "internal", "lib", "fonts", "files")

# name (the folder in google/fonts and here), its files there, and the axes other than weight to pin.
# A static family gives its two files; a variable one gives one file for both.
FONTS = [
    ("inter", ["Inter[opsz,wght].ttf"], {"opsz": 14}),
    ("museomoderno", ["MuseoModerno[wght].ttf"], {}),
    ("exo", ["Exo[wght].ttf"], {}),
    ("overpass", ["Overpass[wght].ttf"], {}),
    ("saira", ["Saira[wdth,wght].ttf"], {"wdth": 100}),
    ("comicneue", ["ComicNeue-Regular.ttf", "ComicNeue-Bold.ttf"], {}),
    ("opensans", ["OpenSans[wdth,wght].ttf"], {"wdth": 100}),
    ("googlesansflex", ["GoogleSansFlex[ROND,opsz,slnt,wdth,wght].ttf"], {"opsz": 18, "wdth": 100, "ROND": 0, "slnt": 0}),
    ("antonio", ["Antonio[wght].ttf"], {}),
]

# What each is called, as its own name table says it.
NAMES = {"inter": "Inter", "museomoderno": "MuseoModerno", "exo": "Exo", "overpass": "Overpass", "saira": "Saira",
         "comicneue": "Comic Neue", "opensans": "Open Sans", "googlesansflex": "Google Sans Flex",
         "antonio": "Antonio"}

# The characters kept, as ranges of code points (both ends included). The font keeps those it has.
KEEP = [
    (0x0020, 0x007E),  # Basic Latin
    (0x00A0, 0x024F),  # Latin-1 Supplement, Latin Extended-A and -B
    (0x02C6, 0x02DD),  # spacing accents (ˆ ˇ ˘ ˙ ˚ ˛ ˜ ˝)
    (0x03C0, 0x03C0),  # π
    (0x1E9E, 0x1E9E),  # ẞ
    (0x2000, 0x206F),  # General Punctuation
    (0x20A0, 0x20CF),  # currency
    (0x2100, 0x214F),  # letterlike symbols (℃ № ™)
    (0x2190, 0x21FF),  # arrows
    (0x2200, 0x22FF),  # math (− ≤ ≥)
    (0x25A0, 0x25FF),  # shapes (■ ▲ ▼ ○ ●)
    (0x2600, 0x26FF),  # symbols (♪)
    (0x2713, 0x2717),  # check marks and crosses
]

# Files carried beside a font besides OFL.txt.
EXTRA = {"googlesansflex": ["TRADEMARKS.md"]}


def unicodes():
    out = []
    for a, b in KEEP:
        out.extend(range(a, b + 1))
    return out


def instance(path, weight, pins):
    f = TTFont(path)
    if "fvar" not in f:
        return f
    axes = {a.axisTag: a.defaultValue for a in f["fvar"].axes}
    axes.update(pins)
    axes["wght"] = weight
    return instancer.instantiateVariableFont(f, axes, updateFontNames=False)


def cut(f, out):
    opts = subset.Options()
    opts.layout_features = ["kern"]
    opts.hinting = False
    opts.notdef_outline = True
    opts.name_IDs = ["*"]  # the copyright, the license and its address among them
    opts.name_languages = ["*"]
    opts.drop_tables += ["STAT", "DSIG", "meta"]
    s = subset.Subsetter(opts)
    s.populate(unicodes=unicodes())
    s.subset(f)
    f.save(out)


def rename(f, family, style):
    """Names a cut for what it is now, one weight, rather than the variable font's default instance."""
    name = f["name"]
    full = family + " " + style
    for nid in (16, 17, 25):
        name.removeNames(nameID=nid)
    for nid, value in ((1, family), (2, style), (3, full + ";techo5"), (4, full), (6, (family + "-" + style).replace(" ", ""))):
        name.setName(value, nid, 3, 1, 0x409)
        name.removeNames(nameID=nid, platformID=1)
    f["OS/2"].usWeightClass = 700 if style == "Bold" else 400


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    src, only = sys.argv[1], set(sys.argv[2:])
    for name, files, pins in FONTS:
        if only and name not in only:
            continue
        where = os.path.join(src, name)
        dst = os.path.join(OUT, name)
        os.makedirs(dst, exist_ok=True)
        for weight, label in ((400, "Regular"), (700, "Bold")):
            path = os.path.join(where, files[0] if len(files) == 1 else files[weight == 700])
            out = os.path.join(dst, label + ".ttf")
            f = instance(path, weight, pins)
            rename(f, NAMES[name], label)
            cut(f, out)
            print("%-16s %-8s %7d bytes" % (name, label, os.path.getsize(out)))
        for extra in ["OFL.txt"] + EXTRA.get(name, []):
            shutil.copyfile(os.path.join(where, extra), os.path.join(dst, extra))


if __name__ == "__main__":
    main()
