# Hardware survey: unchecked blocks on Show 8 (MT8183) and Show 5 (MT8163)

Date: 2026-10-19. Both units on mainline 7.2.11. Everything here is read-only (sysfs, /proc, dmesg, DT)
plus one 90 s ping test on the Show 8 with Wi-Fi power save toggled and restored. Units given back as found.

Things *not* verified: behavior under GPU load, WoWLAN on the Show 5 (out-of-tree driver), and anything
that would need poking unclocked registers (deliberately not done).

## Verdict table

| Block | Show 8 (MT8183) | Show 5 (MT8163) | Worth doing? |
|---|---|---|---|
| mtk-rng / hwrng | No RNG in the DT, no `/dev/hwrng` device (`rng_current` = none). `smccc_trng` driver is built but nothing binds (firmware exposes no TRNG). | `1020f000.rng` bound, active, feeds the pool, ~210 KB/s. | No action. |
| AES/SHA engine | None in DT, none in kernel config (no `CRYPTO_DEV_MEDIATEK`). | Same, none. | No (nothing to enable). |
| ARMv8 Crypto Ext. | `aes pmull sha1 sha2 crc32` on A73+A53. Kernel registers `*-aes-ce`, `gcm-aes-ce`. | Same flags on A53 pair. | Already in use. |
| Wi-Fi power save | `mt7663s`, PS **on**; no measurable latency cost (below). | Vendor out-of-tree `mt76x8_wlan`, PS **off** (iw reports off). | Leave alone. |
| Wi-Fi checksum/TSO/beacon filter | mt76 mac80211: SW-visible offload is limited to what mt76 advertises; no `ethtool` on image so not enumerated. | Vendor driver, not inspectable. | Low; see ranked list. |
| WoWLAN | iw lists the field but no usable triggers shown; no suspend use case on a wall-powered device. | Same. | No. |
| Bluetooth | `btmtksdio` bound (MT7663 BT on SDIO). | `btmtksdio` bound (0x7668). | Not a perf item. |
| GPU devfreq (panfrost) | **On**: `simple_ondemand`, 16 OPPs 300-800 MHz, idle at 300 MHz (min), 1 transition since boot. | **On**: `simple_ondemand`, 3 OPPs 299/416/520 MHz, idle at 299 MHz, 1 transition (to 416) since boot. | Already working. |
| JPEG decode | `mtk-jpeg` driver is built but only the *encoder* node exists (`17030000.jpeg-encoder`, `/dev/video0`). No decoder node in DT. | Same, encoder only (`17003000`). | See below. |
| Unused DT | See last section. | See last section. | Mostly no. |

## 1. Hardware crypto and RNG

- **Show 5**: `mtk_rng 1020f000.rng` registers at 0.43 s, `crng init done` at the same moment. `/dev/hwrng` gives ~210 KB/s,
  `/dev/urandom` ~65 MB/s. The hwrng core thread tops up the pool in the background. Nothing to do; no `rngd` needed.
- **Show 8**: no hardware RNG exists in the DT. `crng init` still completes at 0.63 s (jitter/timer), so there is no
  boot-entropy starvation problem. `CONFIG_HW_RANDOM_ARM_SMCCC_TRNG` and `OPTEE` are on but nothing probes
  (no TRNG in the TF-A/OP-TEE we have). Nothing to gain.
- **Crypto engine**: mainline has no MediaTek AES/SHA engine driver that applies to either SoC, and neither DT declares
  one. Even if the silicon has one (MT8163/MT8183 do not document it), it would lose to the CPU: A53/A73 do AES-GCM at
  hundreds of MB/s per core via the CE instructions, and Go's `crypto/aes`+`crypto/cipher` use them (arm64 asm paths for
  AES, GHASH, SHA-1/256/512). A DMA engine adds copies and interrupt latency for 16 KB TLS records. **Verdict: skip.**
- Minor: Show 5 `/proc/crypto` lacks `sha1-lib` while the Show 8 has it; irrelevant to Go (userspace asm).

