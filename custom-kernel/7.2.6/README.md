# TECHO5 mainline kernel option

This directory builds Linux 7.2.6 for the Echo Show 5 (`cronos`, board revision
`pvt_4_1`). MT8163 support is carried as a patch against the kernel.org release,
adapted from the [MT8163 kernel tree](https://github.com/bengris32/linux-mtk/tree/mt8163/7.0)
at commit `20951722df6ae1f6fecbfcea0a7d585118a06f3f`.

## Hardware integration

The port includes CPU startup, USB serial, eMMC, Wi-Fi, Bluetooth, touch,
ambient light sensing, CPU frequency scaling, thermal management, display,
speaker playback, and microphone capture. The rescue environment and installed
application use Alpine armv7. The privacy driver exposes the hardware mute latch
and button; physical button behavior still needs confirmation. The experimental camera driver detects the OV02B10 sensor, but capture does not
yet produce frames. Camera support is incomplete.

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

The camera's reset, power-down, and clock pins are 22, 23, and 119.
Their wiring alone does not provide a working mainline capture pipeline.

## Build inputs

`k6.sh` runs the arm64 `techo5-kbuild` container with the `techo5-k6` volume mounted
at `/src`. Build the container from `docker/Dockerfile`. The kernel source lives
at `/src/linux-7.2.6`; `KDIR` can select another source directory inside the volume.
`config/cronos.config` is the kernel configuration, and `prepare.sh` installs the
platform patch and board support. Generated codec sources belong in `codec-work/`
and build products in `out/`; both directories are ignored.

The image assembler requires a locally supplied `boot-lineage.img` with a
compatible microloader and boot header. Firmware and calibration remain subject
to the notices in `initramfs/firmware/LICENSE` and `../../NOTICE`.

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
    curl -fLO https://cdn.kernel.org/pub/linux/kernel/v7.x/linux-7.2.6.tar.xz
    echo "039aef84f2b0994aeda3f4fcfc3d02ec9d7a9bbb9020ea264c43f446c860f606  linux-7.2.6.tar.xz" | sha256sum -c -
    tar -xJf linux-7.2.6.tar.xz -C /src'
python3 patches/port-aic3101.py sources/amazon-kernel/sound/soc/codecs/tlv320aic3101.c \
    sources/amazon-kernel/sound/soc/codecs/tlv320aic3101.h codec-work
./k6.sh 'sh /prepare.sh'
./k6.sh 'set -e
    cd /src/linux-7.2.6
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
