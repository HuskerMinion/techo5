# Installing TECHO5 on a Lenovo ThinkSmart View (CD-18781Y)

The ThinkSmart View is an 8 inch desk display Lenovo sold for Teams and Zoom. It is not an Amazon
device. Inside is a Qualcomm APQ8053 with an 800x1280 touch screen, two microphones, a speaker, a
camera with a sliding cover, a microphone switch, Wi-Fi and Bluetooth. TECHO5 replaces its Android
system with TECHO5's own Linux, which talks to Home Assistant.

There is no installer for it yet, and no release carries its image. This page is the steps by hand:
you build the kernel and the image yourself, with firmware taken off your own unit, and write it over the SoC's
emergency download mode (EDL).

> **Read this first.** This has been tried on two units, one with each panel. It erases Android and
> everything on the unit, and it rewrites the eMMC's partition table twice. The full backup in step 1
> is the only way back to Lenovo's firmware. Do it on a unit you can afford to lose, and keep it on
> its power adapter the whole time. This is a hobby project, not a product.
>
> Both units ran the Debian project's image before TECHO5's, so two things here are written
> from how they should go and not from having been done: step 6 run from the bootstrap (it was run
> from Debian), and the rescue screen (no slot has failed to start yet).

`<unit>` is the unit's address on your network.

## What works

- the screen and touch, landscape;
- the speaker and both microphones, the wake word, the microphone switch;
- the camera and its lens cover;
- the volume buttons;
- the light sensor, for the screen's brightness;
- Wi-Fi and Bluetooth;
- updates into the spare of two slots, with the kernel the update carries.

What doesn't: the screen has no portrait layout, and
the rescue environment is reached over Wi-Fi only (the kernel has no USB gadget).

The hardware notes, and how the unit boots, are in
[tools/linux/thinksmart/README.md](../tools/linux/thinksmart/README.md).

## What you need

- A ThinkSmart View CD-18781Y with its power supply.
- A **USB-A to USB-C** cable. The unit's USB-C port has no CC resistors, so a plain C-to-C cable is
  often not seen.
