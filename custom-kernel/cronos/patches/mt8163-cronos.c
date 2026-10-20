// SPDX-License-Identifier: GPL-2.0
/*
 * Sound card for the Amazon Echo Show 5 (2021, cronos) on MT8163.
 *
 * What Amazon's 4.9 kernel does for the same board (sound/soc/mediatek/mt_soc_audio_8163_amzn,
 * "I2S0DL1OUTPUT" -> "MAX98396_Playback"): DL1 fetched at 16 bits, sent out both I2S blocks
 * (AFE_I2S_CON1, here I2S1, whose pads are pins 72-74, and AFE_I2S_CON3, here I2S3, pin 21) in
 * I2S format with the SoC as clock master and the low-jitter APLL clock on. The microphones
 * arrive over an FPGA on SPI, but their ADC still needs the AFE's I2S1 clock output.
 */
#include <linux/bitfield.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <sound/control.h>
#include <sound/pcm_params.h>
#include <sound/soc.h>

#include "mt8163-afe-common.h"
#include "mt8163-afe-clk.h"
#include "mt8163-afe-regs.h"
#include "../common/mtk-soc-card.h"
#include "../common/mtk-soundcard-driver.h"

enum {
	/* FE */
	DAI_LINK_DL1_PLAYBACK = 0,
	DAI_LINK_DL2_PLAYBACK,
	DAI_LINK_VUL_CAPTURE,
	DAI_LINK_AWB_CAPTURE,
	/* BE */
	DAI_LINK_I2S1,
	DAI_LINK_I2S3,
	/* the microphones: a plain link, the FPGA on SPI is its own PCM */
	DAI_LINK_SPI_CAPTURE,
	DAI_LINK_NUM
};

/* The ADC takes its 24.576 MHz MCLK from the AFE's I2S1 MCLK pin, whether or not I2S1 is playing. */
#define CRONOS_ADC_MCLK	24576000

static struct snd_soc_dai *cronos_i2s1(struct snd_soc_card *card)
{
	struct snd_soc_pcm_runtime *rtd;

	for_each_card_rtds(card, rtd)
		if (!strcmp(rtd->dai_link->name, "I2S1_BE"))
			return snd_soc_rtd_to_cpu(rtd, 0);
	return NULL;
}

static int cronos_spi_capture_startup(struct snd_pcm_substream *substream)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct snd_soc_dai *i2s1 = cronos_i2s1(rtd->card);
	struct mtk_base_afe *afe;
	unsigned int con;
	int ret;

	if (!i2s1)
		return -ENODEV;
	afe = snd_soc_component_get_drvdata(i2s1->component);
	ret = pm_runtime_resume_and_get(afe->dev);
	if (ret)
		return ret;
	/* DAPM owns the shared APLL and divider, including their shutdown order. */
	ret = snd_soc_dai_set_sysclk(i2s1, 0, CRONOS_ADC_MCLK, SND_SOC_CLOCK_OUT);
	if (ret)
		goto put_afe;
	/* Prime a silent 48 kHz I2S output when no playback owns it. DAPM shares
	 * I2S1_EN between capture and playback; the divider alone does not drive MCLK.
	 */
	ret = regmap_read(afe->regmap, AFE_I2S_CON1, &con);
	if (!ret && !(con & BIT(AFE_I2S_M_I2S_EN_SHIFT)))
		ret = regmap_update_bits(afe->regmap, AFE_I2S_CON1,
			AFE_I2S_M_I2S_OUT_MODE | AFE_I2S_M_I2S_FMT |
			AFE_I2S_M_I2S_WLEN | AFE_I2S_M_INV_LRCK | AFE_I2S_M_I2S_LR_SWAP,
			FIELD_PREP(AFE_I2S_M_I2S_OUT_MODE, mt8163_general_rate_transform(48000)) |
			AFE_I2S_M_I2S_FMT);
	if (!ret)
		return 0;
