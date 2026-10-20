#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "common.h"

double now_ms(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec * 1e3 + ts.tv_nsec / 1e6;
}

uint8_t *read_file(const char *path, size_t *len)
{
	FILE *f = fopen(path, "rb");
	if (!f) {
		perror(path);
		return NULL;
	}
	fseek(f, 0, SEEK_END);
	long n = ftell(f);
	fseek(f, 0, SEEK_SET);
	uint8_t *b = malloc(n + 16);
	if (!b || fread(b, 1, n, f) != (size_t)n) {
		fclose(f);
		free(b);
		return NULL;
	}
	memset(b + n, 0, 16);
	fclose(f);
	*len = n;
	return b;
}

size_t next_start_code(const uint8_t *b, size_t len, size_t pos, int *sc_len)
{
	for (size_t i = pos; i + 3 <= len; i++) {
		if (b[i] == 0 && b[i + 1] == 0) {
			if (b[i + 2] == 1) {
				if (sc_len) *sc_len = 3;
				if (i > pos && b[i - 1] == 0) {
					if (sc_len) *sc_len = 4;
					return i - 1;
				}
				return i;
			}
		}
	}
	if (sc_len) *sc_len = 0;
	return len;
}

int hbuf_alloc(hbuf_t *b, uint32_t size, uint32_t align, VAL_MEM_CODEC_T codec)
{
	memset(b, 0, sizeof(*b));
	b->mem.eMemType = VAL_MEM_TYPE_FOR_HW_CACHEABLE;
	b->mem.u4MemSize = size;
	b->mem.eAlignment = align ? align : 1;
	b->mem.eMemCodec = codec;
	if (eVideoMemAlloc(&b->mem, sizeof(b->mem)) != VAL_RESULT_NO_ERROR || !b->mem.pvAlignMemVa) {
		fprintf(stderr, "eVideoMemAlloc(%u) failed\n", size);
		return -1;
	}
	b->va = b->mem.pvAlignMemVa;
	b->pa = (uint32_t)(uintptr_t)b->mem.pvAlignMemPa;
	b->size = size;
	return 0;
}

void hbuf_free(hbuf_t *b)
{
	if (b->va)
		eVideoMemFree(&b->mem, sizeof(b->mem));
	memset(b, 0, sizeof(*b));
}

void hbuf_flush(hbuf_t *b)
{
	eVideoFlushCache(&b->mem, sizeof(b->mem), 1);
}

void hbuf_invalidate(hbuf_t *b)
{
	eVideoInvalidateCache(&b->mem, sizeof(b->mem), 1);
}
