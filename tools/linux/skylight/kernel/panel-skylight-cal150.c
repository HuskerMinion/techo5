// SPDX-License-Identifier: GPL-2.0-only
/*
 * Skylight Calendar 15" (150-CAL) display: a 1920x1080 LVDS panel behind a DSI-to-LVDS bridge
 * that is set up with DSI generic writes. The bridge chip is not identified; the writes and the
 * timing are the ones the unit's stock device tree gives its "simple-panel-dsi" node.
 */

#include <linux/delay.h>
#include <linux/gpio/consumer.h>
#include <linux/module.h>
#include <linux/of.h>

#include <drm/drm_connector.h>
#include <drm/drm_mipi_dsi.h>
#include <drm/drm_modes.h>
#include <drm/drm_panel.h>
#include <drm/drm_probe_helper.h>

struct skylight_panel {
	struct drm_panel panel;
	struct mipi_dsi_device *dsi;
	struct gpio_desc *power;
	struct gpio_desc *enable;
};

static inline struct skylight_panel *to_skylight(struct drm_panel *panel)
{
	return container_of(panel, struct skylight_panel, panel);
}

static int skylight_panel_init(struct skylight_panel *ctx)
{
	struct mipi_dsi_multi_context dsi_ctx = { .dsi = ctx->dsi };

	/* Stock panel-init-sequence, in order (the 0x17..0x1a group really is sent twice). */
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x27, 0xaa);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x48, 0x02);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0xb6, 0x20);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x01, 0x80);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x02, 0x38);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x03, 0x47);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x04, 0x3c);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x05, 0x8c);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x06, 0x30);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x07, 0x00);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x08, 0x21);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x09, 0x2d);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x0a, 0x08);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x0b, 0x82);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x0c, 0x13);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x0d, 0x01);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x0e, 0x80);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x0f, 0x20);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x10, 0x20);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x11, 0x03);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x12, 0x1b);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x13, 0x63);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x14, 0x01);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x15, 0x23);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x16, 0x40);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x17, 0x00);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x18, 0x01);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x19, 0x23);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x1a, 0x40);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x17, 0x00);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x18, 0x01);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x19, 0x23);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x1a, 0x40);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x1b, 0x00);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x1e, 0x46);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x51, 0x30);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x1f, 0x10);
	mipi_dsi_generic_write_seq_multi(&dsi_ctx, 0x2a, 0x01);

	return dsi_ctx.accum_err;
}

static int skylight_panel_prepare(struct drm_panel *panel)
{
	struct skylight_panel *ctx = to_skylight(panel);
	int ret;

	/* Stock delays: prepare 10 ms, init 120 ms. */
	gpiod_set_value_cansleep(ctx->power, 1);
	msleep(10);
	gpiod_set_value_cansleep(ctx->enable, 1);
	msleep(120);

	ret = skylight_panel_init(ctx);
	if (ret) {
		dev_err(&ctx->dsi->dev, "bridge init failed: %d\n", ret);
		gpiod_set_value_cansleep(ctx->enable, 0);
		gpiod_set_value_cansleep(ctx->power, 0);
		return ret;
	}

	return 0;
}

static int skylight_panel_enable(struct drm_panel *panel)
{
	msleep(50);	/* stock enable delay, before the backlight comes on */
	return 0;
}

static int skylight_panel_disable(struct drm_panel *panel)
{
	msleep(20);	/* stock disable delay */
	return 0;
}

static int skylight_panel_unprepare(struct drm_panel *panel)
{
	struct skylight_panel *ctx = to_skylight(panel);

	gpiod_set_value_cansleep(ctx->enable, 0);
	msleep(20);	/* stock unprepare delay */
	gpiod_set_value_cansleep(ctx->power, 0);

	return 0;
}

