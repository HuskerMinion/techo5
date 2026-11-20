# TECHO5 on the Lenovo ThinkSmart View (CD-18781Y)

The ThinkSmart View is an 8 inch desk display Lenovo sold for Teams and Zoom: a Qualcomm APQ8053
(eight Cortex-A53 cores, 2 GB), an 800x1280 panel that stands landscape, two digital microphones, a
speaker, a camera with a sliding cover, a microphone switch and a light sensor. TECHO5 runs on it
with the 64-bit msm8953-mainline kernel postmarketOS packages and the Show's 32-bit userland, as on
the 2nd gen Show 8 and the Skylight.

Status: tried on two units, one with each of the panels Lenovo fitted. Not in the releases, and there is no installer yet:
the steps below are by hand, from a unit that already runs Linux.

## What works

| Part | How |
| --- | --- |
| Display | msm DRM's fbdev emulation, 800x1280, drawn rotated as the Show 8's is. The device stands the other way up, so the kernel turns the picture half a turn on the display plane (`video=DSI-1:800x1280,rotate=180`) |
| Backlight | the device tree's `backlight` node, 0 to 4095 |
| Touch | Himax HX83100A ("Himax Touchscreen"), or FocalTech FT8201 on the other panel Lenovo fitted (edt-ft5x06, "generic ft5x06 (09)"); readings turned over with the picture. Both tried with a finger |
| Speaker | TAS5782M behind the audio DSP: `hw:0,0` once the `QUAT_MI2S_RX Audio Mixer MultiMedia1` route is on; periods in multiples of 480 frames (q6asm-dai) |
| Microphones | two digital microphones on `hw:0,1`, 48 kHz S16_LE, with the software echo reference |
| Mute | the slide switch, `SW_MICROPHONE_INSERT` in gpio-keys; software cannot move it |
| Buttons | volume up (gpio-keys) and down (pm8941_resin) |
| Camera | Samsung S5KC505A through qcom-camss, its binned 1304x980 mode (echod/internal/hardware/camera/camera_camss.go); the lens cover is `SW_CAMERA_LENS_COVER` |
| Light sensor | VCNL4200 through the vcnl4000 IIO driver, declared from device.conf |
| Wi-Fi | QCA9379 on SDIO, ath10k, with power save off: with it on a unit stopped answering the network for hours while it went on running |
| Bluetooth | the QCA9379's controller, hci_uart |
| Firewall | techo5-firewall as on every Show, with `xt_owner` and `xt_recent` built for this kernel |

Not there: a USB gadget (the rescue environment is reached over Wi-Fi and SSH only), the GPU for
the screen, the hardware video decoder for the camera stream, a kernel of TECHO5's own.

## How it boots

lk2nd is in the `boot` partition. It reads `extlinux/extlinux.conf`, the kernel, the device tree and
postmarketOS's initramfs from the first partition of an MBR inside what was Android's `system`
partition. That initramfs maps the MBR's two partitions into the device mapper (`system1`,
`system2`), mounts the one `pmos_root_uuid` names as the root and runs its `/sbin/init`.

So `system2` is the slot store and its `/sbin/init` is `init` here: the Skylight's, with this boot
chain under it. It mounts `techo5-data`, a GPT partition after `system` that `repartition.sh` makes,
links the audio DSP's firmware from the unit's modem partition where the kernel looks first, loads
the kernel's modules, binds the store's `/lib/modules` and `/lib/firmware` into the slot and pivots
into it, or stays as the rescue environment.

The initramfs takes an MBR with exactly two partitions and grows the second over any free space
after it, which is why the data is not a third partition there, and why the image fills `system`.

## The kernel's files

The kernel is not built here. It is postmarketOS's `linux-postmarketos-qcom-msm8953` 7.1.3
(msm8953-mainline `v7.1.3-r0`), and it stays what it is: nothing updates it over the air, and the
modules in the store are the ones for it. What goes with it:

- `vmlinuz`, `initramfs` and `apq8053-lenovo-cd-18781y.dtb`, from the package (`/boot` of a unit
  running it): `mkimage.sh -b`.
