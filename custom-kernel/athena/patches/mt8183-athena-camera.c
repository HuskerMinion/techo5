// SPDX-License-Identifier: GPL-2.0-only
/*
 * Fixed OV13855 -> CSI0 -> SENINF1 -> MUX0 -> CAM2 -> CAMSV1 -> IMGO pipeline for the Echo Show 8
 * 2nd gen (athena, MT8183).
 *
 * The sensor's tables are MediaTek's ov13855_mipi_raw imgsensor driver's (sensor_init, the 2112x1568
 * binned preview_setting, and its 4224x3136 capture_setting for the full mode), the one Amazon's
 * kernel builds. The format picks the mode: 2112x1568 for the stream, 4224x3136 for stills. The D-PHY, seninf and CAMSV
 * sequences are those of the MT8183 bring-up of BayLibre's MT8365 seninf/CAMSV drivers
 * (github.com/vpavlin/mt8183-camera), confirmed there on an MT8183 Chromebook: CAM2 feeds CAMSV1,
 * the MUX runs in 1-pixel mode, SENINF_TOP_CTRL must leave its reset value, and the packer's mode
 * numbers are reversed, PAK 1 being 10-bit. The packer writes four pixels in five bytes, least
 * significant bits first, as the Echo Show 5's does.
 */
#include <linux/bitfield.h>
#include <linux/clk.h>
#include <linux/delay.h>
#include <linux/dma-mapping.h>
#include <linux/gpio/consumer.h>
#include <linux/i2c.h>
#include <linux/interrupt.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/kthread.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/regulator/consumer.h>
#include <linux/wait.h>
#include <media/v4l2-ctrls.h>
#include <media/v4l2-device.h>
#include <media/v4l2-ioctl.h>
#include <media/videobuf2-vmalloc.h>

#define DMA_SLOTS 3

#define SENSOR_ADDR 0x36
#define SENSOR_ID 0xd855
/*
 * The streamed mode's shortest frame: 1608 lines of HTS 0x462 at 108 MHz (10.39 us a line), 60 frames a
 * second, which is the 1568 lines it sends and the least blanking after them; the lane rate is
 * preview_setting's, whose 3214-line frame gave 30, so a 2112-pixel line still takes 9.8 us of its
 * 10.39. An exposure longer than that frame lengthens it (VTS, 0x380e/0x380f), up to four times, 15 a
 * second, as dim light wants: the frame rate follows the light. The whole array's mode (stills) keeps
 * the long frame.
 */
#define PREVIEW_LINES 1608
#define FRAME_LINES 6428
/*
 * The streamed mode's longest frame, for a dark room: 52000 lines of 10.39 us, 0.54 s, about 1.9 frames
 * a second (VTS and the exposure registers both reach 65535 lines). The picture comes before the rate:
 * a frame this long gathers eight times the light of the 15-a-second one, where gain would only
 * amplify the noise. The stills' mode keeps FRAME_LINES.
 */
#define STREAM_MAX_LINES 52000
#define EXPOSURE_MARGIN 16
/* 540 Mbps a lane (0x0303 = 1), four lanes, 10 bits: the seninf TG divider's pixel clock. */
#define PIXEL_RATE (270000000ULL * 2 * 4 / 10)

/* seninf (0x1a040000): top, SENINF1's input block at +0, MUX0 at +0 */
#define TOP_CTRL 0x0000
#define TOP_MUX_CTRL 0x0008
#define TOP_CAM_MUX_CTRL 0x0010
#define TOP_PHY_CTL_CSI0 0x001c
#define SENINF_CTRL 0x0200
#define SENINF_CTRL_EXT 0x0204
#define TG1_PH_CNT 0x0600
#define TG1_SEN_CK 0x0604
#define TG1_TM_STP 0x0614
#define MIPI_RX_CON24 0x0824
#define CSI2_CTL 0x0a00
#define CSI2_LNRD_TIMING 0x0a08
#define CSI2_DPCM 0x0a0c
#define CSI2_DGB_SEL 0x0a18
#define CSI2_SPARE0 0x0a20
#define CSI2_HS_TRAIL 0x0a40
#define CSI2_RESYNC_MERGE 0x0a74
#define CSI2_MODE 0x0ae8
#define CSI2_DPHY_SYNC 0x0b20
#define MUX_CTRL 0x0d00
#define MUX_SPARE 0x0d2c
#define MUX_CTRL_EXT 0x0d3c

/* CAMSV1 (0x1a050000): IMGO at +0x20, TG at +0x220, top at +0x510 */
#define IMGO_BASE 0x0020
#define IMGO_XSIZE 0x0030
#define IMGO_YSIZE 0x0034
#define IMGO_STRIDE 0x0038
#define IMGO_CON 0x003c
#define IMGO_CON2 0x0040
#define TG_SEN_MODE 0x0230
#define TG_VF_CON 0x0234
#define TG_GRAB_PXL 0x0238
#define TG_GRAB_LIN 0x023c
#define TG_PATH_CFG 0x0240
#define TG_FRMSIZE_ST 0x0268
#define TG_INTER_ST 0x026c
#define SV_MODULE_EN 0x0510
#define SV_FMT_SEL 0x0514
#define SV_INT_EN 0x0518
#define SV_INT_STATUS 0x051c
#define SV_SW_CTL 0x0520
#define SV_CLK_EN 0x0530
#define SV_PAK 0x054c

#define SV_IRQ_TG_ERR BIT(4)
#define SV_IRQ_TG_GBERR BIT(5)
#define SV_IRQ_PASS1_DON BIT(10)
#define SV_IRQ_IMGO_ERR BIT(16)
/*
 * IMGO_ERR stays raised for the whole stream on MT8183 with whole, correct frames, and neither
 * reading nor writing INT_STATUS clears it (read on athena; the Chromebook bring-up masked it too),
 * so it is not enabled: it would interrupt continuously.
 */
#define SV_IRQ_ERRORS (SV_IRQ_TG_ERR | SV_IRQ_TG_GBERR)

/* MIPI CSI0 RX analog (0x11c80000): CSI0A at +0, CSI0B at +0x1000 */
#define CSI0B 0x1000
#define ANA00 0x0000
#define ANA18 0x0018
#define ANA1C 0x001c
#define ANA20 0x0020
#define ANA24 0x0024
#define ANA40 0x0040
#define WRAPPER80 0x0080
#define ANAA8 0x00a8

/*
 * The lane mux the board's wiring needs: MediaTek's usual 4D1C layout (CSI0A_L0 data 2, A_L1 data 0,
 * A_L2 clock, B_L0 data 1, B_L1 data 3), 0xc9 in MIPI_RX_CON24[31:24], as on MT8183 Chromebooks.
 */
