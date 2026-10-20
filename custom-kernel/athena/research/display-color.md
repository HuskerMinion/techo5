# Display color blocks: CCORR, GAMMA and AAL on mainline mediatek-drm

Status: CCORR in use on both units (night warmth); GAMMA verified present, unused; content-adaptive
backlight built on the GPU instead of the AAL (Adaptive backlight, off by default), not yet run on a
unit. Date: 2026-10-19. Covers the Echo Show 8 2nd gen (MT8183) and, for comparison, the
Echo Show 5 2nd gen (MT8163; its own specifics are in `../../cronos/research/mt8163-display-color.md`).

## What the pipelines have

Both main display paths carry the color blocks, and mainline exposes two of them on the CRTC:

| | MT8183 (Show 8) | MT8163 (Show 5) |
|---|---|---|
| Main path | ovl0, ovl_2l0, rdma0, color0, ccorr0, aal0, gamma0, dither0, dsi0 | ovl0, color0, ccorr, aal0, gamma, dither0, rdma0, ufoe, dsi0 |
| CRTC `CTM` | yes (CCORR) | yes (CCORR) |
| CRTC `GAMMA_LUT` / `_SIZE` | yes, 512 | yes, 512 |
| CCORR at boot | `CFG` 0xa (engine on), coefficients 0x400 on the diagonal | `CFG` 0x2, coefficients 0x400 on the diagonal |
| AAL at boot | `CFG` 0x1 (relay) | `CFG` 0x1 (relay) |
| GAMMA at boot | `CFG` 0x101 (relay) | `CFG` 0x1 (relay) |

Read with a `/dev/mem` peek (CCORR coefficients at block + 0x80, `CFG` at + 0x20) and a libdrm
property listing; tools in the session scratchpad (`color/peek.c`, `color/drmcolor.c`).

## How mainline behaves (7.2.11)

- Setting a property needs DRM master, so it goes through `techo5-gpu` (`OP_COLOR`), which owns the
  card. Another client gets `EACCES`.
- `drmModeObjectSetProperty` on the CRTC is an atomic commit of its own and returns after the vblank
  it lands at: 14.7 to 16.1 ms measured on the Show 8, once per change. The frames after cost the same
  as before (16.4-16.6 ms flip-paced, 0.30-0.36 ms of echod's CPU per frame, with and without a tint),
  because the CCORR is in the scanout path, not the GPU's.
- **Clearing a property does not reset the block.** `mtk_ccorr_ctm_set` and `mtk_gamma_set` return
  early on a NULL blob, so the last matrix or table stays in the hardware. The helper therefore sets
  the identity instead of clearing, and resets either property that a previous process left set
  when it opens the display. The MT8183's CCORR at the identity is exact (1.0 is 1024 there).
  GAMMA is different: once a table is set it is never in relay again until a reboot, so nothing sets
  one. A white-point shift needs only the matrix.
- Coefficients: `drm_color_ctm_s31_32_to_qm_n(v, 2, matrix_bits)`, 10 bits on the MT8183
  (`mediatek,mt8183-disp-ccorr`). A warm matrix diag(1, 0.785, 0.521) read back 0x400, 0x323, 0x215.

## Night warmth (what shipped)

echod had no full-screen software tint to move: its `washOver` calls are local alpha overlays (shades
under sheets, a ground under text on a photo, a leaf's shadow on the flip clock). A CRTC-wide matrix
cannot do those, so they stay on the cores. What the pipeline can do for free is a whole-panel white
point, so that is the feature: **Night warmth** (Home Assistant switch `screen_night_warmth`, off by
default, so nothing changes until it is turned on). Through the night, the CTM shifts the panel's
white to a 3400 K black body's (Kim et al. Planckian locus, normalized to 6500 K, taken through gamma
2.2 because the CCORR works on encoded values): diag(1, 0.785, 0.521). Every gain is at most 1 and
there are no cross terms, so nothing clips and no hue is pushed out of gamut; colors just lose blue
and some green, as under a lamp. It is not applied under the light before an alarm, whose colors
are deliberate.

- Path: `display.nightTint` → `screen.Device.SetTint` → `gpu.Display.SetColor` → `OP_COLOR` →
  CRTC `CTM`. A helper that restarts gets the tint again with the display.
- Fallback (framebuffer path, or the GPU path lost): a per-channel table over a copy of each frame,
  8.6 ms of CPU per 1280x800 frame on the Show 8's cores. The canvas is never tinted in place, since
  echod keeps and re-presents it. On the GPU path without a CTM there is no fallback; it logs and
  shows untinted.

## Content-adaptive backlight (Adaptive backlight)

The idea: on dark content, turn the backlight down and draw every pixel brighter by as much, so the
panel shows the same picture for less light. An LCD's light is backlight times transmission, and the
transmission follows the code to the power 2.2, so a backlight at raw/asked and a gain of
(asked/raw)^(1/2.2) on every code cancel. It is lossless while the brightest red, green or blue value
on the screen times the gain stays at 255 or under.

### The AAL, read but not used

Mainline's AAL driver only writes the frame size and `AAL_EN`; the block stays in relay. The register
map is in MediaTek's GPL display drivers (the MT8163 `ddp_aal.c` of a vendor tablet tree, and the
`video/common/aal30` driver of MediaTek-Labs' common-kernel-4.19, which the MT6771/MT8183 generation
uses); both agree:

| Offset | Register | Use |
|---|---|---|
| 0x000 | `AAL_EN` | engine clock on |
| 0x008 / 0x00c | `INTEN` / `INTSTA` | bit 1: end of frame, when the histogram is ready |
| 0x020 | `CFG` | bit 0 relay; bits 1-2 histogram and engine on (vendor writes 0x6 with relay cleared) |
| 0x030 | `SIZE` | w << 16 \| h |
| 0x20c / 0x214 | `CABC_00` / `CABC_02` | bit 31 / bit 26 force, low 10 bits the CABC pixel gain |
| 0x224..0x2a4 | `STATUS_00..32` | the max(R,G,B) histogram: 33 bins of pixel counts |
| 0x354.. | `DRE_GAIN_FILTER`, `DRE_FLT_FORCE(0..10)` | the DRE curve (local contrast), from Android's AAL service |
| 0x3b0 (MT8163) / 0x3b4 | `DRE_MAPPING_00` | bit 4 DRE map bypass |

Read on the Show 8 (read only, `/dev/mem`): `EN` 1, `CFG` 0x1 (relay), `SIZE` 0x03200500
(800 x 1280), `INTSTA` 3 (never cleared), every histogram bin 0 except `STATUS_32` = 0x3fffff, which
looks like a reset value: no histogram runs in relay. Turning the histogram on, with or without relay,
was not tried: writing the live unit's registers from userspace was refused in this session, and the
proper place for it is a kernel patch anyway (set `CFG` bits 1-2 in `mtk_aal_config`, read the 33
words at end of frame, expose them read-only).

The AAL was not used because echod already has what it would give, exactly and for free:

- echod draws the canvas, so its brightest value is known to the code value. The AAL's bins are 8
  codes wide, so the theme's 233 would have to be taken as 239: 13% instead of 18% on the clock.
- Its only gain over the canvas is video, whose frames are drawn by the GPU, not the canvas. Video
  reaches 255 in almost every scene, so it gives almost nothing.
- Lifting the pixels through the AAL means running its engine out of relay, with its DRE curve and
  CABC gain at whatever reset left them. Without a way to read the panel's output, an engine that
  changes the picture slightly could not be ruled out. The GPU's gain can be read back and checked.
- A CTM gain over 1 (CCORR, S2.10) would work too, but each change is its own blocking commit a vblank
  apart from the page flip, so a gain and its frame would land on different vblanks.

### What was built

