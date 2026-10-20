/*
 * vcshim: shared state behind the replacement libion.so / libion_mtk.so /
 * libm4u.so. The buffer table lives in libion.so. The other stubs link
 * against libion.so and call these functions.
 *
 * Model: an "ION handle" is a small integer that indexes our table. Each
 * entry owns one dma-buf fd from VCODEC_SHIM_ALLOC plus the device address
 * (IOVA). CPU mappings made through ion_mmap() are recorded so that a VA
 * can be turned back into a buffer (for m4u_alloc_mva and cache sync).
 */
#ifndef VCSHIM_H
#define VCSHIM_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define VCSHIM_EXPORT __attribute__((visibility("default")))

struct vcshim_buf {
	int      dmabuf_fd;   /* owned by the table */
	uint64_t size;
	uint32_t iova;
	int      cached;
};

/* /dev/Vcodec fd used for allocation (opened once, never closed) */
VCSHIM_EXPORT int  vcshim_devfd(void);
/* returns handle > 0, or -errno */
VCSHIM_EXPORT int  vcshim_alloc(size_t len, size_t align, int want_cached, int venc);
VCSHIM_EXPORT int  vcshim_free(int handle);
VCSHIM_EXPORT int  vcshim_get(int handle, struct vcshim_buf *out);
VCSHIM_EXPORT void vcshim_map_add(int handle, void *va, size_t len);
VCSHIM_EXPORT void vcshim_map_del(void *va);
/* VA -> handle (+ IOVA of that VA). Returns handle or -1 */
VCSHIM_EXPORT int  vcshim_find_va(uintptr_t va, uint32_t *iova);
/* Cache maintenance. op: 1 = clean (CPU->device), 2 = invalidate
 * (device->CPU), 3 = flush (both). handle <= 0 means all buffers.
 * No-op for write-combined buffers. */
VCSHIM_EXPORT void vcshim_sync(int handle, int op);

VCSHIM_EXPORT void vcshim_log(int level, const char *fmt, ...)
	__attribute__((format(printf, 2, 3)));
/* level: 0 = error, 1 = info, 2 = debug (env VCODEC_SHIM_DEBUG=1|2) */
#define VCS_ERR(...)  vcshim_log(0, __VA_ARGS__)
#define VCS_INFO(...) vcshim_log(1, __VA_ARGS__)
#define VCS_DBG(...)  vcshim_log(2, __VA_ARGS__)

#ifdef __cplusplus
}
#endif
#endif
