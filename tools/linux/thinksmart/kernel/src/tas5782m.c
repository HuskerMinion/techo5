// SPDX-License-Identifier: GPL-2.0-only
// SPDX-FileCopyrightText: rickx (https://github.com/rickx/ubuntu-thinksmart)
// SPDX-FileCopyrightText: 2026 Andrea Mattera (modifications, see README.md)
/*
 * snd-soc-tas5782m.c — ASoC CODEC driver for Texas Instruments TAS5782M
 *
 * Target hardware: Lenovo CD-18781Y (ThinkSmart View), APQ8053/MSM8953,
 * TAS5782M at I2C bus 1 address 0x49 (DT: "1-0049").
 *
 * Based on:
 *   - Earlier TI smart-amp ASoC driver patterns by Andy Liu <andy-liu@ti.com>,
 *     Daniel Beer <daniel.beer@igorinstitute.com>,
 *     Felix Kaechele <felix@kaechele.ca>
 *   - TAS5782M reference driver posted by TI on E2E forum
 *   - Lenovo CD-18781Y OSS package (Android kernel DTS)
 *
 * Critical ordering note (root cause of prior -EIO regression)
 * ============================================================
 * The TAS5782M's I2C interface does not respond reliably until BCLK is
 * present on the SCLK pin. Writes issued before BCLK starts are NACK'd
 * (regmap_write returns -EIO). FelixKa's working driver documents this
 * and gates all chip programming behind a 5–10 ms delay scheduled from
 * the codec DAI .trigger(START) callback — by which time the CPU DAI has
 * already called q6afe_port_start() and BCLK is on GPIO 135.
 *
 * An earlier version of this driver used SND_SOC_DAPM_POST_PMU to do the
 * preboot/firmware/volume sequence. In Qualcomm Q6 ASoC, the codec
 * widget's POST_PMU fires during the DAPM bias-level transition that
 * precedes CPU-side trigger(START), so BCLK is not yet running and the
 * first I2C write fails deterministically. That placement was wrong.
 *
 * This driver therefore:
 *   1. Holds the chip in HI-Z after probe (no I2C beyond regulator+PDN).
 *   2. On .trigger(START), schedules priv->work.
 *   3. The work waits 5–10 ms for BCLK to stabilize, sends the preboot
 *      sequence, waits 5–15 ms for the DSP to boot, loads firmware, then
 *      pushes initial volume/unmute.
 *   4. On DAPM PRE_PMD, cancels any pending work and puts the chip into
 *      standby (HIZ, reg 0x02=0x10), sets is_powered=false.
 *   5. mute_stream and volume kcontrol updates call refresh() directly
 *      under priv->lock — they run in process context.
 *
 * Other design points
 * -------------------
 *  - PDN polarity: DT declares GPIO_ACTIVE_LOW (datasheet semantics);
 *    gpiod_set_value(pdn, 1) yields a physical LOW = amplifier active.
 *  - REGCACHE_NONE: Book/Page multiplex makes register caching incorrect.
 *
 * Changes 2026-11-03 (see README.md):
 *  - volume on the real TAS5782M registers (P0 0x3D/0x3E, or the DSP fine
 *    volume 8C/1E/44,48 + swap with volume_mode=1), not on the TAS5805M
 *    location 8C/2A/24,28; TLV -84..0 dB, 0 = mute;
 *  - PRE_PMD only mutes; standby after standby_ms (point 4 above is now
 *    the delayed standby work).
 */

#include <linux/module.h>
#include <linux/moduleparam.h>
#include <linux/i2c.h>
#include <linux/regmap.h>
#include <linux/gpio/consumer.h>
#include <linux/regulator/consumer.h>
#include <linux/firmware.h>
#include <linux/delay.h>
#include <linux/workqueue.h>
#include <linux/mutex.h>
#include <linux/slab.h>
#include <sound/soc.h>
#include <sound/pcm.h>
#include <sound/initval.h>
#include <sound/tlv.h>

#include "tas5782m_priv.h"

