/*
 * smoke_test: checks the runtime and the memory path without the codec HW.
 *  1. the bionic linker loads libvcodecdrv.so and its whole dependency set
 *     (this binary has it as NEEDED);
 *  2. eVideoMemAlloc() (libvcodec_utility) -> our libion/libion_mtk shims ->
 *     VCODEC_SHIM_ALLOC: allocates, writes and reads back a few buffers, and
 *     prints VA and device address. Then eVideoFlushCache/eVideoMemFree.
 *  3. optionally (-n) the VAL_MEM_TYPE_FOR_HW_NONCACHEABLE path, which goes
 *     to the kernel's VCODEC_ALLOC_NON_CACHE_BUFFER + mmap(/dev/Vcodec).
 * With VCODEC_SHIM_FAKE=1, step 2 works with no kernel driver at all (memfd
 * buffers, fake IOVAs). That is how the loader is tested under qemu.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "val_types_public.h"
#include "common.h"

/* any libvcodecdrv export, so that the NEEDED entry is kept */
extern int eVDecDrvCreate(void *phHandle, int eVideoFormat);

static int test_alloc(VAL_MEM_TYPE_T type, unsigned size, unsigned align, VAL_MEM_CODEC_T codec)
{
	VAL_MEMORY_T m;
	memset(&m, 0, sizeof(m));
	m.eMemType = type;
	m.u4MemSize = size;
	m.eAlignment = align;
	m.eMemCodec = codec;
	VAL_RESULT_T r = eVideoMemAlloc(&m, sizeof(m));
	if (r != VAL_RESULT_NO_ERROR || !m.pvMemVa) {
		printf("  eVideoMemAlloc(type %d, %u) FAILED: %d\n", type, size, r);
		return 1;
	}
	printf("  type %d size %7u align %4u: va %p alignva %p pa(iova) 0x%08lx alignpa 0x%08lx ionfd %d\n",
	       type, size, align, m.pvMemVa, m.pvAlignMemVa, (unsigned long)m.pvMemPa,
	       (unsigned long)m.pvAlignMemPa, (int)m.i4IonShareFd);
	unsigned char *p = m.pvAlignMemVa;
	for (unsigned i = 0; i < size; i += 4096) p[i] = (unsigned char)(i >> 12);
	p[size - 1] = 0x5a;
	int bad = p[size - 1] != 0x5a;
	for (unsigned i = 0; i < size; i += 4096) bad |= p[i] != (unsigned char)(i >> 12);
	if (type != VAL_MEM_TYPE_FOR_HW_NONCACHEABLE) {
		eVideoFlushCache(&m, sizeof(m), 1);
		eVideoInvalidateCache(&m, sizeof(m), 1);
	}
	r = eVideoMemFree(&m, sizeof(m));
	printf("    readback %s, free %d\n", bad ? "MISMATCH" : "ok", r);
	return bad || r;
}

int main(int argc, char **argv)
{
	int nc = argc > 1 && !strcmp(argv[1], "-n");
	int fails = 0;
	printf("smoke_test: libvcodecdrv loaded (eVDecDrvCreate at %p), pid %d\n",
	       (void *)eVDecDrvCreate, getpid());
	double t0 = now_ms();
	fails += test_alloc(VAL_MEM_TYPE_FOR_HW_CACHEABLE, 4096, 512, VAL_MEM_CODEC_FOR_VDEC);
	fails += test_alloc(VAL_MEM_TYPE_FOR_HW_CACHEABLE, 1280 * 736 * 3 / 2, 512, VAL_MEM_CODEC_FOR_VDEC);
	fails += test_alloc(VAL_MEM_TYPE_FOR_HW_CACHEABLE_MCI, 2 << 20, 128, VAL_MEM_CODEC_FOR_VENC);
	fails += test_alloc(VAL_MEM_TYPE_FOR_SW, 65536, 1, VAL_MEM_CODEC_FOR_VENC);
	if (nc)
		fails += test_alloc(VAL_MEM_TYPE_FOR_HW_NONCACHEABLE, 65536, 4096, VAL_MEM_CODEC_FOR_VDEC);
	printf("smoke_test: %s (%.1f ms)\n", fails ? "FAILED" : "PASSED", now_ms() - t0);
	return fails ? 1 : 0;
}
