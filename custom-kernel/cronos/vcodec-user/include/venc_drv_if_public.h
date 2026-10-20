/*
 * venc_drv_if_public.h - MediaTek video ENCODER driver interface
 * (libvcodecdrv.so eVEncDrv* API), reconstructed for the MT8163 Android 11
 * armeabi-v7a vendor build ("cronos": libvcodecdrv.so 945092 bytes,
 * libMtkOmxVenc.so 157524 bytes).
 *
 * Starting point: MediaTek's venc_drv_if_public.h as published in
 * mt8163-dev/android_device_amazon_mt8163-common (include/) and the newer
 * mt8168 copy (soralis0912/cpad-TAB-A05-BD). Every value/offset that matters
 * was then checked against the binaries (libvcodecdrv.so eVEncDrv*,
 * h264_enc_*, H264_* internals; libMtkOmxVenc.so MtkOmxVenc::* callers).
 *
 * Confidence tags used below:
 *   [V]  verified in disassembly (offset/value is read or written by the
 *        driver or by libMtkOmxVenc exactly as stated)
 *   [I]  inferred: consistent with verified neighbors / struct size, but the
 *        field itself is not touched on the MT8163 H.264 path
 *   [H]  taken from the public header only (not exercised / not checked)
 *
 * 32-bit ARM (AAPCS) layout only. 64-bit "_ext64" unions are 8-byte aligned,
 * which creates the padding noted below. Static asserts at the end.
 *
 * NOTE on VAL_MEM_ADDR_T: the userspace val_types_public.h next to this file
 * defines VAL_MEM_ADDR_T as a 12-byte {VA,PA,Size} triple (correct for
 * VCodecDrvMemAllocAligned in libvcodec_utility). The ENCODER structures in
 * this libvcodecdrv build, however, embed the 24-byte "TEE" variant
 * {u64 VA, u64 PA, u64 Size} (verified: libMtkOmxVenc writes VA@+0, PA@+8,
 * Size@+16, u4BSStartVA@+24; driver reads PA@+8, Size@+16, u4BSSize@+32,
 * memcpy(sizeof BS_BUF)=72). So this header uses its own VENC_DRV_MEM_ADDR_T.
 */
#ifndef _VENC_DRV_IF_PUBLIC_H_
#define _VENC_DRV_IF_PUBLIC_H_

#include <stdint.h>
#include "val_types_public.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ion_user_handle_t is an int in Android's libion */
#ifndef VENC_ION_USER_HANDLE_T_DEFINED
#define VENC_ION_USER_HANDLE_T_DEFINED
typedef int venc_ion_user_handle_t;
#endif

/* ------------------------------------------------------------------------- */
/* Enumerations                                                              */
/* ------------------------------------------------------------------------- */

/* eVEncDrvQueryCapability() a_eType */
typedef enum __VENC_DRV_QUERY_TYPE_T {
	VENC_DRV_QUERY_TYPE_NONE = 0,               /* [V] (jump table starts at 1) */
	VENC_DRV_QUERY_TYPE_VIDEO_FORMAT = 1,       /* [V] in: VENC_DRV_QUERY_VIDEO_FORMAT_T (OMX QueryDriverH264Enc) */
	VENC_DRV_QUERY_TYPE_VIDEO_PROPERTY = 2,     /* [H] */
	VENC_DRV_QUERY_TYPE_VIDEO_PROPERTY_LIST = 3,/* [H] */
	VENC_DRV_QUERY_TYPE_PROPERTY = 4,           /* [H] */
	VENC_DRV_QUERY_TYPE_MCI_SUPPORTED = 5,      /* [V] reads property vendor.mtk.venc.mci */
	VENC_DRV_QUERY_TYPE_CHIP_NAME = 6,          /* [V] out: u32, HARD-CODED 19 = VAL_CHIP_NAME_MT8163 */
	VENC_DRV_QUERY_TYPE_INPUT_BUF_LIMIT = 7,    /* [V] VENC_DRV_QUERY_INPUT_BUF_LIMIT: stride=ALIGN16(w), slice=ALIGN16(h) */
	VENC_DRV_QUERY_TYPE_NORMAL_PRIO = 8,        /* [V] unsupported (falls to error) */
	VENC_DRV_QUERY_TYPE_VIDEO_CAMCORDER_CAP = 9,/* [V] handled (low_ram aware) */
	VENC_DRV_QUERY_TYPE_CHIP_VARIANT = 10,      /* [H] not handled (>9 -> error) */
	VENC_DRV_QUERY_TYPE_MAX = 0xFFFFFFFF
} VENC_DRV_QUERY_TYPE_T;

/* Input YUV format (VENC_DRV_PARAM_ENC_T.eVEncFormat) */
typedef enum __VENC_DRV_YUV_FORMAT_T {
	VENC_DRV_YUV_FORMAT_NONE = 0,
	VENC_DRV_YUV_FORMAT_GRAY = 1,               /* [V] rejected by H264_GetScanType */
	VENC_DRV_YUV_FORMAT_422 = 2,                /* [V] rejected */
	VENC_DRV_YUV_FORMAT_420 = 3,                /* [V] accepted: I420 (Y, U, V planar). OMX maps OMX_COLOR_FormatYUV420Planar -> 3 */
	VENC_DRV_YUV_FORMAT_411 = 4,                /* [V] rejected */
	VENC_DRV_YUV_FORMAT_YV12 = 5,               /* [V] accepted: Android YV12, chroma stride ALIGN16(bufW/2). OMX 0x7f000200 -> 5 */
	VENC_DRV_YUV_FORMAT_NV12 = 6,               /* [V] accepted: Y + interleaved CbCr. OMX YUV420SemiPlanar(0x15) -> 6 */
	VENC_DRV_YUV_FORMAT_NV21 = 7,               /* [V] REJECTED by this build's H.264 path */
	VENC_DRV_YUV_FORMAT_BLK16X32 = 8,           /* [V] rejected */
	VENC_DRV_YUV_FORMAT_BLK64X32 = 9,           /* [V] rejected */
	VENC_DRV_YUV_FORMAT_YV12_1688 = 10,         /* [V] accepted (chroma stride bufW/2) */
	VENC_DRV_YUV_FORMAT_MAX = 0xFFFFFFFF
} VENC_DRV_YUV_FORMAT_T;