/*
 * Module parameters (2026-11-03).
 *
 * standby_ms: on qcom DPCM back-ends the machine driver sets
 * ignore_pmdown_time (sound/soc/qcom/common.c), so DAPM PRE_PMD runs as soon
 * as the stream closes and the old driver put the chip in standby right away;
 * the next stream then paid the whole preboot + firmware load (~1500 I2C
 * writes, ~550 ms of lost audio). Now PRE_PMD only soft-mutes and the standby
 * (and the firmware reload on the next stream) happens after standby_ms.
 *   > 0: standby after that many ms (default 3000)
 *     0: standby immediately (old behaviour)
 *   < 0: never: the chip stays configured and muted; with no clocks it sits
 *        in the clock-error standby of the datasheet by itself
 * Read at every stream end: can be changed at runtime in
 * /sys/module/snd_soc_tas5782m/parameters/standby_ms.
 *
 * volume_mode: where "Master Playback Volume" (Speaker Amp Master) goes.
 *   0: DAC digital volume, Book 0 / Page 0 regs 0x3D/0x3E (default)
 *   1: DSP fine volume, Book 0x8C / Page 0x1E regs 0x44/0x48 + swap flag,
 *      scaled from the firmware's own value (Lenovo: -4.5 dB)
 * Read when the volume is pushed to the chip.
 */
static int standby_ms = 3000;
module_param(standby_ms, int, 0644);
MODULE_PARM_DESC(standby_ms,
		 "ms before standby after a stream (0 = immediately, <0 = never)");

static int volume_mode;
module_param(volume_mode, int, 0644);
MODULE_PARM_DESC(volume_mode,
		 "volume: 0 = digital volume P0 0x3D/0x3E, 1 = DSP fine volume 8C/1E + swap");

/* ========================================================================= */
/* Book / Page multiplexer helper                                             */
/* ========================================================================= */

static int tas5782m_set_book_page(struct tas5782m_priv *priv,
				  u8 book, u8 page)
{
	int ret;

	ret = regmap_write(priv->regmap, TAS5782M_REG_PAGE, 0x00);
	if (ret)
		return ret;

	ret = regmap_write(priv->regmap, TAS5782M_REG_BOOK, book);
	if (ret)
		return ret;

	ret = regmap_write(priv->regmap, TAS5782M_REG_PAGE, page);
	if (ret)
		return ret;

	return 0;
}

/* ========================================================================= */
/* Volume                                                                     */
/* ========================================================================= */

/*
 * Volume. "Master Playback Volume" is 0..85, 1 dB per step, 85 = 0 dB,
 * 0 = mute (TLV below). See tas5782m.h for the registers.
 *
 * Caller must hold priv->lock and the chip must be powered (firmware
 * loaded). Leaves the chip on Book 0 / Page 0.
 */
/*
 * Without the tuning file there is no limiter and none of its headroom (the
 * Lenovo file's fine volume is -4.5 dB), so the top of the scale is 4.5 dB
 * lower: 9 register steps.
 */
#define TAS5782M_FLAT_STEPS 9

static u8 tas5782m_dig_vol(struct tas5782m_priv *priv, int v)
{
	int r;

	if (v <= TAS5782M_VOLUME_MIN)
		return TAS5782M_DIG_VOL_MUTE;
	/* 0.5 dB per register step */
	r = TAS5782M_DIG_VOL_0DB + 2 * (TAS5782M_VOLUME_MAX - v);
	if (!priv->dsp_tuned)
		r += TAS5782M_FLAT_STEPS;
	return r < TAS5782M_DIG_VOL_MUTE ? r : TAS5782M_DIG_VOL_MUTE;
}

static void tas5782m_put_coef(struct regmap *rm, unsigned int reg, u32 x)
{
	u8 v[4];
	int i;

	for (i = 0; i < 4; i++) { v[3 - i] = (u8)(x & 0xFF); x >>= 8; }
	regmap_bulk_write(rm, reg, v, 4);
}

