"""Count how much of the screen's text each language translates, and write the table in docs/translating.md.

    python3 tools/locale/status.py           rewrite the table between the status markers
    python3 tools/locale/status.py --check   fail if the table is not what the catalogs say (CI)

The counts come from echod/internal/lib/locale/text.go: every key in uiEnglish, and how many of them each
language in uiTranslations gives a non-empty value. Who translated a language and where it was tried on
a device are kept by hand in status.json beside this script.
"""
import json
import os
import re
import sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..'))
TEXT = os.path.join(ROOT, 'echod', 'internal', 'lib', 'locale', 'text.go')
DOC = os.path.join(ROOT, 'docs', 'translating.md')
HAND = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'status.json')
BEGIN, END = '<!-- status:begin -->', '<!-- status:end -->'
ENTRY = re.compile(r'^\s*"([^"]+)":\s*"((?:[^"\\]|\\.)*)",\s*$')


def catalogs(src):
    """uiEnglish's keys, and each translation's keys with a value, read line by line."""
    english, langs, cur, in_tr = set(), {}, None, False
    for line in src.splitlines():
        if line.startswith('var uiEnglish'):
            cur = english
            continue
        if line.startswith('var uiTranslations'):
            in_tr, cur = True, None
            continue
        m = re.match(r'^\t"([a-z]{2})": \{$', line)
        if in_tr and m:
            cur = langs.setdefault(m.group(1), set())
            continue
        if line in ('}', '\t},'):
            if line == '}':
                in_tr = in_tr and cur is not None
            cur = None
            continue
        e = ENTRY.match(line)
        if cur is not None and e and e.group(2) != '':
            cur.add(e.group(1))
    return english, langs


def table():
    english, langs = catalogs(open(TEXT, encoding='utf-8').read())
    hand = json.load(open(HAND, encoding='utf-8'))['languages']
    rows = ['| Language | Strings | Coverage | Translated by | Tried on a device |', '|---|---:|---:|---|---|',
            f'| English | {len(english)} | source | | |']
    for code in sorted(langs, key=lambda c: hand.get(c, {}).get('name', c)):
        have = len(langs[code] & english)
        h = hand.get(code, {})
        pct = 100 * have // len(english) if english else 0
        rows.append(f'| {h.get("name", code)} ({code}) | {have} | {pct}% | {h.get("by", "")} | {h.get("device", "Not yet")} |')
    return '\n'.join(rows)


def main():
    doc = open(DOC, encoding='utf-8').read().replace('\r\n', '\n')
    a, b = doc.index(BEGIN) + len(BEGIN), doc.index(END)
    want = doc[:a] + '\n' + table() + '\n' + doc[b:]
    if '--check' in sys.argv:
        if want != doc:
            print('docs/translating.md is out of date: run python3 tools/locale/status.py', file=sys.stderr)
            sys.exit(1)
        return
    open(DOC, 'w', encoding='utf-8', newline='\n').write(want)


if __name__ == '__main__':
    main()