/* eVEncDrvCreate()/eVEncDrvRelease() a_eVideoFormat */
typedef enum __VENC_DRV_VIDEO_FORMAT_T {
	VENC_DRV_VIDEO_FORMAT_NONE = 0,
	VENC_DRV_VIDEO_FORMAT_MPEG4 = 1,            /* [V] OMX drvCodecId */
	VENC_DRV_VIDEO_FORMAT_MPEG4_1080P = 2,      /* [H] */
	VENC_DRV_VIDEO_FORMAT_MPEG4_SHORT = 3,      /* [V] (accepted by Create bitmask 0x3a3a) */
	VENC_DRV_VIDEO_FORMAT_H263 = 4,             /* [V] */
	VENC_DRV_VIDEO_FORMAT_H264 = 5,             /* [V] <-- use this for H.264 */
	VENC_DRV_VIDEO_FORMAT_H264_VGA = 6,         /* [V] OMX codec id 1 -> 6 (not in Create's dispatch bitmask) */
	VENC_DRV_VIDEO_FORMAT_WMV9 = 7,             /* [H] */
	VENC_DRV_VIDEO_FORMAT_VC1 = 8,              /* [H] */
	VENC_DRV_VIDEO_FORMAT_VP8 = 9,              /* [V] */
	VENC_DRV_VIDEO_FORMAT_JPEG = 10,            /* [H] */
	VENC_DRV_VIDEO_FORMAT_HEVC = 11,            /* [V] */
	VENC_DRV_VIDEO_FORMAT_H264SEC = 12,         /* [V] */
	VENC_DRV_VIDEO_FORMAT_INHOUSE_H264SEC = 13, /* [V] */
	VENC_DRV_VIDEO_FORMAT_H263VT = 14,          /* [V] */
	VENC_DRV_VIDEO_FORMAT_MAX = 0xFFFFFFFF
} VENC_DRV_VIDEO_FORMAT_T;

/* Legacy frame-rate enum. In practice eFrameRate holds the integer fps
 * (OMX stores xFramerate >> 16). The H.264 driver takes fps from
 * VENC_DRV_PARAM_ENC_EXTRA_T.u4FrameRateQ16 instead. [H] */
typedef enum __VENC_DRV_FRAME_RATE_T {
	VENC_DRV_FRAME_RATE_NONE = 0,
	VENC_DRV_FRAME_RATE_7_5 = 75,
	VENC_DRV_FRAME_RATE_10 = 10,
	VENC_DRV_FRAME_RATE_15 = 15,
	VENC_DRV_FRAME_RATE_20 = 20,
	VENC_DRV_FRAME_RATE_24 = 24,
	VENC_DRV_FRAME_RATE_25 = 25,
	VENC_DRV_FRAME_RATE_29_97 = 2997,
	VENC_DRV_FRAME_RATE_30 = 30,
	VENC_DRV_FRAME_RATE_60 = 60,
	VENC_DRV_FRAME_RATE_120 = 120,
	VENC_DRV_FRAME_RATE_180 = 180,
	VENC_DRV_FRAME_RATE_240 = 240,
	VENC_DRV_FRAME_RATE_480 = 480,
	VENC_DRV_FRAME_RATE_MAX = 0xFFFFFFFF
} VENC_DRV_FRAME_RATE_T;

/* eVEncDrvEncode() a_eOpt (h264_enc_encode switch) */
typedef enum __VENC_DRV_START_OPT_T {
	VENC_DRV_START_OPT_NONE = 0,                            /* [V] error */
	VENC_DRV_START_OPT_ENCODE_SEQUENCE_HEADER = 1,          /* [V] SPS+PPS concatenated into one BS buffer; frm may be NULL */
	VENC_DRV_START_OPT_ENCODE_SEQUENCE_HEADER_H264_SPS = 2, /* [V] SPS only */
	VENC_DRV_START_OPT_ENCODE_SEQUENCE_HEADER_H264_PPS = 3, /* [V] PPS only */
	VENC_DRV_START_OPT_ENCODE_FRAME = 4,                    /* [V] encode one picture */
	VENC_DRV_START_OPT_ENCODE_KEY_FRAME = 5,                /* [V] NO-OP ("key frame not supported yet"), returns OK; use SET FORCE_INTRA_ON + ENCODE_FRAME */
	VENC_DRV_START_OPT_ENCODE_FINAL = 6,                    /* [V] error (not supported) */
	VENC_DRV_START_OPT_ENCODE_DUMMY_NAL = 7,                /* [V] error (not supported) */
	VENC_DRV_START_OPT_MAX = 0xFFFFFFFF
} VENC_DRV_START_OPT_T;

/* VENC_DRV_DONE_RESULT_T.eMessage */
typedef enum __VENC_DRV_MESSAGE_T {
	VENC_DRV_MESSAGE_NONE = 0,
	VENC_DRV_MESSAGE_OK = 1,        /* [V] H264_EncodeFrame ret 0 */
	VENC_DRV_MESSAGE_ERR = 2,       /* [V] ret >= 3 */
	VENC_DRV_MESSAGE_TIMEOUT = 3,   /* [V] ret 2 */
	VENC_DRV_MESSAGE_PARTIAL = 4,   /* [V] ret 1 */
	VENC_DRV_MESSAGE_MAX = 0xFFFFFFFF
} VENC_DRV_MESSAGE_T;

