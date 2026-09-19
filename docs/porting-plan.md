# Porting plan

The order is chosen so that every milestone leaves a usable device.

## M0 — Ground truth on the hardware — done

`tools/hwdump.sh` run on a LineageOS unit; findings in `docs/hardware.md`.
Still open from M0: whether `privacy-enable-gpio` controls the mute and the
red indicator from userspace, and the matching GPL kernel source drop.

## M1 — Voice daemon on cronos

Step 1 — done: `cmd/audioprobe` (pure Go, `internal/alsa` from EchoLocal)
captures and plays through the raw devices with no vendor HAL involvement.
Channel map established: mic, mic copy, loopback L, loopback R. Build with
`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build ./cmd/audioprobe`.

Step 2 — **working end to end** (2026-09-14). The daemon is vendored under `echod/` and builds
for cronos (default) and the Dot (`-tags dot`). On the Show it captures, plays, detects the wake
word (microWakeWord, on the CPU at ~27 % of one core), streams to Home Assistant, and speaks
the reply. Home Assistant discovers it over zeroconf as an ESPHome device ("Echo Show 3D4E5F",
manufacturer TECHO5) with 102 entities. First complete turn: "Hey Jarvis, what time is it?" →
"7:11 PM", at a sensible volume.

What it took, all in `docs/hardware.md`: hold an AFE node so the DL1 driver uses DRAM, never
toggle the amp switch, treat the mute latch as one-way, decode announcement WAVs by header,
and keep Android's audio stack off the devices — for now by stopping the framework
(`tools/bench-nofw.sh`).

Later the same day: Android moved to its null primary audio HAL
(`ro.hardware.audio.primary=default` in `/system/build.prop`), which keeps audioserver off the
PCM devices for good, and the daemon became an init service (`tools/init/techo5.rc`,
`/system/bin/techo5`) that starts on `sys.boot_completed`. Android, ShowAssist on screen and
the daemon now run side by side, and the daemon survives a reboot.

Still to do for M1:

- Done since: `tools/install-cronos.ps1` provisions a Show in one run (binary, init service,
  null HAL, name, key, wake models, key layout); the bench unit is named "Bench Show" with
  the `bench_show_` entity prefix and ShowAssist's satellite is disabled ("Bench Show
  Screen"); the Alexa microWakeWord model is installed from esphome/micro-wake-word-models.
- Understand the second capture channel (identical copy of the mic) and whether a second mic
  exists.
- Volume: even the top of the curve (unity) was heard as a little quiet in the room. The next
  step up is the amplifier's own gain, `Speaker Volume A` (MAX98396, 8 of 17 as the HAL leaves
  it); raise it carefully and re-measure headroom before making it the default.
- Releases: `tools/release.ps1` builds a versioned daemon, writes `manifest.json` with
  `cmd/mkmanifest` and publishes a GitHub release. The daemon's updater (the `update` entity in
  Home Assistant) follows the latest release and replaces `/system/bin/techo5` in place; init
  restarts the daemon into the new binary (the service must not be `oneshot`). The new binary
  runs **on trial** for five minutes, then commits. Cronos has no vendor boot hook, so a trial
  binary that dies is rolled back in-process by the next start (verified 2026-09-15 with
  v0.1.2 → v0.1.1 in five seconds, no reboot). Consequence: `setprop ctl.stop`/`ctl.restart`
  is a SIGKILL and looks like a crash — during a trial, stop the daemon with `kill -TERM` (the
  bench scripts and the installer do), or simply leave it for five minutes.
- Loudness: the amplifier's safe mode was the cap (see hardware.md); the daemon clears it,
  speech is normalised to −14 dBFS RMS, and the volume curve sits 6 dB under the Dot's.

The port covers:

- New device layout: paths, board name, model string.
- `hardware/mic`: open `pcmC0D22c` as S24_3LE, 4 channels, 16 kHz; ch 0 is
  the mic, ch 2/3 the playback reference for the canceller (the Dot has
  7 mics + 2 refs, so `Mics`/`Refs` become 1 and 2 and the beamformer is
  bypassed); re-open on the privacy switch.
- `hardware/speaker`: `pcmC0D23p` at 48 kHz S16_LE stereo; volume through
  the MAX98396 `Digital Volume A` / `Speaker Volume A` controls.
- `hardware/buttons` and `hardware/privacy`: `gpio-keys` (event6),
  `gpio-privacy-button` (event1) and `gpio-privacy-state` (event0);
  drive `privacy-enable-gpio` (gpio-384) for mute.
- Light sensor from `m_alsps_input` (event5, ABS_X = lux).
- Drop the Dot-only LED ring; add the mute LED if reachable.
- Install as an init service on LineageOS (`/system/etc/init/techo5.rc`)
  instead of the Fire OS `ledcontroller` takeover that EchoLocal uses on the Dot.

Result: the Show appears in Home Assistant as an ESPHome device with a voice
satellite and a media player, exactly like the Dots, while Android keeps
running the screen.

## M2 — Display layer

Baseline in place (2026-09-14): ShowAssist stays on the screen as a display-only web view of
the `echo-show` dashboard (its own satellite role is disabled), and the dashboard reacts to the
daemon: a card driven by `assist_satellite.<name>_assist_satellite` shows "Listening…",
"Thinking…" with the transcript, then transcript and reply while the answer plays; the Now
Playing and volume tiles and the station chips target the daemon's `media_player`. All of that
is Home Assistant configuration, no device code.

