# Building TECHO5 yourself

You don't need any of this to install or update a Show 5. `tools/install-show.py` and Home
Assistant's update card use the signed releases. This page is for changing the daemon or the images.

The Echo Dot and Echo Spot have their own pages:
[techo5-dot docs/building.md](https://github.com/HuskerMinion/techo5-dot/blob/main/docs/building.md) and
[techo5-spot docs/building.md](https://github.com/HuskerMinion/techo5-spot/blob/main/docs/building.md).

## 1. Set up your computer

Everything is Go, Python 3 and bash. Two builds need Linux: the root filesystem (it runs Alpine's
package manager under QEMU) and the kernel.

**Linux** (Ubuntu or Debian; other distributions have the same packages under similar names):

```
sudo apt install git python3 qemu-user-static binfmt-support uidmap \
    build-essential bc bison flex libssl-dev curl xz-utils bzip2
```

and Go 1.26 or later from [go.dev/dl](https://go.dev/dl/) (distribution packages are often older).

**Windows:** install [Git for Windows](https://git-scm.com/download/win) (its Git Bash runs the
`.sh` scripts), [Go](https://go.dev/dl/) and [Python 3](https://www.python.org/downloads/). Then
install WSL with Ubuntu (`wsl --install -d Ubuntu`) and, inside Ubuntu, the Linux packages above. The
root filesystem build runs in WSL on its own when you start it from Git Bash; the kernel build is run
inside Ubuntu. On Windows, type `python` where this page says `python3`.

**macOS:** `xcode-select --install` (git, bash, Python 3), then `brew install go`. The daemon and the
boot image build on macOS. The root filesystem is built on a running Show instead (step 4 does that
over SSH), and the kernel needs a Linux machine or virtual machine.

Get the code (the three repositories side by side, if you build for more than one device):

```
git clone https://github.com/HuskerMinion/techo5
cd techo5
```

## 2. Fetch the inputs

```
python3 tools/fetch-inputs.py --device show
```

That fills `inputs/` (git-ignored) with Alpine's base image, `busybox.static`, `apk.static`, the rescue
environment's packages and the wake word models, each from its public source. The root filesystem
build looks for `apk.static` in your home directory, so on Linux (or inside WSL's Ubuntu) also run:

```
mkdir -p ~/apk && cp inputs/apk.static ~/apk/apk.static
```

(From WSL the repository is under `/mnt/c/...` or wherever you cloned it.)

**From your own Show, for a boot image only:** its LineageOS boot image, as
`inputs/boot-lineage-18.1-20260904-cronos.img`. The installer keeps one in `backups/<serial>/`, or with
LineageOS running and Rooted debugging on: `adb pull /dev/block/mmcblk0p9 inputs/boot-lineage-18.1-20260904-cronos.img`.

This one file is yours and stays yours. Every board needs its own — the header, the load addresses and
the kernel command line in a boot image come from the one it was built against, so a Show 8's image has
to start from a Show 8's. It is Amazon's, it is specific to your unit, and this project neither fetches
it nor publishes it. `fetch-inputs.py` takes only what is public; the donor image is the one input you
supply yourself, on every board.

No vendor tree (LineageOS's Wi-Fi and Bluetooth drivers and firmware) is needed or published: each
Show keeps its own in the slot store.

## 3. The daemon

```
cd echod
go test ./...
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o ../bin/echod-arm ./cmd/echod
cd ..
```

(`go test` runs everywhere; a few hardware packages only build for Linux.) To try a daemon on a Show
already running TECHO5: turn on its SSH switch in Home Assistant with a key sent through the
`ssh_keys` action, copy the binary over, and bind it in place until the next reboot. The devices
have no SFTP server, which `scp` uses by default since OpenSSH 9, so `-O` asks for the old way:

```
DEVICE=192.168.x.y   # the device's IP address
scp -O bin/echod-arm root@$DEVICE:/tmp/echod-test
ssh root@$DEVICE 'mount --bind /tmp/echod-test /usr/local/bin/techo5 && killall techo5'
```

## 4. The root filesystem

[tools/linux/deploy-rootfs.sh](../tools/linux/deploy-rootfs.sh) builds the daemon and tools and then the
root filesystem ([mkrootfs.sh](../tools/linux/mkrootfs.sh), with the packages in
[packages-rootfs.txt](../tools/linux/packages-rootfs.txt)):

```
bash tools/linux/deploy-rootfs.sh --out build/rootfs.tar.gz --version v0.0.0-test                  # Linux, or Git Bash on Windows
HOST=<address> bash tools/linux/deploy-rootfs.sh --version v0.0.0-test --install                      # build and install on a running Show
```

The first keeps the tarball. The second sends it to a Show (SSH on, as above) and installs it into the
spare slot, where it boots on trial and falls back if it doesn't settle. On macOS only the second
works: the build then runs on the Show itself, which takes a few minutes longer.

## 5. The kernel (Bluetooth)

LineageOS's kernel has no Bluetooth stack. [tools/linux/build-kernel.sh](../tools/linux/build-kernel.sh)
rebuilds it at the exact commit the LineageOS image came from, so the vendor modules still load, with
Bluetooth added. On Linux, or inside WSL's Ubuntu:

```
bash tools/linux/build-kernel.sh -o inputs/Image.gz-dtb-bt
```

Its header lists the source checkout and the toolchain (Arm's GCC 8.3, downloaded without root).
[tools/linux/README.md](../tools/linux/README.md) has the device tree edit that gives the daemon both
microphones instead of their average (`patch-dtb.py`, which needs `python3 -m pip install fdt`).

**Which configuration.** All three Shows build from the same kernel commit and differ only in
`DEFCONFIG`, and each wants a `KOUT` of its own so one board's objects are not read as another's:

| Board | `DEFCONFIG` | |
|---|---|---|
| Echo Show 5 2nd gen | `cronos_defconfig` | the default |
| Echo Show 5 1st gen | `checkers_defconfig` | plus `PATCHES=tools/linux/patches` |
| Echo Show 8 1st gen | `crown_defconfig` | plus `PATCHES=tools/linux/patches` |

Both 1st gen boards have a mute latch that the 2nd gen doesn't, and `tools/linux/patches` holds three
fixes for it:

- `checkers-0001-mic-enable-on-capture.patch` puts the microphone pin back when a capture stream opens
  and the latch reads ungated.
- `checkers-0002-camera-follows-the-latch.patch` lets the camera follow the latch itself, so a quick
  tap to unmute no longer leaves the camera blocked until a reboot, and starts the driver from the
  latch's real state after a boot with it engaged. A mute that is still being turned on counts as on,
  and a state pin that can't be read counts as muted.
- `checkers-0003-mute-button-follows-a-short-press.patch` keeps the driver's idea of the latch right
  after a quick tap to unmute, so the next long press mutes instead of doing nothing.

A fourth isn't about the latch:

- `checkers-0004-jack-plugged-in-at-boot.patch` lets a unit start with a plug in the headphone jack.
  The jack driver asked for its interrupt before it was ready to handle it, and a plug already in at
  power-on fired that interrupt over and over, so the boot hung at the logo. The 2nd gen Show 5 has
  the driver too, but its device tree turns the jack off, so it never starts there.

Despite the names they are not only for the 1st gen Show 5: they touch `drivers/misc/gating.c`,
`include/misc/gating.h`, `mt_soc_machine.c`, the checkers and crown camera drivers and the jack
driver (`accdet_amzn.c`), and the Show 8 has the same latch and the same jack setup.
`crown_defconfig` is in the Amazon kernel tree already, beside the others, at the same commit — it was diffed against a Show 8's
own `/proc/config.gz`, 1341 options each side and no differences either way.

## 6. The boot image

```
KERNEL=inputs/Image.gz-dtb-bt bash tools/linux/build-image.sh -o build/techo5-boot.img --no-key
```

`--no-key` is how releases are built: the rescue environment then accepts only SSH keys already on the
unit. Put your public key at `inputs/techo5_ed25519.pub` and leave `--no-key` out to have it built in.

`KERNEL_IMAGE` is the donor: your own unit's LineageOS boot image, which the new image takes its
header, load addresses and command line from (step 2). It defaults to the 2nd gen Show 5's, so on a
1st gen or a Show 8 point it at that unit's own:

```
KERNEL_IMAGE=inputs/boot-lineage-crown.img KERNEL=inputs/Image.gz-dtb-crown-bt \
  bash tools/linux/build-image.sh -o build/techo5-boot-crown.img --no-key
```

**Build the image you are going to publish, and boot that one.** An image built with a key in it and
the same image built `--no-key` are different files, and only the one you booted is known to work —
`release.ps1` refuses an image carrying `root/.ssh/authorized_keys`, so the keyed one is not the one
that ships.

## 7. Install your build

On a Show still running LineageOS, the installer takes your files in place of the release's:

```
python3 tools/install-show.py --serial <serial> --name Kitchen --boot build/techo5-boot.img --rootfs build/rootfs.tar.gz
```

On a Show already running TECHO5, step 4's `--install` puts a root filesystem in the spare slot. A boot
image goes on with `fastboot flash boot` (with the Show in fastboot, docs/install.md step 3).

## Optional: the echo canceller

[tools/linux/build-aec.sh](../tools/linux/build-aec.sh) compiles the WebRTC echo canceller helper for
armv7 (Linux or WSL). `deploy-rootfs.sh` includes `bin/techo5-aec-arm` when it exists.

## Optional: the GPU helper (Show 8 2nd gen)

[tools/gpu/build.sh](../tools/gpu/build.sh) builds `bin/techo5-gpu-arm`, the helper that draws the
screen on the Mali-G72 through the kernel's panfrost driver, and
[tools/linux/build-mesa.sh](../tools/linux/build-mesa.sh) `bin/techo5-mesa-arm.tar.gz`, the cut-down
Mesa it loads (panfrost, EGL, GLES and GBM, no LLVM). Both run in an armv7 Alpine container with Docker
(QEMU on an x86_64 host: the helper takes a minute, Mesa about an hour). An athena image
(`DEVICE_OVERLAY=tools/linux/athena/overlay`) carries them when both are there, and the daemon draws on
the cores without them, or on a kernel without panfrost.

## AirPlay 2

[tools/linux/build-shairport-sync.sh](../tools/linux/build-shairport-sync.sh) builds
`bin/techo5-shairport-sync-arm`, shairport-sync with AirPlay 2, and `bin/techo5-nqptp-arm`, the clock it
needs (Linux or WSL, with zig). Every image `deploy-rootfs.sh` builds carries both, and it stops when they
are missing; a release built from a worktree needs them copied into its `bin/`. On the device they let an
iPhone group it with other AirPlay 2 speakers; where they can't run, the daemon keeps the classic receiver
from Alpine. Its sources are checked against the SHA-256s in the script, and it prints the binaries' own.

## The video decoder

Every image carries a small ffmpeg for the video player and live cameras. It is not optional:
`deploy-rootfs.sh` stops when it is missing.

- [tools/linux/build-ffmpeg.sh](../tools/linux/build-ffmpeg.sh) builds `bin/techo5-ffmpeg-arm`, the
  32-bit one every image carries (Linux or WSL).
- [tools/linux/build-ffmpeg-arm64.sh](../tools/linux/build-ffmpeg-arm64.sh) builds
  `bin/techo5-ffmpeg-arm64`: the same ffmpeg for aarch64, fully static, with H.265, VP8 and VP9 added. It
  is a cross build in WSL or on x86_64 Linux (under a minute once its build root is made). Only the
  Show 8 2nd gen (athena) and Skylight images carry it, beside the 32-bit one, and the daemon runs it
  on their 64-bit kernels. `deploy-rootfs.sh` stops when it is missing from either image.
  OpenSSL is linked into it, so an OpenSSL fix needs a rebuild: `ROOTS=fresh` makes the build root again
  with Alpine's current packages.

Both go in `bin/`, which git ignores. A release built from a worktree needs both copied into its `bin/`.

## Tailscale

Every image `deploy-rootfs.sh` builds carries Tailscale's daemon ([docs/tailscale.md](tailscale.md)), and
it stops when it is missing.
[tools/linux/build-tailscaled.sh](../tools/linux/build-tailscaled.sh) builds `bin/tailscaled-arm64` and
its license from Tailscale's own release source, pinned and checked by SHA-256, with Go (Linux or WSL,
under a minute). One static arm64 build serves every image, on the devices' 64-bit kernels; on a 32-bit
one (an athena on Amazon's kernel) the daemon doesn't offer Tailscale. The build is
reproducible: the same script gives the same bytes on any machine. A release built from a worktree needs
it copied into its `bin/` as well, with `tailscaled-arm64.LICENSE`.

## NetBird

No image carries NetBird's client ([docs/netbird.md](netbird.md)): a device downloads it when NetBird
is turned on, from the release that first published it, checked against the size and SHA-256 in
`echod/internal/feature/netbird/pinned.go`.
[tools/linux/build-netbird.sh](../tools/linux/build-netbird.sh) builds `bin/netbird-arm64.tar.gz`, the
client and its license, from NetBird's own release source, pinned and checked by SHA-256, with a pinned
Go (Linux, WSL or Git Bash on Windows). The build is reproducible, so the same script gives the bytes
`pinned.go` names; a new NetBird version means a new build, new values there, and the archive published
with that release.

## The Japanese font

No image carries a Japanese font either: a Show or a Spot whose screen language is Japanese downloads
one into its fonts folder (`echod/internal/feature/cjkfont`), from the release that first published it,
checked against the size and SHA-256 pinned there. [tools/fonts/make_cjk_font.py](../tools/fonts/make_cjk_font.py)
builds `bin/techo5-noto-sans-jp.tar.gz`, Noto Sans JP (SIL Open Font License) cut down to kana and the
everyday kanji, with its license, from google/fonts at a pinned commit, with a pinned fontTools
(`pip install fonttools==4.65.0`). The same script gives the same bytes; release.ps1 checks them against
the pin.

## Optional: a CPU profile from a device

To see where the daemon spends its time on a unit, turn SSH on and write the number of seconds to a
file the daemon watches:

```
echo 60 > /run/techo5/profile
```

A few seconds later the profile starts, and when it ends it is written to
`/data/techo5-linux/cpu-<time>.pprof` (the daemon's log says so). Copy it off and read it with
`go tool pprof -top cpu-<time>.pprof`. Nothing listens on the network for this; it only answers that
file, which only root on the device can create.

## Package versions

`tools/linux/packages.txt` and `packages-rootfs.txt` name exact Alpine package versions. Alpine keeps
only the newest build of each package, so an old version eventually disappears from its mirror:
`fetch-inputs.py` then takes the newest and says so, and a root filesystem build installs whatever
Alpine 3.24 serves that day. Releases don't depend on this: everything a unit or the installer needs
is published with the release, and the lists are brought up to date (and tested) before a release.

What version arrives floats; what arrives unchecked does not. Every package `fetch-inputs.py` keeps has
to match the checksum Alpine's index gives for it and the datahash inside the package itself, and the
wake word models come from a commit pinned in the script, each file pinned to a sha256. Moving a model
pin is a two-line edit at the top of `tools/fetch-inputs.py`; the script prints what a file now hashes
to when it does not match, and techo5-dot's `tools/install-dot.py` carries the same pins for the models
it puts on a Dot.

## Releases (maintainer)

`tools/release.ps1` (Show 5), techo5-dot's `tools/release-dot.ps1` and techo5-spot's
`tools/release-spot.ps1` sign a manifest with the key in `TECHO5_SIGN_KEY` and publish with `gh`. They
are PowerShell scripts for the maintainer's Windows machine. Devices only take a manifest signed by the
project's key, and the installers check the same signature before they believe a manifest, so a fork
publishing its own releases needs its own key, a daemon built with its public key
(`echod/internal/update/trust.go`) and the same key in `tools/techo5lib.py` (`RELEASE_KEY`).

`TECHO5_SIGN_KEY` never leaves the maintainer's machine — it is not a GitHub Actions secret, and the
build itself does not need to happen locally to keep that true. Pushing a release tag runs
[.github/workflows/build.yml](../.github/workflows/build.yml), which tests every device's build and
builds `echod-arm`, `echod-arm-dot` and `echod-arm-spot` on GitHub's own runners, attesting (via
Sigstore, keyless) that they came from that exact commit and tag — so anyone, not just the maintainer,
can check a release actually matches this repo's source instead of trusting that it does.

Each device keeps its own version numbers, and Home Assistant compares the version a device's daemon
reports with its release's, so the tag names the device: `vX.Y.Z` for the Show, `dot-vX.Y.Z`,
`spot-vX.Y.Z`. The binaries are stamped `vX.Y.Z`. All tags go on this repository, since the daemon
for all three is built here:

```
git tag v0.6.0 && git push origin v0.6.0                       # the Show; triggers the workflow
gh run download -n techo5-v0.6.0 -D bin                        # once it finishes
bash tools/linux/third-party-sources.sh v0.6.0                 # build/techo5-third-party-sources-v0.6.0.tar.gz
.\tools\release.ps1 -Version v0.6.0 -Notes "..." -PrebuiltArm bin\echod-arm -PrebuiltArmDot bin\echod-arm-dot -Agents bin -ThirdPartySources build\techo5-third-party-sources-v0.6.0.tar.gz
```

The images carry programs built from others' sources (FFmpeg, Shairport Sync, nqptp, librespot; see
NOTICE), with their licenses in `/usr/share/licenses`. Their licenses want the source beside the
binaries, so every release attaches it: `third-party-sources.sh` downloads each source the build scripts
name, checks its SHA-256, and packs it with the patches, licenses and build scripts. The three release
scripts refuse to publish without `-ThirdPartySources`; the Dot and the Spot take the same archive.

All three Shows share the tag `vX.Y.Z` and the one `echod-arm`: which board a unit is settles itself at
run time from the panel name in its kernel command line. What is per-board is the boot image, and each
goes on the same release under its own name, `-Boot` for the 2nd gen Show 5, `-CheckersBoot` for the
1st gen and `-CrownBoot` for the Show 8. Pass whichever changed; the installer takes the rest from the
newest earlier release that carries them. A boot image is signed into the manifest and checksummed like
everything else, and the script refuses one with an SSH key inside.

Do not give the Show 8 a tag of its own. A `crown-v*` tag would not trigger the workflow, and
`install-show.py` looks back through plain `vX.Y.Z` releases when a release carries no image for a
board, so a separate prefix would make it stop finding them.

For the root filesystem, `PREBUILT_DAEMON=bin/echod-arm tools/linux/deploy-rootfs.sh --out ... --version
v0.6.0` puts that same attested binary in it. The Dot (`git tag dot-v0.5.3`, then techo5-dot's
`release-dot.ps1 -PrebuiltArmDot`) and the Spot (`git tag spot-v0.3.0`, then `BUILD_TAGS=spot
PREBUILT_DAEMON=... deploy-rootfs.sh` and techo5-spot's `release-spot.ps1`) work the same way.

Every one of these runs `gh attestation verify --source-ref refs/tags/<tag>` on the binary itself and
refuses it unless CI built it from that release's own tag. A binary from a branch, or from a run
started by hand (stamped `v0.0.0-dev`), would report the wrong version once installed, and Home
Assistant would offer the update forever. Leaving out the prebuilt binaries still builds locally
exactly as before, for a quick local test release without pushing a tag first.

## Where things default

Everything goes into git-ignored folders in the checkout, and each can be moved with an environment
variable: `inputs/` (`TECHO5_INPUTS`), `build/` (`TECHO5_WORK`), `backups/` (`TECHO5_BACKUPS`).
`KERNEL_IMAGE` and `KERNEL` pick the boot image's kernel; `KEY` or `TECHO5_SSH_KEY` the SSH key
`deploy-rootfs.sh` uses (default `~/.ssh/id_ed25519`); `GO` and `PYTHON` the tools.
