// SPDX-License-Identifier: GPL-2.0
//
// mt8183-athena.c -- MT8183 + TAS5805M + TLV320ADC5140 ALSA SoC machine driver
// for the Amazon Echo Show 8 2nd gen ("athena").
//
// Based on mt8183-mt6358-ts3a227-max98357.c (Copyright (c) 2018 MediaTek Inc.).
//
// Wiring (from the stock athena kernel):
//   I2S1 out -> TAS5805M (i2c4 0x2d) speaker amp, stock 96 kHz, 16-bit words
//   I2S2 in  <- TLV320ADC5140 (i2c4 0x4c) mics: ch1/ch2 on DI, ch3/ch4 on DI2 (patch-afe-di2.py),
//              captured 4-channel through UL1 (VUL12), hw:0,2
//   DL1 -> UL2 (AWB) inside the AFE: the echo reference, hw:0,1 "DL1_AWB_Record" (see below)
// The AFE is clock master on both links; the codecs are I2S consumers.

#include <linux/module.h>
#include <linux/of.h>
#include <linux/pinctrl/consumer.h>
#include <linux/slab.h>
#include <sound/control.h>
#include <sound/pcm_params.h>
#include <sound/soc.h>

#include "../common/mtk-afe-platform-driver.h"
#include "mt8183-afe-common.h"

static int athena_i2s_hw_params(struct snd_pcm_substream *substream,
				struct snd_pcm_hw_params *params)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	unsigned int mclk_fs = params_rate(params) * 128;

	return snd_soc_dai_set_sysclk(snd_soc_rtd_to_cpu(rtd, 0),
				      0, mclk_fs, SND_SOC_CLOCK_OUT);
}

static const struct snd_soc_ops athena_i2s_ops = {
	.hw_params = athena_i2s_hw_params,
};

/* 32-bit slots on both BEs (TAS reads the top 16 bits; the ADC writes 32-bit words) */
static int athena_i2s_fixup(struct snd_soc_pcm_runtime *rtd,
			    struct snd_pcm_hw_params *params)
{
	snd_mask_reset_range(hw_param_mask(params, SNDRV_PCM_HW_PARAM_FORMAT),
			     0, (__force unsigned int)SNDRV_PCM_FORMAT_LAST);
	params_set_format(params, SNDRV_PCM_FORMAT_S32_LE);
	return 0;
}

/*
 * The mic ADC sends ch1/ch2 as I2S stereo with 32-bit slots on SDOUT (I2S2_DI, GPIO174), so ch2
 * belongs in slot 32 (right half); mainline tlv320adcx140 leaves ASI_CH2 at slot 1, which reads as
 * silence. ch3/ch4 go the same way on the secondary output, the ADC's GPIO1 as SDOUT2, into
 * I2S2_DI2 (GPIO12). These are the values stock Fire OS leaves in the ADC (live register dump):
 * ASI_CH3 0x40, ASI_CH4 0x60, GPIO_CFG0 0x32.
 */
#define ATHENA_ADC_ASI_CH2	0x0c
#define ATHENA_ADC_ASI_CH3	0x0d
#define ATHENA_ADC_ASI_CH4	0x0e
#define ATHENA_ADC_GPIO_CFG0	0x21
#define ATHENA_ADC_SLOT_MASK	0x3f
#define ATHENA_ADC_SECONDARY	BIT(6)
#define ATHENA_ADC_GPIO1_SDOUT2	0x32	/* GPIO1 = SDOUT2, driven high and low */

static int athena_i2s2_init(struct snd_soc_pcm_runtime *rtd)
{
	struct snd_soc_component *adc = snd_soc_rtd_to_codec(rtd, 0)->component;
	int ret;

	ret = snd_soc_component_update_bits(adc, ATHENA_ADC_ASI_CH2,
					    ATHENA_ADC_SLOT_MASK, 32);
	if (ret < 0)
		return ret;
	ret = snd_soc_component_write(adc, ATHENA_ADC_ASI_CH3, ATHENA_ADC_SECONDARY | 0);
	if (ret < 0)
		return ret;
	ret = snd_soc_component_write(adc, ATHENA_ADC_ASI_CH4, ATHENA_ADC_SECONDARY | 32);
	if (ret < 0)
		return ret;
	return snd_soc_component_write(adc, ATHENA_ADC_GPIO_CFG0, ATHENA_ADC_GPIO1_SDOUT2);
}

/*
 * Echo reference (theory from the stock tree: the amp's SDOUT goes to I2S0_DI, GPIO7). It is
 * clocked by the playback bus, so I2S0 shares I2S1's clocks.
 */
static int athena_i2s0_init(struct snd_soc_pcm_runtime *rtd)
{
	struct snd_soc_component *cmpnt_afe = snd_soc_rtdcom_lookup(rtd, AFE_PCM_NAME);
	struct mtk_base_afe *afe = snd_soc_component_get_drvdata(cmpnt_afe);

	return mt8183_dai_i2s_set_share(afe, "I2S1", "I2S0");
}

