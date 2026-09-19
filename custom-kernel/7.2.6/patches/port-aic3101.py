#!/usr/bin/env python3
"""Port Amazon's tlv320aic3101.c (android_kernel_amazon_mt8163, 4.9) to the 7.2 ASoC component API.
Usage: port-aic3101.py <src.c> <src.h> <outdir>. Every substitution asserts it matched."""
import re, sys, os
src, hdr, out = sys.argv[1:4]
c = open(src).read()
h = open(hdr).read()

def sub(pattern, repl, count=None, regex=False, text=None):
    global c
    t = c if text is None else text
    if regex:
        t, n = re.subn(pattern, repl, t)
    else:
        n = t.count(pattern); t = t.replace(pattern, repl)
    assert n >= 1, 'no match for %r' % pattern[:70]
    if count is not None:
        assert n == count, '%r: %d matches, wanted %d' % (pattern[:50], n, count)
    if text is None:
        c = t
    return t

# --- the component API in place of snd_soc_codec -----------------------------------------------
sub('struct snd_soc_codec_driver', 'struct snd_soc_component_driver')
sub('snd_soc_codec_get_drvdata', 'snd_soc_component_get_drvdata')
sub('snd_soc_codec_get_dapm', 'snd_soc_component_get_dapm')
sub('struct snd_soc_codec *codec', 'struct snd_soc_component *codec')
c = c.replace('struct snd_soc_codec *', 'struct snd_soc_component *')
sub('dai->codec', 'dai->component')
sub('snd_soc_write(', 'snd_soc_component_write(')
sub('snd_soc_read(', 'snd_soc_component_read(')
sub('snd_soc_update_bits(', 'snd_soc_component_update_bits(')
sub('\tcodec->control_data = aic31xx->adc_control_data[0];\n', '')
# the register cache is ours now: the component has none
sub('u8 *cache = codec->reg_cache;', 'u8 *cache = ((struct aic31xx_priv *)snd_soc_component_get_drvdata(codec))->reg_cache;', count=2)
sub('''	.read			= aic31xx_read_reg_cache,
	.write			= aic31xx_write,
	.reg_cache_size		= ARRAY_SIZE(aic31xx_reg),
	.reg_word_size		= sizeof(u8),
	.reg_cache_default	= aic31xx_reg,
	.set_sysclk		= aic31xx_set_sysclk,''', '''	.read			= aic31xx_read_reg_cache,
	.write			= aic31xx_write,
	.set_sysclk		= aic31xx_set_sysclk,''')
sub('''	.component_driver = {
		.controls		= aic31xx_snd_controls,
		.num_controls		= ARRAY_SIZE(aic31xx_snd_controls),
	},''', '''	.controls		= aic31xx_snd_controls,
	.num_controls		= ARRAY_SIZE(aic31xx_snd_controls),''')
# probe/remove signatures: the component's return nothing on remove
sub('static int aic31xx_codec_remove(struct snd_soc_component *codec)', 'static void aic31xx_codec_remove(struct snd_soc_component *codec)')
c = re.sub(r'(static void aic31xx_codec_remove\(struct snd_soc_component \*codec\)\n\{.*?)\n\treturn 0;\n\}', r'\1\n}', c, count=1, flags=re.S)
# registration
sub('''	ret = snd_soc_register_codec(&pdev->dev,
				     &soc_codec_driver_aic31xx,
				     tlv320aic3101_dai_driver,
				     ARRAY_SIZE
				     (tlv320aic3101_dai_driver));''', '''	ret = devm_snd_soc_register_component(&pdev->dev,
				     &soc_codec_driver_aic31xx,
				     tlv320aic3101_dai_driver,
				     ARRAY_SIZE
				     (tlv320aic3101_dai_driver));''')
sub('''static int aic31xx_i2c_remove(struct i2c_client *pdev)
{
	snd_soc_unregister_codec(&pdev->dev);
	return 0;
}''', '''static void aic31xx_i2c_remove(struct i2c_client *pdev)
{
}''')
sub('''static int aic31xx_i2c_probe(struct i2c_client *pdev,
			     const struct i2c_device_id *id)''', 'static int aic31xx_i2c_probe(struct i2c_client *pdev)')
