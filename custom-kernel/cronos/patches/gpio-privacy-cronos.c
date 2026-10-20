// SPDX-License-Identifier: GPL-2.0-only
/* Echo Show 5 privacy latch. Software can engage it; only the button releases it. */
#include <linux/delay.h>
#include <linux/gpio/consumer.h>
#include <linux/input.h>
#include <linux/interrupt.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/platform_device.h>
#include <linux/workqueue.h>

struct cronos_privacy {
	struct gpio_desc *enable, *state, *button;
	struct input_dev *key, *sw;
	struct mutex lock;
	struct delayed_work button_work, mute_work;
	unsigned long pressed_at;
	bool pressed, engage;
};

static void privacy_pulse(struct cronos_privacy *p)
{
	mutex_lock(&p->lock);
	gpiod_set_value_cansleep(p->enable, 1);
	msleep(1000);
	gpiod_set_value_cansleep(p->enable, 0);
	mutex_unlock(&p->lock);
}

static void privacy_mute_work(struct work_struct *work)
{
	struct cronos_privacy *p = container_of(to_delayed_work(work),
				struct cronos_privacy, mute_work);
	privacy_pulse(p);
}

static void privacy_button_work(struct work_struct *work)
{
	struct cronos_privacy *p = container_of(to_delayed_work(work),
				struct cronos_privacy, button_work);
	int pressed = gpiod_get_value_cansleep(p->button);

	if (pressed < 0 || pressed == p->pressed)
		return;
	p->pressed = pressed;
	if (pressed) {
		p->pressed_at = jiffies;
		p->engage = gpiod_get_value_cansleep(p->state) == 0;
	} else {
		if (p->engage && time_before(jiffies,
				p->pressed_at + msecs_to_jiffies(3000)))
			mod_delayed_work(system_wq, &p->mute_work,
					 msecs_to_jiffies(50));
		p->engage = false;
	}
	input_report_key(p->key, KEY_POWER, pressed);
	input_sync(p->key);
}

static irqreturn_t privacy_button_irq(int irq, void *data)
{
	struct cronos_privacy *p = data;

	mod_delayed_work(system_wq, &p->button_work, msecs_to_jiffies(50));
	return IRQ_HANDLED;
}

static irqreturn_t privacy_state_irq(int irq, void *data)
{
	struct cronos_privacy *p = data;
	int state = gpiod_get_value_cansleep(p->state);

	if (state >= 0) {
		input_report_switch(p->sw, SW_MUTE_DEVICE, state);
		input_sync(p->sw);
	}
	return IRQ_HANDLED;
}

static ssize_t state_show(struct device *dev, struct device_attribute *attr,
			 char *buf)
{
	struct cronos_privacy *p = dev_get_drvdata(dev);
	int state = gpiod_get_value_cansleep(p->state);

	return state < 0 ? state : sysfs_emit(buf, "%d\n", state);
}
static DEVICE_ATTR_RO(state);

static ssize_t enable_store(struct device *dev, struct device_attribute *attr,
			    const char *buf, size_t count)
{
	bool enable;

	if (kstrtobool(buf, &enable) || !enable)
		return -EINVAL;
	privacy_pulse(dev_get_drvdata(dev));
	return count;
}
static DEVICE_ATTR_WO(enable);

static struct attribute *privacy_attrs[] = {
	&dev_attr_state.attr, &dev_attr_enable.attr, NULL,
};
static const struct attribute_group privacy_group = { .attrs = privacy_attrs };

static void privacy_cancel(void *data)
{
	struct cronos_privacy *p = data;

	cancel_delayed_work_sync(&p->button_work);
	cancel_delayed_work_sync(&p->mute_work);
	gpiod_set_value_cansleep(p->enable, 0);
}

static int privacy_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct cronos_privacy *p;
	int ret, irq;

	p = devm_kzalloc(dev, sizeof(*p), GFP_KERNEL);
	if (!p)
		return -ENOMEM;
	platform_set_drvdata(pdev, p);
	mutex_init(&p->lock);
	INIT_DELAYED_WORK(&p->button_work, privacy_button_work);
	INIT_DELAYED_WORK(&p->mute_work, privacy_mute_work);
	p->enable = devm_gpiod_get(dev, "enable", GPIOD_OUT_LOW);
	if (IS_ERR(p->enable))
		return dev_err_probe(dev, PTR_ERR(p->enable), "enable GPIO\n");
	p->state = devm_gpiod_get(dev, "state", GPIOD_IN);
	if (IS_ERR(p->state))
		return dev_err_probe(dev, PTR_ERR(p->state), "state GPIO\n");
	p->button = devm_gpiod_get(dev, "button", GPIOD_IN);
	if (IS_ERR(p->button))
		return dev_err_probe(dev, PTR_ERR(p->button), "button GPIO\n");
	p->key = devm_input_allocate_device(dev);
	p->sw = devm_input_allocate_device(dev);
	if (!p->key || !p->sw)
		return -ENOMEM;
	p->key->name = "gpio-privacy-button";
	p->sw->name = "gpio-privacy-state";
	input_set_capability(p->key, EV_KEY, KEY_POWER);
	input_set_capability(p->sw, EV_SW, SW_MUTE_DEVICE);
	ret = input_register_device(p->key);
	if (ret)
		return ret;
	ret = input_register_device(p->sw);
	if (ret)
		return ret;
	/* IRQ resources are released before draining work and releasing GPIOs. */
	ret = devm_add_action_or_reset(dev, privacy_cancel, p);
	if (ret)
		return ret;
	irq = gpiod_to_irq(p->button);
	if (irq < 0)
		return irq;
	ret = devm_request_irq(dev, irq, privacy_button_irq,
		IRQF_TRIGGER_RISING | IRQF_TRIGGER_FALLING, "gpio-privacy", p);
	if (ret)
		return ret;
	irq = gpiod_to_irq(p->state);
	if (irq < 0)
		return irq;
	ret = devm_request_threaded_irq(dev, irq, NULL, privacy_state_irq,
		IRQF_TRIGGER_RISING | IRQF_TRIGGER_FALLING | IRQF_ONESHOT,
		"gpio-privacy-state", p);
	if (ret)
		return ret;
	privacy_state_irq(irq, p);
	return devm_device_add_group(dev, &privacy_group);
}

static const struct of_device_id privacy_match[] = {
	{ .compatible = "amazon,cronos-privacy" }, {},
};
MODULE_DEVICE_TABLE(of, privacy_match);
static struct platform_driver privacy_driver = {
	.probe = privacy_probe,
	.driver = { .name = "cronos-privacy", .of_match_table = privacy_match },
};
module_platform_driver(privacy_driver);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Echo Show 5 hardware privacy latch");
