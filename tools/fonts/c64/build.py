#!/usr/bin/env python3
"""Builds echod's C64-style pixel font (lib/fonts/files/c64) from glyphs.py.

    python3 tools/fonts/c64/build.py

Each lit pixel is a square 128 units on a side, so a glyph is 1024 units (one em) wide and the font is
monospaced. Caps sit on rows 0 to 6 above the baseline; row 7 is the descender. Each row's runs of lit
pixels are one rectangle each, wound clockwise as TrueType wants, and rows only meet at their edges,
so nothing overlaps. Regular and Bold are the same: the characters are already two pixels thick.
Needs fontTools (pip install fonttools).
"""
import os
import sys

from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from glyphs import G  # noqa: E402

PX = 128
EM = 8 * PX
ASCENT = 7 * PX
DESCENT = PX
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..", "echod", "internal", "lib", "fonts", "files", "c64")


def glyph(rows):
    pen = TTGlyphPen(None)
    for r, row in enumerate(rows):
        top = ASCENT - r * PX
        bottom = top - PX
        c = 0
        while c < 8:
            if row[c] != "#":
                c += 1
                continue
            e = c
            while e < 8 and row[e] == "#":
                e += 1
            x0, x1 = c * PX, e * PX
            pen.moveTo((x0, bottom))
            pen.lineTo((x0, top))
            pen.lineTo((x1, top))
            pen.lineTo((x1, bottom))
            pen.closePath()
            c = e
    return pen.glyph()


def name_of(ch):
    return "space" if ch == " " else f"uni{ord(ch):04X}"


def build(path, style):
    order = [".notdef"] + [name_of(ch) for ch in G]
    fb = FontBuilder(EM, isTTF=True)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap({ord(ch): name_of(ch) for ch in G})
    glyphs = {".notdef": glyph(["########", "#......#", "#......#", "#......#", "#......#", "#......#", "########", "........"])}
    glyphs.update({name_of(ch): glyph(rows) for ch, rows in G.items()})
    fb.setupGlyf(glyphs)
    fb.setupHorizontalMetrics({n: (EM, 0) for n in order})
    fb.setupHorizontalHeader(ascent=ASCENT, descent=-DESCENT)
    fb.setupNameTable({
        "familyName": "TECHO5 C64",
        "styleName": style,
        "copyright": "Copyright 2026 the TECHO5 authors",
        "licenseDescription": "Licensed under the same terms as TECHO5",
    })
    fb.setupOS2(sTypoAscender=ASCENT, sTypoDescender=-DESCENT, usWinAscent=ASCENT, usWinDescent=DESCENT,
                xAvgCharWidth=EM)
    fb.setupPost(isFixedPitch=1)
    fb.save(path)


if __name__ == "__main__":
    os.makedirs(OUT, exist_ok=True)
    for style in ("Regular", "Bold"):
        build(os.path.join(OUT, style + ".ttf"), style)
    print("wrote", os.path.normpath(OUT), len(G), "glyphs")
