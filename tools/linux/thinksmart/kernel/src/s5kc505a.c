// SPDX-License-Identifier: GPL-2.0
/*
 * Samsung S5KC505A 5 Mpixel raw Bayer sensor driver.
 *
 * The register tables, timings and power sequence come from the camera
 * module support library shipped on the Lenovo ThinkSmart View CD-18781Y,
 * decoded for interoperability. The register map follows the SMIA/CCS layout
 * (0x0100 mode select, 0x0202 coarse integration, 0x0204 analogue gain,
 * 0x0340/0x0342 frame/line length) and the tables are the same as the
 * vendor's S5K4E8 ones, apart from the chip ID and one init register.
 *
 * Output is RAW10 over two CSI-2 D-PHY data lanes at 960 Mb/s per lane
 * (480 MHz link frequency) from a 24 MHz external clock: GRBG as read out,
 * another Bayer order when the image is mirrored or flipped.
 *
 * Written by Jetha Chan (jethac/lenovo-thinksmart-view, kernel patches 0402
 * and 0404). thinksmart-view-debian adds the horizontal and vertical flip
 * controls (register 0x0101) with the Bayer order that follows them, so that
 * the read-out can be turned upright in the sensor.
 */

#include <linux/clk.h>
#include <linux/delay.h>
#include <linux/gpio/consumer.h>
#include <linux/i2c.h>
#include <linux/module.h>
#include <linux/pm_runtime.h>
#include <linux/regulator/consumer.h>
#include <linux/units.h>
#include <media/v4l2-cci.h>
#include <media/v4l2-ctrls.h>
#include <media/v4l2-device.h>
#include <media/v4l2-fwnode.h>

#define S5KC505A_LINK_FREQ		(480 * HZ_PER_MHZ)
#define S5KC505A_MCLK_FREQ		(24 * HZ_PER_MHZ)
#define S5KC505A_DATA_LANES		2

/*
 * Video timing pixel rate. Every mode programs the same PLL (24 MHz / 4 x 95,
 * vt_pix_clk_div 6, two pixels per clock), i.e. 190 Mpixel/s. The vendor
 * library lists 200 and 189.09 MHz for the 60 and 85 fps modes, apparently so
 * its frame rate arithmetic comes out at round numbers; the PLL is unchanged.
 */
#define S5KC505A_PIXEL_RATE		190000000

#define S5KC505A_REG_CHIP_ID		CCI_REG16(0x0000)
#define S5KC505A_CHIP_ID		0x5034
#define S5KC505A_REG_REVISION		CCI_REG8(0x0002)
#define S5KC505A_REG_MANUFACTURER	CCI_REG8(0x0003)
#define S5KC505A_REG_SMIA_VERSION	CCI_REG8(0x0004)
/* Read back as 0x5203 on this module; logged only, not relied upon */
#define S5KC505A_REG_VENDOR_ID		CCI_REG16(0x300a)

#define S5KC505A_REG_MODE_SELECT	CCI_REG8(0x0100)
#define S5KC505A_MODE_STREAMING		0x01
#define S5KC505A_REG_ORIENTATION	CCI_REG8(0x0101)
#define S5KC505A_HFLIP			BIT(0)
#define S5KC505A_VFLIP			BIT(1)

#define S5KC505A_REG_EXTCLK_FREQ	CCI_REG16(0x0136)
#define S5KC505A_EXTCLK_24MHZ		0x1800	/* 8.8 fixed point MHz */

#define S5KC505A_REG_EXPOSURE		CCI_REG16(0x0202)
#define S5KC505A_EXPOSURE_MIN		4
#define S5KC505A_EXPOSURE_STEP		1
#define S5KC505A_EXPOSURE_MARGIN	8	/* vendor "vertical offset" */

/* Analogue gain code: 32 is 1x, vendor clamps to 0x1ff (~16x) */
#define S5KC505A_REG_AGAIN		CCI_REG16(0x0204)
#define S5KC505A_AGAIN_MIN		0x20
#define S5KC505A_AGAIN_MAX		0x1ff
#define S5KC505A_AGAIN_STEP		1
#define S5KC505A_AGAIN_DEFAULT		0x20

#define S5KC505A_REG_VTS		CCI_REG16(0x0340)
#define S5KC505A_VTS_MAX		0xffff

#define S5KC505A_REG_TEST_PATTERN	CCI_REG16(0x0600)

/* Pixel array: 2624x1976 with 8 dummy pixels on each side */
#define S5KC505A_NATIVE_WIDTH		2624
#define S5KC505A_NATIVE_HEIGHT		1976
#define S5KC505A_ACTIVE_LEFT		8
#define S5KC505A_ACTIVE_TOP		8
#define S5KC505A_ACTIVE_WIDTH		2608
#define S5KC505A_ACTIVE_HEIGHT		1960

#define to_s5kc505a(_sd)		container_of(_sd, struct s5kc505a, sd)

static const s64 s5kc505a_link_freq_menu[] = {
	S5KC505A_LINK_FREQ,
};

/*
 * Bayer order for each flip setting, indexed by vflip << 1 | hflip: the
 * read-out window does not move when the sensor mirrors or flips, so the
 * order changes with them (both flips give GBRG, checked on the device).
 */
static const u32 s5kc505a_mbus_formats[] = {
	MEDIA_BUS_FMT_SGRBG10_1X10,	MEDIA_BUS_FMT_SRGGB10_1X10,
	MEDIA_BUS_FMT_SBGGR10_1X10,	MEDIA_BUS_FMT_SGBRG10_1X10,
};

struct s5kc505a_reg_list {
	const struct cci_reg_sequence *regs;
	unsigned int num_regs;
};

struct s5kc505a_mode {
	u32 width;
	u32 height;
	u32 hts;			/* line_length_pck */
	u32 vts;			/* default frame_length_lines */
	u32 exposure;			/* default coarse integration */
	u64 pixel_rate;			/* video timing pixel clock */
	struct v4l2_rect crop;		/* analogue crop in active array */
	struct s5kc505a_reg_list reg_list;
};

static const char * const s5kc505a_test_pattern_menu[] = {
	"Disabled",
	"Solid colour",
	"Colour bars",
	"Fade to grey colour bars",
	"PN9",
};

/*
 * Power-up order from the vendor library: analogue, digital core, I/O, then
 * MCLK, then reset and power-down released. Supplies are enabled one by one
 * in this order, not in bulk.
 */
static const char * const s5kc505a_supply_names[] = {
	"vdda",		/* analogue, 2.85 V */
	"vddd",		/* digital core, 1.2 V */
	"vddio",	/* digital I/O, 1.8 V */
};

#define S5KC505A_NUM_SUPPLIES		ARRAY_SIZE(s5kc505a_supply_names)

struct s5kc505a {
	struct device *dev;
	struct regmap *regmap;
	struct clk *mclk;
	struct gpio_desc *reset_gpio;
	struct gpio_desc *powerdown_gpio;
	struct regulator_bulk_data supplies[S5KC505A_NUM_SUPPLIES];

	struct v4l2_subdev sd;
	struct media_pad pad;

