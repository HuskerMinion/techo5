# Reverse-engineering helper. Usage (in the vcodec-user-build container):
#   arm-linux-gnueabihf-objdump -d LIB.so > LIB.dis; python3 annot.py LIB.so LIB.dis > LIB.ann
# Adds pc-relative literal values, strings, GOT symbols and PLT-veneer names.
# annotate objdump -d output (thumb) with resolved pc-relative literals/strings/GOT syms
import sys,re,struct
from elftools.elf.elffile import ELFFile
from elftools.elf.relocation import RelocationSection
so,dis=sys.argv[1],sys.argv[2]
f=open(so,'rb'); e=ELFFile(f); data=open(so,'rb').read()
segs=[(s['p_vaddr'],s['p_offset'],s['p_filesz']) for s in e.iter_segments() if s['p_type']=='PT_LOAD']
def rd(addr,n):
    for v,o,sz in segs:
        if v<=addr<v+sz: return data[o+addr-v:o+addr-v+n]
    return None
got={}
dsym=e.get_section_by_name('.dynsym')
for s in e.iter_sections():
    if isinstance(s,RelocationSection):
        for r in s.iter_relocations():
            if r['r_info_sym']: got[r['r_offset']]=dsym.get_symbol(r['r_info_sym']).name
syms={}
for s in dsym.iter_symbols():
    if s['st_value'] and s.name: syms[s['st_value']&~1]=s.name
def desc(a):
    if a in got: return 'GOT['+got[a]+']'
    if a in syms: return '&'+syms[a]
    b=rd(a,200)
    if b:
        z=b.split(b'\0')[0]
        if len(z)>=3 and all(32<=c<127 or c in (9,10) for c in z): return repr(z.decode())
    return None
lit={}
plt={}
for line in open(dis):
    m=re.match(r'^([0-9a-f]+) <(.+)@plt>:',line)
    if m: plt[int(m.group(1),16)]=m.group(2)
def veneer(x):
    b=rd(x,8)
    if b and b[:4]==b'\x78\x47\xc0\x46':
        w=struct.unpack('<I',b[4:8])[0]
        if w==0xe59fc000:
            o=struct.unpack('<i',rd(x+12,4))[0]; t=(o+x+16)&0xffffffff
            return plt.get(t, syms.get(t&~1, hex(t)))
        if (w>>24)==0xea:
            off=w&0xffffff
            if off&0x800000: off-=0x1000000
            t=x+4+8+off*4
            return plt.get(t, hex(t))
    if x in syms: return syms[x]
    return None
pat_bl=re.compile(r'^\s*([0-9a-f]+):\s+(bl|blx|b\.w|b)\s+([0-9a-f]+) <')
pat_ldr=re.compile(r'^\s*([0-9a-f]+):\s+ldr(?:\.w)?\s+(r\d+|ip|lr|sl|fp), \[pc, #-?\d+\].*@ \(([0-9a-f]+)')
pat_add=re.compile(r'^\s*([0-9a-f]+):\s+add\s+(r\d+|ip|lr|sl|fp), pc')
for line in open(dis):
    line=line.rstrip('\n'); m=pat_ldr.match(line)
    if m:
        w=rd(int(m.group(3),16),4)
        if w:
            v=struct.unpack('<I',w)[0]; lit[m.group(2)]=v
            d=desc(v); line+=f'   ; ={v:#x}'+(' '+d if d else '')
    elif pat_bl.match(line):
        m=pat_bl.match(line); v=veneer(int(m.group(3),16))
        if v: line=re.sub(r'<.*>', '<'+v+'>', line)
    else:
        m=pat_add.match(line)
        if m and m.group(2) in lit:
            a=(lit.pop(m.group(2))+int(m.group(1),16)+4)&0xffffffff
            d=desc(a); line+=f'   ; ->{a:#x}'+(' '+d if d else '')
    print(line)
