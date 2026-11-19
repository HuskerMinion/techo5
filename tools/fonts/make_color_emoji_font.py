#!/usr/bin/env python3
"""Makes the color emoji font a device with a screen downloads the first time it has an emoji to draw
(#639): one file, techo5-fluent-emoji.tar.gz, with FluentEmojiFlat.ttf and its license, LICENSE.

The black-and-white Noto Emoji (make_emoji_font.py) draws every emoji in the text's own color. This is
Microsoft's Fluent Emoji (MIT license), the "Flat" artwork, at the commit and SHA-256 below, made into
a COLR version 0 font: each emoji is a stack of glyphs, each filled with one color from the font's
palette (CPAL), which the daemon draws itself (echod/internal/lib/fonts, color.go). The art is
converted, never drawn by hand:

  - picosvg turns each SVG into plain filled paths, and each path is a glyph of its own, its curves
    made quadratic for the glyf table;
  - a path filled with a gradient is filled with the gradient's color halfway along instead, as COLR
    version 0 has solid colors only (3 of the 1,595 SVGs have any, 6 paths in all);
  - one emoji to a character, as the screen draws a character at a time: a sequence (a family, a
    flag, a keycap) is never reached, and an emoji with skin tones is its default, yellow one, since a
    skin tone modifier is drawn as nothing; the characters of plain ASCII are left out;
  - the one change made for TECHO5: an emoji whose ink is mostly very dark (a black heart, a pool 8
    ball, a spade) vanishes on the screen's dark themes, so it gets a thin light gray edge, its
    outline grown a little, as a layer under the art;
  - no hinting, no layout features, and only the names a font needs.

The same tools, the same source and this script give the same bytes, and the archive is written the
same way every time, so the size and SHA-256 feature/cjkfont pins can be checked by anyone:

    pip install fonttools==4.65.0 picosvg==0.23.0 skia-pathops==0.9.2 lxml==6.1.3
    python3 tools/fonts/make_color_emoji_font.py [out.tar.gz] [--cache DIR]

Fetches its source from GitHub (about 3,200 small files, listed by the commit's tree; with --cache,
kept in DIR and taken from there the next time) and checks one SHA-256 over all of them; writes
bin/techo5-fluent-emoji.tar.gz by default.
"""
import concurrent.futures
import gzip
import hashlib
import io
import json
import os
import re
import sys
import tarfile
import urllib.parse
import urllib.request

import fontTools
import lxml
import pathops
import picosvg
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.cu2quPen import Cu2QuPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.colorLib.builder import buildCOLR, buildCPAL
from picosvg import svg_pathops
from picosvg.svg import SVG

VERSIONS = {"fontTools": (fontTools.version, "4.65.0"), "picosvg": (picosvg.__version__, "0.23.0"),
            "skia-pathops": (pathops.__version__, "0.9.2"), "lxml": (lxml.__version__, "6.1.3")}
REPO = "microsoft/fluentui-emoji"
COMMIT = "1ffb34c752ecf5d402f04cfb4b392c77f57c54bc"
RAW = "https://raw.githubusercontent.com/" + REPO + "/" + COMMIT + "/"
TREE = "https://api.github.com/repos/" + REPO + "/git/trees/" + COMMIT + "?recursive=1"
# The SHA-256 of the list of every file used, one "path<TAB>sha256 of the file" line each, in path
# order: what was fetched is what this script was written for, whatever the list came from.
SOURCES = "1069d876690986db66c6adfa7685d033908bdbf8abb0d7910ce020c137357562"
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")

# A fixed time for the font's head table and the archive's entries: 2026-11-19 00:00 UTC.
EPOCH = 1795046400
# The font's head table counts from 1904.
MAC_EPOCH = EPOCH + 2082844800

# The font's design, at 1024 units an em: each emoji is as wide and sits where Noto Emoji's does, so
# the screen lays a line out the same whichever of the two draws it. Fluent's art is drawn on a box
# 32 units square, set here SIZE units square, its left side at LEFT and its top at TOP.
UPEM = 1024
ADVANCE = 1300
SIZE = 1170
LEFT = (ADVANCE - SIZE) // 2
TOP = 935
ASCENT, DESCENT = 950, 250
# How far, in font units, a curve made quadratic may stray from the art's own.
CURVE_ERROR = 1.0

