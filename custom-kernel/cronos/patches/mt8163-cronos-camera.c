// SPDX-License-Identifier: GPL-2.0-only
/*
 * Fixed OV02B10 -> SENINF1 -> TG1 -> IMGO pipeline for the Echo Show 5, and the ISP's pass 2 to
 * develop its frames: packed RAW10 in through IMGI, black level, white balance, demosaic, gamma
 * and BT.601 in the ISP, YUYV out through IMG2O, made I420 for the caller (CRONOS_IOC_DEVELOP):
 * the MDP (RDMA -> RSZ1 -> WROT) writes the luma straight into the caller's buffer and the chroma
 * as 4:2:2 planes, which the cores make 4:2:0; or, without the MDP, the cores do all of it.
 */
#include <linux/clk.h>
#include <linux/compat.h>
#include <linux/completion.h>
#include <linux/delay.h>
#include <linux/dma-mapping.h>
#include <linux/gpio/consumer.h>
#include <linux/i2c.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/kthread.h>
#include <linux/mfd/syscon.h>
#include <linux/mm.h>
#include <linux/sched.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/of_address.h>
#include <linux/of_platform.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/regmap.h>
#include <linux/regulator/consumer.h>
#include <linux/reset.h>
#include <linux/scatterlist.h>
#include <linux/uaccess.h>
#include <linux/unaligned.h>
#include <asm/simd.h>
#include <media/v4l2-ctrls.h>
#include <media/v4l2-device.h>
#include <media/v4l2-ioctl.h>
#include <media/videobuf2-vmalloc.h>

#include "ov02b10-init.h"

#define WIDTH 1600
#define HEIGHT 1200
#define STRIDE (WIDTH * 10 / 8)
#define FRAME_BYTES (STRIDE * HEIGHT)
#define DMA_SLOTS 3
#define DMA_BYTES PAGE_ALIGN(FRAME_BYTES * DMA_SLOTS)
#define YUYV_BYTES (WIDTH * 2 * HEIGHT)
#define I420_BYTES (WIDTH * HEIGHT * 3 / 2)
#define Y_BYTES (WIDTH * HEIGHT)
#define CHROMA_BYTES (WIDTH * HEIGHT)	/* 4:2:2: two planes of WIDTH / 2 x HEIGHT */
#define I420_PAGES (I420_BYTES / PAGE_SIZE + 2)

/*
 * CRONOS_IOC_DEVELOP, on the video node: develop one frame on the ISP. raw points at a packed
 * RAW10 frame as the node delivers it (STRIDE bytes a line), dst at I420_BYTES for the picture
 * (BT.601 video range). The tone, in the order the ISP applies it: black is added to every sample
 * on the ISP's 12-bit scale (a 10-bit sample times 4); gain[] multiplies red, green on red rows,
 * green on blue rows and blue, in 1/512 (at most 256x), clipping at white (4095); curve[] maps the
 * result to the output, 10 bits (1020 is white), at the inputs 0, 8, ..., 512, 528, ..., 1024,
 * 1056, ..., 2048, 2176, ..., 4096, linear between them. flags must be 0. hw_us comes back with the
 * time the ISP took. Errors: EINVAL (sizes, flags, values), EFAULT, ETIMEDOUT (no frame window or
 * the ISP did not finish), EIO (the ISP reported an error; the capture is reset and goes on). A dst
 * on a cache line's boundary (a page's, as large buffers are) lets the MDP write the luma into it.
 */
struct cronos_develop {
	__u64 raw, dst;
	__u32 raw_size, dst_size;
	__s32 black;
	__u32 gain[4];
	__u16 curve[145];
	__u16 reserved;
	__u32 flags;
	__u32 hw_us;
};
#define CRONOS_IOC_DEVELOP _IOWR('V', BASE_VIDIOC_PRIVATE + 0, struct cronos_develop)

struct isp_buffer {
	void *va;
	dma_addr_t iova;
	size_t size;
	enum dma_data_direction dir;
};

struct camera_buffer {
	struct vb2_v4l2_buffer vb;
	struct list_head list;
};
struct cronos_camera {
	struct device *dev, *larb;
	void __iomem *cam, *sen, *ana;
	struct clk_bulk_data clks[4];
	int nclks;
	struct regulator *avdd, *iovdd;
	struct gpio_desc *reset, *standby;
	struct i2c_client *sensor;
	struct v4l2_device v4l2;
	struct video_device video;
	struct v4l2_ctrl_handler controls;
	struct vb2_queue queue;
	struct mutex lock, sensor_lock;
	spinlock_t buffers_lock;
	struct list_head buffers;
	struct task_struct *thread;
	void *dma;
	dma_addr_t dma_addr;
	u32 sequence;
	bool powered;

	/* Pass 2. isp_lock owns the CAM registers against power changes and between developments. */
	struct mutex isp_lock;
	struct isp_buffer imgi, img2o;
	void *tdr;		/* the tile table, and a target for the DMA bases pass 2 leaves unused */
	dma_addr_t tdr_addr;
	u8 *rows;		/* lines of I420 on their way to the caller */
	/* A development handed to the capture thread, which runs it in the next frame's blanking. */
	atomic_t p2_state;
	struct completion p2_done;
	int p2_result;
	bool p2_pgn;
	bool p2_link;		/* this development goes straight into the MDP (route A) */
	unsigned long p2_last;	/* jiffies of the last development */
	u32 p2_lines;		/* the frame length the sensor keeps while developing; 0: its own */
	u32 shutter;		/* the exposure control, in lines */
	bool p2_failed;		/* pass 2 failed beside pass 1: not again until the stream restarts */
	atomic_t drop;		/* frames the thread throws away (pass 2 ran into a frame) */

	/* The MDP, for the I420 out; mdp is false where the node does not describe it. */
	bool mdp, mdp_failed;	/* failed: the cores make the I420 until the stream restarts */
	/* Route A: pass 2 straight into the MDP. failed: route B (or the cores) until the restart. */
	bool link, link_failed;
	struct clk *camin;		/* CAM_MDP, the link's gate in mmsys */
	void __iomem *rdma, *rsz, *wrot, *mutex, *larb0_mmu;
	struct regmap *mmsys;
	struct device *larb0;
	struct clk_bulk_data mdp_clks[3];
	struct reset_control *mdp_reset;
	struct isp_buffer chroma;	/* WROT's 4:2:2 chroma planes, U then V */
	struct page **pages;		/* the caller's I420 (or its luma), pinned for WROT */
};

static void mask(void __iomem *base, u32 off, u32 clear, u32 set)
{
	writel((readl(base + off) & ~clear) | set, base + off);
}

static int sensor_write(struct cronos_camera *c, u8 reg, u8 value)
{
	return i2c_smbus_write_byte_data(c->sensor, reg, value);
}

static int sensor_exposure(struct cronos_camera *c, u32 shutter)
{
	u32 blank = max3(1235U, READ_ONCE(c->p2_lines), shutter + 7) - 1220;
	int ret;

	ret = sensor_write(c, 0xfd, 1);
	if (!ret) ret = sensor_write(c, 0x14, blank >> 8);
	if (!ret) ret = sensor_write(c, 0x15, blank);
	if (!ret) ret = sensor_write(c, 0x0e, shutter >> 8);
	if (!ret) ret = sensor_write(c, 0x0f, shutter);
	if (!ret) ret = sensor_write(c, 0xfe, 2);
	return ret;
}

static int camera_control(struct v4l2_ctrl *ctrl)
{
	struct cronos_camera *c = container_of(ctrl->handler, struct cronos_camera, controls);
	int ret = 0;

	mutex_lock(&c->sensor_lock);
	if (c->powered) {
		if (ctrl->id == V4L2_CID_EXPOSURE) {
			c->shutter = ctrl->val;
			ret = sensor_exposure(c, ctrl->val);
		} else if (ctrl->id == V4L2_CID_ANALOGUE_GAIN) {
			ret = sensor_write(c, 0xfd, 1);
			if (!ret) ret = sensor_write(c, 0x22, ctrl->val / 4);
			if (!ret) ret = sensor_write(c, 0xfe, 2);
		}
	}
	mutex_unlock(&c->sensor_lock);
	return ret;
}
static const struct v4l2_ctrl_ops control_ops = { .s_ctrl = camera_control };

static void camera_receiver(struct cronos_camera *c, bool on)
{
	void __iomem *s = c->sen, *a = c->ana;
	unsigned int off;

	if (!on) {
		mask(s, 0x3a0, 0x1f, 0);
		mask(s, 0x360, 0x1f, 0);
		mask(a, 0x24, 1, 0);
		mask(a, 0x20, 1, 0);
		for (off = 0; off <= 0x10; off += 4)
			mask(a, off, 1, 0);
		mask(a, 0x4c, 0, 0x1041041);
		mask(a, 0x50, 0, 0x1041041);
		for (off = 0; off <= 0x10; off += 4)
			mask(a, off, BIT(3), 0);
		return;
	}
	mask(a, 0x4c, ~0xfefbefbeU, 0);
	mask(a, 0x50, ~0xfefbefbeU, 0);
	for (off = 0; off <= 0x10; off += 4)
		mask(a, off, 0, BIT(3));
	mask(a, 0x24, 0, 1);
	usleep_range(30, 60);
	mask(a, 0x20, 0, 3);
	udelay(1);
	for (off = 0; off <= 0x10; off += 4)
		mask(a, off, 0, 1);
	writel(0x1f, s + 0x3d8);
	mask(s, 0x338, 0, 1);
	writel(0x1541, s + 0x33c);
	mask(s, 0x338, 0, BIT(2));
	usleep_range(500, 600);
	mask(s, 0x338, 1, 0);
	writel(0, s + 0x3d8);
	mask(s, 0x120, (0xf << 12) | BIT(8) | (3U << 28) |
		(0x3f << 22) | (0x3f << 16) | BIT(10) | BIT(9),
		BIT(31) | (8 << 12) | BIT(28) | (0x3b << 22) | (0x3f << 16));
	mask(s, 0x100, (7U << 28) | (0xf << 12), 1 | (8 << 12));
	writel(0, s + 0x3ac);
	mask(s, 0x3a0, 0, BIT(7));
	writel(30 << 8, s + 0x3a8);
	mask(s, 0x3a0, 0, BIT(26) | BIT(16) | BIT(4) | 1);
	writel(0, s + 0x3b0);
	mask(s, 0x120, 0, 3);
	mask(s, 0x120, 3, 0);
	mask(s, 8, 0xf, 0);
}

static void camera_pipeline(struct cronos_camera *c, dma_addr_t base)
{
	void __iomem *p = c->cam;

	writel(5, p + 0x5c);
	writel(4, p + 0x5c);
	writel(0, p + 0x5c);
	writel(0, p + 0x20);
	writel(0, p + 0x414);
	writel(0x1ffff, p + 0x150);
	writel(BIT(0) | BIT(12), p + 4);
	writel(0, p + 8);
	writel(1, p + 0xc);
	writel(BIT(16) | BIT(12), p + 0x10);
	writel(0, p + 0x18);
	writel(0, p + 0x1c);
	mask(p, 0x78, BIT(4), BIT(18));
	writel(0, p + 0xf4);
	writel(base, p + 0x300);
	writel(0, p + 0x304);
	writel(STRIDE - 1, p + 0x308);
	writel(HEIGHT - 1, p + 0x30c);
	writel(STRIDE, p + 0x310);
	writel((WIDTH << 16) | HEIGHT, p + 0x14c);
	/* Preserve the IMGO FIFO thresholds established by the ISP reset. */
	writel(BIT(0) | BIT(2), p + 0x410);
	writel(BIT(12), p + 0x414);
	writel(WIDTH << 16, p + 0x418);
	writel(HEIGHT << 16, p + 0x41c);
	writel(0, p + 0x420);
}

