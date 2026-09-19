// SPDX-License-Identifier: GPL-2.0-only
/*
 * The Echo Show 5 (2021, cronos) panel: a 480x960 Sitronix ST7701S-driven DSI panel, two lanes,
 * burst video mode. The bootloader brings the panel up and shows its logo; the "truly" variant
 * every unit seen so far reports needs no init table (Amazon's 4.9 lcm driver sends none), so this
 * driver never resets it and only sleeps it in and out, like the tree's rook panel driver.
 * Timings from st7701s_wsvga_dsi_vdo_cronos.c in android_kernel_amazon_mt8163.
 */
#include <linux/delay.h>
#include <linux/gpio/consumer.h>
#include <linux/mod_devicetable.h>
#include <linux/module.h>
#include <linux/regulator/consumer.h>
#include <video/mipi_display.h>
#include <drm/drm_mipi_dsi.h>
#include <drm/drm_modes.h>
#include <drm/drm_panel.h>
#include <drm/drm_probe_helper.h>

struct cronos_panel {
	struct drm_panel panel;
	struct mipi_dsi_device *dsi;
	struct regulator_bulk_data *supplies;
	struct gpio_desc *reset_gpio;
};

static const struct regulator_bulk_data cronos_panel_supplies[] = {
	{ .supply = "vgp3" },	/* reg-lcm-supply in the 4.9 tree */
	{ .supply = "vio28" },	/* reg-lcm-supply1 */
};

static inline struct cronos_panel *to_cronos_panel(struct drm_panel *panel)
{
	return container_of(panel, struct cronos_panel, panel);
}

static int cronos_panel_prepare(struct drm_panel *panel)
{
	struct cronos_panel *ctx = to_cronos_panel(panel);

	ctx->dsi->mode_flags |= MIPI_DSI_MODE_LPM;
	return 0;
}

static int cronos_panel_unprepare(struct drm_panel *panel)
{
	struct cronos_panel *ctx = to_cronos_panel(panel);

	ctx->dsi->mode_flags &= ~MIPI_DSI_MODE_LPM;
	return 0;
}

static int cronos_panel_enable(struct drm_panel *panel)
{
	struct cronos_panel *ctx = to_cronos_panel(panel);
	struct mipi_dsi_multi_context dsi_ctx = { .dsi = ctx->dsi };

	mipi_dsi_dcs_exit_sleep_mode_multi(&dsi_ctx);
	mipi_dsi_msleep(&dsi_ctx, 120);
	mipi_dsi_dcs_set_display_on_multi(&dsi_ctx);
	mipi_dsi_msleep(&dsi_ctx, 20);
	return dsi_ctx.accum_err;
}

static int cronos_panel_disable(struct drm_panel *panel)
{
	struct cronos_panel *ctx = to_cronos_panel(panel);
	struct mipi_dsi_multi_context dsi_ctx = { .dsi = ctx->dsi };

	mipi_dsi_dcs_set_display_off_multi(&dsi_ctx);
	mipi_dsi_msleep(&dsi_ctx, 20);
	mipi_dsi_dcs_enter_sleep_mode_multi(&dsi_ctx);
	mipi_dsi_msleep(&dsi_ctx, 120);
	return dsi_ctx.accum_err;
}

/* 480x960 at 59.6 Hz: hsa 6, hbp 20, hfp 30; vsa 6, vbp 16, vfp 21; PLL 212 MHz on two lanes. */
static const struct drm_display_mode cronos_panel_mode = {
	.clock       = (480 + 30 + 6 + 20) * (960 + 21 + 6 + 16) * 60 / 1000,
	.hdisplay    = 480,
	.hsync_start = 480 + 30,
	.hsync_end   = 480 + 30 + 6,
	.htotal      = 480 + 30 + 6 + 20,
	.vdisplay    = 960,
	.vsync_start = 960 + 21,
	.vsync_end   = 960 + 21 + 6,
	.vtotal      = 960 + 21 + 6 + 16,
	.width_mm    = 68,
	.height_mm   = 136,
	.type        = DRM_MODE_TYPE_DRIVER | DRM_MODE_TYPE_PREFERRED,
};

