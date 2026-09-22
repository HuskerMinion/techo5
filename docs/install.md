# Installing TECHO5 on an Echo Show 5

Step by step, from an Echo Show 5 (2nd generation, `cronos`) running LineageOS to one running
TECHO5. Written from a first install on a second unit (2026-09-16); every command here was run on
that install. Placeholders: `<serial>` is the unit's adb/fastboot serial, `<address>` its IP address
on your network, `<version>` a release such as `v0.2.7`.

**This erases Android.** LineageOS on the `system` partition is replaced by the TECHO5 slot store.
The one part of LineageOS TECHO5 keeps is its `vendor` tree (the Wi-Fi and Bluetooth drivers and
firmware): releases don't carry it, so it is copied into the store before the partition is erased.
`userdata` is kept (TECHO5 reads the Wi-Fi network Android saved from it), and TWRP stays in
`recovery`, so LineageOS can be put back with TWRP and its zip.

## Unlock the bootloader first

**The bootloader has to be unlocked before any of this works.** Running LineageOS does not mean it
is: the unlock is a separate exploit, with a shorting trick to get the device into BROM mode, not a
`fastboot flashing unlock`. Without it the installer stops at the first write with

```
FAILED (remote: 'the command you input is restricted on locked hw')
```

- Show 5 **2nd gen** (cronos):
  [amonet-cronos](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-2nd-gen-2021-cronos.4772596/)
- Show 5 **1st gen** (checkers):
  [amonet-checkers](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/)

## What you need