static void camera_power_off(struct cronos_camera *c)
{
	mutex_lock(&c->sensor_lock);
	c->powered = false;
	mask(c->cam, 0x414, 1, 0);
	writel(0, c->cam + 0xc);
	sensor_write(c, 0xfd, 3);
	sensor_write(c, 0xc2, 0);
	gpiod_set_value_cansleep(c->reset, 1);
	gpiod_set_value_cansleep(c->standby, 1);
	camera_receiver(c, false);
	mask(c->sen, 0x200, BIT(29), 0);
	mutex_unlock(&c->sensor_lock);
	regulator_disable(c->avdd);
	regulator_disable(c->iovdd);
	clk_bulk_disable_unprepare(c->nclks, c->clks);
	pm_runtime_put_sync(c->larb);
	pm_runtime_put_sync(c->dev);
}

static int camera_power_on(struct cronos_camera *c)
{
	unsigned int i;
	int ret, hi, lo;

	ret = pm_runtime_resume_and_get(c->dev);
	if (ret < 0) return ret;
	ret = pm_runtime_resume_and_get(c->larb);
	if (ret < 0) goto put_power;
	ret = clk_bulk_prepare_enable(c->nclks, c->clks);
	if (ret) goto put_larb;
	gpiod_set_value_cansleep(c->reset, 1);
	gpiod_set_value_cansleep(c->standby, 1);
	msleep(10);
	ret = regulator_enable(c->iovdd);
	if (ret) goto clocks;
	usleep_range(2000, 2500);
	ret = regulator_enable(c->avdd);
	if (ret) goto io_power;
	/* 48 MHz camtg divided by two at SENINF TG1 gives 24 MHz CMMCLK. */
	mask(c->sen, 0x200, 0, BIT(31));
	mask(c->sen, 0, 0xc00, 0x300);
	mask(c->sen, 0x204, 0x3f3f3f, 1 | BIT(16));
	mask(c->sen, 0x200, 3 | 4 | BIT(6) | BIT(28), 1 | BIT(28));
	mask(c->sen, 0x120, 0, BIT(31));
	mask(c->sen, 0x100, BIT(31), 1);
	mask(c->sen, 0x200, 0, BIT(29));
	msleep(5);
	gpiod_set_value_cansleep(c->standby, 0);
	msleep(5);
	gpiod_set_value_cansleep(c->reset, 0);
	msleep(10);
	ret = sensor_write(c, 0xfd, 0);
	if (ret) goto off;
	hi = i2c_smbus_read_byte_data(c->sensor, 2);
	lo = i2c_smbus_read_byte_data(c->sensor, 3);
	if (hi < 0 || lo < 0 || ((hi << 8) | lo) != 0x2b) {
		ret = hi < 0 ? hi : lo < 0 ? lo : -ENODEV;
		dev_err(c->dev, "OV02B10 identification failed: %d/%d\n", hi, lo);
		goto off;
	}
	for (i = 0; i < ARRAY_SIZE(sensor_init); i++) {
		if (sensor_init[i].reg == 0xffff) {
			usleep_range(sensor_init[i].value, sensor_init[i].value + 200);
			continue;
		}
		ret = sensor_write(c, sensor_init[i].reg, sensor_init[i].value);
		if (ret) goto off;
	}
	camera_pipeline(c, c->dma_addr);
	camera_receiver(c, true);
	c->powered = true;
	ret = v4l2_ctrl_handler_setup(&c->controls);
	if (ret) goto off;
	ret = sensor_write(c, 0xfd, 3);
	if (!ret) ret = sensor_write(c, 0xc2, 1);
	if (ret) goto off;
	return 0;
off:
	camera_power_off(c);
	return ret;
io_power:
	regulator_disable(c->iovdd);
clocks:
	clk_bulk_disable_unprepare(c->nclks, c->clks);
put_larb:
	pm_runtime_put_sync(c->larb);
put_power:
	pm_runtime_put_sync(c->dev);
	return ret;
}

static void return_buffers(struct cronos_camera *c, enum vb2_buffer_state state)
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

static void develop_in_blanking(struct cronos_camera *c, s64 frame_ns);
static void end_development_frames(struct cronos_camera *c);

/*
 * The TG's counter moves when a frame's last line is in, and the next frame's first line follows
 * the sensor's blanking later: 0.8 ms at 30 frames a second. IMGO takes its address at that first
 * line, so the next slot has to be set before it; and pass 2 runs in the blanking too. Near the
 * expected end of a frame (within 2 ms of it) the thread looks every 20-40 us, elsewhere every
 * 0.5-1 ms.
 */
static int camera_thread(void *data)
{
	struct cronos_camera *c = data;
	unsigned int last, count, slot = 0, ready;
	unsigned long flags, deadline = jiffies + msecs_to_jiffies(3000);
	ktime_t frame_end = 0;
	s64 frame_ns = 0, since;
	bool first = true, skip;

	last = (readl(c->cam + 0x44c) >> 16) & 0xff;
	mask(c->cam, 0x414, 0, 1);
	while (!kthread_should_stop()) {
		struct camera_buffer *b = NULL;
		ktime_t now;

		count = (readl(c->cam + 0x44c) >> 16) & 0xff;
		if (count == last) {
			if (time_after(jiffies, deadline)) {
				dev_err(c->dev, "capture timed out\n");
				vb2_queue_error(&c->queue);
				break;
			}
			/* An idle sleep: an uninterruptible one counts in the load average for as
			 * long as the camera runs, a whole CPU's worth while it waits on frames. */
			/* Fast only within 2 ms either side of where the frame was due: a frame the
			 * exposure has just made longer (up to 0.43 s in a dark room) is followed by a
			 * blanking as long, which the slow look still lands in. Looking every 20-40 us
			 * for the whole of a long frame woke this real-time thread 25 000 times a
			 * second and starved the rest of the CPU it ran on (the screen's helper). */
			since = ktime_to_ns(ktime_sub(ktime_get(), frame_end));
			if (frame_ns && since > frame_ns - 2 * NSEC_PER_MSEC &&
			    since < frame_ns + 2 * NSEC_PER_MSEC)
				usleep_range_idle(20, 40);
			else
				usleep_range_idle(500, 1000);
			continue;
		}
		deadline = jiffies + msecs_to_jiffies(3000);
		last = count;
		ready = slot;
		slot = (slot + 1) % DMA_SLOTS;
		writel(c->dma_addr + slot * FRAME_BYTES, c->cam + 0x300);
		now = ktime_get();
		frame_ns = frame_end ? ktime_to_ns(ktime_sub(now, frame_end)) : 0;
		frame_end = now;
		if (first) { first = false; continue; }
		skip = atomic_add_unless(&c->drop, -1, 0);
		develop_in_blanking(c, frame_ns);
		end_development_frames(c);
		if (skip) continue;
		spin_lock_irqsave(&c->buffers_lock, flags);
		if (!list_empty(&c->buffers)) {
			b = list_first_entry(&c->buffers, struct camera_buffer, list);
			list_del(&b->list);
		}
		spin_unlock_irqrestore(&c->buffers_lock, flags);
		if (!b) continue;
		dma_rmb();
		memcpy(vb2_plane_vaddr(&b->vb.vb2_buf, 0),
			c->dma + ready * FRAME_BYTES, FRAME_BYTES);
		b->vb.sequence = c->sequence++;
		b->vb.field = V4L2_FIELD_NONE;
		b->vb.vb2_buf.timestamp = ktime_get_ns();
		vb2_set_plane_payload(&b->vb.vb2_buf, 0, FRAME_BYTES);
		vb2_buffer_done(&b->vb.vb2_buf, VB2_BUF_STATE_DONE);
	}
	mask(c->cam, 0x414, 1, 0);
	/* Keep the task alive until its owner joins it, including capture errors. */
	while (!kthread_should_stop())
		schedule_timeout_idle(msecs_to_jiffies(100));
	return 0;
}

/*
 * ISP pass 2, memory to memory (research/mt8163-isp-pass2.md has how each part was found): IMGI
 * reads the packed frame, UNP unpacks it, OBC adds the black offset and the per-channel gains,
 * PGN takes the gain OBC's 16x cannot, CFA demosaics, GGM applies the curve, G2C makes BT.601
 * video range (the chroma signed), C42 makes it 4:2:2 and IMG2O writes YUYV. The line buffers
 * hold 768 pixels, so the frame goes as three tiles, from a tile table (TDRI) the ISP reads.
 *
 * Pass 1 shares the CAM's control registers, and the two cannot move data at once: pass 2 stops
 * with a DMA error while IMGO (pass 1's DMA) is enabled, and so does pass 1's frame if IMGO is
 * turned off under it. So pass 2 runs in the sensor's vertical blanking, from the end of one
 * frame to the first line of the next, with pass 1's registers double-buffered meanwhile (SEL bit
 * 4, PASS1_DB_EN): pass 1 keeps what it took at its last first line, pass 2 has the registers,
 * and they are put back before the next first line takes them. The blanking at 30 frames a
 * second is 0.8 ms and pass 2 takes 5.4, so while frames are being developed the sensor's frame
 * is made P2_FRAME_LINES long (25.7 a second; it goes back a second after the last one).
 */
#define CAM_START	0x000
#define CAM_EN1		0x004
#define CAM_EN2		0x008
#define CAM_DMA_EN	0x00c
#define CAM_FMT_SEL	0x010
#define CAM_SEL		0x018
#define CAM_PIX_ID	0x01c
#define CAM_INT_EN	0x020
#define CAM_INT_STATUS	0x024	/* clears on read */
#define CAM_TILE	0x050
#define CAM_TCM_EN	0x054

#define P1_EN1		(BIT(0) | BIT(12))	/* TG1, PAK */
#define P1_DMA_EN	BIT(0)			/* IMGO */
#define P1_FMT_SEL	(BIT(16) | BIT(12))
#define SEL_P1_DB	BIT(4)
#define SEL_TDR_SIZES	BIT(6)

#define P2_EN1_OBC	BIT(3)
#define P2_EN1_PGN	BIT(11)
#define P2_EN1_UNP	BIT(20)
#define P2_EN1_CFA	BIT(21)
#define P2_EN2		(BIT(0) | BIT(1))	/* G2C, C42 */
#define P2_DMA_EN	(BIT(7) | BIT(10))	/* IMGI, IMG2O */
#define P2_FMT_SEL	(P1_FMT_SEL | BIT(8) | 3)	/* RAW10 in, scenario 3 (memory to memory) */
#define INT_SOF		BIT(12)
#define INT_P2_DONE	BIT(14)
#define INT_P2_ERR	(BIT(13) | BIT(23) | BIT(30))	/* CQ, IMG2O, DMA */
#define INT_EN_P2	0x5fbbf41f	/* the status bits latch only where enabled */
#define GGM_RB		0x1000
#define GGM_G		0x1300
#define GGM_EN		0x1600

#define P2_RUN_US	5400
#define P2_TIMEOUT_US	15000
/*
 * A frame's lines take 32.6 ms to come in (1200 of 27 us); the rest of a frame is blanking. Pass
 * 2 runs where a frame was this long at least, and the sensor's frame is held at P2_FRAME_LINES
 * (38.9 ms, 6.3 ms of blanking) while it develops.
 */
#define P2_FRAME_LINES	1440
#define P2_MIN_FRAME_NS	(38400 * NSEC_PER_USEC)

enum { P2_IDLE, P2_PENDING, P2_RUNNING };

/*
 * The tile table for 1600x1200 RAW10 -> YUYV, red first (isp-user's isp2cmp tdr): per tile its
 * control word (edges, PIX_ID 3, IMG2O, LAST_IRQ on the last), IMGI offset and size, IMG2O offset
 * and size, and the unpacker's start and end bits.
 */
static const u32 tile_table[] = {
	0x0008030d, 0x00000000, 0x04af0069, 0x00000000, 0x04af009f, 0x00080000,
	0x0008030c, 0x0000005c, 0x04af03c1, 0x000000a0, 0x04af05eb, 0x00040004,
	0x0008032e, 0x00000410, 0x04af03bf, 0x0000068c, 0x04af05f3, 0x00000000,
};
#define TDR_BYTES 4096

