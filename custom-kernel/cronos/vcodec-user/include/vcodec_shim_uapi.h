/* SPDX-License-Identifier: (GPL-2.0 WITH Linux-syscall-note) OR MIT */
/*
 * New /dev/Vcodec ioctl used by the userspace ION/M4U replacement shims
 * (vcodec-user/stubs). It replaces Android's legacy ION multimedia heap
 * plus /proc/m4u: one call allocates a buffer, maps it into the VDEC/VENC
 * IOMMU (M4U) domain, and returns it as a dma-buf.
 *
 * Shared by the kernel driver and userspace. The struct has the same layout
 * in arm32 and arm64 (only fixed-size fields, naturally aligned, no
 * pointers), so the compat_ioctl handler can pass it through unchanged.
 *
 * The magic is 'V'. The vendor ioctls use MFV_IOC_MAGIC 'M' (nr 0x00-0x34),
 * so the two sets cannot collide.
 */
#ifndef _UAPI_VCODEC_SHIM_H
#define _UAPI_VCODEC_SHIM_H

#include <linux/ioctl.h>
#include <linux/types.h>

#define VCODEC_SHIM_IOC_MAGIC 'V'

/* flags */
/*
 * The CPU mapping is cacheable. Userspace brackets CPU access with
 * DMA_BUF_IOCTL_SYNC on the fd, so the exporter must implement
 * begin_cpu_access/end_cpu_access. Without this flag the CPU mapping is
 * write-combined (dma_alloc_wc-style) and no cache maintenance is needed.
 * A driver that does not support cached buffers returns -EOPNOTSUPP, and the
 * shim then retries without the flag.
 */
#define VCODEC_SHIM_ALLOC_CACHED   (1u << 0)
/* Hint: the buffer is for the encoder (larb3) rather than the decoder
 * (larb1). Only matters if the two do not share one IOMMU domain. */
#define VCODEC_SHIM_ALLOC_VENC     (1u << 1)

struct vcodec_shim_alloc {
	__u64 size;     /* in: bytes (> 0). out: actual size (PAGE_SIZE multiple) */
	__u64 iova;     /* out: device address for VDEC/VENC DMA. Must be < 4 GiB
			 *      (the 32-bit libs truncate it to 32 bits) */
	__u32 align;    /* in: required IOVA alignment in bytes: 0 or a power of
			 *     two. <= PAGE_SIZE must always succeed. If larger,
			 *     honor it or fail with -EINVAL. The libs ask for
			 *     <= 4096 in practice. */
	__u32 flags;    /* in: VCODEC_SHIM_ALLOC_* */
	__s32 fd;       /* out: dma-buf fd (O_RDWR | O_CLOEXEC) */
	__u32 reserved; /* in: must be 0; out: 0 */
};

/*
 * VCODEC_SHIM_ALLOC: allocate zeroed memory, map it into the codec IOMMU
 * domain, and export it as a dma-buf.
 *  - mmap(fd, len <= size, offset 0, PROT_READ|PROT_WRITE, MAP_SHARED) must
 *    work, and must work several times (the shim maps a dup()ed fd).
 *  - Lifetime: the IOVA mapping and the pages are released in the dma-buf's
 *    release(), i.e. after the last fd is closed and the last mapping is
 *    gone. There is no FREE ioctl. Userspace guarantees the hardware is
 *    idle before it drops the last reference.
 *  - The ioctl may be issued on any open /dev/Vcodec fd. The shim opens its
 *    own fd for this.
 *  - Errors: -EINVAL (bad size/align/flags/reserved), -ENOMEM,
 *    -EOPNOTSUPP (CACHED not supported).
 */
#define VCODEC_SHIM_ALLOC _IOWR(VCODEC_SHIM_IOC_MAGIC, 0x01, struct vcodec_shim_alloc)
/* == 0xc0205601 */

#endif
