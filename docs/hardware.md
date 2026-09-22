# Echo Show 5 (2nd gen, 2021) — `cronos`

Ground truth captured with `tools/hwdump.sh` on a unit running LineageOS 18.1
(unofficial). Raw output: [dumps/cronos-lineage-18.1-bench.txt](dumps/cronos-lineage-18.1-bench.txt).
Items marked *unverified* have not been confirmed on a unit by this project.

## Board

| Item | Value |
|---|---|
| SoC | MediaTek MT8163, 4× Cortex-A53 (`CPU part 0xd03`), 600 MHz – 1.3 GHz, 32-bit userspace (`armv8l`, `ABI: arm`) |
| GPU | ARM Mali (`kbase`) |
| RAM | 996 MB (`MemTotal`), plus a 483 MB zram swap on LineageOS |
| Storage | 7.6 GB eMMC (`8GTF4R`), partitions below |
| Display | 5.5-inch 960×480 IPS, DSI video mode, panel `st7701s` (cmdline `lcm=1-st7701s_wsvga_dsi_vdo_cronos_st_truly`), 59.64 Hz, backlight `/sys/class/leds/lcd-backlight` (0–255) |
| Touch | Goodix GT9xx (`gt9xx`, I²C 2-0x5d), input `goodix-ts`, raw axes 480×960 (panel is mounted rotated), 16 slots |
| Audio in | 4-mic array into a TI **TLV320AIC3101** ADC (I²C 0-0x18) |
| Audio out | one speaker on a Maxim **MAX98396** class-D amp (I²C 2-0x3d, reset on `gpio-392`) |
| Wi-Fi / BT | MediaTek **MT7668** SDIO combo (`mt76x8_wlan.ko`, `mt76x8_bt.ko`, firmware in `/vendor/firmware`) |
| Sensors | ambient light + proximity (`alsps`, I²C 0-0x44), exposed as input `m_alsps_input` and Android "Light Sensor" |
| Camera | main + sub camera on I²C 0, mechanical lens cover on `gpio-499` (`SW_CAMERA_LENS_COVER`) |
| Buttons | volume up (`gpio-393`), volume down (`gpio-394`), mic-mute (`gpio-404`) |
| Bootloader | Amazon LK; stock build `77c8c2e-20211019_182552`, amonet replaces it with `44072a3-20240709_162755`; preloader `29ba1b5-20210311_160043` |
| Kernel | Linux **4.9.337** arm64 (LineageOS build) or **4.9.77** 32-bit ARM (TWRP build); see [Kernels](#kernels) |
| Stock OS | Fire OS 7 (Android 9 based) |

## Partitions (eMMC)

| Name | Device | Size |
|---|---|---|
| kb, dkb | p1, p2 | 1 MB each |
| lk | p3 | 1 MB |
| tee1, tee2 | p4, p6 | 5 MB each |
| logo | p5 | 1 MB |
| expdb | p7 | 16 MB |
| MISC | p8 | 512 KB |
| boot | p9 | 16 MB |
| recovery | p10 | 16 MB |
| swdl | p11 | 32 MB |
| system | p12 | 3.0 GB (LineageOS uses ~1.0 GB; on a converted unit the TECHO5 rootfs store, see `tools/linux/README.md`) |
| cache | p13 | 256 MB |
| persist | p14 | 16 MB |
| metadata | p15 | 40 MB |
| userdata | p16 | 3.9 GB |

`boot` is a plain 16 MB Android boot image, which is enough for a kernel plus
a small initramfs. The LineageOS kernel command line already carries
`androidboot.selinux=permissive` and `androidboot.veritymode=disabled`.

The 4.9.337 kernel's filesystems (relevant to where a Linux rootfs can live):
ext2/3/4, vfat, fuse, ubifs, loop devices — no squashfs, no overlayfs, no
f2fs, no devtmpfs. `/dev/rtc0` exists but holds 2010 at boot; the image takes
NTP once up and writes the RTC back. `/proc/idme/bootcount` read 50 on the
bench unit after the Linux boots, while the bootloader still booted normally.

Images pulled from the bench unit on 2026-09-15 (kept outside the repo in
`D:\platform-tools\echoshow\`): `boot-lineage-18.1-20260904-cronos.img`
(md5 `ade8a5e0…`), `recovery-twrp-cronos.img` (`fc6abbcf…`), `lk-amonet-cronos.img`
(`d4688deb…`). Boot image header v0, page 2048, tags at `0x48000000`:

| Image | Kernel | Load | Ramdisk | Load | Command line in header |
|---|---|---|---|---|---|
| LineageOS boot | 7.4 MB gzip arm64 `Image`, DTB appended | `0x40080000` | 0.8 MB gzip | `0x69244e00` | `bootopt=64S3,32N2,64N2 console=ttyMT0,921600n1 firmware_class.path=/vendor/firmware androidboot.selinux=permissive buildvariant=userdebug` |
| TWRP recovery | 6.8 MB ARM `zImage`, DTB appended | `0x40008000` | 9.1 MB lzma | `0x43400000` | `bootopt=64S3,32N2,32N2 buildvariant=eng` |

LK appends the rest (`lcm=…`, `vram=7340032`, `fps=5964`, `bl_level=90`,
`androidboot.*`, `firmware_class.path=/vendor/firmware` on the recovery boot too).

## Kernels

Two downstream 4.9 kernels boot this board; both come from
`github.com/amazon-oss/android_kernel_amazon_mt8163` and both have their full
`/proc/config.gz` saved under `docs/dumps/`.

| | LineageOS boot | TWRP recovery |
|---|---|---|
| Branch | `cronos/lineage-18.1` (also `lineage-18.1`, `lineage-22.2`) | `cm-14.1` (32-bit; `diff/cm-14.1` carries the cronos diff) |
| Version | 4.9.337, `armv8l` (arm64 kernel, 32-bit userspace, `CONFIG_COMPAT`) | 4.9.77, `armv7l`, built 2025-11-28 |
| Config | `dumps/cronos-lineage-kernel-4.9.337.config` | `dumps/cronos-twrp-kernel-4.9.77.config` |
| Framebuffer | `CONFIG_FREE_FB_BUFFER=y` (see Display) | not set |
| Wi-Fi / BT | `CONFIG_MTK_COMBO` off; vendor modules `mt76x8_wlan.ko`, `mt76x8_bt.ko` in LineageOS `/vendor/lib/modules`, loaded by `init.insmod.sh` | `CONFIG_MTK_COMBO=y`, chip `MT7668`, `CONFIG_MTK_COMBO_BT=y`, `CONFIG_WLAN_VENDOR_MEDIATEK=y` — but the driver itself is out of tree, so no SDIO driver binds in TWRP |
| Audio | vendor `mt-snd-card` (validated with the daemon) | same card, all 24 PCM devices present; `audioprobe` captures and plays (with the DL1 hold) |
| Touch | `gt9xx` | `CONFIG_TOUCHSCREEN_GTP9XX=y` |
| Bluetooth core | `# CONFIG_BT is not set` | `# CONFIG_BT is not set` |
| Console/fbcon | `# CONFIG_VT is not set` | `# CONFIG_VT is not set` |
| USB gadget | configfs with ACM, serial, RNDIS, mass storage, FunctionFS | (TWRP uses FunctionFS adb) |
| initramfs | gzip, lzma, xz, lz4 | gzip, lzma, xz |
| devtmpfs | not set (needs an init that populates `/dev` or `mdev`) | — |

`cm-14.1` is the branch r0rt1z2 keeps for the 32-bit TWRP and the postmarketOS
`amazon-checkers` port (cronos support added 2026-05-13: device trees, ST7701S
panel driver, `gpio-privacy`, codecs, `cronos.config`). The `lineage-18.1`
family is what LineageOS ships. The Linux image (porting plan M4) uses the
LineageOS kernel.

The mainline effort, `github.com/bengris32/linux-mtk` branch `mt8163/7.0`
(last touched 2026-03-30), has an MT8163 `mediatek-drm` (mmsys, OVL, RDMA, DSI,
mutex…), `mt8163-afe-pcm` audio and a family of Amazon device trees including
`mt8163-amazon-cronos.dts` (v4.1/v4.2/v4.3 variants) — but the cronos tree only
enables eMMC, USB peripheral, GPIO keys and the light sensor. No panel, touch,
codec or SDIO node yet, and mainline `mt76` has no MT7668 Wi-Fi (its SDIO table
is MT7663 only; `btmtksdio` does list MT7668). Not usable for this project.

## Wi-Fi and Bluetooth (MT7668)

- SDIO on `mmc1` (`11250000.mmc`), vendor `0x037a`: function 1 device `0x7608`
  (Wi-Fi), function 2 device `0x7668` (Bluetooth). On LineageOS the drivers
  `wlan` and `btmtk_sdio` bind to them.
- Firmware in LineageOS `/vendor/firmware`: `WIFI_RAM_CODE_MT7668.bin`,
  `WIFI_RAM_CODE2_SDIO_MT7668.bin`, `mt7668_patch_e2_hdr.bin`,
  `EEPROM_MT7668.bin`, `TxPwrLimit_MT76x8.dat`, `wifi.cfg`. No WMT daemon is
  involved: `init.insmod.sh` insmods the two modules and `wpa_supplicant` runs
  on `wlan0`.
- Driver sources: the LineageOS vendor tree, mirrored as
  `gitlab.com/echo-pmos/amazon-checkers-vendor` (`amazon/wlan/mediatek/driver/mt76x8`,
  `amazon/bluetooth/mediatek/mt76xx/driver/mt76x8/sdio`).
- **Associating from Linux (verified 2026-09-15):** the driver ignores the RSN
  element the supplicant supplies and generates its own for the association
  request (`rsnGenerateRSNIE`, capabilities field 0 unless PMF is requested).
  wpa_supplicant 2.11 puts capabilities 0x000c (16 PTKSA replay counters, added
  whenever WMM is on) into message 2/4, hostapd on the access points compares the two
  elements and deauthenticates with reason 2, which the supplicant reports as a
  wrong key. wpa_supplicant 2.9 (no replay-counter advertisement) associates and
  completes the handshake. Driver trace: `echo "0x11:0xff" > /proc/net/wlan/dbg_level`
  (module 0x11 = RSN), then `dmesg | grep "Gen RSN IE"`.
- The firmware picks the BSS itself (by SSID, any band), whatever BSSID the
  supplicant asked for; pinning `bssid=` just makes the join fail.
- The Bluetooth driver does **not** register a Linux HCI device. It creates the
  character device `/dev/stpbt` (plus `/dev/stpbtfwlog`) carrying raw H4 packets
  (leading type byte `0x01` command, `0x02` ACL, `0x04` event), which Android's
  vendor libbt talks to. For BlueZ the kernel needs `CONFIG_BT` and a bridge:
  `hci_vhci` (a userspace process copying stpbt ↔ `/dev/vhci`) or `hci_uart`
  H4 over a pty. Neither kernel has `CONFIG_BT`, so this needs a rebuild.

## Audio

One ALSA card, `mt-snd-card`, with 24 PCM devices. Only two matter:

| Path | Role | Format seen open |
|---|---|---|
| `/dev/snd/pcmC0D22c` | `TLV320AIC3101 Capture` | **S24_3LE, 4 channels, 16 000 Hz**, period 257, buffer 2570 |
| `/dev/snd/pcmC0D23p` | `MAX98396_Playback` | **S16_LE, 2 channels, 48 000 Hz**, period 768, buffer 1536 |

This is the same shape EchoLocal drives on the Dot (S24_3LE capture, 48 kHz
S16_LE stereo playback), so its raw-ioctl ALSA code should carry over with
the device numbers and channel count changed.

### Verified with `cmd/audioprobe` (raw ioctls, vendor HAL idle, satellite app stopped)

- Capture opens at 320-frame periods × 8 and reads 3 s with no overruns.
  Playback opens at 768 × 4 and plays with no underruns. Both devices are
  free once the satellite app is stopped; the vendor HAL keeps only
  `controlC0` open.
- **Channel map of `pcmC0D22c`:**

  | ch | content |
  |---|---|
  | 0 | microphone |
  | 1 | bit-identical copy of ch 0 |
  | 2 | playback loopback, **left** |
  | 3 | playback loopback, **right** |

  Proven by playing 1 kHz left / 1.5 kHz right while capturing: ch 2 and ch 3
  decorrelate (corr ≈ 0), ch 0 and ch 1 stay identical to the bit in every
  recording. The loopback pair is silent (exact zeros) whenever nothing plays
  and does not depend on `Audio_ExtCodec_EchoRef_Switch`.
- The quiet-room floor on the mic channel is about −69 dBFS RMS; a 0.3 FS
  tone from the device's own speaker lands at about −17 dBFS RMS on the mic.
- `ADC_A Left Mute = 1` did **not** change the captured audio, so the
  `ADC_A` controls are not in the path that produces this stream as
  configured, or the stream is a mono capture duplicated by the AFE. Whether
  a second, independent microphone channel can be enabled was open until
  2026-09-15 — **answered, see "Two microphones" below.**

### Two microphones, and why the stream showed one

The array is **two** microphones, not four: `idme` holds calibration for
`miccal.0` and `miccal.1` only, and the device tree has one enabled
TLV320AIC3101 at I²C `0x18` (with a second address, `0x19`, in its `reg` as
`adc1`, and a separate disabled node). The codec's stereo ADC takes them on
`DIF1_L` / `DIF1_R`. Its output does not go to the MediaTek AFE at all: an
**FPGA** on SPI (`/soc/spi@1100a000/spi@0`, compatible `amzn-mtk,spi-audio-pltfm`,
"FPGA Revision = 208", `fpga-cdone-gpio`) packs the two microphones and the
two playback-loopback channels into the 4-channel frames that Amazon's
`amzn-mt-spi-pcm` driver reads over SPI — which is why the codec's mute
control never touched the stream and why the ring geometry is what it is.

The bit-identical ch 1 was the **driver**: with the device-tree property
`amzn,mic-downmix` present, `amzn-mt-spi-pcm.c` replaces ch 0 and ch 1 of
every frame with their average (`(s0 + s1) / 2`). The LineageOS device trees
(all eleven appended to the kernel) carry it, so Amazon's own build shipped
the Show 5 downmixed too. Deleting the property with
`tools/linux/patch-dtb.py` — no kernel rebuild — makes the driver probe with
`mic_downmix=0` and the stream carries the two microphones separately
(verified: correlation 0.95, difference −66 dBFS RMS, no longer identical).
The bench unit boots `techo5-linux-boot-nodownmix.img` since 2026-09-15.
- Host-side analysis: `go run ./tools/wavstats file.wav`.

Mixer (`tinymix`, 118 controls) — the ones that look relevant:

- ADC (TLV320AIC3101): `ADC_A Digital Volume Control` (88 88), `ADC_A MICPGA Volume Ctrl` (40 40),
  `ADC_A Left/Right Mute`, `ADC_A Left/Right Fine Volume`, the `ADC_A * Ip Select` input routing
  (DIF1_L / DIF1_R selected), `DAI Sel Mux A`.
- Amp (MAX98396): `Digital Volume A` (127), `Speaker Volume A` (8), `Speaker Safe Mode A`,
  `Ramp Up/Down Switch A`, `Dither Switch A`, `Amp Fault Enable`, `VI Sense A Switch`.
- MediaTek AFE: `Ext_Speaker_Amp_Switch` (On), `Audio_I2S0dl1_hd_Switch` (On), `Board Channel Config` (Stereo).

Amazon's processing lives in userspace, in `audio.primary_amazon.mt8163.so`,
with its configs under `/vendor/etc/audio-algorithms/`: `AFE.cfg`,
`coefs_FBF.cfg` (fixed beamformer), `Tap_AEC_mic1/2.cfg` (echo cancellation),
EQ and multiband limiter tables. A replacement daemon gets the raw four
channels and must do its own beamforming or echo cancellation, or duck
playback while listening.

Per-unit microphone calibration is in `/proc/idme/miccal.0` … `miccal.3`.

### Kernel panic hazard: the DL1 driver's SRAM ring

The MediaTek playback driver (`mtk_pcm_I2S0dl1`, which device 23 rides on) decides at `open()`
where its ring lives. If no other AFE stream is open it takes the AFE's internal SRAM
(`mPlaybackSramState = SRAM_STATE_PLAYBACKFULL`); otherwise it uses a DRAM buffer. On this
Amazon kernel the SRAM path faults on the first `copy_from_user` into the ring
(`mtk_pcm_I2S0dl1_copy`, fault address `ffffff8009eb0004`, every time) — a **kernel panic and
reboot** (`sys.boot.reason = kernel_panic,fatal_exception`, log in
`/sys/fs/pstore/console-ramoops`). Reproduced six times on 2026-09-14 with 12 and 16 KB rings.
The DRAM path plays cleanly.

**Workaround, required:** hold any other AFE PCM node open, unconfigured, before opening
`pcmC0D23p` and for as long as it is open. `/dev/snd/pcmC0D1c` (MultiMedia1_Capture, unused
on this device) works. `cmd/audioprobe -hold` and the daemon's speaker do this; the daemon's
`tools play --hold` defaults to it. The driver source that explains the two paths is
`sound/soc/mediatek/mt8163/mt_soc_pcm_dl1_i2s0Dl1.c` in any public MT8163 kernel tree.

Ring geometry: 768 × 4 (12 KB, the vendor HAL's period at twice its depth) is what the
daemon uses; the HAL itself runs 768 × 2.

### Loudness: `Speaker Safe Mode A` must be cleared

The MAX98396 comes up from the kernel driver with `Speaker Safe Mode A = 1`, a power cap that
takes about **30 dB** off the output. Amazon's HAL clears it at boot, which is why the device was
loud with the stock HAL and quiet once Android moved to the null HAL. Measured 2026-09-15 with
`audioprobe -concurrent`: a 0.3 FS tone read −47 dBFS RMS at the microphone with safe mode on
and −15 dBFS with it off, mic floor unchanged. The daemon now clears it in its speaker init
sequence. The hardware is linear either way (10 dB digital → 10 dB acoustic).

Codec controls are register-cached while a stream runs and reach the chip when the codec
powers up again, so a control changed under a running daemon shows only after the stream is
closed and reopened. `Speaker Volume A` is not a usable gain: 5 and 8 sound identical and 14
mutes the output.

Speech from Home Assistant is normalized to −18 dBFS RMS before the volume curve
(`media.Normalize`/`SpeechGain`). The cronos volume curve is linear in dB: −45 dB at step 1,
−24 dB at half the dial, −6 dB at the top (`paths_cronos.go`). Tuned by ear in a small room
on 2026-09-15 across v0.1.1–v0.1.4; half the dial was judged "perfect" for conversation.

### A bare Linux boot leaves the codec unconfigured

Without Android's audio HAL the mixer is at the drivers' power-on state on both
kernels: `Ext_Speaker_Amp_Switch` Off, `Speaker Safe Mode A` 1, `ADC_A MICPGA
Volume Ctrl` 0, no `ADC_A * Ip Select` input chosen. Playback is audible as is
(the "Off" amp switch is only the control's cached value; do not touch it, see
below). Capture reads −95 dBFS until the inputs are routed: set `ADC_A Left Ip
Select ADC_A DIF1_L switch` and `… Right … DIF1_R switch` to 1 and `ADC_A MICPGA
Volume Ctrl` to 40, after which the floor is −70 dBFS, the same as under
LineageOS. `tools/linux/init` does this; the daemon should take it over.

### Do not toggle `Ext_Speaker_Amp_Switch`

On cronos this MediaTek control drives the GPIO wired to the MAX98396's reset (`gpio-392`,
`MAX98396_RESET`). Switching it Off and On resets the amplifier and wipes the register setup the
codec driver did at probe; the driver's `init_done` flag means it never repeats it, so the
speaker stays silent, with the DAC still "powering up" normally in the log, until a reboot.
Found 2026-09-14 when the daemon's speaker start sequence did exactly that. Leave the amplifier
as the kernel brought it up; volume is applied in software.

### Android's audio stack cannot share the devices

With the daemon holding `pcmC0D22c`/`pcmC0D23p` on LineageOS, the vendor HAL fails to open
them, `audioserver` crash-loops once a second, `system_server` dies with it, and the framework
restart hangs until the daemon lets go. Stopping `audioserver` instead makes AudioService block
system_server's main thread and the watchdog kills it a few minutes later; the class restart
then brings audioserver back into the crash loop. Working bench mode (`tools/bench-nofw.sh`):
stop `zygote` (framework, launcher, boot animation), `audioserver`, `bootanim` and
`vendor.audio-hal`; native services, Wi-Fi, adb and the daemon keep running, free memory rises
from ~355 MB to ~640 MB. Android's connectivity service removes the policy-routing rule for the
Wi-Fi table when it dies (`ip rule` falls through to `unreachable`); adding `ip rule add from
all lookup <wlan0 table> pref 5000` restores it. DHCP renewal is the framework's, so the lease
will lapse in this mode. Reboot to get Android back.

**The fix, in place on the bench unit:** point Android at its null primary audio HAL.
`/system/build.prop` selects the HAL with `ro.hardware.audio.primary`; `amazon_wrapper` is the
stock value and `default` loads `/vendor/lib/hw/audio.primary.default.so`, which never opens a
PCM device. With that one line changed (root, `mount -o remount,rw /`) Android boots normally,
audioserver and the HAL run and hold nothing, and the daemon owns the hardware while the
framework, launcher and ShowAssist keep running. Android apps get silence in and out. The
original file is kept as `build.prop.cronos-lineage-orig` next to the LineageOS zip.

### Mute button

`gpio-privacy-button` emits KEY_POWER on LineageOS and nothing else; it is silent and has no
on-screen effect.

The capture side is a separate Amazon driver, `amzn_mt_spi_pcm` (the mic array arrives over
SPI, not the AFE). It rejects a 256 × 10 ring with `EINVAL` and accepts 320 × 8.

### Warning

Do **not** run `dumpsys media.audio_flinger` on this device. It null-derefs in
the vendor audio HAL, audioserver restarts, and both capture and playback stay
silent until reboot. `dumpsys audio` is safe.

## Input and the mute button

| Input device | Node | Events |
|---|---|---|
| `gpio-privacy-state` | event0 | `SW_MUTE_DEVICE` — the hardware mute state |
| `gpio-privacy-button` | event1 | `KEY_POWER` (scan code 116) |
| `mtk-kpd` | event2 | none |
| `goodix-ts` | event3 | multitouch |
| `hwmdata` | event4 | REL_Y/REL_Z (sensor hub) |
| `m_alsps_input` | event5 | ABS_X = lux, ABS_WHEEL = proximity |
| `gpio-keys` | event6 | `KEY_VOLUMEUP`, `KEY_VOLUMEDOWN`, `SW_CAMERA_LENS_COVER` |

- Android's generic layout maps code 116 to POWER, so the mute button sleeps
  the screen. A per-device override in
  `/data/system/devices/keylayout/gpio-privacy-button.kl` containing
  `key 116 WAKEUP` fixes that (needs root, takes effect after reboot).
- The mute is a **hardware** function: the button toggles a latch, and
  `privacy-state-gpio` (`gpio-405`, input) reports it while
  `privacy-enable-gpio` (`gpio-384`, output) engages it. The kernel's
  `gpio-privacy` platform driver exposes both at
  `/sys/devices/platform/gpio-privacy/`: `state` (readable, `1` = cut) and
  `enable` (root write-only). Writing `1` to `enable` pulses the line for the
  device tree's 1000 ms and cuts the microphones. **Software cannot release
  the latch**: a second `1` or a `0` leaves it cut; only the button releases
  it (verified 2026-09-14). The red indicator follows the latch. After
  unmuting, an app that was capturing gets silence until it reopens the
  capture path.
- While the latch is engaged, the vendor audio HAL (`audio.service`) opens and
  holds `pcmC0D22c` even with no client, and reopens it if killed. A daemon
  that owns capture has to take the device before the mute is engaged, or the
  HAL must not be running.

GPIO block: `gpiochip0`, GPIOs 357–511 on `1000b000.pinctrl`.

## LEDs and light sensor

- `/sys/class/leds` only exposes `lcd-backlight`. The red mute indicator is not
  a Linux LED; it is most likely driven by the privacy circuit alongside
  `privacy-enable-gpio`. *(unverified — test by toggling gpio-384.)*
- Light sensor: `alsps` at I²C 0-0x44, read through the input device or the
  Android sensor HAL (`android.hardware.sensors@1.0-service`, sensor
  "Light Sensor" by `amazon-oss`). Calibration in `/proc/idme/alscal`.
  It sits behind MediaTek's hwmsensor framework, not IIO (the only IIO device,
  `iio:device0`, is the auxadc): nothing arrives on `event5` until it is
  switched on — `echo <ns> > /sys/class/misc/m_alsps_misc/alsdelay`, then
  `echo 1 > …/alsactive` — after which it reports `ABS_X` = lux at that period
  (verified 2026-09-15: ~105 lux on the bench, the same figure ShowAssist
  showed). The daemon's `hardware/ambient` does this.
- Touch (`goodix-ts`, event3): multitouch protocol B only — `ABS_MT_SLOT`,
  `TOUCH_MAJOR`, `WIDTH_MAJOR`, `POSITION_X/Y`, `TRACKING_ID`, no single-touch
  axes, `INPUT_PROP_DIRECT`. Coordinates are in the panel's portrait frame; the
  daemon's `hardware/touch` reads the ranges with `EVIOCGABS` and turns them a
  quarter turn to match `hardware/screen`.

## Display

- Panel: `st7701s_wsvga_dsi_vdo_cronos_st_truly` (from the kernel command line
  `lcm=`), a MIPI-DSI **video-mode** panel. Native orientation is portrait: the
  framebuffer is 480 wide × 960 tall, 32 bpp, byte order B, G, R, A (BGRA8888,
  the `mmap` offsets fb0 advertises are r16 g8 b0 a24). The device is used in
  landscape, so anything drawn is composed 960×480 and rotated 90° onto the panel.
- Kernel display stack: MediaTek 4.9 `mtkfb` + `mtk_disp_mgr`
  (`drivers/misc/mediatek/video/mt8163/videox` in
  `amazon-oss/android_kernel_amazon_mt8163`, branch `lineage-18.1`). The overlay
  engine OVL0 has 4 layers; the primary path runs OVL0 → RDMA0 → DSI, driven by
  the GCE command queue (CMDQ). Register window is physical `0x14007000`–`0x14018000`.
- **The Linux framebuffer works outside Android.** Under LineageOS
  `/dev/graphics/fb0` reports `smem_len = 0` and `mmap` returns `EINVAL` at every
  size, with or without SurfaceFlinger. Booted into TWRP (the 4.9.77 kernel)
  the same node reports `smem_len = 5529600` (480×960, 32 bpp, line 1920 bytes,
  virtual 480×1920, pixel order R0 G8 B16 A24), `cmd/fbprobe` maps 3.6 MB and
  paints the panel with `FBIOPAN_DISPLAY` (verified 2026-09-15). The difference
  is `CONFIG_FREE_FB_BUFFER=y` in the LineageOS kernel: `primary_display.c`
  frees the boot framebuffer (reserved region at physical `0x5f900000`, 7 MB)
  the first time a frame config arrives with no layer still pointing at it —
  i.e. once the Android compositor has its own buffers on the overlay — and
  from then on every fbdev path returns `-EPERM`/`-EINVAL`. A Linux boot never
  sends that frame config, so the LineageOS kernel keeps its framebuffer too.
  The panel is portrait; `fbprobe` composes 960×480 landscape and rotates 90°.
- **The display-manager API works up to the commit step.** `/dev/mtk_disp_mgr`
  takes MediaTek's 32-bit compat ioctls (`'O'` magic). `internal/mtkdisp` speaks
  them: create the primary session (id `0x10000`), read info (480×960, `vram` 7 MB,
  physical 63×125 mm), allocate ION multimedia-heap buffers (`/dev/ion`, heap id
  10) that get valid M4U addresses via `ION_MM_CONFIG_BUFFER` + `ION_SYS_GET_PHYS`,
  switch the session between direct-link and decouple mode (which visibly changes
  the RDMA registers), read back the OVL/RDMA registers by mapping the register
  window, capture what the panel scans out (`MTKFB_CAPTURE_FRAMEBUFFER`, WDMA to a
  user buffer), and wait on vsync. The compat struct sizes were pinned by probing
  which argument size each ioctl accepts (`disp_input_config` is 168 bytes, not
  164 — the compat `s64` timestamp is 8-aligned on this arm64 kernel; the input
  config array totals 1432 bytes).
- **The overlay config does not latch from a bare session.** `SET_INPUT_BUFFER`
  with a valid `src_phy_addr` and `TRIGGER_SESSION` both return success, and the
  CMDQ record shows the config tasks executing for the calling process — yet
  `OVL0 src_con` stays 0 and layer 0's address stays at the boot framebuffer, so
  nothing new reaches the panel. The missing piece is the CMDQ trigger-loop /
  display-mutex commit that the hardware composer builds at its own init and a
  session join does not reproduce. Conclusion: paint the panel from the mainline
  `mediatek-drm` KMS driver in the Linux image, not from this vendor stack. See
  `docs/porting-plan.md` (M2/M4).
- TWRP paints this panel with a tiny userspace through exactly this fbdev path.

## Boot logo (LK)

The `logo` partition (p5) is all zeros on this unit; what the bootloader
paints is compiled into LK itself. `lk-amonet-cronos.img` (MTK header, `LK`,
384 KB used of the 1 MB partition, load base `0x4BD00000`) carries five
single-image zlib bundles — `u32 count=1, u32 total, u32 offset=12, zlib(raw
32-bit BGRA)` — and a data table of pointers to them (load address =
`0x4BD00000` + file offset − 512): the "amazon" wordmark (315×170, drawn
centered on black, file offset 335736, pointer at 383384), "Booting…" (315×170),
two battery pictures (408×216) and the full-screen over-temperature
thermometer (960×480, landscape as stored; LK applies the panel rotation).
LK knows each image's size from code, so a replacement must decompress to the
same byte count. `tools/linux/patch-lk-logo.py` appends a new wordmark bundle
after the LK image, repoints that one pointer and grows the header size.

**Do not apply it to the `lk` partition (p3).** Tried 2026-09-15: p3 holds
Amazon's *stock* LK (byte-identical to amonet's `bin/lk.bin`); amonet's kaeru
bootloader is the patched LK copy in `expdb` (p7, `cronos-kaeru.bin`) and the
tee payload in `tee1` chains into it at boot. With p3 enlarged, the chain did
not engage: the stock LK ran alone — no boot of the unsigned image, and the
button-combo fastboot answered `flash lk` with "restricted on locked hw".
Recovery = re-run amonet's fastbrick (`fastboot flash brick fastbrick.img`
from that stock fastboot); it rewrites p3 (stock lk), p7 (kaeru), tee1/tee2,
preloader, flashes TWRP to recovery and reboots into TWRP; the boot slot,
store and data were untouched. If the logo is ever changed, the bundle has to
go into the kaeru copy in `expdb` (same LK layout, wordmark at the same
offset), which kaeru's fastboot (reached via `rebootto bootloader`) can
flash — and only *in place*: kaeru's stage-2 code sits right after the LK
payload, so the header size must stay and the new bundle must fit the old
6105-byte slot (`--in-place --colors 16`: 16 flat colors compress the full
315×170 mark to 5.5 KB). Done 2026-09-15; `fastboot flash expdb` accepted it. `swdl` (p11) holds an Android boot image (Amazon's recovery/download
image).