/* VENC_DRV_PARAM_ENC_T.u4Profile for H.264 (H264_GetProfile) */
typedef enum __VENC_DRV_H264_VIDEO_PROFILE_T {
	VENC_DRV_H264_VIDEO_PROFILE_UNKNOWN = 0,
	VENC_DRV_H264_VIDEO_PROFILE_BASELINE = (1 << 0),             /* [V] -> profile_idc 66 */
	VENC_DRV_H264_VIDEO_PROFILE_CONSTRAINED_BASELINE = (1 << 1), /* [V] not mapped -> logs error, uses 100 */
	VENC_DRV_H264_VIDEO_PROFILE_MAIN = (1 << 2),                 /* [V] -> 77 */
	VENC_DRV_H264_VIDEO_PROFILE_EXTENDED = (1 << 3),             /* [H] -> 100 (error) */
	VENC_DRV_H264_VIDEO_PROFILE_HIGH = (1 << 4),                 /* [V] -> 100 */
	VENC_DRV_H264_VIDEO_PROFILE_HIGH_10 = (1 << 5),
	VENC_DRV_H264_VIDEO_PROFILE_HIGH422 = (1 << 6),
	VENC_DRV_H264_VIDEO_PROFILE_HIGH444 = (1 << 7),
	VENC_DRV_H264_VIDEO_PROFILE_HIGH_10_INTRA = (1 << 8),
	VENC_DRV_H264_VIDEO_PROFILE_HIGH422_INTRA = (1 << 9),
	VENC_DRV_H264_VIDEO_PROFILE_HIGH444_INTRA = (1 << 10),
	VENC_DRV_H264_VIDEO_PROFILE_CAVLC444_INTRA = (1 << 11),
	VENC_DRV_H264_VIDEO_PROFILE_HIGH444_PREDICTIVE = (1 << 12),
	VENC_DRV_H264_VIDEO_PROFILE_SCALABLE_BASELINE = (1 << 13),
	VENC_DRV_H264_VIDEO_PROFILE_SCALABLE_HIGH = (1 << 14),
	VENC_DRV_H264_VIDEO_PROFILE_SCALABLE_HIGH_INTRA = (1 << 15),
	VENC_DRV_H264_VIDEO_PROFILE_MULTIVIEW_HIGH = (1 << 16),
	VENC_DRV_H264_VIDEO_PROFILE_MAX = 0xFFFFFFFF
} VENC_DRV_H264_VIDEO_PROFILE_T;

/* VENC_DRV_PARAM_ENC_T.u4Level (H264_GetLevel: enum -> level_idc) */
typedef enum __VENC_DRV_VIDEO_LEVEL_T {
	VENC_DRV_VIDEO_LEVEL_UNKNOWN = 0,
	VENC_DRV_VIDEO_LEVEL_0 = 1,
	VENC_DRV_VIDEO_LEVEL_1 = 2,      /* [V] -> 10 */
	VENC_DRV_VIDEO_LEVEL_1b = 3,     /* [V] -> 11 (9 if HIGH), sets constraint_set3 */
	VENC_DRV_VIDEO_LEVEL_1_1 = 4,    /* [V] -> 11 */
	VENC_DRV_VIDEO_LEVEL_1_2 = 5,    /* [V] -> 12 */
	VENC_DRV_VIDEO_LEVEL_1_3 = 6,    /* [V] -> 13 */
	VENC_DRV_VIDEO_LEVEL_2 = 7,      /* [V] -> 20 */
	VENC_DRV_VIDEO_LEVEL_2_1 = 8,    /* [V] -> 21 */
	VENC_DRV_VIDEO_LEVEL_2_2 = 9,    /* [V] -> 22 */
	VENC_DRV_VIDEO_LEVEL_3 = 10,     /* [V] -> 30 */
	VENC_DRV_VIDEO_LEVEL_3_1 = 11,   /* [V] -> 31 (also the fallback for unknown) */
	VENC_DRV_VIDEO_LEVEL_3_2 = 12,   /* [V] -> 32 */
	VENC_DRV_VIDEO_LEVEL_4 = 13,     /* [V] -> 40 */
	VENC_DRV_VIDEO_LEVEL_4_1 = 14,   /* [V] -> 41 */
	VENC_DRV_VIDEO_LEVEL_4_2 = 15,   /* [V] -> unsupported, 31 */
	VENC_DRV_VIDEO_LEVEL_5 = 16,
	VENC_DRV_VIDEO_LEVEL_5_1 = 17,
	VENC_DRV_VIDEO_LEVEL_LOW = 18,
	VENC_DRV_VIDEO_LEVEL_MEDIUM = 19,
	VENC_DRV_VIDEO_LEVEL_HIGH1440 = 20,
	VENC_DRV_VIDEO_LEVEL_HIGH = 21,
	VENC_DRV_VIDEO_LEVEL_6 = 22,
	VENC_DRV_VIDEO_LEVEL_7 = 23,
	VENC_DRV_VIDEO_LEVEL_MAX = 0xFFFFFFFF
} VENC_DRV_VIDEO_LEVEL_T;

typedef enum __VENC_DRV_RESOLUTION_T {           /* [H] */
	VENC_DRV_RESOLUTION_UNKNOWN = 0,
	VENC_DRV_RESOLUTION_SUPPORT_QCIF,
	VENC_DRV_RESOLUTION_SUPPORT_QVGA,
	VENC_DRV_RESOLUTION_SUPPORT_CIF,
	VENC_DRV_RESOLUTION_SUPPORT_HVGA,
	VENC_DRV_RESOLUTION_SUPPORT_VGA,
	VENC_DRV_RESOLUTION_SUPPORT_480I,
	VENC_DRV_RESOLUTION_SUPPORT_480P,
	VENC_DRV_RESOLUTION_SUPPORT_576I,
	VENC_DRV_RESOLUTION_SUPPORT_576P,
	VENC_DRV_RESOLUTION_SUPPORT_FWVGA,
	VENC_DRV_RESOLUTION_SUPPORT_720I,
	VENC_DRV_RESOLUTION_SUPPORT_720P,
	VENC_DRV_RESOLUTION_SUPPORT_1080I,
	VENC_DRV_RESOLUTION_SUPPORT_1080P,
	VENC_DRV_RESOLUTION_SUPPORT_2160P,
	VENC_DRV_RESOLUTION_SUPPORT_MAX = 0xFFFFFFFF
} VENC_DRV_RESOLUTION_T;