static void tas5782m_set_fine_volume(struct tas5782m_priv *priv)
{
	struct regmap *rm = priv->regmap;
	u8 flag[4];
	u32 c[2];
	int i, tries;

	for (i = 0; i < 2; i++) {
		int v = priv->vol[i];

		c[i] = v <= TAS5782M_VOLUME_MIN ? 0 :
		       (u32)(((u64)priv->fine_vol_base[i] *
			      tas5782m_volume[v]) >> 23);
	}

	/* The host buffer must not be written while the previous swap is
	 * still pending (datasheet 9.3.9.6.2). Without clocks the DSP never
	 * clears it: wait a little, then write anyway. */
	if (tas5782m_set_book_page(priv, TAS5782M_VOL_BOOK, TAS5782M_SWAP_PAGE))
		return;
	for (tries = 0; tries < 5; tries++) {
		if (regmap_bulk_read(rm, TAS5782M_SWAP_REG, flag, 4) ||
		    !(flag[0] | flag[1] | flag[2] | flag[3]))
			break;
		usleep_range(1000, 2000);
	}

	if (tas5782m_set_book_page(priv, TAS5782M_VOL_BOOK, TAS5782M_VOL_PAGE))
		return;
	tas5782m_put_coef(rm, TAS5782M_VOL_CH_LEFT, c[0]);
	tas5782m_put_coef(rm, TAS5782M_VOL_CH_RIGHT, c[1]);

	if (tas5782m_set_book_page(priv, TAS5782M_VOL_BOOK, TAS5782M_SWAP_PAGE))
		return;
	tas5782m_put_coef(rm, TAS5782M_SWAP_REG, 0x00000001);
}

static void tas5782m_set_volume(struct tas5782m_priv *priv)
{

	if (volume_mode == 1) {
		/* DSP fine volume; the DAC digital volume stays at 0 dB */
		tas5782m_set_fine_volume(priv);
		tas5782m_set_book_page(priv, 0x00, 0x00);
		regmap_write(priv->regmap, TAS5782M_DIG_VOL_LEFT,
				      TAS5782M_DIG_VOL_0DB);
		regmap_write(priv->regmap, TAS5782M_DIG_VOL_RIGHT,
				      TAS5782M_DIG_VOL_0DB);
		return;
	}

	/* DAC digital volume; the DSP fine volume stays as the firmware set it */
	tas5782m_set_book_page(priv, 0x00, 0x00);
	regmap_write(priv->regmap, TAS5782M_DIG_VOL_CTRL, 0x00);
	regmap_write(priv->regmap, TAS5782M_DIG_VOL_LEFT,
			      tas5782m_dig_vol(priv, priv->vol[0]));
	regmap_write(priv->regmap, TAS5782M_DIG_VOL_RIGHT,
			      tas5782m_dig_vol(priv, priv->vol[1]));
}

/*
 * Firmware's own fine volume (Book 0x8C / Page 0x1E / 0x44..0x4B), used as
 * 0 dB reference by volume_mode=1 so the Lenovo headroom is kept.
 */
static void tas5782m_parse_fine_volume(struct tas5782m_priv *priv)
{
	const u8 *d = priv->dsp_cfg_data;
	u8 book = 0, page = 0;
	size_t i;

	priv->fine_vol_base[0] = 0x00800000;
	priv->fine_vol_base[1] = 0x00800000;

	for (i = 0; i + 1 < priv->dsp_cfg_len; i += 2) {
		u8 r = d[i], v = d[i + 1];
		int ch, b;

		if (r == TAS5782M_REG_BOOK) { book = v; continue; }
		if (r == TAS5782M_REG_PAGE) { page = v; continue; }
		if (book != TAS5782M_VOL_BOOK || page != TAS5782M_VOL_PAGE ||
		    r < TAS5782M_VOL_CH_LEFT || r > TAS5782M_VOL_CH_RIGHT + 3)
			continue;
		ch = r >= TAS5782M_VOL_CH_RIGHT;
		b = 3 - (r - (ch ? TAS5782M_VOL_CH_RIGHT : TAS5782M_VOL_CH_LEFT));
		priv->fine_vol_base[ch] &= ~(0xFFu << (8 * b));
		priv->fine_vol_base[ch] |= (u32)v << (8 * b);
	}

	dev_info(&priv->client->dev, "firmware fine volume L=0x%08x R=0x%08x\n",
		 priv->fine_vol_base[0], priv->fine_vol_base[1]);
}

