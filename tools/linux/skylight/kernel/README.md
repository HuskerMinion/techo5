# Mainline kernel for the Skylight Calendar 15

Linux 6.18.56 from kernel.org with the board files here, built by `build.sh` into `boot-skylight.img`:
an Android boot image (header v2, the stock load addresses) whose second stage is a Rockchip resource
image holding the device tree (as `rk-kernel.dtb`, `d156-mipi.dtb` and `d156-edp.dtb`, so the stock
U-Boot cannot pick one of its own) and the boot logo. `uname -r` says `6.18.56-skylight`.

`INSTALLER=1 build.sh` is a release build. It makes, in `$WORK/out`:

- `boot-skylight.img`: TECHO5's kernel, which goes into `recovery`. Its ramdisk is empty: the root is
  `techo5-store`.
- `boot-skylight-installer.img`: the same kernel with the installer's initramfs, for one boot.
- `linux-6.18.56-techo5-skylight-source.tar.xz`: the source of both, for the GPL. It is the kernel.org
  tree with the board files, the panel driver's Kconfig and Makefile lines and the boot logo in it, and
  the build's configuration as `techo5-skylight.config`. Entries are sorted, owned by 0:0 and at the
  build's time, so the same tree makes the same tarball.
- `SHA256SUMS`, the kernel's parts and `kernel.config`.

`check-boot.py` checks a boot image (`--installer` the installer image: exactly its files, a matching
data image, no key or firmware; `--kernel-of` that it carries the boot image's kernel) and
`check-source.py` a source tarball against the image it built: the board files the same as here, the
configuration the one the kernel carries (`CONFIG_IKCONFIG`). `build.sh` runs both, and also refuses a
source tarball that names anything of the computer it was built on.

The bench and installer images carry `busybox` (Alpine's armv7 `busybox-static`, unmodified, from
`inputs/` as `tools/fetch-inputs.py` fetches it; the kernel runs 32-bit programs) with a `SOURCES` file
saying where its source is, and `reboot-normal`, which is built without a C library.

- `rk3566-skylight-cal150.dts`: the board. Its values are the unit's stock device tree's.
- `panel-skylight-cal150.c`: the display, a DSI-to-LVDS bridge set up with the stock panel's 38
  generic writes; the chip is not identified.
- `skylight.config`: merged over arm64 `defconfig`; `build.sh` checks that every line stuck.
  `CONFIG_MODULES` stays on, as `defconfig` has it, but no module is built or shipped, so what the
  board and TECHO5 need is built in (netfilter for the Wi-Fi firewall, IPv6, Bluetooth, the sound card,
  /dev/i2c-N for the light sensor).
- `pack-sky.py`: the boot image. Its header ID is checked against the stock image's algorithm.
- `check-boot.py`, `check-source.py`: the checks above.
- `mklogo.py`: the kernel's boot logo and U-Boot's `logo.bmp`, from `logo/TECHO5_logo.png`.
- `reboot-normal.c`: restarts with the boot mode register saying normal (the bench and installer
  images use it, so it ships in the release's installer image).
- `bench/`: the bench initramfs (a root shell on USB serial, `BENCH=1`).
- `installer/`: the installer image's `/init` (`INSTALLER=1`): it runs `../install/install.sh` on the
  set `tools/install-skylight.py` stages on `/cache`, then restarts into TECHO5, or into Android when
  it stopped before writing anything.

Things learned on the unit:

- The panel's GPIOs must be requested as outputs; left as inputs, setting them drives nothing.
- `dclk_vop1` cannot set VPLL's rate itself, so VPLL is given 912 MHz (152 MHz times 6).
- The speaker amplifier hangs off the codec's headphone outputs.
- `mmc-pwrseq-simple` needs `CONFIG_RESET_GPIO`; built-in cfg80211 needs `CONFIG_RFKILL=y`;
  built-in hci_uart needs `CONFIG_POWER_SEQUENCING=y`.
- brcmfmac asks for its firmware at about 1.8 s, after the root is mounted: the firmware lives in the
  store's `/lib/firmware`, where a ramdisk copy would already be hidden.
- fbcon refuses a logo as tall as the screen, so the logo is 1064 rows, centered.
- The stock U-Boot draws its own `logo.bmp` only through vendor display nodes this tree does not
  carry, so the first seconds stay dark.
