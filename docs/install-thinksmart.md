# Installing TECHO5 on a Lenovo ThinkSmart View (CD-18781Y)

The ThinkSmart View is an 8 inch desk display Lenovo sold for Teams and Zoom. It is not an Amazon
device. Inside is a Qualcomm APQ8053 with an 800x1280 touch screen, two microphones, a speaker, a
camera with a sliding cover, a microphone switch, Wi-Fi and Bluetooth. TECHO5 replaces its Android
system with TECHO5's own Linux, which talks to Home Assistant.

There is no installer for it yet, and no release carries its image. This page is the steps by hand:
you build the image yourself, from files taken off your own unit, and write it over the SoC's
emergency download mode (EDL).

> **Read this first.** This has been tried on two units, one with each panel. It erases Android and
> everything on the unit, and it rewrites the eMMC's partition table twice. The full backup in step 1
> is the only way back to Lenovo's firmware. Do it on a unit you can afford to lose, and keep it on
> its power adapter the whole time. This is a hobby project, not a product.
>
> Both units ran the Debian project's image between steps 4 and 5, so two things here are written
> from how they should go and not from having been done: step 6 run from the bootstrap (it was run
> from Debian), and the rescue screen (no slot has failed to start yet).

Placeholders: `<unit>` is the unit's address on your network, `<uuid>` a UUID you make once
(`cat /proc/sys/kernel/random/uuid`).

## What works

- the screen and touch, landscape;
- the speaker and both microphones, the wake word, the microphone switch;
- the camera and its lens cover;
- the volume buttons;
- the light sensor, for the screen's brightness;
- Wi-Fi and Bluetooth;
- updates into the spare of two slots.

What doesn't: nothing updates the kernel or its modules, the screen has no portrait layout, and
the rescue environment is reached over Wi-Fi only (the kernel has no USB gadget).

The hardware notes, and how the unit boots, are in
[tools/linux/thinksmart/README.md](../tools/linux/thinksmart/README.md).

## What you need

- A ThinkSmart View CD-18781Y with its power supply.
- A **USB-A to USB-C** cable. The unit's USB-C port has no CC resistors, so a plain C-to-C cable is
  often not seen.
- A Linux computer (x86_64), with root, about 20 GB free, and what
  [building.md](building.md) asks for to build a root filesystem.
- [bkerler/edl](https://github.com/bkerler/edl) (`pip install git+https://github.com/bkerler/edl`
  in a venv). Stop ModemManager while you use it (`sudo systemctl stop ModemManager`): it takes the
  device otherwise.