static int lane_mux = 0xc9;
module_param(lane_mux, int, 0644);
MODULE_PARM_DESC(lane_mux, "MIPI_RX_CON24 data lane mux, bits 31:24 (default 0xc9)");

struct reg8 {
	u16 reg;
	u8 value;
};

static const struct reg8 sensor_init[] = {
	{ 0x0103, 0x01 }, { 0xffff, 10 }, { 0x0300, 0x02 }, { 0x0301, 0x00 }, { 0x0302, 0x5a },
	{ 0x0303, 0x00 }, { 0x0304, 0x00 }, { 0x0305, 0x01 }, { 0x3022, 0x01 }, { 0x3012, 0x40 },
	{ 0x3013, 0x72 }, { 0x3016, 0x72 }, { 0x301b, 0xf0 }, { 0x301f, 0xd0 }, { 0x3106, 0x15 },
	{ 0x3107, 0x23 }, { 0x3500, 0x00 }, { 0x3501, 0x80 }, { 0x3502, 0x00 }, { 0x3508, 0x02 },
	{ 0x3509, 0x00 }, { 0x350a, 0x00 }, { 0x350e, 0x00 }, { 0x3510, 0x00 }, { 0x3511, 0x02 },
	{ 0x3512, 0x00 }, { 0x3600, 0x2b }, { 0x3601, 0x52 }, { 0x3602, 0x60 }, { 0x3612, 0x05 },
	{ 0x3613, 0xa4 }, { 0x3620, 0x80 }, { 0x3621, 0x08 }, { 0x3622, 0x30 }, { 0x3624, 0x1c },
	{ 0x3661, 0x80 }, { 0x3662, 0x12 }, { 0x3664, 0x73 }, { 0x3665, 0xa7 }, { 0x366e, 0xff },
	{ 0x366f, 0xf4 }, { 0x3674, 0x00 }, { 0x3679, 0x0c }, { 0x367f, 0x01 }, { 0x3680, 0x0c },
	{ 0x3681, 0x60 }, { 0x3682, 0x17 }, { 0x3683, 0xa9 }, { 0x3684, 0x9a }, { 0x3709, 0x68 },
	{ 0x3714, 0x24 }, { 0x371a, 0x3e }, { 0x3737, 0x04 }, { 0x3738, 0xcc }, { 0x3739, 0x12 },
	{ 0x373d, 0x26 }, { 0x3764, 0x20 }, { 0x3765, 0x20 }, { 0x37a1, 0x36 }, { 0x37a8, 0x3b },
	{ 0x37ab, 0x31 }, { 0x37c2, 0x04 }, { 0x37c3, 0xf1 }, { 0x37c5, 0x00 }, { 0x37d8, 0x03 },
	{ 0x37d9, 0x0c }, { 0x37da, 0xc2 }, { 0x37dc, 0x02 }, { 0x37e0, 0x00 }, { 0x37e1, 0x0a },
	{ 0x37e2, 0x14 }, { 0x37e3, 0x04 }, { 0x37e4, 0x26 }, { 0x37e5, 0x03 }, { 0x37e6, 0x04 },
	{ 0x3800, 0x00 }, { 0x3801, 0x00 }, { 0x3802, 0x00 }, { 0x3803, 0x08 }, { 0x3804, 0x10 },
	{ 0x3805, 0x9f }, { 0x3806, 0x0c }, { 0x3807, 0x57 }, { 0x3808, 0x10 }, { 0x3809, 0x80 },
	{ 0x380a, 0x0c }, { 0x380b, 0x40 }, { 0x380c, 0x04 }, { 0x380d, 0x62 }, { 0x380e, 0x0c },
	{ 0x380f, 0x8e }, { 0x3811, 0x10 }, { 0x3813, 0x08 }, { 0x3814, 0x01 }, { 0x3815, 0x01 },
	{ 0x3816, 0x01 }, { 0x3817, 0x01 }, { 0x3820, 0xa8 }, { 0x3821, 0x00 }, { 0x3822, 0xc2 },
	{ 0x3823, 0x18 }, { 0x3826, 0x11 }, { 0x3827, 0x1c }, { 0x3829, 0x03 }, { 0x3832, 0x00 },
	{ 0x3c80, 0x00 }, { 0x3c87, 0x01 }, { 0x3c8c, 0x19 }, { 0x3c8d, 0x1c }, { 0x3c90, 0x00 },
	{ 0x3c91, 0x00 }, { 0x3c92, 0x00 }, { 0x3c93, 0x00 }, { 0x3c94, 0x41 }, { 0x3c95, 0x54 },
	{ 0x3c96, 0x34 }, { 0x3c97, 0x04 }, { 0x3c98, 0x00 }, { 0x3d8c, 0x73 }, { 0x3d8d, 0xc0 },
	{ 0x3f00, 0x0b }, { 0x3f03, 0x00 }, { 0x4001, 0xe0 }, { 0x4008, 0x00 }, { 0x4009, 0x0f },
	{ 0x4011, 0xf0 }, { 0x4050, 0x04 }, { 0x4051, 0x0b }, { 0x4052, 0x00 }, { 0x4053, 0x80 },
	{ 0x4054, 0x00 }, { 0x4055, 0x80 }, { 0x4056, 0x00 }, { 0x4057, 0x80 }, { 0x4058, 0x00 },
	{ 0x4059, 0x80 }, { 0x405e, 0x20 }, { 0x4500, 0x07 }, { 0x4503, 0x00 }, { 0x450a, 0x04 },
	{ 0x4809, 0x04 }, { 0x480c, 0x12 }, { 0x4833, 0x10 }, { 0x4837, 0x0e }, { 0x4902, 0x01 },
	{ 0x4d00, 0x03 }, { 0x4d01, 0xc9 }, { 0x4d02, 0xbc }, { 0x4d03, 0xd7 }, { 0x4d04, 0xf0 },
	{ 0x4d05, 0xa2 }, { 0x5000, 0xff }, { 0x5001, 0x07 }, { 0x5040, 0x39 }, { 0x5041, 0x10 },
	{ 0x5042, 0x10 }, { 0x5043, 0x84 }, { 0x5044, 0x62 }, { 0x5180, 0x00 }, { 0x5181, 0x10 },
	{ 0x5182, 0x02 }, { 0x5183, 0x0f }, { 0x5200, 0x1b }, { 0x520b, 0x07 }, { 0x520c, 0x0f },
	{ 0x5300, 0x04 }, { 0x5301, 0x0c }, { 0x5302, 0x0c }, { 0x5303, 0x0f }, { 0x5304, 0x00 },
	{ 0x5305, 0x70 }, { 0x5306, 0x00 }, { 0x5307, 0x80 }, { 0x5308, 0x00 }, { 0x5309, 0xa5 },
	{ 0x530a, 0x00 }, { 0x530b, 0xd3 }, { 0x530c, 0x00 }, { 0x530d, 0xf0 }, { 0x530e, 0x01 },
	{ 0x530f, 0x10 }, { 0x5310, 0x01 }, { 0x5311, 0x20 }, { 0x5312, 0x01 }, { 0x5313, 0x20 },
	{ 0x5314, 0x01 }, { 0x5315, 0x20 }, { 0x5316, 0x08 }, { 0x5317, 0x08 }, { 0x5318, 0x10 },
	{ 0x5319, 0x88 }, { 0x531a, 0x88 }, { 0x531b, 0xa9 }, { 0x531c, 0xaa }, { 0x531d, 0x0a },
	{ 0x5405, 0x02 }, { 0x5406, 0x67 }, { 0x5407, 0x01 }, { 0x5408, 0x4a },
};

