/*
 * MediaTek VAL ("video abstraction layer") types, userspace view, as used by
 * the MT8163 Android 11 vendor libraries (libvcodec_utility.so,
 * libvcodecdrv.so).
 *
 * Reconstructed from Amazon's GPL kernel header
 * drivers/misc/mediatek/videocodec/include/val_types_public.h (same enum
 * values and field order), with the MediaTek userspace typedef names, and
 * checked against the armv7 binaries:
 *   - sizeof(VAL_MEMORY_T) == 96: eVideoMemAlloc()/eVideoMemFree() reject
 *     any other size and use the offsets noted below.
 *   - VAL_MEM_ADDR_T is the 12-byte {VA, PA, Size} triple:
 *     VCodecDrvMemAllocAligned() writes exactly three words. The kernel
 *     header's 64-bit "_ext64" unions do NOT apply to this struct here.
 *
 * 32-bit ARM (AAPCS) layout only.
 */
#ifndef _VAL_TYPES_PUBLIC_H_
#define _VAL_TYPES_PUBLIC_H_

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef void               VAL_VOID_T;
typedef char               VAL_BOOL_T;
typedef char               VAL_CHAR_T;
typedef signed char        VAL_INT8_T;
typedef short              VAL_INT16_T;
typedef int                VAL_INT32_T;
typedef long long          VAL_INT64_T;
typedef unsigned char      VAL_UCHAR_T;
typedef unsigned char      VAL_UINT8_T;
typedef unsigned short     VAL_UINT16_T;
typedef unsigned int       VAL_UINT32_T;
typedef unsigned long long VAL_UINT64_T;
typedef unsigned long      VAL_ULONG_T;
typedef void              *VAL_HANDLE_T;

#define IRQ_STATUS_MAX_NUM    16
#define VCODEC_THREAD_MAX_NUM 16

#define VAL_NULL  (0)
#define VAL_TRUE  (1)
#define VAL_FALSE (0)
#define VAL_RESOLUTION_CHANGED (2)

typedef enum _VAL_MEM_CODEC_T {
	VAL_MEM_CODEC_FOR_VENC = 0,
	VAL_MEM_CODEC_FOR_VDEC,
	VAL_MEM_CODEC_MAX = 0xFFFFFFFF
} VAL_MEM_CODEC_T;

typedef enum _VAL_DRIVER_TYPE_T {
	VAL_DRIVER_TYPE_NONE = 0,
	VAL_DRIVER_TYPE_MP4_ENC,              /* 1 */
	VAL_DRIVER_TYPE_MP4_DEC,              /* 2 */
	VAL_DRIVER_TYPE_H263_ENC,             /* 3 */
	VAL_DRIVER_TYPE_H263_DEC,             /* 4 */
	VAL_DRIVER_TYPE_H264_ENC,             /* 5 */
	VAL_DRIVER_TYPE_H264_DEC,             /* 6 */
	VAL_DRIVER_TYPE_SORENSON_SPARK_DEC,   /* 7 */
	VAL_DRIVER_TYPE_VC1_SP_DEC,           /* 8 */
	VAL_DRIVER_TYPE_RV9_DEC,              /* 9 */
	VAL_DRIVER_TYPE_MP1_MP2_DEC,          /* 10 */
	VAL_DRIVER_TYPE_XVID_DEC,             /* 11 */
	VAL_DRIVER_TYPE_DIVX4_DIVX5_DEC,      /* 12 */
	VAL_DRIVER_TYPE_VC1_MP_WMV9_DEC,      /* 13 */
	VAL_DRIVER_TYPE_RV8_DEC,              /* 14 */
	VAL_DRIVER_TYPE_WMV7_DEC,             /* 15 */
	VAL_DRIVER_TYPE_WMV8_DEC,             /* 16 */
	VAL_DRIVER_TYPE_AVS_DEC,              /* 17 */
	VAL_DRIVER_TYPE_DIVX_3_11_DEC,        /* 18 */
	VAL_DRIVER_TYPE_H264_DEC_MAIN,        /* 19 */
	VAL_DRIVER_TYPE_H264_DEC_MAIN_CABAC,  /* 20 */
	VAL_DRIVER_TYPE_VP8_DEC,              /* 21 */
	VAL_DRIVER_TYPE_MP2_DEC,              /* 22 */
	VAL_DRIVER_TYPE_VP9_DEC,              /* 23 */
	VAL_DRIVER_TYPE_VP8_ENC,              /* 24 */
	VAL_DRIVER_TYPE_VC1_ADV_DEC,          /* 25 */
	VAL_DRIVER_TYPE_VC1_DEC,              /* 26 */
	VAL_DRIVER_TYPE_JPEG_ENC,             /* 27 */
	VAL_DRIVER_TYPE_HEVC_ENC,             /* 28 */
	VAL_DRIVER_TYPE_HEVC_DEC,             /* 29 */
	VAL_DRIVER_TYPE_H264_ENC_LIVEPHOTO,   /* 30 */
	VAL_DRIVER_TYPE_MMDVFS,               /* 31 */
	VAL_DRIVER_TYPE_VP9_ENC,              /* 32 */
	VAL_DRIVER_TYPE_MAX = 0xFFFFFFFF
} VAL_DRIVER_TYPE_T;

