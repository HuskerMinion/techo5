/*
 * vdec_drv_if_public.h - MediaTek userspace video decoder driver API
 * (libvcodecdrv.so: eVDecDrv*), reconstructed for the MT8163 Android 11
 * armeabi-v7a vendor build ("cronos").
 *
 * Starting points: public MediaTek headers (MT8163 bq Aquaris M10 FHD tree,
 * MT6797 X20 tree, MT8168 tree). Every value the H.264 path depends on was
 * checked against the disassembly of THIS libvcodecdrv.so (eVDecDrv*,
 * h264_dec_*, Vdec_Drv_H264_*) and of libMtkOmxVdecEx.so, the OMX component
 * that calls it. Where the build differs from the public headers, the
 * binary's layout is used.
 *
 * Confidence tags on fields and values:
 *   [V]  verified in the disassembly (offset or value used by the code)
 *   [v]  consistent with the disassembly but only partly exercised
 *   [P]  taken from a public header, not checked (the H.264 path does not
 *        use it, or ignores it)
 *
 * Layout is 32-bit ARM AAPCS only. Addresses inside the vdec structs are
 * 64-bit "_ext64" unions (8-byte slots). VAL_MEM_ADDR_T in
 * val_types_public.h is the 12-byte form that libvcodec_utility uses, which
 * does NOT match the vdec API, so this header defines its own 24-byte
 * VDEC_DRV_MEM_ADDR_T. Evidence: eVDecDrvDecodeEx reads ringbuf
 * rBase.u4PA at +8 and DrvGetOutputBufferCB reads framebuf rBaseAddr.u4PA
 * at +8; libMtkOmxVdecEx writes the ring buffer at +0/+8/+16/+24/+32.
 */
#ifndef _VDEC_DRV_IF_PUBLIC_H_
#define _VDEC_DRV_IF_PUBLIC_H_

#include "val_types_public.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ------------------------------------------------------------------ */
/* Enumerations                                                        */
/* ------------------------------------------------------------------ */

/* Bits in VDEC_DRV_FRAMEBUF_T.u4FrameBufferStatus, set by the driver on
 * frames returned through GET_TYPE_DISP_FRAME_BUFFER. [V] for bits 0..3:
 * the OMX tests 0x1 ("RL return"), 0x2 ("ND return"), 0x4 ("NU return")
 * and 0x8 (maps to an output nFlags bit 0x800). */
typedef enum _VDEC_DRV_FBSTSTUS {
	VDEC_DRV_FBSTSTUS_NORMAL      = 0,
	VDEC_DRV_FBSTSTUS_REPEAT_LAST = (1 << 0), /* [V] do not display; repeat the previous frame */
	VDEC_DRV_FBSTSTUS_NOT_DISPLAY = (1 << 1), /* [V] decoded but must not be shown */
	VDEC_DRV_FBSTSTUS_NOT_USED    = (1 << 2), /* [V] buffer not written (e.g. EOS / flush) */
	VDEC_DRV_FBSTSTUS_ERROR       = (1 << 3), /* [v] name guessed: frame had decode errors */
} VDEC_DRV_FBSTSTUS;

/* VDEC_DRV_FRAMEBUF_T.u4FrameBufferType (3D packing). The OMX maps
 * 1 -> SBS and 2 -> TAB. [v] */
typedef enum _VDEC_DRV_FBTYPE {
	VDEC_DRV_FBTYPE_NORMAL = 0,
	VDEC_DRV_FBTYPE_3D_SBS = 1,
	VDEC_DRV_FBTYPE_3D_TAB = 2,
} VDEC_DRV_FBTYPE;

/* eVDecDrvCreate() a_eVideoFormat. The values marked [V] are the ones the
 * eVDecDrvCreate dispatch accepts. Any other value fails with
 * "eVDecDrvCreate FAIL". */
typedef enum _VDEC_DRV_VIDEO_FORMAT_T {
	VDEC_DRV_VIDEO_FORMAT_UNKNOWN_VIDEO_FORMAT  = 0,
	VDEC_DRV_VIDEO_FORMAT_DIVX311               = (1 << 0),  /* [V] -> MPEG4 drv */
	VDEC_DRV_VIDEO_FORMAT_DIVX4                 = (1 << 1),  /* [V] -> MPEG4 drv */
	VDEC_DRV_VIDEO_FORMAT_DIVX5                 = (1 << 2),  /* [P] (rejected by Create) */
	VDEC_DRV_VIDEO_FORMAT_XVID                  = (1 << 3),  /* [V] -> MPEG4 drv */
	VDEC_DRV_VIDEO_FORMAT_MPEG1                 = (1 << 4),  /* [P] */
	VDEC_DRV_VIDEO_FORMAT_MPEG2                 = (1 << 5),  /* [V] -> MPEG2 drv */
	VDEC_DRV_VIDEO_FORMAT_MPEG4                 = (1 << 6),  /* [V] -> MPEG4 drv */
	VDEC_DRV_VIDEO_FORMAT_H263                  = (1 << 7),  /* [V] -> MPEG4 drv */
	VDEC_DRV_VIDEO_FORMAT_H264                  = (1 << 8),  /* [V] 0x100 -> gH264_DRV_IF */
	VDEC_DRV_VIDEO_FORMAT_H265                  = (1 << 9),  /* [V] -> gH265_DRV_IF */
	VDEC_DRV_VIDEO_FORMAT_WMV7                  = (1 << 10), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_WMV8                  = (1 << 11), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_WMV9                  = (1 << 12), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_VC1                   = (1 << 13), /* [V] -> VC1 drv */
	VDEC_DRV_VIDEO_FORMAT_REALVIDEO8            = (1 << 14), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_REALVIDEO9            = (1 << 15), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_VP6                   = (1 << 16), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_VP7                   = (1 << 17), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_VP8                   = (1 << 18), /* [V] -> VP8 drv */
	VDEC_DRV_VIDEO_FORMAT_VP8_WEBP_PICTURE_MODE = (1 << 19), /* [V] -> VP8 drv */
	VDEC_DRV_VIDEO_FORMAT_VP8_WEBP_MB_ROW_MODE  = (1 << 20), /* [V] -> VP8 drv */
	VDEC_DRV_VIDEO_FORMAT_VP9                   = (1 << 21), /* [V] -> VP9 drv */
	VDEC_DRV_VIDEO_FORMAT_VP9_WEBP_PICTURE_MODE = (1 << 22), /* [V] -> VP9 drv */
	VDEC_DRV_VIDEO_FORMAT_VP9_WEBP_MB_ROW_MODE  = (1 << 23), /* [V] -> VP9 drv */
	VDEC_DRV_VIDEO_FORMAT_AVS                   = (1 << 24), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_MJPEG                 = (1 << 25), /* [P] */
	VDEC_DRV_VIDEO_FORMAT_S263                  = (1 << 26), /* [V] -> MPEG4 drv */
	VDEC_DRV_VIDEO_FORMAT_H264HP                = (1 << 27), /* [V] rejected by Create */
	VDEC_DRV_VIDEO_FORMAT_H264SEC               = (1 << 28), /* [V] secure (TEE) */
	VDEC_DRV_VIDEO_FORMAT_H265SEC               = (1 << 29), /* [V] secure (TEE) */
} VDEC_DRV_VIDEO_FORMAT_T;

