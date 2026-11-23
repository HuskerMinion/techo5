# TECHO5 on the Lenovo ThinkSmart View (CD-18781Y)

The ThinkSmart View is an 8 inch desk display Lenovo sold for Teams and Zoom: a Qualcomm APQ8053
(eight Cortex-A53 cores, 2 GB), an 800x1280 panel that stands landscape, two digital microphones, a
speaker, a camera with a sliding cover, a microphone switch and a light sensor. TECHO5 runs on it
with TECHO5's 64-bit build of the msm8953-mainline kernel (`kernel/`) and the Show's 32-bit userland, as on
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
the screen, the hardware video decoder for the camera stream.

## How it boots

lk2nd is in the `boot` partition. It reads `extlinux/extlinux.conf`, the kernel and the device tree
from the first partition of an MBR inside what was Android's `system` partition. There is no
initramfs: the kernel's command line has its device mapper make the MBR's two partitions
(`dm-mod.create`, `system1` and `system2`, by the GPT name `system` and the image's offsets), and the
kernel mounts `system2` read-only as `/` and runs its `/sbin/init`.

So `system2` is the slot store and its `/sbin/init` is `init` here: the Skylight's, with this boot
chain under it. It mounts `techo5-data`, a GPT partition after `system` that `repartition.sh` makes,
links the audio DSP's firmware from the unit's modem partition where the kernel looks first, starts
the built-in drivers that found no firmware when they probed (the audio DSP, the video decoder,
Wi-Fi), binds the store's `/lib/firmware` into the slot and pivots into it, or stays as the rescue
environment.

## The kernel

`kernel/build.sh` builds it from msm8953-mainline `v7.1.3-r0`, the tree postmarketOS packages for
this SoC, with the speaker amplifier and camera drivers, the audio and video decoder patches, the
camera in the device tree, and a configuration trimmed to this board, every driver built in
([kernel/README.md](kernel/README.md)). `uname -r` is `7.1.3-techo5-thinksmart`. It gives
`boot-thinksmart.img`, which a root filesystem carries (`deploy-rootfs.sh KERNEL_IMAGE`), and
`vmlinuz` and the device tree for `mkimage.sh -K`.

The firmware (`mkstore.sh -F`) is `/lib/firmware` of a unit running postmarketOS or the Debian
project's image: Wi-Fi, Bluetooth and the GPU. The audio DSP's is the unit's own.

## Building an image

```
tools/linux/thinksmart/kernel/build.sh
K=~/techo5-thinksmart-kbuild/out
DEVICE_OVERLAY=tools/linux/thinksmart/overlay KERNEL_IMAGE=$K/boot-thinksmart.img \
	tools/linux/deploy-rootfs.sh --out rootfs.tar.gz
tools/linux/thinksmart/mkstore.sh -r rootfs.tar.gz -k none -F firmware.tar -S 2048 -o store.img
tools/linux/thinksmart/mkimage.sh -K $K -s store.img -o system.img
```

No `EXTRA_PACKAGES=busybox-extras`, which athena and the Skylight add for `udhcpd`: they offer a
network over USB (`USB_RNDIS` in device.conf) and hand the computer an address. The ThinkSmart View's
USB gadget is the serial console only, and the store, which the rescue environment runs from, has
`busybox-extras` of its own.

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
and was tried on the unit both ways round.

Each slot carries its own kernel (`usr/share/techo5/kernel/boot-thinksmart.img`, which `slotctl`
links to `slots/<x>/boot/boot.img`), and a slot always starts with it, as on athena with the boot
layer (`tools/linux/README.md`, "The boot layer"). The A/B record is `bootctl`'s, kept in a file on
`system1` (`techo5-ab`), since this board has no misc partition; `slotctl` and `boot.sh` keep it as
they do on athena. lk2nd reads none of it: it starts the one entry of `extlinux.conf` on `system1`,
or, in recovery mode, the Android boot image in `recovery`, once. So the boot layer's work is done by
`lk2nd-ab`, the first thing the store's `init` runs:

- A good slot boots from `system1`, with its own kernel and device tree there (`k-<sum>/`) and
  `ab.slot=<x>` on the command line. When `system1` holds another kernel (the slot was just
  committed, or a switch or fallback changed the slot), `lk2nd-ab` puts the slot's there and restarts.
- A slot on trial is started once from `recovery`: `lk2nd-ab` takes a try from the record, writes the
  slot's image there with `ab.slot=<x>` added, and restarts into recovery mode. Any restart after
  that, `boot.sh`'s at the trial's timeout, a panic (`panic=10`), the plug pulled on a hang, is back on
  `system1`, which takes the next try; after three the record falls back to the other slot, and
  `slotctl follow` makes it the store's active slot. A trial that settles (five minutes of a running
  daemon, with a default route: `TRIAL_NEEDS_NETWORK=1`, since the kernel is on trial too) is
  committed, and the next start moves its kernel to `system1`.
- A kernel in `recovery` starts its slot only while the record has that slot on trial. One left
  there from an earlier trial and started again (lk2nd's menu has a Recovery entry) is stale: it
  restarts, back on `system1`. A trial kernel also starts only once: before its slot runs,
  `lk2nd-ab` replaces it in `recovery` with `system1`'s good kernel as the fallback (or clears
  `recovery`), since a slot out of tries still counts as unsettled and a start by hand takes no try.
- The kernel that moves to `system1` at a commit has only started from `recovery` before. If lk2nd's
  extlinux path fails it, nothing on the unit can count the restarts (lk2nd counts nothing, and a
  kernel that never reaches init runs no boot layer), so the move puts the kernel it replaces, the
  slot that was good before, into `recovery`, marked `techo5.kernel=fallback`. Started from there by
  hand, it marks the new slot bad, puts its own kernel back on `system1` and boots its own slot.
  lk2nd's menu: power on with nothing held, then hold Volume Down once the logo shows (held from
  power-on it is Lenovo's own locked fastboot instead); Volume Down moves, Volume Up picks
  Recovery. If that kernel cannot be written at the move, `recovery` is cleared and the boot log
  says there is no way back; `lk2nd-ab status` says what `recovery` holds.
- lk2nd 20.0 (on the units, January 2025) starts a kernel taking at most 32 MiB in memory from
  extlinux, so a slot whose kernel is bigger is marked bad before its trial (and `pack.py` refuses to
  build one).

`lk2nd-ab status` says what the record and `system1` hold. `python3 tools/test_lk2nd_ab.py` runs it
under busybox with a made-up unit. An image from before the boot layer has no record: `init` then
starts the slot `slotctl` picks, on whatever kernel `system1` has.

The firmware is outside the slots and is not updated.

## Files

- `init`: the store's `/sbin/init`.
- `mkstore.sh`, `mkimage.sh`: the store and the image for `system`.
- `repartition.sh`: run on the unit, makes `techo5-data`.
- `rescue-profile.sh`: the rescue environment's login text.
- `overlay/`: the board's `device.conf`.
- `kernel/`: the kernel's build, its drivers, patches and configuration, and `pack.py`, its boot image.
