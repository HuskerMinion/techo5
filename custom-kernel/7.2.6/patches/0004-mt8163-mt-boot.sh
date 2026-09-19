#!/bin/sh
# Install the "mt-boot" enable method (patches/mt8163-boot.c) into the tree. Idempotent.
set -e
K=${KDIR:-/src/linux-7.2.6}/arch/arm64/kernel
cp /patches/mt8163-boot.c $K/mt8163-boot.c
grep -q mt8163-boot $K/Makefile || sed -i 's|^obj-y\t\t\t\t\t+= head.o|obj-$(CONFIG_ARCH_MEDIATEK)\t\t+= mt8163-boot.o\nobj-y\t\t\t\t\t+= head.o|' $K/Makefile
grep -q mt8163-boot $K/Makefile || { echo "no anchor in Makefile"; exit 1; }
grep -q mt8163_boot_ops $K/cpu_ops.c || sed -i 's|^extern const struct cpu_operations cpu_psci_ops;|extern const struct cpu_operations cpu_psci_ops;\n#ifdef CONFIG_ARCH_MEDIATEK\nextern const struct cpu_operations mt8163_boot_ops;\n#endif|; s|^\t&cpu_psci_ops,\n\tNULL,|&|' $K/cpu_ops.c
python3 - <<'PY'
import os
p=os.environ.get('KDIR', '/src/linux-7.2.6')+'/arch/arm64/kernel/cpu_ops.c'; s=open(p).read()
old='''static const struct cpu_operations *const dt_supported_cpu_ops[] __initconst = {
	&smp_spin_table_ops,
	&cpu_psci_ops,
'''
new=old+'''#ifdef CONFIG_ARCH_MEDIATEK
	&mt8163_boot_ops,
#endif
'''
if '&mt8163_boot_ops' not in s:
    assert old in s
    s=s.replace(old,new); open(p,'w').write(s)
PY
grep -n "mt8163" $K/Makefile $K/cpu_ops.c