typedef enum _VDEC_DRV_H264_VIDEO_PROFILE_T {
	VDEC_DRV_H264_VIDEO_PROFILE_UNKNOWN                   = 0,
	VDEC_DRV_H264_VIDEO_PROFILE_H264_BASELINE             = (1 << 0),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_CONSTRAINED_BASELINE = (1 << 1),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_MAIN                 = (1 << 2),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_EXTENDED             = (1 << 3),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_HIGH                 = (1 << 4),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_HIGH_10              = (1 << 5),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_HIGH422              = (1 << 6),
	VDEC_DRV_H264_VIDEO_PROFILE_H264_HIGH444              = (1 << 7),
} VDEC_DRV_H264_VIDEO_PROFILE_T; /* QUERY_TYPE_VIDEO_FORMAT for H.264 reports 0x17 [V] */

/* eVDecDrvQueryCapability() a_eType. [V] 0..5 are handled; 6 and 7 return
 * FAIL in this build (no UFO). */
typedef enum _VDEC_DRV_QUERY_TYPE_T {
	VDEC_DRV_QUERY_TYPE_FBTYPE            = 0, /* [V] out: u32 = 1 */
	VDEC_DRV_QUERY_TYPE_VIDEO_FORMAT      = 1, /* [V] in/out: VDEC_DRV_QUERY_VIDEO_FORMAT_T */
	VDEC_DRV_QUERY_TYPE_PROPERTY          = 2, /* [V] out: VDEC_DRV_PROPERTY_T {16,16,0} */
	VDEC_DRV_QUERY_TYPE_CHIP_NAME         = 3, /* [V] out: u32 = 19 (VAL_CHIP_NAME_* for MT8163) */
	VDEC_DRV_QUERY_TYPE_BUFFER_CONTROL    = 4, /* [v] */
	VDEC_DRV_QUERY_TYPE_FEATURE_SUPPORTED = 5, /* [v] */
	VDEC_DRV_QUERY_TYPE_CPUCORE_FREQUENCY = 6, /* [V] unsupported -> FAIL */
	VDEC_DRV_QUERY_TYPE_UFO_SUPPORT       = 7, /* [V] unsupported -> FAIL */
} VDEC_DRV_QUERY_TYPE_T;

/* eVDecDrvGetParam() a_eType. This build's numbering differs from every
 * public header found: COLOR_DESC sits at 20 and FREE_INPUT_BUFFER at 21.
 * The values marked [V] come from the dispatcher's log strings and jump
 * table, and from the OMX's call sites. */
