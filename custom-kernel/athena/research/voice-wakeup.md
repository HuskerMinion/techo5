# Voice wakeup (VOW) on the Echo Show 8 2nd gen (athena) and Echo Show 5 2nd gen (cronos)

Research note, 2026-10-18. Read-only: nothing was written to a device, and no firmware or kernel was
built for this.

## Verdict

**Neither board can usefully run a low-power voice or wake-word detector in hardware, with or without
Amazon's firmware.** The low-power blocks either don't exist or aren't wired to the microphones.

- **athena (MT8183 + MT6358).** The silicon has the parts. The MT6358 has a VOW front end, and the
  SCP has a VIF FIFO that receives VOW audio. Mainline also has the host side (ChromeOS "Wake on
  Voice": `mt6358` phase-2 switch, `cros_ec_codec` WoV, the kukui machine link). It is moot here for
  three reasons:
  1. The four microphones go to a **TLV320ADC5140 on I2S2**, not into the MT6358's ADC inputs, and
     MT6358 VOW listens only to its own ADC/DMIC inputs.
  2. Amazon never used VOW: the stock kernel was built with `CONFIG_MTK_VOW_SUPPORT` and
     `CONFIG_MTK_TINYSYS_SCP_SUPPORT` off, and the vendor partition says `MTK_VOW_SUPPORT = no`.
  3. The SCP firmware we run (linux-firmware kukui_scp) has no WoV code. ChromeOS's hotword engine is
     a private library; the open EC tree has only a stub for it.

  The RT5514 "DSP mic codec" in the stock device tree is an inherited reference-board node. No driver
  for it is built into Amazon's kernel. Confidence: high that VOW is moot. Medium-high that no
  microphone reaches the MT6358 ADC (one cheap experiment would settle it; see below).
- **cronos (MT8163 + MT6323).** No VOW anywhere. MT8163 has no SCP (its MD32 micro-DSP does sensor
  hub, power and thermal jobs, no audio), the MT6323 PMIC codec has no VOW block, and VOW was off in
  the 4.9 config too. The two microphones go
  TLV320AIC3101 → iCE40 FPGA → SPI, which the kernel polls, so the PMIC codec isn't in the path at
  all. Confidence: high.
- **What to do instead:** keep the always-on path on the CPU and make it cheaper. On athena that
  means confirming the 16 kHz mic capture is actually in use, and skipping the filters on silent
  playback (see "CPU-side alternative"). That attacks the ~75% of idle CPU that is resampling and
  conversion. A hardware VAD could at best have saved the ~8% that is the wake word itself, plus
  whatever the capture path costs.

## 1. athena: does MT8183/MT6358 have VOW, and what does mainline support?

### The hardware and how ChromeOS uses it

MT6358 VOW has two stages. Phase 1 is a PMIC-side low-power detector, configured through the
`AFE_VOW_*` registers. Phase 2 streams 16 kHz VOW audio out of the PMIC on dedicated pins
(`VOW_DAT_MISO` / `VOW_CLK_MISO`) into the SCP's VIF/RXIF FIFO. The SCP runs the keyword model and
wakes the AP.

On ChromeOS kukui the SCP is a Chromium-EC build (kukui_scp), and the AP sees the result as a
`google,cros-ec-codec` "Wake on Voice" PCM.

Mainline 7.2.11 (local tree `linux-7.2.11`) has these pieces:

| Piece | Where | What it does |
|---|---|---|
| MT6358 phase 2 | `sound/soc/codecs/mt6358.c:371-430` (`mt6358_enable_wov_phase2` / `_disable_`) | Fixed register writes: AUDENC analog, `AFE_VOW_CFG0..5`, `AFE_VOW_HPF_CFG0`, `AFE_VOW_TOP` |
| MT6358 control | `sound/soc/codecs/mt6358.c:527` | `"Wake-on-Voice Phase2 Switch"` |
| MT6358 phase 1 | not in mainline | Only phase 2 is upstream; no PMIC-side detector or interrupt |
| Machine link | `sound/soc/mediatek/mt8183/mt8183-mt6358-ts3a227-max98357.c:241-244, 345-377, 503-510, 721-733` | `"Wake on Voice"` FE that selects a `wov` pinctrl state and binds `mediatek,ec-codec` |
| EC codec | `sound/soc/codecs/cros_ec_codec.c:52-68, 393-455` | WoV state, a 64000-byte ring, shared memory for the language model and audio, `EC_CMD_EC_CODEC_WOV` (`include/linux/platform_data/cros_ec_commands.h:4935-4945`) |
| Device trees | none in `arch/arm64/boot/dts/mediatek/` | No upstream MT8183 DTS sets `mediatek,ec-codec` or a `wov` pin state, so the path is untested upstream |

The SCP side is open source in the Chromium EC repo (BSD; GitHub mirror `coreboot/chrome-ec`):

- [`chip/mt_scp/mt818x/audio_codec_wov.c`](https://github.com/coreboot/chrome-ec/blob/main/chip/mt_scp/mt818x/audio_codec_wov.c)
  drives the VIF FIFO (`SCP_AUDIO_BASE = SCP_CFG_BASE + 0x1000`, `RXIF_RGDL2_DMIC_16K`,
  `SCP_IRQ_MAD_FIFO`).
- [`common/audio_codec_wov.c`](https://github.com/coreboot/chrome-ec/blob/main/common/audio_codec_wov.c)
  is the ring buffer and host commands.
- The detector itself is
  [`common/hotword_dsp_api.c`](https://github.com/coreboot/chrome-ec/blob/main/common/hotword_dsp_api.c),
  a stub: `GoogleHotwordDspProcess` returns 0.
- [`chip/mt_scp/mt818x/build.mk`](https://github.com/coreboot/chrome-ec/blob/main/chip/mt_scp/mt818x/build.mk)
  links the real engine only if `private/libkukui_scp_google_hotword_dsp_api.a` exists, so it isn't
  public.
- [`board/kukui_scp/ec.tasklist`](https://github.com/coreboot/chrome-ec/blob/main/board/kukui_scp/ec.tasklist)
  has no WOV task (only VDEC/VENC/MDP/FD/DIP/ISP, host commands, console).

The firmware we ship confirms this. linux-firmware `mediatek/mt8183/scp.img` (`kukui_scp_v2.0.13324`,
the one in the athena ramdisk) has no `wov`, `hotword`, `vow`, `codec` or `audio` strings.

Upstream patch series: "ASoC: mediatek: mt8183-mt6358-ts3a227-max98357: support WoV", Tzung-Bi Shih,
2019 ([lore](https://lore.kernel.org/all/20191011151406.GA18450@bogus/t/)).

### What Amazon's Fire OS does on athena

From the Show 8 partition backup, read in place. The stock kernel was decompressed to scratch, and its
embedded config was extracted and then deleted.

- **Kernel:** Linux 4.4.146, with these options in the embedded IKCONFIG:
  - `CONFIG_SND_SOC_MT8183_ATHENA_MACH=y`
  - `CONFIG_MTK_SND_SOC_CODEC="mt6358"`
  - `CONFIG_SND_SOC_TLV320ADC5140=y`
  - `# CONFIG_MTK_VOW_SUPPORT is not set`
  - `# CONFIG_MTK_VOW_BARGE_IN_SUPPORT is not set`
  - `# CONFIG_MTK_TINYSYS_SCP_SUPPORT is not set` (Fire OS doesn't run the SCP at all)
  - `# CONFIG_SND_SOC_RT551X is not set` (and its `_SPI` variant)
- **Kernel strings:** the MT6358 codec's VOW register names (`AFE_VOW_*`), `Audio_VOW_*` controls and
  `vow_{dat,clk}_miso_{on,off}` pin states are compiled in. That's MediaTek's generic codec driver,
  not a feature Amazon turned on. There is no `/dev/vow` driver and no `rt5514` string.
- **Vendor partition:** the feature list says `MTK_VOW_SUPPORT = no`. The generic MTK HAL still
  carries `VOW_AudioParam.xml` and `/dev/vow` references.
- **Device tree:** the stock DTBs in the boot image carry `audio-codec@57 { compatible =
  "realtek,rt5514" }` on i2c1. That node is in our DTS as `status = "disabled"`
  (`tools/linux/athena/kernel/dts/mt8183-amazon-athena.dts:724-743`). With no driver in the kernel,
  it is a reference-design leftover. Nobody has checked that the part is fitted (an `i2cdetect -y 1`
  at 0x57 would). Even if it were fitted, its DSP firmware (Google's hotword for the RT5514) isn't
  ours to use, and nothing says any microphone is wired to it.
- **No athena GPL kernel source is in the repo.** The evidence above is the binary kernel's config
  and strings.

### Could we build it ourselves?

Only if a microphone reaches the MT6358. If one did, the minimal version would be:

1. Build kukui_scp from EC source with `CONFIG_AUDIO_CODEC_WOV`. Put an energy or VAD detector behind
   the `GoogleHotwordDsp*` hooks, and re-apply our vdec-proxy patch (`patches/scp-vdec-proxy-fw.py`
   patches the binary today, not source).
2. Add `cros-ec-rpmsg` and `google,cros-ec-codec` nodes, plus the `wov` pinctrl.
3. Add the Wake on Voice FE, an MT6358 ADDA UL BE and a WoV pin state to `src/mt8183-athena.c`.
4. Write the phase-1 configuration, which isn't upstream.

That's about 3-6 weeks, high risk, and it would hear one PMIC microphone, not the beamformed array.

## 2. athena: does the microphone path go through the PMIC codec?

**No, as far as all evidence goes.**

- **Mainline:** the card has the AFE as clock master and two codecs: the TAS5805M on I2S1 and the
  TLV320ADC5140 on I2S2, carrying ch1/ch2 on DI and ch3/ch4 on DI2
  (`tools/linux/athena/kernel/src/mt8183-athena.c:9-12, 116-124`;
  DTS `:844-856`, `:896-903`). The MT6358 codec isn't on the card.
- **Fire OS:** it records the same converter: `I2S2ADC2_Capture`, 4 ch at 96 kHz, merged with
  `DL1_AWB_Record` (`echod/internal/hardware/mic/athena.go:19-35`).
- **The one link to the PMIC:** Fire OS's PcmMerge provider also switches on the MT6358 ADC path
  (`Audio_ADC_1/2_Switch On`, `Audio_Preamp1_Switch IN_ADC1`). Our best reading is that this powers
  the PMIC MICBIAS that biases the analog mics feeding the ADC5140; it isn't a capture
  (`echod/internal/hardware/mic/athena_bias.go:12-41`). Even if so, MICBIAS only biases the mics.
  VOW needs the mic's signal on the PMIC's AIN pins.
- **The ADC5140 itself has no VAD.** Its `INT_CFG`/`INT_MASK0` interrupts are for faults and clock
  errors (`sound/soc/codecs/tlv320adcx140.h:50-51`). TI's VAD appears on the PCMD3140 and the
  2-channel ADC5120/6120 family, not this part
  ([TI product page](https://www.ti.com/product/TLV320ADC5140)).

**Open item:** one experiment would turn "medium-high" into certain. Under Fire OS, open the MT6358
ADDA UL capture (the `UL1`/ADDA device on `mt-snd-card`) with `builtin_Mic_SingleMic` applied, and
look for room sound. Or add an MT6358 "Primary Codec" BE to the mainline card and do the same. Silence
there closes the VOW question for good.

## 3. cronos (MT8163 + MT6323)

- **Mic path:** two microphones → TLV320AIC3101 → iCE40 FPGA → SPI. Amazon's `amzn-mt-spi-pcm` is
  ported as `custom-kernel/cronos/patches/amazon-spi-pcm.c:1-14`. A high-priority work item polls
  whole 256-frame "dough" frames every few ms; there is no FPGA→SoC interrupt in the driver. Capture
  is already 4 ch S24_3LE at 16 kHz (`echod/internal/hardware/mic/device_cronos.go:10-16`;
  `custom-kernel/cronos/README.md` sound section). The PMIC codec isn't in the path.
- **VOW support:**
  - The 4.9 config dump (`docs/dumps/cronos-lineage-kernel-4.9.337.config`, a LineageOS build of
    Amazon's source) has `# CONFIG_MTK_VOW_SUPPORT is not set`, `# CONFIG_MTK_TINYSYS_SCP_SUPPORT is
    not set` (lines 1520 and 1615) and `CONFIG_SND_SOC_MT8163_AMZN=y`.
  - Amazon's 4.9 tree (`git show HEAD:` in `sources/amazon-kernel`) carries MediaTek's generic BSP
    VOW driver, `drivers/misc/mediatek/vow/` (Kconfig `MTK_VOW_SUPPORT`, default n, "cooperate with
    SCP VOW driver ... Using IPI"). It has no `mt8163` platform directory and depends on the
    tinysys SCP, which is off. The tree's codec VOW code is the MT6355's, not the MT6323's.
  - MT8163 has no SCP, but it does have an **MD32** micro-DSP (`CONFIG_MD32_SUPPORT=y`,
    `drivers/misc/mediatek/md32/mt8163/`, firmware `md32_p.bin`/`md32_d.bin`). Its Kconfig names
    sensor hub, deep power down, thermal and DVFS as its jobs, and there is no audio or VOW in its
    driver. Even a custom MD32 program couldn't hear the microphones, which reach the SoC only as SPI
    frames the AP polls.
  - The MT6323's audio codec predates MediaTek's VOW block (MT6351/MT6355/MT6358 generation).
    Medium-high confidence on the silicon point; there's no datasheet in the repo.
- **The only exotic option:** a custom iCE40 bitstream (the open IceStorm/nextpnr toolchain exists)
  that computes a level or VAD flag into the status frame's reserved byte (`rsvd`, wire byte 6).
  The kernel would still have to poll SPI, the FPGA only buffers 16 ms (so the wake word's onset
  would be lost without a ring on the AP), and the FPGA's pinout and clocking are unknown. Weeks of
  work for little gain. Not recommended.

## 4. Minimal useful version, effort, saving — and the CPU-side alternative

**VOW route:** not feasible on either board as wired (sections 2 and 3). Even in the best case on
athena (a PMIC mic exists), it costs 3-6 weeks.

That best case saves at most the wake-word share (~8% of the ~48% idle core), plus the capture path
while the room is quiet. It saves nothing while echod needs AEC, levels or the beamformer for other
features. In watts that's at most a few hundred mW (estimate) on a mains-powered device that never
suspends.

**CPU-side alternative (athena, recommended)**, since the measured cost is resampling and
conversion, not the network:

1. **Make sure the 16 kHz microphone path is the one running.**
   - The mainline profile already opens the ADC5140 at `Rate` first, with 96 kHz as the fallback
     (`echod/internal/hardware/mic/athena.go:212`). The converter's own decimation filters then make
     16 kHz, and no `Decimator` runs.
   - The echo reference is the software `echoref` fed with the 16 kHz period before upsampling
     (`echod/internal/hardware/speaker/speaker.go:742`, `echod/internal/lib/echoref/echoref.go:99`).
   - If the profile shows `Decimator.Push` on top, the unit is on the 96 kHz fallback, or the profile
     came from the Fire OS kernel (96 kHz mics and a 96 kHz AWB reference through 192-tap
     decimators: `athena.go:48-56, 86-88`). Check the mic-open log line for the rate actually used.
   - Fixing a fallback removes the mic decimators and cuts `convert`'s input 6x (4 ch × 96 kHz →
     4 ch × 16 kHz).
2. **Don't filter silence.**
   - At idle the speaker still upsamples silent periods to 96 kHz (`Upsampler.S16LE`, 48 taps per
     phase, 2 ch: `speaker.go:58, 694-697`; `paths_cronos.go:147`). The reference decimator does
     the same for zeros.
   - When a period and the filter's history are all zero, write zeros (or keep a pre-rendered silent
     96 kHz period) and skip the dot products. The same goes for `echoref`'s decimator. Then nothing
     is computed for the reference until playback starts.
   - A few hours' work. It removes most of `Upsampler.S16LE` and `echoref.Push` from the idle
     profile.
3. **Optional energy gate** in front of microWakeWord's front end, for long stretches below the
   room floor. Worth at most the ~8% network share, with a risk of clipping word onsets. Low
   priority.

Estimate: 1 + 2 should take athena's idle from ~48% of a core to roughly 10-15%. That's the wake
word, AEC and beamformer that remain, and it's a projection, not a measurement. cronos already
captures at 16 kHz and plays at `Rate`, so only item 3 applies to it.

## Sources

- Repo: `tools/linux/athena/kernel/dts/mt8183-amazon-athena.dts:724-743, 844-856, 896-918`;
  `tools/linux/athena/kernel/src/mt8183-athena.c:9-12, 116-124`;
  `echod/internal/hardware/mic/athena.go:19-56, 86-88, 150-212`;
  `echod/internal/hardware/mic/athena_bias.go:12-41`;
  `echod/internal/hardware/mic/device_cronos.go:10-16`;
  `echod/internal/hardware/speaker/speaker.go:56-58, 694-742`;
  `echod/internal/lib/echoref/echoref.go:1-37, 99-110`;
  `custom-kernel/cronos/patches/amazon-spi-pcm.c:1-60`;
  `docs/dumps/cronos-lineage-kernel-4.9.337.config:1501, 1520, 1615, 2902, 2988`;
  `custom-kernel/cronos/sources/amazon-kernel` at HEAD: `drivers/misc/mediatek/vow/{Kconfig,Makefile}`,
  `drivers/misc/mediatek/md32/{Kconfig,mt8163/md32_helper.c:43-44}`,
  `drivers/misc/mediatek/Makefile:75-76, 129, 138`.
- Show 8 partition backup (boot, vendor), read in place: the stock 4.4.146 kernel's embedded config
  and strings, the vendor feature list (`MTK_VOW_SUPPORT = no`), and the stock DTB's `realtek,rt5514`
  node.
- Mainline 7.2.11: `sound/soc/codecs/mt6358.c:371-430, 527`;
  `sound/soc/mediatek/mt8183/mt8183-mt6358-ts3a227-max98357.c:241-244, 345-377, 503-510, 721-733`;
  `sound/soc/codecs/cros_ec_codec.c:52-68, 393-455`;
  `include/linux/platform_data/cros_ec_commands.h:4935-4945`;
  `sound/soc/codecs/tlv320adcx140.h:50-51`.
- linux-firmware `mediatek/mt8183/scp.img` (kukui_scp v2.0.13324): no WoV strings.
- Chromium EC: <https://github.com/coreboot/chrome-ec> (`chip/mt_scp/mt818x/audio_codec_wov.c`,
  `chip/mt_scp/mt818x/build.mk`, `chip/mt_scp/mt818x/registers.h:202-232`,
  `common/audio_codec_wov.c`, `common/hotword_dsp_api.c`, `board/kukui_scp/ec.tasklist`).
- Upstream WoV series: <https://lore.kernel.org/all/20191011151406.GA18450@bogus/t/>.
- TI TLV320ADC5140: <https://www.ti.com/product/TLV320ADC5140>.
