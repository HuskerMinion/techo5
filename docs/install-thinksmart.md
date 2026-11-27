# Installing TECHO5 on a Lenovo ThinkSmart View (CD-18781Y)

The ThinkSmart View is an 8 inch desk display Lenovo sold for Teams and Zoom. It is not an Amazon
device. Inside is a Qualcomm APQ8053 with an 800x1280 touch screen, two microphones, a speaker, a
camera with a sliding cover, a microphone switch, Wi-Fi and Bluetooth. TECHO5 replaces its Android
system with TECHO5's own Linux, which talks to Home Assistant.

The installer is one Python script. It works over the SoC's emergency download mode (EDL) from the
unit's stock Android, and only on the partition table it knows.

> **Read this first.** The installer has been run from stock on one unit, and the image on two (one
> with each panel). It erases Android and everything on the unit, and it rewrites the eMMC's
> partition table. The installer takes a full backup first and stops if anything is not as it
> expects, but a unit can still end up not starting. Do it on a unit you can afford to lose, keep it
> on its power adapter the whole time, and never unplug it while the install runs. This is a hobby
> project, not a product.

No release carries this board's image yet, so for now you build it (below) and pass it in.

## What works

- the screen and touch, landscape;
- the speaker and both microphones, the wake word, the microphone switch;
- the camera and its lens cover;
- the volume buttons;
- the light sensor, for the screen's brightness;
- Wi-Fi and Bluetooth;
- updates into the spare of two slots, with the kernel the update carries.

What doesn't: the screen has no portrait layout.

The hardware notes, and how the unit boots, are in
[tools/linux/thinksmart/README.md](../tools/linux/thinksmart/README.md).

## What you need

- A ThinkSmart View CD-18781Y with its power supply.
- A **USB-A to USB-C** cable. The unit's USB-C port has no CC resistors, so a plain C-to-C cable is
  often not seen.
- A Linux computer (or macOS, for the install alone) with Python 3 and about 10 GB free for the
  backup.