typedef enum _VDEC_DRV_GET_TYPE_T {
	VDEC_DRV_GET_TYPE_QUERY_REF_POOL_SIZE         = 0,  /* [P] */
	VDEC_DRV_GET_TYPE_QUERY_DISP_POOL_SIZE        = 1,  /* [P] */
	VDEC_DRV_GET_TYPE_DISP_FRAME_BUFFER           = 2,  /* [V] out: P_VDEC_DRV_FRAMEBUF_T (NULL if none) */
	VDEC_DRV_GET_TYPE_FREE_FRAME_BUFFER           = 3,  /* [V] out: P_VDEC_DRV_FRAMEBUF_T (NULL if none) */
	VDEC_DRV_GET_TYPE_GET_PICTURE_INFO            = 4,  /* [V] out: VDEC_DRV_PICINFO_T */
	VDEC_DRV_GET_TYPE_GET_STATISTIC_INFO          = 5,  /* [P] */
	VDEC_DRV_GET_TYPE_GET_FRAME_MODE              = 6,  /* [P] */
	VDEC_DRV_GET_TYPE_GET_FRAME_CROP_INFO         = 7,  /* [V] out: VDEC_DRV_CROPINFO_T, needs Init done */
	VDEC_DRV_GET_TYPE_QUERY_REORDER_ABILITY       = 8,  /* [P] */
	VDEC_DRV_GET_TYPE_QUERY_DOWNSIZE_ABILITY      = 9,  /* [P] */
	VDEC_DRV_GET_TYPE_QUERY_RESIZE_ABILITY        = 10, /* [P] */
	VDEC_DRV_GET_TYPE_QUERY_DEBLOCK_ABILITY       = 11, /* [P] */
	VDEC_DRV_GET_TYPE_QUERY_DEINTERLACE_ABILITY   = 12, /* [P] */
	VDEC_DRV_GET_TYPE_QUERY_DROPFRAME_ABILITY     = 13, /* [P] */
	VDEC_DRV_GET_TYPE_GET_DECODE_STATUS_INFO      = 14, /* [P] */
	VDEC_DRV_GET_TYPE_GET_PIXEL_FORMAT            = 15, /* [V] out: u32, constant 2 (YUV_420_PLANER_MTK) for H.264 */
	VDEC_DRV_GET_TYPE_GET_CPU_LOADING_INFO        = 16, /* [V] returns OK without writing */
	VDEC_DRV_GET_TYPE_GET_HW_CRC                  = 17, /* [v] */
	VDEC_DRV_GET_TYPE_GET_CODEC_TIDS              = 18, /* [V] out: VAL_VCODEC_THREAD_ID_T (76 bytes) */
	VDEC_DRV_GET_TYPE_GET_FRAME_INTERVAL          = 19, /* [V] (log string) */
	VDEC_DRV_GET_TYPE_GET_COLOR_DESC              = 20, /* [V] out: VDEC_DRV_COLORDESC_T (log string) */
	VDEC_DRV_GET_TYPE_FREE_INPUT_BUFFER           = 21, /* [V] out: P_VDEC_DRV_RINGBUF_T (NULL if none) */
	VDEC_DRV_GET_TYPE_QUERY_VIDEO_INTERLACING     = 22, /* [v] out: u32 */
	VDEC_DRV_GET_TYPE_QUERY_VIDEO_DPB_SIZE        = 23, /* [V] out: u32 (log string) */
	VDEC_DRV_GET_TYPE_CODEC_PROPERTY              = 24, /* [V] out: u32 (VDEC_CODEC_* bits) */
	VDEC_DRV_GET_TYPE_ERRORMB_MAP                 = 25, /* [v] out: VDEC_DRV_ERROR_MB_INFO_T */
	VDEC_DRV_GET_TYPE_NEED_LEGACY_MODE            = 26, /* [V] (log string) */
	VDEC_DRV_GET_TYPE_NEED_RESET_CURRENT_TIME     = 27, /* [V] (log string) */
	VDEC_DRV_GET_TYPE_GPU_SUPPORT_BLOCK           = 28, /* [V] (log string) */
	VDEC_DRV_GET_TYPE_PROFILEORLEVELCHANGED       = 29, /* [v] */
	VDEC_DRV_GET_TYPE_SUPPORT_SVP_LEGACY          = 30, /* [V] (log string) */
} VDEC_DRV_GET_TYPE_T;

/* eVDecDrvSetParam() a_eType. [V] = the dispatcher handles it or the OMX
 * uses it with a matching log string. Notes on what eVDecDrvSetParam does:
 * 0,1,2,4,5,11,16 are ignored (return OK); 15,17,18 are only logged
 * (17/18 do NOT reach the codec, so the output format cannot be changed);
 * 19 is stored in the handle; 6 is stored and forwarded; 23 is rewritten
 * (see VDEC_DRV_CALLBACK_T) and forwarded; everything else is forwarded to
 * the codec. h264's setParameter acts only on 6, 20, 21, 22, 23 and 27. */
typedef enum _VDEC_DRV_SET_TYPE_T {
	VDEC_DRV_SET_TYPE_USE_EXT_TIMESTAMP          = 0,  /* [V] ignored */
	VDEC_DRV_SET_TYPE_SET_BUFFER_MODE            = 1,  /* [V] ignored */
	VDEC_DRV_SET_TYPE_SET_FRAME_BUFFER_TYPE      = 2,  /* [V] ignored */
	VDEC_DRV_SET_TYPE_FREE_FRAME_BFFER           = 3,  /* [v] forwarded */
	VDEC_DRV_SET_TYPE_SET_REF_EXT_POOL_ADDR      = 4,  /* [V] ignored */
	VDEC_DRV_SET_TYPE_SET_DISP_EXT_POOL_ADDR     = 5,  /* [V] ignored */
	VDEC_DRV_SET_TYPE_SET_DECODE_MODE            = 6,  /* [V] in: VDEC_DRV_SET_DECODE_MODE_T */
	VDEC_DRV_SET_TYPE_SET_POST_PROC              = 7,  /* [P] */
	VDEC_DRV_SET_TYPE_SET_STATISTIC_ON           = 8,  /* [P] */
	VDEC_DRV_SET_TYPE_SET_STATISTIC_OFF          = 9,  /* [P] */
	VDEC_DRV_SET_TYPE_SET_FRAME_MODE             = 10, /* [P] */
	VDEC_DRV_SET_TYPE_SET_BUF_STATUS_FOR_SPEEDY  = 11, /* [V] ignored */
	VDEC_DRV_SET_TYPE_SET_LAST_DISPLAY_TIME      = 12, /* [v] */
	VDEC_DRV_SET_TYPE_SET_CURRENT_PLAY_TIME      = 13, /* [v] */
	VDEC_DRV_SET_TYPE_SET_CONCEAL_LEVEL          = 14, /* [v] in: u32 */
	VDEC_DRV_SET_TYPE_SET_OMX_TIDS               = 15, /* [V] in: u32 (tid1<<16)|tid2, log only */
	VDEC_DRV_SET_TYPE_SET_SWITCH_TVOUT           = 16, /* [V] ignored */
	VDEC_DRV_SET_TYPE_SET_CODEC_COLOR_FORAMT     = 17, /* [V] in: u32, log only (no effect) */
	VDEC_DRV_SET_TYPE_SET_CODEC_YUV_STRIDE       = 18, /* [V] in: VDEC_DRV_YUV_STRIDE_T, log only */
	VDEC_DRV_SET_TYPE_SET_FRAMESIZE              = 19, /* [V] in: u32 (h<<16)|w, MPEG4 only */
	VDEC_DRV_SET_TYPE_SET_FIXEDMAXOUTPUTBUFFER   = 20, /* [V] in: VDEC_DRV_FIXED_MAX_OUTPUT_BUFFER_T */
	VDEC_DRV_SET_TYPE_SET_UFO_DECODE             = 21, /* [V] in: NULL; no UFO on MT8163 */
	VDEC_DRV_SET_TYPE_SET_WAIT_KEYFRAME          = 22, /* [V] in: u32 (0 none, bit0 wait at start/flush) */
	VDEC_DRV_SET_TYPE_SET_CALLBACK               = 23, /* [V] in: VDEC_DRV_CALLBACK_T (REQUIRED) */
	VDEC_DRV_SET_TYPE_SET_FULL_SPEED             = 24, /* [v] ignored by h264 */
	VDEC_DRV_SET_TYPE_SET_KERNEL_LOG_COUNT       = 25, /* [V] (OMX log string) */
	VDEC_DRV_SET_TYPE_SET_VIDEO_INFO             = 26, /* [v] used by OMX UpdateVideoInfo */
	VDEC_DRV_SET_TYPE_SET_NALSIZELENGTH          = 27, /* [V] in: the value itself (not a pointer); 0 = Annex-B */
	VDEC_DRV_SET_TYPE_UNKNOWN_28                 = 28, /* [V] OMX passes &(u8)1 before Init; ignored by h264 */
	VDEC_DRV_SET_TYPE_UNKNOWN_29                 = 29, /* [V] OMX passes &u32 before each decode; ignored by h264 */
	VDEC_DRV_SET_TYPE_UNKNOWN_30                 = 30, /* [V] OMX passes &u32 after Init; ignored by h264 */
} VDEC_DRV_SET_TYPE_T;