/* Stock display-timings: 152 MHz, 1920+60+140+48 x 1080+33+45+8. */
static const struct drm_display_mode skylight_panel_mode = {
	.clock		= 152000,
	.hdisplay	= 1920,
	.hsync_start	= 1920 + 60,
	.hsync_end	= 1920 + 60 + 140,
	.htotal		= 1920 + 60 + 140 + 48,
	.vdisplay	= 1080,
	.vsync_start	= 1080 + 33,
	.vsync_end	= 1080 + 33 + 45,
	.vtotal		= 1080 + 33 + 45 + 8,
	.width_mm	= 229,
	.height_mm	= 143,
	.flags		= DRM_MODE_FLAG_NHSYNC | DRM_MODE_FLAG_NVSYNC,
	.type		= DRM_MODE_TYPE_DRIVER | DRM_MODE_TYPE_PREFERRED,
};

static int skylight_panel_get_modes(struct drm_panel *panel, struct drm_connector *connector)
{
	connector->display_info.bpc = 8;
	return drm_connector_helper_get_modes_fixed(connector, &skylight_panel_mode);
}

static const struct drm_panel_funcs skylight_panel_funcs = {
	.prepare = skylight_panel_prepare,
	.enable = skylight_panel_enable,
	.disable = skylight_panel_disable,
	.unprepare = skylight_panel_unprepare,
	.get_modes = skylight_panel_get_modes,
};

static int skylight_panel_probe(struct mipi_dsi_device *dsi)
{
	struct device *dev = &dsi->dev;
	struct skylight_panel *ctx;
	int ret;

	ctx = devm_drm_panel_alloc(dev, struct skylight_panel, panel, &skylight_panel_funcs,
				   DRM_MODE_CONNECTOR_DSI);
	if (IS_ERR(ctx))
		return PTR_ERR(ctx);

	mipi_dsi_set_drvdata(dsi, ctx);
	ctx->dsi = dsi;

	/* Stock dsi,flags 0xa03 (4.19 numbering): video, burst, no EoT packet, LP commands. */
	dsi->lanes = 4;
	dsi->format = MIPI_DSI_FMT_RGB888;
	dsi->mode_flags = MIPI_DSI_MODE_VIDEO | MIPI_DSI_MODE_VIDEO_BURST |
			  MIPI_DSI_MODE_NO_EOT_PACKET | MIPI_DSI_MODE_LPM;

	/*
	 * Outputs, low until the first prepare (GPIOD_ASIS would leave them as inputs, and setting
	 * the value of an input drives nothing: the panel stayed off that way).
	 */
	ctx->power = devm_gpiod_get(dev, "power", GPIOD_OUT_LOW);
	if (IS_ERR(ctx->power))
		return dev_err_probe(dev, PTR_ERR(ctx->power), "no power GPIO\n");

	ctx->enable = devm_gpiod_get(dev, "enable", GPIOD_OUT_LOW);
	if (IS_ERR(ctx->enable))
		return dev_err_probe(dev, PTR_ERR(ctx->enable), "no enable GPIO\n");

	ret = drm_panel_of_backlight(&ctx->panel);
	if (ret)
		return ret;

	drm_panel_add(&ctx->panel);

	ret = mipi_dsi_attach(dsi);
	if (ret < 0) {
		drm_panel_remove(&ctx->panel);
		return dev_err_probe(dev, ret, "DSI attach failed\n");
	}

	return 0;
}

static void skylight_panel_remove(struct mipi_dsi_device *dsi)
{
	struct skylight_panel *ctx = mipi_dsi_get_drvdata(dsi);

	mipi_dsi_detach(dsi);
	drm_panel_remove(&ctx->panel);
}

static const struct of_device_id skylight_panel_of_match[] = {
	{ .compatible = "skylight,cal150-panel" },
	{ /* sentinel */ }
};
MODULE_DEVICE_TABLE(of, skylight_panel_of_match);

static struct mipi_dsi_driver skylight_panel_driver = {
	.driver = {
		.name = "panel-skylight-cal150",
		.of_match_table = skylight_panel_of_match,
	},
	.probe = skylight_panel_probe,
	.remove = skylight_panel_remove,
};
module_mipi_dsi_driver(skylight_panel_driver);

MODULE_DESCRIPTION("Skylight Calendar 15 (150-CAL) DSI-to-LVDS panel");
MODULE_LICENSE("GPL");
