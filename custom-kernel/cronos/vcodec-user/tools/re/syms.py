# List undefined symbols of LIBS (in DIR) and which other lib in DIR defines them.
#   python3 syms.py DIR libfoo.so [...]
import sys,os
from elftools.elf.elffile import ELFFile
from elftools.elf.gnuversions import GNUVerNeedSection
d=sys.argv[1]; libs=sys.argv[2:]
def info(p):
    e=ELFFile(open(p,'rb'))
    ds=e.get_section_by_name('.dynsym')
    und=[];defs=set()
    # version needed mapping
    vers={}
    vs=e.get_section_by_name('.gnu.version'); vn=e.get_section_by_name('.gnu.version_r')
    vmap={}
    if vn:
        for v,aux in vn.iter_versions():
            for a in aux: vmap[a['vna_other']]=(v.name,a.name)
    for i,s in enumerate(ds.iter_symbols()):
        if not s.name: continue
        ver=''
        if vs:
            idx=vs.get_symbol(i)['ndx']
            if isinstance(idx,int) and idx in vmap: ver='@'+vmap[idx][1]+'('+vmap[idx][0]+')'
        if s['st_shndx']=='SHN_UNDEF': und.append((s.name,s['st_info']['bind'],s['st_info']['type'],ver))
        else: defs.add(s.name)
    return und,defs
D={}
for f in os.listdir(d):
    if f.endswith('.so'): D[f]=info(os.path.join(d,f))
for l in libs:
    und,_=D[l]
    print('=====',l)
    for n,b,t,v in sorted(und):
        prov=[k for k in D if n in D[k][1] and k!=l]
        print(f'  {n:50s} {b[4:]:6s} {t[4:]:6s} {v:30s} {",".join(prov)}')