- A Linux computer (x86_64), with root, about 20 GB free, what [building.md](building.md) asks for
  to build a root filesystem, and what the kernel build asks for
  ([tools/linux/thinksmart/kernel](../tools/linux/thinksmart/kernel/README.md#building)).
- [bkerler/edl](https://github.com/bkerler/edl) (GPL-3.0), at the commit this was done with, in a
  venv: `pip install git+https://github.com/bkerler/edl@1cda1a616748d608667b2d13b73a28d3197ceb45`.
  Stop ModemManager while you use it (`sudo systemctl stop ModemManager`): it takes the device
  otherwise.
- From [rickx/ubuntu-thinksmart](https://github.com/rickx/ubuntu-thinksmart), two files, each checked
  before it goes near a unit (`sha256sum`):
  - `prebuilt/lk2nd.img` (commit 7b1a5f3e), sha256
    `c81b036c75028c3d5aa915c72c89e075bc044360fe964c2ec0afb1d4a6f5209b`: lk2nd
    `20.0-next-b9c007bb` as its version string says, built from
    [msm8916-mainline/lk2nd](https://github.com/msm8916-mainline/lk2nd) (commit b9c007bb), signed
    and without a QCDT as this board's stock bootloader needs. lk2nd is BSD-3-Clause, with
    GPL-2.0-only parts (its device trees and panel drivers); rickx's repository leaves `prebuilt/`
    under those upstream licenses. lk2nd's own release images do not start on this board: the
    stock bootloader crashes on them.
  - the postmarketOS bootstrap image from its release `bootstrap-2026-05-22`
    (`bootstrap-pmos-ssh-generic-qcom-msm8953.img`), sha256
    `7d79db19b0911647bf6ab0a9a2924ef5cf83b4674798a54acdcbd41cdf07921b` (the release's `.sha256`
    says the same). It is postmarketOS's packages, each under its own license.
Steps 1 and 2 are [m0s4ik/thinksmart-view-debian](https://github.com/m0s4ik/thinksmart-view-debian)'s
own route as far as a unit running Linux, and its
[install guide](https://github.com/m0s4ik/thinksmart-view-debian/blob/main/docs/install.md) has
them at length. A unit that already runs its Debian image starts at step 3, and takes the firmware
from `/usr/lib/firmware` there.

## Entering EDL

Unplug the power, hold **Vol+ and Vol-** together, plug the power back in, release after 5 to 10
seconds. The screen stays black and the computer sees USB device `05c6:9008`. Once lk2nd is on the
unit (step 2), its menu has an EDL entry as well: `reboot bootloader` from Linux, or hold Volume
Down at power-on.

The unit leaves EDL by itself after a few idle minutes; enter it again if `05c6:9008` is gone.

## 1. The backup

```
edl rf stock-full-emmc.bin
edl printgpt > gpt-original.txt
```

The file must be **7,818,182,656 bytes**: `edl rf` cuts it short without an error when the disk
fills up. Keep it somewhere safe. `edl wf stock-full-emmc.bin` puts the unit back as it was,
partition table included.

## 2. lk2nd and the bootstrap

The bootstrap has no Wi-Fi network and the unit has no keyboard, so the network and your SSH key
go into the image before it is written:

```
LOOP=$(sudo losetup -P --show -f bootstrap.img)
sudo mount "${LOOP}p2" /mnt
sudo install -m 600 wifi.nmconnection /mnt/etc/NetworkManager/system-connections/
tr -d - < /proc/sys/kernel/random/uuid | sudo tee /mnt/etc/machine-id
sudo install -d -m 700 /mnt/root/.ssh
sudo install -m 600 ~/.ssh/id_ed25519.pub /mnt/root/.ssh/authorized_keys
sudo umount /mnt && sudo losetup -d "$LOOP"
```

`wifi.nmconnection` is a NetworkManager keyfile for your network (the Debian project's guide has
one to copy). Then, in EDL:

```
edl w boot lk2nd.img
edl w system bootstrap.img
edl reset
```

Android does not boot again after this. The unit joins your Wi-Fi; find its address in your router.

## 3. The firmware

TECHO5's image takes the Wi-Fi, Bluetooth and GPU firmware from the bootstrap. On the computer:

```
ssh root@<unit> 'tar -C /lib --exclude=msm-firmware-loader -cf - firmware' > firmware.tar
```

The audio DSP's firmware is not in it: that is the unit's own, in its modem partition, and TECHO5
links it from there at every boot.

## 4. The kernel

TECHO5's kernel for this board is built from source: msm8953-mainline `v7.1.3-r0` with the
speaker amplifier and camera drivers and the patches the board needs
([tools/linux/thinksmart/kernel](../tools/linux/thinksmart/kernel/README.md)). On Linux or WSL with
the aarch64 and armhf cross compilers:

```
tools/linux/thinksmart/kernel/build.sh
```

It leaves `boot-thinksmart.img`, `vmlinuz` and the device tree in `~/techo5-thinksmart-kbuild/out`.
Every driver is built in; there are no modules.

## 5. The image

```
K=~/techo5-thinksmart-kbuild/out
DEVICE_OVERLAY=tools/linux/thinksmart/overlay KERNEL_IMAGE=$K/boot-thinksmart.img \
	tools/linux/deploy-rootfs.sh --out rootfs.tar.gz
tools/linux/thinksmart/mkstore.sh -r rootfs.tar.gz -k ~/.ssh/id_ed25519.pub -W wpa_supplicant.conf \
	-F firmware.tar -S 2048 -o store.img
tools/linux/thinksmart/mkimage.sh -K $K -s store.img -o system.img
```

`deploy-rootfs.sh` is the build every Show's root filesystem comes from
([building.md](building.md)); it needs the helper programs it names in `bin/` first. With
`KERNEL_IMAGE` the root filesystem carries the kernel, which is how a later one reaches the unit.

`-k` and `-W` make an image for your own unit: SSH with that key from every boot, and your Wi-Fi
network (`wpa_supplicant.conf` holds `ctrl_interface=/run/wpa`, `update_config=0` and a
`network={ ssid="..." psk="..." }` block). Leave both out, with `-k none`, for a unit that is not
yours: its network is then chosen on its screen.

Check the root filesystem before going on: every file in it must be root's
(`tar -tzvf rootfs.tar.gz | awk '{print $2}' | sort | uniq -c`). One built outside the user
namespace `deploy-rootfs.sh` uses carried the builder's own files, and its SSH server refused every
key.

## 6. The partitions

The image is 4,694,016 sectors, and `system` has to be exactly that, with TECHO5's data in a
partition of its own after it. Two steps, both run on the unit from the bootstrap, both ending in
EDL:

```
ssh root@<unit>
/usr/local/sbin/apply-ubuntu-gpt.sh          # rickx's: system grows to the end of the eMMC
```

Then copy [tools/linux/thinksmart/repartition.sh](../tools/linux/thinksmart/repartition.sh) to the
unit and run it. Without an argument it only shows the table it would write:

```
sh repartition.sh
sh repartition.sh --write
```

It needs `sfdisk`, which the bootstrap has (`apply-ubuntu-gpt.sh` uses it); on Debian it is the
`fdisk` package. `system` becomes the image's size and the rest of it `techo5-data`; no other
partition moves. The
table as it was is kept in `/root/gpt-before-techo5.sfdisk`: copy it off the unit. What runs from
`system` does not boot again after this, so go straight on.

## 7. Write it

In EDL:

```
edl printgpt | grep -E '^(system|techo5-data):'     # system: Length 0x000000008f400000
edl w system system.img
edl reset
```

About two minutes. The first boot makes `techo5-data`, and takes about 35 seconds to the clock. The
kernel's log is on the screen until the daemon starts.

### Add it to Home Assistant

Settings, Devices & services: the unit is found as an ESPHome device, "ThinkSmart View" and the end
of its address. A unit whose image carried no network shows the Wi-Fi page first.

## Updates

No release carries a root filesystem for this board yet, so Home Assistant offers nothing.
By hand, a new one goes into the other slot on trial:

```
scp -O rootfs.tar.gz root@<unit>:/data/techo5-linux/
ssh root@<unit> 'PATH=/usr/local/sbin:$PATH; slotctl install /data/techo5-linux/rootfs.tar.gz && reboot'
```

It is committed after five minutes of a running daemon, and the other slot is kept to fall back to.

Each root filesystem carries its own kernel, and the slot on trial is the kernel's trial too: the
unit restarts once into the new slot's kernel from the `recovery` partition. If the trial settles (the
daemon up five minutes, with a network), the kernel moves to `system1`, whose `extlinux.conf` lk2nd
starts every time, which takes one more restart; if it does not, the next restart is back on the old slot with its
own kernel, which tries the new one twice more and then gives up on it. `lk2nd-ab status` on the
unit says where it stands. The firmware is outside the slots and stays as the image left it.

An update replaces a slot, not the boot layer (`lk2nd-ab` and the rest of the store's `init`): a
newer boot layer comes only from installing the unit again from an image that has it.

## If it does not start

- **Text on the screen that stops**: the last lines say where. A line about `device-mapper` or
  `VFS: Unable to mount root` means the kernel did not find the store in the `system` partition:
  check that the partition table from `repartition.sh` is on the unit.
- **The screen says RESCUE**: no slot could be started. The unit is on your Wi-Fi with SSH, if the
  image was built with `-k` and `-W`; `cat /data/techo5-linux/init.log` says what the boot did.
- **A new slot that stops**: pull the plug and put it back. The restart is the slot and kernel the
  unit had before, which counts the failed try.
- **Restarting over and over right after an update settled**: the new kernel fails from where lk2nd
  starts it every time. The kernel before it is kept in `recovery` for this: power on with nothing
  held, hold Volume Down once the logo shows, and pick Recovery in lk2nd's menu (Volume Down moves,
  Volume Up picks). The unit starts the slot it had before, marks the new one bad and puts its kernel
  back.
- **Nothing at all**: enter EDL and write the image again, or go back.

## The way back

| To | How |
| --- | --- |
| Another image of TECHO5 | `edl w system system.img` |
| Another system in `system` (the Debian project's image, the bootstrap) | the table from before step 6 first, then that image. The table is written back with `sfdisk /dev/mmcblk0 < gpt-before-techo5.sfdisk` from a system that still runs; from EDL only as raw sectors, so keep `dd if=/dev/mmcblk0 bs=512 count=34` and the last 33 sectors from before step 6 as well |
| Lenovo's firmware | `edl wf stock-full-emmc.bin`, which restores the original table too |
