// SPDX-License-Identifier: GPL-2.0
/*
 * Amazon Echo Show 5 (cronos) microphone capture: an iCE40 FPGA on SPI packs the TLV320AIC3101's two
 * microphones and the playback loopback into 4-channel frames ("dough" frames) that this driver
 * polls into a PCM device. After Amazon Lab126's amzn-mt-spi-pcm.c (android_kernel_amazon_mt8163,
 * GPL v2), rewritten for the current ASoC component API with pinctrl and GPIO descriptors in place
 * of the MediaTek 4.9 audio helpers.
 *
 * Initialization: reset the ADC, hold the FPGA's chip select low while CRESET is released, stream the
 * bitstream (i2s_to_spi_4ch_v208.bin, carved out of the 4.9 kernel by tools/extract-fpga-fw.py),
 * read one frame and check the revision byte, then switch the I2S pins on and command I2S mode.
 * Capture: a high-priority work item reads a whole frame every few ms and appends the audio it
 * carries to the ring; the FPGA reports overruns and how many frames each read holds.
 */
#include <linux/delay.h>
#include <linux/firmware.h>
#include <linux/gpio/consumer.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/pinctrl/consumer.h>
#include <linux/regulator/consumer.h>
#include <linux/sched.h>
#include <linux/spi/spi.h>
#include <linux/spinlock.h>
#include <linux/workqueue.h>
#include <sound/core.h>
#include <sound/pcm.h>
#include <sound/pcm_params.h>
#include <sound/soc.h>

#define DOUGH_SAMPLE_BYTES	3
#define DOUGH_CHANNELS		4
#define DOUGH_FRAME_BYTES	(DOUGH_SAMPLE_BYTES * DOUGH_CHANNELS)	/* 12 */
#define DOUGH_FRAMES		256
#define DOUGH_REV_MIN		30
#define DOUGH_REV_MAX		251

enum dough_cmd {
	DOUGH_CMD_NOP = 0,
	DOUGH_CMD_OFF = 0x80,
	DOUGH_CMD_I2S = 0x81,
};

struct __packed dough_status {
	u32 timestamp_48mhz;	/* wire bytes 0-3 */
	u16 num_audio_frames;	/* wire bytes 4-5 */
	u8 rsvd;		/* wire byte 6 */
	u8 mode;		/* wire byte 7 */
	u8 dac_inactive;	/* wire byte 8 */
	u8 i2s_inactive;	/* wire byte 9 */
	u8 overrun;		/* wire byte 10 */
	u8 fpga_rev;		/* wire byte 11 */
};

struct __packed dough_frame {
	struct dough_status status;
	u8 audio[DOUGH_FRAMES][DOUGH_FRAME_BYTES];
};

#define FPGA_FIRMWARE		"amazon/i2s_to_spi_4ch_v208.bin"
#define FPGA_FIRMWARE_OLD	"amazon/i2s_to_spi_4ch_v193.bin"
#define FPGA_FIRMWARE_MAX	(10 * 4096)
#define SPI_SPEED_HZ		50000000
#define SPI_SETUP_BYTES		32
#define FPGA_DELAY_MS		2
#define PINCTRL_DELAY_MS	2

/* the ALSA period is one FPGA frame's worth of audio plus one status frame, as Amazon's driver has it */
#define SPI_PERIOD_BYTES	(DOUGH_FRAME_BYTES * (DOUGH_FRAMES + 1))	/* 3084: 257 frames */
#define SPI_PERIODS_MAX		10
#define SPI_BUFFER_BYTES_MAX	(SPI_PERIOD_BYTES * SPI_PERIODS_MAX)

struct fpga_pcm {
	struct spi_device *spi;
	struct gpio_desc *reset;	/* FPGA CRESET */
	struct gpio_desc *cdone;	/* FPGA CDONE, read only */
	struct gpio_desc *adc_reset;
	struct gpio_desc *mic_enable;
	struct pinctrl *pinctrl;
	struct pinctrl_state *pins_default, *cs_low, *i2s_off, *i2s_on;
	bool downmix;
	bool configured;