/* ========================================================================= */
/* Refresh — apply current vol/mute state to the chip                        */
/* ========================================================================= */

/*
 * Caller must hold priv->lock and priv->is_powered must be true.
 * Volume is pushed first so an unmute does not pop to a stale level.
 */
static void tas5782m_refresh(struct tas5782m_priv *priv)
{
	tas5782m_set_volume(priv);
	regmap_write(priv->regmap, TAS5782M_CTRL3,
			      priv->is_muted ? TAS5782M_MUTE
					     : TAS5782M_NORMAL_VOLUME);
}

/* ========================================================================= */
/* Firmware handling                                                          */
/* ========================================================================= */

/*
 * The tuning file (the speaker's EQ, limiter and headroom for the chip's DSP),
 * looked for at each stream start until it is found: the driver is built in
 * and probes before the root filesystem, and its firmware, are there. Until
 * then, or on a unit that has none, the chip plays with its own flat
 * coefficients (tas5782m_dsp_flat), 4.5 dB lower.
 */
static void tas5782m_load_tuning(struct tas5782m_priv *priv)
{
	const struct firmware *fw;
	char fw_name[128];
	u8 *data;

	if (priv->dsp_tuned)
		return;
	snprintf(fw_name, sizeof(fw_name),
		 "tas5728m_dsp_%s.bin", priv->dsp_config_name);
	if (firmware_request_nowarn(&fw, fw_name, &priv->client->dev)) {
		dev_warn_once(&priv->client->dev,
			      "no '%s': playing without the speaker's tuning, 4.5 dB lower\n",
			      fw_name);
		return;
	}
	if (fw->size < 2 || (fw->size & 1)) {
		dev_err(&priv->client->dev, "'%s' is invalid; playing without it\n", fw_name);
		release_firmware(fw);
		return;
	}
	data = devm_kmemdup(&priv->client->dev, fw->data, fw->size, GFP_KERNEL);
	if (data) {
		priv->dsp_cfg_data = data;
		priv->dsp_cfg_len = fw->size;
		priv->dsp_tuned = true;
		dev_info(&priv->client->dev, "speaker tuning '%s' (%zu bytes)\n",
			 fw_name, fw->size);
		tas5782m_parse_fine_volume(priv);
	}
	release_firmware(fw);
}

/* ========================================================================= */
/* Stream-start work — preboot + firmware + initial refresh                  */
/* ========================================================================= */

static int tas5782m_send_cfg(struct tas5782m_priv *priv,
			     const u8 *data, size_t len)
{
	size_t i;
	int ret;
	int first_err = 0;

	for (i = 0; i + 1 < len; i += 2) {
		ret = regmap_write(priv->regmap, data[i], data[i + 1]);
		if (ret)
			if (!first_err)
				first_err = ret;
	}

	if (first_err) {
	}

	return 0;
}

/*
 * tas5782m_do_work() — runs all chip programming that requires a live BCLK.
 *
 * Scheduled from .trigger(START) on the codec DAI, which fires after the
 * CPU DAI's trigger has called q6afe_port_start() and brought BCLK up on
 * the I2S pins. We still wait an extra 5–10 ms because BCLK takes a few
 * frames to settle and the chip latches a clock-error flag if the first
 * I2C write straddles that boundary.
 */