static const struct reg8 sensor_mode[] = {
	{ 0x0303, 0x01 }, { 0x3501, 0x40 }, { 0x3662, 0x10 }, { 0x3714, 0x28 }, { 0x3737, 0x08 },
	{ 0x3739, 0x20 }, { 0x37c2, 0x14 }, { 0x37d9, 0x0c }, { 0x37e3, 0x08 }, { 0x37e4, 0x34 },
	{ 0x37e6, 0x08 }, { 0x3801, 0x00 }, { 0x3802, 0x00 }, { 0x3803, 0x08 }, { 0x3805, 0x9f },
	{ 0x3806, 0x0c }, { 0x3807, 0x4f }, { 0x3808, 0x08 }, { 0x3809, 0x40 }, { 0x380a, 0x06 },
	{ 0x380b, 0x20 }, { 0x380c, 0x04 }, { 0x380d, 0x62 }, { 0x380e, 0x0c }, { 0x380f, 0x8e },
	{ 0x3811, 0x08 }, { 0x3813, 0x02 }, { 0x3814, 0x03 }, { 0x3816, 0x03 }, { 0x3820, 0xab },
	{ 0x3826, 0x04 }, { 0x3827, 0x90 }, { 0x3829, 0x07 }, { 0x4009, 0x0d }, { 0x4050, 0x04 },
	{ 0x4051, 0x0b }, { 0x4837, 0x1c }, { 0x4902, 0x01 }, { 0x0302, 0x5e }, { 0x4837, 0x1c },
};

/*
 * The whole array, from capture_setting's 15 frames a second (PIP) table, but at preview's 540 Mbps a
 * lane (0x0303 = 1, 0x4837 = 0x1c) rather than its 1080, so the D-PHY and seninf need no change: the line
 * is lengthened to 2400 clocks instead (0x380c/0x380d, against 1122), which at 108 MHz is 22.2 us,
 * enough for a 4224-pixel line on four lanes at 540 Mbps (19.6 us). With FRAME_LINES that is about
 * seven frames a second, which is for stills.
 */
static const struct reg8 sensor_full[] = {
	{ 0x0303, 0x01 }, { 0x3501, 0x80 }, { 0x3662, 0x12 }, { 0x3714, 0x24 }, { 0x3737, 0x04 },
	{ 0x3739, 0x12 }, { 0x37c2, 0x04 }, { 0x37d9, 0x0c }, { 0x37e3, 0x04 }, { 0x37e4, 0x26 },
	{ 0x37e6, 0x04 }, { 0x3801, 0x00 }, { 0x3802, 0x00 }, { 0x3803, 0x08 }, { 0x3805, 0x9f },
	{ 0x3806, 0x0c }, { 0x3807, 0x57 }, { 0x3808, 0x10 }, { 0x3809, 0x80 }, { 0x380a, 0x0c },
	{ 0x380b, 0x40 }, { 0x380c, 0x09 }, { 0x380d, 0x60 }, { 0x380e, 0x0f }, { 0x380f, 0xaa },
	{ 0x3811, 0x10 }, { 0x3813, 0x08 }, { 0x3814, 0x01 }, { 0x3816, 0x01 }, { 0x3820, 0xa8 },
	{ 0x3826, 0x11 }, { 0x3827, 0x1c }, { 0x3829, 0x03 }, { 0x4009, 0x0f }, { 0x4050, 0x04 },
	{ 0x4051, 0x0b }, { 0x4837, 0x1c }, { 0x4902, 0x01 }, { 0x0302, 0x5e }, { 0x4837, 0x1c },
};

struct sensor_size {
	u32 width, height;
	const struct reg8 *table;
	unsigned int len;
};

static const struct sensor_size sizes[] = {
	{ 2112, 1568, sensor_mode, ARRAY_SIZE(sensor_mode) },	/* binned: the stream */
	{ 4224, 3136, sensor_full, ARRAY_SIZE(sensor_full) },	/* the whole array: stills */
};

static u32 size_stride(const struct sensor_size *z) { return z->width * 10 / 8; }
static u32 size_bytes(const struct sensor_size *z) { return size_stride(z) * z->height; }

struct camera_buffer {
	struct vb2_v4l2_buffer vb;
	struct list_head list;
};

struct athena_camera {
	struct device *dev;
	void __iomem *sen, *sv, *csi;
	struct clk_bulk_data *clks;
	int nclks;
	struct clk *seninf_clk;
	struct regulator_bulk_data supplies[3];
	struct gpio_desc *reset, *powerdown;
	struct i2c_client *sensor;
	int irq;
	struct v4l2_device v4l2;
	struct video_device video;
	struct v4l2_ctrl_handler controls;
	struct vb2_queue queue;
	struct mutex lock, sensor_lock;
	spinlock_t buffers_lock;
	struct list_head buffers;
	struct task_struct *thread;
	wait_queue_head_t frame_wait;
	const struct sensor_size *size;	/* the mode the format chose */
	u32 vts;			/* the frame's length in lines, as last written */
	void *dma;
	dma_addr_t dma_addr;
	size_t dma_size;
	unsigned int slot;		/* the slot the DMA writes now */
	unsigned int ready;		/* the slot of the last whole frame */
	atomic_t frames;		/* frames written since the start */
	unsigned int errors;
	u32 error_status;
	u32 sequence;
	bool powered;
};

static void mask(void __iomem *base, u32 off, u32 clear, u32 set)
{
	writel((readl(base + off) & ~clear) | set, base + off);
}

static int sensor_write(struct athena_camera *c, u16 reg, u8 value)
{
	u8 buf[3] = { reg >> 8, reg, value };
	int ret = i2c_master_send(c->sensor, buf, sizeof(buf));

	return ret == sizeof(buf) ? 0 : ret < 0 ? ret : -EIO;
}