	struct workqueue_struct *wq;
	struct work_struct work;
	struct snd_pcm_substream *substream;
	spinlock_t lock;
	size_t wr;		/* write offset in the ring, bytes */
	size_t elapsed;		/* bytes since the last period_elapsed */
	bool run;
	unsigned int wait_min_us, wait_max_us;
	size_t fpga_overruns;
	struct dough_frame *tx, *rx;
};

static int fpga_txrx(struct fpga_pcm *f, const void *tx, void *rx, unsigned int len)
{
	struct spi_transfer t = {
		.tx_buf = tx,
		.rx_buf = rx,
		.len = len,
		.bits_per_word = 8,
		.speed_hz = SPI_SPEED_HZ,
	};

	return spi_sync_transfer(f->spi, &t, 1);
}

static inline s32 rd24(const u8 *p)
{
	s32 v = p[0] | (p[1] << 8) | (p[2] << 16);

	return (v & 0x800000) ? v - (1 << 24) : v;
}

static inline void wr24(u8 *p, s32 v)
{
	p[0] = v; p[1] = v >> 8; p[2] = v >> 16;
}

/* ---- the read loop -------------------------------------------------------------------------- */

static void fpga_read_work(struct work_struct *work)
{
	struct fpga_pcm *f = container_of(work, struct fpga_pcm, work);
	struct snd_pcm_substream *ss = f->substream;
	struct snd_pcm_runtime *rt = ss->runtime;
	size_t threshold = frames_to_bytes(rt, rt->period_size);
	ktime_t prev = ktime_get_raw();
	unsigned int iter = 0;
	int ret;

	sched_set_fifo_low(current);
	f->wr = 0;
	f->elapsed = 0;
	f->fpga_overruns = 0;
	memset(f->tx, 0, sizeof(*f->tx));

	while (READ_ONCE(f->run)) {
		struct dough_frame *rx = f->rx;
		unsigned int n, i;
		size_t bytes;
		u8 *src;
		s64 diff_us;

		ret = fpga_txrx(f, f->tx, rx, sizeof(*rx));
		if (ret < 0) {
			dev_err(&f->spi->dev, "frame read failed: %d\n", ret);
			break;
		}
		if (rx->status.fpga_rev < DOUGH_REV_MIN || rx->status.fpga_rev > DOUGH_REV_MAX) {
			dev_err_ratelimited(&f->spi->dev, "bad FPGA revision byte %u\n", rx->status.fpga_rev);
			goto delay;
		}
		if (rx->status.overrun && iter >= 10)
			dev_err_ratelimited(&f->spi->dev, "FPGA overrun (%zu), frames %u, mode %u\n",
					    ++f->fpga_overruns, le16_to_cpu(rx->status.num_audio_frames),
					    rx->status.mode);
		n = le16_to_cpu(rx->status.num_audio_frames);
		if (n > DOUGH_FRAMES) {
			dev_err_ratelimited(&f->spi->dev, "FPGA frame count %u out of range\n", n);
			goto delay;
		}
		if (f->downmix) {
			for (i = 0; i < n; i++) {
				u8 *fr = rx->audio[i];
				s32 mix = (rd24(fr) + rd24(fr + DOUGH_SAMPLE_BYTES)) / 2;

				wr24(fr, mix);
				wr24(fr + DOUGH_SAMPLE_BYTES, mix);
			}
		}
		/* append the audio frames to the ring */
		bytes = (size_t)n * DOUGH_FRAME_BYTES;
		src = &rx->audio[0][0];
		while (bytes) {
			size_t chunk = min(rt->dma_bytes - f->wr, bytes);

			memcpy(rt->dma_area + f->wr, src, chunk);
			spin_lock(&f->lock);
			f->wr = (f->wr + chunk) % rt->dma_bytes;
			spin_unlock(&f->lock);
			src += chunk;
			bytes -= chunk;
			f->elapsed += chunk;
		}
		if (f->elapsed >= threshold) {
			f->elapsed -= threshold;
			snd_pcm_period_elapsed(ss);
		}
delay:
		if (iter < 10)
			iter++;
		diff_us = ktime_us_delta(ktime_get_raw(), prev);
		if (diff_us < (s64)f->wait_min_us - 500 && !rx->status.overrun) {
			usleep_range(f->wait_min_us - diff_us, f->wait_max_us - diff_us);
			prev = ktime_get_raw();
		} else {
			prev = ktime_add_us(prev, diff_us);
		}
	}
}

