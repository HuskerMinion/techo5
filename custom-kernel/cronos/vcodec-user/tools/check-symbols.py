#!/usr/bin/env python3
"""Check that every ELF in a staging tree resolves at load time (all vendor
libs are BIND_NOW): each undefined non-weak symbol must be defined, with a
matching version where one is required, by some library in the global load
set (bionic resolves symbols across the whole load group, executable first).
  check-symbols.py <staging> [exe ...]   (exe paths relative to staging)"""
import sys, os
from elftools.elf.elffile import ELFFile
root = sys.argv[1]
paths = ['system/lib', 'vendor/lib']
def find(name):
    for p in paths:
        f = os.path.join(root, p, name)
        if os.path.exists(f): return f
def info(f):
    e = ELFFile(open(f, 'rb'))
    needed = [t.needed for t in e.get_section_by_name('.dynamic').iter_tags() if t.entry.d_tag == 'DT_NEEDED']
    ds = e.get_section_by_name('.dynsym')
    vs = e.get_section_by_name('.gnu.version'); vr = e.get_section_by_name('.gnu.version_r'); vd = e.get_section_by_name('.gnu.version_d')
    need_ver, def_ver = {}, {}
    if vr:
        for v, aux in vr.iter_versions():
            for a in aux: need_ver[a['vna_other']] = a.name
    if vd:
        for v, aux in vd.iter_versions():
            names = [a.name for a in aux]
            def_ver[v['vd_ndx']] = names[0]
    und, defs = [], {}
    for i, s in enumerate(ds.iter_symbols()):
        if not s.name: continue
        idx = vs.get_symbol(i)['ndx'] if vs else 0
        if isinstance(idx, str): idx = 1
        idx &= 0x7fff
        if s['st_shndx'] == 'SHN_UNDEF':
            if s['st_info']['bind'] != 'STB_WEAK': und.append((s.name, need_ver.get(idx)))
        elif s['st_info']['bind'] != 'STB_LOCAL':
            defs.setdefault(s.name, set()).add(def_ver.get(idx))
    return needed, und, defs
exes = sys.argv[2:] or ['vendor/lib/libvcodecdrv.so']
bad = 0
for exe in exes:
    order, seen, q = [], set(), [os.path.join(root, exe)]
    while q:  # BFS like the bionic linker
        f = q.pop(0)
        if f in seen: continue
        seen.add(f); order.append(f)
        n, _, _ = info(f)
        for x in n:
            p = find(x)
            if not p: print(f'MISSING lib {x} (needed by {os.path.basename(f)})'); bad += 1
            else: q.append(p)
    D = {f: info(f) for f in order}
    for f in order:
        for name, ver in D[f][1]:
            ok = any(name in D[g][2] and (ver is None or ver in D[g][2][name] or None in D[g][2][name]) for g in order)
            if not ok:
                print(f'UNRESOLVED {name}{"@"+ver if ver else ""} in {os.path.relpath(f, root)}'); bad += 1
    print(f'{exe}: {len(order)} libs in load group:', ' '.join(os.path.basename(x) for x in order))
print('OK' if not bad else f'{bad} problems'); sys.exit(1 if bad else 0)
