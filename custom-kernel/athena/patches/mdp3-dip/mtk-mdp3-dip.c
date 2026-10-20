// SPDX-License-Identifier: GPL-2.0
/*
 * mtk-mdp3-dip.c: the MT8183's ISP pass 2 (DIP) as a V4L2 memory-to-memory device.
 *
 * A packed 10-bit Bayer picture goes in (MediaTek's packing: four pixels in five bytes, LSB first,
 * as the 2nd gen Show 8's camera hands it out) and comes out developed by the ISP (demosaic, color
 * conversion) and scaled by the MDP behind it, as YUYV or NV12 of any size up to the input's.
 *
 * Each picture is a frame for the SCP's DIP service (linux-firmware's mediatek/mt8183/scp.img): the
 * frame says where the picture is and where it goes, the SCP writes the DIP's command queue and tile
 * programs and the MDP's config into a working buffer, and the MDP3 driver's GCE path runs them
 * (mtk-mdp3-comp.c's IMGI component, patches/mdp3-mt8183-isp.py). This is the core of the ChromeOS
 * 5.10 kernel's mtk-isp DIP driver (mtk_dip-sys.c) without its media graph: one RAW input, one MDP
 * output. The picture is developed with the tuning a context's controls set (dip_fill_tuning): black
 * level, white balance gains, a color matrix and a gamma curve, in the ISP's own blocks (OBC2, PGN,
 * G2G, GGM), so a caller levels it as it would on the cores (echod's hardware/camera).
 */

#include <linux/clk.h>
#include <linux/dma-mapping.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/of_platform.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/remoteproc/mtk_scp.h>
#include <linux/workqueue.h>
#include <media/v4l2-ctrls.h>
#include <media/v4l2-device.h>
#include <media/v4l2-ioctl.h>
#include <media/v4l2-mem2mem.h>
#include <media/videobuf2-dma-contig.h>
#include "mtk-img-ipi.h"
#include "mtk-mdp3-cmdq.h"
#include "mtk-mdp3-core.h"

#define DIP_NAME		"mtk-dip"

/* The working buffer, as ChromeOS lays it out: sub-frame data, tuning, the config, the frame */
#define DIP_SUBFRM_SZ		(20 * 1024)
#define DIP_TUNING_OFS		DIP_SUBFRM_SZ
#define DIP_TUNING_SZ		(32 * 1024)
#define DIP_CONFIG_OFS		(DIP_TUNING_OFS + DIP_TUNING_SZ)
#define DIP_CONFIG_SZ		PAGE_ALIGN(sizeof(struct img_config))
#define DIP_FRAME_OFS		(DIP_CONFIG_OFS + DIP_CONFIG_SZ)
#define DIP_WORK_SZ		PAGE_ALIGN(DIP_FRAME_OFS + 4096)

#define DIP_TIMEOUT_MS		2000
#define DIP_STREAM_ISP_IC	5	/* the firmware's stream type for a capture */
#define DIP_YCBCR_JPEG		2	/* MDP_YCBCR_PROFILE_JPEG: BT.601, full range */

/* The firmware's enum mdp_color */
#define DIP_COLOR(plane, hf, vf, bits, group, swap, id) \
	(((plane) << 21) | ((hf) << 19) | ((vf) << 18) | ((bits) << 8) | ((group) << 6) | \
	 ((swap) << 5) | (id))
#define DIP_BAYER10(hf, vf)	DIP_COLOR(1, hf, vf, 10, 2, 0, 20)
#define DIP_YUYV		DIP_COLOR(1, 1, 0, 16, 1, 0, 5)
#define DIP_NV12		DIP_COLOR(2, 1, 1, 8, 1, 0, 12)

#define DIP_MAX_W		4224
#define DIP_MAX_H		3136
#define DIP_MIN_W		64
#define DIP_MIN_H		64

/*
 * The DIP's power-on settings: its CQ threads enabled with their descriptor sizes, interrupts off,
 * the DMAs' FIFO thresholds, as MediaTek's own ISP 5.0 driver writes them when it opens the DIP
 * (cameraisp/dip/isp_50, DIP_INIT_ARY). On the Chromebooks the SCP does this; here it cannot reach
 * the DIP's registers (the same lock that stops the codecs), so the AP writes them before each job.
 */
static const struct { u16 ofs; u32 val; } dip_init[] = {
	{ 0x00a0, 0x1 }, { 0x0020, 0x0 }, { 0x0024, 0x0 }, { 0x0028, 0x0 },
	{ 0x002c, 0x0 }, { 0x0204, 0x11 }, { 0x0210, 0x11 }, { 0x021c, 0x11 },
	{ 0x0228, 0x11 }, { 0x0234, 0x11 }, { 0x0240, 0x11 }, { 0x024c, 0x11 },
	{ 0x0258, 0x11 }, { 0x0264, 0x11 }, { 0x0270, 0x11 }, { 0x027c, 0x11 },
	{ 0x0288, 0x11 }, { 0x0294, 0x11 }, { 0x02a0, 0x11 }, { 0x02ac, 0x11 },
	{ 0x02b8, 0x11 }, { 0x02c4, 0x11 }, { 0x02d0, 0x11 }, { 0x02dc, 0x11 },
	{ 0x020c, 0x308 }, { 0x0218, 0x308 }, { 0x0224, 0x308 }, { 0x0230, 0x308 },
	{ 0x023c, 0x308 }, { 0x0248, 0x308 }, { 0x0254, 0x308 }, { 0x0260, 0x308 },
	{ 0x026c, 0x308 }, { 0x0278, 0x308 }, { 0x0284, 0x308 }, { 0x0290, 0x308 },
	{ 0x029c, 0x308 }, { 0x02a8, 0x308 }, { 0x02b4, 0x308 }, { 0x02c0, 0x308 },
	{ 0x02cc, 0x308 }, { 0x02d8, 0x308 }, { 0x02e4, 0x308 }, { 0x034c, 0x80000040 },
	{ 0x0350, 0x400040 }, { 0x0354, 0x150015 }, { 0x037c, 0x80000040 }, { 0x0380, 0x400040 },
	{ 0x0384, 0x150015 }, { 0x03ac, 0x80000040 }, { 0x03b0, 0x400040 }, { 0x03b4, 0x150015 },
	{ 0x03dc, 0x80000020 }, { 0x03e0, 0x200020 }, { 0x03e4, 0xa000a }, { 0x040c, 0x80000020 },
	{ 0x0410, 0x200020 }, { 0x0414, 0xa000a }, { 0x043c, 0x80000040 }, { 0x0440, 0x400040 },
	{ 0x0444, 0x150015 }, { 0x046c, 0x80000040 }, { 0x0470, 0x400040 }, { 0x0474, 0x150015 },
	{ 0x051c, 0x80000080 }, { 0x0520, 0x800080 }, { 0x0524, 0x2a002a }, { 0x054c, 0x80000040 },
	{ 0x0550, 0x400040 }, { 0x0554, 0x150015 }, { 0x057c, 0x80000040 }, { 0x0580, 0x400040 },
	{ 0x0584, 0x150015 }, { 0x05ac, 0x80000080 }, { 0x05b0, 0x800080 }, { 0x05b4, 0x2a002a },
	{ 0x05dc, 0x80000040 }, { 0x05e0, 0x400040 }, { 0x05e4, 0x150015 }, { 0x060c, 0x80000040 },
	{ 0x0610, 0x400040 }, { 0x0614, 0x150015 }, { 0x063c, 0x80000020 }, { 0x0640, 0x200020 },
	{ 0x0644, 0xa000a }, { 0x066c, 0x80000020 }, { 0x0670, 0x200020 }, { 0x0674, 0xa000a },
	{ 0x069c, 0x80000020 }, { 0x06a0, 0x200020 }, { 0x06a4, 0xa000a }, { 0x06cc, 0x80000020 },
	{ 0x06d0, 0x200020 }, { 0x06d4, 0xa000a }, { 0x07ec, 0x80000040 }, { 0x07f0, 0x400040 },
	{ 0x07f4, 0x400040 }, { 0x081c, 0x80000040 }, { 0x0820, 0x400040 }, { 0x0824, 0x400040 },
	{ 0x084c, 0x80000040 }, { 0x0850, 0x400040 }, { 0x0854, 0x400040 }, { 0x087c, 0x80000040 },
	{ 0x0880, 0x400040 }, { 0x0884, 0x400040 }, { 0x08ac, 0x80000040 }, { 0x08b0, 0x400040 },
	{ 0x08b4, 0x400040 }, { 0x08dc, 0x80000040 }, { 0x08e0, 0x400040 }, { 0x08e4, 0x400040 },
	{ 0x090c, 0x80000040 }, { 0x0910, 0x400040 }, { 0x0914, 0x400040 }, { 0x093c, 0x80000040 },
	{ 0x0940, 0x400040 }, { 0x0944, 0x400040 },
};

