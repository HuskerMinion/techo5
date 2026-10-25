// SPDX-License-Identifier: GPL-2.0
/*
 * OmniVision OV13855 13 MP sensor driver (MIPI CSI-2, 4 lanes, RAW10).
 *
 * The front camera of the Echo Show 8 2nd gen (athena). Written in the style of the mainline
 * OmniVision drivers (CCI register access, subdev state, runtime PM); the register tables are
 * Rockchip's, from drivers/media/i2c/ov13855.c in their develop-6.1 kernel (Copyright (C) 2021
 * Rockchip Electronics Co., Ltd., GPL-2.0), which says they are for a 24 MHz input clock.
 *
 * Two modes, both in reach of the MT8183 seninf in one-pixel mode (about 312 Mpixel/s):
 *   2112x1568, 2x2 binned, 540 Mbit/s a lane (the live view and the stream)
 *   4224x3136, full, 1080 Mbit/s a lane, 15 frames a second (stills)
 */

#include <linux/clk.h>
#include <linux/delay.h>
#include <linux/gpio/consumer.h>
#include <linux/i2c.h>
#include <linux/module.h>
#include <linux/pm_runtime.h>
#include <linux/regulator/consumer.h>
#include <media/v4l2-cci.h>
#include <media/v4l2-ctrls.h>
#include <media/v4l2-device.h>
#include <media/v4l2-fwnode.h>

#define OV13855_MCLK			24000000
#define OV13855_LANES			4
#define OV13855_BPP			10
#define OV13855_MBUS_CODE		MEDIA_BUS_FMT_SBGGR10_1X10

#define OV13855_REG_CHIP_ID		CCI_REG24(0x300a)
#define OV13855_CHIP_ID			0x00d855
#define OV13855_REG_REVISION		CCI_REG8(0x302a)

#define OV13855_REG_SOFTWARE_RESET	CCI_REG8(0x0103)
#define OV13855_REG_MODE_SELECT		CCI_REG8(0x0100)
#define OV13855_MODE_STANDBY		0x00
#define OV13855_MODE_STREAMING		0x01

/* in lines, with 4 fractional bits below */
#define OV13855_REG_EXPOSURE		CCI_REG24(0x3500)
#define OV13855_EXPOSURE_MIN		4
#define OV13855_EXPOSURE_MARGIN		8

/* 0x80 is 1x, 0x7c0 15.5x */
#define OV13855_REG_ANALOG_GAIN		CCI_REG16(0x3508)
#define OV13855_GAIN_MIN		0x80
#define OV13855_GAIN_MAX		0x7c0
#define OV13855_GAIN_DEFAULT		0x80

#define OV13855_REG_VTS			CCI_REG16(0x380e)
#define OV13855_VTS_MAX			0x7fff

/*
 * As on the OV13858; Rockchip's driver writes 0x5e00, which showed no pattern on the unit. The mode
 * tables clear this one.
 */
#define OV13855_REG_TEST_PATTERN	CCI_REG8(0x4503)
#define OV13855_TEST_PATTERN_ENABLE	BIT(7)

#include "ov13855-tables.inc"

static const s64 ov13855_link_freqs[] = {
	540000000,	/* full */
	270000000,	/* binned */
};

/*
 * vts is the frame length register (0x380e) the mode table sets. line is a line's length in
 * PIXEL_RATE units, so that HBLANK (line - width) and VBLANK give the frame rate the way userspace
 * reckons it; the sensor's own HTS register counts in other units. Binned: 30 frames a second,
 * measured on a unit (216 Mpixel/s / (30 x 3209 lines)); full: the 15 Rockchip gives.
 */
struct ov13855_mode {
	u32 width, height;
	u32 line, vts;
	u32 link_freq_index;
	const struct cci_reg_sequence *regs;
	unsigned int num_regs;
};

static const struct ov13855_mode ov13855_modes[] = {
	{
		.width = 2112, .height = 1568,
		.line = 2244, .vts = 0x0c89,
		.link_freq_index = 1,
		.regs = ov13855_2112x1568_regs,
		.num_regs = ARRAY_SIZE(ov13855_2112x1568_regs),
	},
	{
		.width = 4224, .height = 3136,
		.line = 8961, .vts = 0x0c8e,
		.link_freq_index = 0,
		.regs = ov13855_4224x3136_regs,
		.num_regs = ARRAY_SIZE(ov13855_4224x3136_regs),
	},
};

