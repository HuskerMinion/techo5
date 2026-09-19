#!/usr/bin/env python3
"""Build the mainline tas5805m driver's DSP config blob (tas5805m_dsp_default.bin) from Amazon's
own downstream driver's MONO_MINI table (sources/amazon-kernel/sound/soc/codecs/tas5805m_mono.h, speaker-config = <4> on
cronos_pvt_4_1.dts). Mainline's own dsp_cfg_preboot[] already applies the equivalent of
this table's first 9 entries (init, reset, first HiZ) with its own timing, so the blob picks up
right after that -- from the Deep Sleep entry onward -- through to the end. Register 0xFE (254) is
Amazon's own "delay N ms" sentinel (TAS5805M_DELAY in their tas5805m.c); patches/0008-tas5805m-delay.sh
teaches mainline's send_cfg() the same convention, so it is kept as-is rather than stripped.
"""
import struct

# sources/amazon-kernel/sound/soc/codecs/tas5805m_mono.h, tas5805m_init_mono_mini[], transcribed in full for auditability.
MONO_MINI = [
    (0x00, 0x00), (0x7f, 0x00), (0x03, 0x02),
    (0xfe, 0x02),                                   # 2ms delay
    (0x01, 0x11),                                   # Reset
    (0xfe, 0x14),                                   # 20ms delay
    (0x00, 0x00), (0x7f, 0x00), (0x03, 0x02),       # HiZ  <- mainline's dsp_cfg_preboot ends here
    (0x03, 0x00),                                   # Deep Sleep
    (0xfe, 0x14),                                   # 20ms delay
    (0x00, 0x00), (0x7f, 0x00), (0x03, 0x02),       # HiZ
    (0x50, 0x04),                                   # Auto Mute Off
    (0x53, 0x60),                                   # BW 175kHz
    (0x54, 0x15),                                   # AGAIN -10.5dB
    (0x02, 0x04),                                   # PBTL Mode
    (0x5d, 0x98),                                   # Dither disable, DEM Enabled
    (0x66, 0x07),                                   # DSP Bypass
    (0x33, 0x00),                                   # 16 bit word
    (0x78, 0x80),                                   # Clear Fault
    (0x00, 0x00), (0x7f, 0x00),
    (0x61, 0x0b),                                   # ADR as FAULTZ output
    (0x60, 0x01),                                   # ADR is output
    (0x7d, 0x11), (0x7e, 0xff), (0x3a, 0xf9), (0x3f, 0x0f), (0x00, 0x01), (0x13, 0x20),
    (0x00, 0x00), (0x7f, 0x00),
    (0x74, 0x10),                                   # Mask clock faults
    (0x03, 0x03),                                   # Enter Play
    (0x78, 0x80),                                   # Clear fault bit
]
CUT_AFTER_INDEX = 8   # inclusive: last entry mainline's dsp_cfg_preboot already applies (the first HiZ)

import sys
out = sys.argv[1] if len(sys.argv) > 1 else 'initramfs/firmware/tas5805m/tas5805m_dsp_default.bin'
blob = b''.join(struct.pack('BB', r, v) for r, v in MONO_MINI[CUT_AFTER_INDEX + 1:])
open(out, 'wb').write(blob)
print('%s: %d bytes, %d register operations' % (out, len(blob), len(blob) // 2))