/*
 * Route A, the direct link: pass 2 (G2C, C42 and the chroma resampler, CRSP) leaves the ISP
 * through its MDP crop, into the MDP's CAMIN, RSZ1 (passed through) and WROT, which writes the
 * caller's I420 itself: the link carries chroma around 128, so WROT's own 4:2:0 filter averages
 * it as the cores do (research/mt8163-isp-pass2.md, "Stage 5"). The table and the tiles are
 * DpFramework's for this path (DpIspStream, captured by isp-user's dpcap), three tiles of 762, 756
 * and 82 pixels, each a pass-2 start of its own (LAST_TILE in each), with the MDP set up for it
 * before: per tile its control word (edges, PIX_ID 3, RSP and MDP crop), IMGI offset and size, the
 * unpacker's bits, and the MDP crop's columns and rows, which the CPU writes as well.
 */
static const u32 link_table[] = {
	0x0012d32d, 0x00000000, 0x04af03bf, 0x00000000, 0x02f90000, 0x04af0000,
	0x0012c32c, 0x000003b1, 0x04af03bf, 0x00000000, 0x02f30000, 0x04af0000,
	0x0012e32e, 0x00000762, 0x04af006d, 0x00000000, 0x00510000, 0x04af0000,
};
#define LINK_TDR_OFF	512
#define LINK_TILE_WORDS	6

static const struct link_tile {
	u16 x, w;
	u32 crop_x;			/* CAM_CROP_X: the columns, MDP_CROP_EN */
	u32 rsz_in, rsz_out, wrot_buf;	/* DpFramework's for the tile */
	unsigned int us;		/* how long pass 2 takes it */
} link_tiles[] = {
	{ 0, 762, 0x02f98000, 0x84b002fa, 0x84b002fa, 0x02fa1400, 2580 },
	{ 762, 756, 0x02f38000, 0xc4b002f4, 0x84b002f4, 0x02f41400, 2560 },
	{ 1518, 82, 0x00518000, 0xc4b00052, 0x04b00052, 0x00524000, 330 },
};
#define LINK_CROP_Y	0x04af0000	/* rows 0..1199 */
#define CAM_CROP_X	0x110
#define CAM_CROP_Y	0x114
#define P2_EN2_CRSA	BIT(23)		/* UV_CRSA_EN: the chroma resampler, on the way to the MDP */
#define P2_LINK_DMA_EN	BIT(7)		/* IMGI only */
#define PIX_ID_MDP_SEL	BIT(20)		/* pass 2's output to the MDP */
#define TCM_LINK	0x98100003	/* TDR_EN, CQ_APB_2T, TCM_LOAD_EN, UNP, IMGI, CTL: the vendor's */

static int link_tile_start(struct cronos_camera *c, const struct link_tile *t);
static int link_tile_end(struct cronos_camera *c);

/* BT.601 video range in 1/512 (G2C), the luma offset in quarters of a code: Y, Cb, Cr rows. */
static const s16 g2c[3][4] = {
	{ 132, 258, 50, 64 }, { -76, -148, 224, 0 }, { 224, -188, -36, 0 },
};

static void isp_reset(struct cronos_camera *c)
{
	writel(5, c->cam + 0x5c);
	writel(4, c->cam + 0x5c);
	writel(0, c->cam + 0x5c);
}

static void free_isp_buffer(struct device *dev, struct isp_buffer *b)
{
	if (b->va)
		dma_free_noncoherent(dev, b->size, b->va, b->iova, b->dir);
	b->va = NULL;
}

static void free_isp_buffers(void *data)
{
	struct cronos_camera *c = data;

	free_isp_buffer(c->dev, &c->imgi);
	free_isp_buffer(c->dev, &c->img2o);
	free_isp_buffer(c->dev, &c->chroma);
	kfree(c->pages);
	c->pages = NULL;
	if (c->tdr)
		dma_free_coherent(c->dev, TDR_BYTES, c->tdr, c->tdr_addr);
	c->tdr = NULL;
	kfree(c->rows);
	c->rows = NULL;
}

/* The pass-2 buffers, the first time: not at probe, as the shared IOMMU is left alone then. */
static int alloc_isp_buffers(struct cronos_camera *c)
{
	if (c->rows)
		return 0;
	c->imgi = (struct isp_buffer) { .size = PAGE_ALIGN(FRAME_BYTES), .dir = DMA_TO_DEVICE };
	c->img2o = (struct isp_buffer) { .size = PAGE_ALIGN(YUYV_BYTES), .dir = DMA_FROM_DEVICE };
	c->imgi.va = dma_alloc_noncoherent(c->dev, c->imgi.size, &c->imgi.iova, c->imgi.dir, GFP_KERNEL);
	c->img2o.va = dma_alloc_noncoherent(c->dev, c->img2o.size, &c->img2o.iova, c->img2o.dir,
					    GFP_KERNEL);
	c->tdr = dma_alloc_coherent(c->dev, TDR_BYTES, &c->tdr_addr, GFP_KERNEL);
	c->rows = kmalloc(WIDTH * 4, GFP_KERNEL);	/* copy_i420's two lines, or CHROMA_LINES */
	if (c->mdp) {
		c->chroma = (struct isp_buffer) { .size = PAGE_ALIGN(CHROMA_BYTES), .dir = DMA_FROM_DEVICE };
		c->chroma.va = dma_alloc_noncoherent(c->dev, c->chroma.size, &c->chroma.iova,
						     c->chroma.dir, GFP_KERNEL);
		c->pages = kmalloc_array(I420_PAGES, sizeof(*c->pages), GFP_KERNEL);
		if (!c->chroma.va || !c->pages)
			c->mdp = false;	/* the cores make the I420 */
	}
	if (!c->imgi.va || !c->img2o.va || !c->tdr || !c->rows) {
		free_isp_buffers(c);
		return -ENOMEM;
	}
	memcpy(c->tdr, tile_table, sizeof(tile_table));
	memcpy(c->tdr + LINK_TDR_OFF, link_table, sizeof(link_table));
	/* Whatever the allocation left in the caches goes now; the devices write these. */
	dma_sync_single_for_device(c->dev, c->img2o.iova, YUYV_BYTES, DMA_FROM_DEVICE);
	if (c->mdp)
		dma_sync_single_for_device(c->dev, c->chroma.iova, CHROMA_BYTES, DMA_FROM_DEVICE);
	return devm_add_action_or_reset(c->dev, free_isp_buffers, c);
}

/*
 * What pass 1 never reads: the DMA addresses and sizes of pass 2's ports, the tone's blocks and
 * the curve. Written outside the window, with pass 1 running or not.
 */
static void program_tone(struct cronos_camera *c, const struct cronos_develop *d)
{
	void __iomem *p = c->cam;
	static const int order[4] = { 3, 2, 1, 0 };	/* the ISP's B, Gb, Gr, R */
	u32 pgn[4], ofs = d->black & 0x1fff;
	unsigned int i;

	writel(0x0fff0fff, p + 0x880);
	writel(0x0fff0fff, p + 0x884);
	for (i = 0; i < 4; i++) {
		u32 g = d->gain[order[i]];

		pgn[i] = g <= 8191 ? 512 : min(g / 8, 8191U);
		writel(ofs, p + 0x500 + 4 * i);
		writel(g <= 8191 ? g : 4096, p + 0x510 + 4 * i);
	}
	writel(pgn[0] | pgn[1] << 16, p + 0x888);
	writel(pgn[2] | pgn[3] << 16, p + 0x88c);
	for (i = 0; i < 3; i++) {
		writel((g2c[i][0] & 0x7ff) | (g2c[i][1] & 0x7ff) << 16, p + 0xa00 + 8 * i);
		writel((g2c[i][2] & 0x7ff) | (g2c[i][3] & 0x7ff) << 16, p + 0xa04 + 8 * i);
	}
	writel(0, p + 0xa1c);
	/* The curve: each entry its output and, in [15:10], the rise to the next (6 bits). */
	for (i = 0; i < 144; i++) {
		s32 v = d->curve[i], next = d->curve[i + 1];
		u32 w;

		/* Only the first entry of a steep curve rises more: start it higher instead,
		 * which lifts only what is at or below black. */
		v = max(v, next - 63);
		w = v | clamp(next - v, 0, 63) << 10;
		writel(w | w << 16, p + GGM_RB + 4 * i);
		writel(w, p + GGM_G + 4 * i);
	}
	writel(c->imgi.iova, p + 0x230);
	writel(0, p + 0x234);
	writel(STRIDE - 1, p + 0x238);
	writel(HEIGHT - 1, p + 0x23c);
	writel(STRIDE, p + 0x240);
	writel(c->img2o.iova, p + 0x320);
	writel(0, p + 0x324);
	writel(WIDTH * 2 - 1, p + 0x328);
	writel(HEIGHT - 1, p + 0x32c);
	writel(WIDTH * 2, p + 0x330);
	writel(c->tdr_addr, p + 0x204);
	writel(0, p + 0x208);
	writel(sizeof(tile_table) - 1, p + 0x20c);
	/* LSCI, ESFKO and AAO stay disabled; aimed at something mapped all the same. */
	writel(c->tdr_addr, p + 0x26c);
	writel(c->tdr_addr, p + 0x36c);
	writel(c->tdr_addr, p + 0x388);
}

/*
 * The window: the control registers for pass 2, the start, and pass 1's values put back. The
 * status words clear on read; nothing else reads them. Returns the ISP's time in microseconds or
 * an error; *late says a frame's first line came while pass 2 had the registers.
 */
static int run_pass2_link(struct cronos_camera *c, u32 *status);

static int run_pass2(struct cronos_camera *c, bool pgn, bool streaming, bool link, bool *late)
{
	void __iomem *p = c->cam;
	u32 status = 0, en1 = P2_EN1_UNP | P2_EN1_CFA | P2_EN1_OBC | (pgn ? P2_EN1_PGN : 0);
	u32 sel = streaming ? SEL_P1_DB : 0;
	ktime_t start, end;
	int ret = -ETIMEDOUT;
	unsigned int i;

	writel(sel, p + CAM_SEL);
	writel(INT_EN_P2, p + CAM_INT_EN);
	readl(p + CAM_INT_STATUS);
	for (i = 0; i < 6; i++)		/* no command queues */
		writel(0, p + 0x0a8 + 4 * i);
	writel(P2_FMT_SEL, p + CAM_FMT_SEL);
	writel(en1, p + CAM_EN1);
	writel(1, p + GGM_EN);
	if (link) {
		ret = run_pass2_link(c, &status);
		goto restore;
	}
	writel(P2_EN2, p + CAM_EN2);
	writel(P2_DMA_EN, p + CAM_DMA_EN);
	writel(0, p + CAM_PIX_ID);
	/* Every edge and LAST_IRQ, as for one tile; the table's words replace it tile by tile. */
	writel(0x32f, p + CAM_TILE);
	writel(BIT(31), p + CAM_TCM_EN);	/* TDR_EN */
	writel(sel | SEL_TDR_SIZES, p + CAM_SEL);
	start = ktime_get();
	end = ktime_add_us(start, P2_TIMEOUT_US);
	writel(1, p + CAM_START);
	usleep_range(P2_RUN_US - 400, P2_RUN_US - 200);
	for (;;) {
		status |= readl(p + CAM_INT_STATUS);
		if (status & INT_P2_ERR) {
			ret = -EIO;
			break;
		}
		if (status & INT_P2_DONE) {
			ret = ktime_us_delta(ktime_get(), start);
			break;
		}
		if (ktime_after(ktime_get(), end))
			break;
		usleep_range(50, 100);
	}
restore:
	if (ret < 0) {	/* where it stopped: the debug port's IMGI, CFA, YUV and output stages */
		u32 dbg[4];

		for (i = 0; i < 4; i++) {
			writel(0x9003 + 3 * i, p + 0x160);
			dbg[i] = readl(p + 0x164);
		}
		dev_warn_ratelimited(c->dev, "pass 2: %s, status %#x, stages %08x %08x %08x %08x, tile %#x\n",
				     ret == -EIO ? "error" : "timed out", status, dbg[0], dbg[1],
				     dbg[2], dbg[3], readl(p + CAM_TILE));
	}
	/* Pass 1's values, taken at the next first line (or now, with pass 1 stopped). */
	writel(0, p + GGM_EN);
	writel(0, p + CAM_TCM_EN);
	writel(0, p + CAM_CROP_X);
	writel(0, p + CAM_CROP_Y);
	writel(0, p + CAM_EN2);
	writel(P1_EN1, p + CAM_EN1);
	writel(P1_DMA_EN, p + CAM_DMA_EN);
	writel(P1_FMT_SEL, p + CAM_FMT_SEL);
	writel(0, p + CAM_PIX_ID);
	writel(sel, p + CAM_SEL);
	status |= readl(p + CAM_INT_STATUS);
	writel(0, p + CAM_INT_EN);
	writel(0, p + CAM_SEL);
	*late = status & INT_SOF;
	return ret;
}