/* VDEC_DRV_GET_TYPE_GET_PIXEL_FORMAT and QUERY_VIDEO_FORMAT.ePixelFormat.
 * H.264 on this chip reports 2 (MTK 16x32 block). The OMX maps 1 -> I420
 * (0x13), 2 -> 0x7F000001 (MTKYUV block), 3 -> 0x7F000003 (UFO) and
 * 4 -> 0x7F000200 (YV12). [V] */
typedef enum _VDEC_DRV_PIXEL_FORMAT_T {
	VDEC_DRV_PIXEL_FORMAT_NONE                 = 0,
	VDEC_DRV_PIXEL_FORMAT_YUV_420_PLANER       = 1,
	VDEC_DRV_PIXEL_FORMAT_YUV_420_PLANER_MTK   = 2, /* [V] H.264 output: 16x32 block NV12 */
	VDEC_DRV_PIXEL_FORMAT_YUV_420_PLANER_UFO   = 3,
	VDEC_DRV_PIXEL_FORMAT_YUV_YV12             = 4,
	VDEC_DRV_PIXEL_FORMAT_YUV_NV12             = 5, /* [v] OMX sends 5 for OMX NV12 via SET 17 (no effect) */
} VDEC_DRV_PIXEL_FORMAT_T;

typedef enum _VDEC_DRV_DECODER_TYPE_T {
	VDEC_DRV_DECODER_MTK_HARDWARE = 0,
	VDEC_DRV_DECODER_MTK_SOFTWARE,
	VDEC_DRV_DECODER_GOOGLE_SOFTWARE,
} VDEC_DRV_DECODER_TYPE_T;

/* VDEC_DRV_SET_DECODE_MODE_T.eDecodeMode. [V] 3, 5, 6, 7, 8 and 9 are
 * sent by the OMX. Property vendor.mtk.vdec.decodemode=1 overrides the
 * mode in the H.264 driver. */
typedef enum _VDEC_DRV_DECODE_MODE_T {
	VDEC_DRV_DECODE_MODE_UNKNOWN              = 0,
	VDEC_DRV_DECODE_MODE_NORMAL               = 1, /* decode all frames */
	VDEC_DRV_DECODE_MODE_I_ONLY               = 2,
	VDEC_DRV_DECODE_MODE_B_SKIP               = 3, /* [V] OMX uses it for seek */
	VDEC_DRV_DECODE_MODE_DROPFRAME            = 4,
	VDEC_DRV_DECODE_MODE_NO_REORDER           = 5, /* [V] output ASAP, no reorder */
	VDEC_DRV_DECODE_MODE_THUMBNAIL            = 6, /* [V] */
	VDEC_DRV_DECODE_MODE_SKIP_REFERENCE_CHECK = 7, /* [V] */
	VDEC_DRV_DECODE_MODE_LOW_LATENCY_DECODE   = 8, /* [V] */
	VDEC_DRV_DECODE_MODE_DROP_ERROR_FRAME     = 9, /* [V] value; name guessed (vendor.mtk.vdec.vtdroperrframe) */
} VDEC_DRV_DECODE_MODE_T;

/* Return value of every eVDecDrv* call. [V] eVDecDrvDecodeEx maps the
 * codec's return codes to these. */
typedef enum __VDEC_DRV_MRESULT_T {
	VDEC_DRV_MRESULT_OK                      = 0,
	VDEC_DRV_MRESULT_FAIL                    = 1,
	VDEC_DRV_MRESULT_FATAL                   = 2,
	VDEC_DRV_MRESULT_RESOLUTION_CHANGED      = 3,
	VDEC_DRV_MRESULT_NEED_MORE_OUTPUT_BUFFER = 4,
	VDEC_DRV_MRESULT_MAX                     = 0x0FFFFFFF,
} VDEC_DRV_MRESULT_T;

/* VDEC_DRV_RINGBUF_T.u4InputFlag bits. [V] set by the OMX from the OMX
 * buffer flags (CODECCONFIG -> 1, EOS -> 2, error -> 8). */
typedef enum _VDEC_DRV_INPUTBUF_T {
	VDEC_DRV_INPUT_BUF_INIT_CONFIG_DATA  = (1 << 0), /* SPS/PPS only */
	VDEC_DRV_INPUT_BUF_EOS               = (1 << 1),
	VDEC_DRV_INPUT_BUF_INVALID_TIMESTAMP = (1 << 2),
	VDEC_DRV_INPUT_BUF_DATACORRUPT       = (1 << 3),
} VDEC_DRV_INPUTBUF_T;

typedef enum _VDEC_DRV_CODEC_PROPERTY_T {
	VDEC_CODEC_NONE             = 0x00000000,
	VDEC_CODEC_SUPPORT_DPB_SIZE = 0x00000001, /* [P] */
} VDEC_DRV_CODEC_PROPERTY_T;