static int sensor_read(struct athena_camera *c, u16 reg)
{
	u8 addr[2] = { reg >> 8, reg }, value;
	struct i2c_msg msgs[] = {
		{ .addr = c->sensor->addr, .len = 2, .buf = addr },
		{ .addr = c->sensor->addr, .flags = I2C_M_RD, .len = 1, .buf = &value },
	};
	int ret = i2c_transfer(c->sensor->adapter, msgs, ARRAY_SIZE(msgs));

	return ret == ARRAY_SIZE(msgs) ? value : ret < 0 ? ret : -EIO;
}

static int sensor_table(struct athena_camera *c, const struct reg8 *t, unsigned int n)
{
	unsigned int i;
	int ret;

	for (i = 0; i < n; i++) {
		if (t[i].reg == 0xffff) {
			msleep(t[i].value);
			continue;
		}
		ret = sensor_write(c, t[i].reg, t[i].value);
		if (ret)
			return ret;
	}
	return 0;
}

/* The frame's length in lines (VTS), written only when it changes; it takes effect at the next frame. */
static int sensor_vts(struct athena_camera *c, u32 vts)
{
	int ret;

	if (vts == c->vts)
		return 0;
	ret = sensor_write(c, 0x380e, vts >> 8);
	if (!ret)
		ret = sensor_write(c, 0x380f, vts & 0xff);
	if (!ret)
		c->vts = vts;
	return ret;
}

/*
 * Exposure in lines, in 1/16 line units from bit 4 of 0x3500-0x3502. In the streamed mode the frame is
 * as long as the exposure needs, from 60 frames a second down to about 2 in a dark room
 * (STREAM_MAX_LINES); the stills' mode keeps its long frame.
 */
static int sensor_exposure(struct athena_camera *c, u32 lines)
{
	int ret = 0;

	lines = clamp(lines, 4U, (c->size->table == sensor_full ? FRAME_LINES : STREAM_MAX_LINES) -
		      EXPOSURE_MARGIN) & ~1U;
	if (c->size->table != sensor_full)
		ret = sensor_vts(c, max_t(u32, PREVIEW_LINES, ALIGN(lines + EXPOSURE_MARGIN, 2)));
	if (!ret)
		ret = sensor_write(c, 0x3500, (lines >> 12) & 0x0f);
	if (!ret)
		ret = sensor_write(c, 0x3501, lines >> 4);
	if (!ret)
		ret = sensor_write(c, 0x3502, lines << 4);
	return ret;
}

/* Gain in 1/64, as MediaTek's driver takes it: 0x3508/0x3509 hold twice that, 0x80 being 1x. */
static int sensor_gain(struct athena_camera *c, u32 gain)
{
	u32 reg = min(gain * 2, 0x7c0U);
	int ret;

	ret = sensor_write(c, 0x3508, reg >> 8);
	if (!ret)
		ret = sensor_write(c, 0x3509, reg);
	return ret;
}

static int camera_control(struct v4l2_ctrl *ctrl)
{
	struct athena_camera *c = container_of(ctrl->handler, struct athena_camera, controls);
	int ret = 0;

	mutex_lock(&c->sensor_lock);
	if (c->powered) {
		if (ctrl->id == V4L2_CID_EXPOSURE)
			ret = sensor_exposure(c, ctrl->val);
		else if (ctrl->id == V4L2_CID_ANALOGUE_GAIN)
			ret = sensor_gain(c, ctrl->val);
	}
	mutex_unlock(&c->sensor_lock);
	return ret;
}
static const struct v4l2_ctrl_ops control_ops = { .s_ctrl = camera_control };

/* CSI0 as a D-PHY, 4 data lanes and a clock (phy-mtk-mipi-csi-0-5's power on, C/D-PHY port). */
static void csi_phy(struct athena_camera *c, bool on)
{
	void __iomem *a = c->csi, *b = c->csi + CSI0B;

	if (!on) {
		mask(a, ANA00, BIT(3) | BIT(2), 0);
		mask(b, ANA00, BIT(3) | BIT(2), 0);
		return;
	}
	/* CPHY_EN off; lanes 0 and 1 data, lane 2 of CSI0A the clock */
	mask(a, ANA00, BIT(0) | BIT(5) | BIT(8), BIT(6) | BIT(9) | BIT(11) | BIT(12));
	mask(b, ANA00, BIT(0) | BIT(5) | BIT(8) | BIT(11), BIT(6) | BIT(9) | BIT(12));
	/* byte clock invert */
	mask(a, ANAA8, 0, 7);
	mask(b, ANAA8, 0, 7);
	/* EQ: IS and BW 1 on each lane */
	mask(a, ANA18, 0xf0, 0x50);
	mask(a, ANA1C, 0xf00000, 0x500000);
	mask(a, ANA20, 0xf00000, 0x500000);
	mask(b, ANA18, 0xf0, 0x50);
	mask(b, ANA1C, 0xf00000, 0x500000);
	mask(b, ANA20, 0xf00000, 0x500000);
	mask(a, ANA40, 0, 0x90);
	mask(a, ANA24, 0xff000000, 0x40000000);
	mask(b, ANA24, 0xff000000, 0x40000000);
	mask(a, WRAPPER80, 3 << 16, 0);
	mask(b, WRAPPER80, 3 << 16, 0);
	/* band gap core, then its low-pass filter */
	mask(a, ANA00, 0, BIT(3));
	mask(b, ANA00, 0, BIT(3));
	usleep_range(20, 40);
	mask(a, ANA00, 0, BIT(2));
	mask(b, ANA00, 0, BIT(2));
}