/*
 * Pass 2 into the MDP (route A), its control registers set but for the outputs: a start a tile,
 * the MDP set up for the tile before it and done with it after. Returns the ISP's time in
 * microseconds or an error.
 */
static int run_pass2_link(struct cronos_camera *c, u32 *status)
{
	void __iomem *p = c->cam;
	ktime_t start = ktime_get(), end;
	unsigned int i;
	int ret = 0;

	writel(P2_EN2 | P2_EN2_CRSA, p + CAM_EN2);
	writel(P2_LINK_DMA_EN, p + CAM_DMA_EN);
	writel(PIX_ID_MDP_SEL | 3, p + CAM_PIX_ID);
	writel(TCM_LINK, p + CAM_TCM_EN);
	writel(readl(p + CAM_SEL) | SEL_TDR_SIZES, p + CAM_SEL);
	writel(LINK_CROP_Y, p + CAM_CROP_Y);
	writel(LINK_TILE_WORDS * 4 - 1, p + 0x20c);
	for (i = 0; !ret && i < ARRAY_SIZE(link_tiles); i++) {
		const struct link_tile *t = &link_tiles[i];
		u32 st = 0;

		ret = link_tile_start(c, t);
		if (ret)
			break;
		writel(link_table[i * LINK_TILE_WORDS], p + CAM_TILE);
		writel(t->crop_x, p + CAM_CROP_X);
		writel(c->tdr_addr + LINK_TDR_OFF + i * LINK_TILE_WORDS * 4, p + 0x204);
		end = ktime_add_us(ktime_get(), P2_TIMEOUT_US);
		writel(1, p + CAM_START);
		usleep_range(t->us - 200, t->us - 50);
		for (;;) {
			st |= readl(p + CAM_INT_STATUS);
			if (st & INT_P2_ERR) {
				ret = -EIO;
				break;
			}
			if (st & INT_P2_DONE)
				break;
			if (ktime_after(ktime_get(), end)) {
				ret = -ETIMEDOUT;
				break;
			}
			usleep_range(20, 50);
		}
		*status |= st;
		if (!ret)
			ret = link_tile_end(c);
	}
	return ret ?: ktime_us_delta(ktime_get(), start);
}

/* After pass 2 failed with pass 1 running: the whole CAM reset, and pass 1 set up as it was. */
static void recover(struct cronos_camera *c)
{
	dma_addr_t base = readl(c->cam + 0x300);

	camera_pipeline(c, base);
	mask(c->cam, 0x414, 0, 1);
}

/* The sensor's frame length for developing, or its own, at the exposure it has. */
static void develop_frames(struct cronos_camera *c, bool on)
{
	if (!!READ_ONCE(c->p2_lines) == on)
		return;
	mutex_lock(&c->sensor_lock);
	WRITE_ONCE(c->p2_lines, on ? P2_FRAME_LINES : 0);
	if (c->powered && c->shutter)
		sensor_exposure(c, c->shutter);
	mutex_unlock(&c->sensor_lock);
}

/* A second after the last development the sensor's frame goes back to its own length. */
static void end_development_frames(struct cronos_camera *c)
{
	if (READ_ONCE(c->p2_lines) && atomic_read(&c->p2_state) == P2_IDLE &&
	    time_after(jiffies, READ_ONCE(c->p2_last) + HZ))
		develop_frames(c, false);
}

/*
 * The capture thread, a frame's last line just in: a waiting development runs now, when the
 * frame that ended was long enough for the blanking to hold it.
 */
static void develop_in_blanking(struct cronos_camera *c, s64 frame_ns)
{
	bool late = false;
	int ret;

	if (atomic_read(&c->p2_state) != P2_PENDING || frame_ns < P2_MIN_FRAME_NS ||
	    atomic_cmpxchg(&c->p2_state, P2_PENDING, P2_RUNNING) != P2_PENDING)
		return;
	ret = run_pass2(c, c->p2_pgn, true, c->p2_link, &late);
	if (ret < 0) {
		recover(c);
		atomic_set(&c->drop, 2);
	} else if (late) {
		/* The next frame took pass 2's registers at its first line: it is not whole. */
		dev_warn_ratelimited(c->dev, "pass 2 ran into a frame\n");
		atomic_set(&c->drop, 1);
	}
	c->p2_result = ret;
	atomic_set(&c->p2_state, P2_IDLE);
	complete(&c->p2_done);
}

static int isp_power(struct cronos_camera *c, bool on)
{
	int ret;

	if (!on) {
		isp_reset(c);
		clk_bulk_disable_unprepare(c->nclks, c->clks);
		pm_runtime_put_sync(c->larb);
		pm_runtime_put_sync(c->dev);
		return 0;
	}
	ret = pm_runtime_resume_and_get(c->dev);
	if (ret < 0)
		return ret;
	ret = pm_runtime_resume_and_get(c->larb);
	if (ret < 0)
		goto put_dev;
	ret = clk_bulk_prepare_enable(c->nclks, c->clks);
	if (ret)
		goto put_larb;
	isp_reset(c);
	writel(0x1ffff, c->cam + 0x150);
	writel(0, c->cam + 0x410);
	writel(0, c->cam + 0x414);
	return 0;
put_larb:
	pm_runtime_put_sync(c->larb);
put_dev:
	pm_runtime_put_sync(c->dev);
	return ret;
}

/* Bytes 0, 2, 4 and 6 of v: the luma of four YUYV pixels. */
static inline u32 even_bytes(u64 v)
{
	v &= 0x00ff00ff00ff00ffULL;
	v = (v | v >> 8) & 0x0000ffff0000ffffULL;
	return v | v >> 16;
}

/*
 * IMG2O's YUYV to the caller's I420: the luma as it is, the chroma of each two lines averaged
 * (rounded) and moved from two's complement to around 128. Four pixels of each line a step: the
 * chroma, biased by 128 first (s ^ 0x80 is s + 128), is averaged in 16-bit lanes.
 */
static int copy_i420(struct cronos_camera *c, u8 __user *dst)
{
	u8 __user *dy = dst, *du = dst + WIDTH * HEIGHT, *dv = du + WIDTH * HEIGHT / 4;
	u8 *y0 = c->rows, *y1 = y0 + WIDTH, *u = y1 + WIDTH, *v = u + WIDTH / 2;
	const u64 lanes = 0x00ff00ff00ff00ffULL, bias = 0x0080008000800080ULL;
	unsigned int row, x;

	dma_sync_single_for_cpu(c->dev, c->img2o.iova, YUYV_BYTES, DMA_FROM_DEVICE);
	for (row = 0; row < HEIGHT; row += 2) {
		const u64 *a = (const u64 *)((const u8 *)c->img2o.va + row * WIDTH * 2);
		const u64 *b = a + WIDTH * 2 / 8;

		for (x = 0; x < WIDTH / 4; x++) {
			u64 ra = a[x], rb = b[x], ch;

			((u32 *)y0)[x] = even_bytes(ra);
			((u32 *)y1)[x] = even_bytes(rb);
			/* U0 V0 U1 V1 */
			ch = ((((ra >> 8) & lanes) ^ bias) + (((rb >> 8) & lanes) ^ bias) +
			      0x0001000100010001ULL) >> 1;
			((u16 *)u)[x] = (ch & 0xff) | (ch >> 24 & 0xff00);
			((u16 *)v)[x] = (ch >> 16 & 0xff) | (ch >> 40 & 0xff00);
		}
		if (copy_to_user(dy + row * WIDTH, y0, WIDTH * 2) ||
		    copy_to_user(du + row / 2 * WIDTH / 2, u, WIDTH / 2) ||
		    copy_to_user(dv + row / 2 * WIDTH / 2, v, WIDTH / 2))
			return -EFAULT;
	}
	return 0;
}

/*
 * The MDP, memory to memory: RDMA reads IMG2O's YUYV, RSZ1 passes it through, WROT writes the luma
 * into the caller's buffer (its pages pinned and mapped into the IOMMU domain every multimedia
 * master shares) and the chroma as 4:2:2 planes into c->chroma. The cores then average each two
 * chroma lines into the caller's 4:2:0 planes, moving them from the ISP's two's complement to around
 * 128 first: no block of the MDP can (its color matrices all take 128 for no color, and WROT's own
 * 4:2:0 filter averages the bytes as they are, which is wrong where the two lines' chroma differ in
 * sign). The result is the cores' I420 to the bit. research/mt8163-isp-pass2.md ("Stage 4") has how
 * this was found: the register values are what the unit's own DpFramework programs for this blit
 * (YUYV 1600x1200 to I422), captured without hardware; the frame goes as two tiles, 1024 and 576
 * pixels wide.
 */
#define MMSYS_RDMA_MOUT_EN	0x020
#define MMSYS_PRZ1_MOUT_EN	0x028
#define MMSYS_PRZ1_SEL_IN	0x048
#define MMSYS_WROT_SEL_IN	0x054
#define MUTEX_MDP		6	/* the vendor's MDP mutex; mtk-mutex hands the display's out from 0 */
#define MUTEX_EN		(0x20 + 0x20 * MUTEX_MDP)
#define MUTEX_MOD		(0x2c + 0x20 * MUTEX_MDP)
#define MUTEX_SOF		(0x30 + 0x20 * MUTEX_MDP)
#define MUTEX_MOD_MDP		(BIT(0) | BIT(2) | BIT(5))	/* RDMA, RSZ1, WROT */
#define LARB_MMU_EN		0xf00
#define LARB0_MDP_PORTS		(BIT(14) | BIT(16))	/* M4U_PORT_MDP_RDMA, M4U_PORT_MDP_WROT */
#define RDMA_STATE		0x408
#define RDMA_IDLE		0x100
#define WROT_DEBUG_SEL		0x018
#define WROT_DEBUG		0x0d0
#define WROT_LINE_SEL		0x1400	/* the debug word whose [15:0] is the line being written */
#define MDP_TIMEOUT_US		30000
#define Y_SENTINEL		0x00	/* video-range luma is 16..235 */
#define C_SENTINEL		0x80	/* and its chroma -112..112: never -128 */

enum { RDMA, RSZ, WROT };

/* mdp=0 (/sys/module/mt8163_cronos_camera/parameters/mdp) has the cores make the I420: for tests. */
static bool use_mdp = true;
module_param_named(mdp, use_mdp, bool, 0644);

/* link=0 (parameters/link) keeps pass 2 off the direct link (route A): route B, for tests. */
static bool use_link = true;
module_param_named(link, use_link, bool, 0644);

/*
 * The last development's times, in microseconds (parameters/times): the frame in (the copy and
 * the cache clean), the luma pages mapped, the MDP, the pages unmapped and the chroma's cache
 * invalidated, the chroma made 4:2:0 for the caller, and the whole I420 out (MDP or cores). On
 * route A: the frame in, the I420's pages mapped, pass 2 into the MDP, the pages unmapped (and
 * the marks read), 0, and that again, as nothing is left to do after pass 2.
 */
enum { T_IN, T_MAP, T_MDP, T_UNMAP, T_CHROMA, T_OUT, T_COUNT };
static unsigned int times_us[T_COUNT];
module_param_array_named(times, times_us, uint, NULL, 0444);

static void stamp(int slot, ktime_t *t)
{
	ktime_t now = ktime_get();

	times_us[slot] = ktime_us_delta(now, *t);
	*t = now;
}

struct mdp_write {
	u8 block;
	u16 off;
	u32 val, mask;
};

