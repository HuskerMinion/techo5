/*
 * Replacement for MediaTek libion_mtk.so: exactly the imports of
 * libvcodec_utility.so, with signatures taken from the vendor
 * libion_mtk.so disassembly.
 *
 *   int   mt_ion_open(const char *name)
 *   int   ion_alloc_mm(int fd, size_t len, size_t align, unsigned flags, int *handle)
 *         (vendor: ion_alloc(fd, len, align, ION_HEAP_MULTIMEDIA_MASK=0x400, flags, h))
 *   int   ion_custom_ioctl(int fd, unsigned cmd, void *arg)
 *         (vendor: ioctl(fd, ION_IOC_CUSTOM=0xc0084906, {cmd, arg}))
 *   void *ion_mmap(int fd, void *addr, size_t len, int prot, int flags, int share_fd, off_t off)
 *   int   ion_munmap(int fd, void *addr, size_t len)
 *   int   ion_share_close(int fd, int share_fd)    -> close(share_fd)
 *
 * How libvcodec_utility eVideoMemAlloc() uses them (eMemType 0..2):
 *   fd = mt_ion_open("val_public")              (closed later with plain close())
 *   ion_alloc_mm(fd, u4MemSize, eAlignment, 3, &mem->pIonBufhandle)
 *   ion_share(fd, handle, &mem->i4IonShareFd)
 *   va = ion_mmap(fd, 0, u4MemSize, PROT_RW, MAP_SHARED, share_fd, 0)
 *   ion_custom_ioctl(fd, ION_CMD_MULTIMEDIA, {ION_MM_CONFIG_BUFFER, handle,
 *                    module 17 (VDEC) / 34 (VENC), security 0, coherent 0})
 *   ion_custom_ioctl(fd, ION_CMD_SYSTEM, {ION_SYS_GET_PHYS, handle, &pa, &len})
 *   -> mem->pvMemPa = pa   (the address the codec HW is programmed with)
 * eVideoFlushCache / eVideoInvalidateCache:
 *   ion_custom_ioctl(fd, ION_CMD_SYSTEM, {ION_SYS_CACHE_SYNC, handle, -, -, type})
 *   type 1 for one buffer, 7 = INVALID_ALL / 8 = FLUSH_ALL for all buffers.
 *   (The per-buffer VA/size fields are never filled in, and
 *   VCodecDrvFlushCachedBuffer() even passes an uninitialized handle, so
 *   only "whole buffer" or "everything" can be honored: we flush.)
 */
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#include "vcshim.h"

/* MediaTek ion_drv.h */
enum { ION_CMD_SYSTEM = 0, ION_CMD_MULTIMEDIA = 1, ION_CMD_MULTIMEDIA_SEC = 2 };
enum { ION_MM_CONFIG_BUFFER = 0 };
enum {
	ION_SYS_CACHE_SYNC = 0, ION_SYS_GET_PHYS = 1, ION_SYS_GET_CLIENT = 2,
	ION_SYS_SET_HANDLE_BACKTRACE = 3, ION_SYS_SET_CLIENT_NAME = 4, ION_SYS_DMA_OP = 5,
};
enum {
	ION_CACHE_CLEAN_BY_RANGE = 0, ION_CACHE_INVALID_BY_RANGE, ION_CACHE_FLUSH_BY_RANGE,
	ION_CACHE_CLEAN_BY_RANGE_USE_VA, ION_CACHE_INVALID_BY_RANGE_USE_VA,
	ION_CACHE_FLUSH_BY_RANGE_USE_VA, ION_CACHE_CLEAN_ALL, ION_CACHE_INVALID_ALL,
	ION_CACHE_FLUSH_ALL,
};

struct ion_mm_data {           /* ION_CMD_MULTIMEDIA */
	uint32_t mm_cmd;
	int32_t  handle;
	uint32_t module_id;
	uint32_t security;
	uint32_t coherent;
};
struct ion_sys_data {          /* ION_CMD_SYSTEM; only the members we use */
	uint32_t sys_cmd;
	union {
		struct { int32_t handle; void *va; uint32_t size; uint32_t sync_type; } cache;
		struct { int32_t handle; uint32_t phy_addr; unsigned long len; } phys;
	};
};

VCSHIM_EXPORT int mt_ion_open(const char *name)
{
	extern int ion_open(void);
	int fd = ion_open();
	VCS_DBG("mt_ion_open(%s) = %d", name ? name : "", fd);
	return fd;
}

VCSHIM_EXPORT int ion_alloc_mm(int fd, size_t len, size_t align, unsigned int flags, int *handle)
{
	const char *c = getenv("VCODEC_SHIM_CACHED");
	int want_cached = (flags & 1) && c && atoi(c);
	(void)fd;
	int h = vcshim_alloc(len, align, want_cached, 0);
	if (h < 0)
		return h;
	*handle = h;
	return 0;
}