static void seninf_start(struct athena_camera *c)
{
	void __iomem *s = c->sen;
	unsigned long rate = clk_get_rate(c->seninf_clk);
	u32 clkcnt = rate > PIXEL_RATE ? div_u64(rate, PIXEL_RATE) - 1 : 0;
	u32 val;

	/* pixel clocks on, out of MUX low-power mode */
	writel(BIT(10) | BIT(11), s + TOP_CTRL);
	/* CSI0 in 4D1C mode, clock on lane 2, lane mux enabled */
	mask(s, TOP_PHY_CTL_CSI0, BIT(0) | GENMASK(10, 8), (2 << 8) | BIT(31));
	csi_phy(c, true);
	mask(s, MIPI_RX_CON24, GENMASK(31, 24), (u32)(lane_mux & 0xff) << 24);

	writel(0x67, s + TG1_TM_STP);
	writel(0, s + TG1_PH_CNT);
	writel(FIELD_PREP(GENMASK(21, 16), min(clkcnt, 0x3fU)) | 1, s + TG1_SEN_CK);
	/* SENINF1 on, CSI-2 source, 10-bit pad */
	mask(s, SENINF_CTRL, GENMASK(30, 28) | GENMASK(14, 12), BIT(0));
	mask(s, SENINF_CTRL_EXT, BIT(5), BIT(6));
	writel(BIT(7), s + CSI2_DPCM);
	mask(s, CSI2_LNRD_TIMING, GENMASK(15, 8), 0x15 << 8);
	mask(s, CSI2_CTL, 0, BIT(16) | BIT(4) | 0xf);
	mask(s, CSI2_RESYNC_MERGE, BIT(10) | BIT(11) | GENMASK(2, 0), 3);
	mask(s, CSI2_MODE, GENMASK(10, 0), 0);
	mask(s, CSI2_DPHY_SYNC, 0xffffffff, (0x001d << 16) | 0xff00);
	mask(s, CSI2_CTL, BIT(27) | BIT(7), BIT(25));
	mask(s, CSI2_HS_TRAIL, 0xff, 8);
	mask(s, CSI2_DGB_SEL, 0xff, BIT(31) | 0x1a);
	writel(0xfffffffe, s + CSI2_SPARE0);
	mask(s, SENINF_CTRL, 0, BIT(7));
	udelay(1);
	mask(s, SENINF_CTRL, BIT(7), 0);

	/* MUX0: MIPI sensor source, normal model, 1-pixel mode, normal FIFO */
	mask(s, MUX_CTRL, BIT(31) | GENMASK(15, 12), BIT(31) | (8 << 12));
	mask(s, MUX_CTRL_EXT, GENMASK(1, 0) | BIT(4), 1);
	mask(s, MUX_CTRL, BIT(8), 0);
	mask(s, MUX_CTRL, GENMASK(29, 28), 2 << 28);
	mask(s, MUX_CTRL, GENMASK(28, 22), 0x1b << 22);
	mask(s, MUX_CTRL, GENMASK(21, 16), 0x1f << 16);
	mask(s, MUX_CTRL, BIT(10) | BIT(9), 0);
	val = readl(s + MUX_CTRL);
	writel(val | 3, s + MUX_CTRL);
	writel(val & ~3U, s + MUX_CTRL);
	writel(BIT(13) | 0xc0000, s + MUX_SPARE);

	/* SENINF1 into MUX0, MUX0 out on CAM2, which feeds CAMSV1 */
	mask(s, TOP_MUX_CTRL, 0xf, 0);
	mask(s, TOP_CAM_MUX_CTRL, GENMASK(11, 8), 0);
}

static void seninf_stop(struct athena_camera *c)
{
	mask(c->sen, CSI2_CTL, BIT(4) | 0xf, 0);
	csi_phy(c, false);
}

static void camsv_slot(struct athena_camera *c, unsigned int slot)
{
	writel(c->dma_addr + slot * size_bytes(c->size), c->sv + IMGO_BASE);
}

static void camsv_start(struct athena_camera *c)
{
	void __iomem *v = c->sv;
	u32 stride = size_stride(c->size), height = c->size->height;
	u32 val;

	writel(0, v + TG_SEN_MODE);
	writel(c->size->width << 16, v + TG_GRAB_PXL);
	writel(height << 16, v + TG_GRAB_LIN);
	writel(0x1000, v + TG_PATH_CFG);
	writel(BIT(2), v + SV_SW_CTL);
	writel(0, v + SV_SW_CTL);
	writel(BIT(0), v + SV_SW_CTL);
	if (readl_poll_timeout_atomic(v + SV_SW_CTL, val, val == 3, 10, 200))
		dev_warn(c->dev, "IMGO reset handshake timed out: %08x\n", val);
	writel(0, v + SV_SW_CTL);
	readl(v + SV_INT_STATUS);
	writel(SV_IRQ_PASS1_DON | SV_IRQ_ERRORS, v + SV_INT_EN);
	/* double-buffered registers, TG, packer; RAW10 in, PAK 1: 10-bit packed on MT8183 */
	writel(BIT(30) | BIT(2) | BIT(0), v + SV_MODULE_EN);
	writel(1, v + SV_FMT_SEL);
	writel(1, v + SV_PAK);
	writel(stride - 1, v + IMGO_XSIZE);
	writel(height - 1, v + IMGO_YSIZE);
	writel((3 << 16) | stride, v + IMGO_STRIDE);
	writel(0x80000080, v + IMGO_CON);
	writel(0x00800080, v + IMGO_CON2);
	c->slot = 0;
	camsv_slot(c, 0);
	mask(v, TG_SEN_MODE, 0, BIT(0));
	mask(v, SV_MODULE_EN, 0, BIT(4));
	writel(BIT(0) | BIT(2) | BIT(15), v + SV_CLK_EN);
	mask(v, TG_VF_CON, 0, BIT(0));
}

static void camsv_stop(struct athena_camera *c)
{
	mask(c->sv, TG_VF_CON, BIT(0), 0);
	mask(c->sv, TG_SEN_MODE, BIT(0), 0);
	mask(c->sv, SV_MODULE_EN, BIT(4), 0);
	writel(0, c->sv + SV_INT_EN);
}

static irqreturn_t camera_irq(int irq, void *data)
{
	struct athena_camera *c = data;
	u32 status = readl(c->sv + SV_INT_STATUS);

	if (status & SV_IRQ_ERRORS) {
		c->error_status |= status;
		dev_err_ratelimited(c->dev, "CAMSV error %08x\n", status);
		/* errors can repeat on every line: stop listening to them */
		if (++c->errors == 64)
			writel(SV_IRQ_PASS1_DON, c->sv + SV_INT_EN);
	}
	if (!(status & SV_IRQ_PASS1_DON))
		return status ? IRQ_HANDLED : IRQ_NONE;
	/*
	 * The frame in c->slot is whole. The base address is latched at the next frame start, so the
	 * one set here takes the frame after this; the slot just finished is left alone for two frames.
	 */
	c->ready = c->slot;
	c->slot = (c->slot + 1) % DMA_SLOTS;
	camsv_slot(c, c->slot);
	atomic_inc(&c->frames);
	wake_up(&c->frame_wait);
	return IRQ_HANDLED;
}