## Factory data (`/proc/idme`)

`board_id`, `serial`, `mac_addr`, `bt_mac_addr`, `miccal.0-3`, `alscal`,
`sensorcal`, `ledparams`, `unlock_code`, `bootcount`, `bootmode`, `postmode`,
`dev_flags`, `fos_flags`, `region`, `locale`. `product_name`, `productid`
and `productid2` read `0` on the unit seen.

## Unlock and recovery

- **amonet-cronos v2.0.1** (k4y0z, r0rt1z2). With the device on mains power,
  hold all three buttons until the screen shows `=> FASTBOOT mode`, connect
  USB, run the fastbrick payload. The exploit reboots the device into TWRP.
  Never interrupt it.
- Fastboot reports `product: CRONOS`, `version: 0.5`, `kaeru-version: 2.0.0`;
  `unlock_status` flips to `true` afterwards. `max-download-size` is 109 MB.
- amonet's LK does **not** implement `fastboot boot` ("unknown command"): test
  images must be flashed. After `adb reboot bootloader`, `fastboot reboot` lands
  back in fastboot; use `fastboot continue` to boot normally. Volume-down while
  plugging in enters fastboot (`TW_HACKED_BL_BUTTON`).
- **The `recovery` slot boots 32-bit kernels only.** The 64-bit LineageOS
  kernel flashed to `recovery` (even the untouched stock image) produces no
  kernel output at all; the watchdog resets the unit (`bootreason
  wdt_by_pass_pwk`) and LK boots `boot` instead. Verified with a command-line
  marker. 64-bit images go in `boot`.
