/*
 * Replacement for AOSP libion.so (legacy /dev/ion API), backed by
 * VCODEC_SHIM_ALLOC on /dev/Vcodec. It also holds the vcshim buffer table
 * (see vcshim.h).
 *
 * Imported from libion by the vendor libs: ion_free, ion_share
 * (libvcodec_utility). The rest of the classic API is here only so that
 * other vendor code finds the symbols; it follows the same model.
 *
 * Environment:
 *   VCODEC_DEV=/dev/Vcodec   device used for allocation
 *   VCODEC_SHIM_CACHED=1     request cacheable buffers (default: WC)
 *   VCODEC_SHIM_FAKE=1       no kernel: memfd buffers with fake IOVAs
 *                            (for loader/qemu tests only; the HW cannot use them)
 *   VCODEC_SHIM_DEBUG=1|2    log to stderr
 */
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/syscall.h>
#include <unistd.h>

#include "vcshim.h"
#include "../include/vcodec_shim_uapi.h"

/* <linux/dma-buf.h> */
struct vcs_dma_buf_sync { __u64 flags; };
#define VCS_DMA_BUF_SYNC_READ  (1 << 0)
#define VCS_DMA_BUF_SYNC_WRITE (2 << 0)
#define VCS_DMA_BUF_SYNC_RW    (VCS_DMA_BUF_SYNC_READ | VCS_DMA_BUF_SYNC_WRITE)
#define VCS_DMA_BUF_SYNC_START (0 << 2)
#define VCS_DMA_BUF_SYNC_END   (1 << 2)
#define VCS_DMA_BUF_IOCTL_SYNC _IOW('b', 0, struct vcs_dma_buf_sync)

#define MAX_BUFS 512
#define MAX_MAPS 1024

static struct vcshim_buf g_buf[MAX_BUFS]; /* index = handle; 0 unused */
static struct { void *va; size_t len; int handle; } g_map[MAX_MAPS];
static pthread_mutex_t g_lock = PTHREAD_RECURSIVE_MUTEX_INITIALIZER_NP;
static int g_devfd = -1;
static int g_dbg = -1;
static uint32_t g_fake_iova = 0x40000000;

static int env_int(const char *n, int def)
{
	const char *v = getenv(n);
	return v ? atoi(v) : def;
}

void vcshim_log(int level, const char *fmt, ...)
{
	if (g_dbg < 0)
		g_dbg = env_int("VCODEC_SHIM_DEBUG", 0);
	if (level > g_dbg && level != 0)
		return;
	va_list ap;
	va_start(ap, fmt);
	fprintf(stderr, "[vcshim] ");
	vfprintf(stderr, fmt, ap);
	fputc('\n', stderr);
	va_end(ap);
}

int vcshim_devfd(void)
{
	pthread_mutex_lock(&g_lock);
	if (g_devfd < 0 && !env_int("VCODEC_SHIM_FAKE", 0)) {
		const char *p = getenv("VCODEC_DEV");
		g_devfd = open(p ? p : "/dev/Vcodec", O_RDWR | O_CLOEXEC);
		if (g_devfd < 0)
			VCS_ERR("open %s: %s", p ? p : "/dev/Vcodec", strerror(errno));
	}
	pthread_mutex_unlock(&g_lock);
	return g_devfd;
}

static int fake_alloc(size_t len, struct vcshim_buf *b)
{
	int fd = syscall(__NR_memfd_create, "vcshim-fake", 1 /* MFD_CLOEXEC */);
	if (fd < 0 || ftruncate(fd, len) < 0)
		return -errno;
	b->dmabuf_fd = fd;
	b->size = len;
	b->iova = g_fake_iova;
	b->cached = 0;
	g_fake_iova += (len + 0xffff) & ~0xffffu;
	return 0;
}