/*
 * eVEncDrvSetParam() a_eType. Dispatch: eVEncDrvSetParam -> handle->setParam
 * (h264_enc_setParameter, tbh over type-6..38). The callback returns
 * VAL_TRUE/FALSE; eVEncDrvSetParam maps nonzero -> VENC_DRV_MRESULT_OK.
 * Types without a case return FAIL (e.g. SET_OMX_TIDS - the OMX component
 * expects that and only logs "[WARN] set tids fail").
 */
typedef enum __VENC_DRV_SET_TYPE_T {
	VENC_DRV_SET_TYPE_UNKONW = 0,
	VENC_DRV_SET_TYPE_RST = 1,                  /* [H] */
	VENC_DRV_SET_TYPE_CB = 2,                   /* [H] */
	VENC_DRV_SET_TYPE_PARAM_RC = 3,             /* [H] */
	VENC_DRV_SET_TYPE_PARAM_ME = 4,             /* [H] */
	VENC_DRV_SET_TYPE_PARAM_EIS = 5,            /* [H] */
	VENC_DRV_SET_TYPE_PARAM_ENC = 6,            /* [V] in: VENC_DRV_PARAM_ENC_T* (copied, 160 B) -> H264_SetEncParam, then allocates work buffers */
	VENC_DRV_SET_TYPE_STATISTIC_ON = 7,         /* [V] "not supported" but returns OK (OMX calls it after PARAM_ENC) */
	VENC_DRV_SET_TYPE_STATISTIC_OFF = 8,        /* [V] "not supported", returns OK (OMX calls at deinit) */
	VENC_DRV_SET_TYPE_SET_OMX_TIDS = 9,         /* [V] no case -> FAIL; OMX passes u32 (tid<<16 | tid2) */
	VENC_DRV_SET_TYPE_MPEG4_SHORT = 10,         /* [V] no case for H.264 */
	VENC_DRV_SET_TYPE_FORCE_INTRA_ON = 11,      /* [V] in: NULL. Next ENCODE_FRAME is IDR */
	VENC_DRV_SET_TYPE_FORCE_INTRA_OFF = 12,     /* [V] in: NULL */
	VENC_DRV_SET_TYPE_TIME_LAPSE = 13,          /* [V] no case */
	VENC_DRV_SET_TYPE_ALLOC_WORK_BUF = 14,      /* [V] same code as tail of PARAM_ENC */
	VENC_DRV_SET_TYPE_DUMP_WORK_BUF = 15,       /* [V] no case */
	VENC_DRV_SET_TYPE_FREE_WORK_BUF = 16,       /* [V] */
	VENC_DRV_SET_TYPE_ADJUST_BITRATE = 17,      /* [V] in: VENC_DRV_PARAM_ENC_EXTRA_T* (u4BitRate in bps, /1000, min 50 kbps) */
	VENC_DRV_SET_TYPE_I_FRAME_INTERVAL = 18,    /* [V] in: u32* seconds (0 -> all-I, <0 -> 0), I period = sec*fps */
	VENC_DRV_SET_TYPE_WFD_MODE = 19,            /* [V] in: NULL */
	VENC_DRV_SET_TYPE_RECORD_SIZE = 20,         /* [V] in: VAL_RECORD_SIZE_T* -> eHalEMICtrlForRecordSize (EMI/BW setting) */
	VENC_DRV_SET_TYPE_USE_MCI_BUF = 21,         /* [V] no case */
	VENC_DRV_SET_TYPE_ADJUST_FRAMERATE = 22,    /* [V] in: u32* integer fps */
	VENC_DRV_SET_TYPE_INIT_QP = 23,             /* [V] no case */
	VENC_DRV_SET_TYPE_SKIP_FRAME = 24,          /* [V] in: NULL */
	VENC_DRV_SET_TYPE_SCENARIO = 25,            /* [V] in: u32* VENC_DRV_SCENARIO_T bits (OMX passes 4 or 0x20) */
	VENC_DRV_SET_TYPE_PREPEND_HEADER = 26,      /* [V] in: u32* 1 = put SPS/PPS in front of every IDR */
	VENC_DRV_SET_TYPE_SLOW_MOTION_ENCODE = 27,  /* [V] no case */
	VENC_DRV_SET_TYPE_SLOW_MOTION_POST_PROC = 28,
	VENC_DRV_SET_TYPE_SLOW_MOTION_LOCK_HW = 29,
	VENC_DRV_SET_TYPE_SLOW_MOTION_UNLOCK_HW = 30,
	VENC_DRV_SET_TYPE_NONREFP = 31,             /* [V] in: u32* */
	VENC_DRV_SET_TYPE_CONFIG_QP = 32,           /* [V] in: u32* qp (clamped to 51) */
	VENC_DRV_SET_TYPE_RFS_ON = 33,              /* [V] in: u32* */
	VENC_DRV_SET_TYPE_IDR_FRAME_INTERVAL = 34,  /* [V] in: u32* frames (0 -> 1) */
	VENC_DRV_SET_TYPE_OPERATION_RATE = 35,      /* [V] in: u32* (>=120 + big frame => slow-motion path) */
	VENC_DRV_SET_TYPE_RESERVED_36 = 36,         /* [I] no case (likely SET_KERNEL_LOG_COUNT) */
	VENC_DRV_SET_TYPE_DETECTED_FRAMERATE = 37,  /* [V] in: u32* */
	VENC_DRV_SET_TYPE_NONREFP_FREQ = 38,        /* [V] in: u32* */
	VENC_DRV_SET_TYPE_MAX = 0xFFFFFFFF
} VENC_DRV_SET_TYPE_T;