- `reboot recovery` is signaled through the RTC spare register
  (`rtc_mark_recovery` in the kernel log), not the MISC bootloader message;
  MISC stays zero. LK keeps a boot counter in idme (`/proc/idme/bootcount`) and
  after enough boots that never complete Android it parks in "hacked fastboot
  mode", which is reachable over USB; `fastboot continue` resumes.
- From Linux, a plain `reboot` on the LineageOS kernel is a normal boot; on
  the 4.9.77 kernel it lands in fastboot. `cmd/rebootto` (RESTART2 with
  `bootloader`/`recovery`) enters fastboot or recovery from Linux.
- TWRP works over `adb shell twrp ...` (format data, wipe, install zip) and is
  a 4.9.77 32-bit kernel (`cm-14.1` branch) with a small userspace; `adb shell`
  in TWRP is root, and `/dev/snd`, `/dev/input`, `/dev/graphics/fb0` are all
  usable there (the bench for non-Android experiments).

## LineageOS 18.1 (unofficial, r0rt1z2)

- Android 11, `userdebug`, test keys. Build seen: `lineage-18.1-20260904-UNOFFICIAL-cronos`.
- Enable USB debugging after first boot; "Rooted debugging" in developer
  options allows `adb root`. SELinux is permissive.
- Auto time zone is wrong; set `settings put global auto_time_zone 0` and
  `service call alarm 3 s16 <zone>`.