struct dip_fmt {
	u32 fourcc;
	u32 color;
	const char *desc;
};

/* MediaTek-packed, under the fourccs athena's camera uses for them */
static const struct dip_fmt dip_raw_fmts[] = {
	{ V4L2_PIX_FMT_SBGGR10P, DIP_BAYER10(1, 1), "10-bit BGGR (MediaTek packing)" },
	{ V4L2_PIX_FMT_SGBRG10P, DIP_BAYER10(1, 0), "10-bit GBRG (MediaTek packing)" },
	{ V4L2_PIX_FMT_SGRBG10P, DIP_BAYER10(0, 1), "10-bit GRBG (MediaTek packing)" },
	{ V4L2_PIX_FMT_SRGGB10P, DIP_BAYER10(0, 0), "10-bit RGGB (MediaTek packing)" },
};

static const struct dip_fmt dip_out_fmts[] = {
	{ V4L2_PIX_FMT_YUYV, DIP_YUYV, NULL },
	{ V4L2_PIX_FMT_NV12, DIP_NV12, NULL },
	/* NV12 with its chroma in a buffer of its own: what the H.264 encoder takes (NM12), so a
	 * developed picture goes to it as two dmabufs, never through the cores */
	{ V4L2_PIX_FMT_NV12M, DIP_NV12, NULL },
};

struct dip_dev {
	struct device *dev;
	struct v4l2_device v4l2_dev;
	struct video_device vfd;
	struct v4l2_m2m_dev *m2m;
	struct mutex lock;		/* the video device's and the queues' */
	struct mtk_scp *scp;
	struct mdp_dev *mdp;
	struct clk_bulk_data clks[2];

	void __iomem *regs;		/* the DIP's registers, for its power-on settings */
	void *work;			/* the working buffer: SCP DRAM, mapped for the DIP */
	dma_addr_t work_scp;
	dma_addr_t work_isp;

	struct img_ipi_frameparam frame;
	struct workqueue_struct *wq;
	struct work_struct run;
	struct delayed_work timeout;
	spinlock_t job_lock;		/* job_ctx */
	struct dip_ctx *job_ctx;	/* the context whose job is out, if any */
	u32 job_no;
	int inited;			/* contexts streaming, for the SCP's INIT/DEINIT */
};

struct dip_ctx {
	struct v4l2_fh fh;
	struct dip_dev *dip;
	struct v4l2_pix_format_mplane src, dst;
	const struct dip_fmt *src_fmt, *dst_fmt;
	bool inited;
	struct v4l2_ctrl_handler hdl;
	struct v4l2_ctrl *black, *red, *blue, *dgain, *gamma, *ccm;
};

/*
 * The tuning: the DIP's register image as the SCP's firmware takes it (offset = register - 0x15022000;
 * custom-kernel/athena/research/dip-tuning.md). With tuning_data.present set, the firmware loads the
 * blocks it is told to enable from it and builds its command queue in it at 0x5580; the blocks it
 * owns (demosaic) take their values from it too, so their defaults (from the firmware) go in as well.
 */
#define DIP_CTL_YUV_EN		0x0004
#define DIP_CTL_RGB_EN		0x000c
#define DIP_CTL_FMT_SEL		0x0014
#define DIP_RGB_PGN		BIT(2)
#define DIP_RGB_G2G		BIT(6)
#define DIP_RGB_GGM		BIT(7)
#define DIP_RGB_OBC2		BIT(11)
#define DIP_YUV_G2C		BIT(3)
#define DIP_OBC2		0x11c0	/* OFFST0..3, GAIN0..3: B, Gr, Gb, R */
#define DIP_PGN			0x1440	/* SATU_1/2, GAIN_1/2, OFST_1/2 */
#define DIP_UDM			0x1540
#define DIP_G2G			0x2080	/* CNV_1..6, CTRL */
#define DIP_GGM_LUT		0x2180	/* 192 x {10, 10, 10} */
#define DIP_GGM_CTRL		0x2480
#define DIP_G2C			0x3240
#define DIP_GGM_POINTS		192

/* The controls of our own: the gamma curve and the color matrix */
#define DIP_CID_GAMMA_CURVE	(V4L2_CID_USER_BASE + 0x2100)
#define DIP_CID_CCM		(V4L2_CID_USER_BASE + 0x2101)

static const u32 dip_udm_default[23] = {
	0x0002f004, 0x1430053f, 0x00500500, 0x052a30dc, 0x02a9124f, 0x0039b4a0,
	0x0a529400, 0x81028000, 0x00000050, 0x84210000, 0x46ff1eff, 0x001eff55,
	0x00942064, 0x00000000, 0x0000000f, 0x000000ff, 0x00000418, 0x00000019,
	0x00000035, 0x00000000, 0x00000000, 0x00000035, 0x00000410,
};

