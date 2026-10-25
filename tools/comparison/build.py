#!/usr/bin/env python3
"""Builds the project comparison from docs/comparison/data.json.

    python3 tools/comparison/build.py              # writes docs/comparison.md
    python3 tools/comparison/build.py --site _site # also writes the interactive page and the finder for GitHub Pages

The data file is the one place a cell changes; the markdown page and the interactive page are both made
from it. The workflow in .github/workflows/comparison-page.yml publishes only the interactive page.
"""
import argparse
import html
import json
import os
import shutil

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DATA = os.path.join(ROOT, "docs", "comparison", "data.json")
MD = os.path.join(ROOT, "docs", "comparison.md")
TEMPLATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "page.html")
FINDER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "choose.html")
PAGE_URL = "https://huskerminion.github.io/techo5/comparison/"
ISSUES = "https://github.com/HuskerMinion/techo5/issues"

SYMBOL = {"Yes": "✅", "Partly": "🟡", "No": "❌", "Not found": "❔", "In progress": "🚧", "n/a": "—"}
DEVSYMBOL = {"yes": "✅", "lineage": "🔧", "progress": "🚧", "no": "❌", "unknown": "❔"}
ZW = "​"


def soft(word):
    """A break point after / _ . , ; ) and every 12 letters in a long word, so a narrow cell can wrap."""
    if len(word) <= 12:
        return word
    out, run = [], 0
    for ch in word:
        out.append(ch)
        run += 1
        if ch in "/_.,;)" or run >= 12:
            out.append(ZW)
            run = 0
    return "".join(out).rstrip(ZW)


def text(s):
    return " ".join(soft(w) for w in s.split(" ")).replace("|", "\\|")


def checked_words(iso):
    import datetime
    d = datetime.date.fromisoformat(iso)
    return f"{d:%B} {d.day}, {d.year}"