static void tas5782m_do_work(struct work_struct *work)
{
	struct tas5782m_priv *priv =
		container_of(work, struct tas5782m_priv, work);
	int ret;

	mutex_lock(&priv->lock);

	/* If chip is already in PLAY mode (e.g. DAPM pmdown_time kept it live),
	 * skip the full 545ms preboot+firmware sequence and just refresh state. */
	if (priv->is_powered) {
		priv->is_muted = false;
		/* Still configured (standby_ms window, or clock-error standby
		 * with RQST=0): the chip resumes by itself once the clocks are
		 * back; PLAY is written again just in case. */
		tas5782m_set_book_page(priv, 0x00, 0x00);
		regmap_write(priv->regmap, TAS5782M_CTRL2, TAS5782M_MODE_PLAY);
		tas5782m_refresh(priv);
		mutex_unlock(&priv->lock);
		return;
	}

	usleep_range(5000, 10000);

	ret = tas5782m_send_cfg(priv, tas5782m_dsp_init, tas5782m_dsp_init_len);
	if (ret)
		dev_err(&priv->client->dev,
			"stream-start: preboot send_cfg reported: %d\n", ret);

	/* DSP needs ~5 ms to boot internally before accepting firmware blob */
	usleep_range(5000, 15000);

	tas5782m_load_tuning(priv);
	tas5782m_send_cfg(priv, priv->dsp_cfg_data, priv->dsp_cfg_len);

	/* The firmware's first write (reg 0x02=0x80) triggers an internal
	 * register reload that restores page-0 defaults, including reg 0x59=0x11
	 * (auto-mute enabled for both channels). Disable it here, after firmware
	 * completes, so silence zeros from WP keep-alive don't trigger auto-mute
	 * before real audio arrives. Firmware ends in book 0/page 0 so no
	 * book/page switch is needed. */
	regmap_write(priv->regmap, TAS5782M_AUTOMUTE_CTRL, 0x00);

	/* Always unmute on stream start — mute_stream handles re-muting.
	 * The preboot sequence set CTRL3=MUTE; this ungate runs after firmware
	 * init. In the PipeWire/DPCM path, mute_stream(0) may not be called. */
	priv->is_muted = false;
	priv->is_powered = true;
	tas5782m_refresh(priv);

	mutex_unlock(&priv->lock);
}

/* ========================================================================= */
/* DAI callbacks                                                              */
/* ========================================================================= */

static int tas5782m_trigger(struct snd_pcm_substream *substream, int cmd,
			    struct snd_soc_dai *dai)
{
	struct snd_soc_component *comp = dai->component;
	struct tas5782m_priv *priv = snd_soc_component_get_drvdata(comp);

	switch (cmd) {
	case SNDRV_PCM_TRIGGER_START:
	case SNDRV_PCM_TRIGGER_RESUME:
	case SNDRV_PCM_TRIGGER_PAUSE_RELEASE:
		/* May run in atomic context: no _sync here. If the standby
		 * work is already running it sees stream_active and backs off;
		 * if it already went to standby, do_work reloads everything. */
		WRITE_ONCE(priv->stream_active, true);
		cancel_delayed_work(&priv->standby_work);
		schedule_work(&priv->work);
		break;

	case SNDRV_PCM_TRIGGER_STOP:
	case SNDRV_PCM_TRIGGER_SUSPEND:
	case SNDRV_PCM_TRIGGER_PAUSE_PUSH:
		/* HI-Z teardown is done in DAPM PRE_PMD */
		break;

	default:
		return -EINVAL;
	}
	return 0;
}

static int tas5782m_mute_stream(struct snd_soc_dai *dai, int mute, int stream)
{
	struct snd_soc_component *comp = dai->component;
	struct tas5782m_priv *priv = snd_soc_component_get_drvdata(comp);

	if (stream != SNDRV_PCM_STREAM_PLAYBACK)
		return 0;

	mutex_lock(&priv->lock);
	priv->is_muted = mute;
	if (priv->is_powered)
		tas5782m_refresh(priv);
	mutex_unlock(&priv->lock);

	return 0;
}

/* ========================================================================= */
/* DAPM event — PRE_PMD only, for HI-Z teardown                              */
/* ========================================================================= */