- From [rickx/ubuntu-thinksmart](https://github.com/rickx/ubuntu-thinksmart): `prebuilt/lk2nd.img`
  and the postmarketOS bootstrap image from its releases
  (`bootstrap-pmos-ssh-generic-qcom-msm8953.img`, with its `.sha256`).
- From [m0s4ik/thinksmart-view-debian](https://github.com/m0s4ik/thinksmart-view-debian): its
  `driver/` folder, for the modules the kernel package does not have.

Steps 1 to 4 are that project's own route as far as a unit running Linux, and its
[install guide](https://github.com/m0s4ik/thinksmart-view-debian/blob/main/docs/install.md) has
them at length. A unit that already runs its Debian image starts at step 5, and takes the kernel's
files from `/boot`, `/usr/lib/modules` and `/usr/lib/firmware` there.

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

## 3. The kernel's files

The bootstrap comes with an older kernel. On the unit, take the one this port was made on:

```
ssh root@<unit>
apk update && apk upgrade linux-postmarketos-qcom-msm8953      # to 7.1.3
ln -s /usr/lib/modules/7.1.3-msm8953 /lib/modules/7.1.3-msm8953 && depmod -a 7.1.3-msm8953
reboot
```

Then, on the computer, take what belongs to that kernel:

```
ssh root@<unit> 'cd /boot && tar -cf - vmlinuz initramfs apq8053-lenovo-cd-18781y.dtb' > boot.tar
ssh root@<unit> 'tar -C /usr/lib -cf - modules/7.1.3-msm8953' > modules.tar
ssh root@<unit> 'tar -C /lib --exclude=msm-firmware-loader -cf - firmware' > firmware.tar
```

## 4. The modules the package does not have

Eight modules are built against the kernel's source (msm8953-mainline `v7.1.3-r0`) and the unit's
own `/boot/config`, with clang 22, as the Debian project's
[driver/tas5782m/README.md](https://github.com/m0s4ik/thinksmart-view-debian/blob/main/driver/tas5782m/README.md)
describes: built with GCC they load and then fail.

| Module | From | Without it |
| --- | --- | --- |
| `snd-soc-tas5782m.ko` | the Debian project's `driver/tas5782m` | no sound card: no speaker and no microphones |
| `vcnl4000.ko` | its `driver/vcnl4000` | no light sensor |
| `s5kc505a.ko`, and `apq8053-lenovo-cd-18781y-camera.dtb` | its `driver/s5kc505a` | no camera |
| `xt_owner.ko`, `xt_recent.ko` | [tools/linux/thinksmart/kernel/netfilter](../tools/linux/thinksmart/kernel/netfilter) here | the firewall leaves the video decoder unfenced and AirPlay 2 off |
| `q6asm.ko`, `q6asm-dai.ko` | its `driver/q6asm`, in place of the package's own | playback stalls when a stream stops and starts again |
| `venus-core.ko` | its `driver/venus`, in place of the package's own | no hardware video decoder |

The first five go into the modules' `extra/`, the last three over the package's own files
(`kernel/sound/soc/qcom/qdsp6/` and `kernel/drivers/media/platform/qcom/venus/`), and `depmod` makes
the index again:

```
mkdir tree && tar -xf modules.tar -C tree
cp snd-soc-tas5782m.ko vcnl4000.ko s5kc505a.ko xt_owner.ko xt_recent.ko tree/modules/7.1.3-msm8953/extra/
mkdir -p lib && mv tree/modules lib/ && depmod -b . 7.1.3-msm8953
tar -C lib -cf modules.tar modules/7.1.3-msm8953
```

## 5. The image

```
DEVICE_OVERLAY=tools/linux/thinksmart/overlay tools/linux/deploy-rootfs.sh --out rootfs.tar.gz
tools/linux/thinksmart/mkstore.sh -r rootfs.tar.gz -k ~/.ssh/id_ed25519.pub -W wpa_supplicant.conf \
	-M modules.tar -F firmware.tar -U <uuid> -S 2048 -o store.img
tools/linux/thinksmart/mkimage.sh -b boot.tar -d apq8053-lenovo-cd-18781y-camera.dtb \
	-s store.img -u <uuid> -o system.img
```

`deploy-rootfs.sh` is the build every Show's root filesystem comes from
([building.md](building.md)); it needs the helper programs it names in `bin/` first.

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
The kernel, its modules and its firmware are outside the slots and stay as the image left them.

## If it does not start

- **Text on the screen that stops**: the last lines say where. `pmos.debug-shell` on the kernel's
  command line (`mkimage.sh -a pmos.debug-shell`) stops in postmarketOS's initramfs instead.
- **The screen says RESCUE**: no slot could be started. The unit is on your Wi-Fi with SSH, if the
  image was built with `-k` and `-W`; `cat /data/techo5-linux/init.log` says what the boot did.
- **Nothing at all**: enter EDL and write the image again, or go back.

## The way back

| To | How |
| --- | --- |
| Another image of TECHO5 | `edl w system system.img` |
| Another system in `system` (the Debian project's image, the bootstrap) | the table from before step 6 first, then that image. The table is written back with `sfdisk /dev/mmcblk0 < gpt-before-techo5.sfdisk` from a system that still runs; from EDL only as raw sectors, so keep `dd if=/dev/mmcblk0 bs=512 count=34` and the last 33 sectors from before step 6 as well |
| Lenovo's firmware | `edl wf stock-full-emmc.bin`, which restores the original table too |