/* RGB to YCbCr, BT.601 full range (the output is MDP_YCBCR_PROFILE_JPEG) */
static const u32 dip_g2c_bt601_full[13] = {
	0x012d0099, 0x0000003a, 0x075607aa, 0x00000100, 0x072a0100, 0x000007d6,
	0x0118000e, 0x0074b740, 0x00000133, 0x079f0a5a, 0x00000000, 0x03f70080,
	0x1ce539ce,
};

/*
 * The default curve: out = 1023 * (x / 4095)^(1/2.2) at the firmware's input points (steps of 8 for 64
 * entries, of 16 for 32, of 32 for 96, on a 12-bit scale)
 */
static const u16 dip_gamma_22[DIP_GGM_POINTS] = {
	   0,   60,   82,   99,  113,  125,  136,  145,  155,  163,  171,  179,
	 186,  193,  199,  206,  212,  218,  223,  229,  234,  240,  245,  250,
	 255,  259,  264,  269,  273,  277,  282,  286,  290,  294,  298,  302,
	 306,  310,  314,  317,  321,  325,  328,  332,  335,  339,  342,  346,
	 349,  352,  355,  359,  362,  365,  368,  371,  374,  377,  380,  383,
	 386,  389,  392,  395,  398,  403,  409,  414,  419,  425,  430,  435,
	 440,  445,  450,  455,  460,  464,  469,  473,  478,  483,  487,  491,
	 496,  500,  504,  509,  513,  517,  521,  525,  529,  533,  537,  541,
	 545,  553,  560,  567,  575,  582,  589,  596,  603,  610,  617,  623,
	 630,  636,  643,  649,  655,  661,  667,  673,  679,  685,  691,  697,
	 703,  708,  714,  720,  725,  730,  736,  741,  747,  752,  757,  762,
	 767,  773,  778,  783,  788,  793,  798,  802,  807,  812,  817,  822,
	 826,  831,  836,  840,  845,  849,  854,  858,  863,  867,  872,  876,
	 881,  885,  889,  893,  898,  902,  906,  910,  915,  919,  923,  927,
	 931,  935,  939,  943,  947,  951,  955,  959,  963,  967,  971,  975,
	 978,  982,  986,  990,  994,  997, 1001, 1005, 1008, 1012, 1016, 1019,
};

/*
 * The default color matrix (1/512, rows R, G, B out). The DIP's demosaic, as the firmware's defaults
 * leave it (dip_udm_default), gives the colors back at about 0.4 of their distance from gray, evenly
 * across hues: measured on an OV13855 frame, its R/G and B/G against the raw cells' after the same
 * white balance. A saturation of 2.5 about BT.601's luma (M = 2.5 I - 1.5 [Y; Y; Y]) puts them back
 * within a few percent and leaves the luma as it was.
 */
static const s32 dip_ccm_default[9] = {
	1050, -451,  -88,
	-230,  829,  -88,
	-230, -451, 1192,
};

/*
 * GGM's control word (bit 0 off: the curve applies; the firmware's 0x100307ff leaves it linear) and the
 * scale OBC2's offset and gain work on (bits above the sensor's 10: 2, a 12-bit pipeline, measured:
 * the OV13855's floor of 64 comes off at 256), for trying others without a rebuild. The pipeline's
 * scale is also why the digital gain wants about a quarter of the stretch the same frame takes on
 * the cores (2048, 4x, matched a top-percentile stretch of 12.4x).
 */
static uint dip_ggm_ctrl = 0x100307fe;
module_param(dip_ggm_ctrl, uint, 0644);
static uint dip_obc_shift = 2;
module_param(dip_obc_shift, uint, 0644);

static inline void dip_tw(void *t, u32 ofs, u32 val)
{
	*(u32 *)(t + ofs) = val;
}

static inline u32 dip_s13(int v)
{
	return (u32)v & 0x1fff;
}

/* The tuning a context's controls say, into the zeroed buffer t */
static void dip_fill_tuning(void *t, struct dip_ctx *ctx)
{
	unsigned int blc = ctx->black->val;
	unsigned int full = 1023U << dip_obc_shift, b = blc << dip_obc_shift;
	u32 ofst = dip_s13(-(int)b);			/* on the pipeline's scale */
	/* stretch back to full scale, and the digital gain on top (the frame's level: white at full) */
	u32 gain = min_t(u32, DIV_ROUND_CLOSEST(ctx->dgain->val * full, full - b), 0x1fff);
	u32 gr = ctx->red->val, gb = ctx->blue->val, gg = 512;
	const u16 *curve = ctx->gamma->p_cur.p_u16;
	const s32 *m = ctx->ccm->p_cur.p_s32;
	int i;

	for (i = 0; i < ARRAY_SIZE(dip_udm_default); i++)
		dip_tw(t, DIP_UDM + 4 * i, dip_udm_default[i]);
	for (i = 0; i < ARRAY_SIZE(dip_g2c_bt601_full); i++)
		dip_tw(t, DIP_G2C + 4 * i, dip_g2c_bt601_full[i]);
	dip_tw(t, 0x3940, 0xad);

	dip_tw(t, DIP_CTL_YUV_EN, DIP_YUV_G2C);
	dip_tw(t, DIP_CTL_RGB_EN, DIP_RGB_OBC2 | DIP_RGB_PGN | DIP_RGB_G2G | DIP_RGB_GGM);
	dip_tw(t, DIP_CTL_FMT_SEL, BIT(31));

	for (i = 0; i < 4; i++) {
		dip_tw(t, DIP_OBC2 + 4 * i, ofst);
		dip_tw(t, DIP_OBC2 + 0x10 + 4 * i, gain);
	}

	dip_tw(t, DIP_PGN + 0x00, 0x0fff0fff);		/* saturation B, Gb */
	dip_tw(t, DIP_PGN + 0x04, 0x0fff0fff);		/* Gr, R */
	dip_tw(t, DIP_PGN + 0x08, gb | gg << 16);	/* gains B, Gb */
	dip_tw(t, DIP_PGN + 0x0c, gg | gr << 16);	/* Gr, R */

	dip_tw(t, DIP_G2G + 0x00, dip_s13(m[0]) | dip_s13(m[1]) << 16);
	dip_tw(t, DIP_G2G + 0x04, dip_s13(m[2]));
	dip_tw(t, DIP_G2G + 0x08, dip_s13(m[3]) | dip_s13(m[4]) << 16);
	dip_tw(t, DIP_G2G + 0x0c, dip_s13(m[5]));
	dip_tw(t, DIP_G2G + 0x10, dip_s13(m[6]) | dip_s13(m[7]) << 16);
	dip_tw(t, DIP_G2G + 0x14, dip_s13(m[8]));
	dip_tw(t, DIP_G2G + 0x18, 9);			/* coefficients in 1/512 */

	for (i = 0; i < DIP_GGM_POINTS; i++) {
		u32 v = min_t(u32, curve[i], 1023);

		dip_tw(t, DIP_GGM_LUT + 4 * i, v | v << 10 | v << 20);
	}
	dip_tw(t, DIP_GGM_CTRL, dip_ggm_ctrl);
}