	struct v4l2_ctrl_handler ctrl_handler;
	struct v4l2_ctrl *link_freq;
	struct v4l2_ctrl *pixel_rate;
	struct v4l2_ctrl *hblank;
	struct v4l2_ctrl *vblank;
	struct v4l2_ctrl *exposure;
	struct v4l2_ctrl *hflip;
	struct v4l2_ctrl *vflip;

	const struct s5kc505a_mode *mode;
};

/* Vendor init, part 1: ends with OTP page select (0x0a02), then 10 ms */
static const struct cci_reg_sequence s5kc505a_init_a[] = {
	{ CCI_REG8(0x0000), 0x10 },
	{ CCI_REG8(0x0000), 0x60 },
	{ CCI_REG8(0x0000), 0x04 },
	{ CCI_REG8(0x0000), 0xe8 },
	{ CCI_REG8(0x0a02), 0x0f },
};

/* Vendor init, part 2: analogue and readout tuning */
static const struct cci_reg_sequence s5kc505a_init_b[] = {
	{ CCI_REG8(0x340f), 0x80 },
	{ CCI_REG8(0x3457), 0x04 },
	{ CCI_REG8(0x3419), 0x00 },
	{ CCI_REG8(0x3416), 0x40 },
	{ CCI_REG8(0x3418), 0x00 },
	{ CCI_REG8(0x3419), 0x04 },
	{ CCI_REG8(0x3419), 0x00 },
	{ CCI_REG8(0x3416), 0x40 },
	{ CCI_REG8(0x3418), 0x24 },
	{ CCI_REG8(0x3419), 0x04 },
	{ CCI_REG8(0x3419), 0x00 },
	{ CCI_REG8(0x3416), 0x40 },
	{ CCI_REG8(0x3418), 0x48 },
	{ CCI_REG8(0x3419), 0x04 },
	{ CCI_REG8(0x3419), 0x00 },
	{ CCI_REG8(0x3416), 0x40 },
	{ CCI_REG8(0x3418), 0x6c },
	{ CCI_REG8(0x3419), 0x04 },
	{ CCI_REG8(0x3419), 0x00 },
	{ CCI_REG8(0x3457), 0x0c },
	{ CCI_REG8(0x3457), 0x04 },
	{ CCI_REG8(0x307c), 0x16 },
	{ CCI_REG8(0x3087), 0xdc },
	{ CCI_REG8(0x3089), 0x0a },
	{ CCI_REG8(0x308b), 0x08 },
	{ CCI_REG8(0x308c), 0x0b },
	{ CCI_REG8(0x308e), 0x36 },
	{ CCI_REG8(0x3075), 0x4d },
	{ CCI_REG8(0x306f), 0xa8 },
	{ CCI_REG8(0x306a), 0x79 },
	{ CCI_REG8(0x3077), 0x10 },
	{ CCI_REG8(0x30b0), 0xc8 },
	{ CCI_REG8(0x3078), 0x17 },
	{ CCI_REG8(0x3070), 0x0f },
	{ CCI_REG8(0x0b00), 0x01 },
	{ CCI_REG8(0x0801), 0x10 },
	{ CCI_REG8(0x0804), 0x10 },
	{ CCI_REG8(0x3931), 0x02 },
	{ CCI_REG8(0x3930), 0x80 },
	{ CCI_REG8(0x3932), 0x60 },
};

/* Mode 0: 2608x1960 full array, 30 fps */
static const struct cci_reg_sequence s5kc505a_mode0_regs[] = {
	{ CCI_REG8(0x0301), 0x06 },
	{ CCI_REG8(0x0305), 0x04 },
	{ CCI_REG8(0x0306), 0x00 },
	{ CCI_REG8(0x0307), 0x5f },
	{ CCI_REG8(0x030d), 0x04 },
	{ CCI_REG8(0x030e), 0x00 },
	{ CCI_REG8(0x030f), 0xa0 },
	{ CCI_REG8(0x3c17), 0x00 },
	{ CCI_REG8(0x3c1f), 0x00 },
	{ CCI_REG8(0x0820), 0x03 },
	{ CCI_REG8(0x0821), 0xc0 },
	{ CCI_REG8(0x0b05), 0x01 },
	{ CCI_REG8(0x3303), 0x02 },
	{ CCI_REG8(0x3301), 0x00 },
	{ CCI_REG8(0x3400), 0x00 },
	{ CCI_REG8(0x0b00), 0x00 },
	{ CCI_REG8(0x3459), 0x75 },
	{ CCI_REG8(0x0200), 0x00 },
	{ CCI_REG8(0x0201), 0x00 },
	{ CCI_REG8(0x0202), 0x00 },
	{ CCI_REG8(0x0203), 0x04 },
	{ CCI_REG8(0x0204), 0x00 },
	{ CCI_REG8(0x0205), 0x20 },
	{ CCI_REG8(0x3c0f), 0x01 },
	{ CCI_REG8(0x020e), 0x01 },
	{ CCI_REG8(0x020f), 0x00 },
	{ CCI_REG8(0x0210), 0x01 },
	{ CCI_REG8(0x0211), 0x00 },
	{ CCI_REG8(0x0212), 0x01 },
	{ CCI_REG8(0x0213), 0x00 },
	{ CCI_REG8(0x0214), 0x01 },
	{ CCI_REG8(0x0215), 0x00 },
	{ CCI_REG8(0x0344), 0x00 },
	{ CCI_REG8(0x0345), 0x00 },
	{ CCI_REG8(0x0346), 0x00 },
	{ CCI_REG8(0x0347), 0x00 },
	{ CCI_REG8(0x0348), 0x0a },
	{ CCI_REG8(0x0349), 0x2f },
	{ CCI_REG8(0x034a), 0x07 },
	{ CCI_REG8(0x034b), 0xa7 },
	{ CCI_REG8(0x034c), 0x0a },
	{ CCI_REG8(0x034d), 0x30 },
	{ CCI_REG8(0x034e), 0x07 },
	{ CCI_REG8(0x034f), 0xa8 },
	{ CCI_REG8(0x0340), 0x07 },
	{ CCI_REG8(0x0341), 0xee },
	{ CCI_REG8(0x0342), 0x0c },
	{ CCI_REG8(0x0343), 0x28 },
	{ CCI_REG8(0x0900), 0x00 },
	{ CCI_REG8(0x0901), 0x10 },
	{ CCI_REG8(0x0383), 0x01 },
	{ CCI_REG8(0x0387), 0x01 },
	{ CCI_REG8(0x300f), 0x01 },
	{ CCI_REG8(0x3010), 0x77 },
	{ CCI_REG8(0x3014), 0x01 },
	{ CCI_REG8(0x3015), 0x86 },
	{ CCI_REG8(0x3929), 0x07 },
	{ CCI_REG8(0x3906), 0x76 },
	{ CCI_REG8(0x3c02), 0x00 },
	{ CCI_REG8(0x3c03), 0xff },
};