/* Caller must hold priv->lock. */
static void tas5782m_enter_standby(struct tas5782m_priv *priv)
{
	int ret;

	priv->is_powered = false;
	regmap_write(priv->regmap, TAS5782M_REG_PAGE, 0x00);
	regmap_write(priv->regmap, TAS5782M_REG_BOOK, 0x00);
	ret = regmap_write(priv->regmap, TAS5782M_CTRL2, TAS5782M_MODE_HIZ);
	if (ret)
		/* Not fatal: is_powered=false makes the next stream redo
		 * reset + firmware anyway. */
		dev_warn(&priv->client->dev, "standby write failed: %d\n", ret);
}

static void tas5782m_standby_work(struct work_struct *work)
{
	struct tas5782m_priv *priv =
		container_of(to_delayed_work(work), struct tas5782m_priv,
			     standby_work);

	mutex_lock(&priv->lock);
	if (priv->is_powered && !READ_ONCE(priv->stream_active))
		tas5782m_enter_standby(priv);
	mutex_unlock(&priv->lock);
}

static int tas5782m_dac_event(struct snd_soc_dapm_widget *w,
			      struct snd_kcontrol *k, int event)
{
	struct snd_soc_component *comp = snd_soc_dapm_to_component(w->dapm);
	struct tas5782m_priv *priv = snd_soc_component_get_drvdata(comp);
	int delay = READ_ONCE(standby_ms);

	if (event & SND_SOC_DAPM_PRE_PMD) {
		WRITE_ONCE(priv->stream_active, false);
		cancel_work_sync(&priv->work);

		mutex_lock(&priv->lock);
		if (!priv->is_powered) {
		} else if (delay == 0) {
			tas5782m_enter_standby(priv);
		} else {
			/* Soft mute now, while BCLK is still running (the
			 * chip ramps down); keep DSP and firmware loaded. */
			priv->is_muted = true;
			tas5782m_set_book_page(priv, 0x00, 0x00);
			regmap_write(priv->regmap, TAS5782M_CTRL3,
					      TAS5782M_MUTE);
			if (delay > 0)
				mod_delayed_work(system_wq, &priv->standby_work,
						 msecs_to_jiffies(delay));
		}
		mutex_unlock(&priv->lock);
	}

	return 0;
}

/* ========================================================================= */
/* DAPM topology                                                              */
/* ========================================================================= */

static const struct snd_soc_dapm_widget tas5782m_dapm_widgets[] = {
	SND_SOC_DAPM_AIF_IN("DAC IN", "Playback", 0, SND_SOC_NOPM, 0, 0),
	SND_SOC_DAPM_DAC_E("DAC", NULL, SND_SOC_NOPM, 0, 0,
			   tas5782m_dac_event, SND_SOC_DAPM_PRE_PMD),
	SND_SOC_DAPM_OUTPUT("OUT"),
};

static const struct snd_soc_dapm_route tas5782m_dapm_routes[] = {
	{ "DAC", NULL, "DAC IN" },
	{ "OUT", NULL, "DAC"    },
};

/* ========================================================================= */
/* Volume kcontrol                                                            */
/* ========================================================================= */

static int tas5782m_vol_info(struct snd_kcontrol *kcontrol,
			     struct snd_ctl_elem_info *uinfo)
{
	uinfo->type = SNDRV_CTL_ELEM_TYPE_INTEGER;
	uinfo->count = 2;
	uinfo->value.integer.min = TAS5782M_VOLUME_MIN;
	uinfo->value.integer.max = TAS5782M_VOLUME_MAX;
	return 0;
}

static int tas5782m_vol_get(struct snd_kcontrol *kcontrol,
			    struct snd_ctl_elem_value *ucontrol)
{
	struct snd_soc_component *comp = snd_kcontrol_chip(kcontrol);
	struct tas5782m_priv *priv = snd_soc_component_get_drvdata(comp);

	mutex_lock(&priv->lock);
	ucontrol->value.integer.value[0] = priv->vol[0];
	ucontrol->value.integer.value[1] = priv->vol[1];
	mutex_unlock(&priv->lock);
	return 0;
}