static const struct v4l2_ctrl_config dip_ctrl_gamma = {
	.id = DIP_CID_GAMMA_CURVE,
	.name = "Gamma curve",
	.type = V4L2_CTRL_TYPE_U16,
	.min = 0, .max = 1023, .step = 1, .def = 0,
	.dims = { DIP_GGM_POINTS },
};

static const struct v4l2_ctrl_config dip_ctrl_ccm = {
	.id = DIP_CID_CCM,
	.name = "Color matrix",
	.type = V4L2_CTRL_TYPE_INTEGER,
	.min = -4096, .max = 4095, .step = 1, .def = 0,
	.dims = { 9 },	/* rows R, G, B out; 512 = 1.0 */
};

/* A context's controls, with the default curve (1/2.2) and matrix (dip_ccm_default) */
static int dip_ctrls_init(struct dip_ctx *ctx)
{
	struct v4l2_ctrl_handler *h = &ctx->hdl;
	int i;

	v4l2_ctrl_handler_init(h, 6);
	/* the black level at 10 bits; the gains in 1/512 (512 = 1.0) */
	ctx->black = v4l2_ctrl_new_std(h, NULL, V4L2_CID_BLACK_LEVEL, 0, 255, 1, 64);
	ctx->red = v4l2_ctrl_new_std(h, NULL, V4L2_CID_RED_BALANCE, 0, 4095, 1, 512);
	ctx->blue = v4l2_ctrl_new_std(h, NULL, V4L2_CID_BLUE_BALANCE, 0, 4095, 1, 512);
	/* the exposure stretch, in OBC2's gain (1/512, up to 16x) */
	ctx->dgain = v4l2_ctrl_new_std(h, NULL, V4L2_CID_DIGITAL_GAIN, 0, 8191, 1, 512);
	ctx->gamma = v4l2_ctrl_new_custom(h, &dip_ctrl_gamma, NULL);
	ctx->ccm = v4l2_ctrl_new_custom(h, &dip_ctrl_ccm, NULL);
	if (h->error) {
		int err = h->error;

		v4l2_ctrl_handler_free(h);
		return err;
	}
	for (i = 0; i < DIP_GGM_POINTS; i++)
		ctx->gamma->p_cur.p_u16[i] = ctx->gamma->p_new.p_u16[i] = dip_gamma_22[i];
	for (i = 0; i < 9; i++)
		ctx->ccm->p_cur.p_s32[i] = ctx->ccm->p_new.p_s32[i] = dip_ccm_default[i];
	ctx->fh.ctrl_handler = h;
	return 0;
}

static struct dip_ctx *file_to_ctx(struct file *file)
{
	return container_of(file_to_v4l2_fh(file), struct dip_ctx, fh);
}

static const struct dip_fmt *dip_find(const struct dip_fmt *f, int n, u32 fourcc)
{
	int i;

	for (i = 0; i < n; i++)
		if (f[i].fourcc == fourcc)
			return &f[i];
	return NULL;
}

static void dip_fill_src(struct v4l2_pix_format_mplane *p)
{
	p->width = clamp(ALIGN(p->width, 4), DIP_MIN_W, DIP_MAX_W);
	p->height = clamp(ALIGN(p->height, 2), DIP_MIN_H, DIP_MAX_H);
	p->field = V4L2_FIELD_NONE;
	p->num_planes = 1;
	p->plane_fmt[0].bytesperline = p->width * 10 / 8;
	p->plane_fmt[0].sizeimage = p->plane_fmt[0].bytesperline * p->height;
	p->colorspace = V4L2_COLORSPACE_RAW;
}

static void dip_fill_dst(struct v4l2_pix_format_mplane *p)
{
	p->width = clamp(ALIGN(p->width, 16), DIP_MIN_W, DIP_MAX_W);
	p->height = clamp(ALIGN(p->height, 2), DIP_MIN_H, DIP_MAX_H);
	p->field = V4L2_FIELD_NONE;
	p->num_planes = 1;
	if (p->pixelformat == V4L2_PIX_FMT_NV12) {
		p->plane_fmt[0].bytesperline = p->width;
		p->plane_fmt[0].sizeimage = p->width * p->height * 3 / 2;
	} else if (p->pixelformat == V4L2_PIX_FMT_NV12M) {
		/*
		 * the planes as large as the H.264 encoder wants them: it pads the height to 32 rows and
		 * then 32 more (measured: 768 rows take 800, 784 take 832)
		 */
		p->num_planes = 2;
		p->plane_fmt[0].bytesperline = p->width;
		p->plane_fmt[0].sizeimage = p->width * (ALIGN(p->height, 32) + 32);
		p->plane_fmt[1].bytesperline = p->width;
		p->plane_fmt[1].sizeimage = p->width * (ALIGN(p->height, 32) + 32) / 2;
	} else {
		p->plane_fmt[0].bytesperline = p->width * 2;
		p->plane_fmt[0].sizeimage = p->width * p->height * 2;
	}
	p->colorspace = V4L2_COLORSPACE_JPEG;
	p->ycbcr_enc = V4L2_YCBCR_ENC_601;
	p->quantization = V4L2_QUANTIZATION_FULL_RANGE;
}

/* The job: the SCP composes it, then the MDP runs it */

static void dip_finish(struct dip_dev *dip, enum vb2_buffer_state state)
{
	struct dip_ctx *ctx;
	struct vb2_v4l2_buffer *src, *dst;
	unsigned long flags;

	spin_lock_irqsave(&dip->job_lock, flags);
	ctx = dip->job_ctx;
	dip->job_ctx = NULL;
	spin_unlock_irqrestore(&dip->job_lock, flags);
	if (!ctx)
		return;

	clk_bulk_disable_unprepare(ARRAY_SIZE(dip->clks), dip->clks);
	pm_runtime_put_autosuspend(dip->dev);
	src = v4l2_m2m_src_buf_remove(ctx->fh.m2m_ctx);
	dst = v4l2_m2m_dst_buf_remove(ctx->fh.m2m_ctx);
	if (src && dst) {
		v4l2_m2m_buf_copy_metadata(src, dst);
		v4l2_m2m_buf_done(src, state);
		v4l2_m2m_buf_done(dst, state);
	}
	v4l2_m2m_job_finish(dip->m2m, ctx->fh.m2m_ctx);
}

static struct dip_dev *dip_the;	/* the MDP's callback carries no context */

static void dip_mdp_done(struct cmdq_cb_data data)
{
	struct dip_dev *dip = dip_the;

	cancel_delayed_work(&dip->timeout);
	if (data.sta < 0)
		dev_err(dip->dev, "frame %u: the ISP/MDP did not finish (%d)\n", dip->job_no, data.sta);
	dip_finish(dip, data.sta < 0 ? VB2_BUF_STATE_ERROR : VB2_BUF_STATE_DONE);
}