/*
 * eVEncDrvGetParam() a_eType. NOTE: in this build eVEncDrvGetParam never
 * reaches the codec; it answers from constants (handle may be NULL for all
 * types except PARAM_ENC). Unlisted types return OK without writing anything.
 * Output is a u32 at a_pvOutParam unless noted. Values below = what MT8163
 * returns.
 */
typedef enum __VENC_DRV_GET_TYPE_T {
	VENC_DRV_GET_TYPE_UNKONW = 0,
	VENC_DRV_GET_TYPE_PARAM_RC = 1,
	VENC_DRV_GET_TYPE_PARAM_ME = 2,
	VENC_DRV_GET_TYPE_PARAM_EIS = 3,
	VENC_DRV_GET_TYPE_PARAM_ENC = 4,                     /* [V] out: VENC_DRV_PARAM_ENC_T*, sets fgMultiSlice(+0x99) = (format==HEVC). Needs handle */
	VENC_DRV_GET_TYPE_STATISTIC = 5,
	VENC_DRV_GET_TYPE_GET_CPU_LOADING_INFO = 6,
	VENC_DRV_GET_TYPE_GET_YUV_FORMAT = 7,                /* [V] -> 5 (YV12) */
	VENC_DRV_GET_TYPE_GET_CODEC_TIDS = 8,
	VENC_DRV_GET_TYPE_ALLOC_META_HANDLE_LIST = 9,
	VENC_DRV_GET_TYPE_GET_BUF_INFO_FROM_META_HANDLE = 10,
	VENC_DRV_GET_TYPE_FREE_META_HANDLE_LIST = 11,
	VENC_DRV_GET_TYPE_QUERY_RESOLUTION_CHANGE_ABILITY = 12,
	VENC_DRV_GET_TYPE_PRIORITY_ADJUSTMENT_TYPE = 13,     /* [V] -> 0 */
	VENC_DRV_GET_TYPE_DEFAULT_AVC_PROFILE_TYPE = 14,     /* [V] -> 2 */
	VENC_DRV_GET_TYPE_DEFAULT_AVC_LEVEL_TYPE = 15,       /* [V] -> 4 */
	VENC_DRV_GET_TYPE_MPEG4_SW_PLATFORM = 16,            /* [V] -> 0 */
	VENC_DRV_GET_TYPE_AVC_HYBRID_PLATFORM = 17,          /* [V] -> 0 */
	VENC_DRV_GET_TYPE_NON_REF_P_FREQUENCY = 18,          /* [V] -> 1 */
	VENC_DRV_GET_TYPE_MDPPIPELINE_ENABLE_TYPE = 19,      /* [V] no case */
	VENC_DRV_GET_TYPE_SW_SOLUTION = 20,                  /* [V] -> 0 */
	VENC_DRV_GET_TYPE_RESET_BWC_ENABLE = 21,             /* [V] -> 0 */
	VENC_DRV_GET_TYPE_IS_HEVC_HW_PLATFORM = 22,          /* [V] -> 0 */
	VENC_DRV_GET_TYPE_IS_HEVC_SW_SPECIFIC_PLATFORM = 23, /* [V] -> 1 */
	VENC_DRV_GET_TYPE_IS_INHOUSE_TEE = 24,               /* [V] -> 1 */
	VENC_DRV_GET_TYPE_MULTISLICE_TYPE = 25,              /* [V] -> 0 */
	VENC_DRV_GET_TYPE_IS_DENALI_PLATFORM = 26,           /* [V] -> 0 */
	VENC_DRV_GET_TYPE_MAX = 0xFFFFFFFF
} VENC_DRV_GET_TYPE_T;

typedef enum __VENC_DRV_MRESULT_T {
	VENC_DRV_MRESULT_OK = 0,                    /* [V] */
	VENC_DRV_MRESULT_FAIL = 1,                  /* [V] */
	VENC_DRV_MRESULT_MAX = 0x0FFFFFFF
} VENC_DRV_MRESULT_T;

/* VENC_DRV_SET_TYPE_SCENARIO argument bits [H] (driver: fn @0x50d7c) */
typedef enum __VENC_DRV_SCENARIO_T {
	VENC_DRV_SCENARIO_CAMERA_REC = 1,
	VENC_DRV_SCENARIO_LIVEPHOTO_CAPTURE = (1 << 1),
	VENC_DRV_SCENARIO_LIVEPHOTO_EFFECT = (1 << 2),
	VENC_DRV_SCENARIO_CAMERA_REC_SLOW_MOTION = (1 << 3),
	VENC_DRV_SCENARIO_SCREEN_REC = (1 << 4),
	VENC_DRV_SCENARIO_VILTE_REC = (1 << 5),
} VENC_DRV_SCENARIO_T;

/* ------------------------------------------------------------------------- */
/* Structures                                                                */
/* ------------------------------------------------------------------------- */

/* 24-byte address triple used inside the encoder structs (see top note). */
typedef struct __VENC_DRV_MEM_ADDR_T {
	union { VAL_ULONG_T u4VA;   VAL_UINT64_T u4VA_ext64;   }; /* +0  [V] CPU VA */
	union { VAL_ULONG_T u4PA;   VAL_UINT64_T u4PA_ext64;   }; /* +8  [V] device address (M4U MVA) - what HW uses */
	union { VAL_ULONG_T u4Size; VAL_UINT64_T u4Size_ext64; }; /* +16 [V] buffer size in bytes */
} VENC_DRV_MEM_ADDR_T;                                        /* 24 bytes */