- `techo5-gpu` (`OP_LIFT`): every present draws its pixels with a gain, in the same frame as the
  content. The 8-bit framebuffer would skip a code every 1/(gain-1) codes, so a 4x4 ordered dither
  puts each pixel on one of its two neighboring codes, averaging to the exact value. At gain 1 the
  shader returns the pixel untouched. `OP_PRESENT` a[0] bit 0 has the helper measure the canvas's
  brightest R, G or B value first (NEON, 0.57 ms for 1280x800 on the Show 8's A73s, about 0.9 ms
  for the Show 5's 960x480), and only while the lift is on. `OP_LIFT` with redraw draws the last canvas
  again from its texture, so a ramp can step with nothing new drawn.
- echod (`hardware/screen/lift.go`): the backlight level asked for (relight) goes through the lift,
  which writes raw = ceil(asked * (held/255)^2.2) (never under half) and draws with the gain matching
  that raw level exactly. The backlight is written right after the page flip of the frame that carries
  the new gain, so the two change within a millisecond. Moves are one raw level at a time: down at 4%
  of the asked level a second, after the content has stayed that dark for 10 s (peak hold, so a
  blinking element does not pump it); up at 100% a second as soon as brighter content needs the room.
  What appears brighter than the room left for it is held at 255 for those frames (a sheet sliding in:
  9 frames, 0.14 s in the test). Nothing already on the panel ever clips. Video and camera frames are
  not measured, so the lift goes back to none under them, each frame taking a step. Below raw 40 of
  1023 (the night's levels) it stays off: steps that coarse are too large, and the LED is least
  linear in its duty there. It is logged hourly as `screen: adaptive backlight over the last hour`
  (`saved_pct`, `asked_mean`).
- Home Assistant switch `screen_adaptive_backlight`, "Adaptive backlight", off by default. With it
  off, nothing is measured and the gain is 1: the panel gets the same pixels and backlight as before.

### Measured

Headroom over the scenes the display tests render (`SHOW_PREVIEW`, current theme, 307 at 1280x800
and 388 at the Show 5's sizes): the mean lossless saving is 7.0% of the backlight on the Show 8 and
10.3% on the Show 5.

| Screen | Brightest | Saving |
|---|---|---|
| Clock, cloudy or rainy, every style; photo saver; clock at night | 233 (the theme's text) | 18% |
| Clock with the sun icon (222 pixels of (247, 208, 122) and (242, 177, 60)) | 247 | 6.8% |
| Dashboard | 247-248 | 5.9-6.8% |
| Now playing, music strip | 249 | 5.1% |
| Settings pages | 250 | 4.3% |
| Calls, alerts, camera, radar, white photos | 255 | 0 |
| Video | not measured | 0 |

The night clock's 18% does not happen in use: at night the backlight is 1-3 of 255, under the lift's
floor. By day the panel sits at about 25% (raw 156 of 1023 on the user's Show 8). There, 7-18% of the
backlight is 1-3% of its full-duty power. For an 8" panel that is probably 1.5-2.5 W at full duty
(not measured: the units have no power meter), so roughly 20-60 mW, about 1% of what the device draws.
The light sensor cannot measure the panel (it is shielded from it, above). The hourly log line will
give the real share once the switch is on.

Checked:

- `screen` tests: for every asked level from 40 to 1023 and every brightest value, the target never
  clips and the gain cancels the backlight exactly. Ramp timing, hold, the sheet case, off, the
  night, an old helper (`ENOSYS`: lift off for good, backlight as asked), video.
- The helper's own shader source (compiled from `techo5-gpu.c` into a harness), on llvmpipe and on both
  units' GPUs through the render node, next to the running display (Mali-G72 MC3 and Mali-T720 MC2,
  panfrost). Gain 1 is bit-identical. At gains 1.0001, 1.032, 1.094 and 1.37 every pixel lands on the
  floor or ceiling of value times gain. The 4x4 mean is within 1/32 code. A gradient stays monotonic.
  llvmpipe's 8-bit samples are a little under k/255, so the shader rounds each value back to its integer
  code before the gain; without that, a code could come out one low.

Not checked, so the switch stays off by default: the feature running on a unit (installing the test
slot was refused in this session; the tarballs are with the hand-over note), the panel by eye, and
the shader's frame-time cost on the Malis (a handful of ALU operations a pixel, under a frame paced at
16.7 ms). The compensation assumes the panel follows gamma 2.2 and the LED is linear in PWM duty
(`pwm-backlight` with a linear 0-1023 table, 25 kHz on the Show 8 and 2 kHz on the Show 5). At this
gain, a panel at gamma 2.0 or 2.4 would be off by at most 1.8% at an 18% cut (0.6% at 7%), and the
change is spread over seconds.