## 2. Wi-Fi and Bluetooth

- Show 8: MT7663 over SDIO (`mt7663s`), SDR104, firmware N9 3.1.1. Connected 2.4 GHz ch6 40 MHz, VHT-MCS9 rx
  / MCS15 tx, signal very strong. Power save **on** with DTIM 1, beacon interval 100 ms.
- PS latency test (gateway ping, 30 x 1 s interval each, rather idle link): PS on avg 4.0 ms (min 2.0, max 7.8);
  PS off avg 4.3 ms (2.6 / 9.0); PS on again avg 11.6 ms (max 124 ms) which is a single noisy sample on a shared
  network, not PS. Conclusion: with DTIM=1 the PS cost is not measurable here; keeping it on saves power. Do not
  change. (A repeat on a weak-signal AP might show more, but nothing indicates a problem.)
- Show 5: MT7668 uses Amazon's out-of-tree `mt76x8_wlan` (taints kernel), PS off already, 5 GHz 433 Mbit/s. This is
  the other session's territory (Show 5 Wi-Fi); mainline `mt7615`/`mt7663s` can't drive the MT7668 WLAN function
  (0x7608 device id is a different firmware generation), so the offload questions are moot until/unless that
  session replaces the driver.
- Checksum/TSO: mt76 on SDIO passes sk_buffs through mac80211 and does RX checksum offload only for the connac
  MCU path; for SDIO there is no TSO. The wire is the limit (SDIO), not the CPU: 142 MB/s of urandom vs ~40 Mbit/s of
  echod network traffic. **Not worth chasing.** WoWLAN/beacon-filter: no suspend use case, skip.
- `ethtool` is not on the image; if someone wants the hard list of offload flags, install a static ethtool in
  scratch, not on the unit.

## 3. GPU frequency scaling

Both panfrost devices have devfreq with `simple_ondemand` (50 ms poll). Idle sits at the lowest OPP on both
(Show 8 300 MHz of 300-800; Show 5 299 MHz of 299-520). Only one transition each since boot (a one-off during
display bring-up), so the GPU is not spinning. Temperature proxies at idle, echod running:

| | CPU zone | GPU zone | SoC zone |
|---|---|---|---|
| Show 8 | 38.6 C (only one thermal zone; GPU has none) | n/a | n/a |
| Show 5 | 52.6 C | 54.0 C | 54.1 C |

The Show 5 runs ~14 C warmer at idle than the Show 8 with a smaller chip. That is the board, not the GPU
clock: GPU is at min OPP. If anyone wants to shave Show 5 temperature, look at CPU idle states (see
`cronos/research/mt8163-cpuidle.md`) and the backlight rather than the GPU. The Show 8 has no GPU thermal zone,
so `DEVFREQ_THERMAL` cannot throttle the Mali there; fine at 800 MHz max but worth adding a zone only if a
GPU workload ever heats it (none today).

Not measured: scaling under a heavy draw. Both governors are the stock one and the OPP tables are populated,
so I expect it to work; a quick `trans_stat` during a video-wall session would confirm (5 min job).

## 4. JPEG decoder on the Show 8

- Mainline `mtk-jpeg` supports decoders on MT8173 (own block) and MT8195 (`mtk-jpegdec-hw`), and only an *encoder*
  for MT8183 (`mediatek,mt8183-jpgenc`). No decode bindings or register map exist upstream for MT8183, and the
  Amazon DT has only the encoder. I found no evidence the MT8183 has a stateless JPEG decoder at all. The
  `mtk-jpegdec-hw` platform driver being listed is just the generic driver for other SoCs.
- Could not confirm against the silicon without poking unclocked registers, which I deliberately did not do.
- Echod uses Go's `image/jpeg` in the photos/Immich/dashboard paths (`internal/feature/home/photos.go`,
  `immich.go`, `dashboard/stream.go`). Decode cost scales with megapixels; an Immich preview (typically 1440 px
  class) decodes in tens of ms on an A73. If larger originals matter, cheaper wins are ordering (ask Immich for the
  "preview" size, which echod likely already does) and, on Go, `image/jpeg` is the slow part (no DCT SIMD).
  **Verdict: no HW decoder to use. The practical lever is a SIMD JPEG decode (libjpeg-turbo via cgo, or the
  MDP/ISP path if it can take YUV from software), not a kernel change.**