static const char * const ov13855_supply_names[] = {
	"avdd",		/* analog, 2.8 V */
	"dovdd",	/* I/O, 1.8 V */
	"dvdd",		/* core, 1.2 V */
};

static const char * const ov13855_test_pattern_menu[] = {
	"Disabled",
	"Vertical Color Bar Type 1",
	"Vertical Color Bar Type 2",
	"Vertical Color Bar Type 3",
	"Vertical Color Bar Type 4",
};

struct ov13855 {
	struct device *dev;
	struct regmap *regmap;
	struct v4l2_subdev sd;
	struct media_pad pad;
	struct v4l2_ctrl_handler ctrls;

	struct v4l2_ctrl *link_freq;
	struct v4l2_ctrl *pixel_rate;
	struct v4l2_ctrl *hblank;
	struct v4l2_ctrl *vblank;
	struct v4l2_ctrl *exposure;

	struct clk *mclk;
	struct gpio_desc *reset;
	struct gpio_desc *powerdown;
	struct regulator_bulk_data supplies[ARRAY_SIZE(ov13855_supply_names)];

	const struct ov13855_mode *mode;
	bool identified;	/* the chip id has been read; see ov13855_probe */
};

static inline struct ov13855 *to_ov13855(struct v4l2_subdev *sd)
{
	return container_of(sd, struct ov13855, sd);
}

static s64 ov13855_pixel_rate(const struct ov13855_mode *mode)
{
	/* DDR lanes: two bits a clock */
	return div_u64(ov13855_link_freqs[mode->link_freq_index] * 2 * OV13855_LANES,
		       OV13855_BPP);
}

static int ov13855_set_ctrl(struct v4l2_ctrl *ctrl)
{
	struct ov13855 *ov = container_of(ctrl->handler, struct ov13855, ctrls);
	int ret = 0;

	if (ctrl->id == V4L2_CID_VBLANK) {
		s64 max = ov->mode->height + ctrl->val - OV13855_EXPOSURE_MARGIN;

		__v4l2_ctrl_modify_range(ov->exposure, ov->exposure->minimum, max,
					 ov->exposure->step,
					 min(ov->exposure->default_value, max));
	}

	/* applied on the next stream start when the sensor is off */
	if (!pm_runtime_get_if_in_use(ov->dev))
		return 0;

	switch (ctrl->id) {
	case V4L2_CID_EXPOSURE:
		cci_write(ov->regmap, OV13855_REG_EXPOSURE, (u64)ctrl->val << 4, &ret);
		break;
	case V4L2_CID_ANALOGUE_GAIN:
		cci_write(ov->regmap, OV13855_REG_ANALOG_GAIN, ctrl->val, &ret);
		break;
	case V4L2_CID_VBLANK:
		cci_write(ov->regmap, OV13855_REG_VTS, ov->mode->height + ctrl->val, &ret);
		break;
	case V4L2_CID_TEST_PATTERN:
		cci_write(ov->regmap, OV13855_REG_TEST_PATTERN,
			  ctrl->val ? (ctrl->val - 1) | OV13855_TEST_PATTERN_ENABLE : 0, &ret);
		break;
	case V4L2_CID_LINK_FREQ:
	case V4L2_CID_PIXEL_RATE:
	case V4L2_CID_HBLANK:
		/* follow the mode, nothing to write */
		break;
	default:
		ret = -EINVAL;
		break;
	}

	pm_runtime_put(ov->dev);

	return ret;
}

static const struct v4l2_ctrl_ops ov13855_ctrl_ops = {
	.s_ctrl = ov13855_set_ctrl,
};

/* Point the timing controls at a mode. Called with the control lock held. */
static void ov13855_apply_mode(struct ov13855 *ov, const struct ov13855_mode *mode)
{
	s64 hblank = mode->line - mode->width;
	s64 vblank = mode->vts - mode->height;

	ov->mode = mode;
	__v4l2_ctrl_s_ctrl(ov->link_freq, mode->link_freq_index);
	__v4l2_ctrl_s_ctrl_int64(ov->pixel_rate, ov13855_pixel_rate(mode));
	__v4l2_ctrl_modify_range(ov->hblank, hblank, hblank, 1, hblank);
	__v4l2_ctrl_modify_range(ov->vblank, vblank, OV13855_VTS_MAX - mode->height, 1, vblank);
	__v4l2_ctrl_s_ctrl(ov->vblank, vblank);
}

