# MT8163 display color blocks (CCORR, GAMMA, AAL)

Date: 2026-10-19. The MT8183 side, the night-warmth feature, the AAL's registers and the adaptive
backlight are in `../../athena/research/display-color.md`; this note has what differs on the Show 5.

## Pipeline

The main path is ovl0, color0, ccorr, aal0, gamma, dither0, rdma0, ufoe, dsi0
(`mt8163_mtk_ddp_main`, patch 0000). The CRTC has `CTM` (from the CCORR) and `GAMMA_LUT` with a
`GAMMA_LUT_SIZE` of 512 (from `mt8163_gamma_driver_data`). Patch 0012 already sends a NULL
`GAMMA_LUT` to relay on this SoC, which mainline doesn't do on others.

Registers at boot (`/dev/mem`; CCORR at 0x1400d000, AAL 0x1400e000, GAMMA 0x1400f000):

| Block | `CFG` (+0x20) | Coefficients (+0x80..0x90) |
|---|---|---|
| CCORR | 0x2 (engine on, not relay) | 0x04000000, 0, 0x04000000, 0, 0x04000000 |
| AAL | 0x1 (relay) | |
| GAMMA | 0x1 (relay) | |

## CCORR coefficient scale: 10 bits, as on the MT8183

The DT gave the CCORR the fallback `mediatek,mt8192-disp-ccorr`, which has `matrix_bits = 11`. With
that fallback, 1.0 becomes 0x800. Three things say this SoC's 1.0 is 1024:

- The identity the block holds at boot is 0x400 on the diagonal, with the engine on and not in relay.
  At 11 bits that would be a matrix of 0.5: a panel at half its level in every channel, which it is
  not.
- Mainline gives the MT8167, the same generation of MediaTek display, the `mt8183-disp-ccorr`
  fallback (10 bits).
- The vendor's MT6735-family `ddp_ccorr` code, from which the MT8163's comes, uses 1024 as its
  identity.

If the fallback stayed, a CTM of 1.0 would write 0x800, which a 12-bit S2.10 field reads as -2.0:
the channel would go black. Patch 0000 now gives `mediatek,mt8183-disp-ccorr` as the fallback, in
both the DT and the binding.

Test boot (2026-10-19): the test image is the boot partition's own image (the MDP build) with only
this compatible string changed in its DTB. 22 bytes differ, including the header hash. Booted
through the one-shot slot, the node reads `mediatek,mt8163-disp-ccorr mediatek,mt8183-disp-ccorr`.
`OP_COLOR` with diag(1, 0.785, 0.521) wrote 0x400, 0x323 and 0x215, the same as on the MT8183 and
on the boot identity's scale. The commit took 15.2 ms (one vblank). Frames cost the same with and
without the tint: 16.6-16.7 ms flip-paced, 0.56 ms of echod's CPU per frame on the Mali-T720 path.
A helper that restarted got the tint again, and the identity came back exactly.
The old fallback was deliberately not tried with a tint: by the same arithmetic it would write 0x800
for 1.0.

Under the old DTB, a requested 1.0 would be written as 0x800. So `techo5-gpu` reads the CCORR node's
compatible and offers no matrix when it is `mt8163-disp-ccorr` with `mt8192-disp-ccorr`. Tested by
bind-mounting the old string over `/proc/device-tree/.../compatible`: the helper said so and
declined, the CCORR stayed at the identity, and echod logged it and showed the screen untinted.
Night warmth does nothing on the Show 5 until the boot partition has the new DTB.

## Adaptive backlight on the Show 5

The same code as on the Show 8 (`techo5-gpu`'s `OP_LIFT`, echod's `screen/lift.go`): the gain is
drawn by the GPU, so it needs neither the CCORR nor the DTB fix above. The Mali-T720 runs the lift's
shader exactly as the Mali-G72 does (bit-identical at gain 1, every pixel on a neighboring code above
it; checked through the render node next to the running display). Measuring the canvas's brightest
value costs about 0.9 ms a frame on the A53s for the 960x480 canvas (1.99 ms for 1280x800). The
backlight is a linear `pwm-backlight` of 0-1023 at 2 kHz. The Show 5 scenes save 10.3% on average,
against 7.0% on the Show 8, because more of its pages stop at the theme's 233.

The AAL is at 0x1400e000. The vendor's MT8163 driver has the same layout as the MT8183's (histogram
at +0x224, 33 bins; `DRE_MAPPING_00` at +0x3b0). It was not read on this unit.

## The display that never starts at boot (not the CCORR)

Seen 2026-10-19 on kernel #36 with the .20 rootfs: at the first modeset (techo5-gpu's
`drmModeSetCrtc`, about 20 s into boot), "[CRTC:57:crtc-0] vblank wait timed out", then echod "the
GPU failed ... device or resource busy". The OVL's interrupt count stays at 2 for the rest of the
boot. Later commits each wait out "flip_done timed out" (10 s), and the disable path logs
"polling dsi wait not busy timeout!": the DSI engine is stuck busy, and the pipeline above it with
it. (The DSI's own interrupt count is 3 on every boot, good or bad. It is not a frame counter here:
the MT8163's driver data has `no_vm_done_irq`.)

It is not the CCORR fallback. That fallback only selects `matrix_bits`, used only by
`mtk_ccorr_ctm_set`, which runs only when a CTM is set, and nothing sets one at boot. The same failure
is in echod's log on Oct 18 at 20:59 (v1.2.19-mainline.11), hours before the fallback changed. It
happened on #36 with the new fallback (2 of 18 boots) and with the DSI patch described below.

Once stuck, nothing from userspace brings it back. A DPMS off/on (flipcheck in the session scratchpad)
re-enabled in 38 ms, against 186 ms with the panel's power-on, and no flip completed. When the
pipeline is healthy, the same DPMS cycle worked 100 times out of 100.

What it is not: the DSI being switched mid-frame. A patch that stopped the bootloader's video
cleanly before the first enable (stop, wait not busy, command mode, reset, in
`mtk_dsi_bridge_mode_set`; the DSI was idle within the bound every time) still stalled once in 41
boots. Without it the rate was about 3 in 38, a difference within chance, so the patch was dropped.
An earlier version, stopping it in `mtk_dsi_poweron`, stalled 1 in 20.

Where it is: that patch's log line showed the first modeset is fbdev's, at 2.8 s, while
mediatek-drm binds, not echod's at 20 s. In good boots the OVL counts from 2.8 s (about 2,300 by
38 s); in every stalled boot it stays at 2-3. So the pipeline fails to start at the boot-time enable,
and echod's commit is only where it shows. That is when the other multimedia drivers probe
(camera with MDP route A on larb0 and mmsys, decoder, encoder, JPEG). But the stall is also in the
log from before route A (mainline.11), so it is older than that. Next steps: log the OVL, RDMA and
mutex status registers right after fbdev's enable on a stalled boot, and try deferring the fbdev
enable (or the camera's probe) past the others. A stall shows up about one boot in ten to thirty,
so each variant needs 30-40 boots.

The checks per boot: through testslot.sh, then 30 s in, OVL interrupts rising (about 60 a second),
no vblank timeout, no "GPU failed". dmesg of the stalled boots is kept on the unit as
/data/dmesg-vblank-*.txt, /data/dmesg-stuck*.txt, /data/dmesg-D-*.txt and /data/dmesg-E-*.txt.

techo5-gpu now logs "no vblank after the first modeset" when the vblank counter does not move in the
60 ms after its first `drmModeSetCrtc`. It tries no recovery, since none works from userspace. It also
keeps the page-flip pending flag in static storage, so an event that arrives late can't write to
a stack frame that is gone.