static void dip_run(struct work_struct *work)
{
	struct dip_dev *dip = container_of(work, struct dip_dev, run);
	struct mdp_cmdq_param param = {
		.config = dip->work + DIP_CONFIG_OFS,
		.param = &dip->frame,
		.cmdq_cb = dip_mdp_done,
	};
	int ret;

	ret = mdp_cmdq_send(dip->mdp, &param);
	if (ret) {
		dev_err(dip->dev, "frame %u: the MDP refused it (%d)\n", dip->job_no, ret);
		cancel_delayed_work(&dip->timeout);
		dip_finish(dip, VB2_BUF_STATE_ERROR);
	}
}

static void dip_timeout(struct work_struct *work)
{
	struct dip_dev *dip = container_of(work, struct dip_dev, timeout.work);

	dev_err(dip->dev, "frame %u timed out\n", dip->job_no);
	mdp_cmdq_abort(dip->mdp);	/* the callback releases the job's buffers as failed */
	dip_finish(dip, VB2_BUF_STATE_ERROR);
}

static void dip_scp_handler(void *data, unsigned int len, void *priv)
{
	struct dip_dev *dip = priv;
	const struct img_ipi_param *ipi = data;
	const struct img_ipi_frameparam *back = dip->work + DIP_FRAME_OFS;

	if (len < sizeof(*ipi) || ipi->usage != IMG_IPI_FRAME) {
		if (len < sizeof(*ipi) || ipi->usage != IMG_IPI_INIT)
			dev_warn(dip->dev, "unexpected message from the SCP: %u bytes, usage %u\n",
				 len, len >= sizeof(u32) ? ipi->usage : 0);
		return;
	}
	if (ipi->frm_param.handle != dip->job_no) {
		dev_warn(dip->dev, "the SCP gave back frame %llu, not %u\n",
			 ipi->frm_param.handle, dip->job_no);
		return;
	}
	dip->frame = *back;
	if (dip->frame.state) {
		dev_err(dip->dev, "frame %u: the SCP could not compose it (%d)\n",
			dip->job_no, (int)dip->frame.state);
		cancel_delayed_work(&dip->timeout);
		dip_finish(dip, VB2_BUF_STATE_ERROR);
		return;
	}
	queue_work(dip->wq, &dip->run);
}

static void dip_device_run(void *priv)
{
	struct dip_ctx *ctx = priv;
	struct dip_dev *dip = ctx->dip;
	struct img_ipi_frameparam *f = &dip->frame;
	struct img_input *in = &f->inputs[0];
	struct img_output *out = &f->outputs[0];
	struct vb2_v4l2_buffer *src = v4l2_m2m_next_src_buf(ctx->fh.m2m_ctx);
	struct vb2_v4l2_buffer *dst = v4l2_m2m_next_dst_buf(ctx->fh.m2m_ctx);
	dma_addr_t src_iova = vb2_dma_contig_plane_dma_addr(&src->vb2_buf, 0);
	dma_addr_t dst_iova = vb2_dma_contig_plane_dma_addr(&dst->vb2_buf, 0);
	struct img_ipi_param ipi = {};
	unsigned long flags;
	int ret, i;

	ret = pm_runtime_resume_and_get(dip->dev);
	if (!ret)
		ret = clk_bulk_prepare_enable(ARRAY_SIZE(dip->clks), dip->clks);
	spin_lock_irqsave(&dip->job_lock, flags);
	dip->job_ctx = ctx;
	spin_unlock_irqrestore(&dip->job_lock, flags);
	if (ret) {
		dev_err(dip->dev, "the DIP did not power up (%d)\n", ret);
		spin_lock_irqsave(&dip->job_lock, flags);
		dip->job_ctx = NULL;
		spin_unlock_irqrestore(&dip->job_lock, flags);
		v4l2_m2m_buf_done(v4l2_m2m_src_buf_remove(ctx->fh.m2m_ctx), VB2_BUF_STATE_ERROR);
		v4l2_m2m_buf_done(v4l2_m2m_dst_buf_remove(ctx->fh.m2m_ctx), VB2_BUF_STATE_ERROR);
		v4l2_m2m_job_finish(dip->m2m, ctx->fh.m2m_ctx);
		return;
	}

	for (i = 0; i < ARRAY_SIZE(dip_init); i++)
		writel(dip_init[i].val, dip->regs + dip_init[i].ofs);

	memset(f, 0, sizeof(*f));
	f->index = ++dip->job_no;
	f->frame_no = dip->job_no;
	f->type = DIP_STREAM_ISP_IC;
	f->num_inputs = 1;
	f->num_outputs = 1;

	in->buffer.format.colorformat = ctx->src_fmt->color;
	in->buffer.format.width = ctx->src.width;
	in->buffer.format.height = ctx->src.height;
	in->buffer.format.plane_fmt[0].stride = ctx->src.plane_fmt[0].bytesperline;
	in->buffer.format.plane_fmt[0].size = ctx->src.plane_fmt[0].sizeimage;
	in->buffer.iova[0] = src_iova;

	out->buffer.format.colorformat = ctx->dst_fmt->color;
	out->buffer.format.width = ctx->dst.width;
	out->buffer.format.height = ctx->dst.height;
	out->buffer.format.ycbcr_prof = DIP_YCBCR_JPEG;
	out->buffer.format.plane_fmt[0].stride = ctx->dst.plane_fmt[0].bytesperline;
	out->buffer.iova[0] = dst_iova;
	if (ctx->dst_fmt->fourcc == V4L2_PIX_FMT_NV12 || ctx->dst_fmt->fourcc == V4L2_PIX_FMT_NV12M) {
		u32 luma = ctx->dst.width * ctx->dst.height;

		out->buffer.format.plane_fmt[0].size = luma;
		out->buffer.format.plane_fmt[1].stride = ctx->dst.width;
		out->buffer.format.plane_fmt[1].size = luma / 2;
		out->buffer.iova[1] = ctx->dst.num_planes == 2 ?
			vb2_dma_contig_plane_dma_addr(&dst->vb2_buf, 1) : dst_iova + luma;
	} else {
		out->buffer.format.plane_fmt[0].size = ctx->dst.plane_fmt[0].sizeimage;
	}
	out->crop.width = ctx->src.width;
	out->crop.height = ctx->src.height;

	/* Sub-frames, tuning (the context's), config, and the frame itself, all in the working buffer */
	memset(dip->work, 0, DIP_FRAME_OFS);
	dip_fill_tuning(dip->work + DIP_TUNING_OFS, ctx);
	f->tuning_data.present = 1;
	f->subfrm_data.pa = dip->work_scp;
	f->subfrm_data.iova = dip->work_isp;
	f->tuning_data.pa = dip->work_scp + DIP_TUNING_OFS;
	f->tuning_data.iova = dip->work_isp + DIP_TUNING_OFS;
	f->config_data.pa = dip->work_scp + DIP_CONFIG_OFS;
	f->config_data.va = (unsigned long)(dip->work + DIP_CONFIG_OFS);
	f->self_data.pa = dip->work_scp + DIP_FRAME_OFS;
	f->self_data.va = (unsigned long)(dip->work + DIP_FRAME_OFS);
	memcpy(dip->work + DIP_FRAME_OFS, f, sizeof(*f));

	ipi.usage = IMG_IPI_FRAME;
	ipi.frm_param.handle = dip->job_no;
	ipi.frm_param.scp_addr = dip->work_scp + DIP_FRAME_OFS;
	schedule_delayed_work(&dip->timeout, msecs_to_jiffies(DIP_TIMEOUT_MS));
	ret = scp_ipi_send(dip->scp, SCP_IPI_DIP, &ipi, sizeof(ipi), 0);
	if (ret) {
		dev_err(dip->dev, "frame %u: the SCP did not take it (%d)\n", dip->job_no, ret);
		cancel_delayed_work(&dip->timeout);
		dip_finish(dip, VB2_BUF_STATE_ERROR);
	}
}

