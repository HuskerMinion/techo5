# Contributing

Thanks for wanting to help. Bug reports, testing on your own devices and pull requests are all welcome.

## Reporting a problem

Open an issue with:

- the device (Echo Show 5 1st or 2nd gen, Show 8 1st gen, Spot, Dot) and the TECHO5 version
- what you did, what you expected, and what happened
- the device's log around the time, if you can get it: with SSH on it is `/data/techo5-linux/techo5.log`
  (`echod.log` on a Dot)

Please remove anything personal first: serial numbers, MAC addresses, IP addresses, Wi-Fi names,
tokens and people's names.

## Getting a build onto your device

You don't need to build a whole image to try a change. Most work is in the daemon, `echod`, and a test
daemon can run on a device that already has TECHO5:

1. Set up Go and fetch the code: [docs/building.md](docs/building.md), steps 1 and 3.
2. In Home Assistant, send your public key with the `ssh_keys` action and turn on the device's SSH
   switch ([docs/actions.md](docs/actions.md#set-the-ssh-authorized-keys)).
3. Build and copy it over:

   ```
   DEVICE=192.168.X.X #Replace with your device's IP address
   
   cd echod
   GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o ../bin/echod-arm ./cmd/echod
   cd ..
   scp -O bin/echod-arm root@$DEVICE:/tmp/echod-test
   ssh root@$DEVICE 'mount --bind /tmp/echod-test /usr/local/bin/techo5 && killall techo5'
   ```

   For a Spot add `-tags spot`. For a Dot add `-tags dot`, and the daemon there is `echod`:
   `mount --bind /tmp/echod-test /usr/local/bin/echod && killall echod`.

The test daemon runs until the next reboot, and the device then comes back on the release it had. A
build from your machine reports version `v0.0.0-dev`, so Home Assistant offers the release as an
update while it runs. That is expected; don't take the update until you're done testing.

Changes to the root filesystem, the kernel or the boot image are in
[docs/building.md](docs/building.md), steps 4 to 7. Those can leave a device that doesn't boot, so read
[docs/install.md](docs/install.md) on getting back to fastboot first.

Turn SSH off again when you're done.

## Before you open a pull request

Run the tests for all three builds, since some packages differ per device:

```
cd echod
go test ./...
go test -tags dot ./...
go test -tags spot ./...
go vet ./...
gofmt -l .
```

The tests need Linux (or WSL on Windows). Then:

- Keep a pull request to one change. Say what it fixes and how you tested it, and on which device.
- Write commit messages like the ones in `git log`: the area, a colon, then what changed, for example
  `display: add Russian as a screen language`.
- Use US English in code, comments and docs.
- New settings, entities or actions need a line in the docs.
- Keep personal details out of code, test data, logs and screenshots, as above.

## Where things are

- `echod/` is the daemon: Home Assistant's API, voice, display, media and the device's hardware.
  `echod/internal/feature/` has one folder per feature.
- `tools/` has the installer, the image builds and the release scripts.
- `docs/` is the documentation. [docs/overview.md](docs/overview.md) is a good place to start.

The Dot and the Spot have their own repositories for their installers and images
([techo5-dot](https://github.com/HuskerMinion/techo5-dot),
[techo5-spot](https://github.com/HuskerMinion/techo5-spot)). Their daemon is built here.

## Releases

Releases are built by GitHub Actions from a tag and signed by the maintainer, so a contributor's build
never reaches other people's devices. Merged changes go out with the next release.