- [bkerler/edl](https://github.com/bkerler/edl) (GPL-3.0), at the commit this was done with, in a
  venv: `pip install git+https://github.com/bkerler/edl@1cda1a616748d608667b2d13b73a28d3197ceb45`. On Linux, stop ModemManager while you use it (`sudo systemctl stop ModemManager`): it
  takes the device otherwise. edl needs raw USB access: its udev rules, or sudo.
- To build the image: Linux or WSL (x86_64), what [building.md](building.md) asks for to build a root
  filesystem, the kernel build's compilers
  ([tools/linux/thinksmart/kernel](../tools/linux/thinksmart/kernel/README.md#building)), and an
  `arm-none-eabi` GCC for lk2nd (Debian and Ubuntu: `gcc-arm-none-eabi`).

The image carries only firmware that may be passed on, with its license beside it: the GPU's
microcode, from linux-firmware under Qualcomm's redistribution license (`LICENSE.qcom` and
`NOTICE.qcom`), and the Wi-Fi regulatory database, from wireless-regdb (ISC); see
[tools/linux/thinksmart/firmware-licenses](../tools/linux/thinksmart/firmware-licenses/README.md).
The rest comes from your unit only: the Wi-Fi, Bluetooth and audio firmware and the GPU's zap shader
are made at every boot from the unit's own modem and vendor partitions, which stay where they are,
and the speaker amplifier's tuning is taken from the unit's stock kernel during the install (into the
backup folder, and back onto that unit only).

## Entering EDL

Unplug the power, hold **Vol+ and Vol-** together, plug the power back in, release after 5 to 10
seconds. The screen stays black and the computer sees USB device `05c6:9008`. The unit leaves EDL by
itself after a few idle minutes; enter it again if `05c6:9008` is gone.

Once TECHO5 is on the unit, lk2nd's menu has an EDL entry: power on with nothing held, then hold
Volume Down once the logo shows (Volume Down moves, Volume Up picks). Held from power-on, Volume Down
is the stock bootloader's own fastboot instead, which is locked and has no way to EDL. From a running
unit, `rebootto bootloader` starts lk2nd's menu too, and `fastboot oem reboot-edl` from the computer
goes on to EDL.

The USB cable from the computer powers the unit on its own, so a unit that is stuck needs the cable
out as well as the power adapter before it really restarts.

## 1. Build the image

```
tools/linux/thinksmart/kernel/build.sh
K=~/techo5-thinksmart-kbuild/out
DEVICE_OVERLAY=tools/linux/thinksmart/overlay KERNEL_IMAGE=$K/boot-thinksmart.img \
	tools/linux/deploy-rootfs.sh --out rootfs.tar.gz
tools/linux/thinksmart/mkstore.sh -r rootfs.tar.gz -k none -S 2048 -o store.img
tools/linux/thinksmart/mkimage.sh -K $K -s store.img -o system.img
tools/linux/thinksmart/lk2nd/build.sh
```

`deploy-rootfs.sh` is the build every Show's root filesystem comes from
([building.md](building.md)); it needs the helper programs it names in `bin/` first. With
`KERNEL_IMAGE` the root filesystem carries the kernel, which is how a later one reaches the unit.
Check it before going on: every file in it must be root's
(`tar -tzvf rootfs.tar.gz | awk '{print $2}' | sort | uniq -c`). One built outside the user
namespace `deploy-rootfs.sh` uses carried the builder's own files, and its SSH server refused every
key.

`mkstore.sh` without `-F` puts in the GPU's microcode and the Wi-Fi regulatory database from
Alpine's packages; everything else is the unit's own.

`lk2nd/build.sh` builds the bootloader that goes into `boot`, as this board's stock bootloader wants
it: signed, and without an appended QCDT. **lk2nd's own release images do not start on a ThinkSmart
View**: the stock bootloader crashes on them to a black screen, and the installer refuses them.

## 2. Install

Put the unit in EDL, then:

```
python3 tools/install-thinksmart.py --system system.img \
	--lk2nd ~/techo5-thinksmart-lk2nd/out/lk2nd-thinksmart.img --name Office --wifi MyNetwork
```

It asks for the Wi-Fi passphrase, and once before it writes. Each step is checked before the next:

1. It finds the unit, reads its partition table (both copies) and its serial, the one on its label.
2. It checks the system image and lk2nd, and works out the new partition table from the unit's own.
3. It reads the whole eMMC into `backups/thinksmart-<serial>/` and checks it against the unit. Every
   unit has the same partition table from Lenovo's factory, so the serial is what tells two units'
   backups apart.
   This takes several minutes, and edl shows no progress while it runs. Without a verified backup
   nothing is written.
4. It takes the speaker's tuning out of the stock kernel in the backup.
5. It says what is erased and how to go back, and asks.
6. It writes lk2nd into `boot`, the new partition table, the system image, and the settings (name,
   Wi-Fi, Home Assistant key, speaker tuning), and reads each back from the flash.
7. It restarts the unit. edl may report a `USBError` as the unit drops off USB: that is the restart.

The new layout keeps every one of Lenovo's partitions where it is. Android's `userdata` becomes
TECHO5's `system` (2.24 GiB: lk2nd's boot files and the slot store) and `techo5-data` (the rest).
Android's own `system` partition is renamed `android-system`, its contents untouched.

`--dry-run` runs every check and prints every command that would write, and writes nothing.
`--for-someone-else` is for a unit you are giving away: no name, network or key is written, and it
starts its own setup on its screen. Run again on a unit it installed before, the installer installs
again (TECHO5's data is erased) without touching the partition table; it needs the backup from the
first time.

The first boot makes `techo5-data` and the firmware, about 35 seconds to the clock.

### Add it to Home Assistant

Settings, Devices & services: the unit is found as an ESPHome device, with the name you gave it.
The installer printed its key and kept it in the backup folder (`home-assistant.key`). A unit
installed without a network shows the Wi-Fi page first.

## Updates

No release carries a root filesystem for this board yet, so Home Assistant offers nothing.
By hand, a new one goes into the other slot on trial:

```
scp -O rootfs.tar.gz root@<unit>:/data/techo5-linux/
ssh root@<unit> 'PATH=/usr/local/sbin:$PATH; slotctl install /data/techo5-linux/rootfs.tar.gz && reboot'
```

(SSH needs an image built with `mkstore.sh -k <authorized_keys>`, which is for your own bench unit
only.) It is committed after five minutes of a running daemon, and the other slot is kept to fall
back to.

Each root filesystem carries its own kernel, and the slot on trial is the kernel's trial too: the
unit restarts once into the new slot's kernel from the `recovery` partition. If the trial settles (the
daemon up five minutes, with a network), the kernel moves to `system1`, whose `extlinux.conf` lk2nd
starts every time, which takes one more restart; if it does not, the unit restarts by itself back to the old slot with
its own kernel, which tries the new one twice more and then gives up on it. `lk2nd-ab status` on the
unit says where it stands. The firmware is outside the slots and is made from the unit's own
partitions at every boot.

An update replaces a slot, not the boot layer (`lk2nd-ab` and the rest of the store's `init`): a
newer boot layer comes only from installing the unit again from an image that has it.

## If it does not start

- **A black screen, or the logo and then black, and the computer sees `05c6:900e`**: the stock
  bootloader crashed, which is what an lk2nd it does not accept does. Take out the USB cable and the
  power, enter EDL, and run the installer again with lk2nd from `lk2nd/build.sh`.
- **The logo and nothing more, no USB device at all**: lk2nd started and hangs. An lk2nd built with
  a Linux GCC (`arm-linux-gnueabihf-`) does this; build it with `arm-none-eabi-`.
- **Restarting over and over right after an update settled**: the new kernel fails from where lk2nd
  starts it every time. The kernel before it is kept in `recovery` for this: power on with nothing
  held, hold Volume Down once the logo shows, and pick Recovery in lk2nd's menu (Volume Down moves,
  Volume Up picks). The unit starts the slot it had before, marks the new one bad and puts its kernel
  back.
- **Text on the screen that stops**: the last lines say where. A line about `device-mapper` or
  `VFS: Unable to mount root` means the kernel did not find the store in the `system` partition.
- **The screen says RESCUE**: no slot could be started. `cat /data/techo5-linux/init.log` over SSH
  (bench images) says what the boot did.
- **Nothing at all**: enter EDL and run the installer again, or go back.

## The way back

The backup folder holds the whole eMMC as it came (`emmc.img`) and `restore/RESTORE.txt`, which
gives the exact commands for this unit. In EDL, from the backup folder:

| To | How |
| --- | --- |
| Another image of TECHO5 | the installer again, with the new image |
| Lenovo's Android, as it came | `edl wf emmc.img`, then `edl reset` (writes the whole eMMC) |
| Lenovo's Android, as a new unit | only what the install changed: the two partition table copies, `boot`, `recovery`, and the start of `userdata` cleared (`restore/RESTORE.txt` has the sectors) |