/* The frame's settings, in the order DpFramework writes them (the buffers' addresses apart). */
static const struct mdp_write mdp_frame[] = {
	{ RDMA, 0x270, 0x00000000, 0x00000007 },
	{ RDMA, 0x028, 0x00010071, 0x00030071 },
	{ RDMA, 0x030, 0x00000605, 0x0100f60f },	/* YUYV */
	{ RDMA, 0xf08, 0, ~0U }, { RDMA, 0xf10, 0, ~0U },
	{ RDMA, 0x108, 0, ~0U }, { RDMA, 0x110, 0, ~0U },
	{ RDMA, 0x060, WIDTH * 2, 0x001fffff },		/* the line, in bytes */
	{ RDMA, 0x090, 0x00000000, 0x001fffff },
	{ RDMA, 0x200, 0x00000000, 0x0f110000 },	/* no color transform */
	{ RDMA, 0x0c8, 0x00000000, 0x0000ffff },
	{ RDMA, 0x0e0, 0x00001200, 0x0000ffff },
	{ RDMA, 0x248, 0x08080808, ~0U }, { RDMA, 0x258, 0x04040404, ~0U },
	{ RDMA, 0x268, 0x04040404, ~0U },
	{ RDMA, 0x240, 0x07000024, 0x0700007f }, { RDMA, 0x250, 0x03000013, 0x0700003f },
	{ RDMA, 0x260, 0x03000013, 0x0700001f },
	{ RSZ, 0x000, 0x00000000, 0x00000001 },		/* RSZ1 bypassed */
	{ WROT, 0x000, 0x80005009, 0xf131510f },	/* I422, three planes */
	{ WROT, 0x074, 0x0006005c, 0x00ffffff },
	{ WROT, 0x030, WIDTH, 0x0000ffff },		/* the planes' strides */
	{ WROT, 0x03c, WIDTH / 2, 0x0000ffff },
	{ WROT, 0x06c, WIDTH / 2, 0x0000ffff },
	{ WROT, 0x084, 0x00000000, 0x000000f3 },	/* no color matrix */
	{ WROT, 0x054, 0xff000000, 0xff000000 },
	{ WROT, 0x070, 0x80000000, 0x80000000 },
	{ WROT, 0x07c, 0x00900000, 0x00900000 },
};

/* A tile's own settings: its left edge and width, and what DpFramework computes for them. */
static const struct mdp_tile {
	u16 x, w;
	u32 rsz_in, rsz_out, wrot_buf, rdma[5];	/* RDMA 0x0c0, 0x088, 0x0d0, 0x0d8, 0x0e8 */
	unsigned int us;			/* how long it takes */
} mdp_tiles[] = {
	{ 0, 1024, 0x84b00400, 0x84b00400, 0x04000800,
	  { 0x00000002, 0x00800004, 0x04000200, 0x00000002, 0x02000100 }, 3400 },
	{ 1024, 576, 0xc4b00240, 0x04b00240, 0x02401400,
	  { 0x00000004, 0x00480008, 0x02400090, 0x00000004, 0x01200048 }, 1900 },
};

static void __iomem *mdp_block(struct cronos_camera *c, int block)
{
	return block == RDMA ? c->rdma : block == RSZ ? c->rsz : c->wrot;
}

static void mdp_mask(void __iomem *p, u32 val, u32 mask)
{
	writel(mask == ~0U ? val : (readl(p) & ~mask) | (val & mask), p);
}

static int mdp_poll(void __iomem *p, u32 mask, u32 val, unsigned int us)
{
	u32 v;

	return readl_poll_timeout(p, v, (v & mask) == val, 5, us);
}

static void mdp_routes(struct cronos_camera *c, bool on)
{
	regmap_write(c->mmsys, MMSYS_RDMA_MOUT_EN, on ? 2 : 0);	/* RDMA -> RSZ1 */
	regmap_write(c->mmsys, MMSYS_PRZ1_MOUT_EN, on ? 2 : 0);	/* RSZ1 -> WROT */
	regmap_write(c->mmsys, MMSYS_PRZ1_SEL_IN, on ? 1 : 0);	/* RSZ1 <- RDMA */
	regmap_write(c->mmsys, MMSYS_WROT_SEL_IN, on ? 1 : 0);	/* WROT <- RSZ1 */
}

/* The three engines stopped, whatever state they are in, and the path taken down. */
static void mdp_stop(struct cronos_camera *c)
{
	writel(0, c->mutex + MUTEX_EN);
	writel(0, c->mutex + MUTEX_MOD);
	reset_control_reset(c->mdp_reset);
	mdp_routes(c, false);
}

/* The engines' own resets, then the frame. */
static int mdp_frame_setup(struct cronos_camera *c, dma_addr_t y, dma_addr_t u, dma_addr_t v)
{
	unsigned int i;
	int ret;

	mdp_mask(c->rdma + 0x008, 1, 1);
	ret = mdp_poll(c->rdma + RDMA_STATE, RDMA_IDLE, RDMA_IDLE, 1000);
	mdp_mask(c->rdma + 0x008, 0, 1);
	mdp_mask(c->rsz, BIT(16), BIT(16));
	mdp_mask(c->rsz, 0, BIT(16));
	mdp_mask(c->rsz, 1, 1);
	mdp_mask(c->wrot + 0x010, 1, 1);
	if (!ret)
		ret = mdp_poll(c->wrot + 0x014, 1, 1, 1000);
	mdp_mask(c->wrot + 0x010, 0, 1);
	if (!ret)
		ret = mdp_poll(c->wrot + 0x014, 1, 0, 1000);
	if (ret)
		return ret;
	for (i = 0; i < ARRAY_SIZE(mdp_frame); i++) {
		const struct mdp_write *w = &mdp_frame[i];

		mdp_mask(mdp_block(c, w->block) + w->off, w->val, w->mask);
		if (w->block == RDMA && w->off == 0x030) {
			writel(c->img2o.iova, c->rdma + 0xf00);
			writel(c->img2o.iova + YUYV_BYTES, c->rdma + 0x100);
		}
		if (w->block == WROT && w->off == 0x000) {
			writel(y, c->wrot + 0xf00);
			writel(u, c->wrot + 0xf04);
			writel(v, c->wrot + 0xf08);
		}
	}
	return 0;
}

static int mdp_run_tile(struct cronos_camera *c, const struct mdp_tile *t)
{
	u32 size = HEIGHT << 16 | t->w;
	unsigned int i;
	ktime_t end;

	mdp_routes(c, true);
	mdp_mask(c->mutex + MUTEX_MOD, MUTEX_MOD_MDP, 0x07ffffff);
	mdp_mask(c->mutex + MUTEX_SOF, 0, 7);		/* single: started by MUTEX_EN */
	mdp_mask(c->wrot + 0x02c, t->x, 0x0fffffff);	/* the planes' offsets */
	mdp_mask(c->wrot + 0x038, t->x / 2, 0x0fffffff);
	mdp_mask(c->wrot + 0x068, t->x / 2, 0x0fffffff);
	mdp_mask(c->wrot + 0x078, size, 0x1fff1fff);
	mdp_mask(c->wrot + 0x024, size, 0x1fff1fff);
	mdp_mask(c->wrot + 0x020, 0, 0x1fff1fff);
	mdp_mask(c->wrot + 0x008, t->wrot_buf, 0x1fff7f77);	/* and no chroma filter */
	mdp_mask(c->wrot + 0x07c, 1, 1);
	mdp_mask(c->rsz + 0x00c, t->rsz_in, 0xdfff1fff);
	for (i = 0x01c; i <= 0x038; i += 4)
		mdp_mask(c->rsz + i, 0, i & 4 ? 0x00001fff : 0x001fffff);
	mdp_mask(c->rsz + 0x010, t->rsz_out, 0x9fff1fff);
	mdp_mask(c->rdma + 0x000, 1, 1);
	mdp_mask(c->rdma + 0x020, 0x00001000, 0x00001110);
	mdp_mask(c->rdma + 0x0c0, t->rdma[0], 0x0000007f);
	mdp_mask(c->rdma + 0x088, t->rdma[1], 0x1ffff3ff);
	mdp_mask(c->rdma + 0x0d0, t->rdma[2], 0x3fff1fff);
	mdp_mask(c->rdma + 0x0d8, t->rdma[3], 0x0000007f);
	mdp_mask(c->rdma + 0x0b8, 0, 0x1ffff3ff);
	mdp_mask(c->rdma + 0x0e8, t->rdma[4], 0x3fff1fff);
	writel(t->x * 2, c->rdma + 0x118);		/* the tile's first byte in the line */
	writel(0, c->rdma + 0x120);
	writel(0, c->rdma + 0x128);
	mdp_mask(c->rdma + 0x070, size, 0x1fff1fff);
	mdp_mask(c->rdma + 0x078, size, 0x1fff1fff);
	mdp_mask(c->rdma + 0x080, 0, 0x003f001f);
	mdp_mask(c->mutex + MUTEX_EN, 1, 1);
	if (mdp_poll(c->mutex + MUTEX_EN, 1, 0, 1000))
		return -ETIMEDOUT;
	mdp_mask(c->mutex + MUTEX_MOD, 0, 0x07ffffff);
	/* Done when RDMA has read its last line and WROT has written its own. */
	usleep_range(t->us - 300, t->us - 100);
	end = ktime_add_us(ktime_get(), MDP_TIMEOUT_US);
	for (;;) {
		writel(WROT_LINE_SEL, c->wrot + WROT_DEBUG_SEL);
		if ((readl(c->rdma + RDMA_STATE) & 0x7ff00) == RDMA_IDLE &&
		    !(readl(c->wrot + WROT_DEBUG) & 0xffff))
			break;
		if (ktime_after(ktime_get(), end))
			return -ETIMEDOUT;
		usleep_range(50, 100);
	}
	mdp_mask(c->rdma + 0x000, 0, 1);
	mdp_mask(c->wrot + 0x07c, 0, 1);
	mdp_routes(c, false);
	return 0;
}

/* The caller's luma plane, pinned and mapped for WROT: one run of IOVA, cache lines whole. */
static int map_user(struct cronos_camera *c, unsigned long uaddr, size_t bytes, struct sg_table *sgt,
		    int *npages)
{
	unsigned long off = offset_in_page(uaddr);
	int n = DIV_ROUND_UP(off + bytes, PAGE_SIZE), got, ret;
	struct scatterlist *sg;
	dma_addr_t next;
	unsigned int i;

	if (uaddr & (L1_CACHE_BYTES - 1))
		return -EINVAL;
	got = pin_user_pages_fast(uaddr & PAGE_MASK, n, FOLL_WRITE, c->pages);
	if (got != n) {
		if (got > 0)
			unpin_user_pages(c->pages, got);
		return -EFAULT;
	}
	ret = sg_alloc_table_from_pages(sgt, c->pages, n, off, bytes, GFP_KERNEL);
	if (ret)
		goto unpin;
	ret = dma_map_sgtable(c->dev, sgt, DMA_FROM_DEVICE, 0);
	if (ret)
		goto free;
	next = sg_dma_address(sgt->sgl);
	for_each_sgtable_dma_sg(sgt, sg, i) {
		if (sg_dma_address(sg) != next)
			break;
		next += sg_dma_len(sg);
	}
	if (next - sg_dma_address(sgt->sgl) == bytes) {
		*npages = n;
		return 0;
	}
	ret = -EINVAL;
	dma_unmap_sgtable(c->dev, sgt, DMA_FROM_DEVICE, 0);
free:
	sg_free_table(sgt);
unpin:
	unpin_user_pages(c->pages, n);
	return ret;
}

static void unmap_user(struct cronos_camera *c, struct sg_table *sgt, int npages)
{
	dma_unmap_sgtable(c->dev, sgt, DMA_FROM_DEVICE, 0);
	sg_free_table(sgt);
	unpin_user_pages_dirty_lock(c->pages, npages, true);
}

/* The last line's first bytes of each tile, in a plane: set to what the MDP never writes. */
static int mark_tiles(struct cronos_camera *c, u8 __user *y)
{
	u8 *u = c->chroma.va, *v = u + CHROMA_BYTES / 2;
	u64 ys = Y_SENTINEL * 0x0101010101010101ULL;
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(mdp_tiles); i++) {
		unsigned int x = mdp_tiles[i].x;

		if (copy_to_user(y + (HEIGHT - 1) * WIDTH + x, &ys, 8))
			return -EFAULT;
		memset(u + (HEIGHT - 1) * WIDTH / 2 + x / 2, C_SENTINEL, 8);
		memset(v + (HEIGHT - 1) * WIDTH / 2 + x / 2, C_SENTINEL, 8);
	}
	return 0;
}

