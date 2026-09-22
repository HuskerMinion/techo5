# Getting started

From a stock Amazon Echo to one running TECHO5, step by step, for each device TECHO5 supports. Each
step links to the guide that does it; this page is the order to do them in, and what to check before
moving on.

> **Read this first.** Unlocking an Echo uses bootloader exploits. It can brick the device, it voids
> whatever warranty is left, and it wipes the device's data. Do it on a unit you can afford to lose,
> keep it on mains power the whole time, and never unplug it while anything is being written. These
> are hobby projects, not products.

## Which Echo do you have?

The model number is on the bottom of the device, or in the Alexa app under the device's settings.

| Device | Codename | Model | Supported by | Difficulty today |
|---|---|---|---|---|
| **Echo Show 5, 2nd gen** (2021) | `cronos` | AEOCN | [TECHO5](https://github.com/HuskerMinion/techo5) | Moderate: the unlock and LineageOS by hand, then a one-command installer |
| **Echo Dot, 2nd gen** (2016) | `biscuit` | RS03QR | [TECHO5 Dot](https://github.com/HuskerMinion/techo5-dot) | Moderate: the unlock and Fire OS steps by hand, then a one-command installer |
| **Echo Spot, 1st gen** (2017) | `rook` | VN94DQ | [TECHO5 Spot](https://github.com/HuskerMinion/techo5-spot) | Moderate: the unlock and LineageOS by hand, then a one-command installer |
| **Echo Show 5, 1st gen** (2019) | `checkers` | — | [TECHO5](https://github.com/HuskerMinion/techo5) | Moderate: the unlock and LineageOS by hand, then the same one-command installer |
| **Echo Show 8, 1st gen** (2019) | — | — | Coming: planned | Not yet: work starts when test units arrive |

Both generations of the Show 5 install with the same command and run the same build, which tells them
apart when it starts; the installer picks each one's boot image out of the release. The 1st gen has been
tested end to end, though on one unit so far. The Show 8 1st gen is planned, with nothing to install yet
and no dates. Other Echos (the Dot 3rd gen and later, and so on) are **not** supported.

## What every device needs

- A **USB data cable** for the device's USB port (micro-USB on the Dot and the Spot). A charge-only
  cable won't work.
- **Home Assistant** with the ESPHome integration (built in).
- A Windows, Linux or macOS computer, set up as below. The unlock steps differ by system; see
  [Windows, Linux or macOS](#windows-linux-or-macos).

## Set up your computer (once)

The installers are Python 3 scripts that work the same on Windows, Linux and macOS. Nothing is compiled:
they download the release, check the manifest's signature against the project's key and every file
against its checksum, and build the Dot and Spot boot
images from your own unit's backup. You need Python 3, the Android platform tools (`adb` and
`fastboot`) and `git`:

- **Windows:** install [Python 3](https://www.python.org/downloads/) (tick "Add python.exe to PATH"),
  [Git for Windows](https://git-scm.com/download/win), and the
  [platform tools](https://developer.android.com/tools/releases/platform-tools) (unzip them and add the
  folder to PATH). In PowerShell or Command Prompt, type `python` where this guide says `python3`.
- **Linux** (Ubuntu or Debian): `sudo apt install python3 git adb fastboot`, then
  `sudo usermod -aG dialout $USER` and log in again, so the installers can open the device's USB serial
  console. If ModemManager is installed, stop it while installing (`sudo systemctl stop ModemManager`);
  it grabs new USB serial ports.
- **macOS:** `xcode-select --install` (Python 3 and git), then
  `brew install android-platform-tools` ([Homebrew](https://brew.sh)).

Check with `python3 --version` and `adb version`. The unlock threads on XDA also need a (free) XDA
account to download attachments.

## Echo Show 5 (1st and 2nd gen)

The steps are the same for both; where they differ, the 1st gen (`checkers`) is called out.

1. **Unlock it with amonet.** On a 2nd gen, follow
   [[UNLOCK][ROOT][TWRP][UNBRICK] Echo Show 5 2nd Gen (cronos)](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-2nd-gen-2021-cronos.4772596/)
   on XDA (source: [R0rt1z2/amonet, branch mt8163-cronos](https://github.com/R0rt1z2/amonet/tree/mt8163-cronos)).
   In short: with the Show on mains power, hold all three buttons until the screen says
   `=> FASTBOOT mode`, connect USB, and run the fastbrick step from the thread. It reboots into TWRP
   on its own; don't interrupt it.
   **Use amonet v2.0.1 or later.** An older one can leave the bootloader still locked while
   everything else looks like it worked, and the first sign is the installer being refused with
   `the command you input is restricted on locked hw` when it writes the boot image. That is the
   bootloader talking, not the installer (techo5 issue #19).
   On a **1st gen**, the same thing with the `checkers` tools:
   [[UNLOCK][ROOT][TWRP][UNBRICK] Echo Show 5 1st Gen (checkers)](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-5-1st-gen-2019-checkers.4762900/)
   (amonet branch `mt8163-checkers`). A unit fresh out of the box may have shipped with firmware too
   new to unlock, so read the version before letting it reach the internet.
   *Check:* the Show boots into TWRP.
2. **Install LineageOS 18.1.** Follow
   [[ROM][UNOFFICIAL][11][cronos] LineageOS 18.1 for the Echo Show 5 (2021)](https://xdaforums.com/t/rom-unofficial-11-cronos-lineageos-18-1-for-the-amazon-echo-show-5-2021.4772598/).
   Use a current build (0.4 or later; earlier ones lose audio after a few days). A **1st gen** takes
   R0rt1z2's `checkers` build of the same LineageOS, dated 2026-09-04 or later: TECHO5's kernel is
   rebuilt from that source, and the installer checks the version before it does anything.
   *Check:* LineageOS boots.
3. **Prepare LineageOS:** join your Wi-Fi, then in Settings → About → tap Build number seven times,
   and in Developer options turn on **USB debugging**.
   *Check:* `adb devices` on the computer lists the Show as `device`.
4. **Install TECHO5.** With the Show connected by USB and on LineageOS:
   ```
   git clone https://github.com/HuskerMinion/techo5
   cd techo5
   python3 tools/install-show.py --serial <serial> --name "Kitchen" --dry-run
   python3 tools/install-show.py --serial <serial> --name "Kitchen"
   ```
   `<serial>` is what `adb devices` shows. The dry run downloads and checks the
   [latest release](https://github.com/HuskerMinion/techo5/releases/latest) (boot image with Bluetooth,
   root filesystem); the second run replaces LineageOS with TECHO5 (it asks before erasing) and waits
   for the first boot. Turning on **Rooted debugging** first lets it keep a backup of LineageOS's boot
   image. Every step by hand, and troubleshooting: [docs/install.md](install.md).
   *Check:* the screen shows the TECHO5 clock.
5. **Add it to Home Assistant**: see [After installing](#after-installing-every-device).

## Echo Dot (2nd gen)

These steps follow [@proffalken](https://github.com/proffalken)'s [write-up](https://gist.github.com/proffalken/377ae50146affe1886dddaaacb87926b)
of installing TECHO5 Dot from Linux, which found the exact Fire OS build that avoids SELinux boot
loops. Every command runs the same on Windows, Linux and macOS once the Dot is unlocked.

1. **Unlock it with amonet-biscuit.** Follow
   [[UNLOCK][ROOT][TWRP][UNBRICK] Echo Dot 2nd Gen / 2016 (biscuit)](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/)
   (source: [R0rt1z2/amonet, branch mt8163-biscuit](https://github.com/R0rt1z2/amonet/tree/mt8163-biscuit)).
   Use the release ZIP attached to the thread, not the bare git repository: it has `fastbrick.sh`
   (Linux/macOS), `fastbrick.bat`/`fastbrick.ps1` (Windows), `boot-root.zip` and the files they
   need — same as the Show's and Spot's amonet forks, so Windows works directly here too.
   - Hold the **Action** button while plugging in power; the light turns **green** (Amazon's factory
     fastboot mode). Connect USB.
   - Run `fastbrick.bat` (or `./fastbrick.sh` on Linux/macOS), check the device it found, and type
     `YES`. It ends with `Exploit most likely successful!` and reboots into TWRP (a **white** light).
   - To get back into TWRP later: unplug power, hold **Volume Up**, plug power back in, and wait for the
     white light.

   *Check:* `adb devices` lists the Dot as `recovery`.
2. **Flash Fire OS 6574.1 into both slots.** TECHO5 Dot's Bluetooth kernel is built from this exact
   build's source (NS6574, build 7623), and its Wi-Fi driver is loaded from Fire OS's system partition,
   so other builds don't match. The file is
   `update-kindle-biscuit_puffin-NS6574_user_7623_0013121734532.bin` (the XDA thread links Amazon's
   update files). From TWRP:
   ```
   adb shell twrp wipe cache
   adb shell twrp wipe data
   adb push update-kindle-biscuit_puffin-NS6574_user_7623_0013121734532.bin /sdcard/update.zip
   adb shell twrp install /sdcard/update.zip
   adb reboot recovery
   adb shell twrp install /sdcard/update.zip
   ```
   Installing twice puts it in both A/B slots. A "no selinux policy bundled" warning in the install log
   means the build doesn't match; stop and get the right file.
3. **Root it.** Still in TWRP:
   ```
   adb push boot-root.zip /sdcard/
   adb shell twrp install /sdcard/boot-root.zip
   ```
4. **Trust this computer's adb key**, so Fire OS never needs to show an authorization prompt (the Dot
   has no screen to show it on):
   ```
   adb push ~/.android/adbkey.pub /sdcard/adbkey.pub
   adb shell "mkdir -p /data/misc/adb && cp /sdcard/adbkey.pub /data/misc/adb/adb_keys && chown 1000:2000 /data/misc/adb/adb_keys && chmod 640 /data/misc/adb/adb_keys"
   adb shell restorecon -v /data/misc/adb/adb_keys
   adb reboot
   ```
   On Windows the key is `%USERPROFILE%\.android\adbkey.pub`. (Run `adb devices` once first if the
   file doesn't exist yet.)
5. **Join Wi-Fi once in Fire OS.** Complete the Alexa app's Wi-Fi step; skipping the rest of the Alexa
   setup is fine. The installer reads the saved network. (Skip this and it asks for a network instead.)
   *Check:* `adb devices` lists the Dot as `device`, and `adb shell id` says `uid=0`.
6. **Install TECHO5 Dot.**
   ```
   git clone https://github.com/HuskerMinion/techo5-dot
   cd techo5-dot
   python3 tools/install-dot.py --serial <serial> --dry-run
   python3 tools/install-dot.py --serial <serial> --name "Kitchen"
   ```
   `<serial>` is what `adb devices` shows. The dry run checks the Dot, backs up every partition that
   boots it into `backups/<serial>/` (keep that folder: it's the way back), downloads and checks the
   release, and builds this Dot's boot image, writing nothing to the Dot. The second run installs, with
   Bluetooth, and waits for the first boot to report healthy. (The installer also works straight from
   TWRP after step 2, without steps 3 to 5.)
   *Check:* the installer ends with the Dot healthy and its Home Assistant port answering.
7. **Add it to Home Assistant**: see [After installing](#after-installing-every-device).

### Going back to TWRP or Fire OS

TECHO5 Dot lives in the Dot's recovery partition, where TWRP was, and asks for recovery on every
boot, so holding + at power-on starts TECHO5, not TWRP, and flashing Fire OS again changes neither.
The installer keeps the way back:

- **While TECHO5 runs** (SSH switched on in Home Assistant): `to-twrp --yes` puts the Dot's own TWRP
  back and reboots into it. From TWRP, `adb shell sh /cache/techo5/back-to-linux.sh` returns to
  TECHO5.
- **From amonet's fastboot mode** (see the unlock thread), with the `backups/<serial>/` folder the
  installer made:
  ```
  fastboot -s <serial> flash recovery backups/<serial>/recovery.img
  fastboot -s <serial> flash misc backups/<serial>/misc.img
  fastboot -s <serial> reboot
  ```
  The first puts TWRP back; the second puts back the boot setting from before the install, so the
  Dot stops going straight to recovery. Then hold + at power-on for TWRP, or power on normally for
  Fire OS.

Prefer to keep Fire OS? [EchoLocal](https://github.com/ygelfand/echolocal), the project TECHO5's daemon
is built on, runs on the unlocked Dot's Fire OS 6 with its own installer, and is the gentler path.

## Echo Spot (1st gen)

1. **Check the Fire OS version.** amonet-rook supports Fire OS **5.5.6.9, 5.5.5.2 and 5.5.3.4** only.
   On the Spot: Settings → Device Options → Device Software Version. If it's older, let it update
   first.
2. **Unlock it with amonet-rook.** Follow
   [[UNLOCK][ROOT][TWRP][UNBRICK] Echo Spot 2017 (rook)](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-spot-2017-rook.4754878/)
   (source: [R0rt1z2/amonet, branch mt8163-rook](https://github.com/R0rt1z2/amonet/tree/mt8163-rook)).
   Everything goes through the micro-USB port under the back cover. The bench unit was unlocked from
   Windows with the thread's `fastbrick` step (fastboot: hold Volume Up, Volume Down and Mute at
   power-on); the thread also has the Linux route. Plan for the Spot's data to be wiped.
   *Check:* the Spot boots into TWRP.
3. **Install LineageOS 18.1.** Follow
   [[ROM][UNOFFICIAL][11][rook] LineageOS 18.1 for the Echo Spot (2017)](https://xdaforums.com/t/rom-unofficial-11-rook-lineageos-18-1-for-the-amazon-echo-spot-2017.4762459/).
   *Check:* LineageOS boots.
4. **Prepare LineageOS:** join your Wi-Fi, and in Developer options (tap Build number seven times to
   show them) turn on **USB debugging** and **Rooted debugging**; the installer needs adb as root.
   If the on-screen keyboard won't type digits in the Wi-Fi password, add the network from the
   computer with `adb shell cmd wifi connect-network "<network>" wpa2 "<password>"`.
5. **Back it up** from TWRP (`adb reboot recovery` from LineageOS), with the Spot connected by USB:
   ```
   git clone https://github.com/HuskerMinion/techo5-spot
   cd techo5-spot
   python3 tools/backup-spot.py --serial <serial> --include-system
   ```
   Keep `backups/<serial>/`: it's the way back to LineageOS and Fire OS.
6. **Install TECHO5 Spot**, booted back into LineageOS with rooted debugging on:
   ```
   python3 tools/install-spot.py --serial <serial> --name "Kitchen" --build-only
   python3 tools/install-spot.py --serial <serial> --name "Kitchen"
   ```
   The first run captures what it needs from this Spot, downloads and checks the release, and builds
   the boot image, touching nothing else. The second replaces LineageOS with TECHO5 (it asks before
   erasing), with Bluetooth, and waits for the first boot. `--logo` also replaces the bootloader's
   Amazon picture (needs `python3 -m pip install pillow`).
   *Check:* the round screen shows the TECHO5 clock.
7. **Add it to Home Assistant**: see below.

## After installing (every device)

1. **Add it.** Home Assistant discovers it as an ESPHome device (Settings → Devices & services).
   Paste the encryption key the installer printed. It's also saved in
   `backups/<serial>/home-assistant.key` (a Show or Spot installed before 2026-09-19:
   `backups/<serial>/api.psk`); keep that folder.
2. **Allow it to perform Home Assistant actions.** On the device's ESPHome entry → Configure, turn on
   **Allow the device to perform Home Assistant actions**. The radio favorites, phone call events and
   some screen features need it.
3. **Pick a wake word** on the device's Assist satellite: Okay Nabu, Hey Jarvis, Hey Mycroft, Alexa,
   and eight more (Computer, Jarvis, Home Assistant and others), the same on every device.
4. **Updates** from then on come from Home Assistant's update card: signed releases install into the
   spare slot and roll back on their own if they don't come up healthy.
5. **Optional:**
   - [phone calls](phone.md) through your own SIP provider;
   - SSH (keys only) through the `ssh_keys` action and the SSH switch;
   - cameras, radio lists and weather from the device's Home Assistant actions (see each README);
   - multi-room audio through [Music Assistant](https://www.music-assistant.io/) (each device is a
     Sendspin player).

   **What multi-room audio trusts.** The Sendspin player is on from the first boot and takes a
   connection from anything on the network, with no key and no pairing — that is what lets Music
   Assistant find a device and play to it with nothing to set up. It also means a program on your
   Wi-Fi can play audio into the room and set the volume without being invited. It cannot listen: the
   microphones are not part of it, and nothing it sends reaches Home Assistant.

   That is a deliberate choice for a device on a home network, not an oversight, and it is the one
   thing here that trusts the network rather than a key. If your Wi-Fi has guests on it, or anything
   you would not hand a speaker to, turn the player off per device with the Sendspin switch in Home
   Assistant.

### If Home Assistant does not find it

Home Assistant finds these devices over mDNS, the same way it finds ESPHome boards.

- **A different subnet or VLAN.** mDNS does not cross subnets on its own. Either put Home Assistant
  and the device on the same one, or set up an mDNS reflector on the router for `_esphomelib._tcp`
  (and `_sendspin._tcp` for multi-room audio).
- **Home Assistant in Docker.** A container on the default bridge network never sees mDNS, so nothing
  is discovered. Run it with `network_mode: host` (Docker Compose) or `--network host`; macvlan works
  too.
- **Add it by hand instead.** Settings → Devices & services → ESPHome → Add device, then the device's
  address, port 6053, and the encryption key from `backups/<serial>/`. This works without mDNS, as
  long as Home Assistant can reach the address.

### If it listens but does not answer out loud

The device wakes, the screen shows the answer, and nothing is spoken. That is almost always the voice
pipeline rather than the device: Home Assistant is replying in text only.

1. **Settings → Voice assistants**, and open the pipeline this device uses. That is the one marked
   Preferred, unless you picked another one in the device's Assistant list.
2. Look at **Text-to-speech**. If it says None, choose an engine (Piper, Home Assistant Cloud, Google
   Translate), press Update, and ask the device something again.
3. **Still silent with an engine set?** Try Home Assistant's speech straight at the device: Settings →
   Developer tools → Actions, search for `tts.speak`, put your text-to-speech entity in as the target,
   the device's media player entity in Media player entity, type a message and press Perform action.
   - If it speaks, the pipeline is what is wrong: back to step 2.
   - If it stays silent, the device's own log says whether any audio arrived. Turn SSH on and read
     `/data/techo5-linux/techo5.log`, or open an issue with the last 60 lines of it.
4. **Reply delivery.** The device has a Reply delivery setting: *Whole file* fetches the reply and
   plays it, *Streamed* plays it as it arrives. Whole file is the default because it survives a slow
   network better, but it needs to be able to reach the address Home Assistant gives it for the audio.
   Where it cannot, the device falls back to the streamed copy on its own (releases from 2026-09-20
   on), and setting Reply delivery to **Streamed** makes that permanent.

## Windows, Linux or macOS

| Step | Windows | Linux | macOS |
|---|---|---|---|
| Unlock: Show 5 (amonet-cronos) | Yes (fastbrick) | Yes | Use a Linux live USB |
| Unlock: Dot (amonet-biscuit) | Yes (`fastbrick.bat`/`fastbrick.ps1`, in the release ZIP) | Yes | Use a Linux live USB |
| Unlock: Spot (amonet-rook) | Yes (fastbrick, as on the bench unit) | Yes | Use a Linux live USB |
| LineageOS (Show 5, Spot) | Yes | Yes | Yes (TWRP and `adb` only) |
| Install TECHO5 on the Show 5 (`install-show.py`) | **Yes** | **Yes** | **Yes** |
| Fire OS 6574.1, root, adb key (Dot) | Yes | Yes | Yes |
| Install TECHO5 Dot (`install-dot.py`) | **Yes** | **Yes** | **Yes** |
| Install TECHO5 Spot (`install-spot.py`) | **Yes** | **Yes** | **Yes** |
| Updates after that | Home Assistant | Home Assistant | Home Assistant |

**On Linux:**
- A serial terminal for working on a unit by hand: `screen /dev/ttyACM0 115200` (or `picocom`).
- If ModemManager is installed, stop it while working with serial consoles and the BootROM
  (`sudo systemctl stop ModemManager`); it grabs new USB serial ports.

**On macOS:**
- `screen` is built in, for working on a unit by hand: `screen /dev/cu.usbmodem* 115200`.
- The amonet BootROM steps need Linux. A live USB (Ubuntu) is more reliable than a virtual machine:
  the exploit re-enumerates USB mid-way, and VM USB passthrough often loses the device.
- The installers work from Terminal once the device is unlocked (and, for the Show 5 and the Spot,
  on LineageOS).

**On Windows:** type `python` for `python3`. Git Bash is only needed for building images yourself
(each repository's `docs/building.md`).

## If something goes wrong

- **Before TECHO5 is installed:** each XDA thread has an unbrick section. Don't improvise with
  bootloader images; that is how Echos get hard-bricked.
- **After:** every TECHO5 device falls back to its previous slot, and then to a rescue environment
  with a USB serial console, if a boot doesn't come up healthy. The Show and Spot keep TWRP; the
  Dot keeps a copy of it (see [Going back to TWRP or Fire OS](#going-back-to-twrp-or-fire-os)). Each
  repository's docs describe the way back to LineageOS or Fire OS.
- Ask in the project's GitHub issues, with the device, the step and what it printed.

## Credits

The unlocks are the work of [R0rt1z2](https://github.com/R0rt1z2) and k4y0z (amonet, kaeru and the
Echo TWRP builds); LineageOS for these devices is R0rt1z2's and
[amazon-oss](https://github.com/amazon-oss)'s. TECHO5's daemon is built on
[EchoLocal](https://github.com/ygelfand/echolocal) by Yuri Gelfand. The Echo Dot steps come from
[@proffalken](https://github.com/proffalken)'s
[write-up](https://gist.github.com/proffalken/377ae50146affe1886dddaaacb87926b) of installing TECHO5
Dot from Linux, whose fixes also made the installers cross-platform.