static int cronos_panel_get_modes(struct drm_panel *panel, struct drm_connector *connector)
{
	return drm_connector_helper_get_modes_fixed(connector, &cronos_panel_mode);
}

static const struct drm_panel_funcs cronos_panel_funcs = {
	.prepare = cronos_panel_prepare,
	.unprepare = cronos_panel_unprepare,
	.enable = cronos_panel_enable,
	.disable = cronos_panel_disable,
	.get_modes = cronos_panel_get_modes,
};

static int cronos_panel_probe(struct mipi_dsi_device *dsi)
{
	struct device *dev = &dsi->dev;
	struct cronos_panel *ctx;
	int ret;

	ctx = devm_drm_panel_alloc(dev, struct cronos_panel, panel, &cronos_panel_funcs,
				   DRM_MODE_CONNECTOR_DSI);
	if (IS_ERR(ctx))
		return PTR_ERR(ctx);
	ctx->panel.prepare_prev_first = true;
	ctx->dsi = dsi;
	mipi_dsi_set_drvdata(dsi, ctx);

	ret = devm_regulator_bulk_get_const(dev, ARRAY_SIZE(cronos_panel_supplies),
					    cronos_panel_supplies, &ctx->supplies);
	if (ret < 0)
		return dev_err_probe(dev, ret, "Failed to get regulators\n");
	/* Kept on: the bootloader powered the panel and it stays as it left it. */
	ret = regulator_bulk_enable(ARRAY_SIZE(cronos_panel_supplies), ctx->supplies);
	if (ret < 0)
		return dev_err_probe(dev, ret, "Failed to enable regulators\n");

	/* As is: never driven, so the bootloader's panel state survives. */
	ctx->reset_gpio = devm_gpiod_get_optional(dev, "reset", GPIOD_ASIS);
	if (IS_ERR(ctx->reset_gpio))
		return dev_err_probe(dev, PTR_ERR(ctx->reset_gpio), "Failed to get reset GPIO\n");

	ret = drm_panel_of_backlight(&ctx->panel);
	if (ret)
		return dev_err_probe(dev, ret, "Failed to get backlight\n");

	dsi->lanes = 2;
	dsi->format = MIPI_DSI_FMT_RGB888;
	dsi->mode_flags = MIPI_DSI_MODE_VIDEO | MIPI_DSI_MODE_VIDEO_BURST | MIPI_DSI_MODE_LPM;

	drm_panel_add(&ctx->panel);
	ret = devm_mipi_dsi_attach(dev, dsi);
	if (ret < 0) {
		drm_panel_remove(&ctx->panel);
		return dev_err_probe(dev, ret, "Failed to attach to DSI host\n");
	}
	return 0;
}

static void cronos_panel_remove(struct mipi_dsi_device *dsi)
{
	struct cronos_panel *ctx = mipi_dsi_get_drvdata(dsi);

	drm_panel_remove(&ctx->panel);
}

static const struct of_device_id cronos_panel_of_match[] = {
	{ .compatible = "amazon,cronos-st7701s" },
	{ /* sentinel */ }
};
MODULE_DEVICE_TABLE(of, cronos_panel_of_match);

static struct mipi_dsi_driver cronos_panel_driver = {
	.probe = cronos_panel_probe,
	.remove = cronos_panel_remove,
	.driver = {
		.name = "panel-amazon-cronos-st7701s",
		.of_match_table = cronos_panel_of_match,
	},
};
module_mipi_dsi_driver(cronos_panel_driver);

MODULE_DESCRIPTION("DRM driver for the Echo Show 5 (2021) ST7701S DSI panel");
MODULE_LICENSE("GPL");
