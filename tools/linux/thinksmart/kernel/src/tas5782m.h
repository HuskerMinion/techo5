/* SPDX-License-Identifier: GPL-2.0 */
/* SPDX-FileCopyrightText: rickx (https://github.com/rickx/ubuntu-thinksmart) */
/* SPDX-FileCopyrightText: 2026 Andrea Mattera (modifications, see README.md) */
#ifndef _TAS5782M_H
#define _TAS5782M_H

#include <linux/regmap.h>
#include <linux/debugfs.h>
#include <linux/atomic.h>
#include <sound/soc.h>

// ---------------------------------------------------------------------------
// Register map — Book 0, Page 0 (default)
// ---------------------------------------------------------------------------

#define TAS5782M_REG_PAGE       0x00  // Page select (within current book)
#define TAS5782M_REG_RST        0x01  // Reset control
#define TAS5782M_CTRL2          0x02  // Device control 2 — power/mode
#define TAS5782M_CTRL3          0x03  // Device control 3 — mute
#define TAS5782M_SIG_CH_CTRL    0x2A  // TAS5782M channel routing (0x2A, not TAS5805M's 0x28)
#define TAS5782M_AUTOMUTE_CTRL  0x59  // Auto-mute enable: bit4=R, bit0=L; default 0x11 = both enabled
#define TAS5782M_SAP_CTRL_1     0x33  // Serial audio port control (BCLK ratio)
/* NOTE: TAS5782M fault/status register address is not fully settled in public
 * docs. 0x68 works on some traces, but FelixKa notes TAS5782M fault registers
 * are not clearly documented. Keep this configurable point visible. */
#define TAS5782M_FAULT          0x68
#define TAS5782M_REG_BOOK       0x7F  // Book select

// CTRL2 (0x02) — power/mode bits (FelixKa/Android-aligned)
#define TAS5782M_MODE_PLAY        0x00  // Power-on/play
#define TAS5782M_MODE_POWERDOWN   0x01
#define TAS5782M_MODE_HIZ         0x10  // Standby/HI-Z
#define TAS5782M_MODE_HIZ_PD      (TAS5782M_MODE_HIZ | TAS5782M_MODE_POWERDOWN)

// CTRL3 (0x03) — mute
#define TAS5782M_MUTE           0x11  // Mute both channels (L|R)
#define TAS5782M_NORMAL_VOLUME  0x00

// SIG_CH_CTRL (0x28) — channel assignment
#define TAS5782M_CH_LEFT_LEFT     0x10
#define TAS5782M_CH_RIGHT_RIGHT   0x01
#define TAS5782M_CH_DAC_MUTE      0x00
#define TAS5782M_CH_DAC_NORMAL    (TAS5782M_CH_LEFT_LEFT | TAS5782M_CH_RIGHT_RIGHT)  /* 0x11, normal L→L R→R */

// SAP_CTRL_1 (0x33) — BCLK ratio
#define TAS5782M_SAP_BCLK_64     0x04  // 64 * LRCLK (32-bit stereo)

// FAULT (0x68) flags
#define TAS5782M_FAULT_CLOCK_ERROR  0x08
#define TAS5782M_FAULT_CLKE         0x04
#define TAS5782M_FAULT_OC           0x02
#define TAS5782M_FAULT_OT           0x01

// ---------------------------------------------------------------------------
// Volume locations: see the comment before TAS5782M_DIG_VOL_CTRL below
// ---------------------------------------------------------------------------

/*
 * Volume (2026-11-03). The original driver wrote Book 0x8C / Page 0x2A regs
 * 0x24/0x28: that is the undocumented TAS5805M location (copied from
 * sound/soc/codecs/tas5805m.c). The Lenovo process flow never touches page
 * 0x2A and the level did not change. TAS5782M datasheet (SLASEG8A):
 *  - Book 0 / Page 0 regs 0x3D (L) / 0x3E (R): DAC digital volume,
 *    0x00 = +24 dB, 0x30 = 0 dB, -0.5 dB per step, 0xFF = mute; ramped by
 *    the chip (P0-R63). The Lenovo firmware itself writes 0x30/0x30 here.
 *  - Book 0x8C / Page 0x1E regs 0x44 (L) / 0x48 (R): "fine volume" of the
 *    process flow, Q9.23 (0x00800000 = 0 dB); Lenovo firmware: 0x004c3ea8
 *    (-4.5 dB). Inside the swap range (8C/11/58 .. 8C/21/78): needs the swap
 *    flag Book 0x8C / Page 0x23 / reg 0x14 = 00 00 00 01 (sec. 9.3.9.6.2).
 */
#define TAS5782M_DIG_VOL_CTRL   0x3C  // PCTL: 0 = L/R independent
#define TAS5782M_DIG_VOL_LEFT   0x3D
#define TAS5782M_DIG_VOL_RIGHT  0x3E
#define TAS5782M_DIG_VOL_0DB    0x30
#define TAS5782M_DIG_VOL_MUTE   0xFF
#define TAS5782M_POWER_STATE    0x76  // bit7 DSP boot done, 3:0 state (5 = run, 8 = standby)

#define TAS5782M_VOL_BOOK       0x8C
#define TAS5782M_VOL_PAGE       0x1E
#define TAS5782M_VOL_CH_LEFT    0x44
#define TAS5782M_VOL_CH_RIGHT   0x48
#define TAS5782M_SWAP_PAGE      0x23
#define TAS5782M_SWAP_REG       0x14

#define TAS5782M_VOLUME_ENTRIES 86
#define TAS5782M_VOLUME_MIN     0
#define TAS5782M_VOLUME_MAX     (TAS5782M_VOLUME_ENTRIES - 1)

// ---------------------------------------------------------------------------
// Volume table (86 entries, 0.5dB steps) — from FelixKa, Apache-2.0
// ---------------------------------------------------------------------------

extern const uint32_t tas5782m_volume[TAS5782M_VOLUME_ENTRIES];

// ---------------------------------------------------------------------------
// Preboot / init sequence
// ---------------------------------------------------------------------------

extern const uint8_t tas5782m_dsp_init[];
extern const size_t  tas5782m_dsp_init_len;
extern const uint8_t tas5782m_dsp_flat[];
extern const size_t  tas5782m_dsp_flat_len;

// ---------------------------------------------------------------------------
// ASoC descriptors (defined in tas5782m.c or tas5782m_codec.c)
// ---------------------------------------------------------------------------

extern const struct regmap_config         tas5782m_regmap_config;
extern const struct snd_soc_component_driver tas5782m_component;
extern struct snd_soc_dai_driver          tas5782m_dai;

#endif /* _TAS5782M_H */