put_afe:
	pm_runtime_put(afe->dev);
	return ret;
}

static void cronos_spi_capture_shutdown(struct snd_pcm_substream *substream)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct snd_soc_dai *i2s1 = cronos_i2s1(rtd->card);
	struct mtk_base_afe *afe;

	if (!i2s1)
		return;
	afe = snd_soc_component_get_drvdata(i2s1->component);
	pm_runtime_put(afe->dev);
}

/* What Amazon's tlv3101_hw_params sets: the ADC clocks from MCLK directly, I2S master, DSP_B, slots 0x7f. */
static int cronos_spi_capture_hw_params(struct snd_pcm_substream *substream,
					struct snd_pcm_hw_params *params)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct snd_soc_dai *codec_dai = snd_soc_rtd_to_codec(rtd, 0);
	int ret;

	ret = snd_soc_dai_set_pll(codec_dai, 1 /* AIC3101_PLL_BCLK */, 0 /* CLKIN_MCLK */,
			    CRONOS_ADC_MCLK, params_rate(params));
	if (ret)
		return ret;
	ret = snd_soc_dai_set_fmt(codec_dai, SND_SOC_DAIFMT_CBP_CFP | SND_SOC_DAIFMT_DSP_B | SND_SOC_DAIFMT_NB_NF);
	if (ret)
		return ret;
	return snd_soc_dai_set_tdm_slot(codec_dai, 0x00, 0x7f, params_channels(params),
				 snd_pcm_format_width(params_format(params)));
}

/* Playback may start before capture. Configure the shared ADC clock before
 * DAPM enables I2S1, rather than changing its divider on an active output.
 */
static int cronos_i2s1_hw_params(struct snd_pcm_substream *substream,
                               struct snd_pcm_hw_params *params)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);

	return snd_soc_dai_set_sysclk(snd_soc_rtd_to_cpu(rtd, 0), 0,
				     CRONOS_ADC_MCLK, SND_SOC_CLOCK_OUT);
}

static const struct snd_soc_ops cronos_i2s1_ops = {
	.hw_params = cronos_i2s1_hw_params,
};

static const struct snd_soc_ops cronos_spi_capture_ops = {
	.startup = cronos_spi_capture_startup,
	.shutdown = cronos_spi_capture_shutdown,
	.hw_params = cronos_spi_capture_hw_params,
};

static const struct snd_soc_dapm_widget cronos_widgets[] = {
	SND_SOC_DAPM_SPK("Speaker", NULL),
};

static const struct snd_soc_dapm_route cronos_routes[] = {
	{ "Speaker", NULL, "OUT" },  /* the TAS5805M's own output widget name (max98396's is "BE_OUT") */
	{ "SPI Capture", NULL, "I2S1_EN" },
	{ "SPI Capture", NULL, "I2S1_MCLK_EN" },
};

SND_SOC_DAILINK_DEFS(playback1,
		     DAILINK_COMP_ARRAY(COMP_CPU("DL1")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));
SND_SOC_DAILINK_DEFS(playback2,
		     DAILINK_COMP_ARRAY(COMP_CPU("DL2")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));
SND_SOC_DAILINK_DEFS(vul,
		     DAILINK_COMP_ARRAY(COMP_CPU("VUL")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));
SND_SOC_DAILINK_DEFS(awb,
		     DAILINK_COMP_ARRAY(COMP_CPU("AWB")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));
/* the codec comes from the device tree (dai-link "I2S1_BE" -> codec -> sound-dai) */
SND_SOC_DAILINK_DEFS(i2s1,
		     DAILINK_COMP_ARRAY(COMP_CPU("I2S1")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));
SND_SOC_DAILINK_DEFS(i2s3,
		     DAILINK_COMP_ARRAY(COMP_CPU("I2S3")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));