static void camera_dump(struct athena_camera *c)
{
	dev_err(c->dev, "seninf ctrl %08x/%08x top %08x/%08x/%08x csi2 %08x lnrc %08x mux %08x\n",
		readl(c->sen + SENINF_CTRL), readl(c->sen + SENINF_CTRL_EXT), readl(c->sen + TOP_CTRL),
		readl(c->sen + TOP_MUX_CTRL), readl(c->sen + TOP_CAM_MUX_CTRL), readl(c->sen + CSI2_CTL),
		readl(c->sen + 0x0a28), readl(c->sen + MUX_CTRL));
	dev_err(c->dev, "seninf csi2 int %08x dbg %08x mux int %08x size %08x err size %08x\n",
		readl(c->sen + 0x0a14), readl(c->sen + 0x0a1c), readl(c->sen + 0x0d08),
		readl(c->sen + 0x0d28), readl(c->sen + 0x0d30));
	dev_err(c->dev, "camsv module %08x int %08x tg mode %08x vf %08x frame %08x inter %08x\n",
		readl(c->sv + SV_MODULE_EN), readl(c->sv + SV_INT_STATUS), readl(c->sv + TG_SEN_MODE),
		readl(c->sv + TG_VF_CON), readl(c->sv + TG_FRMSIZE_ST), readl(c->sv + TG_INTER_ST));
}

static void camera_power_off(struct athena_camera *c)
{
	dev_dbg(c->dev, "stopped after %d frames, %u errors (%08x)\n",
		 atomic_read(&c->frames), c->errors, c->error_status);
	mutex_lock(&c->sensor_lock);
	c->powered = false;
	sensor_write(c, 0x0100, 0);
	camsv_stop(c);
	seninf_stop(c);
	gpiod_set_value_cansleep(c->reset, 1);
	gpiod_set_value_cansleep(c->powerdown, 1);
	mutex_unlock(&c->sensor_lock);
	disable_irq(c->irq);
	regulator_bulk_disable(ARRAY_SIZE(c->supplies), c->supplies);
	clk_bulk_disable_unprepare(c->nclks, c->clks);
	pm_runtime_put_sync(c->dev);
}

static int camera_power_on(struct athena_camera *c)
{
	unsigned int i;
	int ret, hi, lo;

	ret = pm_runtime_resume_and_get(c->dev);
	if (ret < 0)
		return ret;
	ret = clk_bulk_prepare_enable(c->nclks, c->clks);
	if (ret)
		goto put_power;
	gpiod_set_value_cansleep(c->reset, 1);
	gpiod_set_value_cansleep(c->powerdown, 1);
	/* DOVDD, AVDD, DVDD in that order, with MCLK already running */
	for (i = 0; i < ARRAY_SIZE(c->supplies); i++) {
		ret = regulator_enable(c->supplies[i].consumer);
		if (ret) {
			while (i--)
				regulator_disable(c->supplies[i].consumer);
			goto clocks;
		}
		usleep_range(1000, 1500);
	}
	usleep_range(5000, 6000);
	gpiod_set_value_cansleep(c->powerdown, 0);
	usleep_range(2000, 3000);
	gpiod_set_value_cansleep(c->reset, 0);
	/* 8192 MCLK cycles before the first SCCB access */
	usleep_range(10000, 11000);
	enable_irq(c->irq);
	mutex_lock(&c->sensor_lock);
	hi = sensor_read(c, 0x300b);
	lo = sensor_read(c, 0x300c);
	if (hi < 0 || lo < 0 || ((hi << 8) | lo) != SENSOR_ID) {
		ret = hi < 0 ? hi : lo < 0 ? lo : -ENODEV;
		dev_err(c->dev, "OV13855 identification failed: %d/%d\n", hi, lo);
		goto unlock;
	}
	ret = sensor_table(c, sensor_init, ARRAY_SIZE(sensor_init));
	if (!ret)
		ret = sensor_table(c, c->size->table, c->size->len);
	c->vts = 0;
	if (!ret)
		ret = sensor_vts(c, c->size->table == sensor_full ? FRAME_LINES : PREVIEW_LINES);
	if (ret)
		goto unlock;
	camsv_start(c);
	seninf_start(c);
	c->powered = true;
	mutex_unlock(&c->sensor_lock);
	ret = v4l2_ctrl_handler_setup(&c->controls);
	if (!ret)
		ret = sensor_write(c, 0x0100, 1);
	if (ret)
		goto off;
	return 0;
unlock:
	mutex_unlock(&c->sensor_lock);
off:
	camera_power_off(c);
	return ret;
clocks:
	clk_bulk_disable_unprepare(c->nclks, c->clks);
put_power:
	pm_runtime_put_sync(c->dev);
	return ret;
}

static void return_buffers(struct athena_camera *c, enum vb2_buffer_state state)
{
	struct camera_buffer *b, *next;
	unsigned long flags;

	spin_lock_irqsave(&c->buffers_lock, flags);
	list_for_each_entry_safe(b, next, &c->buffers, list) {
		list_del(&b->list);
		vb2_buffer_done(&b->vb.vb2_buf, state);
	}
	spin_unlock_irqrestore(&c->buffers_lock, flags);
}

static int camera_thread(void *data)
{
	struct athena_camera *c = data;
	size_t bytes = size_bytes(c->size);
	int last = 0, count;
	unsigned long flags;
	bool first = true;

	while (!kthread_should_stop()) {
		struct camera_buffer *b = NULL;
		unsigned int ready;
		long left;

		left = wait_event_interruptible_timeout(c->frame_wait,
			atomic_read(&c->frames) != last || kthread_should_stop(),
			msecs_to_jiffies(3000));
		if (kthread_should_stop())
			break;
		count = atomic_read(&c->frames);
		if (count == last) {
			if (left)
				continue;
			dev_err(c->dev, "capture timed out\n");
			camera_dump(c);
			vb2_queue_error(&c->queue);
			break;
		}
		last = count;
		ready = READ_ONCE(c->ready);
		/* the first frame may have started before the DMA was ready */
		if (first) {
			first = false;
			continue;
		}
		spin_lock_irqsave(&c->buffers_lock, flags);
		if (!list_empty(&c->buffers)) {
			b = list_first_entry(&c->buffers, struct camera_buffer, list);
			list_del(&b->list);
		}
		spin_unlock_irqrestore(&c->buffers_lock, flags);
		if (!b)
			continue;
		dma_rmb();
		memcpy(vb2_plane_vaddr(&b->vb.vb2_buf, 0), c->dma + ready * bytes, bytes);
		b->vb.sequence = c->sequence++;
		b->vb.field = V4L2_FIELD_NONE;
		b->vb.vb2_buf.timestamp = ktime_get_ns();
		vb2_set_plane_payload(&b->vb.vb2_buf, 0, bytes);
		vb2_buffer_done(&b->vb.vb2_buf, VB2_BUF_STATE_DONE);
	}
	/* Keep the task alive until its owner joins it, including capture errors. */
	while (!kthread_should_stop())
		msleep(100);
	return 0;
}

static int queue_setup(struct vb2_queue *q, unsigned int *buffers,
	unsigned int *planes, unsigned int psizes[], struct device *alloc_devs[])
{
	struct athena_camera *c = vb2_get_drv_priv(q);
	u32 bytes = size_bytes(c->size);

	if (*planes)
		return psizes[0] < bytes ? -EINVAL : 0;
	*planes = 1;
	psizes[0] = bytes;
	return 0;
}