# The light edge: drawn when more than DARK_SHARE of an emoji's ink, as seen, is darker than DARK
# (its luma, 0 black to 1 white), in EDGE_COLOR, the art's outline grown by EDGE units of its 32.
DARK = 0.26
DARK_SHARE = 0.5
EDGE = 1.0
EDGE_COLOR = (0xB0, 0xB0, 0xB0, 0xFF)

# Characters drawn as nothing, with no width: the variation selectors and the zero-width joiner that
# emoji sequences are made with, and which the screen meets a character at a time.
INVISIBLE = [0x200D, 0xFE0E, 0xFE0F]

NAMED = {"white": (255, 255, 255), "black": (0, 0, 0)}


def get(url):
    req = urllib.request.Request(url, headers={"User-Agent": "techo5-font-build"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def chosen(paths):
    """The files used: the license, and for each emoji its metadata and its Flat SVG (the Default one
    for an emoji with skin tones)."""
    out = ["LICENSE"]
    for p in paths:
        parts = p.split("/")
        if parts[0] != "assets" or not p.endswith((".svg", ".json")):
            continue
        if len(parts) == 3 and parts[2] == "metadata.json":
            out.append(p)
        elif len(parts) == 4 and parts[2] == "Flat" or len(parts) == 5 and parts[2:4] == ["Default", "Flat"]:
            out.append(p)
    return sorted(out)


def fetch_all(cache):
    tree = json.loads(get(TREE))
    if tree.get("truncated"):
        sys.exit("the commit's file list came back cut short")
    paths = chosen(e["path"] for e in tree["tree"] if e["type"] == "blob")

    def one(p):
        local = os.path.join(cache, *p.split("/")) if cache else None
        if local and os.path.exists(local):
            with open(local, "rb") as f:
                return p, f.read()
        data = get(RAW + urllib.parse.quote(p))
        if local:
            os.makedirs(os.path.dirname(local), exist_ok=True)
            with open(local, "wb") as f:
                f.write(data)
        return p, data

    with concurrent.futures.ThreadPoolExecutor(16) as pool:
        files = dict(pool.map(one, paths))
    listing = "".join("%s\t%s\n" % (p, hashlib.sha256(files[p]).hexdigest()) for p in paths)
    digest = hashlib.sha256(listing.encode()).hexdigest()
    if digest != SOURCES:
        sys.exit("the files fetched are not the ones this script was written for (%s)" % digest)
    return files


def character(meta):
    """The one character an emoji's metadata names, without its emoji presentation selector, or None
    for a sequence or plain ASCII."""
    points = [int(u, 16) for u in meta["unicode"].split() if u.lower() != "fe0f"]
    if len(points) != 1 or points[0] < 0x80:
        return None
    return points[0]


def parse_color(s):
    """An SVG color as (r, g, b), 0 to 255."""
    s = s.strip().lower()
    if s in NAMED:
        return NAMED[s]
    m = re.fullmatch(r"#([0-9a-f]{3}|[0-9a-f]{6})", s)
    if not m:
        sys.exit("a color this script does not know: %r" % s)
    h = m.group(1)
    if len(h) == 3:
        h = "".join(c * 2 for c in h)
    return tuple(int(h[i:i + 2], 16) for i in (0, 2, 4))


def middle(gradient):
    """The color halfway along a gradient, (r, g, b, opacity): its stops mixed at offset one half."""
    stops = []
    for stop in gradient:
        if not isinstance(stop.tag, str) or not stop.tag.endswith("stop"):
            continue
        off = stop.get("offset", "0")
        off = float(off[:-1]) / 100 if off.endswith("%") else float(off)
        rgb = parse_color(stop.get("stop-color", "black"))
        stops.append((off, rgb + (float(stop.get("stop-opacity", "1")),)))
    if not stops:
        sys.exit("a gradient with no stops")
    stops.sort(key=lambda s: s[0])
    if stops[0][0] >= 0.5:
        return stops[0][1]
    for (o0, c0), (o1, c1) in zip(stops, stops[1:]):
        if o0 <= 0.5 <= o1:
            t = 0 if o1 == o0 else (0.5 - o0) / (o1 - o0)
            return tuple(a + (b - a) * t for a, b in zip(c0, c1))
    return stops[-1][1]


def luma(c):
    return (0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]) / 255


def layers_of(text):
    """An emoji's art as layers, bottom first: each a path in the art's own units (pathops) and its
    color (r, g, b, a), 0 to 255; and how many of its paths were filled with a gradient."""
    svg = SVG.fromstring(text).topicosvg()
    gradients = {g.get("id"): g for g in svg.xpath("//svg:linearGradient | //svg:radialGradient")}
    vb = svg.view_box()
    scale = 32 / max(vb.w, vb.h)
    dx, dy = (32 - vb.w * scale) / 2 - vb.x * scale, (32 - vb.h * scale) / 2 - vb.y * scale
    out, graded = [], 0
    for shape in svg.shapes():
        fill = shape.fill.strip()
        alpha = shape.fill_opacity * shape.opacity
        m = re.fullmatch(r"url\(#(.+)\)", fill)
        if m:
            r, g, b, a = middle(gradients[m.group(1)])
            rgb, alpha = (round(r), round(g), round(b)), alpha * a
            graded += 1
        elif fill == "none":
            continue
        else:
            rgb = parse_color(fill)
        a = round(alpha * 255)
        if a == 0:
            continue
        path = svg_pathops.skia_path(shape.as_cmd_seq(), shape.fill_rule)
        path.transform(scale, 0, 0, scale, dx, dy)
        out.append((path, rgb + (a,)))
    return out, graded


def op(a, b, how):
    """pathops' op, tried again with each path's overlaps taken out first should Skia give up on the
    paths as they are, which it does for a few."""
    try:
        return pathops.op(a, b, how)
    except pathops.PathOpsError:
        return pathops.op(pathops.simplify(a), pathops.simplify(b), how)


def dark_share(layers):
    """The share of an emoji's ink, as seen with each layer over those under it, darker than DARK; and
    all of its ink as one path."""
    covered = pathops.Path()
    dark = total = 0.0
    for path, color in reversed(layers):
        seen = abs(op(path, covered, pathops.PathOp.DIFFERENCE).area) * color[3] / 255
        total += seen
        if luma(color) < DARK:
            dark += seen
        covered = op(covered, path, pathops.PathOp.UNION)
    return (dark / total if total else 0.0), covered


def edge_of(ink):
    """The light edge under an emoji: the outline of all its ink grown by EDGE."""
    stroke = pathops.Path()
    ink.draw(stroke.getPen())
    stroke.stroke(2 * EDGE, pathops.LineCap.ROUND_CAP, pathops.LineJoin.ROUND_JOIN, 4)
    stroke.convertConicsToQuads()
    return op(ink, stroke, pathops.PathOp.UNION)


def glyph_of(path):
    """A path in the art's units as a TrueType glyph, in its place in the em."""
    pen = TTGlyphPen(None)
    k = SIZE / 32
    path.draw(TransformPen(Cu2QuPen(pen, CURVE_ERROR, reverse_direction=True), (k, 0, 0, -k, LEFT, TOP)))
    return pen.glyph()


def build(files):
    """The font, and what to say of it."""
    emoji = {}
    for p in sorted(files):
        if not p.endswith("/metadata.json"):
            continue
        name = p[: -len("metadata.json")]
        c = character(json.loads(files[p]))
        svgs = [q for q in files if q.startswith(name) and q.endswith(".svg")]
        if c is None or not svgs:
            continue
        if c in emoji:
            sys.exit("two emoji for U+%04X" % c)
        if len(svgs) != 1:
            sys.exit("%s: %d Flat SVGs" % (name, len(svgs)))
        emoji[c] = files[svgs[0]].decode("utf-8")

    order = [".notdef", "nothing"]
    glyphs = {".notdef": TTGlyphPen(None).glyph(), "nothing": TTGlyphPen(None).glyph()}
    metrics = {".notdef": (ADVANCE, 0), "nothing": (0, 0)}
    cmap = {c: "nothing" for c in INVISIBLE}
    colr, palette, shared = {}, {}, {}
    graded = graded_emoji = 0
    edged = []

    def layer(path):
        g = glyph_of(path)
        key = (tuple(g.coordinates), tuple(g.endPtsOfContours), bytes(g.flags))
        if key not in shared:
            name = "l%d" % len(shared)
            shared[key] = name
            glyphs[name] = g
        return shared[key]

    def color(c):
        if c not in palette:
            palette[c] = len(palette)
        return palette[c]

    for c in sorted(emoji):
        art, n = layers_of(emoji[c])
        if not art:
            continue
        graded += n
        graded_emoji += n > 0
        share, ink = dark_share(art)
        if share > DARK_SHARE:
            art.insert(0, (edge_of(ink), EDGE_COLOR))
            edged.append("U+%04X" % c)
        base = "u%04X" % c
        glyphs[base] = TTGlyphPen(None).glyph()
        metrics[base] = (ADVANCE, 0)
        cmap[c] = base
        colr[base] = [(layer(path), color(col)) for path, col in art]
        order.append(base)
    layers = sorted(shared.values(), key=lambda n: int(n[1:]))
    order += layers

    fb = FontBuilder(UPEM, isTTF=True)
    fb.font.recalcTimestamp = False
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap(cmap)
    fb.setupGlyf(glyphs)
    glyf = fb.font["glyf"]
    for name in layers:
        metrics[name] = (ADVANCE, getattr(glyf[name], "xMin", 0))
    fb.setupHorizontalMetrics(metrics)
    fb.setupHorizontalHeader(ascent=ASCENT, descent=-DESCENT)
    fb.setupNameTable({"familyName": "TECHO5 Fluent Emoji", "styleName": "Regular",
                       "copyright": "Fluent Emoji: Copyright (c) Microsoft Corporation. MIT License."},
                      mac=False)
    fb.setupOS2(sTypoAscender=ASCENT, sTypoDescender=-DESCENT, sTypoLineGap=0,
                usWinAscent=ASCENT, usWinDescent=DESCENT, fsType=0)
    fb.setupPost(keepGlyphNames=False)
    fb.setupHead(unitsPerEm=UPEM, created=MAC_EPOCH, modified=MAC_EPOCH)
    fb.font["COLR"] = buildCOLR(colr, version=0)
    colors = sorted(palette, key=palette.get)
    fb.font["CPAL"] = buildCPAL([[tuple(v / 255 for v in c) for c in colors]])
    report = [
        "emoji: %d, from %d layer glyphs in %d colors" % (len(colr), len(shared), len(colors)),
        "gradients: %d paths in %d emoji, each filled with its middle color" % (graded, graded_emoji),
        "light edge: %d emoji: %s" % (len(edged), " ".join(edged)),
    ]
    return fb.font, report


def main():
    for name, (have, want) in VERSIONS.items():
        if have != want:
            sys.exit("%s %s, not %s: the bytes would differ (pip install %s==%s)" % (name, have, want, name, want))
    args = sys.argv[1:]
    cache = None
    if "--cache" in args:
        i = args.index("--cache")
        cache = args[i + 1]
        del args[i:i + 2]
    out = args[0] if args else os.path.join(ROOT, "bin", "techo5-fluent-emoji.tar.gz")
    files = fetch_all(cache)
    license = files.pop("LICENSE")
    font, report = build(files)
    ttf = io.BytesIO()
    font.save(ttf)
    ttf = ttf.getvalue()

    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.USTAR_FORMAT) as tar:
        for name, data in [("FluentEmojiFlat.ttf", ttf), ("LICENSE", license)]:
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), 0o644, EPOCH
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            tar.addfile(info, io.BytesIO(data))
    os.makedirs(os.path.dirname(os.path.abspath(out)), exist_ok=True)
    with open(out, "wb") as f:
        with gzip.GzipFile(filename="", mode="wb", fileobj=f, mtime=0, compresslevel=9) as gz:
            gz.write(buf.getvalue())
    data = open(out, "rb").read()
    for line in report:
        print(line)
    print("font: %d bytes, %d glyphs" % (len(ttf), len(font.getGlyphOrder())))
    print("built: %s %d bytes, %s" % (out, len(data), hashlib.sha256(data).hexdigest()))


if __name__ == "__main__":
    main()