static int tas5782m_vol_put(struct snd_kcontrol *kcontrol,
			    struct snd_ctl_elem_value *ucontrol)
{
	struct snd_soc_component *comp = snd_kcontrol_chip(kcontrol);
	struct tas5782m_priv *priv = snd_soc_component_get_drvdata(comp);
	int v0 = ucontrol->value.integer.value[0];
	int v1 = ucontrol->value.integer.value[1];
	int changed = 0;

	if (v0 < TAS5782M_VOLUME_MIN || v0 > TAS5782M_VOLUME_MAX ||
	    v1 < TAS5782M_VOLUME_MIN || v1 > TAS5782M_VOLUME_MAX)
		return -EINVAL;

	mutex_lock(&priv->lock);
	if (priv->vol[0] != v0 || priv->vol[1] != v1) {
		priv->vol[0] = v0;
		priv->vol[1] = v1;
		changed = 1;
		if (priv->is_powered)
			tas5782m_refresh(priv);

	}
	mutex_unlock(&priv->lock);
	return changed;
}

/* 0 = mute, 1..85 = -84..0 dB */
static const DECLARE_TLV_DB_SCALE(tas5782m_vol_tlv, -8500, 100, 1);

static const struct snd_kcontrol_new tas5782m_controls[] = {
	{
		.iface  = SNDRV_CTL_ELEM_IFACE_MIXER,
		.name   = "Master Playback Volume",
		.access = SNDRV_CTL_ELEM_ACCESS_TLV_READ |
			  SNDRV_CTL_ELEM_ACCESS_READWRITE,
		.info   = tas5782m_vol_info,
		.get    = tas5782m_vol_get,
		.put    = tas5782m_vol_put,
		.tlv.p  = tas5782m_vol_tlv,
	},
};

static const struct snd_soc_dai_ops tas5782m_dai_ops = {
	.trigger         = tas5782m_trigger,
	.mute_stream     = tas5782m_mute_stream,
	.no_capture_mute = 1,
};

struct snd_soc_dai_driver tas5782m_dai = {
	/* Name kept as "tas5805m-amplifier" for ABI compatibility with the
	 * existing machine-driver/DT setup that was wired for the legacy
	 * tas5805m driver fork. The chip is a TAS5782M; only the DAI string
	 * is preserved. */
	.name           = "tas5805m-amplifier",
	.playback = {
		.stream_name  = "Playback",
		.channels_min = 2,
		.channels_max = 2,
		.rates        = SNDRV_PCM_RATE_44100 | SNDRV_PCM_RATE_48000,
		.formats      = SNDRV_PCM_FMTBIT_S32_LE,
	},
	.ops = &tas5782m_dai_ops,
};

const struct snd_soc_component_driver tas5782m_component = {
	.controls         = tas5782m_controls,
	.num_controls     = ARRAY_SIZE(tas5782m_controls),
	.dapm_widgets     = tas5782m_dapm_widgets,
	.num_dapm_widgets = ARRAY_SIZE(tas5782m_dapm_widgets),
	.dapm_routes      = tas5782m_dapm_routes,
	.num_dapm_routes  = ARRAY_SIZE(tas5782m_dapm_routes),
	.use_pmdown_time  = 1,
	.endianness       = 1,
};

/* ========================================================================= */
/* I2C probe / remove                                                         */
/* ========================================================================= */