- An Echo Show 5 2nd gen, **unlocked as above**, running
  [LineageOS 18.1](https://xdaforums.com/t/rom-unofficial-11-cronos-lineageos-18-1-for-the-amazon-echo-show-5-2021.4772598/),
  connected to your Wi-Fi in Android, with USB debugging on.
- Its power adapter and a USB **data** cable to the PC. Keep it on mains power while flashing.
- A Windows, Linux or macOS computer with `adb` and `fastboot` (Android platform tools).
- Home Assistant with the ESPHome integration.

## The quick way: one command

With Python 3, `adb`, `fastboot` and `git` (setup for each system:
[getting started](getting-started.md#set-up-your-computer-once)):

```
git clone https://github.com/HuskerMinion/techo5
cd techo5
python3 tools/install-show.py --serial <serial> --name "Kitchen" --dry-run   # download and check the release only
python3 tools/install-show.py --serial <serial> --name "Kitchen"
```

On Windows, type `python` instead of `python3`.

It does steps 1 to 6 below: downloads the latest release and checks the boot image and root filesystem
against their checksums, keeps LineageOS's boot image in `backups/<serial>/` when adb is root, flashes,
creates the slot store over the USB serial console (it asks before erasing), provisions the name and
the Home Assistant key (kept in `backups/<serial>/home-assistant.key`), and waits for the first boot.
`--ssh-key ~/.ssh/id_ed25519.pub` also turns SSH on with your key. Then go to
[step 7](#7-add-it-to-home-assistant).

On Linux you may need to be in the `dialout` group for the serial console
(`sudo usermod -aG dialout $USER`, then log in again), and ModemManager, if installed, should be
stopped while installing (`sudo systemctl stop ModemManager`).

## By hand

The same steps, typed. On Windows work in Git Bash, which rewrites arguments that look like paths,
including paths on the device, so set `export MSYS_NO_PATHCONV=1` first or `adb push … /sdcard/…`
lands somewhere else. Linux and macOS shells need nothing extra.

If more than one Android or fastboot device is plugged in, pass `-s <serial>` to every `adb` and
`fastboot` command and check `adb devices -l` first.

## 1. Get the boot image

The boot image is the kernel plus the small rescue environment that sets a unit up. Two ways:

- **From a release (simplest).** Download `techo5-boot-<version>.img` — on an Echo Show 5 1st gen
  (checkers), `techo5-boot-checkers-<version>.img`, and on an Echo Show 8 (crown),
  `techo5-boot-crown-<version>.img`, each built against that board's own
  kernel and device tree — from the same
  [release](https://github.com/HuskerMinion/techo5/releases) as the root filesystem. The boot image
  changes rarely, so most releases don't carry one; when yours doesn't, take it from the newest
  earlier release that does. It carries no SSH key, so steps 4 and 5 are typed into the unit's
  **USB serial console**.
- **Built yourself with your own SSH key**, to SSH into the rescue environment instead:

  ```
  ssh-keygen -t ed25519 -f techo5_ed25519        # into your inputs directory
  ```

  then build the kernel and boot image as [docs/building.md](building.md) describes.

Either way the kernel is the LineageOS commit the Show's own kernel came from, so the vendor Wi-Fi
and Bluetooth modules load. Check before flashing:

```
adb -s <serial> shell uname -r      # must be 4.9.337-g8d928c5176cc
```

## 2. Put the root filesystem on the unit

Download `techo5-rootfs-<version>.tar.gz` from the latest
[release](https://github.com/HuskerMinion/techo5/releases) and copy it to the unit's storage, which
TECHO5 can read from its rescue environment:

```
adb -s <serial> push techo5-rootfs-<version>.tar.gz /sdcard/Download/
adb -s <serial> shell md5sum /sdcard/Download/techo5-rootfs-<version>.tar.gz   # compare with your copy
```

## 3. Flash the boot image

```
adb -s <serial> reboot bootloader
fastboot devices                                  # exactly the unit you mean
fastboot -s <serial> flash boot techo5-boot-<version>.img    # or your own techo5-linux-boot.img
fastboot -s <serial> continue
```

The unit boots the TECHO5 initramfs. With LineageOS still on `system` there is no slot store, so it
stays in the **rescue environment**: it joins the Wi-Fi network Android saved and shows a test
screen. Open a root shell on it:

- **USB serial console** (any boot image), at 115200 baud; press Enter for a `#` prompt:
  - Windows: a new "USB Serial Device (COMn)" in Device Manager; open it in PuTTY (connection type
    Serial).
  - Linux: `screen /dev/ttyACM0 115200` (or `picocom -b 115200 /dev/ttyACM0`).
  - macOS: `screen /dev/cu.usbmodem* 115200`.
- **SSH** (a boot image built with your key), within about a minute:

  ```
  ssh -i techo5_ed25519 root@<address>
  ```

## 4. Create the slot store and install

In the rescue shell. `mkstore` is the step that erases LineageOS.

```
cat /proc/idme/serial                             # the unit you mean
tar -cf /data/techo5-linux/vendor.tar -C /android/system vendor   # this unit's drivers and firmware
umount /android                                   # LineageOS's system, mounted read-only
PATH=/usr/local/sbin:$PATH
slotctl mkstore /dev/mmcblk0p12 --i-know-this-erases-it
tar -xf /data/techo5-linux/vendor.tar -C /store   # kept in the store from now on
STORE=/store slotctl install /data/media/0/Download/techo5-rootfs-<version>.tar.gz
[ -e /store/slots/a/vendor/lib/modules/mt76x8_wlan.ko ] || cp -a /store/vendor/. /store/slots/a/vendor/
rm /data/techo5-linux/vendor.tar
STORE=/store slotctl status                       # slot a: trial 3
```

Run `mkdir -p /data/techo5-linux` first if the `tar -cf` line says the directory is missing.

If `mkfs failed` mentions `libgcc_s.so.1`, the boot image predates the fix (one older than v0.2.8):
take the library from the root filesystem and run `mkstore` again.

```
tar xzf /data/media/0/Download/techo5-rootfs-<version>.tar.gz -C /tmp ./usr/lib/libgcc_s.so.1
cp /tmp/usr/lib/libgcc_s.so.1 /usr/lib/
```

## 5. Provision before the first boot

Still in the rescue shell. None of this is required, but each saves a step later.

```
mkdir -p /data/misc/techo5
printf 'Kitchen\n' > /data/misc/techo5/name       # the name Home Assistant shows

# The ESPHome encryption key. Keep the printed value for Home Assistant.
umask 077; head -c 32 /dev/urandom | base64 > /data/misc/techo5/psk; cat /data/misc/techo5/psk

# Only with a boot image built with your key: keep SSH after the switch to the slot. Otherwise
# skip these four lines; SSH stays off, and a key comes later from Home Assistant (ssh_keys).
mkdir -p -m 700 /data/misc/techo5/ssh
cp /root/.ssh/authorized_keys /data/misc/techo5/ssh/authorized_keys
chmod 600 /data/misc/techo5/ssh/authorized_keys
printf '{"security":{"ssh":true}}\n' > /data/misc/techo5/state.json   # only on a unit with no state.json yet
sync
```

## 6. Boot TECHO5

The rescue environment's PID 1 is a script, so a plain `reboot` does nothing:

```
/bin/busybox.static reboot -f
```

The first boot after `adb reboot bootloader` may stop at the bootloader ("hacked fastboot") once.
Continue it from the PC:

```
fastboot -s <serial> continue
```

The unit boots slot a, the daemon starts, and after five minutes of running the slot commits
(`slotctl status`: `good`).

## 7. Add it to Home Assistant

Settings → Devices & services: the unit appears under Discovered as an ESPHome device with the name
from step 5. Add it and paste the key printed in step 5 when asked. If it does not appear, add the
ESPHome integration by hand with host `<address>` and port 6053.

Then:

- **Time zone**: nothing to set. The unit starts on UTC and takes Home Assistant's zone as soon as it
  connects, and keeps it from then on.
- **Wake word**: the default is "Alexa". Change it on the device (swipe down from the top, Sound,
  Wake word) or in Home Assistant; the other follows.
- **Home Assistant token** (optional, for the forecast page, local radio stations and the list of
  weather sources): create a long-lived access token on your Home Assistant profile page, then run
  the `esphome.<device>_home_assistant` action with `url` (like `http://192.168.1.20:8123`) and
  `token`.
- **Weather**: Home Assistant's own forecast by default. To show another weather entity, use the
  settings screen (General, Weather), the "Weather source" select, or `esphome.<device>_home_weather`.
- **Radio**: three ways to get stations onto the device, none of which needs the others. See
  [Radio](#radio) below.
- **Security**: SSH, and the camera and screen pages on port 8181, have switches on the settings
  screen (Privacy) and in Home Assistant. SSH keys only come from Home Assistant (`esphome.<device>_ssh_keys`).
- **Updates**: the firmware update entity installs new releases into the other slot, reboots, and
  falls back if the new slot does not settle.
- The old Android integrations for the unit (ShowAssist, the View Assist companion) can be deleted.

## Radio

There are three places a station can come from. Any of them works on its own.

**1. Radio Browser (needs the Home Assistant token).** On a Show, swipe in from the right edge of
the clock and choose Radio; on a Spot, turn the dial to Radio. The device lists stations near home
and popular ones, from the Radio Browser integration Home Assistant sets up by itself. If it is
missing, add it under Devices & services. "Near home" is worked out from the location set in Home
Assistant, so set that if the list looks like somebody else's country.

**2. Stations kept on the device (needs nothing at all).** Open the setup page (Settings, Privacy,
Setup page) and add a station as a name and the address of its stream. The device plays these
itself, so they work with Home Assistant switched off, which is the point of them. Only an `http` or
`https` address is kept. They appear under Radio as "On this device", beside the lists that need
Home Assistant.

  On a Dot these can be typed on the setup page but not yet started: the radio page belongs to the
  screen, and a Dot has none. Ask it for music through Home Assistant until that is fixed.

**3. Your own favorites in Home Assistant.** If you already have an input_select of stations and a
script that plays one, wire them up with the `esphome.<device>_home_radio` action:

| Argument | What it is |
|---|---|
| `stations` | `input_select` entities whose options are station names, comma separated, listed in that order |
| `now` | an entity whose state names the station playing, shown on the screen while it plays |
| `service` | the script that plays a station, e.g. `script.radio_play_on_speaker` |
| `field` | the script's station argument (default `station`) |
| `speaker_field` | the script's argument for which speaker to play on |
| `speaker` | this device's media player entity, if the device should not work it out itself |

The device calls that script with the station name and its own media player entity, and the script
decides what to play. A script of two lines is enough:

```yaml
radio_play_on_speaker:
  fields:
    station: {}
    speaker: {}
  variables:
    urls:
      "KXYZ 101.1": "https://stream.example.org/kxyz"
      "The Mountain": "https://stream.example.org/mountain"
  sequence:
    - action: media_player.play_media
      target: { entity_id: "{{ speaker }}" }
      data:
        media_content_type: music
        media_content_id: "{{ urls[station] }}"
```

**Nothing to set up for the audio itself.** Home Assistant transcodes a stream for the device
through its own ESPHome proxy, which is part of that integration: the URLs in the log with
`/api/esphome/ffmpeg_proxy/` in them are its doing, and there is nothing to install or configure.

**If the stream is dropped.** Those proxy streams end by themselves sometimes: after five minutes,
or thirteen, with no pattern. The device notices and asks for the station again three seconds later.
It will do that three times in ten minutes and then leave it off, since a station that will not stay
up is not going to. The log says `the stream ended by itself, putting it back on`. Music starting
again on its own is this, not a fault.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| The daemon restarts every few seconds on a fresh install, log shows `slice bounds out of range` in `microwakeword` | v0.2.5 shipped damaged wake word models. Use v0.2.6 or later; on a unit already installed, copy good `.tflite` files over `/data/misc/techo5/models/`. |
| An update from Home Assistant fails with `context deadline exceeded` | The download was too slow, usually on 2.4 GHz next to the unit's own Bluetooth. Press Install again; from v0.2.5 the unit moves itself to the network's 5 GHz radio when one is in range. |
| The screen shows "hacked fastboot" | A leftover `reboot bootloader` request: `fastboot -s <serial> continue`. |
| No SSH after the switch to the slot | SSH is off unless step 5 was done: turn on the SSH switch in Home Assistant and send a key with the `ssh_keys` action, or use the USB serial console. |
| The unit sits in rescue | No bootable slot. `slotctl status` shows why; the daemon still runs from a slot in rescue, so Home Assistant keeps working while you look. |

To go back to LineageOS: boot TWRP from `recovery`, flash the LineageOS zip (which rewrites `system`)
and the LineageOS boot image.
