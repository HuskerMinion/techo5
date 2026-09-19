#!/bin/sh
# TAS5805M DSP config blobs (PPC3 output) use register 0xFE as an out-of-band "delay N ms"
# instruction -- confirmed against Amazon's own downstream driver (TAS5805M_DELAY == 254,
# tas5805m.c:tas5805m_apply_seq()). Mainline's send_cfg() writes byte pairs verbatim with no
# such handling, so a blob carrying one would land as a real (harmless but useless, and the
# intended delay lost) register write. Teach it the same convention Amazon's driver uses.
set -e
p=${KDIR:-/src/linux-7.2.6}/sound/soc/codecs/tas5805m.c
grep -q 'TAS5805M_CFG_DELAY' $p && exit 0
python3 - "$p" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
old = '''static void send_cfg(struct regmap *rm,
		     const uint8_t *s, unsigned int len)
{
	unsigned int i;

	for (i = 0; i + 1 < len; i += 2)
		regmap_write(rm, s[i], s[i + 1]);
}'''
new = '''/* TAS5805M DSP config blobs (TI's PPC3 tool output) use register 0xFE as an out-of-band
 * "delay N milliseconds" instruction -- confirmed against Amazon's downstream driver for this
 * exact chip and speaker config (TAS5805M_DELAY == 254). 0xFE is not a real writable register.
 */
#define TAS5805M_CFG_DELAY 0xfe

static void send_cfg(struct regmap *rm,
		     const uint8_t *s, unsigned int len)
{
	unsigned int i;

	for (i = 0; i + 1 < len; i += 2) {
		if (s[i] == TAS5805M_CFG_DELAY)
			usleep_range(s[i + 1] * 1000, s[i + 1] * 1000 + 200);
		else
			regmap_write(rm, s[i], s[i + 1]);
	}
}'''
assert old in s
open(p, 'w').write(s.replace(old, new, 1))
PY
grep -n "TAS5805M_CFG_DELAY" $p | head -3