static int buffer_prepare(struct vb2_buffer *vb)
{
	struct athena_camera *c = vb2_get_drv_priv(vb->vb2_queue);

	return vb2_plane_size(vb, 0) < size_bytes(c->size) ? -EINVAL : 0;
}

static void buffer_queue(struct vb2_buffer *vb)
{
	struct athena_camera *c = vb2_get_drv_priv(vb->vb2_queue);
	struct camera_buffer *b = container_of(to_vb2_v4l2_buffer(vb), struct camera_buffer, vb);
	unsigned long flags;

	spin_lock_irqsave(&c->buffers_lock, flags);
	list_add_tail(&b->list, &c->buffers);
	spin_unlock_irqrestore(&c->buffers_lock, flags);
}

static int start_streaming(struct vb2_queue *q, unsigned int count)
{
	struct athena_camera *c = vb2_get_drv_priv(q);
	int ret;

	/* The DMA slots are sized for the mode: 3x 4 MB binned, 3x 16.5 MB for the whole array. */
	if (c->dma && c->dma_size != (size_t)size_bytes(c->size) * DMA_SLOTS) {
		dma_free_coherent(c->dev, c->dma_size, c->dma, c->dma_addr);
		c->dma = NULL;
	}
	if (!c->dma) {
		c->dma_size = (size_t)size_bytes(c->size) * DMA_SLOTS;
		c->dma = dma_alloc_coherent(c->dev, c->dma_size, &c->dma_addr, GFP_KERNEL);
		if (!c->dma) {
			ret = -ENOMEM;
			goto fail;
		}
	}
	atomic_set(&c->frames, 0);
	c->errors = 0;
	c->error_status = 0;
	c->sequence = 0;
	ret = camera_power_on(c);
	if (ret)
		goto fail;
	c->thread = kthread_run(camera_thread, c, "athena-camera");
	if (IS_ERR(c->thread)) {
		ret = PTR_ERR(c->thread);
		c->thread = NULL;
		camera_power_off(c);
		goto fail;
	}
	return 0;
fail:
	return_buffers(c, VB2_BUF_STATE_QUEUED);
	return ret;
}

static void stop_streaming(struct vb2_queue *q)
{
	struct athena_camera *c = vb2_get_drv_priv(q);

	if (c->thread) {
		kthread_stop(c->thread);
		c->thread = NULL;
	}
	camera_power_off(c);
	return_buffers(c, VB2_BUF_STATE_ERROR);
}

static const struct vb2_ops queue_ops = {
	.queue_setup = queue_setup, .buf_prepare = buffer_prepare,
	.buf_queue = buffer_queue, .start_streaming = start_streaming,
	.stop_streaming = stop_streaming,
};

static int querycap(struct file *f, void *priv, struct v4l2_capability *cap)
{
	strscpy(cap->driver, "athena-camera", sizeof(cap->driver));
	strscpy(cap->card, "athena-ov13855", sizeof(cap->card));
	strscpy(cap->bus_info, "platform:athena-camera", sizeof(cap->bus_info));
	return 0;
}

static int enum_fmt(struct file *f, void *priv, struct v4l2_fmtdesc *fmt)
{
	if (fmt->index)
		return -EINVAL;
	fmt->pixelformat = V4L2_PIX_FMT_SBGGR10P;
	return 0;
}

static void fill_fmt(const struct sensor_size *z, struct v4l2_format *fmt)
{
	fmt->fmt.pix = (struct v4l2_pix_format) {
		.width = z->width, .height = z->height, .pixelformat = V4L2_PIX_FMT_SBGGR10P,
		.field = V4L2_FIELD_NONE, .bytesperline = size_stride(z), .sizeimage = size_bytes(z),
		.colorspace = V4L2_COLORSPACE_RAW,
	};
}

/* The whole array for a width past the binned one, the binned mode otherwise. */
static const struct sensor_size *pick_size(u32 width)
{
	return width > sizes[0].width ? &sizes[1] : &sizes[0];
}

static int get_fmt(struct file *f, void *priv, struct v4l2_format *fmt)
{
	struct athena_camera *c = video_drvdata(f);

	fill_fmt(c->size, fmt);
	return 0;
}

static int try_fmt(struct file *f, void *priv, struct v4l2_format *fmt)
{
	fill_fmt(pick_size(fmt->fmt.pix.width), fmt);
	return 0;
}

static int set_fmt(struct file *f, void *priv, struct v4l2_format *fmt)
{
	struct athena_camera *c = video_drvdata(f);

	if (vb2_is_busy(&c->queue))
		return -EBUSY;
	c->size = pick_size(fmt->fmt.pix.width);
	fill_fmt(c->size, fmt);
	return 0;
}

static int enum_framesizes(struct file *f, void *priv, struct v4l2_frmsizeenum *fs)
{
	if (fs->index >= ARRAY_SIZE(sizes) || fs->pixel_format != V4L2_PIX_FMT_SBGGR10P)
		return -EINVAL;
	fs->type = V4L2_FRMSIZE_TYPE_DISCRETE;
	fs->discrete.width = sizes[fs->index].width;
	fs->discrete.height = sizes[fs->index].height;
	return 0;
}

static int enum_input(struct file *f, void *priv, struct v4l2_input *in)
{
	if (in->index)
		return -EINVAL;
	in->type = V4L2_INPUT_TYPE_CAMERA;
	strscpy(in->name, "OV13855", sizeof(in->name));
	return 0;
}

static int get_input(struct file *f, void *priv, unsigned int *i)
{
	*i = 0;
	return 0;
}

static int set_input(struct file *f, void *priv, unsigned int i)
{
	return i ? -EINVAL : 0;
}

static const struct v4l2_ioctl_ops camera_ioctls = {
	.vidioc_querycap = querycap, .vidioc_enum_fmt_vid_cap = enum_fmt,
	.vidioc_g_fmt_vid_cap = get_fmt, .vidioc_s_fmt_vid_cap = set_fmt,
	.vidioc_try_fmt_vid_cap = try_fmt, .vidioc_enum_input = enum_input,
	.vidioc_enum_framesizes = enum_framesizes,
	.vidioc_g_input = get_input, .vidioc_s_input = set_input,
	.vidioc_reqbufs = vb2_ioctl_reqbufs, .vidioc_querybuf = vb2_ioctl_querybuf,
	.vidioc_qbuf = vb2_ioctl_qbuf, .vidioc_dqbuf = vb2_ioctl_dqbuf,
	.vidioc_streamon = vb2_ioctl_streamon, .vidioc_streamoff = vb2_ioctl_streamoff,
};