Direction chosen (2026-09-15): the daemon gets its **own** display layer, so the
end device runs no Android UI at all. ShowAssist stays only as the interim screen
until that layer works. Where the daemon-owned layer runs is the open question
below.

### What was learned reaching for the panel from userspace

The goal was to paint the panel directly from a small Go program
(`internal/mtkdisp`, `cmd/dispprobe`), with the Android framework stopped. The
kernel is MediaTek's 4.9 `mtkfb`/`mtk_disp_mgr` stack (sources at
`amazon-oss/android_kernel_amazon_mt8163`, branch `lineage-18.1`). Findings, all
verified on the bench unit:

- **The Linux framebuffer is a dead end.** `/dev/graphics/fb0` reports
  `smem_len = 0`; the driver allocates the real buffer itself and exposes it only
  through the overlay path, so `mmap` on fb0 is refused at every size, with or
  without SurfaceFlinger running. `cmd/fbprobe` records this.
- **The display-manager API works up to the last step.** `/dev/mtk_disp_mgr`
  takes a 32-bit compat ABI; the struct sizes were confirmed against the running
  kernel by probing which argument size each ioctl accepts (`ScanIoctlSize`).
  `internal/mtkdisp` creates the primary session, reads its info (480×960,
  ~60 Hz, physical 63×125 mm), allocates ION multimedia buffers that get valid
  M4U addresses (get-phys returns non-zero), switches the session between direct
  link and decouple (which visibly moves the RDMA registers), reads back the
  overlay/RDMA registers, captures what the panel is scanning out, and waits on
  vsync — all working.
