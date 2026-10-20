# TECHO5 mainline kernel option

This directory builds Linux 7.2.11 for the Echo Show 5 (`cronos`, board revision
`pvt_4_1`). MT8163 support is carried as a patch against the kernel.org release,
adapted from the [MT8163 kernel tree](https://github.com/bengris32/linux-mtk/tree/mt8163/7.0)
at commit `20951722df6ae1f6fecbfcea0a7d585118a06f3f`.

The Echo Show 8 2nd gen (`athena`) build of the same release is in [../athena](../athena/README.md).

## Hardware integration

The port includes CPU startup, USB serial, eMMC, Wi-Fi, Bluetooth, touch,
ambient light sensing, CPU frequency scaling, thermal management, display,
speaker playback, microphone capture, and camera capture. The rescue environment
and installed application use Alpine armv7. The privacy driver exposes the hardware mute latch
and button. Muting stops camera capture as well as cutting microphone input. The camera driver
provides the OV02B10 sensor through V4L2, and develops frames on the ISP for the application
(see "Camera" below).

- Secondary CPUs use Amazon's `mt-boot` sequence: PSCI records the entry point,
  then the kernel powers the CPU through the SPM. USB requires MUSB PIO mode.
- The MT7668 driver uses the GPIO descriptor and cfg80211 interfaces provided by
  Linux 7.2. Bluetooth uses `btmtksdio` with the MT7668 firmware.
- Goodix touch uses GPIO 122, which maps to EINT 123. VMCH supplies its analog
  power and VIO18 supplies its I/O. The application retains unchanged coordinates
  between contacts, as required by evdev's multitouch protocol.
- The ST7701S panel uses `mediatek-drm` and a portrait 480x960 XRGB8888 framebuffer.
  Renderers only write alpha when the framebuffer declares a transparency channel.
  The display PWM drives pin 59, with brightness scaled to the backlight's range.
  Kernel console output and boot diagnostic graphics are disabled on the panel.
  DRM resets the overlay during handover and initializes gamma bypass and dithering
  before scanout. The application avoids redundant pan requests on a single buffer.
- The sound card connects the AFE to the TAS5805M amplifier and SPI microphone
  FPGA. Amplifier configuration uses the vendor's MONO_MINI table, including its
  delay instructions. The application discovers streams by card and stream name.
- FPGA initialization enables VCAMD, VCAMAF, and VCN18 and sequences ADC reset
  before loading firmware. VIBR supplies the ADC. Revision 208 uses a twelve-byte
  status frame. I2S1 and MCLK supplies are shared through DAPM.
- Capture uses four-channel S24_3LE at 16 kHz: channels 0 and 1 are microphones,
  and channels 2 and 3 are playback loopback. The PCM buffer has 257-frame periods.
  Capture teardown cancels the reader before releasing runtime memory.
- CPU temperature uses the enabled AUXADC, and radio temperature comes from the
  Wi-Fi firmware. The application maps both to its Home Assistant sensor names.
- Factory identity is read through vendor IDME or the device tree, allowing the
  application's Home Assistant API to start with either kernel.

## Board wiring

GPIO numbers and interrupt numbers use different namespaces on MT8163.
The mappings below come from the pvt_4_1 vendor tree and MT8163 pin controller.

| Function | GPIO / pin function | Interrupt or supply |
| --- | --- | --- |
| Touch interrupt / reset | 122 / 123 | EINT 123; VMCH and VIO18 |
| Volume up / down | 36 / 37, active low | EINT 47 / 48 |
| Camera cover | 142, active low | EINT 21 |
| PMIC interrupt | 2 | EINT 24 |
| Backlight | 59, DISP_PWM function 1 | PWM controller 0 |
| Panel reset | 83, bootloader state retained | VGP3 and VIO28 |
| Amplifier power-down | 35 | TAS5805M at I2C2 address 0x2c |
| ADC master clock | 6, I2S1_MCK function 4 | 24.576 MHz; VIBR |
| Speaker I2S data / word / bit clock | 72 / 73 / 74, function 4 | I2S1 |
| FPGA reset / configuration done | 51 active low / 49 | VCAMD, VCAMAF, VCN18 |
| ADC reset / microphone enable | 24 active low / 46 active high | FPGA initialization |
| SPI chip select / clock / MISO / MOSI | 53 / 54 / 55 / 56, function 1 | SPI controller native pads |
| Wi-Fi power | 32 and 28, active high | MSDC2 pins 85–90; VIO18 |
| Privacy enable / button / state | 27 active high / 47 active low / 48 active low | EINT 15 (button), EINT 16 (state) |
| Ambient light | I2C0 address 0x44 | VCN28 |

The camera uses reset pin 22, power-down pin 23, clock pin 119, and CSI pins
101, 102, 105, and 106. VCAMA and VCAMIO supply the sensor. The capture driver
preserves the ISP's IMGO FIFO thresholds and captures packed RAW10 frames, which
the application reads through V4L2.

## Camera

`patches/mt8163-cronos-camera.c` drives the OV02B10 through SENINF, TG1 and IMGO (pass 1). It
hands the application packed RAW10 frames through V4L2 (`read()`). It also develops a frame on the
ISP's pass 2 for the application: `CRONOS_IOC_DEVELOP` on the same node.

The ISP's pass 2 does the following:

- black level and per-channel gains (OBC, PGN);
- the demosaic (CFA);
- the gamma curve (GGM);
- BT.601 video range (G2C);
- 4:2:2 (C42).

The frame goes through in three tiles from a static tile table. The tone is the application's
(black, white balance, the stretch to white, the curve), passed with each frame. The node's
`iommus` in `config/cronos-techo5.dtsi` include the IMGI and IMG2O ports for it.

Pass 2 feeds the MDP directly (route A): its output leaves through the ISP's MDP crop into
CAMIN, RSZ1 (passed through) and WROT, which writes the caller's whole I420 itself. The link
carries the chroma around 128, so WROT's own 4:2:0 filter averages each two lines as the cores
would. Pass 2 runs as three tiles, one start each, with the MDP set up for each tile before it
starts; the table and the values are the ones the unit's own DpFramework computes for this path.
If anything fails, the driver runs pass 2 again on route B, and keeps route B until the stream
restarts.

On route B, the MDP converts the ISP's YUYV memory to memory: RDMA, RSZ1 and WROT, in two tiles.
WROT writes the luma straight into the caller's buffer and the chroma as 4:2:2 planes, and the
cores average the chroma into 4:2:0 on NEON. They have to, because IMG2O's chroma is two's
complement. If the MDP fails, the cores make the whole I420.

The node names the MDP's registers, its clocks (including the link's gate, `mdp-camin`) and
resets, mmsys, the display's mutex controller and larb0. Every route gives the same bytes as the
cores' conversion. The parameters under `/sys/module/mt8163_cronos_camera/parameters/`:

- `link` set to 0 keeps route B.
- `mdp` set to 0 has the cores make the I420.
- `times` has the last frame's steps, in microseconds.

Pass 1 and pass 2 share the CAM's control registers and cannot move data at once. So pass 2 runs
in the sensor's vertical blanking, with pass 1's registers double-buffered (SEL bit 4) for the
5.4 ms it takes. While frames are being developed, the driver holds the sensor's frame at 1440
lines (25.7 a second) to make that blanking. It goes back to the sensor's own length a second
after the last one.

`research/mt8163-isp-pass2.md` has the register map, the measurements and the comparison with the
application's CPU develop ("Stage 4" for the MDP, "Stage 5" for the direct link). `isp-user/` has the tools.

## CPU power

There are two mechanisms; `research/mt8163-cpuidle.md` has the measurements.

Frequency scaling is `mtk-cpufreq` with schedutil. The root filesystem's `boot.sh` sets schedutil's
`rate_limit_us` to 10 ms (the kernel's default is 1 ms, since the driver declares no transition
latency). Every change below 1133 MHz swings the sym827 up to the 1.2 V of the 1092 MHz intermediate
clock and back over I2C. At 1 ms that was 4 000 interrupts a second and 13-15% of a core in
`sugov:0` at idle; at 10 ms it is a few hundred and about 2% (section 10 of the research note).

- **Hotplug.** `mt-boot` (`patches/mt8163-boot.c`) powers an offline core's domain down through the
  SPM. echod's `hardware/cpuplug` keeps two cores up and takes cpu2-3 down when the load is low.