typedef enum _VAL_RESULT_T {
	VAL_RESULT_NO_ERROR = 0,
	VAL_RESULT_INVALID_DRIVER,
	VAL_RESULT_INVALID_PARAMETER,
	VAL_RESULT_INVALID_MEMORY,
	VAL_RESULT_INVALID_ISR,
	VAL_RESULT_ISR_TIMEOUT,
	VAL_RESULT_UNKNOWN_ERROR,
	VAL_RESULT_RESTARTSYS,
	VAL_RESULT_MAX = 0xFFFFFFFF
} VAL_RESULT_T;

typedef enum _VAL_MEM_ALIGN_T {
	VAL_MEM_ALIGN_1 = 1,
	VAL_MEM_ALIGN_2 = (1 << 1),
	VAL_MEM_ALIGN_4 = (1 << 2),
	VAL_MEM_ALIGN_8 = (1 << 3),
	VAL_MEM_ALIGN_16 = (1 << 4),
	VAL_MEM_ALIGN_32 = (1 << 5),
	VAL_MEM_ALIGN_64 = (1 << 6),
	VAL_MEM_ALIGN_128 = (1 << 7),
	VAL_MEM_ALIGN_256 = (1 << 8),
	VAL_MEM_ALIGN_512 = (1 << 9),
	VAL_MEM_ALIGN_1K = (1 << 10),
	VAL_MEM_ALIGN_2K = (1 << 11),
	VAL_MEM_ALIGN_4K = (1 << 12),
	VAL_MEM_ALIGN_8K = (1 << 13),
	VAL_MEM_ALIGN_MAX = 0xFFFFFFFF
} VAL_MEM_ALIGN_T;

/*
 * eVideoMemAlloc() routing (libvcodec_utility, disassembly):
 *   eMemType 0..2 -> ION path (our libion/libion_mtk shims -> VCODEC_SHIM_ALLOC)
 *   eMemType 3    -> ioctl VCODEC_ALLOC_NON_CACHE_BUFFER + mmap(/dev/Vcodec,
 *                    offset = returned pvMemPa)
 */
typedef enum _VAL_MEM_TYPE_T {
	VAL_MEM_TYPE_FOR_SW = 0,
	VAL_MEM_TYPE_FOR_HW_CACHEABLE,
	VAL_MEM_TYPE_FOR_HW_CACHEABLE_MCI,
	VAL_MEM_TYPE_FOR_HW_NONCACHEABLE,
	VAL_MEM_TYPE_MAX = 0xFFFFFFFF
} VAL_MEM_TYPE_T;

/* 12 bytes (verified, see top of file) */
typedef struct _VAL_MEM_ADDR_T {
	VAL_ULONG_T u4VA;     /* +0 */
	VAL_ULONG_T u4PA;     /* +4  device (M4U/IOMMU) address */
	VAL_ULONG_T u4Size;   /* +8 */
} VAL_MEM_ADDR_T, *P_VAL_MEM_ADDR_T;

#define VAL_U64_UNION(type, name) union { type name; unsigned long long name##_ext64; }

/* 96 bytes; offsets verified in eVideoMemAlloc/eVideoMemFree */
typedef struct _VAL_MEMORY_T {
	VAL_UINT32_T    u4MemSign;                      /* +0x00 */
	VAL_MEM_TYPE_T  eMemType;                       /* +0x04 */
	VAL_U64_UNION(VAL_ULONG_T, u4MemSize);          /* +0x08 */
	VAL_U64_UNION(VAL_VOID_T *, pvMemVa);           /* +0x10 */
	VAL_U64_UNION(VAL_VOID_T *, pvMemPa);           /* +0x18 device address */
	VAL_MEM_ALIGN_T eAlignment;                     /* +0x20 */
	VAL_U64_UNION(VAL_VOID_T *, pvAlignMemVa);      /* +0x28 */
	VAL_U64_UNION(VAL_VOID_T *, pvAlignMemPa);      /* +0x30 */
	VAL_MEM_CODEC_T eMemCodec;                      /* +0x38 */
	VAL_UINT32_T    i4IonShareFd;                   /* +0x3c */
	VAL_U64_UNION(VAL_VOID_T *, pIonBufhandle);     /* +0x40 ion handle (int) */
	VAL_U64_UNION(VAL_VOID_T *, pvReserved);        /* +0x48 */
	VAL_U64_UNION(VAL_ULONG_T, u4ReservedSize);     /* +0x50 kernel VA (NC path) */
	VAL_UINT32_T    i4IonDevFd;                     /* +0x58 */
} VAL_MEMORY_T, *P_VAL_MEMORY_T;

