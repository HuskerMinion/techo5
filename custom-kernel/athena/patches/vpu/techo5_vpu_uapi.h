/* SPDX-License-Identifier: (GPL-2.0 WITH Linux-syscall-note) OR MIT */
/*
 * /dev/techo5-vpu (techo5_vpu.c): the MT8183's VPU, one command at a time. Fixed-size fields only,
 * so 32- and 64-bit callers share the layout.
 */
#ifndef _TECHO5_VPU_UAPI_H
#define _TECHO5_VPU_UAPI_H

#include <linux/ioctl.h>
#include <linux/types.h>

#define TECHO5_VPU_MAX_BUFS	4

/* A firmware image or an algorithm's ELF, in the caller's memory */
struct techo5_vpu_blob {
	__u64 data;
	__u32 size;
	__u32 reserved;
};

/*
 * The data area, mapped from the device: at offset 0 write-combined (uncached: nothing to keep in
 * step, but slow for the cores to read), at TECHO5_VPU_MMAP_CACHED cached, which needs SYNC around a
 * job for every range the cores write before it or read after it.
 */
#define TECHO5_VPU_MMAP_CACHED	0x10000000

/* One image of a job, in the data area (offset a multiple of 64) */
struct techo5_vpu_buf {
	__u8 port;		/* the algorithm's port id */
	__u8 format;		/* the firmware's format id (1: Y8 / raw bytes) */
	__u8 reserved[2];
	__u32 width;		/* in bytes, as the algorithms count it */
	__u32 height;
	__u32 stride;
	__u32 length;
	__u32 offset;
};

struct techo5_vpu_d2d {
	__u32 nbufs;
	__u32 settings_len;
	struct techo5_vpu_buf bufs[TECHO5_VPU_MAX_BUFS];
	__u8 settings[64];	/* the algorithm's settings, as it reads them */
	__u32 timeout_ms;	/* 0: one second */
	__u32 stage;		/* out: INFO25, the firmware's stage (0x1400: the job is done) */
};

/*
 * Cache maintenance on a range of the data area, for its cached mapping: FOR_DEVICE writes what the
 * cores wrote there back to memory (before a job reads it), FOR_CPU drops the cores' copies of it
 * (after a job wrote it, before they read it). Both may be given.
 */
#define TECHO5_VPU_SYNC_FOR_DEVICE	1
#define TECHO5_VPU_SYNC_FOR_CPU		2

struct techo5_vpu_sync {
	__u32 offset;
	__u32 length;
	__u32 flags;
	__u32 reserved;
};

#define TECHO5_VPU_BOOT	_IOW('v', 0x01, struct techo5_vpu_blob)
#define TECHO5_VPU_LOAD	_IOW('v', 0x02, struct techo5_vpu_blob)
#define TECHO5_VPU_D2D	_IOWR('v', 0x03, struct techo5_vpu_d2d)
#define TECHO5_VPU_SYNC	_IOW('v', 0x04, struct techo5_vpu_sync)

#endif
