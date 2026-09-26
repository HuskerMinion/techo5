#!/usr/bin/env python3
"""Makes echod/internal/feature/home/places.tsv.gz, the rain map's town names.

From GeoNames' cities5000 list (https://download.geonames.org/export/dump/cities5000.zip, CC BY 4.0):
places of 15,000 people or more everywhere, and of 5,000 or more in the U.S. and Canada, where the
country between cities is emptier. Each is kept as its name, latitude and longitude in thousandths of a
degree, and population, largest first, one tab-separated line each, gzipped.

A name the screen's font (Go Regular) cannot draw is given in its Latin spelling, GeoNames' asciiname:
the name is composed first (a letter and a separate accent become one character), and anything outside
Latin (to Latin Extended-A), Greek or Cyrillic counts as not drawable. home/radar_places_test.go checks
every name against the font itself.

    python3 tools/places/make_places.py [cities5000.zip]
"""
import gzip
import io
import os
import sys
import unicodedata
import urllib.request
import zipfile

URL = "https://download.geonames.org/export/dump/cities5000.zip"
OUT = os.path.join(os.path.dirname(__file__), "..", "..", "echod", "internal", "feature", "home", "places.tsv.gz")


def drawable(ch):
    o = ord(ch)
    return (0x20 <= o < 0x7F or 0xA0 <= o < 0x180  # Latin, Latin-1, Latin Extended-A
            or 0x384 <= o < 0x3D0                  # Greek
            or 0x400 <= o < 0x460)                 # Cyrillic


def main():
    if len(sys.argv) > 1:
        data = open(sys.argv[1], "rb").read()
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