/* Mode 1: 1304x980 2x2 binned, 30 fps */
static const struct cci_reg_sequence s5kc505a_mode1_regs[] = {
	{ CCI_REG8(0x0301), 0x06 },
	{ CCI_REG8(0x0305), 0x04 },
	{ CCI_REG8(0x0306), 0x00 },
	{ CCI_REG8(0x0307), 0x5f },
	{ CCI_REG8(0x030d), 0x04 },
	{ CCI_REG8(0x030e), 0x00 },
	{ CCI_REG8(0x030f), 0xa0 },
	{ CCI_REG8(0x3c17), 0x00 },
	{ CCI_REG8(0x3c1f), 0x00 },
	{ CCI_REG8(0x0820), 0x03 },
	{ CCI_REG8(0x0821), 0xc0 },
	{ CCI_REG8(0x0b05), 0x01 },
	{ CCI_REG8(0x3303), 0x02 },
	{ CCI_REG8(0x3301), 0x10 },
	{ CCI_REG8(0x3400), 0x00 },
	{ CCI_REG8(0x0b00), 0x00 },
	{ CCI_REG8(0x3459), 0x75 },
	{ CCI_REG8(0x0200), 0x00 },
	{ CCI_REG8(0x0201), 0x00 },
	{ CCI_REG8(0x0202), 0x00 },
	{ CCI_REG8(0x0203), 0x04 },
	{ CCI_REG8(0x0204), 0x00 },
	{ CCI_REG8(0x0205), 0x20 },
	{ CCI_REG8(0x3c0f), 0x01 },
	{ CCI_REG8(0x020e), 0x01 },
	{ CCI_REG8(0x020f), 0x00 },
	{ CCI_REG8(0x0210), 0x01 },
	{ CCI_REG8(0x0211), 0x00 },
	{ CCI_REG8(0x0212), 0x01 },
	{ CCI_REG8(0x0213), 0x00 },
	{ CCI_REG8(0x0214), 0x01 },
	{ CCI_REG8(0x0215), 0x00 },
	{ CCI_REG8(0x0344), 0x00 },
	{ CCI_REG8(0x0345), 0x00 },
	{ CCI_REG8(0x0346), 0x00 },
	{ CCI_REG8(0x0347), 0x00 },
	{ CCI_REG8(0x0348), 0x0a },
	{ CCI_REG8(0x0349), 0x2f },
	{ CCI_REG8(0x034a), 0x07 },
	{ CCI_REG8(0x034b), 0xa7 },
	{ CCI_REG8(0x034c), 0x05 },
	{ CCI_REG8(0x034d), 0x18 },
	{ CCI_REG8(0x034e), 0x03 },
	{ CCI_REG8(0x034f), 0xd4 },
	{ CCI_REG8(0x0340), 0x07 },
	{ CCI_REG8(0x0341), 0xee },
	{ CCI_REG8(0x0342), 0x0c },
	{ CCI_REG8(0x0343), 0x28 },
	{ CCI_REG8(0x0900), 0x01 },
	{ CCI_REG8(0x0901), 0x22 },
	{ CCI_REG8(0x0383), 0x01 },
	{ CCI_REG8(0x0387), 0x03 },
	{ CCI_REG8(0x300f), 0x01 },
	{ CCI_REG8(0x3010), 0x77 },
	{ CCI_REG8(0x3014), 0x01 },
	{ CCI_REG8(0x3015), 0x86 },
	{ CCI_REG8(0x3929), 0x07 },
	{ CCI_REG8(0x3906), 0x76 },
	{ CCI_REG8(0x3c02), 0x00 },
	{ CCI_REG8(0x3c03), 0xff },
};

/* Mode 2: 1936x1096 centre crop of the full array (no binning), 60 fps */
static const struct cci_reg_sequence s5kc505a_mode2_regs[] = {
	{ CCI_REG8(0x0301), 0x06 },
	{ CCI_REG8(0x0305), 0x04 },
	{ CCI_REG8(0x0306), 0x00 },
	{ CCI_REG8(0x0307), 0x5f },
	{ CCI_REG8(0x030d), 0x04 },
	{ CCI_REG8(0x030e), 0x00 },
	{ CCI_REG8(0x030f), 0xa0 },
	{ CCI_REG8(0x3c17), 0x00 },
	{ CCI_REG8(0x3c1f), 0x00 },
	{ CCI_REG8(0x0820), 0x03 },
	{ CCI_REG8(0x0821), 0x5a },
	{ CCI_REG8(0x0b05), 0x01 },
	{ CCI_REG8(0x3303), 0x02 },
	{ CCI_REG8(0x3301), 0x00 },
	{ CCI_REG8(0x3400), 0x00 },
	{ CCI_REG8(0x0b00), 0x00 },
	{ CCI_REG8(0x3459), 0x75 },
	{ CCI_REG8(0x0200), 0x00 },
	{ CCI_REG8(0x0201), 0x00 },
	{ CCI_REG8(0x0202), 0x00 },
	{ CCI_REG8(0x0203), 0x04 },
	{ CCI_REG8(0x0204), 0x00 },
	{ CCI_REG8(0x0205), 0x20 },
	{ CCI_REG8(0x3c0f), 0x01 },
	{ CCI_REG8(0x020e), 0x01 },
	{ CCI_REG8(0x020f), 0x00 },
	{ CCI_REG8(0x0210), 0x01 },
	{ CCI_REG8(0x0211), 0x00 },
	{ CCI_REG8(0x0212), 0x01 },
	{ CCI_REG8(0x0213), 0x00 },
	{ CCI_REG8(0x0214), 0x01 },
	{ CCI_REG8(0x0215), 0x00 },
	{ CCI_REG8(0x0344), 0x01 },
	{ CCI_REG8(0x0345), 0x50 },
	{ CCI_REG8(0x0346), 0x01 },
	{ CCI_REG8(0x0347), 0xb0 },
	{ CCI_REG8(0x0348), 0x08 },
	{ CCI_REG8(0x0349), 0xdf },
	{ CCI_REG8(0x034a), 0x05 },
	{ CCI_REG8(0x034b), 0xf7 },
	{ CCI_REG8(0x034c), 0x07 },
	{ CCI_REG8(0x034d), 0x90 },
	{ CCI_REG8(0x034e), 0x04 },
	{ CCI_REG8(0x034f), 0x48 },
	{ CCI_REG8(0x0340), 0x04 },
	{ CCI_REG8(0x0341), 0x8e },
	{ CCI_REG8(0x0342), 0x0a },
	{ CCI_REG8(0x0343), 0xc8 },
	{ CCI_REG8(0x0900), 0x00 },
	{ CCI_REG8(0x0901), 0x10 },
	{ CCI_REG8(0x0383), 0x01 },
	{ CCI_REG8(0x0387), 0x01 },
	{ CCI_REG8(0x300f), 0x03 },
	{ CCI_REG8(0x3010), 0xb8 },
	{ CCI_REG8(0x3014), 0x03 },
	{ CCI_REG8(0x3015), 0xc7 },
	{ CCI_REG8(0x3929), 0x0f },
	{ CCI_REG8(0x3906), 0x92 },
	{ CCI_REG8(0x3c02), 0x00 },
	{ CCI_REG8(0x3c03), 0xff },
};