- `/usr/lib/modules/7.1.3-msm8953` and `/lib/firmware`, from the same unit: `mkstore.sh -M` and `-F`.
- Out-of-tree modules in the modules' `extra/`, built against that source and configuration with
  clang 22, as [m0s4ik/thinksmart-view-debian](https://github.com/m0s4ik/thinksmart-view-debian)
  describes (driver/tas5782m/README.md there): `snd-soc-tas5782m.ko` (rickx's driver; without it
  there is no sound card), `vcnl4000.ko`, `s5kc505a.ko`, and from `kernel/netfilter` here `xt_owner.ko`
  and `xt_recent.ko`. `depmod` is run over the tree afterwards.
- Three of the package's own modules rebuilt with that project's patches, in place of the package's:
  `q6asm.ko` and `q6asm-dai.ko` (driver/q6asm there: without them playback stalls when a stream is
  stopped and started again, which the daemon does all the time) and `venus-core.ko` (driver/venus).
- The device tree with the camera in it, `apq8053-lenovo-cd-18781y-camera.dtb`, from the same
  project (driver/s5kc505a): `mkimage.sh -d`.

`kernel/netfilter` builds the two netfilter modules the package's configuration leaves out, from
the kernel's own files:

```
cp linux-7.1.3/net/netfilter/xt_owner.c linux-7.1.3/net/netfilter/xt_recent.c tools/linux/thinksmart/kernel/netfilter/
make -C linux-7.1.3 ARCH=arm64 LLVM=1 LLVM_SUFFIX=-22 KBUILD_MODPOST_WARN=1 M=$PWD/tools/linux/thinksmart/kernel/netfilter modules
```

## Building an image

```
DEVICE_OVERLAY=tools/linux/thinksmart/overlay tools/linux/deploy-rootfs.sh --out rootfs.tar.gz
tools/linux/thinksmart/mkstore.sh -r rootfs.tar.gz -k none -M modules.tar -F firmware.tar \
	-U <uuid> -S 2048 -o store.img
tools/linux/thinksmart/mkimage.sh -b boot.tar -d apq8053-lenovo-cd-18781y-camera.dtb \
	-s store.img -u <uuid> -o system.img
```

`-k <authorized_keys>` and `-W <wpa_supplicant.conf>` make a bench image: SSH with that key from
every boot, and a network for a unit with nobody at its screen. Never for anyone else's unit.

## Putting it on a unit

A unit comes with Android. Getting from there to lk2nd, the larger `system` partition and a
running Linux is m0s4ik/thinksmart-view-debian's install guide (a full backup over EDL first,
lk2nd, rickx's bootstrap and partition table), and is not repeated here. From such a unit:

1. On the unit, as root: `repartition.sh` shows the table it would write, `repartition.sh --write`
   writes it. `system` becomes the image's size and the rest of it `techo5-data`. What ran from
   `system` does not boot again after this.
2. Into EDL, and `edl w system system.img`, then `edl reset`.

The first boot formats `techo5-data`. Going back to another system in `system` needs the old table
first (`repartition.sh` keeps it), written back with sfdisk from a running system or as raw sectors
over EDL.

## Updates

A ThinkSmart View takes a rootfs keyed `arm-thinksmart` into its other slot, on trial, as the Shows
take theirs; no release carries one yet. `slotctl install <rootfs.tar.gz>` does the same by hand,
and was tried on the unit both ways round. The kernel, its modules and firmware are outside the
slots and are not updated.

## Files

- `init`: the store's `/sbin/init`.
- `mkstore.sh`, `mkimage.sh`: the store and the image for `system`.
- `repartition.sh`: run on the unit, makes `techo5-data`.
- `rescue-profile.sh`: the rescue environment's login text.
- `overlay/`: the board's `device.conf`, `mdev.conf`, and `/lib/modules` for the store's to be bound on.
- `kernel/netfilter/`: the build file for `xt_owner` and `xt_recent`.