- Show 5: MT8163 is MT8173-family, whose JPEG decoder sits in the imgsys at a different base. The Show 5 DT
  declares only the encoder. If a decoder exists it would need a new DT node plus clock/larb/iommu wiring and
  testing: speculative, medium-high effort, and the Show 5 has a small screen where Go decode time is less of an issue.

## 5. Present-but-disabled in the DT

Show 8 (disabled nodes: many are just unrouted SoC peripherals):

| Node | Judgement |
|---|---|
| `soc/efuse@8000000` | **Worth a look.** Disabled efuse; mainline `mtk-efuse` could expose calibration (thermal, GPU/CPU speedbins, serial). Check whether `mediatek,efuse` driver ("10206000.efuse" is enabled on the Show 5) can be pointed at it. Low value unless we want per-chip thermal calibration. |
| `i2c@1100f000` temperature-sensor@71/72/73, `i2c@11011000` audio-codec@57, touchscreen@5d | Disabled alternates for other board variants (Amazon DT covers several SKUs). No: not our board. |
| `soc/dpi@14015000` | Parallel display out, unused (DSI panel in use). No. |
| `usb@11200000` (host child) | Controller is peripheral/OTG-ish; no port wired. No. |
| `keyboard@10010000` | SoC keypad, no keys on the board (pmic-keys / gpio used). No. |
| Spare SPI x6, I2C x8, UART x2, `pwm-leds` | Unwired pads. No. |

Show 5 (few disabled): `mmc@11240000` (extra MSDC, unwired), `usb@11270000` (second USB, unwired), three
spare UARTs/keypad, `mt6323/leds` (PMIC LED driver, no LEDs wired; the real LED path is elsewhere). Nothing worth enabling.

Driver-side not enabled in config that has hardware here: `CONFIG_VIDEO_MEDIATEK_MDP` (old MDP) is off on the Show 8
because MDP3 is used; `VPU` old driver is replaced by the `techo5-vpu`. Both fine.

## Ranked: what is worth doing next

| # | Item | Effort | Gain |
|---|---|---|---|
| 1 | Swap Go `image/jpeg` for a SIMD decode in the photo/Immich/camera-grid paths (cgo libjpeg-turbo or equivalent), measure on both units first with a 12 MP and a 1440 px sample | 0.5-1 day incl. benchmark; first step 1 h to measure current ms/frame | Probably the only block here with a tangible user-visible gain (photo page latency) on both boards |
| 2 | Check Show 8 `efuse@8000000` and enable `mtk-efuse` with the right size/cells; read-only first | 1-2 h | Only useful if thermal/GPU speedbin calibration is wanted; low |
| 3 | Add a GPU thermal zone to the Show 8 DT so devfreq-thermal can throttle | 1-2 h + kernel rebuild | Insurance only; no heat problem today |
| 4 | Load-test GPU devfreq on both (trans_stat during a video wall) | 5-15 min, lock needed | Confirms scaling; probably nothing to change |
| 5 | Show 5 JPEG decoder discovery (MT8173-style block) | 1-3 days, risky | Speculative; skip unless photos on the Show 5 are slow |
| - | Crypto engine, hwrng on Show 8, Wi-Fi PS change, TSO/WoWLAN | none | Nothing to gain; skip |

## Method notes

Commands were sysfs/proc reads (`/sys/class/devfreq`, `/sys/class/thermal`, `/sys/firmware/devicetree/base` status
walk, `/proc/crypto`, `/proc/config.gz` on the Show 8 only: the Show 5 kernel has no `config.gz`), `iw`, `dmesg`, and
`dd` of 64 KB from `/dev/hwrng` on the Show 5. The Show 8 agent-lock was taken for the ping test only and removed.