- **SODI** (screen-on deep idle, `patches/mt8163-sodi.c`, installed as `drivers/cpuidle/mt8163-sodi.c`).
  The SPM's PCM, loaded with Amazon's GPL `sodi_binary`, powers CPU0, the cluster and MCUSYS down while
  the display runs, once CPU0 is the only online core. It wakes on the tick broadcast (the GPT), EINT
  or CIRQ. It is **off by default**, and no cpuidle driver registers until it is enabled:

  ```sh
  echo timer > /sys/devices/system/clocksource/clocksource0/current_clocksource
  echo 1 > /sys/module/mt8163_sodi/parameters/enable
  cat /sys/module/mt8163_sodi/parameters/status
  ```

  The clocksource must not be the arch timer. Its counter is in MCUSYS, and the secure monitor
  restores it from a stale value after each power-down, so the driver waits (`blocked_clock`) until the
  GPT keeps time. DRAM never enters self-refresh, and the PCM does no voltage scaling.

  It works, with 15 entries a second and 95% residency over a 30 minute soak with the daemon stopped,
  and no errors. But it never applies in normal use: echod's load needs two cores, and on one core
  the microphone FPGA overruns. That is why it stays off and cpuplug keeps two cores.

## Build inputs

`k6.sh` runs the arm64 `techo5-kbuild` container with the `techo5-k6` volume mounted
at `/src`. Build the container from `docker/Dockerfile`. The kernel source lives
at `/src/linux-7.2.11`; `KDIR` can select another source directory inside the volume.
`config/cronos.config` is the kernel configuration, and `prepare.sh` installs the
platform patch and board support. Generated codec sources belong in `codec-work/`
and build products in `out/`; both directories are ignored.

The image assembler requires a locally supplied `boot-lineage.img` with a
compatible microloader and boot header. Firmware and calibration remain subject
to the notices in `initramfs/firmware/LICENSE` and `../../NOTICE`.
Binary firmware is not stored in the repository. `tools/prepare-firmware.py`
extracts FPGA firmware from that vendor image, generates the amplifier settings
from the pinned vendor source, and copies locally supplied Bluetooth firmware
and regulatory database files into the ignored `out/firmware/` directory.
The bundle builder verifies their checksums before packaging them.

## Build

Initialize the pinned source dependencies from the repository root:

```sh
git submodule update --init --recursive
```

`wifi-amazon/` provides the unmodified MT7668 driver. `sources/amazon-kernel/`
provides the vendor codec and sensor build inputs and hardware reference sources. Local
adaptations are applied from `patches/` and `wifi/patches/` without modifying
these submodules.

From this directory, prepare the source archive:

```sh
./k6.sh 'set -e
    mkdir -p /src/downloads
    cd /src/downloads
    curl -fLO https://cdn.kernel.org/pub/linux/kernel/v7.x/linux-7.2.11.tar.xz
    echo "fd0fc479f983a8c05313f13e9c36330fe6651cf725396748c40d5cd93fa1e6a4  linux-7.2.11.tar.xz" | sha256sum -c -
    tar -xJf linux-7.2.11.tar.xz -C /src'
python3 patches/port-aic3101.py sources/amazon-kernel/sound/soc/codecs/tlv320aic3101.c \
    sources/amazon-kernel/sound/soc/codecs/tlv320aic3101.h codec-work
python3 tools/prepare-firmware.py --boot boot-lineage.img \
    --bluetooth /path/to/mt7668pr2h.bin --regulatory /path/to/regulatory-database
./k6.sh 'sh /prepare.sh'
./k6.sh 'set -e
    cd /src/linux-7.2.11
    make -s ARCH=arm64 O=out -j10 Image.gz dtbs modules
    cp out/arch/arm64/boot/Image.gz /out/Image.gz
    dtc -I dtb -O dtb -p 262144 -o /out/cronos-display.dtb \
        out/arch/arm64/boot/dts/mediatek/mt8163-amazon-cronos-techo5-v41-display.dtb'
./k6.sh 'sh /host/wifi/build.sh'
python3 ../../tools/fetch-inputs.py --device show
sh initramfs/build-rescue.sh
./build-rootfs.sh
python3 mkboot-injected.py --lineage boot-lineage.img \
    --ramdisk out/initramfs-rescue.xz --kernel out/Image.gz \
    --dtb out/cronos-display.dtb --cmdline-append 'clk_ignore_unused pd_ignore_unused' \
    -o out/techo5-mainline.img
```