typedef enum _VDEC_DRV_COLOR_PRIMARIES_E {
	COLOR_PRIMARIES_NO_INFO = 0,
	COLOR_PRIMARIES_BT601,
	COLOR_PRIMARIES_BT709,
	COLOR_PRIMARIES_BT2020,
} VDEC_DRV_COLOR_PRIMARIES_E;

/* ------------------------------------------------------------------ */
/* Structures (32-bit ARM layout, offsets in bytes)                    */
/* ------------------------------------------------------------------ */

#define VDEC_U64_UNION(type, name) \
	union { type name; VAL_UINT64_T name##_ext64; }

/* 24 bytes, 8-byte aligned. [V] VA@0, PA@8, Size@16. PA is the device
 * (M4U/IOMMU MVA) address that the hardware uses. Zero the upper
 * halves. */
typedef struct __VDEC_DRV_MEM_ADDR_T {
	VDEC_U64_UNION(VAL_ULONG_T, u4VA);   /* +0  [V] */
	VDEC_U64_UNION(VAL_ULONG_T, u4PA);   /* +8  [V] */
	VDEC_U64_UNION(VAL_ULONG_T, u4Size); /* +16 [V] */
} VDEC_DRV_MEM_ADDR_T;

/* Bitstream descriptor, 56 bytes. The H.264 codec reads the data at
 * rBase.u4VA with length rBase.u4Size (NOT u4Write - u4Read). The OMX
 * still fills Read=VA and Write=VA+len.
 * eVDecDrvDecodeEx rejects the buffer unless (rBase.u4VA | rBase.u4PA) is
 * 64-byte aligned. [V] */
typedef struct __VDEC_DRV_RINGBUF_T {
	VDEC_DRV_MEM_ADDR_T rBase;                 /* +0  [V] VA@0 PA@8 Size(=data length)@16 */
	VDEC_U64_UNION(VAL_ULONG_T, u4Read);       /* +24 [V] OMX: = VA */
	VDEC_U64_UNION(VAL_ULONG_T, u4Write);      /* +32 [V] OMX: = VA + length */
	VAL_UINT32_T        u4Timestamp;           /* +40 [V] OMX: low 32 bits of nTimeStamp (us) */
	VAL_UINT32_T        rSecMemHandle;         /* +44 [V] secure path only, else 0 */
	VAL_UINT32_T        u4InputFlag;           /* +48 [V] VDEC_DRV_INPUTBUF_T bits */
	VAL_UINT32_T        u4OmxInputBufSz;       /* +52 [v] OMX: input port nBufferSize */
} VDEC_DRV_RINGBUF_T, *P_VDEC_DRV_RINGBUF_T;

/* 16 bytes [V] (read by the OMX at framebuf +120/+124/+128/+132) */
typedef struct __VDEC_DRV_COLOR_PRIMARIES_INFO_T {
	VAL_BOOL_T   bVideoRangeExist;      /* +0  */
	VAL_UINT32_T u4VideoRange;          /* +4  0 narrow, 1 full */
	VAL_BOOL_T   bColourPrimariesExist; /* +8  */
	VAL_UINT32_T eColourPrimaries;      /* +12 VDEC_DRV_COLOR_PRIMARIES_E */
} VDEC_DRV_COLOR_PRIMARIES_INFO_T;

/* Output frame descriptor, 136 bytes (the OMX mallocs and memcpys exactly
 * 136). The caller owns the memory: hand it to the driver from the output
 * buffer callback and keep it alive until GET_TYPE_FREE_FRAME_BUFFER
 * returns it. The OMX fills only rBaseAddr.u4VA and .u4PA (and zeroes
 * u4Timestamp and u4FrameBufferStatus). The driver derives the geometry
 * from the SPS. (u4VA | u4PA) must be 512-byte aligned, or the
 * callback/DecodeEx rejects the buffer. [V] */
typedef struct __VDEC_DRV_FRAMEBUF_T {
	VDEC_DRV_MEM_ADDR_T rBaseAddr;             /* +0   [V] VA@0, PA@8; Y plane, C plane at +Ysize */
	VDEC_DRV_MEM_ADDR_T rPostProcAddr;         /* +24  [P] unused */
	VAL_UINT32_T    u4BufWidth;                /* +48  [P] */
	VAL_UINT32_T    u4BufHeight;               /* +52  [P] */
	VAL_UINT32_T    u4DispWidth;               /* +56  [P] */
	VAL_UINT32_T    u4DispHeight;              /* +60  [P] */
	VAL_UINT32_T    u4DispPitch;               /* +64  [P] */
	VAL_UINT32_T    u4Timestamp;               /* +68  [v] zeroed by OMX before handing over */
	VAL_UINT32_T    u4AspectRatioW;            /* +72  [P] */
	VAL_UINT32_T    u4AspectRatioH;            /* +76  [P] */
	VAL_UINT32_T    u4FrameBufferType;         /* +80  [V] VDEC_DRV_FBTYPE */
	VAL_UINT32_T    u4PictureStructure;        /* +84  [P] */
	VAL_UINT32_T    u4FrameBufferStatus;       /* +88  [V] VDEC_DRV_FBSTSTUS bits */
	VAL_UINT32_T    u4IsUFOEncoded;            /* +92  [P] */
	VAL_UINT32_T    u4Reserved1;               /* +96  [P] */
	VAL_UINT32_T    u4Reserved2;               /* +100 [P] */
	VAL_UINT32_T    u4Reserved3;               /* +104 [P] */
	VAL_UINT32_T    rSecMemHandle;             /* +108 [V] secure path only */
	VAL_UINT32_T    u4ReeVA;                   /* +112 [P] */
	VAL_UINT32_T    rFrameBufVaShareHandle;    /* +116 [V] secure path only */
	VDEC_DRV_COLOR_PRIMARIES_INFO_T rColorPriInfo; /* +120 [V] */
} VDEC_DRV_FRAMEBUF_T, *P_VDEC_DRV_FRAMEBUF_T;  /* sizeof == 136 [V] */

/* GET_TYPE_GET_FRAME_CROP_INFO, 16 bytes [V]. Right and Bottom are
 * INCLUSIVE indices (width-1, height-1). */
typedef struct __VDEC_DRV_CROPINFO_T {
	VAL_UINT32_T u4CropLeft;   /* +0  */
	VAL_UINT32_T u4CropRight;  /* +4  */
	VAL_UINT32_T u4CropTop;    /* +8  */
	VAL_UINT32_T u4CropBottom; /* +12 */
} VDEC_DRV_CROPINFO_T, *P_VDEC_DRV_CROPINFO_T;

/* GET_TYPE_GET_PICTURE_INFO, 52 bytes. +0..+12 are used by the OMX; the
 * rest come from the MT8168 header. Use a buffer of at least 64 bytes. */
typedef struct __VDEC_DRV_PICINFO_T {
	VAL_UINT32_T u4Width;              /* +0  [V] display width */
	VAL_UINT32_T u4Height;             /* +4  [V] display height */
	VAL_UINT32_T u4RealWidth;          /* +8  [V] allocation width (OMX aligns to 16) */
	VAL_UINT32_T u4RealHeight;         /* +12 [V] allocation height (OMX aligns to 32) */
	VAL_UINT32_T u4Timestamp;          /* +16 [P] */
	VAL_UINT32_T u4AspectRatioW;       /* +20 [P] */
	VAL_UINT32_T u4AspectRatioH;       /* +24 [P] */
	VAL_UINT32_T u4FrameRate;          /* +28 [P] */
	VAL_UINT32_T u4PictureStructure;   /* +32 [v] */
	VAL_UINT32_T u4IsProgressiveOnly;  /* +36 [P] */
	VAL_INT32_T  u4BitDepthLuma;       /* +40 [P] */
	VAL_INT32_T  u4BitDepthChroma;     /* +44 [P] */
	VAL_BOOL_T   bIsHorizontalScaninLSB; /* +48 [P] */
	VAL_BOOL_T   bHDRColorDesc;        /* +49 [P] */
} VDEC_DRV_PICINFO_T, *P_VDEC_DRV_PICINFO_T;

/* eVDecDrvInit() output, 40 bytes [V]. The OMX zeroes it, sets
 * bIsThumbnail, and then reads u4PicWidth/u4PicHeight as the video size.
 * Bytes 25/26 are inputs that the H.264 init copies into its instance. */
typedef struct __VDEC_DRV_SEQINFO_T {
	VAL_UINT32_T u4Width;              /* +0  [V] out: buffer (aligned) width */
	VAL_UINT32_T u4Height;             /* +4  [V] out: buffer (aligned) height */
	VAL_UINT32_T u4PicWidth;           /* +8  [V] out: display width  (OMX uses this) */
	VAL_UINT32_T u4PicHeight;          /* +12 [V] out: display height (OMX uses this) */
	VAL_INT32_T  i4AspectRatioWidth;   /* +16 [V] out: H.264 writes 1 */
	VAL_INT32_T  i4AspectRatioHeight;  /* +20 [V] out: H.264 writes 1 */
	VAL_BOOL_T   bIsThumbnail;         /* +24 [V] in */
	VAL_BOOL_T   bIsMinWorkBuffer;     /* +25 [V] in (name from MT8168 header) */
	VAL_BOOL_T   bIsMinDpbStrategy;    /* +26 [V] in (name from MT8168 header) */
	VAL_INT32_T  u4BitDepthLuma;       /* +28 [V] out */
	VAL_INT32_T  u4BitDepthChroma;     /* +32 [V] out */
	VAL_BOOL_T   bIsHorizontalScaninLSB; /* +36 [v] */
	VAL_BOOL_T   bHDRColorDesc;        /* +37 [v] */
} VDEC_DRV_SEQINFO_T, *P_VDEC_DRV_SEQINFO_T; /* sizeof == 40 */

/* SET_TYPE_SET_CODEC_YUV_STRIDE (log only), 8 bytes */
typedef struct __VDEC_DRV_YUV_STRIDE_T {
	VAL_UINT32_T u4YStride;
	VAL_UINT32_T u4UVStride;
} VDEC_DRV_YUV_STRIDE_T, *P_VDEC_DRV_YUV_STRIDE_T;

/* QUERY_TYPE_VIDEO_FORMAT, 44 bytes [V]. Input: u4VideoFormat, plus
 * u4Width/u4Height (must be >= 16). H.264 output: profile 0x17, level 17
 * or 20, max 1920x1088, StrideAlign 16, SliceHeightAlign 32,
 * ePixelFormat 2. */
typedef struct __VDEC_DRV_QUERY_VIDEO_FORMAT_T {
	VAL_UINT32_T u4VideoFormat;        /* +0  [V] in/out */
	VAL_UINT32_T u4Profile;            /* +4  [V] */
	VAL_UINT32_T u4Level;              /* +8  [V] */
	VAL_UINT32_T u4Resolution;         /* +12 [V] */
	VAL_UINT32_T u4Width;              /* +16 [V] in/out */
	VAL_UINT32_T u4Height;             /* +20 [V] in/out */
	VAL_UINT32_T u4StrideAlign;        /* +24 [V] 16 */
	VAL_UINT32_T u4SliceHeightAlign;   /* +28 [V] 32 */
	VAL_UINT32_T ePixelFormat;         /* +32 [V] VDEC_DRV_PIXEL_FORMAT_T */
	VAL_UINT32_T eDecodeType;          /* +36 [P] VDEC_DRV_DECODER_TYPE_T */
	VAL_UINT32_T u4CompatibleFlag;     /* +40 [P] */
} VDEC_DRV_QUERY_VIDEO_FORMAT_T, *P_VDEC_DRV_QUERY_VIDEO_FORMAT_T;

/* SET_TYPE_SET_DECODE_MODE, 12 bytes [V] */
typedef struct __VDEC_DRV_SET_DECODE_MODE_T {
	VAL_UINT32_T eDecodeMode;          /* +0 VDEC_DRV_DECODE_MODE_T */
	VAL_UINT32_T u4DisplayFrameNum;    /* +4 */
	VAL_UINT32_T u4DropFrameNum;       /* +8 */
} VDEC_DRV_SET_DECODE_MODE_T, *P_VDEC_DRV_SET_DECODE_MODE_T;

/* QUERY_TYPE_PROPERTY, 16 bytes [v] (H.264: {16, 16, 0}) */
typedef struct __VDEC_DRV_PROPERTY_T {
	VAL_UINT32_T u4BufAlign;           /* +0 */
	VAL_UINT32_T u4BufUnitSize;        /* +4 */
	VAL_BOOL_T   fgPostprocessSupport; /* +8 */
	struct {
		VAL_UINT32_T fgOverlay:1;
		VAL_UINT32_T fgRotate:1;
		VAL_UINT32_T fgResize:1;
		VAL_UINT32_T fgCrop:1;
	} PostProcCapability;              /* +12 */
} VDEC_DRV_PROPERTY_T, *P_VDEC_DRV_PROPERTY_T;

/* GET_TYPE_ERRORMB_MAP [P] */
typedef struct __VDEC_DRV_ERROR_MB_INFO_T {
	VAL_UINT32_T u4ErrorFrameNum;
	VAL_UINT32_T u4ErrorStartMB;
	VAL_UINT32_T u4ErrorMBCount;
} VDEC_DRV_ERROR_MB_INFO_T, *P_VDEC_DRV_ERROR_MB_INFO_T;

/* GET_TYPE_GET_COLOR_DESC [P] (MT8168 layout, 72 bytes) */
typedef struct __VDEC_DRV_COLORDESC_T {
	VAL_UINT32_T u4ColorPrimaries;
	VAL_UINT32_T u4TransformCharacter;
	VAL_UINT32_T u4MatrixCoeffs;
	VAL_UINT32_T u4DisplayPrimariesX[3];
	VAL_UINT32_T u4DisplayPrimariesY[3];
	VAL_UINT32_T u4WhitePointX;
	VAL_UINT32_T u4WhitePointY;
	VAL_UINT32_T u4MaxDisplayMasteringLuminance;
	VAL_UINT32_T u4MinDisplayMasteringLuminance;
	VAL_UINT32_T u4MaxContentLightLevel;
	VAL_UINT32_T u4MaxPicAverageLightLevel;
	VAL_UINT32_T u4IsHDR;
	VAL_UINT32_T u4FullRange;
} VDEC_DRV_COLORDESC_T;

/* Output-buffer callback. The codec calls it, through the libvcodecdrv
 * trampoline DrvGetOutputBufferCB, from inside eVDecDrvDecodeEx on the
 * caller's thread whenever it needs a new frame buffer. Arguments:
 *   hHandle  = VDEC_DRV_CALLBACK_T.u4hHandle (the user's cookie)
 *   ppFrame  = out: store a P_VDEC_DRV_FRAMEBUF_T, or NULL if none is free
 *   u4Flag   = 0 (always, from the trampoline)
 *   bFlag    = passed through (the OMX ignores it)
 *   pExtra   = passed through (the OMX ignores it)
 * Return 0 when a buffer is given, or (VAL_UINT32_T)-1 when none is.
 * The OMX blocks on a semaphore until an output buffer becomes free. [V] */
typedef VAL_UINT32_T (*PFN_VDEC_DRV_GET_OUTPUT_BUFFER)(
	VAL_HANDLE_T hHandle, P_VDEC_DRV_FRAMEBUF_T *ppFrame,
	VAL_UINT32_T u4Flag, VAL_BOOL_T bFlag, VAL_VOID_T *pExtra);

/* SET_TYPE_SET_CALLBACK, 16 bytes [V]. Mandatory: without it
 * eVDecDrvDecodeEx fails ("no output buffer callback function").
 * eVDecDrvSetParam keeps the caller's {handle, fn}, then overwrites this
 * struct in place with {vdec handle, DrvGetOutputBufferCB} before passing
 * it to the codec, so do not reuse the struct afterwards. */
typedef struct __VDEC_DRV_CALLBACK_T_ {
	VDEC_U64_UNION(VAL_HANDLE_T, u4hHandle);                          /* +0 [V] */
	VDEC_U64_UNION(PFN_VDEC_DRV_GET_OUTPUT_BUFFER, pfnGetOutputBuffer); /* +8 [V] */
} VDEC_DRV_CALLBACK_T, *P_VDEC_DRV_CALLBACK_T;

/* SET_TYPE_SET_FIXEDMAXOUTPUTBUFFER, 8 bytes [V] */
typedef struct _VDEC_DRV_FIXED_MAX_OUTPUT_BUFFER_T_ {
	VAL_UINT32_T u4MaxWidth;
	VAL_UINT32_T u4MaxHeight;
} VDEC_DRV_FIXED_MAX_OUTPUT_BUFFER_T, *P_VDEC_DRV_FIXED_MAX_OUTPUT_BUFFER_T;

/* SET_TYPE_SET_VIDEO_INFO [P] */
typedef struct __VDEC_DRV_SET_VIDEO_INFO_T {
	VAL_UINT32_T u4fps;
	VAL_UINT32_T u4Width;
	VAL_UINT32_T u4Height;
} VDEC_DRV_SET_VIDEO_INFO_T;

/* ------------------------------------------------------------------ */
/* Functions (libvcodecdrv.so)                                         */
/* ------------------------------------------------------------------ */

VDEC_DRV_MRESULT_T eVDecDrvQueryCapability(VDEC_DRV_QUERY_TYPE_T a_eType,
		VAL_VOID_T *a_pvInParam, VAL_VOID_T *a_pvOutParam);

/* Writes the handle (an 872-byte heap block) to *a_phHandle. */
VDEC_DRV_MRESULT_T eVDecDrvCreate(VAL_HANDLE_T *a_phHandle,
		VDEC_DRV_VIDEO_FORMAT_T a_eVideoFormat);
VDEC_DRV_MRESULT_T eVDecDrvRelease(VAL_HANDLE_T a_hHandle);

/* Parses SPS/PPS from a_prBitstream (VA + rBase.u4Size) and opens the
 * hardware. Returns FAIL if no SPS was found (the OMX retries with the
 * next input buffer), or FATAL. */
VDEC_DRV_MRESULT_T eVDecDrvInit(VAL_HANDLE_T a_hHandle,
		VDEC_DRV_RINGBUF_T *a_prBitstream, VDEC_DRV_SEQINFO_T *a_prSeqinfo);
VDEC_DRV_MRESULT_T eVDecDrvDeInit(VAL_HANDLE_T a_hHandle);

VDEC_DRV_MRESULT_T eVDecDrvGetParam(VAL_HANDLE_T a_hHandle,
		VDEC_DRV_GET_TYPE_T a_eType, VAL_VOID_T *a_pvInParam,
		VAL_VOID_T *a_pvOutParam);
VDEC_DRV_MRESULT_T eVDecDrvSetParam(VAL_HANDLE_T a_hHandle,
		VDEC_DRV_SET_TYPE_T a_eType, VAL_VOID_T *a_pvInParam,
		VAL_VOID_T *a_pvOutParam);

/* WARNING: in this build eVDecDrvDecode is an alias of a stub that returns
 * 0 and does nothing (same address as H264_GetEncParamGeneric). Use
 * eVDecDrvDecodeEx. */
VDEC_DRV_MRESULT_T eVDecDrvDecode(VAL_HANDLE_T a_hHandle,
		VDEC_DRV_RINGBUF_T *a_prBitstream, VDEC_DRV_FRAMEBUF_T *a_prFramebuf);

/* Synchronous decode of one access unit. a_prBitstream == NULL means
 * flush/EOS: the decoder outputs all pending frames, and
 * GET_FREE_FRAME_BUFFER then drains every buffer it holds. u4Flag and
 * pExtra are passed to the codec but ignored by H.264 (the OMX passes
 * 0, 0). */
VDEC_DRV_MRESULT_T eVDecDrvDecodeEx(VAL_HANDLE_T a_hHandle,
		VDEC_DRV_RINGBUF_T *a_prBitstream, VAL_UINT32_T u4Flag,
		VAL_VOID_T *pExtra);

/* ------------------------------------------------------------------ */
/* Layout checks                                                       */
/* ------------------------------------------------------------------ */
#if defined(__arm__) && !defined(__aarch64__)
#include <stddef.h>
#define VDEC_DRV_STATIC_ASSERT(c, m) typedef char vdec_drv_sa_##m[(c) ? 1 : -1]
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_MEM_ADDR_T) == 24, memaddr);
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_RINGBUF_T) == 56, ringbuf);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_RINGBUF_T, u4Timestamp) == 40, rb_ts);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_RINGBUF_T, u4InputFlag) == 48, rb_flag);
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_FRAMEBUF_T) == 136, framebuf);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_FRAMEBUF_T, u4FrameBufferStatus) == 88, fb_status);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_FRAMEBUF_T, rSecMemHandle) == 108, fb_sec);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_FRAMEBUF_T, rColorPriInfo) == 120, fb_color);
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_SEQINFO_T) == 40, seqinfo);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_SEQINFO_T, u4BitDepthLuma) == 28, seq_bd);
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_QUERY_VIDEO_FORMAT_T) == 44, qvf);
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_CALLBACK_T) == 16, cb);
VDEC_DRV_STATIC_ASSERT(offsetof(VDEC_DRV_CALLBACK_T, pfnGetOutputBuffer) == 8, cb_fn);
VDEC_DRV_STATIC_ASSERT(sizeof(VDEC_DRV_PICINFO_T) == 52, picinfo);
#undef VDEC_DRV_STATIC_ASSERT
#endif

