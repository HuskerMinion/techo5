# Linux image for cronos

The [Linux 7.2.6 option](../../custom-kernel/7.2.6/README.md) uses these slot
and service scripts with Alpine 3.24.2 and matching kernel modules. The build
commands below describe the vendor-kernel image.

The Echo Show 5 boots Linux with no Android userspace: the LineageOS 4.9.337
kernel with a small Alpine initramfs in the `boot` partition, and a persistent
Alpine (armv7) root filesystem on the eMMC `system` partition. TWRP stays in
`recovery`; the LineageOS `boot` image plus its zip restore Android if ever
needed.

State on 2026-09-15: boots to the daemon from a rootfs slot, Wi-Fi with the
credentials Android had saved, dropbear, NTP; the initramfs doubles as the
rescue environment; rootfs updates go through two slots with a boot-count
trial that mirrors the daemon's own update trial.

## Layout

```
boot      (p9, 16 MB)   kernel + initramfs (tools/linux/init): picks a slot, or rescue
system    (p12, 3 GB)   the "store", ext4, label techo5-store — mounted at /store
  .techo5-store          marker (a LineageOS system partition has none)
  active                 slot to boot next: a | b
  slots/a, slots/b       two complete root filesystems (plain directories)
  slots/<x>.state        good | trial <tries left> | bad
  rescue                 present → the next boot stays in the initramfs (one boot)
userdata  (p16, 3.9 GB) /data — daemon state (/data/misc/techo5), logs, Wi-Fi
                         config and dropbear host keys (/data/techo5-linux)
recovery  (p10)         TWRP, untouched
```