static bool tiles_written(struct cronos_camera *c, u8 __user *y)
{
	const u8 *u = c->chroma.va, *v = u + CHROMA_BYTES / 2;
	u64 ys = Y_SENTINEL * 0x0101010101010101ULL, cs = C_SENTINEL * 0x0101010101010101ULL;
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(mdp_tiles); i++) {
		unsigned int x = mdp_tiles[i].x;
		u64 got;

		if (copy_from_user(&got, y + (HEIGHT - 1) * WIDTH + x, 8) || got == ys ||
		    get_unaligned((const u64 *)(u + (HEIGHT - 1) * WIDTH / 2 + x / 2)) == cs ||
		    get_unaligned((const u64 *)(v + (HEIGHT - 1) * WIDTH / 2 + x / 2)) == cs)
			return false;
	}
	return true;
}

/*
 * WROT's 4:2:2 chroma to the caller's 4:2:0: each two lines averaged (rounded), and moved from two's
 * complement to around 128. On NEON a signed rounding halving add (SRHADD, (s + t + 1) >> 1) and
 * the flip of bit 7 do it, 32 samples a step; otherwise, eight a step, the flip first and then
 * (a | b) - ((a ^ b) >> 1), the rounded average of each byte pair. The same bytes either way, and
 * the same as copy_i420's.
 */
#define CHROMA_LINES 8	/* lines out a step, through c->rows */

static void chroma_lines(u8 *out, const u8 *src, unsigned int lines)
{
#ifdef CONFIG_KERNEL_MODE_NEON
	if (may_use_simd()) {
		u64 w = WIDTH / 2;

		scoped_ksimd() asm volatile(
		"	movi	v4.16b, #0x80\n"
		"1:	mov	x3, %[w]\n"
		"	add	x4, %[s], %[w]\n"
		"2:	ld1	{v0.16b, v1.16b}, [%[s]], #32\n"
		"	ld1	{v2.16b, v3.16b}, [x4], #32\n"
		"	srhadd	v0.16b, v0.16b, v2.16b\n"
		"	srhadd	v1.16b, v1.16b, v3.16b\n"
		"	eor	v0.16b, v0.16b, v4.16b\n"
		"	eor	v1.16b, v1.16b, v4.16b\n"
		"	st1	{v0.16b, v1.16b}, [%[d]], #32\n"
		"	subs	x3, x3, #32\n"
		"	b.ne	2b\n"
		"	add	%[s], %[s], %[w]\n"
		"	subs	%w[n], %w[n], #1\n"
		"	b.ne	1b\n"
		: [s] "+r" (src), [d] "+r" (out), [n] "+r" (lines)
		: [w] "r" (w)
		: "x3", "x4", "v0", "v1", "v2", "v3", "v4", "memory", "cc");
		return;
	}
#endif
	{
		const u64 flip = 0x8080808080808080ULL, low7 = 0x7f7f7f7f7f7f7f7fULL;
		const unsigned int line = WIDTH / 2 / 8;	/* u64s a line */
		const u64 *a = (const u64 *)src;
		u64 *o = (u64 *)out;
		unsigned int k, x;

		for (k = 0; k < lines; k++, a += 2 * line, o += line) {
			for (x = 0; x < line; x++) {
				u64 p = a[x] ^ flip, q = a[x + line] ^ flip;

				o[x] = (p | q) - (((p ^ q) >> 1) & low7);
			}
		}
	}
}

static int copy_chroma(struct cronos_camera *c, u8 __user *dst)
{
	const u8 *src = c->chroma.va;
	unsigned int j;

	/* U then V: 2 * HEIGHT source lines, HEIGHT out. */
	for (j = 0; j < HEIGHT; j += CHROMA_LINES, src += 2 * CHROMA_LINES * WIDTH / 2) {
		chroma_lines(c->rows, src, CHROMA_LINES);
		if (copy_to_user(dst + j * WIDTH / 2, c->rows, CHROMA_LINES * WIDTH / 2))
			return -EFAULT;
	}
	return 0;
}

static int mdp_i420(struct cronos_camera *c, u8 __user *dst)
{
	ktime_t t = ktime_get();
	struct sg_table sgt;
	unsigned int i;
	int ret, npages;
	u32 mmu;

	if (mark_tiles(c, dst))
		return -EFAULT;
	ret = map_user(c, (unsigned long)dst, Y_BYTES, &sgt, &npages);
	if (ret) {
		dev_info_once(c->dev, "MDP: cannot map the caller's buffer (%d); the cores make its I420\n",
			      ret);
		return ret;
	}
	/* The cores write only the marks there (and read the rest): those lines are cleaned. */
	for (i = 0; i < ARRAY_SIZE(mdp_tiles); i++) {
		unsigned int off = (HEIGHT - 1) * WIDTH / 2 + mdp_tiles[i].x / 2;

		dma_sync_single_range_for_device(c->dev, c->chroma.iova, off, 8, DMA_FROM_DEVICE);
		dma_sync_single_range_for_device(c->dev, c->chroma.iova, off + CHROMA_BYTES / 2, 8,
						 DMA_FROM_DEVICE);
	}
	stamp(T_MAP, &t);
	ret = pm_runtime_resume_and_get(c->larb0);
	if (ret < 0)
		goto unmap;
	ret = clk_bulk_prepare_enable(ARRAY_SIZE(c->mdp_clks), c->mdp_clks);
	if (ret)
		goto put_larb;
	/*
	 * larb0 translates only the ports of the devices attached when it last resumed, and the display
	 * keeps it resumed: RDMA's and WROT's are added here, as mtk-smi would set them.
	 */
	mmu = readl(c->larb0_mmu);
	if ((mmu & LARB0_MDP_PORTS) != LARB0_MDP_PORTS)
		writel(mmu | LARB0_MDP_PORTS, c->larb0_mmu);
	if (readl(c->mutex + MUTEX_MOD) & ~MUTEX_MOD_MDP) {
		ret = -EBUSY;	/* someone else's mutex after all */
		goto clocks;
	}
	ret = mdp_frame_setup(c, sg_dma_address(sgt.sgl), c->chroma.iova,
			      c->chroma.iova + CHROMA_BYTES / 2);
	for (i = 0; !ret && i < ARRAY_SIZE(mdp_tiles); i++)
		ret = mdp_run_tile(c, &mdp_tiles[i]);
	if (ret)
		mdp_stop(c);	/* before the pages can go */
clocks:
	clk_bulk_disable_unprepare(ARRAY_SIZE(c->mdp_clks), c->mdp_clks);
put_larb:
	pm_runtime_put(c->larb0);
unmap:
	stamp(T_MDP, &t);
	unmap_user(c, &sgt, npages);
	dma_sync_single_for_cpu(c->dev, c->chroma.iova, CHROMA_BYTES, DMA_FROM_DEVICE);
	stamp(T_UNMAP, &t);
	if (!ret && !tiles_written(c, dst))
		ret = -EIO;
	if (!ret) {
		ret = copy_chroma(c, dst + Y_BYTES);
		stamp(T_CHROMA, &t);
	} else if (ret != -EFAULT) {
		dev_warn(c->dev, "MDP: %d, the cores make the I420 until the stream restarts\n", ret);
		c->mdp_failed = true;
	}
	return ret;
}

/*
 * Route A's MDP: CAMIN -> RSZ1 -> WROT, set up a frame and a tile at a time (DpFramework's values
 * for the path), with WROT writing the caller's three planes and averaging each two chroma lines
 * (VIDO_MAIN_BUF_SIZE [6:4] = 3: (a + b + 1) / 2, the cores' arithmetic).
 */
#define MMSYS_ISP_MOUT_EN	0x01c
#define MUTEX_MOD_LINK		(BIT(2) | BIT(5))	/* RSZ1, WROT */

static void link_routes(struct cronos_camera *c, bool on)
{
	regmap_write(c->mmsys, MMSYS_ISP_MOUT_EN, on ? 2 : 0);	/* CAMIN -> RSZ1 */
	regmap_write(c->mmsys, MMSYS_PRZ1_MOUT_EN, on ? 2 : 0);	/* RSZ1 -> WROT */
	regmap_write(c->mmsys, MMSYS_PRZ1_SEL_IN, 0);		/* RSZ1 <- CAMIN */
	regmap_write(c->mmsys, MMSYS_WROT_SEL_IN, on ? 1 : 0);	/* WROT <- RSZ1 */
}

static int link_frame_setup(struct cronos_camera *c, dma_addr_t y)
{
	int ret;

	mdp_mask(c->rsz, BIT(16), BIT(16));
	mdp_mask(c->rsz, 0, BIT(16));
	mdp_mask(c->rsz, 1, 1);
	mdp_mask(c->wrot + 0x010, 1, 1);
	ret = mdp_poll(c->wrot + 0x014, 1, 1, 1000);
	mdp_mask(c->wrot + 0x010, 0, 1);
	if (!ret)
		ret = mdp_poll(c->wrot + 0x014, 1, 0, 1000);
	mdp_mask(c->rsz, 0, 1);				/* RSZ1 passed through */
	if (ret)
		return ret;
	writel(y, c->wrot + 0xf00);
	writel(y + Y_BYTES, c->wrot + 0xf04);
	writel(y + Y_BYTES + Y_BYTES / 4, c->wrot + 0xf08);
	mdp_mask(c->wrot + 0x000, 0x00005008, 0xf131510f);	/* I420, three planes */
	mdp_mask(c->wrot + 0x074, 0x0006005c, 0x00ffffff);
	mdp_mask(c->wrot + 0x030, WIDTH, 0x0000ffff);		/* the planes' strides */
	mdp_mask(c->wrot + 0x03c, WIDTH / 2, 0x0000ffff);
	mdp_mask(c->wrot + 0x06c, WIDTH / 2, 0x0000ffff);
	mdp_mask(c->wrot + 0x084, 0x00000000, 0x000000f3);	/* no color matrix */
	mdp_mask(c->wrot + 0x054, 0xff000000, 0xff000000);
	mdp_mask(c->wrot + 0x070, 0x80000000, 0x80000000);
	mdp_mask(c->wrot + 0x07c, 0x00900000, 0x00900000);
	return 0;
}

/* A tile's MDP: started by the mutex at once, it then takes what pass 2 sends it. */
static int link_tile_start(struct cronos_camera *c, const struct link_tile *t)
{
	u32 size = HEIGHT << 16 | t->w;
	unsigned int i;

	link_routes(c, true);
	mdp_mask(c->mutex + MUTEX_MOD, MUTEX_MOD_LINK, 0x07ffffff);
	mdp_mask(c->mutex + MUTEX_SOF, 0, 7);		/* single: started by MUTEX_EN */
	mdp_mask(c->wrot + 0x02c, t->x, 0x0fffffff);	/* the planes' offsets */
	mdp_mask(c->wrot + 0x038, t->x / 2, 0x0fffffff);
	mdp_mask(c->wrot + 0x068, t->x / 2, 0x0fffffff);
	mdp_mask(c->wrot + 0x078, size, 0x1fff1fff);
	mdp_mask(c->wrot + 0x024, size, 0x1fff1fff);
	mdp_mask(c->wrot + 0x020, 0, 0x1fff1fff);
	mdp_mask(c->wrot + 0x008, t->wrot_buf | 0x30, 0x1fff7f77);	/* 4:2:0 by the average */
	mdp_mask(c->wrot + 0x07c, 1, 1);
	mdp_mask(c->rsz + 0x00c, t->rsz_in, 0xdfff1fff);
	for (i = 0x01c; i <= 0x038; i += 4)
		mdp_mask(c->rsz + i, 0, i & 4 ? 0x00001fff : 0x001fffff);
	mdp_mask(c->rsz + 0x010, t->rsz_out, 0x9fff1fff);
	mdp_mask(c->mutex + MUTEX_EN, 1, 1);
	if (mdp_poll(c->mutex + MUTEX_EN, 1, 0, 1000))
		return -ETIMEDOUT;
	mdp_mask(c->mutex + MUTEX_MOD, 0, 0x07ffffff);
	return 0;
}