/* Mode 3: 1296x736 2x2 binned, 85 fps */
static const struct cci_reg_sequence s5kc505a_mode3_regs[] = {
	{ CCI_REG8(0x0301), 0x06 },
	{ CCI_REG8(0x0305), 0x04 },
	{ CCI_REG8(0x0306), 0x00 },
	{ CCI_REG8(0x0307), 0x5f },
	{ CCI_REG8(0x030d), 0x04 },
	{ CCI_REG8(0x030e), 0x00 },
	{ CCI_REG8(0x030f), 0xa0 },
	{ CCI_REG8(0x3c17), 0x00 },
	{ CCI_REG8(0x3c1f), 0x00 },
	{ CCI_REG8(0x0820), 0x03 },
	{ CCI_REG8(0x0821), 0x5a },
	{ CCI_REG8(0x0b05), 0x01 },
	{ CCI_REG8(0x3303), 0x02 },
	{ CCI_REG8(0x3301), 0x10 },
	{ CCI_REG8(0x3400), 0x00 },
	{ CCI_REG8(0x0b00), 0x00 },
	{ CCI_REG8(0x3459), 0x75 },
	{ CCI_REG8(0x0200), 0x00 },
	{ CCI_REG8(0x0201), 0x00 },
	{ CCI_REG8(0x0202), 0x00 },
	{ CCI_REG8(0x0203), 0x04 },
	{ CCI_REG8(0x0204), 0x00 },
	{ CCI_REG8(0x0205), 0x20 },
	{ CCI_REG8(0x3c0f), 0x01 },
	{ CCI_REG8(0x020e), 0x01 },
	{ CCI_REG8(0x020f), 0x00 },
	{ CCI_REG8(0x0210), 0x01 },
	{ CCI_REG8(0x0211), 0x00 },
	{ CCI_REG8(0x0212), 0x01 },
	{ CCI_REG8(0x0213), 0x00 },
	{ CCI_REG8(0x0214), 0x01 },
	{ CCI_REG8(0x0215), 0x00 },
	{ CCI_REG8(0x0344), 0x00 },
	{ CCI_REG8(0x0345), 0x08 },
	{ CCI_REG8(0x0346), 0x00 },
	{ CCI_REG8(0x0347), 0xf4 },
	{ CCI_REG8(0x0348), 0x0a },
	{ CCI_REG8(0x0349), 0x27 },
	{ CCI_REG8(0x034a), 0x06 },
	{ CCI_REG8(0x034b), 0xb3 },
	{ CCI_REG8(0x034c), 0x05 },
	{ CCI_REG8(0x034d), 0x10 },
	{ CCI_REG8(0x034e), 0x02 },
	{ CCI_REG8(0x034f), 0xe0 },
	{ CCI_REG8(0x0340), 0x03 },
	{ CCI_REG8(0x0341), 0x26 },
	{ CCI_REG8(0x0342), 0x0a },
	{ CCI_REG8(0x0343), 0xc8 },
	{ CCI_REG8(0x0900), 0x01 },
	{ CCI_REG8(0x0901), 0x22 },
	{ CCI_REG8(0x0383), 0x01 },
	{ CCI_REG8(0x0387), 0x03 },
	{ CCI_REG8(0x300f), 0x02 },
	{ CCI_REG8(0x3010), 0x41 },
	{ CCI_REG8(0x3014), 0x02 },
	{ CCI_REG8(0x3015), 0x50 },
	{ CCI_REG8(0x3929), 0x0f },
	{ CCI_REG8(0x3906), 0x40 },
	{ CCI_REG8(0x3c02), 0x00 },
	{ CCI_REG8(0x3c03), 0xff },
};


static const struct s5kc505a_mode s5kc505a_supported_modes[] = {
	{
		.width = 2608,
		.height = 1960,
		.hts = 3112,
		.vts = 2030,
		.exposure = 2000,
		.pixel_rate = S5KC505A_PIXEL_RATE,
		.crop = { 0, 0, 2608, 1960 },
		.reg_list = {
			.regs = s5kc505a_mode0_regs,
			.num_regs = ARRAY_SIZE(s5kc505a_mode0_regs),
		},
	},
	{
		.width = 1936,
		.height = 1096,
		.hts = 2760,
		.vts = 1166,
		.exposure = 1100,
		.pixel_rate = S5KC505A_PIXEL_RATE,
		.crop = { 336, 432, 1936, 1096 },
		.reg_list = {
			.regs = s5kc505a_mode2_regs,
			.num_regs = ARRAY_SIZE(s5kc505a_mode2_regs),
		},
	},
	{
		.width = 1304,
		.height = 980,
		.hts = 3112,
		.vts = 2030,
		.exposure = 2000,
		.pixel_rate = S5KC505A_PIXEL_RATE,
		.crop = { 0, 0, 2608, 1960 },
		.reg_list = {
			.regs = s5kc505a_mode1_regs,
			.num_regs = ARRAY_SIZE(s5kc505a_mode1_regs),
		},
	},
	{
		.width = 1296,
		.height = 736,
		.hts = 2760,
		.vts = 806,
		.exposure = 780,
		.pixel_rate = S5KC505A_PIXEL_RATE,
		.crop = { 8, 244, 2592, 1472 },
		.reg_list = {
			.regs = s5kc505a_mode3_regs,
			.num_regs = ARRAY_SIZE(s5kc505a_mode3_regs),
		},
	},
};

static u32 s5kc505a_get_format_code(struct s5kc505a *s5kc505a)
{
	unsigned int i;

	i = (s5kc505a->vflip->val ? 2 : 0) | (s5kc505a->hflip->val ? 1 : 0);

	return s5kc505a_mbus_formats[i];
}

static u32 s5kc505a_get_orientation(struct s5kc505a *s5kc505a)
{
	return (s5kc505a->vflip->val ? S5KC505A_VFLIP : 0) |
	       (s5kc505a->hflip->val ? S5KC505A_HFLIP : 0);
}