/* eVEncDrvQueryCapability(VENC_DRV_QUERY_TYPE_VIDEO_FORMAT, &in, NULL) */
typedef struct __VENC_DRV_QUERY_VIDEO_FORMAT_T {
	VENC_DRV_VIDEO_FORMAT_T eVideoFormat;  /* +0x00 [V] OMX: 5 */
	VAL_UINT32_T            u4Profile;     /* +0x04 [V] */
	VENC_DRV_VIDEO_LEVEL_T  eLevel;        /* +0x08 [V] */
	VENC_DRV_RESOLUTION_T   eResolution;   /* +0x0c [V] OMX: 0 */
	VAL_UINT32_T            u4Width;       /* +0x10 [V] */
	VAL_UINT32_T            u4Height;      /* +0x14 [V] */
	VAL_UINT32_T            u4Bitrate;     /* +0x18 [V] OMX: 0 */
	VAL_UINT32_T            u4FrameRate;   /* +0x1c [V] OMX: 0 */
} VENC_DRV_QUERY_VIDEO_FORMAT_T, *P_VENC_DRV_QUERY_VIDEO_FORMAT_T;  /* 32 bytes */

/* eVEncDrvQueryCapability(VENC_DRV_QUERY_TYPE_INPUT_BUF_LIMIT, &s, NULL) */
typedef struct __VENC_DRV_QUERY_INPUT_BUF_LIMIT {
	VENC_DRV_VIDEO_FORMAT_T eVideoFormat;  /* +0x00 [V] in  */
	VAL_UINT32_T            u4Width;       /* +0x04 [V] in  */
	VAL_UINT32_T            u4Height;      /* +0x08 [V] in  */
	VAL_UINT32_T            u4Stride;      /* +0x0c [V] out = ALIGN(w,16) */
	VAL_UINT32_T            u4SliceHeight; /* +0x10 [V] out = ALIGN(h,16) */
	VENC_DRV_SCENARIO_T     eScenario;     /* +0x14 [H] */
} VENC_DRV_QUERY_INPUT_BUF_LIMIT;

/* VENC_DRV_SET_TYPE_PARAM_ENC argument. sizeof == 160 [V: driver
 * eVideoMemCpy(handle+0x648, p, 0xa0)]. OMX keeps it at MtkOmxVenc+0x710. */
typedef struct __VENC_DRV_PARAM_ENC_T {
	VENC_DRV_YUV_FORMAT_T eVEncFormat;   /* +0x00 [V] 3=I420, 5=YV12, 6=NV12 */
	VAL_UINT32_T          u4Profile;     /* +0x04 [V] VENC_DRV_H264_VIDEO_PROFILE_T bit */
	VAL_UINT32_T          u4Level;       /* +0x08 [V] VENC_DRV_VIDEO_LEVEL_T */
	VAL_UINT32_T          u4Width;       /* +0x0c [V] picture width  */
	VAL_UINT32_T          u4Height;      /* +0x10 [V] picture height */
	VAL_UINT32_T          u4BufWidth;    /* +0x14 [V] luma stride (bytes); also used to locate chroma */
	VAL_UINT32_T          u4BufHeight;   /* +0x18 [V] luma rows in buffer (slice height) */
	VAL_UINT32_T          u4NumPFrm;     /* +0x1c [V] OMX writes (I-period - 1, default 29); H.264 driver ignores */
	VAL_UINT32_T          u4NumBFrm;     /* +0x20 [V] OMX writes 0; ignored */
	VAL_UINT32_T          eFrameRate;    /* +0x24 [V] OMX writes integer fps; H.264 driver ignores (uses extra.u4FrameRateQ16) */
	VAL_BOOL_T            fgInterlace;   /* +0x28 [V] (1 byte) OMX writes 0 */
	VAL_UINT8_T           _pad29[7];     /* +0x29 */
	union {
		VAL_VOID_T   *pvExtraEnc;      /* +0x30 [V] -> VENC_DRV_PARAM_ENC_EXTRA_T, MUST be non-NULL ("pvExtraEnc is NULL!" -> FAIL) */
		VAL_UINT64_T  pvExtraEnc_ext64;
	};
	VAL_MEMORY_T          rExtraEncMem;  /* +0x38 [I] 96 bytes, unused by OMX/driver for H.264 */
	VAL_BOOL_T            fgUseMCI;      /* +0x98 [I] */
	VAL_BOOL_T            fgMultiSlice;  /* +0x99 [V] written by GetParam(PARAM_ENC) */
	VAL_BOOL_T            fgMBAFF;       /* +0x9a [V] read by driver (-> handle+0xd0) ; OMX = checkMBAFF() (0) */
	VAL_UINT8_T           _pad9b[5];     /* +0x9b */
} VENC_DRV_PARAM_ENC_T, *P_VENC_DRV_PARAM_ENC_T;   /* 160 bytes */

/* Pointed to by VENC_DRV_PARAM_ENC_T.pvExtraEnc and passed directly to
 * ADJUST_BITRATE. OMX keeps it at MtkOmxVenc+0x7b0 (5 words, then the driver
 * handle at +0x7c4). (If word0 == 0x114 and word1 == 0xf264e7cf the driver
 * instead treats it as a 0x114-byte internal "UT" parameter block.) */
typedef struct __VENC_DRV_PARAM_ENC_EXTRA_T {
	VAL_UINT32_T u4IntraFrameRate;  /* +0x00 [V] I-frame period in FRAMES (OMX: nPFrames+1-ish; -1 -> fps; 0 -> 1) */
	VAL_UINT32_T u4BitRate;         /* +0x04 [V] bits per second (driver divides by 1000) */
	VAL_UINT32_T u4FrameRateQ16;    /* +0x08 [V] fps in Q16 (driver uses the high 16 bits only) */
	VAL_UINT32_T u4UseMBAFF;        /* +0x0c [I] not written by OMX */
	VAL_UINT32_T u4BitrateMode;     /* +0x10 [V] OMX: 1 if CBR else 0 (driver H.264 path does not read it) */
} VENC_DRV_PARAM_ENC_EXTRA_T, *pVENC_DRV_PARAM_ENC_EXTRA_T;  /* 20 bytes */