/* ---- PCM ------------------------------------------------------------------------------------ */

static const struct snd_pcm_hardware fpga_pcm_hardware = {
	.info = SNDRV_PCM_INFO_INTERLEAVED | SNDRV_PCM_INFO_MMAP | SNDRV_PCM_INFO_MMAP_VALID,
	.formats = SNDRV_PCM_FMTBIT_S24_3LE,
	.rates = SNDRV_PCM_RATE_16000 | SNDRV_PCM_RATE_48000 | SNDRV_PCM_RATE_96000,
	.rate_min = 16000,
	.rate_max = 96000,
	.channels_min = DOUGH_CHANNELS,
	.channels_max = DOUGH_CHANNELS,
	.buffer_bytes_max = SPI_BUFFER_BYTES_MAX,
	.period_bytes_min = SPI_PERIOD_BYTES,
	.period_bytes_max = SPI_PERIOD_BYTES,
	.periods_min = 1,
	.periods_max = SPI_PERIODS_MAX,
};

static int fpga_pcm_open(struct snd_soc_component *c, struct snd_pcm_substream *ss)
{
	struct fpga_pcm *f = snd_soc_component_get_drvdata(c);

	if (ss->stream != SNDRV_PCM_STREAM_CAPTURE)
		return -EINVAL;
	/* Keep the card/speaker registered, but never start a worker on an unresponsive FPGA. */
	if (!f->configured)
		return -ENODEV;
	snd_soc_set_runtime_hwparams(ss, &fpga_pcm_hardware);
	snd_pcm_hw_constraint_integer(ss->runtime, SNDRV_PCM_HW_PARAM_PERIODS);
	f->substream = ss;
	return 0;
}

static int fpga_pcm_hw_params(struct snd_soc_component *c, struct snd_pcm_substream *ss,
			      struct snd_pcm_hw_params *params)
{
	struct fpga_pcm *f = snd_soc_component_get_drvdata(c);

	switch (params_rate(params)) {
	case 48000:
		f->wait_min_us = 1500; f->wait_max_us = 2000;
		break;
	case 96000:
		f->wait_min_us = 2000; f->wait_max_us = 2500;
		break;
	default:
		f->wait_min_us = 6000; f->wait_max_us = 7000;
	}
	return 0;
}

static int fpga_pcm_trigger(struct snd_soc_component *c, struct snd_pcm_substream *ss, int cmd)
{
	struct fpga_pcm *f = snd_soc_component_get_drvdata(c);

	switch (cmd) {
	case SNDRV_PCM_TRIGGER_START:
		WRITE_ONCE(f->run, true);
		queue_work(f->wq, &f->work);
		return 0;
	case SNDRV_PCM_TRIGGER_STOP:
	case SNDRV_PCM_TRIGGER_SUSPEND:
		WRITE_ONCE(f->run, false);
		return 0;
	default:
		return -EINVAL;
	}
}

static int fpga_pcm_sync_stop(struct snd_soc_component *c, struct snd_pcm_substream *ss)
{
	struct fpga_pcm *f = snd_soc_component_get_drvdata(c);

	WRITE_ONCE(f->run, false);
	cancel_work_sync(&f->work);
	return 0;
}

static int fpga_pcm_close(struct snd_soc_component *c, struct snd_pcm_substream *ss)
{
	struct fpga_pcm *f = snd_soc_component_get_drvdata(c);

	/* Also cover failed setup/START paths before ALSA frees the runtime. */
	fpga_pcm_sync_stop(c, ss);
	f->substream = NULL;
	return 0;
}

