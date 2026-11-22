#!/usr/bin/env python3
"""Makes echod/internal/feature/home/meteoalarm_regions.bin.gz, MeteoAlarm's warning regions.

From MeteoAlarm's own geocode files (https://gitlab.com/meteoalarm-pm-group/documents):
MeteoAlarm_Geocodes_<date>.json, a GeoJSON of every region a warning names by its EMMA_ID, and
MeteoAlarm_Geocodes-Aliases_<date>.csv, the other codes some countries' warnings name the same
regions by (France's NUTS3, Germany's WARNCELLID...). MeteoAlarm's data is licensed under terms
equivalent to CC BY 4.0.

Each region is kept as its EMMA_ID, country, name, aliases, and outline: the outer ring of each of its
polygons, thinned to about a kilometer between points (enough to tell which region a home is in), in
ten-thousandths of a degree as zigzag varint deltas. The whole is gzipped.

Format, every number an unsigned varint and every string its length then its UTF-8 bytes:
    "MAR1", region count, then per region: code, country, name, alias count, (type, value) per
    alias, ring count, and per ring its point count and (longitude, latitude) deltas from the
    point before (the first from 0, 0), zigzag-encoded.

    python3 tools/meteoalarm/make_regions.py [geocodes.json aliases.csv]

Without arguments it downloads the newest pair from MeteoAlarm's GitLab.
"""
import csv
import gzip
import io
import json
import os
import re
import sys
import urllib.request

PROJECT = "https://gitlab.com/api/v4/projects/meteoalarm-pm-group%2Fdocuments/repository/tree?per_page=100"
RAW = "https://gitlab.com/meteoalarm-pm-group/documents/-/raw/master/"
OUT = os.path.join(os.path.dirname(__file__), "..", "..", "echod", "internal", "feature", "home",
                   "meteoalarm_regions.bin.gz")

STEP = 0.01  # degrees between kept points, about a kilometer
SCALE = 10000  # coordinates kept in ten-thousandths of a degree

# Names the geocode file has garbled, by code.
NAMES = {
    "ES072": "Poniente y Almería Capital",
    "ES133": "Litoral cántabro",
    "ES842": "Costa - Litoral cántabro",
    "ES857": "Costa - Poniente y Almería Capital",
}


def newest():
    """The newest geocode and alias files' names in MeteoAlarm's documents project."""
    with urllib.request.urlopen(PROJECT, timeout=60) as r:
        files = [f["path"] for f in json.load(r) if f["type"] == "blob"]
    geo = sorted(f for f in files if re.fullmatch(r"MeteoAlarm_Geocodes_\d{4}_\d\d_\d\d\.json", f))
    ali = sorted(f for f in files if re.fullmatch(r"MeteoAlarm_Geocodes-Aliases_\d{4}_\d\d_\d\d\.csv", f))
    if not geo or not ali:
        sys.exit("no geocode or alias file in MeteoAlarm's documents project")
    return geo[-1], ali[-1]


def fetch(name):
    print("downloading", name, file=sys.stderr)
    with urllib.request.urlopen(RAW + name, timeout=600) as r:
        return r.read()


def varint(out, n):
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out.append(b | 0x80)
        else:
            out.append(b)
            return


def zigzag(n):
    return n * 2 if n >= 0 else -n * 2 - 1


def string(out, s):
    b = s.encode("utf-8")
    varint(out, len(b))
    out += b


def thin(ring):
    out = [ring[0]]
    for p in ring[1:]:
        if abs(p[0] - out[-1][0]) >= STEP or abs(p[1] - out[-1][1]) >= STEP:
            out.append(p)
    if out[-1] != ring[-1]:
        out.append(ring[-1])
    return out


def outer_rings(geometry):
    if geometry["type"] == "Polygon":
        return [geometry["coordinates"][0]]
    if geometry["type"] == "MultiPolygon":
        return [p[0] for p in geometry["coordinates"] if p]
    return []


def main():
    args = sys.argv[1:]
    if len(args) == 2:
        geo, ali = open(args[0], "rb").read(), open(args[1], "rb").read()
    elif not args:
        g, a = newest()
        geo, ali = fetch(g), fetch(a)
    else:
        sys.exit(__doc__)

    aliases = {}
    for row in csv.DictReader(io.StringIO(ali.decode("utf-8-sig"))):
        aliases.setdefault(row["CODE"].strip(), []).append((row["ALIAS_TYPE"].strip(), row["ALIAS_CODE"].strip()))

    regions = []
    for f in json.loads(geo)["features"]:
        p = f["properties"]
        if p.get("type", "EMMA_ID") != "EMMA_ID" or not f.get("geometry"):
            continue
        rings = []
        for r in outer_rings(f["geometry"]):
            r = thin(r)
            if len(r) >= 4:
                rings.append(r)
        if not rings:
            continue
        code = p["code"].strip()
        regions.append((code, p["country"].strip(), NAMES.get(code, p["name"].strip()),
                        sorted(set(aliases.get(code, []))), rings))
    regions.sort(key=lambda r: r[0])

    out = bytearray(b"MAR1")
    varint(out, len(regions))
    points = 0
    for code, country, name, al, rings in regions:
        string(out, code)
        string(out, country)
        string(out, name)
        varint(out, len(al))
        for t, v in al:
            string(out, t)
            string(out, v)
        varint(out, len(rings))
        for r in rings:
            varint(out, len(r))
            px = py = 0
            for lon, lat in r:
                x, y = round(lon * SCALE), round(lat * SCALE)
                varint(out, zigzag(x - px))
                varint(out, zigzag(y - py))
                px, py = x, y
                points += 1
    with open(OUT, "wb") as w:
        # mtime 0: the same input makes the same file.
        with gzip.GzipFile(fileobj=w, mode="wb", compresslevel=9, mtime=0) as z:
            z.write(bytes(out))
    print(f"{len(regions)} regions, {points} points, {os.path.getsize(OUT)} bytes", file=sys.stderr)


if __name__ == "__main__":
    main()