static const struct v4l2_m2m_ops dip_m2m_ops = {
	.device_run = dip_device_run,
};

/* SCP session: INIT while any context streams */

static int dip_scp_init(struct dip_dev *dip)
{
	struct img_ipi_param ipi = { .usage = IMG_IPI_INIT };
	int ret;

	if (dip->inited++)
		return 0;
	/*
	 * Powered while the SCP sets the DIP up: its firmware writes some of the DIP's registers, which
	 * the AP does for it (mtk_scp_mmproxy.c) only while the DIP is on; autosuspend keeps it on for
	 * what it writes just after answering.
	 */
	ret = pm_runtime_resume_and_get(dip->dev);
	if (ret) {
		dip->inited--;
		return ret;
	}
	ret = scp_ipi_send(dip->scp, SCP_IPI_DIP, &ipi, sizeof(ipi), 200);
	pm_runtime_put_autosuspend(dip->dev);
	if (ret) {
		dip->inited--;
		dev_err(dip->dev, "the SCP's DIP service did not start (%d)\n", ret);
	}
	return ret;
}

static void dip_scp_deinit(struct dip_dev *dip)
{
	struct img_ipi_param ipi = { .usage = IMG_IPI_DEINIT };

	if (--dip->inited)
		return;
	scp_ipi_send(dip->scp, SCP_IPI_DIP, &ipi, sizeof(ipi), 200);
}

/* Queues */

static int dip_queue_setup(struct vb2_queue *vq, unsigned int *nbuf, unsigned int *nplanes,
			   unsigned int sizes[], struct device *alloc[])
{
	struct dip_ctx *ctx = vb2_get_drv_priv(vq);
	const struct v4l2_pix_format_mplane *p = V4L2_TYPE_IS_OUTPUT(vq->type) ? &ctx->src : &ctx->dst;

	unsigned int i;

	if (*nplanes) {
		if (*nplanes != p->num_planes)
			return -EINVAL;
		for (i = 0; i < p->num_planes; i++)
			if (sizes[i] < p->plane_fmt[i].sizeimage)
				return -EINVAL;
		return 0;
	}
	*nplanes = p->num_planes;
	for (i = 0; i < p->num_planes; i++)
		sizes[i] = p->plane_fmt[i].sizeimage;
	return 0;
}

static int dip_buf_prepare(struct vb2_buffer *vb)
{
	struct dip_ctx *ctx = vb2_get_drv_priv(vb->vb2_queue);
	const struct v4l2_pix_format_mplane *p =
		V4L2_TYPE_IS_OUTPUT(vb->type) ? &ctx->src : &ctx->dst;

	unsigned int i;

	for (i = 0; i < p->num_planes; i++) {
		if (vb2_plane_size(vb, i) < p->plane_fmt[i].sizeimage)
			return -EINVAL;
		if (!V4L2_TYPE_IS_OUTPUT(vb->type))
			vb2_set_plane_payload(vb, i, p->plane_fmt[i].sizeimage);
	}
	return 0;
}

static void dip_buf_queue(struct vb2_buffer *vb)
{
	struct dip_ctx *ctx = vb2_get_drv_priv(vb->vb2_queue);

	v4l2_m2m_buf_queue(ctx->fh.m2m_ctx, to_vb2_v4l2_buffer(vb));
}

static int dip_start_streaming(struct vb2_queue *vq, unsigned int count)
{
	struct dip_ctx *ctx = vb2_get_drv_priv(vq);
	struct vb2_v4l2_buffer *vb;
	int ret = 0;

	if (!ctx->inited) {
		ret = dip_scp_init(ctx->dip);
		ctx->inited = !ret;
	}
	if (ret) {
		while ((vb = V4L2_TYPE_IS_OUTPUT(vq->type) ?
				v4l2_m2m_src_buf_remove(ctx->fh.m2m_ctx) :
				v4l2_m2m_dst_buf_remove(ctx->fh.m2m_ctx)))
			v4l2_m2m_buf_done(vb, VB2_BUF_STATE_QUEUED);
	}
	return ret;
}

static void dip_stop_streaming(struct vb2_queue *vq)
{
	struct dip_ctx *ctx = vb2_get_drv_priv(vq);
	struct vb2_v4l2_buffer *vb;

	while ((vb = V4L2_TYPE_IS_OUTPUT(vq->type) ?
			v4l2_m2m_src_buf_remove(ctx->fh.m2m_ctx) :
			v4l2_m2m_dst_buf_remove(ctx->fh.m2m_ctx)))
		v4l2_m2m_buf_done(vb, VB2_BUF_STATE_ERROR);
}

static const struct vb2_ops dip_vb2_ops = {
	.queue_setup = dip_queue_setup,
	.buf_prepare = dip_buf_prepare,
	.buf_queue = dip_buf_queue,
	.start_streaming = dip_start_streaming,
	.stop_streaming = dip_stop_streaming,
};

static int dip_queue_init(void *priv, struct vb2_queue *src, struct vb2_queue *dst)
{
	struct dip_ctx *ctx = priv;
	int ret;

	src->type = V4L2_BUF_TYPE_VIDEO_OUTPUT_MPLANE;
	src->io_modes = VB2_MMAP | VB2_DMABUF;
	src->drv_priv = ctx;
	src->buf_struct_size = sizeof(struct v4l2_m2m_buffer);
	src->ops = &dip_vb2_ops;
	src->mem_ops = &vb2_dma_contig_memops;
	src->timestamp_flags = V4L2_BUF_FLAG_TIMESTAMP_COPY;
	src->lock = &ctx->dip->lock;
	src->dev = ctx->dip->dev;
	ret = vb2_queue_init(src);
	if (ret)
		return ret;

	dst->type = V4L2_BUF_TYPE_VIDEO_CAPTURE_MPLANE;
	dst->io_modes = VB2_MMAP | VB2_DMABUF;
	dst->drv_priv = ctx;
	dst->buf_struct_size = sizeof(struct v4l2_m2m_buffer);
	dst->ops = &dip_vb2_ops;
	dst->mem_ops = &vb2_dma_contig_memops;
	dst->timestamp_flags = V4L2_BUF_FLAG_TIMESTAMP_COPY;
	dst->lock = &ctx->dip->lock;
	dst->dev = ctx->dip->dev;
	return vb2_queue_init(dst);
}