static int s5kc505a_set_ctrl(struct v4l2_ctrl *ctrl)
{
	struct s5kc505a *s5kc505a = container_of(ctrl->handler,
						 struct s5kc505a, ctrl_handler);
	const struct s5kc505a_mode *mode = s5kc505a->mode;
	struct v4l2_subdev_state *state;
	s64 exposure_max;
	int ret;

	if (ctrl->id == V4L2_CID_HFLIP || ctrl->id == V4L2_CID_VFLIP) {
		/* The Bayer order of the active format follows the flips */
		state = v4l2_subdev_get_locked_active_state(&s5kc505a->sd);
		v4l2_subdev_state_get_format(state, 0)->code =
			s5kc505a_get_format_code(s5kc505a);
	}

	if (ctrl->id == V4L2_CID_VBLANK) {
		exposure_max = mode->height + ctrl->val -
			       S5KC505A_EXPOSURE_MARGIN;
		__v4l2_ctrl_modify_range(s5kc505a->exposure,
					 s5kc505a->exposure->minimum,
					 exposure_max,
					 s5kc505a->exposure->step,
					 min(s5kc505a->exposure->default_value,
					     exposure_max));
	}

	/* Controls are applied when the sensor is powered up for streaming */
	if (!pm_runtime_get_if_active(s5kc505a->dev))
		return 0;

	switch (ctrl->id) {
	case V4L2_CID_ANALOGUE_GAIN:
		ret = cci_write(s5kc505a->regmap, S5KC505A_REG_AGAIN,
				ctrl->val, NULL);
		break;
	case V4L2_CID_EXPOSURE:
		ret = cci_write(s5kc505a->regmap, S5KC505A_REG_EXPOSURE,
				ctrl->val, NULL);
		break;
	case V4L2_CID_VBLANK:
		ret = cci_write(s5kc505a->regmap, S5KC505A_REG_VTS,
				ctrl->val + mode->height, NULL);
		break;
	case V4L2_CID_TEST_PATTERN:
		ret = cci_write(s5kc505a->regmap, S5KC505A_REG_TEST_PATTERN,
				ctrl->val, NULL);
		break;
	case V4L2_CID_HFLIP:
	case V4L2_CID_VFLIP:
		ret = cci_write(s5kc505a->regmap, S5KC505A_REG_ORIENTATION,
				s5kc505a_get_orientation(s5kc505a), NULL);
		break;
	case V4L2_CID_HBLANK:
	case V4L2_CID_PIXEL_RATE:
	case V4L2_CID_LINK_FREQ:
		/*
		 * Read-only, follow the mode; set_pad_format updates them
		 * and can do so while the sensor is still runtime active.
		 */
		ret = 0;
		break;
	default:
		ret = -EINVAL;
		break;
	}

	pm_runtime_put(s5kc505a->dev);

	return ret;
}

static const struct v4l2_ctrl_ops s5kc505a_ctrl_ops = {
	.s_ctrl = s5kc505a_set_ctrl,
};

static int s5kc505a_init_controls(struct s5kc505a *s5kc505a)
{
	struct v4l2_ctrl_handler *hdl = &s5kc505a->ctrl_handler;
	const struct s5kc505a_mode *mode = s5kc505a->mode;
	struct v4l2_fwnode_device_properties props;
	s64 hblank, vblank, exposure_max;
	int ret;

	v4l2_ctrl_handler_init(hdl, 12);

	s5kc505a->link_freq =
		v4l2_ctrl_new_int_menu(hdl, &s5kc505a_ctrl_ops,
				       V4L2_CID_LINK_FREQ,
				       ARRAY_SIZE(s5kc505a_link_freq_menu) - 1,
				       0, s5kc505a_link_freq_menu);
	if (s5kc505a->link_freq)
		s5kc505a->link_freq->flags |= V4L2_CTRL_FLAG_READ_ONLY;

	s5kc505a->pixel_rate =
		v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops, V4L2_CID_PIXEL_RATE,
				  1, S5KC505A_PIXEL_RATE, 1, mode->pixel_rate);
	if (s5kc505a->pixel_rate)
		s5kc505a->pixel_rate->flags |= V4L2_CTRL_FLAG_READ_ONLY;

	hblank = mode->hts - mode->width;
	s5kc505a->hblank = v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops,
					     V4L2_CID_HBLANK, hblank, hblank,
					     1, hblank);
	if (s5kc505a->hblank)
		s5kc505a->hblank->flags |= V4L2_CTRL_FLAG_READ_ONLY;

	vblank = mode->vts - mode->height;
	s5kc505a->vblank = v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops,
					     V4L2_CID_VBLANK, vblank,
					     S5KC505A_VTS_MAX - mode->height,
					     1, vblank);

	v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops, V4L2_CID_ANALOGUE_GAIN,
			  S5KC505A_AGAIN_MIN, S5KC505A_AGAIN_MAX,
			  S5KC505A_AGAIN_STEP, S5KC505A_AGAIN_DEFAULT);

	exposure_max = mode->vts - S5KC505A_EXPOSURE_MARGIN;
	s5kc505a->exposure = v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops,
					       V4L2_CID_EXPOSURE,
					       S5KC505A_EXPOSURE_MIN,
					       exposure_max,
					       S5KC505A_EXPOSURE_STEP,
					       mode->exposure);

	v4l2_ctrl_new_std_menu_items(hdl, &s5kc505a_ctrl_ops,
				     V4L2_CID_TEST_PATTERN,
				     ARRAY_SIZE(s5kc505a_test_pattern_menu) - 1,
				     0, 0, s5kc505a_test_pattern_menu);

	s5kc505a->hflip = v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops,
					    V4L2_CID_HFLIP, 0, 1, 1, 0);
	if (s5kc505a->hflip)
		s5kc505a->hflip->flags |= V4L2_CTRL_FLAG_MODIFY_LAYOUT;

	s5kc505a->vflip = v4l2_ctrl_new_std(hdl, &s5kc505a_ctrl_ops,
					    V4L2_CID_VFLIP, 0, 1, 1, 0);
	if (s5kc505a->vflip)
		s5kc505a->vflip->flags |= V4L2_CTRL_FLAG_MODIFY_LAYOUT;

	ret = v4l2_fwnode_device_parse(s5kc505a->dev, &props);
	if (ret)
		goto error;

	ret = v4l2_ctrl_new_fwnode_properties(hdl, &s5kc505a_ctrl_ops, &props);
	if (ret)
		goto error;

	if (hdl->error) {
		ret = hdl->error;
		goto error;
	}

	s5kc505a->sd.ctrl_handler = hdl;

	return 0;

error:
	v4l2_ctrl_handler_free(hdl);

	return ret;
}

static int s5kc505a_enable_streams(struct v4l2_subdev *sd,
				   struct v4l2_subdev_state *state, u32 pad,
				   u64 streams_mask)
{
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);
	const struct s5kc505a_reg_list *reg_list = &s5kc505a->mode->reg_list;
	int ret;

	ret = pm_runtime_resume_and_get(s5kc505a->dev);
	if (ret)
		return ret;

	ret = cci_multi_reg_write(s5kc505a->regmap, s5kc505a_init_a,
				  ARRAY_SIZE(s5kc505a_init_a), NULL);
	if (ret)
		goto error;

	usleep_range(10 * USEC_PER_MSEC, 11 * USEC_PER_MSEC);

	cci_multi_reg_write(s5kc505a->regmap, s5kc505a_init_b,
			    ARRAY_SIZE(s5kc505a_init_b), &ret);
	cci_write(s5kc505a->regmap, S5KC505A_REG_EXTCLK_FREQ,
		  S5KC505A_EXTCLK_24MHZ, &ret);
	cci_multi_reg_write(s5kc505a->regmap, reg_list->regs,
			    reg_list->num_regs, &ret);
	if (ret)
		goto error;

	ret = __v4l2_ctrl_handler_setup(s5kc505a->sd.ctrl_handler);
	if (ret)
		goto error;

	cci_write(s5kc505a->regmap, S5KC505A_REG_ORIENTATION,
		  s5kc505a_get_orientation(s5kc505a), &ret);
	cci_write(s5kc505a->regmap, S5KC505A_REG_MODE_SELECT,
		  S5KC505A_MODE_STREAMING, &ret);
	if (ret)
		goto error;

	/* The Bayer order cannot change while streaming */
	__v4l2_ctrl_grab(s5kc505a->hflip, true);
	__v4l2_ctrl_grab(s5kc505a->vflip, true);

	return 0;

