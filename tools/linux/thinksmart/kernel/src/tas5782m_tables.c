// SPDX-License-Identifier: GPL-2.0
//
// TAS5782M — static tables: volume, preboot sequence, regmap config,
// DAI descriptor, and component driver.
//
// Split into a separate file so tas5782m.c stays readable.

#include <linux/module.h>
#include <linux/regmap.h>
#include <sound/soc.h>
#include <sound/pcm.h>

#include "tas5782m.h"

// ---------------------------------------------------------------------------
// Volume table (86 steps, 1 dB per step, Q8.23 fixed-point coefficients)
// Written to Book 0x8C, Page 0x2A, regs 0x24 (L) / 0x28 (R).
//
// Format: Q8.23 signed 32-bit, where 0x00800000 = 1.0 = 0 dB.
// Verified against FelixKa's reference table (tas5805m-felixka.h):
//   entry  0 = -85 dB = 0x000001D7
//   entry 60 = -25 dB = 0x000732AE  (exact match with FelixKa[85])
//   entry 85 =   0 dB = 0x00800000  (exact match with FelixKa[110])
// ---------------------------------------------------------------------------

const uint32_t tas5782m_volume[] = {
	0x000001D7, //  0 = -85 dB
	0x00000211, //  1 = -84 dB
	0x00000251, //  2 = -83 dB
	0x0000029A, //  3 = -82 dB
	0x000002EB, //  4 = -81 dB
	0x00000346, //  5 = -80 dB
	0x000003AD, //  6 = -79 dB
	0x00000420, //  7 = -78 dB
	0x000004A0, //  8 = -77 dB
	0x00000531, //  9 = -76 dB
	0x000005D3, // 10 = -75 dB
	0x00000689, // 11 = -74 dB
	0x00000755, // 12 = -73 dB
	0x0000083B, // 13 = -72 dB
	0x0000093C, // 14 = -71 dB
	0x00000A5C, // 15 = -70 dB
	0x00000BA0, // 16 = -69 dB
	0x00000D0B, // 17 = -68 dB
	0x00000EA3, // 18 = -67 dB
	0x0000106C, // 19 = -66 dB
	0x0000126D, // 20 = -65 dB
	0x000014AC, // 21 = -64 dB
	0x00001732, // 22 = -63 dB
	0x00001A07, // 23 = -62 dB
	0x00001D34, // 24 = -61 dB
	0x000020C4, // 25 = -60 dB
	0x000024C4, // 26 = -59 dB
	0x00002940, // 27 = -58 dB
	0x00002E49, // 28 = -57 dB
	0x000033EF, // 29 = -56 dB
	0x00003A45, // 30 = -55 dB
	0x00004161, // 31 = -54 dB
	0x0000495B, // 32 = -53 dB
	0x0000524F, // 33 = -52 dB
	0x00005C5A, // 34 = -51 dB
	0x0000679F, // 35 = -50 dB
	0x00007443, // 36 = -49 dB
	0x00008273, // 37 = -48 dB
	0x0000925E, // 38 = -47 dB
	0x0000A43A, // 39 = -46 dB
	0x0000B844, // 40 = -45 dB
	0x0000CEC0, // 41 = -44 dB
	0x0000E7FA, // 42 = -43 dB
	0x00010449, // 43 = -42 dB
	0x0001240B, // 44 = -41 dB
	0x000147AE, // 45 = -40 dB
	0x00016FA9, // 46 = -39 dB
	0x00019C86, // 47 = -38 dB
	0x0001CEDC, // 48 = -37 dB
	0x00020756, // 49 = -36 dB
	0x000246B4, // 50 = -35 dB
	0x00028DCE, // 51 = -34 dB
	0x0002DD95, // 52 = -33 dB
	0x00033718, // 53 = -32 dB
	0x00039B87, // 54 = -31 dB
	0x00040C37, // 55 = -30 dB
	0x00048AA7, // 56 = -29 dB
	0x00051884, // 57 = -28 dB
	0x0005B7B1, // 58 = -27 dB
	0x00066A4A, // 59 = -26 dB
	0x000732AE, // 60 = -25 dB
	0x00081385, // 61 = -24 dB
	0x00090FCB, // 62 = -23 dB
	0x000A2ADA, // 63 = -22 dB
	0x000B6873, // 64 = -21 dB
	0x000CCCCC, // 65 = -20 dB
	0x000E5CA1, // 66 = -19 dB
	0x00101D3F, // 67 = -18 dB
	0x0012149A, // 68 = -17 dB
	0x00144960, // 69 = -16 dB
	0x0016C310, // 70 = -15 dB
	0x00198A13, // 71 = -14 dB
	0x001CA7D7, // 72 = -13 dB
	0x002026F3, // 73 = -12 dB
	0x00241346, // 74 = -11 dB
	0x00287A26, // 75 = -10 dB
	0x002D6A86, // 76 =  -9 dB
	0x0032F52C, // 77 =  -8 dB
	0x00392CED, // 78 =  -7 dB
	0x004026E7, // 79 =  -6 dB
	0x0047FACC, // 80 =  -5 dB
	0x0050C335, // 81 =  -4 dB
	0x005A9DF7, // 82 =  -3 dB
	0x0065AC8C, // 83 =  -2 dB
	0x00721482, // 84 =  -1 dB
	0x00800000, // 85 =   0 dB (unity — matches FelixKa table[110])
};

// ---------------------------------------------------------------------------
// Preboot sequence (Book 0x00 Page 0x00 context)
// Taken from FelixKa's reference. Send before firmware load.
// Format: { reg, val } pairs
// ---------------------------------------------------------------------------

const uint8_t tas5782m_dsp_init[] = {
	// Always start from Book0/Page0
	TAS5782M_REG_PAGE, 0x00,
	TAS5782M_REG_BOOK, 0x00,

	// Enter standby + power-down before reset (FelixKa reference)
	TAS5782M_CTRL2,    TAS5782M_MODE_HIZ_PD,
	TAS5782M_REG_RST,  0x11,

	// Reset can disturb page/book state; reselect page 0 (Felix: 4x REG_PAGE).
	TAS5782M_REG_PAGE, 0x00,
	TAS5782M_REG_PAGE, 0x00,
	TAS5782M_REG_PAGE, 0x00,
	TAS5782M_REG_PAGE, 0x00,

	// Keep muted until stream-start path drives PLAY and unmutes.
	TAS5782M_CTRL3,    TAS5782M_MUTE,
	TAS5782M_SIG_CH_CTRL, TAS5782M_CH_DAC_MUTE,

	// Ignore MCLK/mclk-halt and use SCLK as PLL reference.
	0x25, 0x18,
	0x0d, 0x10,

	// Power-on mode; actual audio still gated by mute + DAPM ordering.
	TAS5782M_CTRL2,    TAS5782M_MODE_PLAY,
};

const size_t tas5782m_dsp_init_len = ARRAY_SIZE(tas5782m_dsp_init);

// ---------------------------------------------------------------------------
// regmap config — IMPORTANT: no cache, chip uses Book/Page multiplexing
// ---------------------------------------------------------------------------

const struct regmap_config tas5782m_regmap_config = {
	.reg_bits        = 8,
	.val_bits        = 8,
	.cache_type      = REGCACHE_NONE,  // MUST be NONE for Book/Page chips
	.max_register    = 0xFF,
};