int vcshim_alloc(size_t len, size_t align, int want_cached, int venc)
{
	struct vcshim_buf b = { .dmabuf_fd = -1 };
	int h, ret;

	if (len == 0)
		return -EINVAL;
	len = (len + 4095) & ~(size_t)4095;

	if (env_int("VCODEC_SHIM_FAKE", 0)) {
		ret = fake_alloc(len, &b);
		if (ret)
			return ret;
	} else {
		int dev = vcshim_devfd();
		struct vcodec_shim_alloc a;
		if (dev < 0)
			return -ENODEV;
		memset(&a, 0, sizeof(a));
		a.size = len;
		a.align = (align & (align - 1)) ? 0 : (uint32_t)align;
		a.flags = (want_cached ? VCODEC_SHIM_ALLOC_CACHED : 0) |
			  (venc ? VCODEC_SHIM_ALLOC_VENC : 0);
		ret = ioctl(dev, VCODEC_SHIM_ALLOC, &a);
		if (ret < 0 && errno == EOPNOTSUPP && want_cached) {
			a.flags &= ~VCODEC_SHIM_ALLOC_CACHED;
			want_cached = 0;
			ret = ioctl(dev, VCODEC_SHIM_ALLOC, &a);
		}
		if (ret < 0) {
			VCS_ERR("VCODEC_SHIM_ALLOC(%zu, align %zu): %s", len, align, strerror(errno));
			return -errno;
		}
		if (a.iova >> 32)
			VCS_ERR("IOVA 0x%llx above 4 GiB, the 32-bit libs will truncate it",
				(unsigned long long)a.iova);
		b.dmabuf_fd = a.fd;
		b.size = a.size;
		b.iova = (uint32_t)a.iova;
		b.cached = want_cached;
	}

	pthread_mutex_lock(&g_lock);
	for (h = 1; h < MAX_BUFS; h++)
		if (g_buf[h].size == 0)
			break;
	if (h == MAX_BUFS) {
		pthread_mutex_unlock(&g_lock);
		close(b.dmabuf_fd);
		VCS_ERR("buffer table full");
		return -ENOMEM;
	}
	g_buf[h] = b;
	pthread_mutex_unlock(&g_lock);
	VCS_DBG("alloc h=%d size=0x%llx iova=0x%08x fd=%d %s", h,
		(unsigned long long)b.size, b.iova, b.dmabuf_fd, b.cached ? "cached" : "wc");
	return h;
}

int vcshim_get(int h, struct vcshim_buf *out)
{
	int ok;
	pthread_mutex_lock(&g_lock);
	ok = h > 0 && h < MAX_BUFS && g_buf[h].size;
	if (ok && out)
		*out = g_buf[h];
	pthread_mutex_unlock(&g_lock);
	return ok ? 0 : -EINVAL;
}

int vcshim_free(int h)
{
	int i;
	pthread_mutex_lock(&g_lock);
	if (h <= 0 || h >= MAX_BUFS || !g_buf[h].size) {
		pthread_mutex_unlock(&g_lock);
		VCS_ERR("free of bad handle %d", h);
		return -EINVAL;
	}
	VCS_DBG("free h=%d iova=0x%08x", h, g_buf[h].iova);
	close(g_buf[h].dmabuf_fd);
	memset(&g_buf[h], 0, sizeof(g_buf[h]));
	for (i = 0; i < MAX_MAPS; i++)
		if (g_map[i].handle == h)
			g_map[i].handle = 0; /* stale mapping: forget it */
	pthread_mutex_unlock(&g_lock);
	return 0;
}

void vcshim_map_add(int h, void *va, size_t len)
{
	int i;
	pthread_mutex_lock(&g_lock);
	for (i = 0; i < MAX_MAPS; i++)
		if (!g_map[i].handle) {
			g_map[i].va = va;
			g_map[i].len = len;
			g_map[i].handle = h;
			break;
		}
	pthread_mutex_unlock(&g_lock);
	if (i == MAX_MAPS)
		VCS_ERR("mapping table full");
}

void vcshim_map_del(void *va)
{
	int i;
	pthread_mutex_lock(&g_lock);
	for (i = 0; i < MAX_MAPS; i++)
		if (g_map[i].handle && g_map[i].va == va)
			g_map[i].handle = 0;
	pthread_mutex_unlock(&g_lock);
}

int vcshim_find_va(uintptr_t va, uint32_t *iova)
{
	int i, h = -1;
	pthread_mutex_lock(&g_lock);
	for (i = 0; i < MAX_MAPS; i++) {
		uintptr_t s = (uintptr_t)g_map[i].va;
		if (g_map[i].handle && va >= s && va < s + g_map[i].len) {
			h = g_map[i].handle;
			if (iova)
				*iova = g_buf[h].iova + (uint32_t)(va - s);
			break;
		}
	}
	pthread_mutex_unlock(&g_lock);
	return h;
}

