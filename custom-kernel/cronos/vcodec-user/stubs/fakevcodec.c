/*
 * TEST ONLY (qemu / no kernel driver): LD_PRELOAD=/system/lib/libfakevcodec.so
 * fakes /dev/Vcodec, so the harnesses can run the whole driver-API flow
 * without hardware.
 *  - open("/dev/Vcodec") returns an fd on /dev/null.
 *  - Every 'M' ioctl succeeds. VCODEC_ALLOC_NON_CACHE_BUFFER hands out a fake
 *    address. VCODEC_WAITISR returns at once ("HW done"), and the registers
 *    read as zero.
 *  - mmap() of a fake fd gives zeroed anonymous memory (register windows,
 *    non-cached buffers).
 * The decoded output is garbage. This checks the call sequence, the struct
 * layouts and the buffer handling, not the codec. Use it together with
 * VCODEC_SHIM_FAKE=1.
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <unistd.h>

#define EXPORT __attribute__((visibility("default")))
static int fake_fds[64];
static uint32_t next_pa = 0x50000000;

static int is_fake(int fd)
{
	for (int i = 0; i < 64; i++)
		if (fake_fds[i] == fd && fd > 0)
			return 1;
	return 0;
}

static int fake_open(void)
{
	int fd = (int)syscall(__NR_openat, AT_FDCWD, "/dev/null", O_RDWR | O_CLOEXEC, 0);
	for (int i = 0; i < 64 && fd >= 0; i++)
		if (!fake_fds[i]) { fake_fds[i] = fd; break; }
	fprintf(stderr, "[fakevcodec] open /dev/Vcodec -> %d\n", fd);
	return fd;
}

EXPORT int open(const char *path, int flags, ...)
{
	if (path && !strcmp(path, "/dev/Vcodec"))
		return fake_open();
	va_list ap;
	va_start(ap, flags);
	int mode = va_arg(ap, int);
	va_end(ap);
	return (int)syscall(__NR_openat, AT_FDCWD, path, flags, mode);
}

EXPORT int __open_2(const char *path, int flags)
{
	if (path && !strcmp(path, "/dev/Vcodec"))
		return fake_open();
	return (int)syscall(__NR_openat, AT_FDCWD, path, flags, 0);
}

EXPORT int ioctl(int fd, int req, ...)
{
	va_list ap;
	va_start(ap, req);
	void *arg = va_arg(ap, void *);
	va_end(ap);
	if (is_fake(fd)) {
		unsigned nr = req & 0xff, magic = (req >> 8) & 0xff;
		if (magic == 'M' && nr == 0x22) {          /* ALLOC_NON_CACHE_BUFFER */
			uint8_t *m = arg;
			uint32_t size = *(uint32_t *)(m + 8);
			*(uint32_t *)(m + 0x18) = next_pa;       /* pvMemPa */
			next_pa += (size + 0xffff) & ~0xffffu;
		}
		if (getenv("FAKEVCODEC_VERBOSE"))
			fprintf(stderr, "[fakevcodec] ioctl %c 0x%02x\n", magic, nr);
		return 0;
	}
	return (int)syscall(__NR_ioctl, fd, req, arg);
}

EXPORT void *mmap64(void *addr, size_t len, int prot, int flags, int fd, off64_t off)
{
	if (is_fake(fd)) {
		if (getenv("FAKEVCODEC_VERBOSE"))
			fprintf(stderr, "[fakevcodec] mmap 0x%llx +0x%zx\n", (unsigned long long)off, len);
		return (void *)syscall(__NR_mmap2, addr, len, prot, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
	}
	return (void *)syscall(__NR_mmap2, addr, len, prot, flags, fd, (unsigned long)(off >> 12));
}

EXPORT void *mmap(void *addr, size_t len, int prot, int flags, int fd, off_t off)
{
	return mmap64(addr, len, prot, flags, fd, (off64_t)(unsigned long)off);
}