/* ioctls */

static int dip_querycap(struct file *file, void *fh, struct v4l2_capability *cap)
{
	strscpy(cap->driver, DIP_NAME, sizeof(cap->driver));
	strscpy(cap->card, "MT8183 ISP pass 2 (DIP)", sizeof(cap->card));
	return 0;
}

static int dip_enum_fmt_out(struct file *file, void *fh, struct v4l2_fmtdesc *f)
{
	if (f->index >= ARRAY_SIZE(dip_raw_fmts))
		return -EINVAL;
	f->pixelformat = dip_raw_fmts[f->index].fourcc;
	strscpy(f->description, dip_raw_fmts[f->index].desc, sizeof(f->description));
	return 0;
}

static int dip_enum_fmt_cap(struct file *file, void *fh, struct v4l2_fmtdesc *f)
{
	if (f->index >= ARRAY_SIZE(dip_out_fmts))
		return -EINVAL;
	f->pixelformat = dip_out_fmts[f->index].fourcc;
	return 0;
}

static int dip_g_fmt(struct file *file, void *fh, struct v4l2_format *f)
{
	struct dip_ctx *ctx = file_to_ctx(file);

	f->fmt.pix_mp = V4L2_TYPE_IS_OUTPUT(f->type) ? ctx->src : ctx->dst;
	return 0;
}

static int dip_try_fmt_out(struct file *file, void *fh, struct v4l2_format *f)
{
	if (!dip_find(dip_raw_fmts, ARRAY_SIZE(dip_raw_fmts), f->fmt.pix_mp.pixelformat))
		f->fmt.pix_mp.pixelformat = dip_raw_fmts[0].fourcc;
	dip_fill_src(&f->fmt.pix_mp);
	return 0;
}

static int dip_try_fmt_cap(struct file *file, void *fh, struct v4l2_format *f)
{
	if (!dip_find(dip_out_fmts, ARRAY_SIZE(dip_out_fmts), f->fmt.pix_mp.pixelformat))
		f->fmt.pix_mp.pixelformat = dip_out_fmts[0].fourcc;
	dip_fill_dst(&f->fmt.pix_mp);
	return 0;
}

static int dip_s_fmt_out(struct file *file, void *fh, struct v4l2_format *f)
{
	struct dip_ctx *ctx = file_to_ctx(file);

	if (vb2_is_busy(v4l2_m2m_get_vq(ctx->fh.m2m_ctx, f->type)))
		return -EBUSY;
	dip_try_fmt_out(file, fh, f);
	ctx->src = f->fmt.pix_mp;
	ctx->src_fmt = dip_find(dip_raw_fmts, ARRAY_SIZE(dip_raw_fmts), ctx->src.pixelformat);
	return 0;
}

static int dip_s_fmt_cap(struct file *file, void *fh, struct v4l2_format *f)
{
	struct dip_ctx *ctx = file_to_ctx(file);

	if (vb2_is_busy(v4l2_m2m_get_vq(ctx->fh.m2m_ctx, f->type)))
		return -EBUSY;
	dip_try_fmt_cap(file, fh, f);
	ctx->dst = f->fmt.pix_mp;
	ctx->dst_fmt = dip_find(dip_out_fmts, ARRAY_SIZE(dip_out_fmts), ctx->dst.pixelformat);
	return 0;
}

static const struct v4l2_ioctl_ops dip_ioctl_ops = {
	.vidioc_querycap = dip_querycap,
	.vidioc_enum_fmt_vid_out = dip_enum_fmt_out,
	.vidioc_enum_fmt_vid_cap = dip_enum_fmt_cap,
	.vidioc_g_fmt_vid_out_mplane = dip_g_fmt,
	.vidioc_g_fmt_vid_cap_mplane = dip_g_fmt,
	.vidioc_try_fmt_vid_out_mplane = dip_try_fmt_out,
	.vidioc_try_fmt_vid_cap_mplane = dip_try_fmt_cap,
	.vidioc_s_fmt_vid_out_mplane = dip_s_fmt_out,
	.vidioc_s_fmt_vid_cap_mplane = dip_s_fmt_cap,
	.vidioc_reqbufs = v4l2_m2m_ioctl_reqbufs,
	.vidioc_querybuf = v4l2_m2m_ioctl_querybuf,
	.vidioc_qbuf = v4l2_m2m_ioctl_qbuf,
	.vidioc_dqbuf = v4l2_m2m_ioctl_dqbuf,
	.vidioc_expbuf = v4l2_m2m_ioctl_expbuf,
	.vidioc_create_bufs = v4l2_m2m_ioctl_create_bufs,
	.vidioc_prepare_buf = v4l2_m2m_ioctl_prepare_buf,
	.vidioc_streamon = v4l2_m2m_ioctl_streamon,
	.vidioc_streamoff = v4l2_m2m_ioctl_streamoff,
};

static int dip_open(struct file *file)
{
	struct dip_dev *dip = video_drvdata(file);
	struct dip_ctx *ctx;
	int ret;

	ctx = kzalloc(sizeof(*ctx), GFP_KERNEL);
	if (!ctx)
		return -ENOMEM;
	ctx->dip = dip;
	v4l2_fh_init(&ctx->fh, video_devdata(file));
	ret = dip_ctrls_init(ctx);
	if (ret) {
		v4l2_fh_exit(&ctx->fh);
		kfree(ctx);
		return ret;
	}
	ctx->src.width = 2112;
	ctx->src.height = 1568;
	ctx->src.pixelformat = dip_raw_fmts[0].fourcc;
	dip_fill_src(&ctx->src);
	ctx->src_fmt = &dip_raw_fmts[0];
	ctx->dst.width = 2112;
	ctx->dst.height = 1568;
	ctx->dst.pixelformat = dip_out_fmts[0].fourcc;
	dip_fill_dst(&ctx->dst);
	ctx->dst_fmt = &dip_out_fmts[0];

	mutex_lock(&dip->lock);
	ctx->fh.m2m_ctx = v4l2_m2m_ctx_init(dip->m2m, ctx, dip_queue_init);
	mutex_unlock(&dip->lock);
	if (IS_ERR(ctx->fh.m2m_ctx)) {
		ret = PTR_ERR(ctx->fh.m2m_ctx);
		v4l2_ctrl_handler_free(&ctx->hdl);
		v4l2_fh_exit(&ctx->fh);
		kfree(ctx);
		return ret;
	}
	v4l2_fh_add(&ctx->fh, file);
	return 0;
}

