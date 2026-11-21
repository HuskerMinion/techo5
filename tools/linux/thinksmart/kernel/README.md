# Kernel for the Lenovo ThinkSmart View (CD-18781Y)

The kernel TECHO5 boots on the ThinkSmart View (Qualcomm APQ8053): msm8953-mainline `v7.1.3-r0`, the
tree postmarketOS packages for this SoC, with the drivers and patches in this directory and the
configuration in `thinksmart_defconfig`, every driver built in. `build.sh` builds it. `uname -r` says
`7.1.3-techo5-thinksmart`.

## Upstream base

- `https://github.com/msm8953-mainline/linux`, tag `v7.1.3-r0`
- commit `7213855948c70fd165356526f1de1c3b6bf4d554`

The tree is cloned shallow at the tag and checked against the commit. GitHub's tarball of the tag
is large and its download does not resume.

## Building

On Linux or in WSL, with aarch64 and armhf cross GCCs (`gcc-aarch64-linux-gnu`,
`gcc-arm-linux-gnueabihf`; Ubuntu's GCC 15.2 tested) and the usual kernel build tools
(`build-essential bc bison flex libssl-dev libelf-dev python3 git xz-utils`):

    tools/linux/thinksmart/kernel/build.sh

Output in `$WORK/out` (default `~/techo5-thinksmart-kbuild/out`):

| File | |
| --- | --- |
| `boot-thinksmart.img` | the kernel as a root filesystem carries it (`deploy-rootfs.sh KERNEL_IMAGE`): an Android boot image, header version 0, Image.gz with the device tree after it, an empty ramdisk and the command line (`pack.py`) |
| `vmlinuz` | Image.gz, for an image's boot partition (`mkimage.sh -K`) |
| `apq8053-lenovo-cd-18781y.dtb` | the board's device tree, with the front camera |
| `kernel.config` | the full configuration used |
| `linux-7.1.3-techo5-thinksmart-source.tar.xz` | the whole prepared source tree, with the configuration in it as `techo5-thinksmart.config`, for the GPL |
| `SHA256SUMS` | |

Environment: `WORK`, `OUT`, `JOBS`, `CROSS_COMPILE`, `CROSS_COMPILE_COMPAT` and `KTREE` (a checkout
of the tag to prepare in place, for quick rebuilds; every step is idempotent). The build sets
`KBUILD_BUILD_TIMESTAMP`, `KBUILD_BUILD_USER`, `KBUILD_BUILD_HOST` and `KBUILD_BUILD_VERSION` to fixed
values and maps its own directory out of the debug info. Bump the timestamp in `build.sh` whenever
the kernel changes.

## How it boots

lk2nd reads `extlinux/extlinux.conf`, `vmlinuz` and the device tree from the first partition of the
MBR inside the `system` partition (`mkimage.sh -K`), with the command line `build.sh` puts in the
boot image's header (less its last word, `techo5.kernel=trial`). There is no initramfs. The command line has the
kernel's device mapper make that MBR's two partitions at boot (`dm-mod.create`, by the GPT name
`system`, so one image fits every unit) as `system1` and `system2`, the names `init` looks for. The kernel then mounts `system2`, the store, read-only as `/` and runs its
`/sbin/init` (`../init`). For that the eMMC, the GPT, ext4, `DM_INIT` and devtmpfs mounted on `/dev`
are built in, and so is everything else: there are no modules, so the kernel is the one file an
update moves (`../README.md`, "Updates").

Built-in drivers probe before `/` is mounted, when no firmware can be read. The audio DSP's and the
video decoder's are the unit's own, in the modem partition, and Wi-Fi's is the store's. `init` links
the modem partition's where the kernel looks, then starts the remote processors that stayed offline,
probes again the devices left without a driver, and binds Wi-Fi again. Bluetooth and the GPU ask for
theirs later, when they are first used.

`pack.py` writes the boot image as mkbootimg would, byte for byte (header version 0, base
0x80000000, kernel at +0x8000, ramdisk at +0x01000000, tags at +0x100, page size 2048), and checks one.

## The configuration

`thinksmart_defconfig` started from postmarketOS's `linux-postmarketos-qcom-msm8953` configuration for
7.1.3, with these changes:

- `DM_INIT` on, so there is no initramfs.
- `MODULES` off: every driver below is built in.
- `SND_SOC_TAS5782M` (the speaker amplifier) and `VIDEO_S5KC505A` (the front camera) added.
- Drivers for hardware other msm8953 devices have and this one does not are left out: other panels,
  touch controllers, cameras, sensors, NFC, the modem and WCNSS Wi-Fi, other audio codecs. That was
  done with `localmodconfig` over the modules a running unit loads, then putting back everything
  generic it dropped: networking and netfilter, file systems, crypto, HID, USB classes and gadget
  functions, Bluetooth, the device mapper. That was 465 modules where the package has 670, all built
  in, then trimmed: file systems, network and USB drivers, Wi-Fi chips and HID drivers no ThinkSmart View
  uses, tracing and KVM left out. The kernel is 11 MB compressed and takes 28 MiB in memory, under
  the 32 MiB lk2nd starts from extlinux (`pack.py` checks it).
- `xt_owner` and `xt_recent`, which techo5-firewall needs and the package leaves out.

## Drivers and patches

| File | From | |
| --- | --- | --- |
| `src/tas5782m*` | rickx ([rickx/ubuntu-thinksmart](https://github.com/rickx/ubuntu-thinksmart)), as changed in m0s4ik/thinksmart-view-debian 02821bc (`driver/tas5782m`), with its debug build layer (`tas5782m_dbg.h`, never compiled in) taken out here, and changed here to look for its tuning file (the speaker's DSP table) at each stream start and play without it, flat and 4.5 dB lower, until it is there; GPL-2.0, `src/LICENSE.tas5782m` | the TAS5782M speaker amplifier; without it there is no sound card |
| `src/s5kc505a.c` | Jetha Chan (jethac/lenovo-thinksmart-view), with flip controls, from m0s4ik/thinksmart-view-debian 02821bc (`driver/s5kc505a`); GPL-2.0 | the front camera's sensor |
| `patches/0001`-`0003` | Vladyslav Tymoshchuk, from m0s4ik/thinksmart-view-debian 02821bc (`driver/q6asm`) | q6asm-dai positions: without them playback stalls when a stream is stopped and started again, which the daemon does all the time |
| `patches/0004` | m0s4ik/thinksmart-view-debian 02821bc (`driver/venus`); GPL-2.0-only per that project's README | the video decoder's capability parser |
| `patches/0005` | written for TECHO5 from the stock Lenovo device tree's camera node; GPL-2.0-only OR BSD-3-Clause (the file it changes is BSD-3-Clause) | the front camera, its rails and CSI link in the board's device tree |
| `patches/0006` | written for TECHO5; GPL-2.0-only, as the file it changes | the video decoder's interrupt on only while its core is powered: a late one after the core powered down made its handler read unclocked registers and panic the kernel, now and then, at boot |