static int tas5782m_i2c_probe(struct i2c_client *i2c)
{
	struct device *dev = &i2c->dev;
	struct tas5782m_priv *priv;
	const char *config_name;
	int ret;

	priv = devm_kzalloc(dev, sizeof(*priv), GFP_KERNEL);
	if (!priv)
		return -ENOMEM;

	priv->client = i2c;
	mutex_init(&priv->lock);
	INIT_WORK(&priv->work, tas5782m_do_work);
	INIT_DELAYED_WORK(&priv->standby_work, tas5782m_standby_work);
	priv->vol[0] = TAS5782M_VOLUME_MIN;
	priv->vol[1] = TAS5782M_VOLUME_MIN;
	priv->is_muted = true;
	priv->is_powered = false;

	ret = of_property_read_string(dev->of_node,
				      "ti,dsp-config-name", &config_name);
	if (ret) {
		dev_info(dev, "no ti,dsp-config-name; using 'default'\n");
		config_name = "default";
	}
	strscpy(priv->dsp_config_name, config_name,
		sizeof(priv->dsp_config_name));

	priv->pvdd = devm_regulator_get(dev, "pvdd");
	if (IS_ERR(priv->pvdd))
		return dev_err_probe(dev, PTR_ERR(priv->pvdd),
				     "failed to get pvdd supply\n");

	priv->gpio_pdn = devm_gpiod_get_optional(dev, "pdn", GPIOD_OUT_LOW);
	if (IS_ERR(priv->gpio_pdn))
		return dev_err_probe(dev, PTR_ERR(priv->gpio_pdn),
				     "failed to get pdn gpio\n");

	priv->regmap = devm_regmap_init_i2c(i2c, &tas5782m_regmap_config);
	if (IS_ERR(priv->regmap))
		return dev_err_probe(dev, PTR_ERR(priv->regmap),
				     "failed to create regmap\n");

	i2c_set_clientdata(i2c, priv);

	priv->dsp_cfg_data = tas5782m_dsp_flat;
	priv->dsp_cfg_len = tas5782m_dsp_flat_len;
	tas5782m_parse_fine_volume(priv);

	ret = regulator_enable(priv->pvdd);
	if (ret)
		return dev_err_probe(dev, ret, "failed to enable pvdd\n");

	/*
	 * FelixKa note: hold the chip in PDN for ~100 ms after PVDD comes up
	 * so the ADR pin samples cleanly. Otherwise the I2C address can come
	 * up unpredictable.
	 */
	usleep_range(100000, 150000);

	if (priv->gpio_pdn)
		gpiod_set_value_cansleep(priv->gpio_pdn, 1);

	/* Allow internal regulator/PLL domains to settle after PDN release. */
	usleep_range(10000, 15000);

	ret = devm_snd_soc_register_component(dev, &tas5782m_component,
					      &tas5782m_dai, 1);
	if (ret) {
		dev_err(dev, "failed to register ASoC component: %d\n", ret);
		goto err_disable_pvdd;
	}

	dev_info(dev, "TAS5782M ready (i2c=0x%02x config='%s')\n",
		 i2c->addr, priv->dsp_config_name);
	return 0;

err_disable_pvdd:
	if (priv->gpio_pdn)
		gpiod_set_value_cansleep(priv->gpio_pdn, 0);
	regulator_disable(priv->pvdd);
	return ret;
}

static void tas5782m_i2c_remove(struct i2c_client *i2c)
{
	struct tas5782m_priv *priv = i2c_get_clientdata(i2c);

	cancel_work_sync(&priv->work);
	cancel_delayed_work_sync(&priv->standby_work);

	if (priv->gpio_pdn)
		gpiod_set_value_cansleep(priv->gpio_pdn, 0);

	regulator_disable(priv->pvdd);
	dev_info(&i2c->dev, "TAS5782M removed\n");
}

/* ========================================================================= */
/* Module scaffolding                                                         */
/* ========================================================================= */

static const struct of_device_id tas5782m_of_ids[] = {
	{ .compatible = "ti,tas5782m" },
	{ }
};
MODULE_DEVICE_TABLE(of, tas5782m_of_ids);

static const struct i2c_device_id tas5782m_id[] = {
	{ "tas5782m", 0 },
	{ }
};
MODULE_DEVICE_TABLE(i2c, tas5782m_id);

static struct i2c_driver tas5782m_driver = {
	.driver = {
		.name           = "tas5782m",
		.of_match_table = tas5782m_of_ids,
	},
	.probe    = tas5782m_i2c_probe,
	.remove   = tas5782m_i2c_remove,
	.id_table = tas5782m_id,
};

module_i2c_driver(tas5782m_driver);

MODULE_AUTHOR("Based on FelixKa / Andy Liu / Daniel Beer");
MODULE_DESCRIPTION("TAS5782M Smart Amplifier ASoC CODEC Driver");
MODULE_LICENSE("GPL v2");