static snd_pcm_uframes_t fpga_pcm_pointer(struct snd_soc_component *c, struct snd_pcm_substream *ss)
{
	struct fpga_pcm *f = snd_soc_component_get_drvdata(c);
	snd_pcm_uframes_t frames;

	spin_lock(&f->lock);
	frames = bytes_to_frames(ss->runtime, f->wr);
	spin_unlock(&f->lock);
	return frames;
}

static int fpga_pcm_new(struct snd_soc_component *c, struct snd_soc_pcm_runtime *rtd)
{
	snd_pcm_set_managed_buffer_all(rtd->pcm, SNDRV_DMA_TYPE_VMALLOC, NULL,
				       SPI_BUFFER_BYTES_MAX, SPI_BUFFER_BYTES_MAX);
	return 0;
}

static const struct snd_soc_component_driver fpga_component = {
	.name = "amazon-spi-pcm",
	.open = fpga_pcm_open,
	.close = fpga_pcm_close,
	.hw_params = fpga_pcm_hw_params,
	.hw_free = fpga_pcm_sync_stop,
	.sync_stop = fpga_pcm_sync_stop,
	.trigger = fpga_pcm_trigger,
	.pointer = fpga_pcm_pointer,
	.pcm_new = fpga_pcm_new,
};

static struct snd_soc_dai_driver fpga_dai = {
	.name = "amazon-spi-capture",
	.capture = {
		.stream_name = "SPI Capture",
		.channels_min = DOUGH_CHANNELS,
		.channels_max = DOUGH_CHANNELS,
		.rates = SNDRV_PCM_RATE_16000 | SNDRV_PCM_RATE_48000 | SNDRV_PCM_RATE_96000,
		.formats = SNDRV_PCM_FMTBIT_S24_3LE,
	},
};

/* ---- bring-up ------------------------------------------------------------------------------- */

static int fpga_command(struct fpga_pcm *f, u8 cmd)
{
	u8 *buf = kzalloc(SPI_SETUP_BYTES, GFP_KERNEL);
	int ret;

	if (!buf)
		return -ENOMEM;
	buf[0] = cmd;
	ret = fpga_txrx(f, buf, NULL, SPI_SETUP_BYTES);
	kfree(buf);
	return ret;
}