typedef struct _VAL_RECORD_SIZE_T {
	VAL_UINT32_T u4FrmWidth;
	VAL_UINT32_T u4FrmHeight;
	VAL_UINT32_T u4BufWidth;
	VAL_UINT32_T u4BufHeight;
} VAL_RECORD_SIZE_T, *P_VAL_RECORD_SIZE_T;

/* 96 bytes: WaitISR() checks the size */
typedef struct _VAL_ISR_T {
	VAL_VOID_T        *pvHandle;
	VAL_UINT32_T       u4HandleSize;
	VAL_DRIVER_TYPE_T  eDriverType;
	VAL_VOID_T        *pvIsrFunction;
	VAL_VOID_T        *pvReserved;
	VAL_UINT32_T       u4ReservedSize;
	VAL_UINT32_T       u4TimeoutMs;
	VAL_UINT32_T       u4IrqStatusNum;
	VAL_UINT32_T       u4IrqStatus[IRQ_STATUS_MAX_NUM];
} VAL_ISR_T, *P_VAL_ISR_T;

/* 32 bytes */
typedef struct _VAL_HW_LOCK_T {
	VAL_VOID_T        *pvHandle;
	VAL_UINT32_T       u4HandleSize;
	VAL_VOID_T        *pvLock;
	VAL_UINT32_T       u4TimeoutMs;
	VAL_VOID_T        *pvReserved;
	VAL_UINT32_T       u4ReservedSize;
	VAL_DRIVER_TYPE_T  eDriverType;
	VAL_BOOL_T         bSecureInst;
} VAL_HW_LOCK_T, *P_VAL_HW_LOCK_T;

typedef struct _VAL_TIME_T {
	VAL_UINT32_T u4Sec;
	VAL_UINT32_T u4uSec;
} VAL_TIME_T, *P_VAL_TIME_T;

typedef struct _VAL_VCODEC_THREAD_ID_T {
	VAL_UINT32_T u4tid1;
	VAL_UINT32_T u4tid2;
	VAL_UINT32_T u4VCodecThreadNum;
	VAL_UINT32_T u4VCodecThreadID[VCODEC_THREAD_MAX_NUM];
} VAL_VCODEC_THREAD_ID_T;

typedef enum _VAL_SET_TYPE_T {
	VAL_SET_TYPE_CURRENT_SCENARIO,
	VAL_SET_TYPE_MCI_PORT_CONFIG,
	VAL_SET_TYPE_M4U_PORT_CONFIG,
	VAL_SET_TYPE_SET_TCM_ON,
	VAL_SET_TYPE_SET_TCM_OFF,
	VAL_SET_TYPE_SET_AV_TASK_GROUP,
	VAL_SET_FRAME_INFO,
} VAL_SET_TYPE_T;

typedef struct _HAL_POWER_T {
	VAL_VOID_T        *pvHandle;
	VAL_UINT32_T       u4HandleSize;
	VAL_DRIVER_TYPE_T  eDriverType;
	VAL_BOOL_T         fgEnable;
	VAL_VOID_T        *pvReserved;
	VAL_UINT32_T       u4ReservedSize;
} HAL_POWER_T;

/* libvcodec_utility exports used by the harness */
VAL_RESULT_T eVideoMemAlloc(P_VAL_MEMORY_T a_prParam, VAL_UINT32_T a_u4ParamSize);
VAL_RESULT_T eVideoMemFree(P_VAL_MEMORY_T a_prParam, VAL_UINT32_T a_u4ParamSize);
/* a_u4Flag != 0: this buffer (by ION handle); 0: all buffers */
VAL_RESULT_T eVideoFlushCache(P_VAL_MEMORY_T a_prParam, VAL_UINT32_T a_u4ParamSize, VAL_UINT32_T a_u4Flag);
VAL_RESULT_T eVideoInvalidateCache(P_VAL_MEMORY_T a_prParam, VAL_UINT32_T a_u4ParamSize, VAL_UINT32_T a_u4Flag);

#ifdef __cplusplus
}
#endif
#endif