/* After pass 2's tile: WROT has written its last line (it is that quick), and lets go. */
static int link_tile_end(struct cronos_camera *c)
{
	int ret;

	writel(WROT_LINE_SEL, c->wrot + WROT_DEBUG_SEL);
	ret = mdp_poll(c->wrot + WROT_DEBUG, 0xffff, 0, 2000);
	mdp_mask(c->wrot + 0x07c, 0, 1);
	link_routes(c, false);
	return ret;
}

/*
 * The last line's first bytes of each tile, in each plane: set to what the MDP never writes, 0
 * (video range: luma 16..235, chroma 16..240 around 128 here).
 */
static int link_mark(u8 __user *dst, bool check)
{
	unsigned int i, k;

	for (i = 0; i < ARRAY_SIZE(link_tiles); i++) {
		unsigned int x = link_tiles[i].x;
		u8 __user *at[3] = {
			dst + (HEIGHT - 1) * WIDTH + x,
			dst + Y_BYTES + (HEIGHT / 2 - 1) * WIDTH / 2 + x / 2,
			dst + Y_BYTES * 5 / 4 + (HEIGHT / 2 - 1) * WIDTH / 2 + x / 2,
		};

		for (k = 0; k < 3; k++) {
			u64 want = 0, got;

			if (check) {
				if (copy_from_user(&got, at[k], 8) || got == want)
					return -EIO;
			} else if (copy_to_user(at[k], &want, 8)) {
				return -EFAULT;
			}
		}
	}
	return 0;
}

/*
 * Before pass 2 goes into the MDP: the caller's I420 pinned and mapped as one run of IOVA (its start
 * on a cache line), marked, and the MDP powered and set up for the frame. link_finish undoes it.
 */
static int link_prepare(struct cronos_camera *c, u8 __user *dst, struct sg_table *sgt, int *npages)
{
	int ret;
	u32 mmu;

	if (link_mark(dst, false))
		return -EFAULT;
	ret = map_user(c, (unsigned long)dst, I420_BYTES, sgt, npages);
	if (ret) {
		dev_info_once(c->dev, "MDP link: cannot map the caller's buffer (%d)\n", ret);
		return ret;
	}
	ret = pm_runtime_resume_and_get(c->larb0);
	if (ret < 0)
		goto unmap;
	ret = clk_bulk_prepare_enable(ARRAY_SIZE(c->mdp_clks), c->mdp_clks);
	if (ret)
		goto put_larb;
	ret = clk_prepare_enable(c->camin);
	if (ret)
		goto clocks;
	mmu = readl(c->larb0_mmu);
	if ((mmu & LARB0_MDP_PORTS) != LARB0_MDP_PORTS)
		writel(mmu | LARB0_MDP_PORTS, c->larb0_mmu);
	ret = -EBUSY;	/* someone else's mutex after all */
	if (!(readl(c->mutex + MUTEX_MOD) & ~(MUTEX_MOD_MDP | MUTEX_MOD_LINK)))
		ret = link_frame_setup(c, sg_dma_address(sgt->sgl));
	if (!ret)
		return 0;
	mdp_stop(c);
	link_routes(c, false);
	clk_disable_unprepare(c->camin);
clocks:
	clk_bulk_disable_unprepare(ARRAY_SIZE(c->mdp_clks), c->mdp_clks);
put_larb:
	pm_runtime_put(c->larb0);
unmap:
	unmap_user(c, sgt, *npages);
	return ret;
}

/* After pass 2: the MDP let go (stopped first if anything failed), the pages, and the marks read. */
static int link_finish(struct cronos_camera *c, u8 __user *dst, struct sg_table *sgt, int npages,
		       int ret)
{
	if (ret)
		mdp_stop(c);	/* before the pages can go */
	link_routes(c, false);
	clk_disable_unprepare(c->camin);
	clk_bulk_disable_unprepare(ARRAY_SIZE(c->mdp_clks), c->mdp_clks);
	pm_runtime_put(c->larb0);
	unmap_user(c, sgt, npages);
	if (!ret && link_mark(dst, true))
		ret = -EIO;
	return ret;
}

/* With the camera streaming: the thread runs it in a blanking, within 400 ms. */
static int develop_streaming(struct cronos_camera *c, bool pgn, bool link)
{
	WRITE_ONCE(c->p2_last, jiffies);
	develop_frames(c, true);
	reinit_completion(&c->p2_done);
	c->p2_pgn = pgn;
	c->p2_link = link;
	atomic_set(&c->p2_state, P2_PENDING);
	if (!wait_for_completion_timeout(&c->p2_done, msecs_to_jiffies(400))) {
		if (atomic_cmpxchg(&c->p2_state, P2_PENDING, P2_IDLE) == P2_PENDING)
			return -ETIMEDOUT;
		wait_for_completion(&c->p2_done);	/* running: P2_TIMEOUT_US at most */
	}
	WRITE_ONCE(c->p2_last, jiffies);
	return c->p2_result;
}

static int develop(struct cronos_camera *c, struct cronos_develop *d)
{
	u8 __user *dst = u64_to_user_ptr(d->dst);
	bool late, link, pgn = false;
	struct sg_table sgt;
	int npages = 0;
	ktime_t t;
	unsigned int i;
	int ret, us;

	if (d->flags || d->reserved || d->raw_size < FRAME_BYTES || d->dst_size < I420_BYTES ||
	    d->black < -4096 || d->black > 4095)
		return -EINVAL;
	for (i = 0; i < 4; i++) {
		if (d->gain[i] > 256 * 512)
			return -EINVAL;
		pgn |= d->gain[i] > 8191;
	}
	for (i = 0; i < ARRAY_SIZE(d->curve); i++)
		if (d->curve[i] > 1023)
			return -EINVAL;

	mutex_lock(&c->isp_lock);
	ret = alloc_isp_buffers(c);
	if (ret)
		goto out;
	t = ktime_get();
	if (copy_from_user(c->imgi.va, u64_to_user_ptr(d->raw), FRAME_BYTES)) {
		ret = -EFAULT;
		goto out;
	}
	dma_sync_single_for_device(c->dev, c->imgi.iova, FRAME_BYTES, DMA_TO_DEVICE);
	/* IMG2O needs no clean: the cores only ever read it (after an invalidate). */
	stamp(T_IN, &t);
	link = c->link && !c->link_failed && READ_ONCE(use_link) && READ_ONCE(use_mdp) &&
	       !link_prepare(c, dst, &sgt, &npages);
	if (link)
		stamp(T_MAP, &t);
again:
	if (c->thread && c->powered) {
		if (c->p2_failed) {
			ret = -EIO;
			goto unlink;
		}
		program_tone(c, d);
		us = develop_streaming(c, pgn, link);
		c->p2_failed = us == -EIO && !link;
	} else {
		ret = isp_power(c, true);
		if (ret)
			goto unlink;
		program_tone(c, d);
		us = run_pass2(c, pgn, false, link, &late);
		isp_power(c, false);
	}
	if (link) {
		/* The I420 is the MDP's, written into the caller's buffer; or pass 2 runs again, for
		 * route B, and the link is not tried again until the stream restarts. */
		link = false;
		t = ktime_get();
		ret = link_finish(c, dst, &sgt, npages, us < 0 ? us : 0);
		stamp(T_UNMAP, &t);
		times_us[T_MDP] = us < 0 ? 0 : us;
		times_us[T_CHROMA] = 0;
		if (!ret) {
			d->hw_us = us;
			times_us[T_OUT] = times_us[T_UNMAP];
			goto out;
		}
		dev_warn(c->dev, "MDP link: %d; route B until the stream restarts\n", ret);
		c->link_failed = true;
		goto again;
	}
	if (us < 0) {
		ret = us;
		goto out;
	}
	d->hw_us = us;
	t = ktime_get();
	ret = -ENODEV;
	if (c->mdp && !c->mdp_failed && READ_ONCE(use_mdp))
		ret = mdp_i420(c, u64_to_user_ptr(d->dst));
	if (ret)
		ret = copy_i420(c, u64_to_user_ptr(d->dst));
	stamp(T_OUT, &t);
out:
	mutex_unlock(&c->isp_lock);
	return ret;
unlink:
	if (link)
		link_finish(c, dst, &sgt, npages, ret);
	goto out;
}

static long camera_ioctl(struct file *f, unsigned int cmd, unsigned long arg)
{
	struct cronos_camera *c = video_drvdata(f);
	struct cronos_develop d;
	int ret;

	if (cmd != CRONOS_IOC_DEVELOP)
		return video_ioctl2(f, cmd, arg);
	if (copy_from_user(&d, (void __user *)arg, sizeof(d)))
		return -EFAULT;
	ret = develop(c, &d);
	if (!ret && copy_to_user((void __user *)arg, &d, sizeof(d)))
		ret = -EFAULT;
	return ret;
}

#ifdef CONFIG_COMPAT
/* The private ioctls of 32-bit callers (v4l2_compat_ioctl32 hands them here). The structure is
 * the same for them: fixed-size fields, pointers as __u64. */
static long camera_compat_ioctl32(struct file *f, unsigned int cmd, unsigned long arg)
{
	if (cmd == CRONOS_IOC_DEVELOP)
		return camera_ioctl(f, cmd, (unsigned long)compat_ptr(arg));
	return -ENOIOCTLCMD;
}
#endif

static int queue_setup(struct vb2_queue *q, unsigned int *buffers,
	unsigned int *planes, unsigned int sizes[], struct device *alloc_devs[])
{
	if (*planes) return sizes[0] < FRAME_BYTES ? -EINVAL : 0;
	*planes = 1;
	sizes[0] = FRAME_BYTES;
	return 0;
}
static int buffer_prepare(struct vb2_buffer *vb)
{
	return vb2_plane_size(vb, 0) < FRAME_BYTES ? -EINVAL : 0;
}
static void buffer_queue(struct vb2_buffer *vb)
{
	struct cronos_camera *c = vb2_get_drv_priv(vb->vb2_queue);
	struct camera_buffer *b = container_of(to_vb2_v4l2_buffer(vb), struct camera_buffer, vb);
	unsigned long flags;

	spin_lock_irqsave(&c->buffers_lock, flags);
	list_add_tail(&b->list, &c->buffers);
	spin_unlock_irqrestore(&c->buffers_lock, flags);
}
static int start_streaming(struct vb2_queue *q, unsigned int count)
{
	struct cronos_camera *c = vb2_get_drv_priv(q);
	int ret;

	/* Avoid activating the shared IOMMU while the bootloader still scans out. */
	if (!c->dma) {
		c->dma = dmam_alloc_coherent(c->dev, DMA_BYTES, &c->dma_addr, GFP_KERNEL);
		if (!c->dma) { ret = -ENOMEM; goto fail; }
	}
	mutex_lock(&c->isp_lock);
	ret = camera_power_on(c);
	if (ret) { mutex_unlock(&c->isp_lock); goto fail; }
	c->sequence = 0;
	atomic_set(&c->drop, 0);
	atomic_set(&c->p2_state, P2_IDLE);
	c->p2_failed = false;
	c->mdp_failed = false;
	c->link_failed = false;
	c->thread = kthread_create(camera_thread, c, "cronos-camera");
	if (IS_ERR(c->thread)) {
		ret = PTR_ERR(c->thread);
		c->thread = NULL;
		camera_power_off(c);
		mutex_unlock(&c->isp_lock);
		goto fail;
	}
	/* A frame's next slot, and pass 2, have to start within the blanking: a fraction of a
	 * millisecond late is a lost frame. The thread sleeps nearly all the time. */
	sched_set_fifo_low(c->thread);
	wake_up_process(c->thread);
	mutex_unlock(&c->isp_lock);
	return 0;
fail:
	return_buffers(c, VB2_BUF_STATE_QUEUED);
	return ret;
}
static void stop_streaming(struct vb2_queue *q)
{
	struct cronos_camera *c = vb2_get_drv_priv(q);

	mutex_lock(&c->isp_lock);
	if (c->thread) { kthread_stop(c->thread); c->thread = NULL; }
	camera_power_off(c);
	mutex_unlock(&c->isp_lock);
	return_buffers(c, VB2_BUF_STATE_ERROR);
}
static const struct vb2_ops queue_ops = {
	.queue_setup = queue_setup, .buf_prepare = buffer_prepare,
	.buf_queue = buffer_queue, .start_streaming = start_streaming,
	.stop_streaming = stop_streaming,
};