The boot image must fit the 16 MiB boot partition. The padded device tree leaves
room for bootloader updates. `CONFIG_COMPAT` supports the armv7 rescue userspace.
Install `out/techo5-rootfs-mainline.tar.gz` through `slotctl install` to update the
inactive Alpine slot. The root filesystem and rescue environment use Alpine
3.24.2, and the slot includes modules for the matching kernel release. Boot stays
in rescue when a selected slot lacks those modules. Application state, network
credentials, and firmware calibration remain on the device's data partition.

## Updates

Releases of this build are published to a private channel rather than to GitHub, because the image
carries firmware taken from the unit's own boot image. `release.sh <version>` builds the rescue
image, the root filesystem and the boot image, signs a manifest with the key at
`~/.config/techo5/release-key`, and copies the release to Home Assistant's `/config/www/techo5`,
which Home Assistant serves at `/local/techo5`. A daemon built by it follows that channel and trusts
only that key, so Home Assistant's update card offers these releases and installs them into the
spare slot. Use versions like `v1.1.1-mainline.1`. The channel's address and the copy's destination
are set in `~/.config/techo5/release.env` (`CHANNEL`, `DEST`, and `DEVICE` for the kernel check).

An update writes the root filesystem only. A release on a new kernel needs the boot image flashed
first, by fastboot as below; `release.sh` checks the unit's running kernel and refuses otherwise.
The first build that follows the channel is installed by hand (`slotctl install`), over SSH or the
USB console.

A new kernel goes on in three steps, so the unit never boots a kernel whose modules its slot lacks:
`release.sh --dry-run <version>` builds and signs the release in `out/release/<version>` without
publishing it; its root filesystem goes into the spare slot by hand (`slotctl install`, which does not
reboot); then the boot image is flashed (`tools/kernel-recovery.py flash`), and the unit restarts into
both. `release.sh --publish <version>` then publishes that same staged release, once the unit runs its
kernel, so Home Assistant offers the next one.