error:
	dev_err(s5kc505a->dev, "failed to start streaming: %d\n", ret);
	pm_runtime_put_autosuspend(s5kc505a->dev);

	return ret;
}

static int s5kc505a_disable_streams(struct v4l2_subdev *sd,
				    struct v4l2_subdev_state *state, u32 pad,
				    u64 streams_mask)
{
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);
	int ret;

	ret = cci_write(s5kc505a->regmap, S5KC505A_REG_MODE_SELECT, 0, NULL);
	if (ret)
		dev_err(s5kc505a->dev, "failed to stop streaming: %d\n", ret);

	__v4l2_ctrl_grab(s5kc505a->hflip, false);
	__v4l2_ctrl_grab(s5kc505a->vflip, false);

	pm_runtime_put_autosuspend(s5kc505a->dev);

	return ret;
}

static void s5kc505a_update_pad_format(struct s5kc505a *s5kc505a,
				       const struct s5kc505a_mode *mode,
				       struct v4l2_mbus_framefmt *fmt)
{
	fmt->code = s5kc505a_get_format_code(s5kc505a);
	fmt->width = mode->width;
	fmt->height = mode->height;
	fmt->field = V4L2_FIELD_NONE;
	fmt->colorspace = V4L2_COLORSPACE_RAW;
	fmt->ycbcr_enc = V4L2_YCBCR_ENC_601;
	fmt->quantization = V4L2_QUANTIZATION_FULL_RANGE;
	fmt->xfer_func = V4L2_XFER_FUNC_NONE;
}

static int s5kc505a_set_pad_format(struct v4l2_subdev *sd,
				   struct v4l2_subdev_state *state,
				   struct v4l2_subdev_format *fmt)
{
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);
	const struct s5kc505a_mode *mode;
	s64 hblank, vblank, exposure_max;
	int ret;

	mode = v4l2_find_nearest_size(s5kc505a_supported_modes,
				      ARRAY_SIZE(s5kc505a_supported_modes),
				      width, height,
				      fmt->format.width, fmt->format.height);

	s5kc505a_update_pad_format(s5kc505a, mode, &fmt->format);
	*v4l2_subdev_state_get_format(state, 0) = fmt->format;
	*v4l2_subdev_state_get_crop(state, 0) = mode->crop;

	if (fmt->which == V4L2_SUBDEV_FORMAT_TRY || s5kc505a->mode == mode)
		return 0;

	if (v4l2_subdev_is_streaming(sd))
		return -EBUSY;

	s5kc505a->mode = mode;

	ret = __v4l2_ctrl_s_ctrl_int64(s5kc505a->pixel_rate, mode->pixel_rate);
	if (ret)
		return ret;

	hblank = mode->hts - mode->width;
	ret = __v4l2_ctrl_modify_range(s5kc505a->hblank, hblank, hblank, 1,
				       hblank);
	if (ret)
		return ret;

	vblank = mode->vts - mode->height;
	ret = __v4l2_ctrl_modify_range(s5kc505a->vblank, vblank,
				       S5KC505A_VTS_MAX - mode->height, 1,
				       vblank);
	if (ret)
		return ret;
	ret = __v4l2_ctrl_s_ctrl(s5kc505a->vblank, vblank);
	if (ret)
		return ret;

	exposure_max = mode->vts - S5KC505A_EXPOSURE_MARGIN;
	ret = __v4l2_ctrl_modify_range(s5kc505a->exposure,
				       S5KC505A_EXPOSURE_MIN, exposure_max,
				       S5KC505A_EXPOSURE_STEP, mode->exposure);
	if (ret)
		return ret;

	return __v4l2_ctrl_s_ctrl(s5kc505a->exposure, mode->exposure);
}

static int s5kc505a_enum_mbus_code(struct v4l2_subdev *sd,
				   struct v4l2_subdev_state *state,
				   struct v4l2_subdev_mbus_code_enum *code)
{
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);

	if (code->index > 0)
		return -EINVAL;

	code->code = s5kc505a_get_format_code(s5kc505a);

	return 0;
}

static int s5kc505a_enum_frame_size(struct v4l2_subdev *sd,
				    struct v4l2_subdev_state *state,
				    struct v4l2_subdev_frame_size_enum *fse)
{
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);

	if (fse->index >= ARRAY_SIZE(s5kc505a_supported_modes))
		return -EINVAL;

	if (fse->code != s5kc505a_get_format_code(s5kc505a))
		return -EINVAL;

	fse->min_width = s5kc505a_supported_modes[fse->index].width;
	fse->max_width = fse->min_width;
	fse->min_height = s5kc505a_supported_modes[fse->index].height;
	fse->max_height = fse->min_height;

	return 0;
}

static int s5kc505a_get_selection(struct v4l2_subdev *sd,
				  struct v4l2_subdev_state *state,
				  struct v4l2_subdev_selection *sel)
{
	switch (sel->target) {
	case V4L2_SEL_TGT_CROP:
		sel->r = *v4l2_subdev_state_get_crop(state, 0);
		sel->r.left += S5KC505A_ACTIVE_LEFT;
		sel->r.top += S5KC505A_ACTIVE_TOP;
		return 0;
	case V4L2_SEL_TGT_NATIVE_SIZE:
		sel->r.left = 0;
		sel->r.top = 0;
		sel->r.width = S5KC505A_NATIVE_WIDTH;
		sel->r.height = S5KC505A_NATIVE_HEIGHT;
		return 0;
	case V4L2_SEL_TGT_CROP_DEFAULT:
	case V4L2_SEL_TGT_CROP_BOUNDS:
		sel->r.left = S5KC505A_ACTIVE_LEFT;
		sel->r.top = S5KC505A_ACTIVE_TOP;
		sel->r.width = S5KC505A_ACTIVE_WIDTH;
		sel->r.height = S5KC505A_ACTIVE_HEIGHT;
		return 0;
	default:
		return -EINVAL;
	}
}

static int s5kc505a_init_state(struct v4l2_subdev *sd,
			       struct v4l2_subdev_state *state)
{
	struct v4l2_subdev_format fmt = {
		.which = V4L2_SUBDEV_FORMAT_TRY,
		.pad = 0,
		.format = {
			.width = s5kc505a_supported_modes[0].width,
			.height = s5kc505a_supported_modes[0].height,
		},
	};

	return s5kc505a_set_pad_format(sd, state, &fmt);
}

static const struct v4l2_subdev_video_ops s5kc505a_video_ops = {
	.s_stream = v4l2_subdev_s_stream_helper,
};

static const struct v4l2_subdev_pad_ops s5kc505a_pad_ops = {
	.set_fmt = s5kc505a_set_pad_format,
	.get_fmt = v4l2_subdev_get_fmt,
	.get_selection = s5kc505a_get_selection,
	.enum_mbus_code = s5kc505a_enum_mbus_code,
	.enum_frame_size = s5kc505a_enum_frame_size,
	.enable_streams = s5kc505a_enable_streams,
	.disable_streams = s5kc505a_disable_streams,
};