static int fpga_configure(struct fpga_pcm *f)
{
	struct device *dev = &f->spi->dev;
	const struct firmware *fw;
	size_t bytes;
	u8 *buf;
	int ret, i;

	/* The vendor boot leaves the 1.2 V core rail on without a consumer. */
	ret = devm_regulator_get_enable(dev, "vcc");
	if (ret)
		return dev_err_probe(dev, ret, "FPGA core supply\n");
	/* Amazon's CONFIG_FPGA_POWER_SEQUENCE: VCCIO2, wait, ADC reset, then VCCIO0. */
	ret = devm_regulator_get_enable(dev, "vcamaf");
	if (ret)
		return dev_err_probe(dev, ret, "FPGA VCCIO2 supply\n");
	msleep(10);
	/* Pulse the ADC reset between the two FPGA I/O supplies. */
	if (f->adc_reset) {
		gpiod_set_value_cansleep(f->adc_reset, 1);
		msleep(10);
		gpiod_set_value_cansleep(f->adc_reset, 0);
	}
	ret = devm_regulator_get_enable(dev, "vcn18");
	if (ret)
		return dev_err_probe(dev, ret, "FPGA VCCIO0 supply\n");
	ret = fpga_command(f, DOUGH_CMD_OFF);
	if (ret)
		return dev_err_probe(dev, ret, "first SPI transfer failed\n");
	if (f->i2s_off)
		pinctrl_select_state(f->pinctrl, f->i2s_off);

	ret = request_firmware(&fw, FPGA_FIRMWARE, dev);
	if (ret)
		ret = request_firmware(&fw, FPGA_FIRMWARE_OLD, dev);
	if (ret)
		return dev_err_probe(dev, ret, "no FPGA bitstream (%s)\n", FPGA_FIRMWARE);
	bytes = roundup(fw->size, 1024) + 1024;	/* the trailing zeros are the dummy clocks CDONE needs */
	if (bytes > FPGA_FIRMWARE_MAX) {
		release_firmware(fw);
		return dev_err_probe(dev, -EFBIG, "FPGA bitstream too big\n");
	}
	buf = kzalloc(bytes, GFP_KERNEL);
	if (!buf) {
		release_firmware(fw);
		return -ENOMEM;
	}
	memcpy(buf, fw->data, fw->size);
	release_firmware(fw);

	/*
	 * iCE40 slave configuration (Lattice TN1248): chip select low while CRESET is released, a
	 * pause, then the bitstream with trailing dummy clocks. The controller owns the chip select,
	 * so the "cs-low" pin state turns pin 53 into a GPIO held low for the reset pulse and
	 * "default" hands it back before the transfer.
	 */
	if (f->cs_low)
		pinctrl_select_state(f->pinctrl, f->cs_low);
	gpiod_set_value_cansleep(f->reset, 1);
	msleep(FPGA_DELAY_MS);
	gpiod_set_value_cansleep(f->reset, 0);
	msleep(FPGA_DELAY_MS);
	if (f->cs_low)
		pinctrl_select_state(f->pinctrl, f->pins_default);
	ret = fpga_txrx(f, buf, NULL, bytes);
	kfree(buf);
	if (ret)
		return dev_err_probe(dev, ret, "bitstream transfer failed\n");
	msleep(FPGA_DELAY_MS);
	/* Allow the configured fabric to settle before checking its revision. */
	for (i = 0; i < 5; i++) {
		msleep(10 * (i + 1));
		memset(f->tx, 0, sizeof(*f->tx));
		ret = fpga_txrx(f, f->tx, f->rx, sizeof(*f->rx));
		if (ret)
			return dev_err_probe(dev, ret, "revision read failed\n");
		if (f->rx->status.fpga_rev >= DOUGH_REV_MIN && f->rx->status.fpga_rev <= DOUGH_REV_MAX)
			break;
	}
	if (f->rx->status.fpga_rev < DOUGH_REV_MIN || f->rx->status.fpga_rev > DOUGH_REV_MAX) {
		/*
		 * Do not fail probe over this: ASoC will not finish instantiating the card -- and
		 * so will never even attempt the amplifier's DAI link -- until every declared DAI
		 * link's component has registered, "amazon-spi-capture" included. Register it in
		 * this degraded state (capture will simply not work until the real bug is found)
		 * so a broken microphone path stops blocking a working speaker path.
		 */
		dev_err(dev, "FPGA did not configure: revision byte %u after %d tries; capture will not work\n",
			f->rx->status.fpga_rev, i);
		/*
		 * The I2S1 pins (72-74) still have to leave GPIO mode: the amplifier hangs off the
		 * same I2S1 lines, and returning here with them as GPIOs is exactly a silent speaker
		 * with the AFE clocking, DAPM fully on and the amplifier in "Play" mode.
		 */
		if (f->i2s_on)
			pinctrl_select_state(f->pinctrl, f->i2s_on);
		return 0;
	}
	dev_info(dev, "FPGA revision %u\n", f->rx->status.fpga_rev);

	if (f->i2s_on)
		pinctrl_select_state(f->pinctrl, f->i2s_on);
	msleep(PINCTRL_DELAY_MS);
	if (f->mic_enable)
		gpiod_set_value_cansleep(f->mic_enable, 1);
	ret = fpga_command(f, DOUGH_CMD_I2S);
	if (!ret)
		f->configured = true;
	return ret;
}