static const struct v4l2_file_operations camera_fops = {
	.owner = THIS_MODULE, .open = v4l2_fh_open, .release = vb2_fop_release,
	.read = vb2_fop_read, .poll = vb2_fop_poll, .mmap = vb2_fop_mmap,
	.unlocked_ioctl = video_ioctl2,
};

static void sensor_unregister(void *data)
{
	i2c_unregister_device(data);
}

static int camera_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct athena_camera *c;
	struct device_node *node;
	struct i2c_adapter *bus;
	int ret;

	if (!device_iommu_mapped(dev))
		return -EPROBE_DEFER;
	c = devm_kzalloc(dev, sizeof(*c), GFP_KERNEL);
	if (!c)
		return -ENOMEM;
	c->dev = dev;
	c->size = &sizes[0];
	platform_set_drvdata(pdev, c);
	mutex_init(&c->lock);
	mutex_init(&c->sensor_lock);
	spin_lock_init(&c->buffers_lock);
	INIT_LIST_HEAD(&c->buffers);
	init_waitqueue_head(&c->frame_wait);
	c->sen = devm_platform_ioremap_resource_byname(pdev, "seninf");
	if (IS_ERR(c->sen))
		return PTR_ERR(c->sen);
	c->sv = devm_platform_ioremap_resource_byname(pdev, "camsv");
	if (IS_ERR(c->sv))
		return PTR_ERR(c->sv);
	c->csi = devm_platform_ioremap_resource_byname(pdev, "csi");
	if (IS_ERR(c->csi))
		return PTR_ERR(c->csi);
	c->nclks = devm_clk_bulk_get_all(dev, &c->clks);
	if (c->nclks < 0)
		return c->nclks;
	c->seninf_clk = devm_clk_get(dev, "seninf-top");
	if (IS_ERR(c->seninf_clk))
		return PTR_ERR(c->seninf_clk);
	c->supplies[0].supply = "dovdd";
	c->supplies[1].supply = "avdd";
	c->supplies[2].supply = "dvdd";
	ret = devm_regulator_bulk_get(dev, ARRAY_SIZE(c->supplies), c->supplies);
	if (ret)
		return ret;
	c->reset = devm_gpiod_get(dev, "reset", GPIOD_OUT_HIGH);
	if (IS_ERR(c->reset))
		return PTR_ERR(c->reset);
	c->powerdown = devm_gpiod_get(dev, "powerdown", GPIOD_OUT_HIGH);
	if (IS_ERR(c->powerdown))
		return PTR_ERR(c->powerdown);
	c->irq = platform_get_irq(pdev, 0);
	if (c->irq < 0)
		return c->irq;
	ret = devm_request_irq(dev, c->irq, camera_irq, IRQF_NO_AUTOEN, dev_name(dev), c);
	if (ret)
		return ret;
	node = of_parse_phandle(dev->of_node, "sensor-bus", 0);
	if (!node)
		return -EINVAL;
	bus = of_find_i2c_adapter_by_node(node);
	of_node_put(node);
	if (!bus)
		return -EPROBE_DEFER;
	c->sensor = i2c_new_dummy_device(bus, SENSOR_ADDR);
	i2c_put_adapter(bus);
	if (IS_ERR(c->sensor))
		return PTR_ERR(c->sensor);
	ret = devm_add_action_or_reset(dev, sensor_unregister, c->sensor);
	if (ret)
		return ret;
	/* IMGO_BASE_ADDR is a 32-bit IOVA */
	ret = dma_set_mask_and_coherent(dev, DMA_BIT_MASK(32));
	if (ret)
		return ret;
	ret = v4l2_device_register(dev, &c->v4l2);
	if (ret)
		return ret;
	v4l2_ctrl_handler_init(&c->controls, 2);
	v4l2_ctrl_new_std(&c->controls, &control_ops, V4L2_CID_EXPOSURE, 4,
			  STREAM_MAX_LINES - EXPOSURE_MARGIN, 2, 1600);
	v4l2_ctrl_new_std(&c->controls, &control_ops, V4L2_CID_ANALOGUE_GAIN, 64, 992, 1, 128);
	if (c->controls.error) {
		ret = c->controls.error;
		goto controls;
	}
	c->v4l2.ctrl_handler = &c->controls;
	c->queue = (struct vb2_queue) {
		.type = V4L2_BUF_TYPE_VIDEO_CAPTURE, .io_modes = VB2_MMAP | VB2_READ,
		.drv_priv = c, .buf_struct_size = sizeof(struct camera_buffer),
		.ops = &queue_ops, .mem_ops = &vb2_vmalloc_memops,
		.timestamp_flags = V4L2_BUF_FLAG_TIMESTAMP_MONOTONIC,
		.lock = &c->lock, .dev = dev, .min_queued_buffers = 2,
	};
	ret = vb2_queue_init(&c->queue);
	if (ret)
		goto controls;
	strscpy(c->video.name, "athena-ov13855", sizeof(c->video.name));
	c->video.v4l2_dev = &c->v4l2;
	c->video.fops = &camera_fops;
	c->video.ioctl_ops = &camera_ioctls;
	c->video.release = video_device_release_empty;
	c->video.lock = &c->lock;
	c->video.queue = &c->queue;
	c->video.device_caps = V4L2_CAP_VIDEO_CAPTURE | V4L2_CAP_STREAMING | V4L2_CAP_READWRITE;
	video_set_drvdata(&c->video, c);
	pm_runtime_enable(dev);
	ret = video_register_device(&c->video, VFL_TYPE_VIDEO, -1);
	if (!ret)
		return 0;
	pm_runtime_disable(dev);
	vb2_queue_release(&c->queue);
controls:
	v4l2_ctrl_handler_free(&c->controls);
	v4l2_device_unregister(&c->v4l2);
	return ret;
}

static void camera_remove(struct platform_device *pdev)
{
	struct athena_camera *c = platform_get_drvdata(pdev);

	vb2_video_unregister_device(&c->video);
	v4l2_ctrl_handler_free(&c->controls);
	v4l2_device_unregister(&c->v4l2);
	pm_runtime_disable(c->dev);
	if (c->dma)
		dma_free_coherent(c->dev, c->dma_size, c->dma, c->dma_addr);
}

static const struct of_device_id camera_match[] = {
	{ .compatible = "amazon,athena-camera" }, {},
};
MODULE_DEVICE_TABLE(of, camera_match);

static struct platform_driver camera_driver = {
	.probe = camera_probe,
	.remove = camera_remove,
	.driver = { .name = "athena-camera", .of_match_table = camera_match },
};
module_platform_driver(camera_driver);
MODULE_DESCRIPTION("Echo Show 8 2nd gen OV13855 capture");
MODULE_LICENSE("GPL");