- **The overlay config never latches.** With a valid buffer address handed to
  the overlay (`src_phy_addr`, so the kernel's own fence lookup is bypassed),
  `SET_INPUT_BUFFER` + `TRIGGER_SESSION` return success and the CMDQ record shows
  the config tasks executing for our process — yet `OVL0 src_con` stays 0 and the
  layer-0 address stays at the boot framebuffer. The frame config is accepted and
  submitted but is never committed to the overlay registers. That commit is the
  CMDQ trigger-loop / display-mutex machinery the hardware composer builds at its
  own init; a bare session join does not reproduce it, and reconstructing it means
  reimplementing most of `primary_display.c` against undocumented CMDQ tokens.

Conclusion: driving this vendor stack from userspace is a poor investment. The
display layer is folded into M4, and until then the screen stays on ShowAssist (below).

**Resolved later the same day (2026-09-15): the plain Linux framebuffer works once
Android is out of the picture.** Booted into TWRP, `cmd/fbprobe` mapped `fb0`
(5.5 MB, 480×960×32 bpp, two pages) and painted the panel. The LineageOS kernel
reports `smem_len = 0` only because it is built with `CONFIG_FREE_FB_BUFFER`: the
display driver frees the boot framebuffer the first time the Android compositor
configures its own overlay layers. Nothing in a Linux boot triggers that, so the
same kernel keeps its framebuffer there. Details in `docs/hardware.md` (Display).
So the daemon-owned screen is **fbdev on the downstream kernel**, drawn by the
daemon itself — no DRM, no compositor, no Android. Mainline `mediatek-drm` was
checked and is not needed (and, see M4, mainline is ruled out anyway by Wi-Fi).

`internal/mtkdisp`, `cmd/dispprobe` and `cmd/fbprobe` are kept as the record of
the vendor path and a working probe of the panel geometry, capture and registers;
`fbprobe` is the seed of the display layer.

### Interim screen (in place since 2026-09-14)

ShowAssist stays on the screen as a display-only web view of the `echo-show`
dashboard (its own satellite role is disabled), and the dashboard reacts to the
daemon: a card driven by `assist_satellite.<name>_assist_satellite` shows
"Listening…", "Thinking…" with the transcript, then transcript and reply while
the answer plays; the Now Playing and volume tiles and the station chips target
the daemon's `media_player`. All of that is Home Assistant configuration, no
device code. Screen state (on/off, brightness) can later be exposed through the
same ESPHome device so Home Assistant sees one device, not two.

## M3 — Slim the OS (skipped)

Superseded by M4 on 2026-09-15: with fbdev proven and the daemon already owning
audio, going straight to a Linux-only image is less work than trimming Android.

## M4 — Linux-only image (the end state)

Decided 2026-09-15: the end device boots a Linux image with no Android userspace
at all — the downstream kernel, a small Alpine (armv7) rootfs, the daemon owning
mic, speaker, wake word, ESPHome API **and the screen** (fbdev, see M2), Wi-Fi
through the vendor driver, and Bluetooth for earbuds. M3 (slimming Android) is
skipped; Android stays only as the fallback in the `boot` partition until the
Linux image is trusted.

### Kernel: downstream 4.9, not mainline

Surveyed 2026-09-15, all details in `docs/hardware.md` (Kernels, Wi-Fi/Bluetooth):

- Mainline (bengris32 `linux-mtk`, branch `mt8163/7.0`, March 2026) has MT8163
  `mediatek-drm` and cronos device trees, but the cronos tree is a skeleton (eMMC,
  USB, keys, light sensor — no panel, touch, audio codecs or SDIO), and the
  mainline `mt76` driver has **no MT7668 Wi-Fi** support at all (only its
  Bluetooth half, `btmtksdio`). Wi-Fi alone rules mainline out.
- Two downstream kernels exist for cronos, both from `amazon-oss/android_kernel_amazon_mt8163`:
  - `cronos/lineage-18.1`: **4.9.337, arm64** with 32-bit userspace — what LineageOS
    boots and what the daemon is validated on. Wi-Fi/BT are vendor modules shipped
    in LineageOS `/vendor/lib/modules` (`mt76x8_wlan.ko`, `mt76x8_bt.ko`) and load
    with a plain `insmod`. USB gadget (configfs: ACM, RNDIS, FunctionFS) built in.
  - `cm-14.1`: **4.9.77, 32-bit ARM** — what TWRP and the postmarketOS
    `amazon-checkers` port boot (touch driver, audio codecs and the MT7668 combo
    glue built in, but the Wi-Fi/BT drivers themselves are out-of-tree).
- **Choice: `cronos/lineage-18.1`.** Same kernel as the Android fallback, the audio
  quirks are already mapped, and the Wi-Fi/BT modules exist as binaries, so the
  first Linux boot needs no kernel build at all. `cm-14.1` stays the fallback if
  the arm64 kernel misbehaves outside Android.
- A kernel rebuild is still needed later, for Bluetooth: neither kernel has
  `CONFIG_BT`. The vendor BT driver exposes `/dev/stpbt` (raw H4 packets), not a
  Linux HCI device, so BlueZ needs the kernel BT core plus either `hci_vhci` (a
  small userspace bridge stpbt ↔ vhci) or `hci_uart` H4 over a pty. Rebuilding
  means also rebuilding the vendor Wi-Fi/BT modules (sources: the LineageOS
  vendor tree / `gitlab.com/echo-pmos/amazon-checkers-vendor`, `amazon/wlan` and
  `amazon/bluetooth`). WSL Ubuntu 24.04 is on the build host; toolchain not yet
  installed (needs sudo).

### Bootloader facts that shape the plan

- amonet's LK (kaeru 2.0.0) does **not** implement `fastboot boot`; every test
  image has to be flashed. Use the `recovery` slot (16 MB) for Linux images and
  keep the Android `boot` untouched as the fallback. Backups of all three:
  `D:\platform-tools\echoshow\{recovery-twrp-cronos,boot-lineage-18.1-20260904-cronos,lk-amonet-cronos}.img`.
- `fastboot reboot` after `adb reboot bootloader` lands back in fastboot;
  `fastboot continue` boots normally.
- Kernel + initramfs must fit 16 MB: the LineageOS kernel is 7.4 MB gzip, so the
  initramfs has ~8 MB. Enough for Alpine's minirootfs (3.2 MB gz) plus the daemon
  (~4 MB gz, or lzma/xz which the kernel accepts). The real rootfs can live on
  `system`/`userdata` later; a self-contained initramfs is the first target.

### Status 2026-09-15 evening: steps 1–3 done in initramfs form

`tools/linux/` builds a boot image (LineageOS kernel + Alpine armv7 initramfs)
that, flashed to **`boot`**, comes up in 12 s with a root shell on USB serial,
joins the Wi-Fi network Android had saved, sets the clock, routes the
microphone, starts dropbear and the daemon; Home Assistant reconnected to the
same "Bench Show" device and a full voice turn ("Alexa, what time is it?")
worked with no Android userspace running. What it took, beyond the plan:

- The bootloader boots 64-bit kernels only from `boot`; from `recovery` even
  the stock LineageOS image dies before the first kernel message and the
  watchdog falls back to `boot`. Hours went into that. TWRP's kernel is 32-bit,
  which is why `recovery` works for it. Layout now: Linux in `boot`, TWRP in
  `recovery`, the LineageOS boot image on disk to restore Android.
- wpa_supplicant 2.11 cannot associate through the vendor driver (RSN
  capability mismatch, see `tools/linux/README.md`); 2.9 can.
- The "second capture channel" question is answered (hardware.md, "Two
  microphones"): the Show 5 has two microphones behind an FPGA on SPI, and the
  kernel driver averaged them into both slots because the device tree says
  `amzn,mic-downmix`. `tools/linux/patch-dtb.py` deletes the property from the
  eleven device trees appended to the kernel, `build-image.sh` takes the
  patched boot image through `KERNEL_IMAGE`, and the daemon now treats the
  stream as two microphones (`Mics = 2`) with an "All microphones" mix as the
  cronos default — what the factory driver did, one layer up where the
  canceller and anything smarter can see both channels. **Wanted next (user,
  2026-09-15): a two-microphone echo canceller** — the canceller today runs on
  one channel (`CenterMic`); running it on both, or on the mix with both as
  inputs, is the step past what the factory build did.
- A bare boot leaves the codec unrouted: init selected the DIF1 inputs and set
  the mic gain; the daemon does the same at capture start (`mic.routeInputs`),
  so the rootfs boot script leaves the codec to it.
- The daemon's firewall helper expected Android's iptables; off Android it now
  stands down.
- `reboot recovery` is marked in the RTC spare register, not MISC; a failed
  boot ends in the watchdog and a normal boot; the bootloader's boot counter
  (idme) eventually parks the unit in fastboot, which is reachable and fine.
- Custom boot logo (user request): the `logo` partition turned out empty; the
  picture is compiled into LK (hardware.md, "Boot logo"). `tools/linux/patch-lk-logo.py`
  builds an LK image with the TECHO5 mark (`logo/TECHO5_logo.png`) in the
  wordmark's place, keyed onto black. Flashed to `lk` (p3) 2026-09-15: bricked
  the boot (p3 is the stock LK that amonet's kaeru in `expdb` chains from; see
  hardware.md "Boot logo"), recovered with amonet fastbrick. DONE the same day
  the right way: `--in-place --colors 16` puts the mark into the wordmark's own
  6105-byte slot of the kaeru copy (`kaeru-techo5.bin` flashed to `expdb`
  from kaeru's fastboot); the bench unit boots through it. The daemon shows the same mark as a
  splash from its start until Home Assistant is listening, with the signal
  arcs pulsing outward both ways (`feature/display/splash.go`), so power-on
  reads as one identity through to the clock.

### Status 2026-09-15, later: step 5 done — persistent rootfs with slots

The bench unit now boots a persistent Alpine root filesystem from the eMMC
`system` partition (LineageOS's system is gone from that unit; the LineageOS
zip and boot image on disk can put it back through TWRP). Layout, tools and
the update/rollback story are in `tools/linux/README.md`; the short version:

- `system` (p12) is an ext4 **store** holding two rootfs slots as plain
  directories plus one-line state files. The kernel has no overlayfs or
  squashfs and repartitioning under amonet's LK is not a one-command undo, so
  directories on one partition are the A/B mechanism.
- The initramfs in `boot` **is the rescue**: it mounts the store, lets
  `slotctl next` pick the slot (taking one trial try), bind-mounts it and
  `switch_root`s in ~3.7 s. No store, no bootable slot, or a `rescue` marker
  → it stays up as before (USB shell, Wi-Fi, SSH, daemon from any slot).
- The rootfs runs busybox init: `etc/techo5/boot.sh` once (devices, USB
  serial, Wi-Fi from the saved credentials, NTP, dropbear, trial watcher,
  network keeper), `techo5-run` (the daemon) and `techo5-console` respawned.
  Root is read-only; state is on userdata (`/data/misc/techo5` unchanged, so
  Home Assistant saw the same device come back), the LineageOS `vendor` tree
  is copied into the slot, wpa_supplicant 2.9 and the rest come from `apk`
  run **on the device** (`mkrootfs.sh`, driven by `deploy-rootfs.sh`).
- **Trial semantics** mirror the daemon's: a new slot gets three boot tries
  and commits itself once the daemon has run five minutes; a slot whose daemon
  never settles reboots after 15 minutes to spend a try, and after the third
  the initramfs falls back to the last good slot. The daemon's own in-place
  updater keeps working inside a slot: `layout.Dir` follows the executable off
  Android, and Android properties (the trial marker) live as files in
  `/run/techo5/prop`. Android-only setup (resolver override, cert dirs, the
  vendor firewall) stands down when there is no `/system/bin/setprop`.
- Verified: slot a booted, joined Wi-Fi in 12 s, HA reconnected to the same
  "Bench Show" entry, an announcement played, the slot committed at 315 s;
  then slot b installed from the running system and the switch/commit cycle.

Lessons from the conversion: the rescue image must carry every library
wpa_supplicant 2.9 is linked against (dbus-libs, pcsc-lite-libs — the first
image without them silently had no Wi-Fi, diagnosed over the USB serial
console) and `libeconf` for mke2fs; under busybox init a respawn entry must
not `setsid` (it forks, the entry "exits", init spawns another shell every
cycle); an `&&` chain ending in a backgrounded reboot dies with the SSH
session.

### Steps

1. **First Linux boot** (no kernel build): LineageOS kernel + an initramfs built
   from the Alpine 3.24 armv7 minirootfs with a tiny init (`tools/linux/init`)
   that populates `/dev` (no devtmpfs in this kernel), **clears the recovery
   bootloader message in MISC first**, keeps a boot log in `/data/techo5-linux/`
   on userdata (readable from Android afterwards), paints the panel with
   `fbprobe` (clock ticking), offers a root shell over USB CDC ACM (Windows sees
   a COM port; RNDIS is out because Windows 11 24H2+ dropped it), insmods the
   vendor Wi-Fi module from the mounted Android system partition as a first
   probe, and reboots into Android after 15 minutes unless `/tmp/stay` exists.
   `tools/linux/mkimage.py` builds the boot image straight from the tarball
   (no root, no cpio binary). Built 2026-09-15 as
   `D:\platform-tools\echoshow\techo5-linux-test.img` (13 MB; not yet booted).
   Flash to `recovery`, `adb reboot recovery`. Failure mode: if init never runs
   (kernel panic before userspace), MISC keeps the recovery message and the unit
   loops into the test image — volume-down at power-up into fastboot, then
   `fastboot flash recovery recovery-twrp-cronos.img`. So: first boot with the
   unit at hand.
2. **Wi-Fi**: mount the LineageOS `system` partition read-only, `insmod` the
   vendor `mt76x8_wlan.ko` with `firmware_class.path` pointing at its `vendor/firmware`,
   `wpa_supplicant` + DHCP. Then SSH (dropbear) replaces the USB cable.
3. **Daemon**: run `techo5` from the initramfs — audio is already raw ALSA, so
   it should work unchanged; verify the DL1 hold trick and the amp safe-mode
   clear still apply on a non-Android boot.
4. **Display layer** in the daemon — started 2026-09-15: `hardware/screen` maps the
   framebuffer (three pages, page-flipped so a frame is never seen half drawn) and drives
   the backlight; `feature/display` draws the screens itself with the Go fonts — a big
   clock and date when idle, "Listening…" with a breathing bar, "Thinking…" with the
   transcript, then transcript and reply lingering twelve seconds after the turn — and
   exposes the screen to Home Assistant as a brightness-only light (`light.<name>_screen`,
   saved in the config as `screen`). The conversation publishes its phase and words on
   `voice.Changed` for it. Later the same day: `hardware/touch` reads the Goodix
   controller (protocol B) and reports taps and swipes in the landscape frame; a tap is the
   action button (start or end a turn; on a dark screen it only lights it), a vertical swipe
   is the volume a notch per step, with the level shown as it moves (`media.OnVolume`).
   `hardware/ambient` switches the light sensor on through hwmsensor and streams lux;
   with the "Screen auto-brightness" switch on, the room's light scales the backlight
   below the ceiling Home Assistant set, on a log curve with a running average. Still to
   do in this step: media metadata once the player carries any; a pairing UI when
   Bluetooth exists; then retire ShowAssist and the `echo-show` dashboard for this device.
5. **Rootfs on eMMC** — done 2026-09-15 (above): a persistent Alpine on
   `system` in two slots with a boot-count trial that fits the updater's trial
   semantics; the initramfs stays as the rescue environment. Still open: the
   daemon's manifest could carry a rootfs tarball so a slot update rides the
   same Home Assistant update entity as a binary update.
6. **Bluetooth** (earbuds, user requirement 2026-09-15) — **radio, stack and
   A2DP proven 2026-09-15 13:38**: the kernel rebuilt at the LineageOS commit
   with `CONFIG_BT` + `hci_vhci` (`tools/linux/build-kernel.sh`; the vendor
   Wi-Fi/BT modules still load), `cmd/btbridge` turning the vendor driver's
   `/dev/stpbt` into hci0 with the factory address, BlueZ 5.86 + bluez-alsa in
   the rootfs (`t5_bt_up`). A pair of earbuds and a phone paired;
   a tone played to the buds through `bluealsa:DEV=…,PROFILE=a2dp`. Quirks:
   inquiry/LE scans return nothing until hci0 is power-cycled once after
   bring-up (t5_bt_up does it); bluealsa must start after bluetoothd. **Daemon
   side done the same evening (v0.1.5-dev8/dev9):** `feature/btaudio` is the
   pairing agent (BlueZ D-Bus, `lib/bluez`) and the player (`lib/bluealsa`
   PCM → `hardware/speaker/sink.go`; the codec keeps getting silence for pace,
   the sink gets the audio, stereo, untuned, its own dB curve); pairing mode
   from HA (`switch.*_bluetooth_pairing`) or a swipe left on the clock lists
   audio devices on the screen, a tap pairs+trusts+connects; `sensor.*_bluetooth_audio`,
   reconnect/disconnect buttons; the remembered (or any bonded) device is tried
   at start-up. A connection the buds initiate gives a transport bluetoothd will
   not let us acquire (NotAuthorized), so after two refusals the daemon drops
   and remakes the link itself. Verified: HA announcement heard in the wireless
   earbuds (AAC, 48 kHz). Bonds and bluez-alsa state on userdata. Next:
   the settings sheet (below) absorbs the pairing page; media metadata.

   Settings sheet (user request 2026-09-15): swipe down from the top edge
   opens it — Bluetooth, brightness + auto, mute, wake word/volume, about
   (name, address, version, slot, restart). Vertical swipes elsewhere stay
   volume.
7. Second unit rollout with the installer rewritten for the Linux image.
8. **Echo cancellation, WebRTC grade** (from jxlarrea/lineageos-echo-show-camera,
   docs/echo-cancellation.md, measured on crown with the same FPGA front end):
   the loopback channels are a sample-aligned far end and WebRTC's full linear
   canceller on them removes 38–44 dB at ~13% of a core; low suppression with
   the extended filter is the double-talk balance that keeps the wake word
   working; analog mic gain must stay modest (our MICPGA 40) with the boost
   applied digitally after cancellation, because a clipped echo cannot be
   cancelled; averaging or beamforming mics this close buys nothing, so one
   mic + AEC + NS is the end state — which retires the two-mic canceller idea.
   DONE the same night as a helper process: `tools/aec/techo5-aec.cpp`
   (WebRTC AudioProcessing from Alpine's `webrtc-audio-processing-1`, full
   linear canceller, NS low, high-pass; blocks of 320 mic + 320 loopback
   samples over pipes, `hardware/mic/webrtc.go`), selectable against the
   built-in filter in Home Assistant, with fallback. Built for armv7 by
   `tools/linux/build-aec.sh` in WSL under QEMU. MEASURED (same announcement
   through the speaker, ERLE as the daemon reports it): WebRTC best 31.3 dB,
   25.5 dB at the end of the sentence; built-in best 26.6 dB, 18.8 dB at the
   end. But in use, the wake word over a playing station was markedly harder
   to catch with WebRTC (its suppressor clamps the talker in double talk, as
   the Show 8 author measured) and clearly better with the built-in linear
   filter, so **Built-in is the default**; WebRTC stays selectable. The
   canceller's "playing" gate had to drop to -76 dBFS with a one-second hold:
   a radio at a quiet volume sits at -50 dBFS on the loopback and flapped the
   -60 dBFS gate every second. The wake threshold gets 0.10 of slack while the
   canceller runs. "Music during a turn" is set to Pause on the bench. Oddity to look at: with earbuds connected and the
   codec fed silence, the built-in filter still reported 17–19 dB of ERLE, so
   the loopback channel carried something — check what the FPGA loops back
   when the DAC input is silence.

   **A2DP and Wi-Fi share the antenna (2026-09-15):** an idle A2DP stream
   held open dropped the Wi-Fi rate to 6.5 Mbit/s (a 74 MB tarball crawled at
   1 MB/min); with the stream paused the link sits at 72 Mbit/s and moves
   8 MB/s. The speaker sink now pauses bluez-alsa's transport after two
   seconds of silence and resumes on the next audio (`sink.go`); that is why
   Bluetooth discovery is also only on while pairing.
9. **Camera, later** (same repo): its kernel patches — Amazon's imgsensor
   struct layouts, the OV02B10 driver for cronos, mirror and timing fixes —
   apply to the tree we build and would give a sensor that streams into the
   ISP. Everything past that is Android (MediaTek's camera HAL, the display
   pipeline library, cmdq) and does not exist in our image; the MT8163 ISP has
   no V4L2 interface, so a Linux camera means driving the ISP pass-through
   from its ioctls and debayering in the daemon. docs/findings.md there is the
   only map of that path. First target if ever: one raw still frame, not
   video. CHECKED 2026-09-15: the kernel commit we build (8d928c5176cc)
   already has the OV02B10 driver, `CONFIG_CUSTOM_KERNEL_IMGSENSOR=
   "ov02b10_mipi_raw"` and the imgsensor fixes — that work was upstreamed —
   and the running image has `/dev/kd_camera_hw`, `/dev/camera-isp`,
   `/dev/camera-sysram` with the ISP probed at boot. So nothing to patch; what
   remains is all userspace: select the sensor through the imgsensor ioctls,
   configure the ISP pass-through, get one raw frame. STARTED 2026-09-15 late:
   `docs/camera-research.md` — `cmd/camprobe` drives the sensor driver from
   Linux (it selects and powers the OV02B10; the run failed only because the
   privacy latch was engaged: "Mute on will not init sensor"); the ISP driver
   exposes raw register access, IRQ waits and the IMGO DMA ring, but its
   register map is not in the tree — finding it is the next step. `build-kernel.sh` keeps
   KPATCHED=1 for a patched branch should that ever be needed.
   FIRST FRAME 2026-09-16: `cmd/camframe` grabs a full 1600x1200 raw Bayer
   frame from Linux. The sensor was silent only because nobody programmed the
   CMMCLK divider (Android does it from userspace); the receiver, TG and IMGO
   DMA are programmed through the ISP driver's register mmap from the genuine
   MT8163 libcamdrv sources (saved outside the repo, see camera-research.md).
   IN THE DAEMON 2026-09-16: `hardware/camera` streams on demand (TG frame
   counter for sync, three DMA slots, 800x600 RGBA with grey-world white
   balance, sensor stops 5 s after the last user, refused while muted);
   `feature/camera` serves http://<device>:8181/camera.jpg and /camera.mjpeg
   for Home Assistant's Generic Camera / MJPEG IP Camera integrations; the
   cameras page lists "This Show" first, and "show this show" puts the live
   view up. Later the same day: the Show is an ESPHome **camera entity**
   (`camera.<area>_<device>_camera`, stills and paced streams over the API,
   no URL setup; `component.Describer` puts the entry ahead of the library's
   entity list). Cost measured: the daemon sits at ~37 % of the two cores
   with the wake word running and ~58 % while the MJPEG stream is served
   (14 fps conversion plus ~4.5 fps JPEG); 40 % of the machine stays idle.
   Both done later that day: CAM_OUT_FMT 1 gives 10-bit packed frames, and
   stills are demosaiced at 1600x1200 (the stream stays 800x600).
   NOW PLAYING 2026-09-16: `lib/radiometa` resolves a station on
   iHeartRadio (v3 search, live-meta currentTrackMeta with
   defaultMetadata, trackHistory as the fallback while the live endpoint
   says 204/410) or TuneIn (Search/Describe) from its list name; `home`
   polls it every 15 s while the radio runs and the now-playing screen
   draws the cover, or the station logo on the right, or drawn notes/waves,
   behind station, song and artist. The bottom bar has drawn play/pause
   marks. `/screen.png?radio=<station>|stop` starts a station for a look.
   FIRST-RUN AND SETUP 2026-09-16 (night): a first-run card until tapped;
   new devices come up in Ember answering to "alexa" with the wake models
   shipped in the image; Wi-Fi setup on the screen (`lib/wifi` over
   wpa_cli, network list and keyboard pages from the Device tab, opened on
   its own when a device has no address 90 s after boot; the boot keeper
   holds off while `/run/techo5/wifi-setup` exists); the Bluetooth proxy
   scans through BlueZ on the Show (`hardware/ble/bluez.go`) but LE
   discovery reports nothing through the bridged controller yet, so it
   stays off by default; camprobe and camframe removed; docs/overview.md
   is the one-page state of the device.
   RELEASE v0.2.0 (2026-09-16 06:00): the first release carrying a rootfs
   tarball. The bench Show, asked through Home Assistant's update entity,
   fetched the 74 MB tarball from GitHub, installed it into slot b and
   committed after the trial — the reboot itself had to be done by hand,
   because the updater rebooted from a goroutine the supervisor's restart
   killed; fixed to reboot synchronously (goes out with the next release).
   LE SCANNING FIXED 2026-09-16: the empty BlueZ LE discovery was the
   firmware's Read Local Supported Commands reply — the MT7668 leaves
   octets 25-28 (the Bluetooth 4.0 LE commands) all zero, and the kernel
   builds the LE event mask from that table, so it sent `LE Set Event Mask`
   0x5d0 without LE Advertising Report or LE Connection Complete. The
   controller scanned and discarded every report; power-cycling hci0 did
   not help. `btbridge` now fills those four octets when the firmware
   leaves them empty. On the bench: 431 advertising reports in 10 s where
   there were none, and the proxy's rate sensor at ~33 advertisements/s.
   SECURITY TAB 2026-09-16: the images carried the maintainer's SSH public
   key (every install of a published rootfs trusted it), SSH was always on,
   and port 8181 served the camera and a state-changing screenshot page to
   the whole network with no login. Now `feature/security` owns all three:
   switches in Home Assistant (`ssh`, `camera_web_access`,
   `screen_web_access`) and on a sixth sheet tab, all off by default; echod
   starts and stops dropbear (`-s`, keys only) and opens 8181 only while a
   page is on. Keys arrive only through the `ssh_keys` action (public key
   lines, no options) into /data/misc/techo5/ssh, which /root/.ssh links to;
   the rootfs has no key and boot.sh no longer starts dropbear. The rescue
   init dropped its empty root password. Tested on the bench: keys action
   (good and a `command=` line refused), SSH off from Home Assistant with a
   session open (session kept, new logins refused), on again from the tab,
   8181 open/404/closed per switch, password logins refused.
   RESCUE HARDENED 2026-09-16: boot image rebuilt with the same kernel
   (techo5-linux-boot-sec.img, written with dd from the running system after
   a backup of the partition); rescue SSH is keys only (the image's key plus
   userdata's), no empty root password. A forced rescue boot also showed an
   older gap: the lease came after t5_wifi_up's wait, so rescue skipped SSH
   and the daemon and sat on the test screen reachable only over USB. The
   init now starts both from its wait loop whenever the address shows up.
   Verified: rescue boot with SSH and daemon up at 38 s, password logins
   refused, normal slot boot afterwards.
   TIMERS, ALARMS, SETTINGS SIDEBAR 2026-09-16: timers were only drawn on an
   LED ring the Show does not have; they now count down under the clock
   (soonest, a bar for what is left, "+n more") and ring on a full-screen
   page. `feature/alarm` adds alarms: set on the device (Alarms tab editor:
   hour, minute, day chips, repeat presets) or from Home Assistant
   (`alarm_set{time,days,label}`, `alarm_delete{time,label}`), and helpers
   followed with `alarms_follow{entities}` ("input_datetime.x" rings daily,
   a date+time once; "=input_boolean.y" arms it). They ring from the
   device's clock, skip anything more than 10 minutes stale (clock set by
   NTP after boot), turn one-offs off after ringing, and snooze 9 minutes.
   Entities: sensor next_alarm, buttons stop_alarm (also cancels a snooze)
   and snooze_alarm. `hastate` now keeps a follow list per owner, so the
   home feature rebuilding its list no longer drops anyone else's. The
   settings sheet moved its tabs into a left sidebar with Done at its foot;
   rows are 52 px with 40 px buttons (were 36/28, under 5 mm on this panel).
   Verified on the bench: alarm set from Home Assistant rang on the minute,
   stopped from the screen; snoozed from Home Assistant, shown as snoozed,
   cancelled with Stop.
   EXPOSURE 2026-09-16: the loop metered the plain mean of the frame, so a
   window or ceiling lamp set the exposure and a face went dark; the tone put
   the top percentile at white with nothing at black (darkest pixel 44/255,
   milky). Now `meter` averages an 8x6 zone grid of green samples weighted to
   the centre (x4) with zones over 870/1023 at 0.15; `convert` takes the
   darkest 0.1% as black (capped at 64 on the summed-green scale; a room's
   floor measured 43) and steepens gamma from 1/1.8 up to 1/2.7 when the top
   percentile is over 3x the central median. Synthetic backlit frame: metered
   262 against a plain mean of 564. Bench, face under an overhead light:
   readable face, dark furniture black (darkest 5/255).
   WI-FI BAND 2026-09-16: transfers to the bench crawled (1 MB/s in, 4 MB/s
   out, 2 Mbit/s tx rate while earbuds reconnected). Cause: the supplicant
   had picked HomeWiFi on 2.4 GHz from a different access point, where the
   chip's Bluetooth shares antenna and spectrum; Bluetooth off doubled it.
   The same network also offers 5 GHz at -27 dBm: 2.7 MB/s in, 11.9
   MB/s out with earbuds connected. `t5_wifi_prefer5` (network keeper, once
   a minute) moves a 2.4 GHz link to the same SSID's 5 GHz radio when the
   scan shows it at -70 dBm or better, on the running supplicant only, and
   backs off for 30 minutes if that does not associate in 30 s. Tested by
   forcing 2.4 GHz: back on 5180 MHz in 6 s with address and route intact.
   TIME ZONE FROM HOME ASSISTANT 2026-09-16: images baked the build PC's
   zone. Now /etc/localtime and /etc/timezone link to userdata, boot.sh
   creates them from /etc/techo5/default-timezone (UTC) when missing, and
   `feature/timezone` sends GetTimeRequest once Home Assistant subscribes.
   Home Assistant answers with a POSIX rule, not a zone name
   ("MST7MDT,M3.2.0,M11.1.0"); the daemon writes it as the footer of a
   minimal TZif v2 file (one transition in 1901 so the footer governs every
   later time), which Go and musl both apply, and sets time.Local. A zone
   name is still accepted and linked into zoneinfo. Tested on the second
   Show: rule applied 1.3 s after start, date prints MDT.
   SETTINGS TABS 2026-09-16: the sheet is four tabs — Device (volume and
   brightness with −/+ buttons, auto-brightness and microphone toggles, wake
   word, About, Restart), Bluetooth (connect/disconnect the remembered device,
   Pair opens the scan page), Cameras and Radio (rows with a button, paged
   past seven). The separate radio and cameras pages are gone. A swipe down
   from the top quarter opens it. `/screen.png[?sheet=<tab>|off]` on port
   8181 is a screenshot of the panel, for checking layouts from the PC.
   Later: tabs and buttons are bevelled blocks with a drop shadow (theme.go),
   and the Device tab has a Theme row cycling six palettes — Walnut, Slate,
   Midnight, Forest, Plum, Paper (a light one) — saved in the screen config;
   `?theme=<name>` on /screen.png switches it from the PC.
10. **Home on the screen** (2026-09-15 late): `feature/hastate` follows Home
   Assistant entities over the ESPHome state subscription; `feature/home`
   shows the weather on the clock and a radio page whose stations come from
   the house's input_selects and whose taps call `script.radio_play_on_speaker`
   through a device-initiated action (needs "Allow the device to perform Home
   Assistant actions" on the ESPHome entry). Wired from Home Assistant with the
   device actions `home_weather` and `home_radio`. Next page: **cameras** — fetch
   snapshots / the MJPEG stream from Home Assistant's camera proxy with a token
   handed over by an action, list the camera entities, tap to view live.
11. **Echo Dot (biscuit) on the same image**: same MT8163 family, the daemon
   already builds with the `dot` tag. Inventory one Dot (partition table,
   FireOS kernel version/config, Wi-Fi/BT modules and firmware, RAM/eMMC),
   first boot = rescue initramfs on the FireOS kernel flashed to recovery, then
   the slot store on its system partition. The Wi-Fi driver loading is the
   unknown that decides it.
12. **Settings screen by category** (2026-09-18): the tabbed sheet became six
   categories (Display, Sound & Voice, Alarms & Timers, Connections, Privacy &
   Security, General) on a rail, each on a card with soft shadows, switches,
   − / + steppers and pills that open a list of choices. Settings that were
   Home Assistant only (clock format, slideshow, wake sensitivity and sound,
   Bluetooth proxy, update channel) are on the screen too, plus two new ones:
   the alarm sound and the snooze length (also in Home Assistant). Taps are
   matched to where each control was drawn in the frame on the screen; long
   cards and lists scroll with a swipe. Cameras and Radio moved to a drawer
   brought in by a swipe from the right edge. Placeholder mode for published
   screenshots also hides weather and station names, which give away where a
   device is.

## M5 — Mainline kernel and Alpine Linux (Show 5)

The [mainline kernel option](../custom-kernel/7.2.6/README.md) builds Linux 7.2.6
with MT8163 platform support and an Alpine 3.24.2 armv7 root filesystem. The port
covers CPU cores, storage, USB console, Wi-Fi, display, touch, ambient light,
thermal management, speaker playback, and microphone capture. The application
uses the mainline identity, audio, backlight, touch, and light-sensor interfaces.

Matching kernel modules and firmware are packaged with each root filesystem.
Slot installation retains A/B trials and persistent application state. The boot
partition remains shared: selecting an older root filesystem does not restore
its kernel. An incompatible slot falls back to the rescue environment.
`CONFIG_COMPAT` supports the armv7 userspace. Dot and Spot retain their platforms.

The OV02B10 camera pipeline and hardware privacy control still require mainline
integration. Native Bluetooth uses btmtksdio with the existing BlueZ services.

## Ground rules

- Everything stays local; no cloud services.
- Upstream projects (EchoLocal, VACA, LineageOS device tree) are used under
  their licenses and credited. Changes worth sending upstream are proposed,
  not assumed.
