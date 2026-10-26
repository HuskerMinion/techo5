# Mainline kernel for the Echo Show 8 2nd gen (athena)

The kernel TECHO5 boots on the Echo Show 8 2nd gen ("athena", MediaTek MT8183): Linux 6.18.56 from
kernel.org with the patches in this directory, the board device tree and an ALSA machine driver.
`build.sh` turns them into the boot image the installer writes (`boot-athena.img`): Image.gz with the
DTB appended, an Android boot image v1 header, and a ramdisk holding only the Wi-Fi/Bluetooth
firmware files. `uname -r` says `6.18.56-techo5-athena`.

Everything here is source. The one thing the kernel needs that is not here is the speaker amp's
tuning (see [The amp tuning slot](#the-amp-tuning-slot)): it comes from each unit's own stock
firmware when TECHO5 is installed, and is never shipped.

## Upstream base

- `https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-6.18.56.tar.xz`
- sha256 `4633bbda2ab95eaf2a7af4041236451d6759c72e6946265531af4b3aacefbf89`

## Building

On Linux or in WSL (Ubuntu 24.04 tested), with an aarch64 cross GCC (`gcc-aarch64-linux-gnu`; GCC
13.3 tested) and the usual kernel build tools (`build-essential bc bison flex libssl-dev libelf-dev
python3 curl xz-utils`):

    tools/linux/athena/kernel/build.sh

It downloads the kernel tarball and the Wi-Fi firmware (both checked against the sha256 sums in the
script), unpacks a fresh tree under `$WORK` (default `~/techo5-athena-kbuild`), applies the patches
below in order, builds `Image.gz` and the DTB, packs the boot image and checks it with
`check-boot.py`. Output in `$WORK/out`:

| File | |
| --- | --- |
| `boot-athena.img` | the boot image (header v1, kernel + DTB, firmware ramdisk) |
| `Image.gz`, `athena.dtb`, `ramdisk.cpio.gz` | the parts |
| `kernel.config` | the full configuration used |
| `linux-6.18.56-techo5-athena-source.tar.xz` | the whole patched source tree the image was built from, with `kernel.config` in it as `techo5-athena.config`: what a release publishes beside the boot image, for the GPL. Built reproducibly, checked for anything of the build computer (paths, user names) and by `check-source.py` |
| `SHA256SUMS` | |

Environment: `WORK`, `OUT`, `JOBS`, `CROSS_COMPILE` and `KTREE` (use an unpacked linux-6.18.56 tree
in place instead of a fresh one, for quick rebuilds; every step is idempotent). The build is
byte-for-byte reproducible with the same toolchain (and the same zlib for the ramdisk's gzip). It
sets
`KBUILD_BUILD_TIMESTAMP`, `KBUILD_BUILD_USER`, `KBUILD_BUILD_HOST` and `KBUILD_BUILD_VERSION` to
fixed values and maps its own directory out of the debug info (the linker's build-id in the Image
hashes it), so the output does not depend on where it is built.

## Patches

Applied by `build.sh` in this order. The Python patchers each carry a docstring with the full
reasoning; each one checks its anchors and is a no-op on a tree it has already patched.

| Patch | What and why |
| --- | --- |
| `0001-mt8183-afe-leave-cache-only-before-enabling-clocks.patch` | mt8183 AFE: leave regmap cache-only mode before enabling the audio clocks. The clock gates go through the same syscon regmap, so the writes were dropped and the AFE never ran when the bootloader had left its clocks off. |
| `patch-kbuild.py` | Kconfig `SND_SOC_MT8183_ATHENA` and the Makefile lines for the machine driver and the DTB. |
| `patch-afe-di2.py` | mt8183 AFE: the second I2S2 data input (DI2) and VUL12's 4-channel mode, for athena's 4 microphones. Also fixes an upstream bug: UL1_CH2's "I2S0_CH2" switch wrote channel 1's register. |
| `patch-afe-awb-dl1.py` | mt8183 AFE: routes from DL1 into UL2 (the AWB memif), so its "UL2_CH1 DL1_CH1" and "UL2_CH2 DL1_CH2" switches exist and the machine driver's `DL1_AWB_Record` (hw:0,1) records what DL1 sends the amplifier: the echo reference, as on Amazon's kernel. They have to be the AFE's: it makes its mixers' controls at platform probe, before a card's routes are added. |
| `patch-usb-force-vbus.py` | mtu3 + T-PHY: a `mediatek,force-vbus` property. athena senses VBUS on a GPIO, so neither the PHY nor the controller sees a session and USB device mode never comes up without it. |
| `patch-tcs3400.py` | tcs3472 IIO driver: support the AMS TCS3400 light sensor (no command-type field, its own gains, integration step and interrupt clear) and the `ams,tcs3400` compatible. |
| `patch-btmtksdio-bdaddr.py` | btmtksdio: program a public address set through mgmt again on every power-on, since the controller can lose it while off. |
| `patch-btmtksdio-p192.py` | btmtksdio, MT7663 only: report P-256 link keys as P-192. The chip encrypts with E0 anyway, and the core dropped the link right after pairing ("Missing AES-CCM usage"). |
| `patch-btmtksdio-late-register.py` | btmtksdio: register the driver 8 s after boot, so that Wi-Fi starts the shared MT7663 alone (they raced on about one boot in three and both failed). |
| `patch-mt7663s-firmware-hold.py` | mt7663s: load the four MT7663 firmware files at init, while the initramfs is still the root, and keep them, so that later requests (Wi-Fi's init work, Bluetooth at 8 s) are served from memory after the real root has replaced the initramfs. See [Firmware](#firmware). |
| `patch-display.py` + `panel/0001`-`0004` + `src/panel/` | Panel tables for athena's ILI9881C and JD9365 panels (bindings and drivers); `panel-athena-select.c` picks the unit's own panel at boot from the name LK passes, so one image drives every screen; and mediatek-drm removes the bootloader's simple-framebuffer when it binds, so its fbdev emulation becomes `/dev/fb0`. |
| `patch-tas5805m-dsp-slot.py` | tas5805m: take the DSP configuration from the device-tree slot, and keep the sound card up without one (below). `tas5805m-dsp-slot.diff` shows the change it makes, for reading. |
| `patch-cci-sram.py` | mtk-cci-devfreq: SRAM voltage limits for MT8183, the same as mediatek-cpufreq has. athena gives the CCI an SRAM supply (Fire OS sets the CPU SRAM rails in software), and without limits the driver asked for a range with a maximum of 0 V and the kernel stopped at boot. |
| `patch-thermal-devinfo.py` | auxadc_thermal: when the efuse reads as zeros (Amazon's secure firmware hides it from Linux), take the thermal calibration from the copy LK puts in `/chosen` as `atag,devinfo`. Without it the driver used default values and read several degrees off. |
| `patch-camera.py` | The front camera, OV13855 -> CSI-2 D-PHY -> seninf -> CAMSV -> memory. Mainline has no MediaTek camera receive path, and the MT8183 ISP itself is only ever programmed by the SCP firmware, so CAMSV writes the raw frames and echod develops them. Adds `src/camera/isp` (the seninf and CAMSV drivers from [vpavlin/mt8183-camera](https://github.com/vpavlin/mt8183-camera), with four changes marked `athena:`), the `mediatek,mt8183-csi-rx` compatible in the mainline CSI-2 PHY driver, and `src/camera/ov13855.c`, a sensor driver with Rockchip's register tables. 2112x1568 binned at 30 frames a second, packed 10-bit. |

New files, copied into the tree:

- `dts/mt8183-amazon-athena.dts`: the board device tree. GPIO, regulator and bus details carry
  comments on where each value came from (stock device tree, read-only probe of a stock unit, or a
  guess marked TODO). I2C2 and I2C4 run bit-banged on their pads: the hardware controllers are
  shared with the SCP and time out under mainline.
- `src/mt8183-athena.c`: the ALSA SoC machine driver (MT8183 AFE + TAS5805M amp on I2S1 +
  TLV320ADC5140 4-channel mic ADC on I2S2, and the AFE's DL1 write-back as the echo reference),
  GPL-2.0. PCMs: hw:0,0 `Playback_1` (DL1), hw:0,1 `DL1_AWB_Record` (the reference), hw:0,2
  `Capture_1` (the four microphones, UL1).
- `athena_defconfig`: the full kernel configuration.

## Display panels

athena units ship with several panels. Only the ILI9881C on BOE glass
(`amazon,athena-ili9881c-boe`, the compatible in the DTS) has been tested on a unit. The tables for
the other variants (four more `amazon,athena-ili9881c-*`, five `amazon,athena-jd9365-*`) are in the
panel patches, taken from Amazon's GPL-2.0 kernel for the device (its LCM drivers). At boot
`src/panel/panel-athena-select.c` sets the panel node's compatible to the unit's own panel, by the
LCM name LK leaves in `/chosen` `atag,videolfb` (or the `jd936x_id=` nibble); the node carries the
JD9365 driver's supplies too (see the comment at `&dsi0`). All the panel init tables, the tested one
included, are taken from that kernel.

## GPU

The Mali-G72 MP3 runs on mainline's panfrost (`CONFIG_DRM_PANFROST=y`, `mediatek,mt8183b-mali`), as
on the MT8183 Chromebooks: `/dev/dri/renderD128` beside mediatek-drm's `/dev/dri/card0`. devfreq
scales it along mt8183.dtsi's OPP table, 300 to 800 MHz, with vgpu and vsram_gpu following (625 and
850 mV at 300 MHz, 825 and 925 mV at 800 MHz, read on a unit). The DTS also puts it in the die's
thermal zone beside the two CPU clusters (a power model and a third cooling map on the 80 C trip),
so the power allocator slows it when the chip is hot, as it does the CPUs. Nothing in the kernel
uses it by itself; a userspace with Mesa's panfrost driver can.

## Firmware

The MT7663 Wi-Fi and Bluetooth drivers load their firmware from `/lib/firmware`. `build.sh` fetches
it from linux-firmware at tag `20260916`
(`https://git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git`) and checks the sha256
sums:

- `mediatek/mt7663_n9_rebb.bin`, `mediatek/mt7663_n9_v3.bin`, `mediatek/mt7663pr2h.bin`,
  `mediatek/mt7663pr2h_rebb.bin`

The files are MediaTek's, under linux-firmware's `LICENSES/LICENCE.mediatek`, which does not allow
them to be distributed as part of the Linux kernel. So they are not linked into the kernel
(`CONFIG_EXTRA_FIRMWARE` is empty): they ship as separate files in the boot image's ramdisk, a
gzip'd newc cpio (`mkfwcpio.py`, deterministic) holding `lib/firmware/mediatek/...` and the
license text as `lib/firmware/LICENCE.mediatek`, and nothing else.

The ramdisk has no `/init`. The kernel unpacks it as its initial root, logs "check access for
rdinit=/init failed: -2, ignoring", and goes on to mount the real root (`root=` from LK's command
line) and run `init=/init` there, as with no ramdisk. The firmware loader waits for the initramfs
to be unpacked before it looks (`wait_for_initramfs()` in 6.18's loader), so requests made during
the initcalls find the files. Once the real root is mounted they are gone from view, so
`patch-mt7663s-firmware-hold.py` loads all four at the Wi-Fi driver's init and keeps them; the
firmware loader serves later requests for the same names from that copy.

## The amp tuning slot

The TAS5805M amp needs a register blob (made with TI's PPC3) holding the speaker tuning. Amazon's
blob is in each unit's stock firmware and is not redistributable, so the published boot image has an
empty slot for it in the device tree, and the TECHO5 installer fills it from the unit itself.

The amp node carries `techo5,dsp-config`, exactly 8192 bytes:

| Bytes | |
| --- | --- |
| 0..7 | magic, ASCII `T5DSPCF1` |
| 8..11 | N, u32 little-endian: the data length (0 in the published image) |
| 12..43 | SHA-256 of the data (zeros when empty) |
| 44..44+N-1 | the data, in the mainline tas5805m blob format (register, value byte pairs) |
| rest | zeros |

The magic occurs exactly once in the boot image (the driver keeps its copy XOR'd, and
`check-boot.py` checks for it). The DTB is appended to Image.gz uncompressed, so an installer finds
the slot by its magic, writes N, the hash and the data in place (the size does not change) and then
recomputes the boot image header's SHA-1 id (`pack.image_id()`).

At probe the patched driver uses, in order: a valid filled slot (magic, 0 < N <= 8148, N even,
matching SHA-256); else a `tas5805m_dsp_athena.bin` firmware file, as upstream (from
`ti,dsp-config-name`; nothing provides one in TECHO5); else nothing. Upstream fails the probe when
the firmware is missing, which would take the whole sound card down, microphones included. Patched,
the codec still registers, so the card and the microphones work, but the amp is never started: no
DSP configuration is sent and it is never put into play mode, so the speaker stays silent and the
kernel logs "no DSP configuration (-2): speaker output stays off".

`dsp_slot.py` makes the empty slot that the DTS includes (`/incbin/`) and can describe the slot in
a boot image (`dsp_slot.py show boot-athena.img`). `check-boot.py boot-athena.img` checks a built
image: header fields, SHA-1 id, layout, the magic exactly once, and the slot empty (`--filled`
accepts a filled one and checks its hash).

## Boot image

`pack.py` writes the Android boot image header v1 from constants: page size 2048, kernel at
0x40200000, ramdisk at 0x55000000, second/tags addresses and os_version as in the stock image,
TECHO5's command line. The id is mkbootimg's SHA-1 over kernel, ramdisk and second stage (each
followed by its size) and a zero recovery-DTBO size. LK adds `root=PARTUUID=<system> init=/init`,
so the kernel mounts the system partition (the TECHO5 slot store) and runs `/init` there, the way
the stock kernel boots.

## License

The kernel patches, patchers, DTS and machine driver are GPL-2.0, like the kernel they become part
of (the DTS is GPL-2.0 OR MIT). The build scripts (`build.sh`, `pack.py`, `mkfwcpio.py`,
`dsp_slot.py`, `check-boot.py`) are MIT, like the rest of TECHO5. In a boot image built from this,
the kernel is under the GPL-2.0, and this directory plus the kernel.org tarball is its complete
source; the firmware files in its ramdisk are MediaTek's, under `LICENCE.mediatek`, which they
carry.