def markdown(d):
    P, R, C = d["projects"], d["rows"], d["cells"]
    feat = [r for r in R if not r["fact"]]
    facts = [r for r in R if r["fact"]]
    first = d["sections"][0]
    o = []
    o.append("![](comparison/banner.jpg)\n")
    o.append("# Comparison\n")
    o.append(f"""People often ask how the projects that bring Echo devices and other screens to Home Assistant
compare. This page lists what each one covers. It isn't a ranking: each project is good work with
its own focus, and the right one depends on your devices and what you want from them.

**[Open the interactive version]({PAGE_URL})**: the header stays put while you scroll, you can filter
by section or project, and each cell shows its note and source. Or **[answer a few questions]({PAGE_URL}choose/)**
about your devices and what you want, and see which projects fit.

This page lives in the TECHO5 repo and is kept by TECHO5's author, so read it with that in mind.
To keep it even, it was built the same way for every project, TECHO5 included:

1. A separate reviewer for each project read only that project's public repos (code, docs,
   releases and pull requests) and listed every feature it found, with a source link.
2. The rows come from all six lists: every feature area that two or more projects share has a
   row, and each project named four of its own central features as rows too.
3. Each project's reviewer answered every row for that project, using the row definitions at the
   bottom of this page, and a second reviewer checked every answer again.

Every cell shows only what each project's own GitHub repos say: its code, docs and releases, and
for devices also what users report in its issues and discussions (the note says when a device
rests on user reports). Reports elsewhere (Reddit, Discord, forums) aren't counted, and nothing
here was tested by me. A ❔ means nothing was found there, not that it can't work.

Checked {checked_words(d["checked"])}. All of these projects move fast, so some cells will be out of date
soon. If you spot something wrong or missing, please [open an issue]({ISSUES}) and it will be fixed.
Projects are in alphabetical order.
""")
    o.append("## The projects\n")
    for p in P:
        o.append(f"**{p['name']}**\n")
        for r in facts:
            a, n, u = C[p["key"]][r["id"]]
            t = a + (f" ({n})" if n else "")
            o.append(f"- {r['name']}: {t}" + (f" ([source]({u}))" if u else ""))
        o.append("")
    o.append("## Devices\n")
    o.append("✅ supported &nbsp; 🔧 after putting another OS on it first &nbsp; 🚧 in progress &nbsp; "
             "❌ the project says it isn't &nbsp; ❔ not found &nbsp; † see the notes. Each symbol links to its source.\n")
    DV, DC = d["devices"]["list"], d["devices"]["cells"]
    o.append("| Device | " + " | ".join(p["name"] for p in P) + " |")
    o.append("|---" + "|:---:" * len(P) + "|")
    dnotes = []
    for dv in DV:
        row = []
        for p in P:
            st_, n, u = DC[p["key"]].get(dv["id"], ["unknown", "", ""])
            sym = DEVSYMBOL[st_]
            row.append((f"[{sym}]({u})" if u else sym) + ("†" if n else ""))
            if n:
                dnotes.append((dv["name"], p["name"], n))
        o.append(f"| {dv['name']} | " + " | ".join(row) + " |")
    o.append("")
    if dnotes:
        o.append("<details>\n<summary>Notes to the devices</summary>\n")
        for dv in DV:
            ns = [f"{pn}: {text(n)}" for dn, pn, n in dnotes if dn == dv["name"]]
            if ns:
                o.append(f"- **{dv['name']}**: " + "; ".join(ns))
        o.append("\n</details>\n")
    o.append("## The table\n")
    o.append("✅ yes &nbsp; 🟡 partly &nbsp; ❌ no (the project says so) &nbsp; ❔ not found &nbsp; "
             "🚧 in progress (not in a release yet) &nbsp; — doesn't apply &nbsp; † see the notes after the table. "
             "Each symbol links to its source.\n")
    o.append(f"| **{first}** | " + " | ".join(p["name"] for p in P) + " |")
    o.append("|---" + "|:---:" * len(P) + "|")
    notes = []
    for s in d["sections"]:
        rs = [r for r in feat if r["section"] == s]
        if not rs:
            continue
        if s != first:
            o.append(f"| **{s}** | " + " | ".join(f"**{p['name']}**" for p in P) + " |")
        sec_notes = []
        for r in rs:
            row = []
            for p in P:
                a, n, u = C[p["key"]][r["id"]]
                sym = SYMBOL.get(a, a)
                row.append((f"[{sym}]({u})" if u else sym) + ("†" if n else ""))
            o.append(f"| {r['name']} | " + " | ".join(row) + " |")
            ns = [f"{p['name']}: {text(C[p['key']][r['id']][1])}" for p in P if C[p["key"]][r["id"]][1]]
            if ns:
                sec_notes.append(f"- **{r['name']}**: " + "; ".join(ns))
        if sec_notes:
            notes.append(f"**{s}**\n\n" + "\n".join(sec_notes) + "\n")
    o.append("")
    o.append("## Notes to the table\n")
    o.extend(notes)
    o.append("## Also does\n")
    o.append("Released features that no row covers, as each project's reviewer picked them.\n")
    for p in P:
        o.append(f"**{p['name']}**\n")
        o.extend(f"- {x}" for x in p["also"])
        o.append("")
    o.append("## What each project focuses on\n")
    o.append("In each project's own terms.\n")
    o.extend(f"- **{p['name']}**: {p['focus']}" for p in P)
    o.append("")
    o.append("## What the rows mean\n")
    o.append("<details>\n<summary>Row definitions</summary>\n")
    for s in d["sections"]:
        o.append(f"**{s}**\n")
        o.extend(f"- **{r['name']}**: {r['def']}" for r in R if r["section"] == s)
        o.append("")
    o.append("</details>\n")
    o.append("## Sources\n")
    for p in P:
        o.append(f"- {p['name']}: " + ", ".join(f"[{n}]({u})" for n, u in p["repos"]))
    return "\n".join(o) + "\n"


def page(d, template=TEMPLATE):
    t = open(template, encoding="utf-8").read()
    data = json.dumps(d, ensure_ascii=False).replace("</", "<\\/")
    return (t.replace("/*DATA*/null", data)
             .replace("{{CHECKED}}", html.escape(checked_words(d["checked"])))
             .replace("{{ISSUES}}", ISSUES)
             .replace("{{PAGE_URL}}", PAGE_URL))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--site", help="also write the GitHub Pages site into this folder")
    a = ap.parse_args()
    d = json.load(open(DATA, encoding="utf-8"))
    open(MD, "w", encoding="utf-8", newline="\n").write(markdown(d))
    if a.site:
        os.makedirs(os.path.join(a.site, "comparison"), exist_ok=True)
        open(os.path.join(a.site, "comparison", "index.html"), "w", encoding="utf-8").write(page(d))
        os.makedirs(os.path.join(a.site, "comparison", "choose"), exist_ok=True)
        open(os.path.join(a.site, "comparison", "choose", "index.html"), "w", encoding="utf-8").write(page(d, FINDER))
        for img in ("banner.jpg", "og.jpg"):
            shutil.copy(os.path.join(os.path.dirname(DATA), img), os.path.join(a.site, "comparison", img))
        open(os.path.join(a.site, "index.html"), "w", encoding="utf-8").write(
            '<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0; url=comparison/">'
            '<link rel="canonical" href="comparison/"><title>TECHO5</title><a href="comparison/">Comparison</a>\n')


if __name__ == "__main__":
    main()