/* the codec (the ADC) comes from the device tree (dai-link "SPI_Capture" -> codec -> sound-dai) */
SND_SOC_DAILINK_DEFS(spi_capture,
		     DAILINK_COMP_ARRAY(COMP_CPU("amazon-spi-capture")),
		     DAILINK_COMP_ARRAY(COMP_DUMMY()),
		     DAILINK_COMP_ARRAY(COMP_EMPTY()));

static struct snd_soc_dai_link cronos_dais[] = {
	[DAI_LINK_DL1_PLAYBACK] = {
		.name = "DL1_FE",
		.stream_name = "MultiMedia1_Playback",
		.id = DAI_LINK_DL1_PLAYBACK,
		.trigger = { SND_SOC_DPCM_TRIGGER_POST, SND_SOC_DPCM_TRIGGER_POST },
		.dynamic = 1,
		.playback_only = 1,
		.dpcm_merged_rate = 1,
		SND_SOC_DAILINK_REG(playback1),
	},
	[DAI_LINK_DL2_PLAYBACK] = {
		.name = "DL2_FE",
		.stream_name = "MultiMedia2_Playback",
		.id = DAI_LINK_DL2_PLAYBACK,
		.trigger = { SND_SOC_DPCM_TRIGGER_POST, SND_SOC_DPCM_TRIGGER_POST },
		.dynamic = 1,
		.playback_only = 1,
		.dpcm_merged_rate = 1,
		SND_SOC_DAILINK_REG(playback2),
	},
	[DAI_LINK_VUL_CAPTURE] = {
		.name = "VUL_FE",
		.stream_name = "MultiMedia1_Capture",
		.id = DAI_LINK_VUL_CAPTURE,
		.trigger = { SND_SOC_DPCM_TRIGGER_POST, SND_SOC_DPCM_TRIGGER_POST },
		.dynamic = 1,
		.capture_only = 1,
		.dpcm_merged_rate = 1,
		SND_SOC_DAILINK_REG(vul),
	},
	[DAI_LINK_AWB_CAPTURE] = {
		.name = "AWB_FE",
		.stream_name = "DL1_AWB_Record",
		.id = DAI_LINK_AWB_CAPTURE,
		.trigger = { SND_SOC_DPCM_TRIGGER_POST, SND_SOC_DPCM_TRIGGER_POST },
		.dynamic = 1,
		.capture_only = 1,
		.dpcm_merged_rate = 1,
		SND_SOC_DAILINK_REG(awb),
	},
	[DAI_LINK_I2S1] = {
		.name = "I2S1_BE",
		.no_pcm = 1,
		.id = DAI_LINK_I2S1,
		.playback_only = 1,
		.dai_fmt = SND_SOC_DAIFMT_I2S | SND_SOC_DAIFMT_NB_NF | SND_SOC_DAIFMT_CBC_CFC,
		.ops = &cronos_i2s1_ops,
		SND_SOC_DAILINK_REG(i2s1),
	},
	[DAI_LINK_I2S3] = {
		.name = "I2S3_BE",
		.no_pcm = 1,
		.id = DAI_LINK_I2S3,
		.playback_only = 1,
		.dai_fmt = SND_SOC_DAIFMT_I2S | SND_SOC_DAIFMT_NB_NF | SND_SOC_DAIFMT_CBC_CFC,
		SND_SOC_DAILINK_REG(i2s3),
	},
	[DAI_LINK_SPI_CAPTURE] = {
		.name = "SPI_Capture",
		.stream_name = "Microphones",
		.id = DAI_LINK_SPI_CAPTURE,
		.capture_only = 1,
		.ignore_pmdown_time = 1,
		.ops = &cronos_spi_capture_ops,
		SND_SOC_DAILINK_REG(spi_capture),
	},
};