static int ov13855_init_controls(struct ov13855 *ov)
{
	const struct ov13855_mode *mode = &ov13855_modes[0];
	struct v4l2_ctrl_handler *h = &ov->ctrls;
	struct v4l2_fwnode_device_properties props;
	s64 rate_max = ov13855_pixel_rate(&ov13855_modes[1]);
	s64 vblank = mode->vts - mode->height;
	s64 hblank = mode->line - mode->width;
	s64 exposure_max = mode->vts - OV13855_EXPOSURE_MARGIN;
	int ret;

	v4l2_ctrl_handler_init(h, 10);

	ov->link_freq = v4l2_ctrl_new_int_menu(h, &ov13855_ctrl_ops, V4L2_CID_LINK_FREQ,
					       ARRAY_SIZE(ov13855_link_freqs) - 1,
					       mode->link_freq_index, ov13855_link_freqs);
	if (ov->link_freq)
		ov->link_freq->flags |= V4L2_CTRL_FLAG_READ_ONLY;

	ov->pixel_rate = v4l2_ctrl_new_std(h, &ov13855_ctrl_ops, V4L2_CID_PIXEL_RATE,
					   1, rate_max, 1, ov13855_pixel_rate(mode));

	ov->hblank = v4l2_ctrl_new_std(h, &ov13855_ctrl_ops, V4L2_CID_HBLANK,
				       hblank, hblank, 1, hblank);
	if (ov->hblank)
		ov->hblank->flags |= V4L2_CTRL_FLAG_READ_ONLY;

	ov->vblank = v4l2_ctrl_new_std(h, &ov13855_ctrl_ops, V4L2_CID_VBLANK,
				       vblank, OV13855_VTS_MAX - mode->height, 1, vblank);

	ov->exposure = v4l2_ctrl_new_std(h, &ov13855_ctrl_ops, V4L2_CID_EXPOSURE,
					 OV13855_EXPOSURE_MIN, exposure_max, 1,
					 exposure_max / 2);

	v4l2_ctrl_new_std(h, &ov13855_ctrl_ops, V4L2_CID_ANALOGUE_GAIN,
			  OV13855_GAIN_MIN, OV13855_GAIN_MAX, 1, OV13855_GAIN_DEFAULT);

	v4l2_ctrl_new_std_menu_items(h, &ov13855_ctrl_ops, V4L2_CID_TEST_PATTERN,
				     ARRAY_SIZE(ov13855_test_pattern_menu) - 1, 0, 0,
				     ov13855_test_pattern_menu);

	ret = v4l2_fwnode_device_parse(ov->dev, &props);
	if (ret)
		return ret;
	v4l2_ctrl_new_fwnode_properties(h, &ov13855_ctrl_ops, &props);

	if (h->error)
		return h->error;

	ov->mode = mode;
	ov->sd.ctrl_handler = h;

	return 0;
}

static void ov13855_fill_format(const struct ov13855_mode *mode, struct v4l2_mbus_framefmt *fmt)
{
	fmt->width = mode->width;
	fmt->height = mode->height;
	fmt->code = OV13855_MBUS_CODE;
	fmt->field = V4L2_FIELD_NONE;
	fmt->colorspace = V4L2_COLORSPACE_RAW;
	fmt->ycbcr_enc = V4L2_YCBCR_ENC_601;
	fmt->quantization = V4L2_QUANTIZATION_FULL_RANGE;
	fmt->xfer_func = V4L2_XFER_FUNC_NONE;
}

static int ov13855_identify(struct ov13855 *ov);

static int ov13855_enable_streams(struct v4l2_subdev *sd, struct v4l2_subdev_state *state,
				  u32 pad, u64 streams_mask)
{
	struct ov13855 *ov = to_ov13855(sd);
	int ret;

	ret = pm_runtime_resume_and_get(ov->dev);
	if (ret)
		return ret;

	if (!ov->identified) {
		ret = ov13855_identify(ov);
		if (ret)
			goto out;
	}

	ret = cci_write(ov->regmap, OV13855_REG_SOFTWARE_RESET, 0x01, NULL);
	if (ret)
		goto out;
	usleep_range(5000, 6000);

	ret = cci_multi_reg_write(ov->regmap, ov13855_global_regs,
				  ARRAY_SIZE(ov13855_global_regs), NULL);
	if (!ret)
		ret = cci_multi_reg_write(ov->regmap, ov->mode->regs, ov->mode->num_regs, NULL);
	if (ret) {
		dev_err(ov->dev, "writing the %ux%u mode failed: %d\n",
			ov->mode->width, ov->mode->height, ret);
		goto out;
	}

	ret = __v4l2_ctrl_handler_setup(ov->sd.ctrl_handler);
	if (ret)
		goto out;

	ret = cci_write(ov->regmap, OV13855_REG_MODE_SELECT, OV13855_MODE_STREAMING, NULL);
out:
	if (ret)
		pm_runtime_put(ov->dev);

	return ret;
}

