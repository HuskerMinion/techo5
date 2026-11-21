/* SPDX-License-Identifier: GPL-2.0-only */
/* SPDX-FileCopyrightText: rickx (https://github.com/rickx/ubuntu-thinksmart) */
/* SPDX-FileCopyrightText: 2026 Andrea Mattera (modifications, see README.md) */
#ifndef _TAS5782M_PRIV_H
#define _TAS5782M_PRIV_H

#include <linux/i2c.h>
#include <linux/regmap.h>
#include <linux/gpio/consumer.h>
#include <linux/regulator/consumer.h>
#include <linux/workqueue.h>
#include <linux/mutex.h>

#include "tas5782m.h"

/* Private per-chip state shared by core and debug translation units. */
struct tas5782m_priv {
	struct i2c_client        *client;
	struct regmap            *regmap;
	struct regulator         *pvdd;
	struct gpio_desc         *gpio_pdn;

	char                      dsp_config_name[64];
	const u8                 *dsp_cfg_data;    /* the tuning file, or tas5782m_dsp_flat */
	size_t                    dsp_cfg_len;
	bool                      dsp_tuned;       /* the tuning file is loaded */

	int                       vol[2];
	bool                      is_powered;
	bool                      is_muted;
	bool                      stream_active;   /* trigger(START) .. DAPM PRE_PMD */
	u32                       fine_vol_base[2]; /* fine volume of the firmware (Q9.23) */

	struct work_struct        work;
	struct delayed_work       standby_work;    /* delayed standby after the stream */
	struct mutex              lock;
};

#endif /* _TAS5782M_PRIV_H */