/* FE */
SND_SOC_DAILINK_DEFS(playback1,
	DAILINK_COMP_ARRAY(COMP_CPU("DL1")),
	DAILINK_COMP_ARRAY(COMP_DUMMY()),
	DAILINK_COMP_ARRAY(COMP_EMPTY()));

SND_SOC_DAILINK_DEFS(awb_record,
	DAILINK_COMP_ARRAY(COMP_CPU("UL2")),
	DAILINK_COMP_ARRAY(COMP_DUMMY()),
	DAILINK_COMP_ARRAY(COMP_EMPTY()));

SND_SOC_DAILINK_DEFS(capture1,
	DAILINK_COMP_ARRAY(COMP_CPU("UL1")),
	DAILINK_COMP_ARRAY(COMP_DUMMY()),
	DAILINK_COMP_ARRAY(COMP_EMPTY()));

SND_SOC_DAILINK_DEFS(i2s0,
	DAILINK_COMP_ARRAY(COMP_CPU("I2S0")),
	DAILINK_COMP_ARRAY(COMP_DUMMY()),
	DAILINK_COMP_ARRAY(COMP_EMPTY()));

/* BE: codec of_node is filled in from the DT at probe */
SND_SOC_DAILINK_DEFS(i2s1,
	DAILINK_COMP_ARRAY(COMP_CPU("I2S1")),
	DAILINK_COMP_ARRAY(COMP_CODEC(NULL, "tas5805m-amplifier")),
	DAILINK_COMP_ARRAY(COMP_EMPTY()));

SND_SOC_DAILINK_DEFS(i2s2,
	DAILINK_COMP_ARRAY(COMP_CPU("I2S2")),
	DAILINK_COMP_ARRAY(COMP_CODEC(NULL, "tlv320adcx140-codec")),
	DAILINK_COMP_ARRAY(COMP_EMPTY()));

static struct snd_soc_dai_link athena_dai_links[] = {
	/* FE */
	{
		.name = "Playback_1",
		.stream_name = "Playback_1",
		.trigger = {SND_SOC_DPCM_TRIGGER_PRE, SND_SOC_DPCM_TRIGGER_PRE},
		.dynamic = 1,
		.playback_only = 1,
		SND_SOC_DAILINK_REG(playback1),
	},
	/*
	 * The echo reference, hw:0,1, as Amazon's kernel's "DL1_AWB_Record": the AWB memif (UL2)
	 * records what DL1 sends I2S1, sample for sample, on the AFE's own clocks, so it is locked to
	 * the microphones (UL1) and to the speaker. Nothing outside the AFE takes part, so this is a plain
	 * link rather than a DPCM front end, which would need a back end in its path and refuse to open
	 * ("no backend DAIs enabled"). The path is the AFE's DL1 -> UL2 (patch-afe-awb-dl1.py),
	 * through the interconnect switches "UL2_CH1 DL1_CH1" and "UL2_CH2 DL1_CH2", set from
	 * userspace like the others; it powers up only while DL1 plays, so with nothing playing the
	 * stream runs and reads zero.
	 * It is the second link so the numbering stays: Playback_1 hw:0,0, Capture_1 (mics) hw:0,2.
	 */
	{
		.name = "DL1_AWB_Record",
		.stream_name = "DL1_AWB_Record",
		.capture_only = 1,
		SND_SOC_DAILINK_REG(awb_record),
	},
	{
		.name = "Capture_1",
		.stream_name = "Capture_1",
		.trigger = {SND_SOC_DPCM_TRIGGER_PRE, SND_SOC_DPCM_TRIGGER_PRE},
		.dynamic = 1,
		.capture_only = 1,
		SND_SOC_DAILINK_REG(capture1),
	},
	/* BE */
	{
		.name = "I2S1",
		.no_pcm = 1,
		.playback_only = 1,
		.ignore_suspend = 1,
		.dai_fmt = SND_SOC_DAIFMT_I2S | SND_SOC_DAIFMT_NB_NF |
			   SND_SOC_DAIFMT_CBC_CFC,
		.be_hw_params_fixup = athena_i2s_fixup,
		.ops = &athena_i2s_ops,
		SND_SOC_DAILINK_REG(i2s1),
	},
	{
		.name = "I2S2",
		.no_pcm = 1,
		.capture_only = 1,
		.ignore_suspend = 1,
		.dai_fmt = SND_SOC_DAIFMT_I2S | SND_SOC_DAIFMT_NB_NF |
			   SND_SOC_DAIFMT_CBC_CFC,
		.be_hw_params_fixup = athena_i2s_fixup,
		.ops = &athena_i2s_ops,
		.init = athena_i2s2_init,
		SND_SOC_DAILINK_REG(i2s2),
	},
	{
		.name = "I2S0",
		.no_pcm = 1,
		.capture_only = 1,
		.ignore_suspend = 1,
		.dai_fmt = SND_SOC_DAIFMT_I2S | SND_SOC_DAIFMT_NB_NF |
			   SND_SOC_DAIFMT_CBC_CFC,
		.be_hw_params_fixup = athena_i2s_fixup,
		.ops = &athena_i2s_ops,
		.init = athena_i2s0_init,
		SND_SOC_DAILINK_REG(i2s0),
	},
};

