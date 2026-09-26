#!/usr/bin/env python3
"""Makes echod/internal/feature/home/places.tsv.gz, the rain map's town names.

From GeoNames' cities5000 list (https://download.geonames.org/export/dump/cities5000.zip, CC BY 4.0):
places of 15,000 people or more everywhere, and of 5,000 or more in the U.S. and Canada, where the
country between cities is emptier. Each is kept as its name, latitude and longitude in thousandths of a
degree, and population, largest first, one tab-separated line each, gzipped.

A name the screen's font (Go Regular) cannot draw is given in its Latin spelling, GeoNames' asciiname:
the name is composed first (a letter and a separate accent become one character), and then each of its
characters is looked up in the font's own character map. home/radar_places_test.go checks every name
against the font too.

Needs fontTools (pip install fonttools) and Go-Regular.ttf, found in Go's module cache
(golang.org/x/image/font/gofont/ttfs) or given with --font.

    python3 tools/places/make_places.py [--font Go-Regular.ttf] [cities5000.zip]
"""
import glob
import gzip
import io
import os
import subprocess
import sys
import unicodedata
import urllib.request
import zipfile

from fontTools.ttLib import TTFont

URL = "https://download.geonames.org/export/dump/cities5000.zip"
OUT = os.path.join(os.path.dirname(__file__), "..", "..", "echod", "internal", "feature", "home", "places.tsv.gz")


def go_regular():
    """The path of Go-Regular.ttf in Go's module cache, the newest x/image there."""
    cache = subprocess.run(["go", "env", "GOMODCACHE"], capture_output=True, text=True, check=True).stdout.strip()
    found = sorted(glob.glob(os.path.join(cache, "golang.org", "x", "image@*", "font", "gofont", "ttfs", "Go-Regular.ttf")))
    if not found:
        sys.exit("Go-Regular.ttf not in Go's module cache: run 'go mod download golang.org/x/image' in echod, or pass --font")
    return found[-1]


def main():
    args = sys.argv[1:]
    font = None
    if args[:1] == ["--font"]:
        font, args = args[1], args[2:]
    glyphs = set(TTFont(font or go_regular()).getBestCmap())

    def drawable(ch):
        return ord(ch) in glyphs

    if args:
        data = open(args[0], "rb").read()
    else:
        req = urllib.request.Request(URL, headers={"User-Agent": "TECHO5 (https://github.com/HuskerMinion/techo5)"})
        data = urllib.request.urlopen(req, timeout=120).read()
    text = zipfile.ZipFile(io.BytesIO(data)).read("cities5000.txt").decode("utf-8")
    rows, latin = [], 0
    for line in text.splitlines():
        f = line.split("\t")
        name, ascii_name, cc = f[1], f[2], f[8]
        pop = int(f[14] or 0)
        if pop < 15000 and cc not in ("US", "CA"):
            continue
        name = unicodedata.normalize("NFC", name)
        if not all(drawable(c) for c in name):
            name, latin = ascii_name, latin + 1
        if not name:
            continue
        rows.append((pop, f"{name}\t{round(float(f[4]) * 1000)}\t{round(float(f[5]) * 1000)}\t{pop}"))
    rows.sort(key=lambda r: -r[0])
    body = ("\n".join(r[1] for r in rows) + "\n").encode("utf-8")
    # mtime=0: the same list gives the same file, so a rebuild changes nothing.
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode="wb", compresslevel=9, mtime=0) as g:
        g.write(body)
    with open(OUT, "wb") as f:
        f.write(buf.getvalue())
    print(f"{len(rows)} places, {latin} in Latin spelling, {len(buf.getvalue())} bytes -> {os.path.normpath(OUT)}")


if __name__ == "__main__":
    main()