static int ov13855_disable_streams(struct v4l2_subdev *sd, struct v4l2_subdev_state *state,
				   u32 pad, u64 streams_mask)
{
	struct ov13855 *ov = to_ov13855(sd);

	cci_write(ov->regmap, OV13855_REG_MODE_SELECT, OV13855_MODE_STANDBY, NULL);
	pm_runtime_put(ov->dev);

	return 0;
}

static int ov13855_set_format(struct v4l2_subdev *sd, struct v4l2_subdev_state *state,
			      struct v4l2_subdev_format *fmt)
{
	struct ov13855 *ov = to_ov13855(sd);
	const struct ov13855_mode *mode;

	/* the mode's registers are only written at stream start */
	if (fmt->which == V4L2_SUBDEV_FORMAT_ACTIVE && v4l2_subdev_is_streaming(sd))
		return -EBUSY;

	mode = v4l2_find_nearest_size(ov13855_modes, ARRAY_SIZE(ov13855_modes), width, height,
				      fmt->format.width, fmt->format.height);
	ov13855_fill_format(mode, &fmt->format);
	*v4l2_subdev_state_get_format(state, fmt->pad) = fmt->format;

	if (fmt->which == V4L2_SUBDEV_FORMAT_ACTIVE)
		ov13855_apply_mode(ov, mode);

	return 0;
}

static int ov13855_enum_mbus_code(struct v4l2_subdev *sd, struct v4l2_subdev_state *state,
				  struct v4l2_subdev_mbus_code_enum *code)
{
	if (code->index > 0)
		return -EINVAL;
	code->code = OV13855_MBUS_CODE;

	return 0;
}

static int ov13855_enum_frame_size(struct v4l2_subdev *sd, struct v4l2_subdev_state *state,
				   struct v4l2_subdev_frame_size_enum *fse)
{
	if (fse->index >= ARRAY_SIZE(ov13855_modes) || fse->code != OV13855_MBUS_CODE)
		return -EINVAL;

	fse->min_width = fse->max_width = ov13855_modes[fse->index].width;
	fse->min_height = fse->max_height = ov13855_modes[fse->index].height;

	return 0;
}

static int ov13855_init_state(struct v4l2_subdev *sd, struct v4l2_subdev_state *state)
{
	ov13855_fill_format(&ov13855_modes[0], v4l2_subdev_state_get_format(state, 0));

	return 0;
}

static const struct v4l2_subdev_video_ops ov13855_video_ops = {
	.s_stream = v4l2_subdev_s_stream_helper,
};

static const struct v4l2_subdev_pad_ops ov13855_pad_ops = {
	.set_fmt = ov13855_set_format,
	.get_fmt = v4l2_subdev_get_fmt,
	.enum_mbus_code = ov13855_enum_mbus_code,
	.enum_frame_size = ov13855_enum_frame_size,
	.enable_streams = ov13855_enable_streams,
	.disable_streams = ov13855_disable_streams,
};

static const struct v4l2_subdev_ops ov13855_subdev_ops = {
	.video = &ov13855_video_ops,
	.pad = &ov13855_pad_ops,
};

static const struct media_entity_operations ov13855_entity_ops = {
	.link_validate = v4l2_subdev_link_validate,
};

static const struct v4l2_subdev_internal_ops ov13855_internal_ops = {
	.init_state = ov13855_init_state,
};

static int ov13855_power_off(struct device *dev)
{
	struct ov13855 *ov = to_ov13855(dev_get_drvdata(dev));

	gpiod_set_value_cansleep(ov->powerdown, 1);
	gpiod_set_value_cansleep(ov->reset, 1);
	clk_disable_unprepare(ov->mclk);
	regulator_bulk_disable(ARRAY_SIZE(ov->supplies), ov->supplies);

	return 0;
}