static int dip_release(struct file *file)
{
	struct dip_ctx *ctx = file_to_ctx(file);
	struct dip_dev *dip = ctx->dip;

	mutex_lock(&dip->lock);
	v4l2_m2m_ctx_release(ctx->fh.m2m_ctx);
	if (ctx->inited)
		dip_scp_deinit(dip);
	mutex_unlock(&dip->lock);
	v4l2_fh_del(&ctx->fh, file);
	v4l2_ctrl_handler_free(&ctx->hdl);
	v4l2_fh_exit(&ctx->fh);
	kfree(ctx);
	return 0;
}

static const struct v4l2_file_operations dip_fops = {
	.owner = THIS_MODULE,
	.open = dip_open,
	.release = dip_release,
	.poll = v4l2_m2m_fop_poll,
	.unlocked_ioctl = video_ioctl2,
	.mmap = v4l2_m2m_fop_mmap,
};

static int dip_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct device_node *np;
	struct platform_device *mdp_pdev;
	struct dip_dev *dip;
	int ret;

	dip = devm_kzalloc(dev, sizeof(*dip), GFP_KERNEL);
	if (!dip)
		return -ENOMEM;
	dip->dev = dev;
	mutex_init(&dip->lock);
	spin_lock_init(&dip->job_lock);
	INIT_WORK(&dip->run, dip_run);
	INIT_DELAYED_WORK(&dip->timeout, dip_timeout);

	dip->regs = devm_platform_ioremap_resource(pdev, 0);
	if (IS_ERR(dip->regs))
		return PTR_ERR(dip->regs);

	dip->clks[0].id = "larb5";
	dip->clks[1].id = "dip";
	ret = devm_clk_bulk_get(dev, ARRAY_SIZE(dip->clks), dip->clks);
	if (ret)
		return dev_err_probe(dev, ret, "no clocks\n");

	/* The MDP3 driver, whose GCE path runs the jobs */
	np = of_find_compatible_node(NULL, NULL, "mediatek,mt8183-mdp3-rdma");
	mdp_pdev = np ? of_find_device_by_node(np) : NULL;
	of_node_put(np);
	if (!mdp_pdev)
		return dev_err_probe(dev, -ENODEV, "no MDP3\n");
	dip->mdp = platform_get_drvdata(mdp_pdev);
	put_device(&mdp_pdev->dev);
	if (!dip->mdp)
		return -EPROBE_DEFER;

	dip->scp = scp_get(pdev);
	if (!dip->scp)
		return dev_err_probe(dev, -EPROBE_DEFER, "no SCP\n");

	ret = dma_set_mask_and_coherent(dev, DMA_BIT_MASK(34));
	if (ret)
		goto put_scp;
	dip->work = dma_alloc_coherent(scp_get_device(dip->scp), DIP_WORK_SZ, &dip->work_scp,
				       GFP_KERNEL);
	if (!dip->work) {
		ret = -ENOMEM;
		goto put_scp;
	}
	dip->work_isp = dma_map_resource(dev, dip->work_scp, DIP_WORK_SZ, DMA_BIDIRECTIONAL,
					 DMA_ATTR_SKIP_CPU_SYNC);
	if (dma_mapping_error(dev, dip->work_isp)) {
		ret = -ENOMEM;
		goto free_work;
	}

	dip->wq = alloc_ordered_workqueue("mtk-dip", WQ_HIGHPRI | WQ_FREEZABLE);
	if (!dip->wq) {
		ret = -ENOMEM;
		goto unmap;
	}
	ret = scp_ipi_register(dip->scp, SCP_IPI_DIP, dip_scp_handler, dip);
	if (ret)
		goto free_wq;

	pm_runtime_set_autosuspend_delay(dev, 1000);
	pm_runtime_use_autosuspend(dev);
	devm_pm_runtime_enable(dev);

	ret = v4l2_device_register(dev, &dip->v4l2_dev);
	if (ret)
		goto unregister_ipi;
	dip->m2m = v4l2_m2m_init(&dip_m2m_ops);
	if (IS_ERR(dip->m2m)) {
		ret = PTR_ERR(dip->m2m);
		goto unregister_v4l2;
	}
	dip->vfd = (struct video_device) {
		.name = DIP_NAME,
		.fops = &dip_fops,
		.ioctl_ops = &dip_ioctl_ops,
		.release = video_device_release_empty,
		.lock = &dip->lock,
		.v4l2_dev = &dip->v4l2_dev,
		.vfl_dir = VFL_DIR_M2M,
		.device_caps = V4L2_CAP_VIDEO_M2M_MPLANE | V4L2_CAP_STREAMING,
	};
	video_set_drvdata(&dip->vfd, dip);
	ret = video_register_device(&dip->vfd, VFL_TYPE_VIDEO, -1);
	if (ret)
		goto release_m2m;
	platform_set_drvdata(pdev, dip);
	dip_the = dip;
	dev_info(dev, "ISP pass 2 registered as %s\n", video_device_node_name(&dip->vfd));
	return 0;

release_m2m:
	v4l2_m2m_release(dip->m2m);
unregister_v4l2:
	v4l2_device_unregister(&dip->v4l2_dev);
unregister_ipi:
	scp_ipi_unregister(dip->scp, SCP_IPI_DIP);
free_wq:
	destroy_workqueue(dip->wq);
unmap:
	dma_unmap_resource(dev, dip->work_isp, DIP_WORK_SZ, DMA_BIDIRECTIONAL,
			   DMA_ATTR_SKIP_CPU_SYNC);
free_work:
	dma_free_coherent(scp_get_device(dip->scp), DIP_WORK_SZ, dip->work, dip->work_scp);
put_scp:
	scp_put(dip->scp);
	return ret;
}

static void dip_remove(struct platform_device *pdev)
{
	struct dip_dev *dip = platform_get_drvdata(pdev);

	video_unregister_device(&dip->vfd);
	v4l2_m2m_release(dip->m2m);
	v4l2_device_unregister(&dip->v4l2_dev);
	scp_ipi_unregister(dip->scp, SCP_IPI_DIP);
	cancel_delayed_work_sync(&dip->timeout);
	destroy_workqueue(dip->wq);
	dma_unmap_resource(dip->dev, dip->work_isp, DIP_WORK_SZ, DMA_BIDIRECTIONAL,
			   DMA_ATTR_SKIP_CPU_SYNC);
	dma_free_coherent(scp_get_device(dip->scp), DIP_WORK_SZ, dip->work, dip->work_scp);
	scp_put(dip->scp);
	dip_the = NULL;
}

static const struct of_device_id dip_of_ids[] = {
	{ .compatible = "mediatek,mt8183-dip" },
	{}
};
MODULE_DEVICE_TABLE(of, dip_of_ids);

static struct platform_driver dip_driver = {
	.probe = dip_probe,
	.remove = dip_remove,
	.driver = {
		.name = DIP_NAME,
		.of_match_table = dip_of_ids,
	},
};
module_platform_driver(dip_driver);

MODULE_DESCRIPTION("MediaTek MT8183 ISP pass 2 (DIP), through the SCP and the MDP3");
MODULE_LICENSE("GPL");