/*
 * DL1 -> I2S1, I2S2 -> UL1 and DL1 -> UL2 go through the AFE's interconnect mixers, switched from
 * userspace (amixer: "I2S1_CH1 DL1_CH1", "UL1_CH1 I2S2_CH1", "UL2_CH1 DL1_CH1", ...). The DL1 -> UL2
 * routes are the AFE's own (patches/patch-afe-awb-dl1.py): added here, they would come after the
 * AFE has made its mixers' controls, and the switches would never appear.
 */

/*
 * I2S1 and I2S2 must run in the AFE's low-jitter (APLL) mode at 96 kHz. In "Normal" mode the
 * TAS5805M sees no valid frame rate (FS_MON 0, CLK_FAULT) and mutes, and the ADC sees no valid
 * clocks (ASI_STS 0xff) and powers no channel, so the mics read digital zero. Stock Fire OS runs both
 * links in HD mode (AFE_I2S_CON2 bit 12 set); Amazon's amp tuning is for 96 kHz.
 */
static int athena_set_hd(struct snd_soc_card *card, const char *name)
{
	struct snd_kcontrol *kctl = snd_soc_card_get_kcontrol(card, name);
	struct snd_ctl_elem_value *val;
	int ret;

	if (!kctl)
		return dev_err_probe(card->dev, -ENOENT, "no %s control\n", name);
	val = kzalloc(sizeof(*val), GFP_KERNEL);
	if (!val)
		return -ENOMEM;
	val->value.integer.value[0] = 1;	/* "Low_Jitter" (also enumerated.item[0]) */
	ret = kctl->put(kctl, val);
	kfree(val);
	return ret < 0 ? ret : 0;
}

static int athena_late_probe(struct snd_soc_card *card)
{
	int ret = athena_set_hd(card, "I2S1_HD_Mux");

	return ret ? ret : athena_set_hd(card, "I2S2_HD_Mux");
}

static struct snd_soc_card athena_card = {
	.name = "mt8183_athena",
	.owner = THIS_MODULE,
	.dai_link = athena_dai_links,
	.num_links = ARRAY_SIZE(athena_dai_links),
	.late_probe = athena_late_probe,
};

static int athena_dev_probe(struct platform_device *pdev)
{
	struct snd_soc_card *card = &athena_card;
	struct device_node *platform_node, *amp, *adc;
	struct snd_soc_dai_link *dai_link;
	struct pinctrl *pinctrl;
	int i, ret;

	platform_node = of_parse_phandle(pdev->dev.of_node, "mediatek,platform", 0);
	if (!platform_node)
		return dev_err_probe(&pdev->dev, -EINVAL, "mediatek,platform missing\n");
	amp = of_parse_phandle(pdev->dev.of_node, "mediatek,audio-codec", 0);
	adc = of_parse_phandle(pdev->dev.of_node, "mediatek,audio-codec", 1);
	if (!amp || !adc) {
		ret = dev_err_probe(&pdev->dev, -EINVAL, "mediatek,audio-codec needs <&amp>, <&adc>\n");
		goto out;
	}

	card->dev = &pdev->dev;
	for_each_card_prelinks(card, i, dai_link) {
		if (!dai_link->platforms->name)
			dai_link->platforms->of_node = platform_node;
		if (!strcmp(dai_link->name, "I2S1"))
			dai_link->codecs->of_node = amp;
		else if (!strcmp(dai_link->name, "I2S2"))
			dai_link->codecs->of_node = adc;
	}

	pinctrl = devm_pinctrl_get_select_default(&pdev->dev);
	if (IS_ERR(pinctrl)) {
		ret = dev_err_probe(&pdev->dev, PTR_ERR(pinctrl), "audio pins\n");
		goto out;
	}

	ret = devm_snd_soc_register_card(&pdev->dev, card);
out:
	of_node_put(platform_node);
	of_node_put(amp);
	of_node_put(adc);
	return ret;
}

static const struct of_device_id athena_dt_match[] = {
	{ .compatible = "amzn,athena-sound" },
	{}
};
MODULE_DEVICE_TABLE(of, athena_dt_match);

static struct platform_driver athena_driver = {
	.driver = {
		.name = "mt8183_athena",
		.of_match_table = athena_dt_match,
		.pm = &snd_soc_pm_ops,
	},
	.probe = athena_dev_probe,
};
module_platform_driver(athena_driver);

MODULE_DESCRIPTION("MT8183 Echo Show 8 2nd gen (athena) ALSA SoC machine driver");
MODULE_LICENSE("GPL");