# GPIO descriptors in place of the legacy API; the enable line, when this driver owns it
c = re.sub(r'#else\n\tif \(!aic31xx->disable_reset\) \{\n\t\tenable_gpio = of_get_named_gpio.*?gpio_to_desc\(enable_gpio\);\n#endif\n',
           '''#else
	if (!aic31xx->disable_reset) {
		aic31xx->enable_gpiod = devm_gpiod_get(&pdev->dev, "enable", GPIOD_OUT_LOW);
		if (IS_ERR(aic31xx->enable_gpiod))
			return dev_err_probe(&pdev->dev, PTR_ERR(aic31xx->enable_gpiod), "enable-gpios\\\\n");
	}
#endif
''', c, count=1, flags=re.S)
assert 'of_get_named_gpio' not in c
# the reset pulse no longer has a gpio number to print
sub('''			dev_err(&pdev->dev,
				"could not set gpio(%d) to 0 (err=%d)\\n",
				enable_gpio, ret);''', '''			dev_err(&pdev->dev, "could not drive the enable line low (err=%d)\\n", ret);''')
sub('''			dev_err(&pdev->dev,
				"could not set gpio(%d) to 1 (err=%d)\\n",
				enable_gpio, ret);''', '''			dev_err(&pdev->dev, "could not drive the enable line high (err=%d)\\n", ret);''')
# renamed kernel APIs
sub('snd_soc_component_get_dapm(codec)', 'snd_soc_component_to_dapm(codec)')
sub('SND_SOC_DAIFMT_CBM_CFM', 'SND_SOC_DAIFMT_CBP_CFP')
sub('SND_SOC_DAIFMT_CBS_CFS', 'SND_SOC_DAIFMT_CBC_CFC')
sub('SND_SOC_DAIFMT_CBS_CFM', 'SND_SOC_DAIFMT_CBC_CFP')
sub('i2c_new_secondary_device(pdev, name, 0)', 'i2c_new_ancillary_device(pdev, name, 0)')
sub('#include <linux/of_gpio.h>', '#include <linux/gpio/consumer.h>\n#include <linux/regulator/consumer.h>')
# the ADC's supply (vibr on the MT6323): keep it on, the kernel would otherwise switch it off as unused
sub('''	mutex_init(&aic31xx->codecMutex);
''', '''	mutex_init(&aic31xx->codecMutex);
	aic31xx->reg_cache = devm_kmemdup(&pdev->dev, aic31xx_reg, sizeof(aic31xx_reg), GFP_KERNEL);
	if (!aic31xx->reg_cache)
		return -ENOMEM;
	ret = devm_regulator_get_enable_optional(&pdev->dev, "vibr");
	if (ret && ret != -ENODEV)
		return dev_err_probe(&pdev->dev, ret, "vibr supply\\n");
''')
sub('aic31xx = kzalloc(sizeof(struct aic31xx_priv), GFP_KERNEL);', 'aic31xx = devm_kzalloc(&pdev->dev, sizeof(struct aic31xx_priv), GFP_KERNEL);')
# Apply the codec's analog input/gain defaults before DAPM builds its mixer paths.
# Otherwise the reset cache says every input is disconnected, and set_pll saves
# those reset values over the intended defaults on the first capture.
sub('\taic31xx_add_widgets(codec);', '''	tlv320aic3101_adc_cfg(codec);
	aic31xx_add_widgets(codec);''', count=1)
# Routine codec clock configuration does not need informational logging.
c = c.replace('dev_info(codec->dev,', 'dev_dbg(codec->dev,')
# private data: the cache
h = sub('''	struct clk *mclk;
	u8 adc_page_no[NUM_ADC3101];''', '''	struct clk *mclk;
	u8 *reg_cache;
	u8 adc_page_no[NUM_ADC3101];''', text=h)
# the unused variable the old GPIO code left behind
sub('\tint enable_gpio = 0;\n', '')
os.makedirs(out, exist_ok=True)
open(os.path.join(out, 'tlv320aic3101.c'), 'w').write(c)
open(os.path.join(out, 'tlv320aic3101.h'), 'w').write(h)
print('ported')