The booted slot is a bind mount of `/store/slots/<x>`; the store, and with it
the root, is mounted read-only. Everything that writes to it (`slotctl`, the
daemon's in-place updater) remounts it writable and back. `/etc/resolv.conf`
and `/etc/dropbear` point at `/run` and `/data`.

Inside the rootfs: busybox init (`etc/inittab`) runs `etc/techo5/boot.sh`
once (devices, USB serial, Wi-Fi, clock, SSH, the slot-trial watcher, a
network keeper that reboots after 15 minutes without an address), then keeps
`techo5-run` (the daemon) and `techo5-console` (root shell on the USB COM
port) alive. The daemon is `/usr/local/bin/techo5`; the LineageOS `vendor`
tree (Wi-Fi/BT modules, firmware, audio tuning) is copied in as `/vendor`.

## Updates and rollback

`slotctl` (in both the initramfs and the rootfs) owns the slots:

```
slotctl status                      # slots, states, active and booted
slotctl install rootfs.tar.gz       # → inactive slot, "trial 3", made active
slotctl commit                      # booted slot → good (boot.sh does this itself)
slotctl rollback                    # booted slot → bad, boot the other (good) one
slotctl switch a|b                  # boot this one next (a bad slot goes back on trial)
slotctl rescue [off]                # stay in the initramfs at the next boot
```

A fresh slot has three boot tries. The initramfs takes one at every boot;
`boot.sh` commits the slot once the daemon has been running for five minutes
without a restart — the same evidence the daemon takes before it commits one
of its own updates. A slot that never commits is marked bad after its third
boot and the other slot boots; with no good slot the initramfs stays up as
the rescue (USB shell, Wi-Fi, SSH, and the daemon from whichever slot has a
binary), so the unit is always reachable. The daemon's own updater keeps
working unchanged inside a slot: it replaces `/usr/local/bin/techo5` in place
(`layout.Dir` follows the executable off Android) and its trial marker lives
in `/run/techo5/prop`, the file-backed stand-in for Android properties.

Kernel/initramfs updates are separate: they are a `fastboot flash boot`
(or `dd` to p9), not a slot.

## Build

Inputs (kept out of the repo, in `inputs/` or `$TECHO5_INPUTS`; [docs/building.md](../../docs/building.md) says where each comes from):

- `boot-lineage-18.1-20260904-cronos.img` — LineageOS boot image (kernel + header)
- `alpine-minirootfs-3.24.2-armv7.tar.gz`, `busybox.static` (from `busybox-static-1.37.0-r31.apk`)
- `apks/`, `apks312/` — the packages in `packages.txt` (initramfs) and the
  wpa_supplicant 2.9 set (both)
- no vendor tree: images don't carry LineageOS's drivers and firmware. Each unit keeps its own in the
  slot store, mounted at `/vendor` (`rootfs/etc/techo5/boot.sh`); `VENDOR_TGZ` puts one into a
  development image only
- `techo5_ed25519` / `.pub` — the SSH key built into the rescue boot image (`build-image.sh`);
  root filesystems carry no key: send one with the `ssh_keys` action and turn on the SSH switch

Boot image (kernel + rescue initramfs). The kernel comes from the LineageOS
boot image with one device-tree edit: `amzn,mic-downmix` removed, so the
capture driver hands over both microphones instead of their average
(`docs/hardware.md`, "Two microphones"):

```
python tools/linux/patch-dtb.py boot-lineage-18.1-20260904-cronos.img \
  boot-lineage-18.1-20260904-cronos-nodownmix.img \
  --delete /soc/spi@1100a000/spi@0 amzn,mic-downmix     # MSYS_NO_PATHCONV=1 in Git Bash
KERNEL_IMAGE=.../boot-lineage-18.1-20260904-cronos-nodownmix.img \
  bash tools/linux/build-image.sh -o techo5-linux-boot.img
fastboot flash boot techo5-linux-boot.img && fastboot continue
```

`patch-dtb.py` needs `pip install fdt`; given a raw `Image.gz-dtb` instead of a
boot image it patches that.

Kernel (since the Bluetooth work): the LineageOS cronos tree rebuilt in WSL at
the exact commit the LineageOS boot image came from, so the vendor Wi-Fi and
Bluetooth modules (`CONFIG_MODVERSIONS`) still load, with `CONFIG_BT`,
`BT_HCIVHCI` and friends added. `build-kernel.sh` documents the checkout, the
toolchain (Arm's GCC 8.3 tarball, no root needed) and the config; then:

```
# in WSL
bash tools/linux/build-kernel.sh -o inputs/Image.gz-dtb-bt
# on Windows
python tools/linux/patch-dtb.py Image.gz-dtb-bt Image.gz-dtb-bt-nodownmix --delete /soc/spi@1100a000/spi@0 amzn,mic-downmix
KERNEL=.../Image.gz-dtb-bt-nodownmix KERNEL_IMAGE=.../boot-lineage-18.1-20260904-cronos-nodownmix.img \
  bash tools/linux/build-image.sh -o techo5-linux-boot-bt.img
```

The image published with releases is built with `--no-key` (no SSH key inside:
the rescue environment then accepts only keys already on userdata, and starts
no SSH server without one, so a new unit is set up from the USB serial console),
from a kernel built in a clean checkout of the LineageOS tree; `build-kernel.sh`
sets `KBUILD_BUILD_USER`/`KBUILD_BUILD_HOST` to `techo5` and builds in UTC, so the
version string names no one.

`KERNEL` replaces the kernel blob; the header, load addresses and command line
still come from `KERNEL_IMAGE`.

Root filesystem: `mkrootfs.sh` installs the packages in `packages-rootfs.txt`
with `apk` into an Alpine base, adds the vendor tree, our binaries and the
overlay (`tools/linux/rootfs/`), and packs a tarball. `deploy-rootfs.sh` builds
the daemon and tools for armv7, stages everything, runs `mkrootfs.sh` and can
install the result into the inactive slot:

```
HOST=<device address> bash tools/linux/deploy-rootfs.sh --version v0.1.5 [--install [--reboot]] [--on-device]
```

It builds **in WSL** when it can: apk needs to run the packages' triggers
inside an armv7 root, which QEMU user emulation provides (`apt install
qemu-user-static binfmt-support`), Alpine's static `apk` sits at
`~/apk/apk.static` (from gitlab.alpinelinux.org, "apk-tools" package
registry), and `unshare -Ur --map-auto` gives the build root-owned files
without sudo. That takes a minute and only the tarball crosses the Wi-Fi.
Without those it builds on the device (`--on-device` forces that), which takes
about ten minutes.

First conversion of a unit (once; erases LineageOS on `system`):

```
# on the device, from the initramfs (rescue) with the daemon stopped
slotctl mkstore /dev/mmcblk0p12 --i-know-this-erases-it
slotctl install /data/techo5-linux/techo5-rootfs-<version>.tar.gz
reboot
```

Git Bash on Windows is the expected shell for the host scripts; they pass
Windows paths to python and normalise line endings on the way to the device.

## Why these choices

- **Boot slot, not recovery.** amonet's LK boots 64-bit kernels only from
  `boot`; from `recovery` even the stock LineageOS image fails silently and the
  watchdog falls back to `boot`. TWRP's 32-bit kernel is what makes `recovery` work.
- **Directories as slots, not partitions.** The kernel has ext4 and loop but
  no overlayfs or squashfs, and repartitioning the eMMC under amonet's LK is
  not something a single fastboot command undoes. Two directories on one ext4
  partition give A/B with `tar` and `mv` and keep everything inspectable from
  the rescue shell; 3 GB holds many 130 MB slots.
- **busybox init, not OpenRC.** Three services and a boot script; the
  daemon's supervisor only has to restart it, which is what the Android init
  did too (`techo5.rc` is not oneshot for the same reason).
- **wpa_supplicant 2.9, not 2.11.** The vendor `mt76x8` driver writes its own RSN
  element (capabilities 0) into the association request; 2.11 advertises 16
  replay counters (0x000c) in the handshake, hostapd on the access points sees the
  mismatch and deauthenticates with "wrong key". 2.9 does not advertise them.
- **Static busybox as `/init`'s interpreter**, breadcrumbs in the spare area of
  MISC (`readmisc.sh`), boot log on userdata: the image explains its own failures.
- **Wi-Fi credentials come from Android's saved networks** on userdata
  (`/data/misc/apexdata/com.android.wifi/WifiConfigStore.xml`), copied once into
  `/data/techo5-linux/wpa_supplicant.conf`; nothing is typed and nothing leaves
  the device.

## Bluetooth

The MT7668's Bluetooth half is driven by the vendor `mt76x8_bt.ko`, which
exposes a raw H4 channel at `/dev/stpbt` rather than a Linux HCI device.
`btbridge` (cmd/btbridge) sets the factory address (MediaTek vendor command
`0xFC1A`, from `/proc/idme/bt_mac_addr`, the one thing Android's HAL did) and
copies packets between `/dev/stpbt` and the kernel's `/dev/vhci`, so BlueZ sees
an ordinary `hci0`. `t5_bt_up` (techo5-lib.sh) loads the module, keeps the
bridge running, starts `dbus-daemon`, `bluetoothd`, and `bluealsa -p
a2dp-source` after it, and power-cycles `hci0` once: on the bench the
controller answered commands but never reported an inquiry result or an
advertisement until it had been powered off and on after the first bring-up.
Bonds live on userdata (`/data/misc/techo5/bluetooth`, bind-mounted over
`/var/lib/bluetooth`).

The daemon (`feature/btaudio`) is the pairing agent and the player: pairing
mode (Home Assistant switch, or a swipe left on the clock) makes the Show
discoverable and scans, lists what it finds on the screen, a tap pairs, trusts
and connects; while an A2DP stream exists the speaker hands its audio to
bluez-alsa's PCM (`hardware/speaker/sink.go`) instead of the codec. Scanning is
only on while pairing: the radio shares the antenna with Wi-Fi. `btmon` and
`btmgmt` are in the image for the console.

## Echo cancellation

Two engines, chosen in Home Assistant (`select.*_microphone_echo_canceller`):
the daemon's own linear filter, and WebRTC's canceller in `techo5-aec`
(tools/aec, C++ against Alpine's `webrtc-audio-processing-1`), which the
daemon feeds 20 ms blocks of microphone and loopback over pipes. The loopback
channels of the FPGA capture stream are sample-aligned with the microphones,
so the reported delay is 0 and the plain filter converges. `build-aec.sh`
compiles the helper for armv7 in WSL inside an Alpine root under QEMU
(`~/alpine-armv7-sdk`, made on first run); `deploy-rootfs.sh` ships it when
it is there and the rootfs carries the runtime library. Without the helper the
select settles on the built-in filter. Measured on the bench: about a third
of a core on worst-case noise, nothing while nothing plays.

## Screen

The daemon paints everything (hardware/screen, feature/display): the clock,
the conversation, a splash at boot. A swipe down from the top edge opens the
settings screen: Display, Sound, Alarms, Connections, Privacy and General down
the left, each one's settings on a card beside them. A swipe left from the
right edge brings in Cameras and Radio. Vertical swipes anywhere else are the
volume; a tap is the action button.

## Camera

The camera is driven from userspace through the ISP driver's register windows; the code is
`echod/internal/hardware/camera` and the map, with the dead ends, is docs/camera-research.md.
(The probe tools that got there, camprobe and camframe, were removed once the package worked;
they are in the history before 2026-09-16 evening.) `hardware/camera` streams on demand with
auto-exposure and `feature/camera` serves
the ESPHome camera entity Home Assistant creates on its own, plus `http://<device>:8181/camera.jpg`
and `/camera.mjpeg` while Camera web access is on. `/screen.png[?sheet=<category>&list=<row>&theme=<name>]` on the
same port, while Screen web access is on, is a screenshot of the panel, for checking layouts from a
PC. With both off the port is closed.

## Updates on the slots

A release may carry a rootfs tarball (`release.ps1 -Rootfs <techo5-rootfs-*.tar.gz>`, built by
`deploy-rootfs.sh` and copied back from `/data/techo5-linux/`). A device that boots from a slot
installs that through `slotctl install` into the other slot and reboots; the boot scripts run the
trial and commit or fall back. The daemon-only binary in the manifest is for the Android install.

## Next

The camera: the kernel at this commit already carries the OV02B10 sensor
driver and the imgsensor fixes (jxlarrea's work, upstreamed into amazon-oss),
and `/dev/kd_camera_hw` and `/dev/camera-isp` exist on the running image;
pictures need the ISP driven from its ioctls — `docs/porting-plan.md` item 9.
Then the Echo Dot on the same image.
