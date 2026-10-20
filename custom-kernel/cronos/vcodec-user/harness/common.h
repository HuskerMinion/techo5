/* Small helpers shared by the test harnesses. */
#ifndef HARNESS_COMMON_H
#define HARNESS_COMMON_H

#include <stddef.h>
#include <stdint.h>
#include "val_types_public.h"

double now_ms(void);

/* whole file into malloc()ed memory */
uint8_t *read_file(const char *path, size_t *len);

/* Annex-B: offset of the next start code (00 00 01 / 00 00 00 01) at or
 * after pos, or len. *sc_len gets the start code length. */
size_t next_start_code(const uint8_t *b, size_t len, size_t pos, int *sc_len);

/*
 * Buffer from eVideoMemAlloc (ION path -> VCODEC_SHIM_ALLOC). va/pa are the
 * aligned addresses. The device address is what the driver API wants as
 * "PA" (it is an M4U/IOMMU address, not a physical one).
 */
typedef struct {
	VAL_MEMORY_T mem;
	uint8_t *va;
	uint32_t pa;
	uint32_t size;
} hbuf_t;
int  hbuf_alloc(hbuf_t *b, uint32_t size, uint32_t align, VAL_MEM_CODEC_T codec);
void hbuf_free(hbuf_t *b);
void hbuf_flush(hbuf_t *b);      /* CPU wrote, HW will read */
void hbuf_invalidate(hbuf_t *b); /* HW wrote, CPU will read */

#endif
