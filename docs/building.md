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
`ssh_keys` action, copy the binary over, and bind it in place until the next reboot:

```
scp bin/echod-arm root@<address>:/tmp/echod-test
ssh root@<address> 'mount --bind /tmp/echod-test /usr/local/bin/techo5 && killall techo5'
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

## 6. The boot image

```
KERNEL=inputs/Image.gz-dtb-bt bash tools/linux/build-image.sh -o build/techo5-boot.img --no-key
```

`--no-key` is how releases are built: the rescue environment then accepts only SSH keys already on the
unit. Put your public key at `inputs/techo5_ed25519.pub` and leave `--no-key` out to have it built in.

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

## Package versions

`tools/linux/packages.txt` and `packages-rootfs.txt` name exact Alpine package versions. Alpine keeps
only the newest build of each package, so an old version eventually disappears from its mirror:
`fetch-inputs.py` then takes the newest and says so, and a root filesystem build installs whatever
Alpine 3.24 serves that day. Releases don't depend on this: everything a unit or the installer needs
is published with the release, and the lists are brought up to date (and tested) before a release.

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
.\tools\release.ps1 -Version v0.6.0 -Notes "..." -PrebuiltArm bin\echod-arm -PrebuiltArmDot bin\echod-arm-dot
```

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