static void sync_one(struct vcshim_buf *b, int op)
{
	struct vcs_dma_buf_sync s;
	if (!b->size || !b->cached)
		return;
	if (op & 1) { /* clean: end of CPU access -> device */
		s.flags = VCS_DMA_BUF_SYNC_END | VCS_DMA_BUF_SYNC_RW;
		ioctl(b->dmabuf_fd, VCS_DMA_BUF_IOCTL_SYNC, &s);
	}
	if (op & 2) { /* invalidate: begin CPU access <- device */
		s.flags = VCS_DMA_BUF_SYNC_START | VCS_DMA_BUF_SYNC_RW;
		ioctl(b->dmabuf_fd, VCS_DMA_BUF_IOCTL_SYNC, &s);
	}
}

void vcshim_sync(int h, int op)
{
	int i;
	pthread_mutex_lock(&g_lock);
	if (h > 0 && h < MAX_BUFS && g_buf[h].size)
		sync_one(&g_buf[h], op);
	else
		for (i = 1; i < MAX_BUFS; i++)
			sync_one(&g_buf[i], op);
	pthread_mutex_unlock(&g_lock);
}

/* ---------------- classic libion API ---------------- */
typedef int ion_user_handle_t;

VCSHIM_EXPORT int ion_open(void)
{
	/* The caller may close() this fd, so hand out a fresh one each time. */
	if (env_int("VCODEC_SHIM_FAKE", 0))
		return open("/dev/null", O_RDWR | O_CLOEXEC);
	const char *p = getenv("VCODEC_DEV");
	return open(p ? p : "/dev/Vcodec", O_RDWR | O_CLOEXEC);
}

VCSHIM_EXPORT int ion_close(int fd)
{
	return close(fd) ? -errno : 0;
}

VCSHIM_EXPORT int ion_alloc(int fd, size_t len, size_t align, unsigned int heap_mask,
			    unsigned int flags, ion_user_handle_t *handle)
{
	(void)fd; (void)heap_mask;
	int want_cached = (flags & 1) && env_int("VCODEC_SHIM_CACHED", 0);
	int h = vcshim_alloc(len, align, want_cached, 0);
	if (h < 0)
		return h;
	*handle = h;
	return 0;
}

VCSHIM_EXPORT int ion_free(int fd, ion_user_handle_t handle)
{
	(void)fd;
	return vcshim_free(handle);
}

/* share fd: a dup of our dma-buf fd. The caller owns it (ion_share_close/close). */
VCSHIM_EXPORT int ion_share(int fd, ion_user_handle_t handle, int *share_fd)
{
	struct vcshim_buf b;
	(void)fd;
	if (vcshim_get(handle, &b))
		return -EINVAL;
	*share_fd = fcntl(b.dmabuf_fd, F_DUPFD_CLOEXEC, 3);
	if (*share_fd < 0)
		return -errno;
	VCS_DBG("share h=%d -> fd %d", handle, *share_fd);
	return 0;
}

VCSHIM_EXPORT int ion_map(int fd, ion_user_handle_t handle, size_t length, int prot,
			  int flags, off_t offset, unsigned char **ptr, int *map_fd)
{
	int sfd, ret = ion_share(fd, handle, &sfd);
	if (ret)
		return ret;
	void *p = mmap(NULL, length, prot, flags, sfd, offset);
	if (p == MAP_FAILED) {
		close(sfd);
		return -errno;
	}
	vcshim_map_add(handle, p, length);
	*ptr = p;
	*map_fd = sfd;
	return 0;
}

VCSHIM_EXPORT int ion_alloc_fd(int fd, size_t len, size_t align, unsigned int heap_mask,
			       unsigned int flags, int *handle_fd)
{
	ion_user_handle_t h;
	int ret = ion_alloc(fd, len, align, heap_mask, flags, &h);
	if (ret)
		return ret;
	ret = ion_share(fd, h, handle_fd);
	ion_free(fd, h); /* the dup keeps the dma-buf alive */
	return ret;
}

/* Importing foreign dma-bufs would need a kernel import ioctl: unsupported. */
VCSHIM_EXPORT int ion_import(int fd, int share_fd, ion_user_handle_t *handle)
{
	(void)fd; (void)share_fd; (void)handle;
	VCS_ERR("ion_import unsupported");
	return -ENOSYS;
}

VCSHIM_EXPORT int ion_sync_fd(int fd, int handle_fd)
{
	(void)fd; (void)handle_fd;
	return 0;
}