/* identical in the vendor lib: ion_alloc_mm with another heap mask */
VCSHIM_EXPORT int ion_alloc_camera(int fd, size_t len, size_t align, unsigned int flags, int *handle)
{
	return ion_alloc_mm(fd, len, align, flags, handle);
}

VCSHIM_EXPORT int ion_alloc_syscontig(int fd, size_t len, size_t align, unsigned int flags, int *handle)
{
	return ion_alloc_mm(fd, len, align, flags, handle);
}

VCSHIM_EXPORT int ion_custom_ioctl(int fd, unsigned int cmd, void *arg)
{
	(void)fd;
	if (cmd == ION_CMD_MULTIMEDIA) {
		struct ion_mm_data *d = arg;
		if (d->mm_cmd == ION_MM_CONFIG_BUFFER) {
			/* The port/module only selects which M4U port the vendor
			 * kernel maps the buffer for. Our buffers are already
			 * mapped into the codec domain at allocation time. */
			if (vcshim_get(d->handle, NULL)) {
				VCS_ERR("CONFIG_BUFFER: bad handle %d", d->handle);
				return -EINVAL;
			}
			VCS_DBG("CONFIG_BUFFER h=%d module=%u", d->handle, d->module_id);
		} else {
			VCS_DBG("ION_CMD_MULTIMEDIA mm_cmd=%u ignored", d->mm_cmd);
		}
		return 0;
	}
	if (cmd == ION_CMD_SYSTEM) {
		struct ion_sys_data *d = arg;
		struct vcshim_buf b;
		switch (d->sys_cmd) {
		case ION_SYS_GET_PHYS:
			if (vcshim_get(d->phys.handle, &b)) {
				VCS_ERR("GET_PHYS: bad handle %d", d->phys.handle);
				return -EINVAL;
			}
			d->phys.phy_addr = b.iova;
			d->phys.len = (unsigned long)b.size;
			VCS_DBG("GET_PHYS h=%d -> 0x%08x", d->phys.handle, b.iova);
			return 0;
		case ION_SYS_CACHE_SYNC: {
			uint32_t t = d->cache.sync_type;
			int op = (t == ION_CACHE_CLEAN_BY_RANGE || t == ION_CACHE_CLEAN_BY_RANGE_USE_VA ||
				  t == ION_CACHE_CLEAN_ALL) ? 1 : 3;
			int all = t >= ION_CACHE_CLEAN_ALL;
			int h = all ? 0 : d->cache.handle;
			if (!all && vcshim_get(h, NULL))
				h = 0; /* garbage handle (see top comment): do everything */
			vcshim_sync(h, op);
			return 0;
		}
		default:
			VCS_DBG("ION_CMD_SYSTEM sys_cmd=%u ignored", d->sys_cmd);
			return 0;
		}
	}
	VCS_ERR("ion_custom_ioctl cmd %u unsupported", cmd);
	return -EINVAL;
}

VCSHIM_EXPORT void *ion_mmap(int fd, void *addr, size_t length, int prot, int flags,
			     int share_fd, off_t offset)
{
	(void)fd;
	void *p = mmap(addr, length, prot, flags, share_fd, offset);
	if (p == MAP_FAILED) {
		VCS_ERR("ion_mmap(share_fd %d, len %zu): %s", share_fd, length, strerror(errno));
		return MAP_FAILED;
	}
	/* Which buffer is share_fd? A dup()ed fd shares the open file, so
	 * st_dev/st_ino match (each dma-buf / memfd has its own inode). */
	struct stat st, sb;
	int h, found = 0;
	if (fstat(share_fd, &st) == 0) {
		for (h = 1; h < 512 && !found; h++) {
			struct vcshim_buf b;
			if (vcshim_get(h, &b) || fstat(b.dmabuf_fd, &sb))
				continue;
			if (sb.st_dev == st.st_dev && sb.st_ino == st.st_ino) {
				vcshim_map_add(h, p, length);
				found = 1;
			}
		}
	}
	if (!found)
		VCS_DBG("ion_mmap: fd %d not one of ours (VA lookups won't see it)", share_fd);
	return p;
}

VCSHIM_EXPORT int ion_munmap(int fd, void *addr, size_t length)
{
	(void)fd;
	vcshim_map_del(addr);
	return munmap(addr, length) ? -errno : 0;
}

VCSHIM_EXPORT int ion_share_close(int fd, int share_fd)
{
	(void)fd;
	return close(share_fd) ? -errno : 0;
}

/* Rarely used helpers of the vendor lib, accepted as no-ops. */
VCSHIM_EXPORT int ion_cache_sync_flush_all(int fd)
{
	(void)fd;
	vcshim_sync(0, 3);
	return 0;
}