The Echo Show 8 2nd gen has its own release script, `athena/release.sh`, with the same settings and
key and a channel of its own (`$CHANNEL/athena`): see [athena/README.md](../athena/README.md#updates).

### 64-bit userspace

The kernel is 64-bit; the root filesystem has been Alpine armv7 with a 32-bit daemon, as on every
other TECHO5 board. `T5_ARCH=aarch64` builds it 64-bit instead: Alpine aarch64, the daemon and its
tools for arm64, and the helpers from `bin/*-arm64` (`techo5-aec` built against Alpine's aarch64
webrtc-audio-processing, `techo5-librespot` built for `aarch64-unknown-linux-musl`, statically). Its
Alpine base and the Alpine 3.12 wpa_supplicant set come from `$TECHO5_INPUTS/aarch64`.

```sh
T5_ARCH=aarch64 custom-kernel/cronos/release.sh --dry-run v1.2.19-mainline.4
```

The release ships the daemon as `echod-arm64` and publishes the slot under the 64-bit key, which a
64-bit daemon asks for, and under the 32-bit one too, so a unit still on the 32-bit userspace moves
over with an ordinary update. Measured 2026-10-17 against the armv7 build on the same units: the
wake word takes 10-15% less time a frame, and the daemon about a fifth to a third less CPU.

### GPU

The Mali-T720 MP2 runs on panfrost (`CONFIG_DRM_PANFROST`, and `CONFIG_DEVFREQ_THERMAL` so
mt8163.dtsi's gpu-thermal zone can slow it), at 299 to 520 MHz. The 64-bit userspace carries the same
lean Mesa and `techo5-gpu` as athena's (see [athena/README.md](../athena/README.md#gpu)), and the
helper draws the screen: the 960x480 canvas turned onto the 480x960 panel with page flips. The camera
stays on the cores here: the T720 (OpenGL ES 2.0 only) takes about 110 ms to develop a 1600x1200 frame
and as long again to bring it back, where the cores' bands manage five frames a second.

The JPEG encoder at 0x17003000 (`cronos-techo5.dtsi`, mainline's mtk-jpeg) encodes the camera's
pictures: 720p at about 50 frames a second, where the cores took about 300 ms for a 1600x1200 frame.

### H.264 and H.265 decoder, H.264 encoder

The MT8163's H.264/H.265 decoder (0x16020000) and H.264 encoder (0x17002000) have no co-processor: MediaTek's own
libraries (`libvcodecdrv.so` and the rest, 32-bit Android) parse the stream and program the blocks
from the AP. `patches/mt8163-vcodec.c` (a module, loaded by `boot.sh`) is their kernel side,
`/dev/Vcodec` with the ABI of Amazon's GPL driver: the hardware lock, the interrupt waits, buffers
from the DMA API at IOMMU addresses (the two blocks are IOMMU clients on larb1 and larb3), and one
call of ours, `VCODEC_SHIM_ALLOC`, that the stand-ins for Android's ION and M4U libraries allocate
through (dma-bufs, write-combined or cached). `vcodec-user/` runs the libraries on the 64-bit
userspace: the Android 11 runtime (AOSP), our stand-ins, and two filters, `vcodec-dec` (H.264, or
H.265 with `-c hevc`, in; NV12 out) and `vcodec-enc` (I420 in, H.264 out). The image carries those (`vcodec-user/tools/bundle.sh`,
under `/usr/local/lib/techo5-vcodec`); MediaTek's libraries are the unit's own, read from its
`/vendor/lib`, and not in the image.

Measured on a unit: the decoder takes 720p H.264 High in about 3 ms a frame, bit-exact against
ffmpeg, and H.265 Main in 2.4 ms at 720p and 7.1 ms at 1080p, bit-exact too (the same library and
block, HEVC_VLD at 0x16028000; no kernel change: see "H.265" in the research note); the encoder
1600x1200 High in about 20 ms. echod's video player (`feature/video`, `hw_vcodec.go`) has
ffmpeg take the stream apart, `vcodec-dec` decode the picture and the GPU convert, scale and turn
each frame onto the panel (`techo5-gpu`'s `OP_PRESENT_YUV`): 720p30 plays in real time on about 0.7
of the four cores, where the cores alone took 1.8 of them and fell behind (decoding was 21 ms a
frame, the conversion to the panel 60). H.265 goes the same way (ffmpeg copies it out with
`hevc_mp4toannexb`; the bundle's `etc/codecs` says its `vcodec-dec` takes it): 1080p30 on about 0.65
of a core above idle, where the cores took 2.9 and fell behind. The camera stream (`feature/camstream`) is the sensor's
whole 1600x1200 at 12 Mbit/s, encoded by `vcodec-enc`. Each frame is developed into I420 on the
ISP and the MDP (see "Camera"): 25.3 frames a second on about 0.7 of a core. If the ISP is not there or fails, the
cores develop it instead: 29 a second on about 2 cores. `research/mt8163-video-codec.md` has the
way there.

## Kernel recovery

The boot partition is shared by both Alpine slots. Slot rollback cannot recover
a kernel that fails before rescue starts. Keep a working boot image on the host
and use payload fastboot to restore it if necessary.

From this directory, save an image already verified on the unit:

```sh
python3 tools/kernel-recovery.py prepare --known-good out/working-boot.img \
    --directory ../../backups/kernel-recovery
```

After entering payload fastboot, use `flash` for a new image or `restore` for the
saved image. Both verify the recovery bundle and image layout before writing only
the boot partition. Select the unit explicitly with its fastboot serial:

```sh
python3 tools/kernel-recovery.py flash --image out/techo5-mainline.img \
    --directory ../../backups/kernel-recovery --serial "$DEVICE_SERIAL"
python3 tools/kernel-recovery.py restore \
    --directory ../../backups/kernel-recovery --serial "$DEVICE_SERIAL"
```

`--dry-run` validates the files and prints the commands without accessing the
device. The bundle stays in the ignored `backups/` directory and must be kept
outside the device being updated. Recovery is manual; these commands do not
provide automatic kernel rollback.

Rescue retains the upstream status log, persistent boot-stage records, and fallback
screen. Normal boot stays quiet and switches directly into the selected slot.