static int fpga_probe(struct spi_device *spi)
{
	struct device *dev = &spi->dev;
	struct fpga_pcm *f;
	int ret;

	f = devm_kzalloc(dev, sizeof(*f), GFP_KERNEL);
	if (!f)
		return -ENOMEM;
	f->spi = spi;
	f->tx = devm_kzalloc(dev, sizeof(*f->tx), GFP_KERNEL);
	f->rx = devm_kzalloc(dev, sizeof(*f->rx), GFP_KERNEL);
	if (!f->tx || !f->rx)
		return -ENOMEM;
	spin_lock_init(&f->lock);
	INIT_WORK(&f->work, fpga_read_work);
	f->downmix = of_property_read_bool(dev->of_node, "amazon,mic-downmix");

	f->reset = devm_gpiod_get(dev, "reset", GPIOD_OUT_HIGH);	/* asserted */
	if (IS_ERR(f->reset))
		return dev_err_probe(dev, PTR_ERR(f->reset), "reset-gpios\n");
	f->cdone = devm_gpiod_get_optional(dev, "cdone", GPIOD_IN);
	if (IS_ERR(f->cdone))
		return PTR_ERR(f->cdone);
	f->adc_reset = devm_gpiod_get_optional(dev, "adc-reset", GPIOD_OUT_LOW);
	if (IS_ERR(f->adc_reset))
		return PTR_ERR(f->adc_reset);
	f->mic_enable = devm_gpiod_get_optional(dev, "mic-enable", GPIOD_OUT_LOW);
	if (IS_ERR(f->mic_enable))
		return PTR_ERR(f->mic_enable);
	f->pinctrl = devm_pinctrl_get(dev);
	if (!IS_ERR(f->pinctrl)) {
		f->pins_default = pinctrl_lookup_state(f->pinctrl, PINCTRL_STATE_DEFAULT);
		f->cs_low = pinctrl_lookup_state(f->pinctrl, "cs-low");
		f->i2s_off = pinctrl_lookup_state(f->pinctrl, "i2s-off");
		f->i2s_on = pinctrl_lookup_state(f->pinctrl, "i2s-on");
		if (IS_ERR(f->pins_default) || IS_ERR(f->cs_low))
			f->cs_low = NULL;
		if (IS_ERR(f->i2s_off))
			f->i2s_off = NULL;
		if (IS_ERR(f->i2s_on))
			f->i2s_on = NULL;
	} else {
		f->pinctrl = NULL;
	}

	spi->mode = SPI_MODE_3;
	spi->bits_per_word = 8;
	spi->max_speed_hz = SPI_SPEED_HZ;
	ret = spi_setup(spi);
	if (ret)
		return dev_err_probe(dev, ret, "spi_setup\n");

	f->wq = alloc_workqueue("amazon-spi-pcm", WQ_HIGHPRI | WQ_MEM_RECLAIM | WQ_PERCPU, 1);
	if (!f->wq)
		return -ENOMEM;
	spi_set_drvdata(spi, f);

	ret = fpga_configure(f);
	if (ret) {
		destroy_workqueue(f->wq);
		return ret;
	}
	ret = devm_snd_soc_register_component(dev, &fpga_component, &fpga_dai, 1);
	if (ret)
		destroy_workqueue(f->wq);
	return ret;
}

static void fpga_remove(struct spi_device *spi)
{
	struct fpga_pcm *f = spi_get_drvdata(spi);

	WRITE_ONCE(f->run, false);
	destroy_workqueue(f->wq);
	fpga_command(f, DOUGH_CMD_OFF);
}

static const struct of_device_id fpga_of_match[] = {
	{ .compatible = "amazon,cronos-audio-fpga" },
	{ }
};
MODULE_DEVICE_TABLE(of, fpga_of_match);

static struct spi_driver fpga_driver = {
	.driver = {
		.name = "amazon-spi-pcm",
		.of_match_table = fpga_of_match,
	},
	.probe = fpga_probe,
	.remove = fpga_remove,
};
module_spi_driver(fpga_driver);

MODULE_FIRMWARE(FPGA_FIRMWARE);
MODULE_DESCRIPTION("Amazon Echo Show 5 microphone FPGA on SPI");
MODULE_LICENSE("GPL");