static int ov13855_power_on(struct device *dev)
{
	struct ov13855 *ov = to_ov13855(dev_get_drvdata(dev));
	int ret;

	/* the order MediaTek's own power-up uses for this sensor: rails, clock, then out of reset */
	ret = regulator_bulk_enable(ARRAY_SIZE(ov->supplies), ov->supplies);
	if (ret) {
		dev_err(dev, "enabling the supplies failed: %d\n", ret);
		return ret;
	}

	ret = clk_prepare_enable(ov->mclk);
	if (ret) {
		dev_err(dev, "enabling the clock failed: %d\n", ret);
		regulator_bulk_disable(ARRAY_SIZE(ov->supplies), ov->supplies);
		return ret;
	}

	usleep_range(1000, 1100);
	gpiod_set_value_cansleep(ov->powerdown, 0);
	usleep_range(1000, 1100);
	gpiod_set_value_cansleep(ov->reset, 0);
	/* 8192 clocks before the first SCCB access; at 24 MHz well under 1 ms */
	usleep_range(5000, 6000);

	return 0;
}

static int ov13855_identify(struct ov13855 *ov)
{
	u64 id = 0, rev = 0;
	int ret = 0;

	cci_read(ov->regmap, OV13855_REG_CHIP_ID, &id, &ret);
	cci_read(ov->regmap, OV13855_REG_REVISION, &rev, &ret);
	if (ret)
		return dev_err_probe(ov->dev, ret, "reading the chip id failed\n");
	if (id != OV13855_CHIP_ID)
		return dev_err_probe(ov->dev, -ENODEV, "chip id %06llx, not an OV13855\n", id);

	dev_info(ov->dev, "OV13855 revision %02llx\n", rev);
	ov->identified = true;

	return 0;
}

static int ov13855_check_hwcfg(struct ov13855 *ov)
{
	struct v4l2_fwnode_endpoint bus = { .bus_type = V4L2_MBUS_CSI2_DPHY };
	struct fwnode_handle *ep;
	unsigned long freqs;
	int ret;

	ep = fwnode_graph_get_endpoint_by_id(dev_fwnode(ov->dev), 0, 0, 0);
	if (!ep)
		return dev_err_probe(ov->dev, -EPROBE_DEFER, "waiting for the endpoint\n");

	ret = v4l2_fwnode_endpoint_alloc_parse(ep, &bus);
	fwnode_handle_put(ep);
	if (ret)
		return dev_err_probe(ov->dev, ret, "parsing the endpoint failed\n");

	if (bus.bus.mipi_csi2.num_data_lanes != OV13855_LANES) {
		ret = dev_err_probe(ov->dev, -EINVAL, "%u data lanes, the modes need %u\n",
				    bus.bus.mipi_csi2.num_data_lanes, OV13855_LANES);
		goto out;
	}

	ret = v4l2_link_freq_to_bitmap(ov->dev, bus.link_frequencies, bus.nr_of_link_frequencies,
				       ov13855_link_freqs, ARRAY_SIZE(ov13855_link_freqs), &freqs);
	if (!ret && freqs != GENMASK(ARRAY_SIZE(ov13855_link_freqs) - 1, 0))
		ret = dev_err_probe(ov->dev, -EINVAL, "both link frequencies are needed\n");
out:
	v4l2_fwnode_endpoint_free(&bus);

	return ret;
}