static const struct v4l2_subdev_ops s5kc505a_subdev_ops = {
	.video = &s5kc505a_video_ops,
	.pad = &s5kc505a_pad_ops,
};

static const struct v4l2_subdev_internal_ops s5kc505a_internal_ops = {
	.init_state = s5kc505a_init_state,
};

static const struct media_entity_operations s5kc505a_entity_ops = {
	.link_validate = v4l2_subdev_link_validate,
};

/*
 * Bring-up aid: sources disagree on whether the module answers at 0x36 or
 * 0x37. If the configured address does not answer, look at the other one
 * and log what is there, so a wrong DT address is obvious from one boot.
 */
static void s5kc505a_probe_other_address(struct s5kc505a *s5kc505a)
{
	struct i2c_client *client = v4l2_get_subdevdata(&s5kc505a->sd);
	u16 other = client->addr == 0x37 ? 0x36 : 0x37;
	struct i2c_client *dummy;
	u8 addr[2] = { 0x00, 0x00 }, id[2];
	struct i2c_msg msgs[2] = {
		{ .addr = other, .flags = 0, .len = 2, .buf = addr },
		{ .addr = other, .flags = I2C_M_RD, .len = 2, .buf = id },
	};
	int ret;

	dummy = i2c_new_dummy_device(client->adapter, other);
	if (IS_ERR(dummy))
		return;

	ret = i2c_transfer(client->adapter, msgs, ARRAY_SIZE(msgs));
	if (ret == ARRAY_SIZE(msgs))
		dev_warn(s5kc505a->dev,
			 "no answer at 0x%02x, but 0x%02x has 0x0000 = 0x%02x%02x\n",
			 client->addr, other, id[0], id[1]);
	else
		dev_warn(s5kc505a->dev, "no answer at 0x%02x or 0x%02x (%d)\n",
			 client->addr, other, ret);

	i2c_unregister_device(dummy);
}

static int s5kc505a_identify_sensor(struct s5kc505a *s5kc505a)
{
	u64 id = 0, rev = 0, mfr = 0, smia = 0, vid = 0;
	unsigned int i;
	int ret;

	/* The ID registers can come up a few ms after the sensor answers */
	for (i = 0; i < 3; i++) {
		ret = cci_read(s5kc505a->regmap, S5KC505A_REG_CHIP_ID, &id, NULL);
		if (!ret && id == S5KC505A_CHIP_ID)
			break;
		usleep_range(5 * USEC_PER_MSEC, 6 * USEC_PER_MSEC);
	}

	if (ret) {
		s5kc505a_probe_other_address(s5kc505a);
		return dev_err_probe(s5kc505a->dev, ret,
				     "failed to read chip id\n");
	}

	cci_read(s5kc505a->regmap, S5KC505A_REG_REVISION, &rev, NULL);
	cci_read(s5kc505a->regmap, S5KC505A_REG_MANUFACTURER, &mfr, NULL);
	cci_read(s5kc505a->regmap, S5KC505A_REG_SMIA_VERSION, &smia, NULL);
	cci_read(s5kc505a->regmap, S5KC505A_REG_VENDOR_ID, &vid, NULL);

	dev_info(s5kc505a->dev,
		 "chip id 0x%04llx, revision 0x%02llx, manufacturer 0x%02llx, smia 0x%02llx, 0x300a 0x%04llx\n",
		 id, rev, mfr, smia, vid);

	if (id != S5KC505A_CHIP_ID)
		return dev_err_probe(s5kc505a->dev, -ENODEV,
				     "chip id mismatch: 0x%04llx != 0x%04x\n",
				     id, S5KC505A_CHIP_ID);

	return 0;
}

static int s5kc505a_check_hwcfg(struct s5kc505a *s5kc505a)
{
	struct fwnode_handle *fwnode = dev_fwnode(s5kc505a->dev), *ep;
	struct v4l2_fwnode_endpoint bus_cfg = {
		.bus_type = V4L2_MBUS_CSI2_DPHY,
	};
	unsigned long freq_bitmap;
	int ret;

	if (!fwnode)
		return -ENODEV;

	ep = fwnode_graph_get_next_endpoint(fwnode, NULL);
	if (!ep)
		return -EINVAL;

	ret = v4l2_fwnode_endpoint_alloc_parse(ep, &bus_cfg);
	fwnode_handle_put(ep);
	if (ret)
		return ret;

	if (bus_cfg.bus.mipi_csi2.num_data_lanes != S5KC505A_DATA_LANES) {
		dev_err(s5kc505a->dev, "unsupported number of data lanes: %u\n",
			bus_cfg.bus.mipi_csi2.num_data_lanes);
		ret = -EINVAL;
		goto out;
	}

	ret = v4l2_link_freq_to_bitmap(s5kc505a->dev, bus_cfg.link_frequencies,
				       bus_cfg.nr_of_link_frequencies,
				       s5kc505a_link_freq_menu,
				       ARRAY_SIZE(s5kc505a_link_freq_menu),
				       &freq_bitmap);
out:
	v4l2_fwnode_endpoint_free(&bus_cfg);

	return ret;
}

static int s5kc505a_power_on(struct device *dev)
{
	struct v4l2_subdev *sd = dev_get_drvdata(dev);
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);
	unsigned int i;
	int ret;

	/* Both lines held low (asserted) before any supply comes up */
	gpiod_set_value_cansleep(s5kc505a->reset_gpio, 1);
	gpiod_set_value_cansleep(s5kc505a->powerdown_gpio, 1);
	usleep_range(1000, 2000);

	for (i = 0; i < S5KC505A_NUM_SUPPLIES; i++) {
		ret = regulator_enable(s5kc505a->supplies[i].consumer);
		if (ret) {
			dev_err(dev, "failed to enable %s: %d\n",
				s5kc505a->supplies[i].supply, ret);
			goto disable_supplies;
		}
		usleep_range(1000, 2000);
	}

	ret = clk_prepare_enable(s5kc505a->mclk);
	if (ret)
		goto disable_supplies;
	usleep_range(1000, 2000);

	gpiod_set_value_cansleep(s5kc505a->reset_gpio, 0);
	usleep_range(10 * USEC_PER_MSEC, 11 * USEC_PER_MSEC);

	gpiod_set_value_cansleep(s5kc505a->powerdown_gpio, 0);
	usleep_range(10 * USEC_PER_MSEC, 11 * USEC_PER_MSEC);

	return 0;

disable_supplies:
	while (i--)
		regulator_disable(s5kc505a->supplies[i].consumer);

	return ret;
}

static int s5kc505a_power_off(struct device *dev)
{
	struct v4l2_subdev *sd = dev_get_drvdata(dev);
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);
	unsigned int i;

	/* Reverse of power-up, as in the vendor power-down table */
	gpiod_set_value_cansleep(s5kc505a->powerdown_gpio, 1);
	usleep_range(1000, 2000);
	gpiod_set_value_cansleep(s5kc505a->reset_gpio, 1);
	usleep_range(1000, 2000);

	clk_disable_unprepare(s5kc505a->mclk);
	usleep_range(1000, 2000);

	for (i = S5KC505A_NUM_SUPPLIES; i-- > 0; )
		regulator_disable(s5kc505a->supplies[i].consumer);

	return 0;
}

