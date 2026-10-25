# TECHO5 on the Skylight Calendar 15 (150-CAL)

The Skylight Calendar 15 is a 1920x1080 touchscreen calendar built on a Rockchip RK3566 (Skyworth
board D156, FCC ID 2AABK-150). TECHO5 runs on it with its own 64-bit mainline kernel and the Show's
32-bit userland, as on the 2nd gen Show 8. It has no microphones, so it is a screen, a speaker and
Bluetooth: no wake word and no voice assistant.

Status: in the releases, tried on one unit. Install it with `tools/install-skylight.py`:
[docs/install-skylight.md](../../../docs/install-skylight.md) has the steps, the risks and the way back.

## What works

| Part | How |
| --- | --- |
| Display | 4-lane MIPI DSI into a DSI-to-LVDS bridge set up over DSI generic writes (`panel-skylight-cal150.c`), VOP2 video port 1, 1920x1080 at 152 MHz (VPLL at 912 MHz) |
| Backlight | pwm4, levels 20..255, enable GPIO0_C6 |
| Touch | Goodix GT911 on I2C5 (m1 pins); it reports 1920x1200, the daemon scales by the driver's ranges |
| Audio | RK809 codec on I2S1; the speaker amplifier (enable GPIO4_C4) is fed from the codec's headphone outputs, not SPKO |
| Wi-Fi | AP6256 (BCM43456) on SDIO, brcmfmac |
| Bluetooth | AP6256 (BCM4345C5) on UART1, hci_uart Broadcom over serdev |
| Clock | HYM8563 on I2C5 (battery backed) and the RK809's |
| CPU | cpufreq 408 MHz to 1.8 GHz, the SYR827 regulator |
| Boot logo | the kernel's fbcon logo, TECHO5's mark where the daemon's splash puts it |
| Light sensor | EM3071X on I2C1, read by echod through /dev/i2c-1 (no kernel driver) |

The light sensor (EM3071X, at 0x24 on I2C1) has no mainline driver: echod reads it through
/dev/i2c-1 for the room's brightness (`echod/internal/hardware/ambient/em3071x.go`). The MAC is not the Wi-Fi chip's (the stock NVRAM
gives every unit Broadcom's placeholder): it is made from the eMMC's CID (`device.conf`, and
`echod/internal/layout/skylight.go` for the factory MAC).

## How it boots

The stock U-Boot stays. Android's userdata partition is split into two GPT partitions of TECHO5's
own, `techo5-store` (2 GiB: the slot store, as on the other Shows) and `techo5-data` (the rest:
TECHO5's state, bound to `/data`). TECHO5's boot image goes in the `recovery` partition, and a
`boot-recovery` command at the start of `misc` makes the stock U-Boot start recovery on every boot.

The kernel mounts `techo5-store` read-only as `/` and runs `init` (this directory) at its root, which
picks a slot with `slotctl` and pivots into it, or stays as the rescue environment. `boot`, `super`
and the rest of Android are left as they were.

## Updates

A Skylight takes the releases' `arm-skylight` root filesystem (`techo5-rootfs-skylight-<version>.tar.gz`)
into its other slot, on trial, as the Shows take theirs; it never takes another board's, nor they its.
The root filesystem carries the kernel it was tested with (`deploy-rootfs.sh` with `KERNEL_IMAGE`), and
once the new slot is good that kernel goes into `recovery` on trial too (`KERNEL_UPDATE` in
`device.conf`, `echod/internal/update/kernel`): three boots to settle, the previous kernel kept on
`techo5-data` and put back if it never does. It is written only over TECHO5's own Skylight kernel. A
kernel that never gets as far as the boot script cannot be caught that way; its way back is Loader
mode (docs/install-skylight.md).

## Files

- `kernel/`: the kernel's board files and `build.sh` (kernel.org linux-6.18.56, pinned by sha256),
  which makes `boot-skylight.img` and the kernel's source tarball, for the GPL; `INSTALLER=1` also
  makes the installer image (a release build), and `BENCH=1` a bench image whose initramfs is a root
  shell on USB serial. `check-boot.py` and `check-source.py` check them; `tools/release.ps1` runs both
  again before it publishes.
- `init`, `mkstore.sh`, `rescue-profile.sh`, `overlay/`: the store and the board's `device.conf`.
- `install/`: `mkgpt.py` (the new GPT, made from the unit's own table and size) and `install.sh`
  (the writes, each read back), which `tools/install-skylight.py` stages and the installer image runs.
- `kernel/installer/`: the installer image's `/init` (`INSTALLER=1` in `kernel/build.sh`).

## The unit's own firmware

The Wi-Fi and Bluetooth firmware are the vendor's, from the unit's stock vendor partition
(`/vendor/etc/firmware`: `fw_bcm43456c5_ag.bin`, `nvram_ap6256.txt`, `BCM4345C5.hcd`). They are
never published. `tools/install-skylight.py` copies them from the unit into its backup folder and,
during the install, back into that unit's store (`install.sh`), so the store image stays the same for
everyone (`mkstore.sh -k none`, no `-F`). For bench work, `mkstore.sh -F <dir>` puts a unit's own
files into its store image, which is then for that unit only.

## How the installer works

`tools/install-skylight.py` does it in one command, over adb from stock Android, on the firmware
and hardware it knows (D156.V1.3; see [docs/install-skylight.md](../../../docs/install-skylight.md)):

1. Checks: the firmware's fingerprint, `adb root`, the board, the eMMC's size, the stock partition
   table (both copies), the vendor firmware, room on `/cache`.
2. A full copy of the eMMC (`dd` of the whole block device over `adb exec-out`), checked against the
   unit's own sha256 of every partition Android does not have mounted read-write. Nothing is written
   without it.
3. The vendor firmware into the backup folder (`adb pull`), never into a release or the repository.
4. The new GPT from the unit's own (`install/mkgpt.py`): every LBA from its table and size.
5. After the confirm: the set on `/cache/techo5-install` (`install.conf` with the unit's geometry, the
   GPT halves, `store.img.gz`, the boot image, the firmware, the settings, `SHA256SUMS`), the
   installer image into `recovery`, `adb reboot recovery`. Android's `reboot recovery` writes
   `boot-recovery` into `misc`; the installer's `/init` clears it first, so that anything that stops
   before the end restarts into Android.
6. The installer image's `/init` copies the set into RAM, unmounts `/cache` and runs `install.sh`,
   which writes and reads back each part: the GPT, `techo5-store`, `techo5-data` (an empty 64 MiB
   ext4 from the installer image), the firmware into the store's `/lib/firmware/brcm`, the settings
   into `techo5-data`, the boot image into `recovery` and, last, `boot-recovery` into `misc`. Then it
   restarts into TECHO5.
7. TECHO5's `/init` grows `techo5-data` to fill its partition (`resize2fs`, online) on every boot;
   once it does, there is nothing to do.

For bench work, `kernel/build.sh` with `BENCH=1` makes the bench image (a root shell on USB serial
that goes back to Android by itself); `install.sh SETDIR` can be run from it by hand on a set made
the same way.

## Back to Android

The backup, written back in Loader mode with `rkdeveloptool` (the installer writes the steps, with
the unit's own numbers, into the backup's `restore/RESTORE.txt`). Writing the first 2048 bytes of
`misc` back to zeros is meant to start stock Android again with a fresh setup, but has not been
tried: in TECHO5's table there is no `userdata` partition for Android to find.