static int ov13855_probe(struct i2c_client *client)
{
	struct device *dev = &client->dev;
	struct ov13855 *ov;
	unsigned int i;
	int ret;

	ov = devm_kzalloc(dev, sizeof(*ov), GFP_KERNEL);
	if (!ov)
		return -ENOMEM;
	ov->dev = dev;

	ov->mclk = devm_v4l2_sensor_clk_get(dev, NULL);
	if (IS_ERR(ov->mclk))
		return dev_err_probe(dev, PTR_ERR(ov->mclk), "no clock\n");
	if (clk_get_rate(ov->mclk) != OV13855_MCLK)
		return dev_err_probe(dev, -EINVAL, "clock at %lu Hz, the tables want %u\n",
				     clk_get_rate(ov->mclk), OV13855_MCLK);

	v4l2_i2c_subdev_init(&ov->sd, client, &ov13855_subdev_ops);

	ret = ov13855_check_hwcfg(ov);
	if (ret)
		return ret;

	ov->reset = devm_gpiod_get_optional(dev, "reset", GPIOD_OUT_HIGH);
	if (IS_ERR(ov->reset))
		return dev_err_probe(dev, PTR_ERR(ov->reset), "no reset gpio\n");
	ov->powerdown = devm_gpiod_get_optional(dev, "powerdown", GPIOD_OUT_HIGH);
	if (IS_ERR(ov->powerdown))
		return dev_err_probe(dev, PTR_ERR(ov->powerdown), "no powerdown gpio\n");

	for (i = 0; i < ARRAY_SIZE(ov->supplies); i++)
		ov->supplies[i].supply = ov13855_supply_names[i];
	ret = devm_regulator_bulk_get(dev, ARRAY_SIZE(ov->supplies), ov->supplies);
	if (ret)
		return dev_err_probe(dev, ret, "no supplies\n");

	ov->regmap = devm_cci_regmap_init_i2c(client, 16);
	if (IS_ERR(ov->regmap))
		return PTR_ERR(ov->regmap);

	ret = ov13855_power_on(dev);
	if (ret)
		return ret;

	/*
	 * The mute latch may hold the camera off (the 1st gen Show 8's does), and a unit started
	 * muted would then lose its camera until the next boot. A sensor that does not answer (a
	 * bit-banged bus says -ENXIO for that) is registered all the same and identified at its
	 * first stream start; only a wrong id fails.
	 */
	ret = ov13855_identify(ov);
	if (ret == -ENODEV)
		goto err_power_off;
	if (ret)
		dev_warn(dev, "not answering (muted?); it is checked again when it starts\n");

	ret = ov13855_init_controls(ov);
	if (ret) {
		dev_err_probe(dev, ret, "controls\n");
		goto err_ctrls;
	}

	ov->sd.internal_ops = &ov13855_internal_ops;
	ov->sd.flags |= V4L2_SUBDEV_FL_HAS_DEVNODE;
	ov->sd.entity.ops = &ov13855_entity_ops;
	ov->sd.entity.function = MEDIA_ENT_F_CAM_SENSOR;
	ov->pad.flags = MEDIA_PAD_FL_SOURCE;
	ret = media_entity_pads_init(&ov->sd.entity, 1, &ov->pad);
	if (ret)
		goto err_ctrls;

	ov->sd.state_lock = ov->ctrls.lock;
	ret = v4l2_subdev_init_finalize(&ov->sd);
	if (ret)
		goto err_entity;

	pm_runtime_set_active(dev);
	pm_runtime_enable(dev);

	ret = v4l2_async_register_subdev_sensor(&ov->sd);
	if (ret) {
		dev_err_probe(dev, ret, "registering the subdev\n");
		goto err_subdev;
	}

	pm_runtime_idle(dev);

	return 0;

err_subdev:
	pm_runtime_disable(dev);
	pm_runtime_set_suspended(dev);
	v4l2_subdev_cleanup(&ov->sd);
err_entity:
	media_entity_cleanup(&ov->sd.entity);
err_ctrls:
	v4l2_ctrl_handler_free(&ov->ctrls);
err_power_off:
	ov13855_power_off(dev);

	return ret;
}

static void ov13855_remove(struct i2c_client *client)
{
	struct v4l2_subdev *sd = i2c_get_clientdata(client);
	struct ov13855 *ov = to_ov13855(sd);

	v4l2_async_unregister_subdev(sd);
	v4l2_subdev_cleanup(sd);
	media_entity_cleanup(&sd->entity);
	v4l2_ctrl_handler_free(&ov->ctrls);
	pm_runtime_disable(ov->dev);
	if (!pm_runtime_status_suspended(ov->dev)) {
		ov13855_power_off(ov->dev);
		pm_runtime_set_suspended(ov->dev);
	}
}

static DEFINE_RUNTIME_DEV_PM_OPS(ov13855_pm_ops, ov13855_power_off, ov13855_power_on, NULL);

static const struct of_device_id ov13855_of_match[] = {
	{ .compatible = "ovti,ov13855" },
	{ /* sentinel */ }
};
MODULE_DEVICE_TABLE(of, ov13855_of_match);

static struct i2c_driver ov13855_driver = {
	.driver = {
		.name = "ov13855",
		.pm = pm_ptr(&ov13855_pm_ops),
		.of_match_table = ov13855_of_match,
	},
	.probe = ov13855_probe,
	.remove = ov13855_remove,
};
module_i2c_driver(ov13855_driver);

MODULE_DESCRIPTION("OmniVision OV13855 sensor driver");
MODULE_LICENSE("GPL");
