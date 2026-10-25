# Installing TECHO5 on a Skylight Calendar 15 (150-CAL)

The Skylight Calendar 15 is a wall-powered touchscreen calendar. It is not an Amazon device. Inside
is a Rockchip RK3566 board (Skyworth D156) with a 1920x1080 screen, a speaker, Wi-Fi and Bluetooth.
TECHO5 replaces its Android system with TECHO5's own Linux, which talks to Home Assistant.

The installer is one Python script. It works over adb from the unit's stock Android, and only on the
firmware and hardware it knows.

> **Read this first.** This has been tried on one unit. It erases the Skylight app and everything it
> keeps on the unit, and it rewrites the eMMC's partition table. The installer takes a full backup
> first and stops if anything is not as it expects, but a unit can still end up not starting. Do it
> on a unit you can afford to lose, keep it on its power adapter the whole time, and never unplug it
> while the install runs. This is a hobby project, not a product.

Placeholders: `<serial>` is the unit's adb serial.

## What works

- the screen and touch;
- the speaker;
- Wi-Fi and Bluetooth (with the unit's own firmware, see below);
- the light sensor, for the screen's brightness;
- the clock, which keeps time without power.

What doesn't: **the Skylight has no microphone.** There is no wake word, no voice assistant, no
phone calls and no intercom or talkback on it. It is a screen, a speaker and Bluetooth.

The hardware notes are in [tools/linux/skylight/README.md](../tools/linux/skylight/README.md) and
[tools/linux/skylight/kernel/README.md](../tools/linux/skylight/kernel/README.md).

## What you need

- A Skylight Calendar 15 (model 150-CAL, FCC ID 2AABK-150 on the label) on its stock firmware
  **D156.V1.3**. The installer reads the firmware's fingerprint and stops on any other version.
- Its power adapter. The unit has no battery.
- A micro-USB **data** cable. A charge-only cable won't work.
- A computer with Python 3 and adb (Windows, Linux or macOS; see
  [Set up your computer](getting-started.md#set-up-your-computer-once)).
- About 17 GB free on the computer for the backup.
- Home Assistant with the ESPHome integration.

**Keep the unit off Wi-Fi until the backup is done.** Once it sees a network, the Skylight app can
update itself, and an update may change the firmware. A firmware the installer doesn't know stops
it. Don't join a network in its setup screens.

On the firmware the installer knows, adb is open over USB with no prompt and `adb root` works. Plug
the unit into the computer and check:

```
adb devices
adb shell getprop ro.build.fingerprint
```

The fingerprint should start with `rockchip/D156/D156:12/AndroidS/D156.V1.3:userdebug/`. If
`adb devices` shows nothing, or the fingerprint is different, this installer can't be used on that
unit (see [Firmware the installer doesn't know](#firmware-the-installer-doesnt-know)).

## The backup

Before it writes anything, the installer reads the whole eMMC (about 15.6 GB) into
`backups/skylight-<serial>/` on the computer. It hashes the copy as it arrives. Then the unit reads
each partition again and the hashes have to match. Without a verified backup nothing is written.

- It takes a while over USB. The installer says how far it is every half minute.
- **Keep that folder.** It is the only way back to the unit as it came.
- `userdata`, `cache` and `metadata` are copied while Android is running and using them. They are
  copied as they were, but not compared. The rest is checked.
- The folder also holds `restore/RESTORE.txt`: the way back, with your unit's own numbers.
- The backup notes the eMMC chip's id (its CID), and is only used for that chip. A backup of
  another unit, or one made by an installer before v1.3.0, is refused and left as it is: move it
  aside for a new one.

The installer also copies the unit's Wi-Fi and Bluetooth firmware out of its vendor partition, into
`backups/skylight-<serial>/firmware/`. Those files are the vendor's, so TECHO5 doesn't ship them.
They go back onto the same unit during the install, and nowhere else.

## The risk

What the install changes on the eMMC:

- **The partition table.** Android's `userdata` partition becomes two of TECHO5's own:
  `techo5-store` (2 GiB, TECHO5's system) and `techo5-data` (the rest, TECHO5's settings).
- **`recovery`.** Android's recovery is replaced by TECHO5's kernel.
- **`misc`, its first 2048 bytes.** They say `boot-recovery`, so the stock boot loader starts TECHO5
  every time.
- **`cache`.** It gets a `techo5-install` folder with the install set.

Nothing else is written. The boot loader (`uboot`, `trust`), Android's `boot` and `super` (Android
itself) stay as they are.

What could go wrong:

- It has been tried on one unit. Another unit may differ in a way the checks don't catch.
- The one-command install hasn't run end to end on a unit yet. The unit it was tried on had the same
  writes made by the same install script, started by hand; the installer image that starts it by
  itself is new.
- If the power goes out while the install writes, the unit may not start until it is written back
  from the backup in Loader mode.
- The way back in Loader mode has not been tried on this model yet. Reading the eMMC in Loader mode
  works; writing it hasn't been tried (other projects did write these units over the same USB mode).

## Install

Try a dry run first. It runs every check and prints every command that would write, and writes
nothing to the unit:

```
python3 tools/install-skylight.py --dry-run
```

Then install:

```
python3 tools/install-skylight.py --name Kitchen
python3 tools/install-skylight.py --name Kitchen --wifi MyNetwork
```

`--name` is the name Home Assistant shows. `--wifi` adds a network; the installer asks for its
passphrase, and only the WPA key goes to the unit. Without `--wifi`, pick a network on the unit's
screen once TECHO5 starts.

It takes the images from the latest release (`--release <tag>` for another one), checked against
the release's signed manifest. To use images you built, pass all three: `--installer`, `--boot` and
`--store` (see [tools/linux/skylight/README.md](../tools/linux/skylight/README.md)).

What happens:

1. **Checks.** The firmware, `adb root`, the board, the eMMC's size, the partition table, the vendor
   firmware, room on `/cache`. Any one that fails stops it and says why.
2. **Images.** The release's installer, boot and store images, checked.
3. **Backup.** The whole eMMC, checked. A verified backup already there is checked and kept.
4. **Firmware.** The unit's Wi-Fi and Bluetooth files, into the backup folder.
5. **Confirm.** It says what will be erased and how to go back. Type `ERASE` to go on.
6. **Stage.** The install set goes onto `/cache`, and the installer image into `recovery`. Each file
   is checked on the unit. Then the unit restarts.
7. **Install.** On the unit, by itself. The screen says what it is doing. **Don't unplug it.** It
   writes the new partition table, TECHO5's system, its data partition, the unit's firmware, your
   settings and TECHO5's kernel, and reads each one back. Then it restarts into TECHO5.

The first start grows TECHO5's partitions to their full size. That takes a minute.

### Add it to Home Assistant

Settings → Devices & services: the unit appears under Discovered as an ESPHome device with the name
you gave it. Add it and paste the key the installer printed. It is also in
`backups/skylight-<serial>/home-assistant.key`. If it doesn't appear, add the ESPHome integration by
hand with the unit's address and port 6053.

## Updates

Updates come from Home Assistant, as on the Echos: the update card installs the next release into
TECHO5's other system slot and goes back to the one before if the new one doesn't come up.

A release can bring a new kernel too. Once the new system has run for a few minutes with Wi-Fi, the
screen and the sound card working, the kernel goes into `recovery`, on trial for three starts. If it
never settles, the kernel before it goes back by itself, and so does one that starts but can't hand
over to the system.

Two cases can't be caught that way, because `recovery` is the only partition the unit starts TECHO5
from, and the new kernel is written over the old one there:

- **Power lost while the kernel is written.** The write takes a second or two. Cut in that window,
  `recovery` holds part of each kernel and the unit stays dark.
- **A kernel that panics before TECHO5 starts.** The unit restarts by itself, over and over,
  and never gets as far as counting its tries.

The way back from either is Loader mode (below): write the boot image of the release before it into
`recovery`, from a computer:

```
rkdeveloptool wl <recovery> techo5-boot-skylight-<version>.img
rkdeveloptool rd
```

`<recovery>` is in the backup's `restore/RESTORE.txt`. The boot image is on that release's page.

## If the install stops

- **Before the install, on the computer.** Nothing was written. Fix what it says and run it again.
- **On the unit, before writing anything.** The unit restarts into Android, which is as it was. The
  installer says why. Fix it, then run it again with `--resume`. That uses the set already on the
  unit and the backup already made.
- **On the unit, after writing began.** The unit stays on the installer screen with what happened,
  and doesn't restart by itself. Leave it plugged in. Its micro-USB port is a root shell (USB serial).
  [Open an issue](https://github.com/HuskerMinion/techo5/issues) with what the screen says. The way
  back is the backup, below.

## Firmware the installer doesn't know

The installer knows one firmware: D156.V1.3, a userdebug build where `adb root` works without a
prompt. It refuses anything else, because a newer firmware may change what the install relies on
(adb, `adb root`, the partition table). Please open an issue with the fingerprint
(`adb shell getprop ro.build.fingerprint`). Don't edit the installer's list to get past it.

## The way back

The backup folder's `restore/RESTORE.txt` has these steps with your unit's numbers. They need
`rkdeveloptool` and the unit in Loader mode: unplug the power, hold Volume down and plug the power back
in. `rkdeveloptool ld` should list it as Loader. On Windows the
Rockusb device needs a WinUSB driver (for example with Zadig).

From the backup folder:

**The unit as it came** (the whole eMMC, slow):

```
rkdeveloptool wl 0 emmc.img
rkdeveloptool rd
```

**Only what the install changed** (the partition table, `recovery`, `misc`, and the start of the old
`userdata` cleared). Android then starts like a new unit:

```
rkdeveloptool wl 0 restore/lba0-33.bin
rkdeveloptool wl <last 33 sectors> restore/last33.bin
rkdeveloptool wl <recovery> restore/recovery.img
rkdeveloptool wl <misc> restore/misc.img
rkdeveloptool wl <userdata> restore/zero-1MiB.bin
rkdeveloptool rd
```

The numbers in `<...>` are in `RESTORE.txt`. Don't write anything else.

Without Loader mode: writing the first 2048 bytes of `misc` back to zeros, from TECHO5's USB serial
shell, is meant to start stock Android again with a fresh setup:

```
dd if=/dev/zero of=/dev/mmcblk0p4 bs=2048 count=1 conv=notrunc,fsync
```

**This has not been tried.** In TECHO5's partition table there is no `userdata` for Android to
find, so Android may not start, and the unit may come back to TECHO5. Rely on the backup.
