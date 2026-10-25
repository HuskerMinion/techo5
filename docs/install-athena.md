# Installing TECHO5 on an Echo Show 8 2nd gen (athena)

The 2nd gen Echo Show 8 (`athena`, 2021, MT8183) installs differently from the other Shows. There is
no LineageOS for it. TECHO5 brings its own 64-bit mainline Linux kernel and writes two partitions:

- `boot`: TECHO5's kernel and device tree, with the unit's own speaker tuning filled in (see below);
- `system`: the slot store. At its root, the boot and rescue environment (`tools/linux/athena/init`);
  beside it, root filesystem slots `a` and `b`, as on the other Shows (`slotctl`).

The kernel mounts `system` read-only as `/` and runs `/init` there. TECHO5 keeps its state in one
folder on `userdata` (`userdata/techo5`, which is `/data` on the unit) and touches nothing else there.
`vendor`, `recovery` (TWRP) and the unlock's own partitions are never written.

There is also an older way that keeps Amazon's 32-bit kernel: see
[Amazon's kernel instead](#amazons-kernel-instead) at the end.

Placeholders: `<serial>` is the unit's adb serial, `<version>` a release version.

## What works

On TECHO5's kernel:

- the screen (a real display driver, not the boot loader's framebuffer) and touch;
- the four microphones and the speaker, with Amazon's own tuning for the amp;
- Bluetooth audio with phones;
- the light sensor;
- Wi-Fi.

Known limits:

- **Two screens are tested: the ILI9881C panel with BOE glass, and the JD9365 with KD/HSD glass.**
  Amazon built this model with several panels. TECHO5's kernel has the others' settings too, but they
  haven't been tried on a unit yet. The installer checks which one your unit has (see [Which screen](#which-screen)).
- The light sensor works but its lux values aren't calibrated yet.
- The microphone beamformer is off; the microphones are used without it.

## Before you start

- **Unlock the unit with saiyaku-athena** (by R0rt1z2 with bengris32 and FieryFlames; see
  [Credits](#credits)), from the XDA thread [[UNLOCK][ROOT][TWRP][UNBRICK] Amazon Echo Show 8 2nd Gen - 2021 (athena)](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-2nd-gen-2021-athena.4804958/). It needs Fire OS 7080 (PS7574.1.7080N): update the Show to the latest Fire OS first. The
  unlock can take several tries; that's normal. When it's done, the unit has TWRP in `recovery`.
- **Never write `tee1`, `lk` or the preloader.** That is where the unlock lives, and writing any of
  them bricks the unit. The installer only ever writes `system` and `boot`, and checks each by name
  right before it writes.
- The unit is in TWRP (hold Volume Up while it powers on) and `adb devices` lists it as `recovery`.
- Python 3 and adb on the computer.
- About 4 GB free for the backup: the installer saves every partition except `cache` and `userdata`
  (the `system` partition alone is 3 GB) before it writes anything. **Keep that backup.** It is your
  way back to Fire OS, and the speaker tuning comes from it.

## Install

```
python3 tools/install-athena.py --name Kitchen
python3 tools/install-athena.py --name Kitchen --wifi MyNetwork --ssh-key ~/.ssh/id_ed25519.pub
python3 tools/install-athena.py --dry-run
```

It takes the images from the latest release (`--release <tag>` for another one), checked against the
release's signed manifest: `techo5-system-athena-<version>.img.gz` for `system` and
`techo5-boot-athena-<version>.img` for `boot`. To install images you built, pass both:
`--image system.img --boot boot.img`.

What it does, each step checked before the next:

1. **Checks.** adb sees the unit in TWRP as root; the unit says it is an athena; its partition table
   is the one the installer knows (`boot` p20, `system` p21, `userdata` p23, the unlock's `tee1_real`
   p3 and `lk_real` p18, and the rest). The screen is the one TECHO5's kernel drives. Anything else
   stops it before a single write. `userdata` can be mounted (read-only, when TWRP hasn't mounted it
   already). Nothing is unmounted yet.
2. **Backup.** Every partition except `cache` and `userdata` into `backups/<serial>/partitions/`, each
   compared with the sha256 the unit computes of its own copy. The `boot0` and `boot1` areas (the
   preloader's) are not in it: TWRP links them to `/dev/null`, so they can't be read from TWRP. The
   installer never writes them either. Skipped when a backup with `p20-boot.img` and `p21-system.img`
   is already there, so the first backup (Fire OS's) is the one that stays.
3. **Images.** The store image must be ext4 labeled `techo5-store` and fit `system`. The boot image
   must be TECHO5's, as released: its header checks out and its tuning slot is empty.
4. **Speaker tuning.** See [The speaker tuning](#the-speaker-tuning). Stops if the tuning isn't one it
   knows.
5. **Write.** Asked once (type `ERASE`). Up to here nothing on the unit has been written. Then
   `system` and `boot` are unmounted if TWRP had them mounted, and written: first `system`, then
   `boot`. Each image goes to TWRP's RAM disk, is checked by sha256, written (the node is checked to
   be the partition with the right name right before every write) and read back from the flash. A
   read back that doesn't match is written again once; a second mismatch stops with the unit still
   in TWRP. `system` goes first because TECHO5's store also starts under Amazon's kernel, so a unit
   stopped between the two still runs.
6. **Settings** in `userdata/techo5`: the name (a unit that has one keeps it), the Home Assistant key
   (a unit that has one keeps it, so Home Assistant keeps the device; a copy goes to
   `backups/<serial>/home-assistant.key`), an SSH key (`--ssh-key`, which also turns SSH on) and a
   Wi-Fi network (`--wifi`; only the WPA key reaches the unit, never the passphrase). The network is
   added to the ones the unit has saved; a saved one with the same name is replaced.
7. **First boot.** TWRP restarts the unit. The first boot grows the store to fill the partition (see
   below), then starts slot `a`. The installer watches the USB serial console for the daemon (up to
   five minutes; `--no-watch` skips this).

`--dry-run` does all the checks, reads the speaker tuning and fills the boot image on the computer,
and writes nothing to the unit.

### Wi-Fi

Without `--wifi`, the unit takes the network Fire OS saved. If there is none, pick one on the Show's
own screen.

With `--wifi`, the network is added to any the unit already has from an earlier install; the others
stay. On a first install that also means Fire OS's saved networks aren't taken: the unit uses the
one you gave.

### Add it to Home Assistant

Settings → Devices & services: the unit appears under Discovered as an ESPHome device with the name
you gave it. Add it and paste the key the installer printed (it's also in
`backups/<serial>/home-assistant.key`). If it doesn't appear, add the ESPHome integration by hand
with the unit's address and port 6053. The rest of the setup is as on the other Shows
([install.md](install.md), "Add it to Home Assistant").

## Which screen

Amazon built this model with several panels, on two controllers (ILI9881C and JD9365). The boot
loader tells the kernel which one the unit has: by name in its framebuffer entry in the device tree,
and with `jd936x_id=` on the command line, whose low digit is the panel. TECHO5's kernel picks the
panel's settings from that at boot, so one kernel covers every unit.

Two have been tested on units: the **ILI9881C with BOE glass** (`jd936x_id=0x36`, the boot
loader's name `ili9881c_wxga_dsi_vdo_st_boe`) and the **JD9365 with KD/HSD glass** (`jd936x_id=0x31`,
`jd936x_wxga_dsi_vdo_athena_st_kd_hsd`, reported in #187). All of them:

| `jd936x_id` low digit | Boot loader's name | Panel |
|---|---|---|
| 1 | `jd936x_wxga_dsi_vdo_athena_st_kd_hsd` | JD9365, KD/HSD glass (tested) |
| 2 | `jd936x_wxga_dsi_vdo_athena_st_kd_hsd_new` | JD9365, KD/HSD glass (new) |
| 3 | `jd936x_wxga_dsi_vdo_athena_st_kd_hsd_proto` or `ili9881c_wxga_dsi_vdo_st_kd_hsd` | JD9365 or ILI9881C, KD/HSD glass |
| 4 | `jd936x_wxga_dsi_vdo_athena_st_boe` | JD9365, BOE glass |
| 5 | `jd936x_wxga_dsi_vdo_athena_st_boe_new` | JD9365, BOE glass (new) |
| 6 | `ili9881c_wxga_dsi_vdo_st_boe` | ILI9881C, BOE glass (tested) |
| 7 | `jd936x_wxga_dsi_vdo_athena_st_inx` | JD9365, INX glass |
| 8 | `ili9881c_wxga_dsi_vdo_st_inx` | ILI9881C, INX glass |
| 9 | `ili9881c_wxga_dsi_vdo_st_kd_hsd_onyx` | ILI9881C, KD/HSD glass (onyx) |
| a | `ili9881c_wxga_dsi_vdo_st_kd_inx_o` | ILI9881C, KD/INX glass |

The installer reads both in TWRP (`/proc/cmdline` and the device tree) and goes on by itself only on
a tested panel. On one of the others it stops and says which panel it found: add `--force-panel`
to try it, with `--wifi` and `--ssh-key` too, so you can still reach the unit if its screen stays
dark (without a screen there's no other way to set up Wi-Fi). On a panel it doesn't know, or when it can't tell, it stops too. To check by hand:

```
adb shell cat /proc/cmdline
```

Look for `jd936x_id=`. Running TECHO5's kernel, `dmesg | grep "athena panel"` shows which panel it
picked.

If you try a panel that isn't tested and the screen stays dark, TECHO5 should still start: Home Assistant
should find it as before. Please tell us on GitHub which panel it was. To go back to Amazon's kernel,
which drives every screen: hold Volume Up while the unit powers on to start TWRP (TWRP shows on the
screen with its own kernel), write `p20-boot.img` back from your backup (see
[The way back to Fire OS](#the-way-back-to-fire-os), the `p20` line only), then install with
`--kernel amazon`.

## The speaker tuning

The speaker amp (a TI TAS5805M) needs a tuning: a register sequence with Amazon's EQ and speaker
protection. Amazon doesn't ship it as a file; it's compiled into the Fire OS kernel, which carries
two (table A and table B) and picks one per unit with `speaker-config` in its device tree. TECHO5
doesn't redistribute it. The installer gets it from your unit instead:

1. It takes Fire OS's `boot` from the backup (`p20-boot.img`), or from `--stock-boot <file>`.
2. It unpacks Amazon's kernel and reads `speaker-config` from the device tree that matches the unit.
3. It finds both tables in the kernel by their structure, converts the one the unit uses to the
   format the mainline driver loads (register/value byte pairs, the delays left out), and checks its
   sha256 against the tunings it knows. An unknown tuning stops the install.
4. It writes the tuning, its length and its sha256 into the slot TECHO5's boot image reserves for
   it, and recomputes the boot image's header id.

The tuning stays on your computer (in the backup) and on the unit.

If `boot` already holds TECHO5's kernel and the backup's `p20-boot.img` is TECHO5's too (for
example, a backup made after an earlier install), the installer stops: pass Fire OS's boot with
`--stock-boot <file>`.

### A Fire OS version the installer doesn't know

The known tunings are those of Fire OS 7080, the version the unlock needs. If the installer says
the tuning isn't known:

```
python3 tools/install-athena.py --show-amp-tables backups/<serial>/partitions/p20-boot.img
```

prints every device tree's `speaker-config`, each table it finds and the sha256 of each converted
table. To add them, put the hashes in `KNOWN_AMP_TABLES` in `tools/install-athena.py`, with the table
letter. Only do that after checking the tables: the speaker-config 0 table must be the one Amazon's
driver uses for 0 (table A), the other for 1. Please open an issue with the Fire OS version and the
hashes so the next release knows them.

## Stereo speakers

The Show 8 2nd gen has two speakers. TECHO5 plays them in mono by default: left and right are mixed
and both speakers play the mix. To hear left and right on their own speakers, turn on **Stereo
speakers** in the **Configuration** card of the device's page in Home Assistant, or on the setup
page under **Speaker and voice**. It changes at once, no restart.

In stereo each speaker gets its own EQ and limiter, and the speaker EQ, bass and treble apply to
both. Amazon wired the speakers the other way round from the usual left/right order, so TECHO5
swaps them back. A Bluetooth speaker always gets stereo, with or without this switch.

**Stereo can make the wake word harder to catch while music plays.** The echo canceller works from
one reference, the mix of the two speakers, so a sound panned hard to one side is canceled less
well than in mono. If the wake word gets missed over music, turn stereo back off.

## The store fills the partition

The image is only as big as its contents (about 200 MB), so it writes in seconds. The `system`
partition is 3 GB, and a second slot needs that room. Since v1.2.24 the installer grows the store to
fill it right after writing it, from TWRP, with nothing mounted. As a second chance, at every boot,
before anything else writes to the store, `/init` runs `slotctl grow` on the `system` partition:

- It does nothing once the store fills the partition, so in practice it runs once.
- It grows the filesystem online with `resize2fs`, with the root remounted read-write for the moment
  it takes, then read-only again.
- It refuses a filesystem that was not left clean, one with no room reserved for growing
  (`resize_inode`), and a partition that would need more group descriptor blocks than were reserved,
  so the layout never changes. No feature is added; the features are compared before and after.
- A failure leaves the store as it was, and the boot goes on.
- The result is in `/data/techo5-linux/grow.log`. By hand, from the rescue shell:
  `STORE=/ slotctl grow /dev/mmcblk0p21` (`mmcblk1p21` under Amazon's kernel).

What it can't protect against: a power cut during the second or two the grow takes, on a filesystem
without a journal. If that ever happens, run `e2fsck -fy /dev/block/mmcblk1p21` from TWRP, or
reinstall.

The eMMC is `mmcblk0` under TECHO5's kernel and `mmcblk1` under Amazon's and TWRP's. The partition
numbers are the same.

### The store did not grow

A unit installed before v1.2.24 on **Amazon's kernel** (`--kernel amazon`) may never have grown: that
kernel marks the store as having errors each time it mounts it, so the online grow is refused at every
boot, and `e2fsck` from TWRP doesn't help for long ([#192](https://github.com/HuskerMinion/techo5/issues/192)).
The store stays at about 200 MB, and an update fails with "the store has no room for the new slot"
(`tar: write error: No space left on device` in the log). `STORE=/store slotctl status` shows the size,
and `/data/techo5-linux/grow.log` says why it wasn't grown.

Grow it once from TWRP, with nothing mounted (thanks to the reporter of #192 for working this out):

```
adb shell e2fsck -fy /dev/block/mmcblk1p21
adb shell resize2fs /dev/block/mmcblk1p21
adb shell e2fsck -fn /dev/block/mmcblk1p21
adb reboot
```

The last `e2fsck` should report the filesystem clean. Afterwards the store is about 3 GB and updates
install again. Units installed with v1.2.24's installer or later are grown this way by the installer,
and the store image it writes no longer gives Amazon's kernel the reason to flag it.

## Updates

Updates work as on the other Shows: a new root filesystem goes into the slot that isn't running, on
trial for three boots, and commits itself once the daemon has run for five minutes. A slot that never
gets there is marked bad and the unit goes back to the other one; with no good slot it stays up in
the rescue environment (USB serial and RNDIS at 172.16.42.1).

- **From Home Assistant**: the daemon's update card. A release offers an athena only the rootfs keyed
  `arm-athena` in its signed manifest (published as `techo5-rootfs-athena-<version>.tar.gz`), never
  the other Shows' `arm` one, and the other Shows never take athena's.
- **By hand over SSH**: copy a rootfs to the unit and install it into the other slot:

  ```
  scp techo5-rootfs-athena-<version>.tar.gz root@<address>:/data/techo5-linux/
  ssh root@<address> /usr/local/sbin/slotctl install /data/techo5-linux/techo5-rootfs-athena-<version>.tar.gz
  ssh root@<address> reboot
  ```

  The athena store accepts only a rootfs made for athena (its `device.conf` says `T5_BOARD=athena`);
  anything else is refused before the other slot is emptied.
- `slotctl status`, `slotctl rollback` (mark the running slot bad, boot the other next) and
  `slotctl switch a|b` work as on the other Shows.

### Kernel updates

The athena rootfs carries the kernel it was tested with. Once a new slot has committed, it checks
the kernel on `boot`. If that's a different one, it writes the new kernel the way the installer
does, with the unit's own speaker tuning taken from `boot`, and reboots into it. So an update can
take two reboots. The second one comes about five minutes after the first, once the unit isn't
busy: it waits while an alarm or timer is ringing, while it's hearing or answering a voice request,
during a call, while music or an announcement is playing, while the wake light is on, and when an
alarm is due within 30 minutes. After two hours of waiting it only waits for a ring to stop, or for
an alarm or timer due within the minute, so a unit that is never idle still gets its kernel.

A new kernel is on trial too. It has three boots to settle: the daemon up for five minutes, with a
network address and a default route, and the sound card and backlight there. A boot that doesn't
settle within 15 minutes reboots to try again, with the same wait for the unit not to be busy. If
it never settles, the unit writes the previous kernel back (it keeps a copy in
`/data/techo5-linux/kernel`) and never tries that kernel again. A system update waits while a
kernel is on trial.

One case is treated differently: every try had everything but a network (no default route), say
because the router was down. The previous kernel still goes back, since a kernel that broke Wi-Fi
would otherwise leave the unit unreachable. But the new kernel isn't marked bad, and the unit tries
it again a day later.

- `/usr/local/bin/techo5 tools kernel status` shows where it is.
- `/usr/local/bin/techo5 tools kernel restore` puts the previous kernel back by hand and reboots,
  for a kernel that settled but doesn't work right. Run it from the USB shell.

What this can't catch: anything that stops the unit before TECHO5's boot script runs. That's a
kernel that hangs or panics early, or one that can't find the eMMC. A power cut during the second
the kernel is being written is the same. The way back from those is the way the unit was unlocked
(mtkclient). Kernels are tested on units before they are released for this reason.

Running the installer again from TWRP still works too. It keeps the name, the Home Assistant key
and the first backup.

## The USB cable is a root shell

As on the other Shows, the USB cable gives a root shell: the USB serial console has no password,
in normal running and in rescue. Anyone who can plug into the unit can get root on it.

## The way back to Fire OS

`vendor`, `recovery` and the unlock are as they were. Only `boot` and `system` changed, and their
backups are `backups/<serial>/partitions/p20-boot.img` and `p21-system.img`.

From TWRP, stream each backup straight to its partition from a Git Bash, Linux or macOS shell
(PowerShell has no `<`; run each line through `cmd /c "..."`):

```
adb exec-in "dd of=/dev/block/mmcblk1p20 bs=1048576" < p20-boot.img
adb exec-in "dd of=/dev/block/mmcblk1p21 bs=1048576" < p21-system.img
```

Restore both. Fire OS's `system` doesn't start on TECHO5's kernel. **Never write `tee1`, `lk` or
the preloader**, even from a full backup.

Fire OS replaces TWRP with Amazon's recovery the first time it starts. If you want TWRP back
afterward, write `p7-recovery.img` from the same backup to `recovery` with mtkclient
(`mtk w recovery p7-recovery.img`, the unit in its download mode).

Back on Fire OS, keep the unit away from Fire OS updates if you want to come back to TECHO5 later
(for example, don't set it up on Wi-Fi, or keep it off the network). An update might remove the
unlock or TWRP. That's a guess, not something tested, but there's no reason to find out.

If the installer says the backup is not a Fire OS backup, it was made with TECHO5 already on the
unit. Its `p20-boot.img` and `p21-system.img` are TECHO5's: use the backup made before the first
install instead.

## Amazon's kernel instead

`--kernel amazon` keeps Amazon's 32-bit kernel on `boot` and writes only `system`. Use it on a unit
whose screen TECHO5's kernel doesn't drive yet, or to compare. Amazon's kernel drives every screen. TECHO5 then
uses Amazon's drivers from the `vendor` partition (mounted read-only).

```
python3 tools/install-athena.py --kernel amazon --name Kitchen
```

The same store image works under both kernels. If `boot` holds TECHO5's kernel, the installer stops:
write `p20-boot.img` back first (see the way back above). To go back to Fire OS from this path,
restore `p21-system.img` only.

## Credits

The unlock, saiyaku-athena and kaeru, is by R0rt1z2 with bengris32 and FieryFlames (jackmthws),
with thanks to nuno407 for the META mode idea:
[XDA thread](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-show-8-2nd-gen-2021-athena.4804958/).
The Linux port is by HuskerMinion.