- The default launcher package is `com.android.launcher3`.
- Vendor services still running that a slimmer image can drop: camera HAL
  and cameraserver, Widevine and ClearKey DRM, CAS, `amazonthermal`,
  `vendor.power-amazon`, `securetime`, `kisd`, media codec services.

## Network behavior seen in the field

- The satellite app listens on TCP 10800 (VACA / ShowAssist) and advertises
  over mDNS. mDNS across subnets needs a reflector rule for `_esphomelib._tcp`
  or the VACA service depending on the daemon in use.

## Open questions

- Whether toggling `privacy-enable-gpio` (gpio-384) from userspace mutes the
  array and lights the red indicator, and whether the latch can be cleared
  after a button press.
- Whether the AIC3101 needs any mixer setup beyond what the vendor HAL leaves
  behind at boot when the HAL is not running.
- Why capture channels 0 and 1 are identical: mono ADC, AFE duplication, or a
  second mic that needs routing. The `ADC_A` mute had no effect on it.
- Whether the arm64 LineageOS kernel boots a non-Android initramfs as cleanly
  as the 32-bit TWRP kernel does (first M4 boot will tell).
- Whether the DL1 SRAM-ring panic and the amplifier safe-mode cap also apply
  on the 4.9.77 kernel (`audioprobe` with the default hold worked there;
  nothing else measured).
