// SPDX-License-Identifier: GPL-2.0-only
/* Fixed OV02B10 -> SENINF1 -> TG1 -> IMGO pipeline for the Echo Show 5. */
#include <linux/clk.h>
#include <linux/delay.h>
#include <linux/dma-mapping.h>
#include <linux/gpio/consumer.h>
#include <linux/i2c.h>
#include <linux/io.h>
#include <linux/kthread.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/of_platform.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/regulator/consumer.h>
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

struct camera_buffer {
	struct vb2_v4l2_buffer vb;
	struct list_head list;
};
struct cronos_camera {
	struct device *dev, *larb;
	void __iomem *cam, *sen, *ana;
	struct clk_bulk_data *clks;
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
	u32 blank = max(1235U, shutter + 7) - 1220;
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

static void camera_pipeline(struct cronos_camera *c)
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
	writel(c->dma_addr, p + 0x300);
	writel(0, p + 0x304);
	writel(STRIDE - 1, p + 0x308);
	writel(HEIGHT - 1, p + 0x30c);
	writel(STRIDE, p + 0x310);
	writel((WIDTH << 16) | HEIGHT, p + 0x14c);
	writel(0x80000040, p + 0x314);
	writel(0x00200020, p + 0x318);
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
	camera_pipeline(c);
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

static int camera_thread(void *data)
{
	struct cronos_camera *c = data;
	unsigned int last, count, slot = 0, ready;
	unsigned long flags, deadline = jiffies + msecs_to_jiffies(3000);
	bool first = true;

	last = (readl(c->cam + 0x44c) >> 16) & 0xff;
	mask(c->cam, 0x414, 0, 1);
	while (!kthread_should_stop()) {
		struct camera_buffer *b = NULL;

		count = (readl(c->cam + 0x44c) >> 16) & 0xff;
		if (count == last) {
			if (time_after(jiffies, deadline)) {
				dev_err(c->dev, "capture timed out\n");
				vb2_queue_error(&c->queue);
				break;
			}
			usleep_range(500, 1000);
			continue;
		}
		deadline = jiffies + msecs_to_jiffies(3000);
		last = count;
		ready = slot;
		slot = (slot + 1) % DMA_SLOTS;
		writel(c->dma_addr + slot * FRAME_BYTES, c->cam + 0x300);
		if (first) { first = false; continue; }
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
		msleep(100);
	return 0;
}

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
	ret = camera_power_on(c);
	if (ret) goto fail;
	c->sequence = 0;
	c->thread = kthread_run(camera_thread, c, "cronos-camera");
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
	struct cronos_camera *c = vb2_get_drv_priv(q);

	if (c->thread) { kthread_stop(c->thread); c->thread = NULL; }
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
	.unlocked_ioctl = video_ioctl2,
};

static void sensor_unregister(void *data) { i2c_unregister_device(data); }
static void larb_put(void *data) { put_device(data); }
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
	spin_lock_init(&c->buffers_lock);
	INIT_LIST_HEAD(&c->buffers);
	c->cam = devm_platform_ioremap_resource(pdev, 0);
	if (IS_ERR(c->cam)) return PTR_ERR(c->cam);
	c->sen = devm_platform_ioremap_resource(pdev, 1);
	if (IS_ERR(c->sen)) return PTR_ERR(c->sen);
	c->ana = devm_platform_ioremap_resource(pdev, 2);
	if (IS_ERR(c->ana)) return PTR_ERR(c->ana);
	c->nclks = devm_clk_bulk_get_all(dev, &c->clks);
	if (c->nclks < 0) return c->nclks;
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
	ret = v4l2_device_register(dev, &c->v4l2);
	if (ret) return ret;
	v4l2_ctrl_handler_init(&c->controls, 2);
	v4l2_ctrl_new_std(&c->controls, &control_ops, V4L2_CID_EXPOSURE, 4, 4000, 1, 600);
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