#define VENC_DRV_VDO_PROP_LIST_MAX (64)

typedef struct __VENC_DRV_VIDEO_PROPERTY_T {   /* [H] */
	VENC_DRV_VIDEO_FORMAT_T eVideoFormat;
	VAL_UINT32_T u4Width;
	VAL_UINT32_T u4Height;
	VAL_UINT32_T u4FrameRate;
	VAL_UINT32_T u4BitRate;
	VAL_BOOL_T   fgPropIsValid;
} VENC_DRV_VIDEO_PROPERTY_T, *P_VENC_DRV_VIDEO_PROPERTY_T;

typedef struct __VENC_DRV_TIMESTAMP_T {
	VAL_UINT32_T u4TimeStamp[2];   /* read as one 64-bit value (lo, hi), microseconds; only used for ViLTE adaptive drop */
} VENC_DRV_TIMESTAMP_T, *P_VENC_DRV_TIMESTAMP_T;

typedef struct __VENC_DRV_EIS_INPUT_T {
	VAL_UINT32_T u4X;
	VAL_UINT32_T u4Y;
} VENC_DRV_EIS_INPUT_T, *P_VENC_DRV_EIS_INPUT_T;

/* Input picture. OMX keeps it at MtkOmxVenc+0x628 (next member at +0x680). */
typedef struct __VENC_DRV_PARAM_FRM_BUF_T {
	VENC_DRV_MEM_ADDR_T  rFrmBufAddr;    /* +0x00 [V] VA@+0, PA(MVA)@+8 -> VENC reg 0x6c (Y, >>4), Size@+16 */
	VENC_DRV_MEM_ADDR_T  rCoarseAddr;    /* +0x18 [I] unused (zero) */
	VENC_DRV_TIMESTAMP_T rTimeStamp;     /* +0x30 [V] read by H264_EncodeFrameInit (optional, OMX leaves it) */
	VENC_DRV_EIS_INPUT_T rEISInput;      /* +0x38 [I] */
	VAL_UINT32_T         rSecMemHandle;  /* +0x40 [V] OMX writes; 0 for normal buffers */
	VAL_UINT32_T         _pad44;         /* +0x44 */
	union {
		venc_ion_user_handle_t pIonBufhandle; /* +0x48 [V] OMX ION entry handle; driver uses it only in WFD flush path */
		VAL_UINT64_T           pIonBufhandle_ext64;
	};
	VAL_UINT32_T         u4IonDevFd;     /* +0x50 [V] OMX writes ion device fd (0 for non-ION) */
	VAL_UINT32_T         _pad54;         /* +0x54 */
} VENC_DRV_PARAM_FRM_BUF_T, *P_VENC_DRV_PARAM_FRM_BUF_T;   /* 88 bytes */

/* Output bitstream buffer. sizeof == 72 [V: driver memcpy 0x48].
 * OMX keeps it at MtkOmxVenc+0x5e0. */
typedef struct __VENC_DRV_PARAM_BS_BUF_T {
	VENC_DRV_MEM_ADDR_T  rBSAddr;        /* +0x00 [V] VA@+0 (CPU writes SPS/PPS here), PA(MVA)@+8 -> reg 0x64 (>>4), Size@+16 -> reg 0x68 (>>7, 128-B units) */
	union {
		VAL_ULONG_T  u4BSStartVA;      /* +0x18 [V] OMX sets = VA */
		VAL_UINT64_T u4BSStartVA_ext64;
	};
	union {
		VAL_ULONG_T  u4BSSize;         /* +0x20 [V] OUT: bytes produced (header or frame) */
		VAL_UINT64_T u4BSSize_ext64;
	};
	VENC_DRV_TIMESTAMP_T rTimeStamp;     /* +0x28 [I] */
	VAL_UINT32_T         rSecMemHandle;  /* +0x30 [V] OMX writes 0 */
	VAL_UINT32_T         _pad34;         /* +0x34 */
	union {
		venc_ion_user_handle_t pIonBufhandle; /* +0x38 [V] used only by WFD-mode flush / chip 27,28 invalidate */
		VAL_UINT64_T           pIonBufhandle_ext64;
	};
	VAL_UINT32_T         u4IonDevFd;     /* +0x40 [V] OMX writes ion dev fd */
	VAL_UINT32_T         _pad44;         /* +0x44 */
} VENC_DRV_PARAM_BS_BUF_T, *P_VENC_DRV_PARAM_BS_BUF_T;   /* 72 bytes */

/* eVEncDrvEncode() a_prResult */
typedef struct __VENC_DRV_DONE_RESULT_T {
	VENC_DRV_MESSAGE_T eMessage;       /* +0x00 [V] (frame path only) */
	VAL_UINT32_T       _pad04;         /* +0x04 */
	union {
		P_VENC_DRV_PARAM_BS_BUF_T prBSBuf;   /* +0x08 [V] = a_prBSBuf; size in prBSBuf->u4BSSize */
		VAL_UINT64_T              prBSBuf_ext64;
	};
	union {
		P_VENC_DRV_PARAM_FRM_BUF_T prFrmBuf; /* +0x10 [H] */
		VAL_UINT64_T               prFrmBuf_ext64;
	};
	VAL_BOOL_T         fgIsKeyFrm;     /* +0x18 [V] 1 byte; 0 for headers, 1 for IDR */
	VAL_UINT8_T        _pad19[3];
	VAL_UINT32_T       u4HWEncodeTime; /* +0x1c [H] */
} VENC_DRV_DONE_RESULT_T, *P_VENC_DRV_DONE_RESULT_T;   /* 32 bytes */