static int querycap(struct file *f, void *priv, struct v4l2_capability *cap)
{
	strscpy(cap->driver, "cronos-camera", sizeof(cap->driver));
	strscpy(cap->card, "cronos-ov02b10", sizeof(cap->card));
	strscpy(cap->bus_info, "platform:cronos-camera", sizeof(cap->bus_info));
	return 0;
}
static int enum_fmt(struct file *f, void *priv, struct v4l2_fmtdesc *fmt)
{
	if (fmt->index) return -EINVAL;
	fmt->pixelformat = V4L2_PIX_FMT_SRGGB10P;
	return 0;
}
static int get_fmt(struct file *f, void *priv, struct v4l2_format *fmt)
{
	fmt->fmt.pix = (struct v4l2_pix_format) {
		.width = WIDTH, .height = HEIGHT, .pixelformat = V4L2_PIX_FMT_SRGGB10P,
		.field = V4L2_FIELD_NONE, .bytesperline = STRIDE, .sizeimage = FRAME_BYTES,
		.colorspace = V4L2_COLORSPACE_RAW,
	};
	return 0;
}
static int set_fmt(struct file *f, void *priv, struct v4l2_format *fmt)
{
	struct cronos_camera *c = video_drvdata(f);

	if (vb2_is_busy(&c->queue)) return -EBUSY;
	return get_fmt(f, priv, fmt);
}
static int enum_input(struct file *f, void *priv, struct v4l2_input *in)
{
	if (in->index) return -EINVAL;
	in->type = V4L2_INPUT_TYPE_CAMERA;
	strscpy(in->name, "OV02B10", sizeof(in->name));
	return 0;
}
static int get_input(struct file *f, void *priv, unsigned int *i) { *i = 0; return 0; }
static int set_input(struct file *f, void *priv, unsigned int i) { return i ? -EINVAL : 0; }
static const struct v4l2_ioctl_ops camera_ioctls = {
	.vidioc_querycap = querycap, .vidioc_enum_fmt_vid_cap = enum_fmt,
	.vidioc_g_fmt_vid_cap = get_fmt, .vidioc_s_fmt_vid_cap = set_fmt,
	.vidioc_try_fmt_vid_cap = get_fmt, .vidioc_enum_input = enum_input,
	.vidioc_g_input = get_input, .vidioc_s_input = set_input,
	.vidioc_reqbufs = vb2_ioctl_reqbufs, .vidioc_querybuf = vb2_ioctl_querybuf,
	.vidioc_qbuf = vb2_ioctl_qbuf, .vidioc_dqbuf = vb2_ioctl_dqbuf,
	.vidioc_streamon = vb2_ioctl_streamon, .vidioc_streamoff = vb2_ioctl_streamoff,
};
static const struct v4l2_file_operations camera_fops = {
	.owner = THIS_MODULE, .open = v4l2_fh_open, .release = vb2_fop_release,
	.read = vb2_fop_read, .poll = vb2_fop_poll, .mmap = vb2_fop_mmap,
	.unlocked_ioctl = camera_ioctl,
#ifdef CONFIG_COMPAT
	.compat_ioctl32 = camera_compat_ioctl32,
#endif
};

static void sensor_unregister(void *data) { i2c_unregister_device(data); }
static void larb_put(void *data) { put_device(data); }

/* The MDP's registers, clocks, resets and larb, when the node has them (mediatek,mdp-larb). */
static int mdp_probe(struct cronos_camera *c, struct platform_device *pdev)
{
	static const char * const clocks[] = { "mdp-rdma", "mdp-rsz1", "mdp-wrot" };
	struct device *dev = c->dev;
	struct platform_device *larb;
	struct device_node *np;
	struct resource res;
	unsigned int i;
	int ret;

	np = of_parse_phandle(dev->of_node, "mediatek,mdp-larb", 0);
	if (!np)
		return 0;
	larb = of_find_device_by_node(np);
	ret = of_address_to_resource(np, 0, &res);
	of_node_put(np);
	if (!larb)
		return -EPROBE_DEFER;
	c->larb0 = &larb->dev;
	i = devm_add_action_or_reset(dev, larb_put, c->larb0);
	if (i)
		return i;
	if (ret)
		return ret;
	if (!device_is_bound(c->larb0))
		return -EPROBE_DEFER;
	/* mtk-smi owns larb0's registers; only its MMU_EN word is touched here. */
	c->larb0_mmu = devm_ioremap(dev, res.start + LARB_MMU_EN, 4);
	np = of_parse_phandle(dev->of_node, "mediatek,mutex", 0);
	ret = np ? of_address_to_resource(np, 0, &res) : -EINVAL;
	of_node_put(np);
	if (ret)
		return ret;
	/* mtk-mutex owns the mutexes; only MUTEX_MDP's words are touched here. */
	c->mutex = devm_ioremap(dev, res.start, resource_size(&res));
	if (!c->larb0_mmu || !c->mutex)
		return -ENOMEM;
	c->mmsys = syscon_regmap_lookup_by_phandle(dev->of_node, "mediatek,mmsys");
	if (IS_ERR(c->mmsys))
		return PTR_ERR(c->mmsys);
	c->rdma = devm_platform_ioremap_resource(pdev, 3);
	if (IS_ERR(c->rdma)) return PTR_ERR(c->rdma);
	c->rsz = devm_platform_ioremap_resource(pdev, 4);
	if (IS_ERR(c->rsz)) return PTR_ERR(c->rsz);
	c->wrot = devm_platform_ioremap_resource(pdev, 5);
	if (IS_ERR(c->wrot)) return PTR_ERR(c->wrot);
	for (i = 0; i < ARRAY_SIZE(c->mdp_clks); i++)
		c->mdp_clks[i].id = clocks[i];
	ret = devm_clk_bulk_get(dev, ARRAY_SIZE(c->mdp_clks), c->mdp_clks);
	if (ret)
		return ret;
	c->mdp_reset = devm_reset_control_array_get_exclusive(dev);
	if (IS_ERR(c->mdp_reset))
		return PTR_ERR(c->mdp_reset);
	c->camin = devm_clk_get_optional(dev, "mdp-camin");
	if (IS_ERR(c->camin))
		return PTR_ERR(c->camin);
	c->link = c->camin;	/* route A where the node gives the link's gate */
	c->mdp = true;
	return 0;
}

static int camera_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct cronos_camera *c;
	struct device_node *node;
	struct platform_device *larb;
	struct i2c_adapter *bus;
	int ret;

	c = devm_kzalloc(dev, sizeof(*c), GFP_KERNEL);
	if (!c) return -ENOMEM;
	c->dev = dev;
	platform_set_drvdata(pdev, c);
	mutex_init(&c->lock);
	mutex_init(&c->sensor_lock);
	mutex_init(&c->isp_lock);
	init_completion(&c->p2_done);
	spin_lock_init(&c->buffers_lock);
	INIT_LIST_HEAD(&c->buffers);
	c->cam = devm_platform_ioremap_resource(pdev, 0);
	if (IS_ERR(c->cam)) return PTR_ERR(c->cam);
	c->sen = devm_platform_ioremap_resource(pdev, 1);
	if (IS_ERR(c->sen)) return PTR_ERR(c->sen);
	c->ana = devm_platform_ioremap_resource(pdev, 2);
	if (IS_ERR(c->ana)) return PTR_ERR(c->ana);
	c->clks[0].id = "smi";
	c->clks[1].id = "cam";
	c->clks[2].id = "tg";
	c->clks[3].id = "sen";
	c->nclks = ARRAY_SIZE(c->clks);
	ret = devm_clk_bulk_get(dev, c->nclks, c->clks);
	if (ret) return ret;
	c->avdd = devm_regulator_get(dev, "avdd");
	if (IS_ERR(c->avdd)) return PTR_ERR(c->avdd);
	c->iovdd = devm_regulator_get(dev, "iovdd");
	if (IS_ERR(c->iovdd)) return PTR_ERR(c->iovdd);
	c->reset = devm_gpiod_get(dev, "reset", GPIOD_OUT_HIGH);
	if (IS_ERR(c->reset)) return PTR_ERR(c->reset);
	c->standby = devm_gpiod_get(dev, "standby", GPIOD_OUT_HIGH);
	if (IS_ERR(c->standby)) return PTR_ERR(c->standby);
	node = of_parse_phandle(dev->of_node, "sensor-bus", 0);
	if (!node) return -EINVAL;
	bus = of_find_i2c_adapter_by_node(node);
	of_node_put(node);
	if (!bus) return -EPROBE_DEFER;
	c->sensor = i2c_new_dummy_device(bus, 0x3c);
	i2c_put_adapter(bus);
	if (IS_ERR(c->sensor)) return PTR_ERR(c->sensor);
	ret = devm_add_action_or_reset(dev, sensor_unregister, c->sensor);
	if (ret) return ret;
	node = of_parse_phandle(dev->of_node, "mediatek,larb", 0);
	if (!node) return -EINVAL;
	larb = of_find_device_by_node(node);
	of_node_put(node);
	if (!larb) return -EPROBE_DEFER;
	c->larb = &larb->dev;
	ret = devm_add_action_or_reset(dev, larb_put, c->larb);
	if (ret) return ret;
	if (!device_is_bound(c->larb)) return -EPROBE_DEFER;
	ret = dma_set_mask_and_coherent(dev, DMA_BIT_MASK(32));
	if (ret) return ret;
	dma_set_max_seg_size(dev, UINT_MAX);
	ret = mdp_probe(c, pdev);
	if (ret) return ret;
	ret = v4l2_device_register(dev, &c->v4l2);
	if (ret) return ret;
	v4l2_ctrl_handler_init(&c->controls, 2);
	/*
	 * Up to 16000 lines (the frame's blanking follows, in 0x14/0x15): 0.43 s, about 2.3 frames a second,
	 * for a dark room, where a longer frame gathers light that gain would only turn into noise.
	 */
	v4l2_ctrl_new_std(&c->controls, &control_ops, V4L2_CID_EXPOSURE, 4, 16000, 1, 600);
	v4l2_ctrl_new_std(&c->controls, &control_ops, V4L2_CID_ANALOGUE_GAIN, 64, 992, 4, 128);
	if (c->controls.error) { ret = c->controls.error; goto controls; }
	c->v4l2.ctrl_handler = &c->controls;
	c->queue = (struct vb2_queue) {
		.type = V4L2_BUF_TYPE_VIDEO_CAPTURE, .io_modes = VB2_MMAP | VB2_READ,
		.drv_priv = c, .buf_struct_size = sizeof(struct camera_buffer),
		.ops = &queue_ops, .mem_ops = &vb2_vmalloc_memops,
		.timestamp_flags = V4L2_BUF_FLAG_TIMESTAMP_MONOTONIC,
		.lock = &c->lock, .dev = dev, .min_queued_buffers = 2,
	};
	ret = vb2_queue_init(&c->queue);
	if (ret) goto controls;
	strscpy(c->video.name, "cronos-ov02b10", sizeof(c->video.name));
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
	if (!ret) return 0;
	pm_runtime_disable(dev);
	vb2_queue_release(&c->queue);
controls:
	v4l2_ctrl_handler_free(&c->controls);
	v4l2_device_unregister(&c->v4l2);
	return ret;
}
static const struct of_device_id camera_match[] = {
	{ .compatible = "amazon,cronos-camera" }, {},
};
static struct platform_driver camera_driver = {
	.probe = camera_probe,
	.driver = { .name = "cronos-camera", .of_match_table = camera_match,
		.suppress_bind_attrs = true },
};
builtin_platform_driver(camera_driver);
MODULE_LICENSE("GPL");