static int s5kc505a_probe(struct i2c_client *client)
{
	struct s5kc505a *s5kc505a;
	unsigned long freq;
	unsigned int i;
	int ret;

	s5kc505a = devm_kzalloc(&client->dev, sizeof(*s5kc505a), GFP_KERNEL);
	if (!s5kc505a)
		return -ENOMEM;

	s5kc505a->dev = &client->dev;
	v4l2_i2c_subdev_init(&s5kc505a->sd, client, &s5kc505a_subdev_ops);

	s5kc505a->regmap = devm_cci_regmap_init_i2c(client, 16);
	if (IS_ERR(s5kc505a->regmap))
		return dev_err_probe(s5kc505a->dev, PTR_ERR(s5kc505a->regmap),
				     "failed to init CCI\n");

	s5kc505a->mclk = devm_v4l2_sensor_clk_get(s5kc505a->dev, NULL);
	if (IS_ERR(s5kc505a->mclk))
		return dev_err_probe(s5kc505a->dev, PTR_ERR(s5kc505a->mclk),
				     "failed to get MCLK\n");

	freq = clk_get_rate(s5kc505a->mclk);
	if (freq != S5KC505A_MCLK_FREQ)
		return dev_err_probe(s5kc505a->dev, -EINVAL,
				     "MCLK %lu Hz not supported\n", freq);

	ret = s5kc505a_check_hwcfg(s5kc505a);
	if (ret)
		return dev_err_probe(s5kc505a->dev, ret,
				     "failed to check HW configuration\n");

	s5kc505a->reset_gpio = devm_gpiod_get(s5kc505a->dev, "reset",
					      GPIOD_OUT_HIGH);
	if (IS_ERR(s5kc505a->reset_gpio))
		return dev_err_probe(s5kc505a->dev,
				     PTR_ERR(s5kc505a->reset_gpio),
				     "cannot get reset GPIO\n");

	s5kc505a->powerdown_gpio = devm_gpiod_get_optional(s5kc505a->dev,
							   "powerdown",
							   GPIOD_OUT_HIGH);
	if (IS_ERR(s5kc505a->powerdown_gpio))
		return dev_err_probe(s5kc505a->dev,
				     PTR_ERR(s5kc505a->powerdown_gpio),
				     "cannot get powerdown GPIO\n");

	for (i = 0; i < S5KC505A_NUM_SUPPLIES; i++)
		s5kc505a->supplies[i].supply = s5kc505a_supply_names[i];

	ret = devm_regulator_bulk_get(s5kc505a->dev, S5KC505A_NUM_SUPPLIES,
				      s5kc505a->supplies);
	if (ret)
		return dev_err_probe(s5kc505a->dev, ret,
				     "failed to get supplies\n");

	ret = s5kc505a_power_on(s5kc505a->dev);
	if (ret)
		return ret;

	ret = s5kc505a_identify_sensor(s5kc505a);
	if (ret)
		goto power_off;

	s5kc505a->mode = &s5kc505a_supported_modes[0];
	ret = s5kc505a_init_controls(s5kc505a);
	if (ret) {
		dev_err_probe(s5kc505a->dev, ret, "failed to init controls\n");
		goto power_off;
	}

	s5kc505a->sd.state_lock = s5kc505a->ctrl_handler.lock;
	s5kc505a->sd.internal_ops = &s5kc505a_internal_ops;
	s5kc505a->sd.flags |= V4L2_SUBDEV_FL_HAS_DEVNODE;
	s5kc505a->sd.entity.ops = &s5kc505a_entity_ops;
	s5kc505a->sd.entity.function = MEDIA_ENT_F_CAM_SENSOR;
	s5kc505a->pad.flags = MEDIA_PAD_FL_SOURCE;

	ret = media_entity_pads_init(&s5kc505a->sd.entity, 1, &s5kc505a->pad);
	if (ret) {
		dev_err_probe(s5kc505a->dev, ret, "failed to init pads\n");
		goto free_ctrls;
	}

	ret = v4l2_subdev_init_finalize(&s5kc505a->sd);
	if (ret) {
		dev_err_probe(s5kc505a->dev, ret, "failed to init subdev\n");
		goto entity_cleanup;
	}

	pm_runtime_set_active(s5kc505a->dev);
	pm_runtime_enable(s5kc505a->dev);

	ret = v4l2_async_register_subdev_sensor(&s5kc505a->sd);
	if (ret) {
		dev_err_probe(s5kc505a->dev, ret, "failed to register subdev\n");
		goto subdev_cleanup;
	}

	pm_runtime_set_autosuspend_delay(s5kc505a->dev, 1000);
	pm_runtime_use_autosuspend(s5kc505a->dev);
	pm_runtime_idle(s5kc505a->dev);

	return 0;

subdev_cleanup:
	v4l2_subdev_cleanup(&s5kc505a->sd);
	pm_runtime_disable(s5kc505a->dev);
	pm_runtime_set_suspended(s5kc505a->dev);
entity_cleanup:
	media_entity_cleanup(&s5kc505a->sd.entity);
free_ctrls:
	v4l2_ctrl_handler_free(s5kc505a->sd.ctrl_handler);
power_off:
	s5kc505a_power_off(s5kc505a->dev);

	return ret;
}

static void s5kc505a_remove(struct i2c_client *client)
{
	struct v4l2_subdev *sd = i2c_get_clientdata(client);
	struct s5kc505a *s5kc505a = to_s5kc505a(sd);

	v4l2_async_unregister_subdev(sd);
	v4l2_subdev_cleanup(sd);
	media_entity_cleanup(&sd->entity);
	v4l2_ctrl_handler_free(sd->ctrl_handler);
	pm_runtime_disable(s5kc505a->dev);

	if (!pm_runtime_status_suspended(s5kc505a->dev)) {
		s5kc505a_power_off(s5kc505a->dev);
		pm_runtime_set_suspended(s5kc505a->dev);
	}
}

static const struct dev_pm_ops s5kc505a_pm_ops = {
	SET_RUNTIME_PM_OPS(s5kc505a_power_off, s5kc505a_power_on, NULL)
};

static const struct of_device_id s5kc505a_of_match[] = {
	{ .compatible = "samsung,s5kc505a" },
	{ /* sentinel */ }
};
MODULE_DEVICE_TABLE(of, s5kc505a_of_match);

static struct i2c_driver s5kc505a_i2c_driver = {
	.driver = {
		.name = "s5kc505a",
		.pm = &s5kc505a_pm_ops,
		.of_match_table = s5kc505a_of_match,
	},
	.probe = s5kc505a_probe,
	.remove = s5kc505a_remove,
};

module_i2c_driver(s5kc505a_i2c_driver);

MODULE_DESCRIPTION("Samsung S5KC505A image sensor driver");
MODULE_LICENSE("GPL");