typedef struct __VENC_DRV_PROPERTY_T {   /* [H] */
	VAL_UINT32_T u4BufAlign;
	VAL_UINT32_T u4BufUnitSize;
	VAL_UINT32_T u4ExtraBufSize;
	VAL_BOOL_T   fgOutputRingBuf;
	VAL_BOOL_T   fgCoarseMESupport;
	VAL_BOOL_T   fgEISSupport;
} VENC_DRV_PROPERTY_T, *P_VENC_DRV_PROPERTY_T;

/* ------------------------------------------------------------------------- */
/* Functions (libvcodecdrv.so)                                               */
/* ------------------------------------------------------------------------- */

VENC_DRV_MRESULT_T eVEncDrvQueryCapability(VENC_DRV_QUERY_TYPE_T a_eType,
					   VAL_VOID_T *a_pvInParam,
					   VAL_VOID_T *a_pvOutParam);
/* Allocates a 976-byte driver handle (*a_phHandle). */
VENC_DRV_MRESULT_T eVEncDrvCreate(VAL_HANDLE_T *a_phHandle,
				  VENC_DRV_VIDEO_FORMAT_T a_eVideoFormat);
VENC_DRV_MRESULT_T eVEncDrvRelease(VAL_HANDLE_T a_hHandle,
				   VENC_DRV_VIDEO_FORMAT_T a_eVideoFormat);
/* eValInit + eHalInit (opens /dev/Vcodec, mmaps VENC regs) + codec init
 * (ISR init, power on, chip query, reg base, M4U port config). */
VENC_DRV_MRESULT_T eVEncDrvInit(VAL_HANDLE_T a_hHandle);
VENC_DRV_MRESULT_T eVEncDrvDeInit(VAL_HANDLE_T a_hHandle);
VENC_DRV_MRESULT_T eVEncDrvSetParam(VAL_HANDLE_T a_hHandle,
				    VENC_DRV_SET_TYPE_T a_eType,
				    VAL_VOID_T *a_pvInParam,
				    VAL_VOID_T *a_pvOutParam);
VENC_DRV_MRESULT_T eVEncDrvGetParam(VAL_HANDLE_T a_hHandle,
				    VENC_DRV_GET_TYPE_T a_eType,
				    VAL_VOID_T *a_pvInParam,
				    VAL_VOID_T *a_pvOutParam);
VENC_DRV_MRESULT_T eVEncDrvEncode(VAL_HANDLE_T a_hHandle,
				  VENC_DRV_START_OPT_T a_eOpt,
				  VENC_DRV_PARAM_FRM_BUF_T *a_prFrmBuf,
				  VENC_DRV_PARAM_BS_BUF_T *a_prBSBuf,
				  VENC_DRV_DONE_RESULT_T *a_prResult);

/* ------------------------------------------------------------------------- */
/* Layout checks (armeabi-v7a)                                               */
/* ------------------------------------------------------------------------- */
#if defined(__arm__) && !defined(__aarch64__)
#if defined(__cplusplus)
#define VENC_SASSERT(c, m) static_assert(c, m)
#else
#define VENC_SASSERT(c, m) _Static_assert(c, m)
#endif
#include <stddef.h>
VENC_SASSERT(sizeof(VAL_MEMORY_T) == 96, "VAL_MEMORY_T");
VENC_SASSERT(sizeof(VENC_DRV_MEM_ADDR_T) == 24, "MEM_ADDR");
VENC_SASSERT(sizeof(VENC_DRV_QUERY_VIDEO_FORMAT_T) == 32, "QUERY_VIDEO_FORMAT");
VENC_SASSERT(sizeof(VENC_DRV_PARAM_ENC_T) == 160, "PARAM_ENC");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_ENC_T, pvExtraEnc) == 0x30, "pvExtraEnc");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_ENC_T, rExtraEncMem) == 0x38, "rExtraEncMem");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_ENC_T, fgMultiSlice) == 0x99, "fgMultiSlice");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_ENC_T, fgMBAFF) == 0x9a, "fgMBAFF");
VENC_SASSERT(sizeof(VENC_DRV_PARAM_ENC_EXTRA_T) == 20, "ENC_EXTRA");
VENC_SASSERT(sizeof(VENC_DRV_PARAM_FRM_BUF_T) == 88, "FRM_BUF");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_FRM_BUF_T, rTimeStamp) == 0x30, "frm ts");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_FRM_BUF_T, rSecMemHandle) == 0x40, "frm sec");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_FRM_BUF_T, pIonBufhandle) == 0x48, "frm ion");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_FRM_BUF_T, u4IonDevFd) == 0x50, "frm fd");
VENC_SASSERT(sizeof(VENC_DRV_PARAM_BS_BUF_T) == 72, "BS_BUF");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_BS_BUF_T, u4BSStartVA) == 0x18, "bs startva");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_BS_BUF_T, u4BSSize) == 0x20, "bs size");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_BS_BUF_T, rSecMemHandle) == 0x30, "bs sec");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_BS_BUF_T, pIonBufhandle) == 0x38, "bs ion");
VENC_SASSERT(offsetof(VENC_DRV_PARAM_BS_BUF_T, u4IonDevFd) == 0x40, "bs fd");
VENC_SASSERT(sizeof(VENC_DRV_DONE_RESULT_T) == 32, "DONE_RESULT");
VENC_SASSERT(offsetof(VENC_DRV_DONE_RESULT_T, prBSBuf) == 0x08, "res bs");
VENC_SASSERT(offsetof(VENC_DRV_DONE_RESULT_T, fgIsKeyFrm) == 0x18, "res key");
#undef VENC_SASSERT
#endif

#ifdef __cplusplus
}
#endif

#endif /* _VENC_DRV_IF_PUBLIC_H_ */