/* ------------------------------------------------------------------ */
/* MTK block output format (H.264 on MT8163), for software detiling.   */
/* Verified from libMtkOmxVdecEx's MDP source config:                  */
/* DP_COLOR_420_BLKP (0x00CD004C), yPitch = W*32, uvPitch = W*16,      */
/* C plane at Y + W*H, where W = ALIGN(width,16), H = ALIGN(height,32). */
/* Y: 16x32-pixel tiles of 512 bytes (16 bytes per row), tiles in      */
/* raster order. C: interleaved CbCr (NV12 order) in 16x16-byte tiles  */
/* of 256 bytes, same ordering. This is the same as V4L2 MM21.         */
/* ------------------------------------------------------------------ */
#define VDEC_MTK_BLK_ALIGN_W(w)   (((w) + 15u) & ~15u)
#define VDEC_MTK_BLK_ALIGN_H(h)   (((h) + 31u) & ~31u)
#define VDEC_MTK_BLK_Y_SIZE(w, h) (VDEC_MTK_BLK_ALIGN_W(w) * VDEC_MTK_BLK_ALIGN_H(h))
#define VDEC_MTK_BLK_FRAME_SIZE(w, h) (VDEC_MTK_BLK_Y_SIZE(w, h) * 3u / 2u)
/* byte offset of luma (x,y) inside the Y plane; W = aligned width */
#define VDEC_MTK_BLK_Y_OFF(x, y, W) \
	(((y) >> 5) * ((W) * 32u) + ((x) >> 4) * 512u + ((y) & 31u) * 16u + ((x) & 15u))
/* byte offset of chroma byte (bx = 2*cx + {0:Cb,1:Cr}, cy) inside C plane */
#define VDEC_MTK_BLK_C_OFF(bx, cy, W) \
	(((cy) >> 4) * ((W) * 16u) + ((bx) >> 4) * 256u + ((cy) & 15u) * 16u + ((bx) & 15u))

#ifdef __cplusplus
}
#endif

#endif /* _VDEC_DRV_IF_PUBLIC_H_ */
