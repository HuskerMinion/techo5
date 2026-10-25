// SPDX-License-Identifier: GPL-2.0
/*
 * athena: panel select. The Echo Show 8 2nd gen ships with one of several screens, on two
 * controllers that bind two drivers: ILI9881C (panel-ilitek-ili9881c) or JD9365
 * (panel-jadard-jd9365da-h3). LK names the one it set up in /chosen "atag,videolfb" and passes
 * its id as jd936x_id= on the command line; Amazon's kernel picks its driver by that name and the
 * init table by the id's low nibble.
 *
 * The device tree's panel node is the ILI9881C on BOE glass. This sets its compatible to the
 * unit's own screen before the DSI host adds the panel: by LK's name, else by the nibble when only
 * one controller uses it. A screen it can't tell keeps the device tree's.
 */
#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/kstrtox.h>
#include <linux/of.h>
#include <linux/slab.h>
#include <linux/string.h>

struct athena_panel {
	const char *lk_name;	/* LK's LCM name, in atag,videolfb */
	int nibble;		/* jd936x_id & 0xf, or -1 when both controllers use it */
	const char *compat;	/* the panel node's compatible: both strings */
	int len;
};

#define ATHENA_ILI(name, nib, glass) \
	{ "ili9881c_wxga_dsi_vdo_st_" name, nib, \
	  "amazon,athena-ili9881c-" glass "\0ilitek,ili9881c", \
	  sizeof("amazon,athena-ili9881c-" glass "\0ilitek,ili9881c") }
#define ATHENA_JD(name, nib, glass) \
	{ "jd936x_wxga_dsi_vdo_athena_st_" name, nib, \
	  "amazon,athena-jd9365-" glass "\0jadard,jd9365da-h3", \
	  sizeof("amazon,athena-jd9365-" glass "\0jadard,jd9365da-h3") }

/* From Amazon's LCM drivers (panel-notes: "How the vendor nibble picks the init function"). */
static const struct athena_panel athena_panels[] = {
	ATHENA_ILI("boe", 6, "boe"),
	ATHENA_ILI("kd_hsd", -1, "kd-hsd"),		/* nibble 3, as JD9365 kd_hsd proto */
	ATHENA_ILI("inx", 8, "inx"),
	ATHENA_ILI("kd_hsd_onyx", 9, "kd-hsd-onyx"),
	ATHENA_ILI("kd_inx_o", 0xa, "kd-inx-o"),
	ATHENA_JD("kd_hsd", 1, "kd-hsd"),
	ATHENA_JD("kd_hsd_new", 2, "kd-hsd-new"),
	ATHENA_JD("kd_hsd_proto", -1, "kd-hsd"),
	ATHENA_JD("boe", 4, "boe"),
	ATHENA_JD("boe_new", 5, "boe-new"),
	ATHENA_JD("inx", 7, "inx"),
};

static int athena_id = -1;

static int __init athena_jd936x_id(char *s)
{
	unsigned int v;

	if (!kstrtouint(s, 0, &v) && v <= 0xff)
		athena_id = v;
	return 0;	/* not claimed: init gets it in its environment, as before */
}
__setup("jd936x_id=", athena_jd936x_id);

/* Whether the tag holds name as a whole string (after a NUL or its start, before a NUL or its end). */
static bool athena_tag_names(const char *tag, int len, const char *name)
{
	int n = strlen(name);
	int i;

	for (i = 0; i + n <= len; i++)
		if ((!i || !tag[i - 1]) && !memcmp(tag + i, name, n) && (i + n == len || !tag[i + n]))
			return true;
	return false;
}

static int __init athena_panel_select(void)
{
	const struct athena_panel *want = NULL;
	struct device_node *np;
	struct property *prop;
	const char *tag = NULL;
	char id[12] = "none";
	int i, len = 0;

	if (!of_machine_is_compatible("amzn,athena"))
		return 0;
	np = of_find_compatible_node(NULL, NULL, "amazon,athena-ili9881c-boe");
	if (!np)
		return 0;

	if (athena_id >= 0)
		snprintf(id, sizeof(id), "0x%02x", athena_id);
	if (of_chosen)
		tag = of_get_property(of_chosen, "atag,videolfb", &len);
	for (i = 0; tag && !want && i < ARRAY_SIZE(athena_panels); i++)
		if (athena_tag_names(tag, len, athena_panels[i].lk_name))
			want = &athena_panels[i];
	for (i = 0; athena_id >= 0 && !want && i < ARRAY_SIZE(athena_panels); i++)
		if (athena_panels[i].nibble == (athena_id & 0xf))
			want = &athena_panels[i];
	if (!want) {
		pr_warn("athena panel: LK names no screen this kernel knows (jd936x_id %s), keeping %pOF's\n",
			id, np);
		goto out;
	}
	pr_info("athena panel: %s (LK %s, jd936x_id %s)\n", want->compat, want->lk_name, id);
	if (want == &athena_panels[0])
		goto out;	/* the device tree's own */

	prop = kzalloc(sizeof(*prop), GFP_KERNEL);
	if (!prop)
		goto out;
	prop->name = "compatible";
	prop->value = (void *)want->compat;
	prop->length = want->len;
	if (of_update_property(np, prop)) {
		pr_err("athena panel: could not set %pOF's compatible\n", np);
		kfree(prop);
	}
out:
	of_node_put(np);
	return 0;
}
/* Before device_initcall, when mediatek-dsi probes and adds the panel. */
arch_initcall(athena_panel_select);