/* Set a mixer control the way userspace would; the AFE's interconnect switches default to off. */
static int cronos_set_control(struct snd_soc_card *card, const char *name, int value, bool is_enum)
{
	struct snd_kcontrol *kctl = snd_ctl_find_id_mixer(card->snd_card, name);
	struct snd_ctl_elem_value *uc;
	int ret;

	if (!kctl) {
		dev_warn(card->dev, "no control %s\n", name);
		return -ENOENT;
	}
	uc = kzalloc(sizeof(*uc), GFP_KERNEL);
	if (!uc)
		return -ENOMEM;
	if (is_enum)
		uc->value.enumerated.item[0] = value;
	else
		uc->value.integer.value[0] = value;
	ret = kctl->put(kctl, uc);
	kfree(uc);
	if (ret < 0)
		dev_warn(card->dev, "setting %s failed: %d\n", name, ret);
	return ret;
}

static int cronos_late_probe(struct snd_soc_card *card)
{
	/* DL1 to both I2S outputs, as the 4.9 DL1 driver connects O03/O04 and O00/O01 */
	cronos_set_control(card, "I2S1_CH1 DL1_CH1", 1, false);
	cronos_set_control(card, "I2S1_CH2 DL1_CH2", 1, false);
	cronos_set_control(card, "I2S3_CH1 DL1_CH1", 1, false);
	cronos_set_control(card, "I2S3_CH2 DL1_CH2", 1, false);
	/* low-jitter APLL clock, the 4.9 "Audio_I2S0dl1_hd_Switch" that Amazon's HAL turned on */
	cronos_set_control(card, "I2S1_HD_Mux", 1, true);
	cronos_set_control(card, "I2S3_HD_Mux", 1, true);
	/*
	 * The speaker has no jack: nothing else ever marks "Speaker" as an in-use DAPM sink, so
	 * without this DAPM's power-up graph walk never reaches it, and the AFE's own I2S1/I2S3
	 * enable bits (SUPPLY widgets, upstream of the DL1->I2S mixer switches above) never power
	 * on either -- meaning the amplifier can be sitting in Play mode over I2C the whole time
	 * while genuinely receiving no clock or data at all. Every hardwired, non-jack ASoC speaker
	 * needs this.
	 */
	snd_soc_dapm_enable_pin(card->dapm, "Speaker");
	snd_soc_dapm_sync(card->dapm);
	return 0;
}

static struct snd_soc_card cronos_card = {
	.name = "mt8163-cronos",
	.owner = THIS_MODULE,
	.dai_link = cronos_dais,
	.num_links = ARRAY_SIZE(cronos_dais),
	.dapm_widgets = cronos_widgets,
	.num_dapm_widgets = ARRAY_SIZE(cronos_widgets),
	.dapm_routes = cronos_routes,
	.num_dapm_routes = ARRAY_SIZE(cronos_routes),
	.late_probe = cronos_late_probe,
};

static int cronos_dev_probe(struct mtk_soc_card_data *soc_card_data, bool legacy)
{
	struct snd_soc_card *card = soc_card_data->card_data->card;
	int ret;

	ret = parse_dai_link_info(card);
	if (ret) {
		clean_card_reference(card);
		return ret;
	}
	snd_soc_card_set_drvdata(card, soc_card_data);
	return 0;
}

static const struct mtk_soundcard_pdata cronos_pdata = {
	.card_name = "mt8163-cronos",
	.card_data = &(struct mtk_platform_card_data) {
		.card = &cronos_card,
	},
	.soc_probe = cronos_dev_probe,
};

static const struct of_device_id cronos_dt_match[] = {
	{ .compatible = "amazon,cronos-audio", .data = &cronos_pdata },
	{ /* sentinel */ }
};
MODULE_DEVICE_TABLE(of, cronos_dt_match);

static struct platform_driver cronos_driver = {
	.driver = {
		.name = "mt8163-cronos",
		.of_match_table = cronos_dt_match,
		.pm = &snd_soc_pm_ops,
	},
	.probe = mtk_soundcard_common_probe,
};
module_platform_driver(cronos_driver);

MODULE_DESCRIPTION("Amazon Echo Show 5 (cronos) MT8163 sound card");
MODULE_LICENSE("GPL");
