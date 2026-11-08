# Spotify Soloist device experiment

This development build runs the official Spotify client on a TECHO5 device through a private
glibc runtime. It is a proof of concept, not a release-image migration. Librespot is retained as
a recoverable alternative; the two receivers are never intentionally launched together.

**Experimental, largely AI-assisted implementation.** This port was built primarily with AI
assistance and tested interactively on one physical device. It is not a promise of production
quality, long-term maintenance, or compatibility with future Soloist releases. Spotify can change
the WebSocket interface, client behavior, binary ABI or audio/volume handling at any time, and
those changes may break this integration. Automatic executable updates do not make the private
runtime or these assumptions future-proof. Review and additional independent device testing are
needed before considering this for normal release images.

Native ARM64 playback, Spotify pairing, cover/title display, previous/next and play/pause have
been confirmed on an MT8163 device with Alpine 3.24.2, a 32-bit userspace and an aarch64 4.9.337
kernel. The user confirmed audible music. Other boards and 32-bit kernels are not validated.
The initial ARM32 client passed `--version` but its runner became unresponsive during real use.
ARM64 is the working path on this test device; passing a version check was insufficient.

## Enter your key

1. Open the TECHO5 setup page as usual and authorize the browser with the device button.
2. Under **Sound & Voice → Play to this device**, enable **Spotify Connect** and
   **Use Spotify Soloist (experimental)**.
3. Generate a personal key at the [Spotify Soloist dashboard](https://developer.spotify.com/dashboard/soloist).
   Spotify requires a Premium account to generate it. Enter the key in the password field and Save.
4. Open Spotify on the same LAN and select TECHO5 under its usual device name.

The page reports whether a key is saved, not the key itself. An empty field preserves the saved
key. **Remove saved API key** prevents Soloist from starting again; the saved Connect pairing is
retained. Turning Spotify off also retains Soloist's key and pairing. Factory reset removes both.
The legacy bitrate/normalization options apply only to librespot.

The current setup page uses TECHO5's existing temporary, physically authorized LAN HTTP interface.
The password input masks the display; it does not encrypt HTTP traffic. Use a trusted LAN. The key
does not enter TECHO5's exported settings, source code or logs. soloistd's private JSON config is
0600 and readable by `streaming`. Its config directories are root-owned, group-accessible to
`streaming` (0750), so the receiver cannot redirect privileged configuration writes through
directory symlinks. Writable session/cache/runtime directories are 0700. Soloist still receives the key in argv; the test
device mounts `/proc` with `hidepid=2`, but root and the same user can inspect it. This is not a
secret-file interface supplied by Spotify.

## Runtime and audio

* `/data/techo5-soloist/runtime`: extracted Ubuntu Noble packages, including glibc, libpulse,
  PulseAudio, modules and patchelf. System musl is unchanged. The package tree is deliberately
  oversized for this experiment; unrelated executables in it are not started as services.
* `/data/techo5-soloist/soloistd`: statically cross-built, pinned upstream supervisor.
* `/data/techo5-soloist/exec`: launcher used for every upstream Soloist command, including update
  validation. It patches each fresh download's ELF interpreter and RPATH to the private runtime,
  then executes Soloist directly. Starting it by executing the loader instead makes `/proc/self/exe`
  name the loader and breaks self-execution/crash-handler behavior; the patch avoids that problem.
* The normal TECHO5 streaming component starts a private PulseAudio server as `streaming`, with only
  a private Unix socket and a clocked `module-null-sink`. No PulseAudio TCP listener, ALSA device takeover,
  systemd service or desktop session is introduced.
* A persistent `parec` monitor reader emits 44.1 kHz stereo float32 PCM. TECHO5 discards idle silence,
  undoes PulseAudio's cubic input-volume attenuation before S16 conversion, and forwards playing
  sessions into `media.PlayReceived`, preserving its mixer, output routing and volume limits. The
  first FIFO-only prototype sometimes needed an extra PulseAudio client before sound became audible;
  the clocked monitor path started by itself in the subsequent user test.
* A loopback-only Soloist WebSocket supplies metadata, timing, play/pause and skip control. Commands
  require an acknowledgement before being reported as accepted. Position and duration feed the
  same timing API used by Modern Now Playing and synchronized lyrics; pause freezes the anchor,
  seeks replace it and track changes discard the previous position.
  Cover selection prefers the largest advertised HTTPS image, and Spotify's square cover is retained
  at up to 640 pixels instead of being reduced to the legacy 240-pixel thumbnail before Modern display.
* Soloist chooses its own Connect TCP port. TECHO5 inspects the real child's listening sockets and
  opens only its wildcard IPv4 TCP listener on `wlan0`, in `TECHO5-SOLOIST`. The loopback WebSocket
  is excluded. Rules are adjusted after restart and removed at shutdown; ordinary mDNS is already
  permitted by the existing firewall.

The supervisor starts only when the feature, experimental backend, runtime and key are configured.
Unexpected exits back off rather than busy-loop. PulseAudio failure restarts the session. A local
WebSocket health query detects a hung client. Shutdown gives soloistd time to reap Soloist and
cleans up the process group, audio pipes, timing and dynamic firewall rule.

Phone and device volume now share TECHO5's device steps, using the existing echo suppression,
connection handling and raise limits. The user confirmed both directions. The monitor stays in
float format until normalization so low phone volumes do not lose precision through premature
S16 quantization. Conservative buffering delays a larger inverse gain until earlier audio has
passed, avoiding a loud burst. Voice ducking, alarms and Bluetooth output use the existing mixer
but have not all been physically retested with Soloist.

## Automatic updates and authentication

The small checked patch to soloistd changes its execution call sites to honor `SOLOIST_EXEC`.
Its updater is otherwise upstream: startup and six-hour CDN metadata checks, validated temporary
downloads, atomic replacement, restart, and forced refresh after build-expiry exit 10. Failed
proactive downloads keep the installed client; expiry failures exit for TECHO5's bounded retry.
An update requiring a newer glibc may fail validation and need a newer private runtime.

Configuration, managed binaries and pairing live under `/data/misc/techo5-soloist` on this device,
outside firmware slots. Paths are derived beside `layout.StateDir` for other TECHO5 variants.
Executable replacement does not erase them. Playback cache is limited to 100 MB.

Build expiry (90 days), the personal API key and the saved Connect session are separate mechanisms.
Spotify does not document a matching 90-day key or session lifetime. A revoked/rejected key can be
replaced in the same setup field; lost pairing requires selecting the device in Spotify again.
No permanent authentication guarantee is made.

## Build and install the experiment

Use Linux/WSL with Ubuntu's archive keyring, APT, dpkg-deb, Python, Git, tar and Go compatible with
TECHO5's `go.mod`. Build the working ARM64 sidecar on a 64-bit-kernel device:

```sh
SOLOIST_ARCH=arm64 bash tools/linux/build-soloist-runtime.sh
SOLOIST_GOARCH=arm64 bash tools/linux/build-soloistd.sh
(cd echod && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-s -w' -o ../bin/echod-soloist-arm ./cmd/echod)
SOLOIST_GOARCH=arm64 bash tools/linux/package-soloist-poc.sh
```

The TECHO5 daemon remains ARM32. The sidecar and runtime are ARM64; use `armhf` for the runtime
and `arm` for soloistd to reproduce the unsuccessful ARM32 path. Do not use ARM64 on a 32-bit kernel.

APT uses a private state/cache and verifies signed Ubuntu indices and package hashes. It does not
install packages or run maintainer scripts on the host. `packages.lock`, `packages.sha256`, source
URLs and the packages' copyright files are retained. Keep the cached `.deb` files for exact
reproduction; `SOLOIST_PACKAGE_LOCK` can request the recorded versions while they remain available.
No guessed checksum or unsigned mirror fallback is used. No Spotify archive is packaged: soloistd
downloads directly from Spotify on the user's device.

Copy `bin/techo5-soloist-poc.tar.gz` to a test device, extract it to a temporary bundle directory,
then run `sh device.sh install BUNDLE_DIRECTORY` there. This developer installation requires SSH;
entering/replacing the personal Spotify key afterwards does not. The installer verifies its bundle,
saves the original daemon/librespot, and bind-mounts the experimental daemon over the original.

The default installation is temporary: **reboot restores the original firmware daemon**, while
private credentials and runtime files remain on `/data`. `sh device.sh boot-on` adds a gated boot
hook to the existing runner and enables reactivation after reboot; the original runner is backed
up first and unrelated local additions are preserved. `boot-off` disables future reactivation.
`sh device.sh rollback` disables that hook and removes only this experiment's topmost bind mount.
The Git baseline is `codex/librespot-baseline`; device binaries are
also saved under `/data/techo5-soloist/baseline`. No remote repository or release was modified.
Do not apply the regular firmware updater while running this bind-mounted experimental daemon.

## Evidence and outstanding checks

Executed locally: upstream soloistd tests after the launcher adaptation; ARM32/ARM64 cross-builds;
targeted TECHO5 tests for streaming, setup, config and factory reset; Spot/Dot streaming/config
tests; race tests of the new Soloist cases; static binary inspection and shell syntax checks.
The timing changes add tests for playback, pause/resume, seek, title switch and source-safe cleanup.
Float-volume tests cover 1–100% attenuation and every device step's percentage echo.

Executed on the device: native client/runtime startup, PulseAudio Unix-protocol connection, a
synthetic native PCM transfer (88,160 nonzero samples, peak 999), GUI key entry with restricted file
modes, soloistd-managed official download, direct Connect HTTP `OK` response over the LAN,
Spotify mDNS advertisement, successful pairing, audible playback and user-confirmed controls,
Modern progress display, automatic audio start, and both directions of device-volume synchronization.
The old receiver was absent while Soloist ran. Native recovery tests explicitly blocked HTTPS for
the receiver user in IPv4 and IPv6: an attempted update failed without changing the managed binary,
key or running client, and all temporary rules were removed. An ARM64 PIE fixture returning exit
10 was then atomically substituted for the managed client; TECHO5 restarted the supervisor,
soloistd downloaded/patched the official client, restarted it and restored pairing with the same
key. This simulates the documented expiry exit; it does not claim to wait 90 days or exercise a
future release's ABI. The fixture and opt-in device recovery test are in `tools/linux/`.

The opt-in boot hook was also activated on the test device and a real reboot completed. The
experimental daemon, audio service, supervisor and authenticated Soloist session returned without
re-entering the key, with a new Connect port admitted automatically. Spotify reported the device
as logged in but inactive until selected for playback again. The original firmware executable
remains underneath the bind mount, and the saved original runner can restore the boot script.
The user then confirmed audible music, the progress bar and synchronized volume after reboot,
without entering the key again.
A final hard SIGKILL of the TECHO5 daemon deliberately left native children behind. The replacement
daemon removed those processes, started one fresh client and restored the authenticated session.
Permission checks confirmed root-owned config directories, a 0600 key file and receiver-private data.

Missing-key gating and malformed input are covered by tests. A deliberately invalid key also
started an isolated native pair-mode process: startup alone does **not** prove that Spotify accepts
a key. Real rejected-key pairing was not exercised against the user's account. The UI reports
saved configuration separately from an authenticated session and recognizes key-rejection errors
without logging their contents.

Still not claimed: a fully tested fresh release image, six-hour live-update cycle, real 90-day
expiry, unattended multi-day operation, or validation across every TECHO5 board. The private runtime
does not automatically update its Ubuntu libraries. Keep this experiment isolated from production.

## Changed files

* `echod/internal/config/streaming.go`: experimental backend preference; no credentials in exported settings.
* `echod/internal/feature/setup/streaming.go`: key input/removal, backend selection and status.
* `echod/internal/feature/streaming/streaming.go`, `spotify.go`, `avahi.go`: backend dispatch, restart
  tracking, transport registration and cleanup of orphaned native processes.
* New streaming files `soloist.go`, `soloist_ws.go`, `soloist_audio.go`, `soloist_door.go`,
  `soloist_position.go`: lifecycle/secrets, WebSocket, float PCM/volume, dynamic firewall and timing.
* `soloist_test.go` and `soloist_position_test.go`: configuration/security, PCM, volume, controls,
  socket filtering and timing tests.
* `echod/internal/feature/media/position.go`: source-owned timing cleanup;
  `echod/internal/feature/firstrun/reset.go`: remove private credentials/session at factory reset.
* New `echod/cmd/soloist-probe/main.go`: on-device Spotify mDNS diagnostic.
* New `tools/linux/build-soloist-runtime.sh`, `build-soloistd.sh`, `patch-soloistd.py`,
  `soloistd-exec.go.txt`, `techo5-soloist-exec`, `package-soloist-poc.sh`, `soloist-poc-device.sh`,
  `soloist-expired-probe.go.txt`, `test-soloist-poc-recovery.sh`, `inspect-soloist.sh`: reproducible
  runtime/build/launcher/packaging/installation/recovery and inspection tools.
* `docs/setup.md`, this report, the historical `spotify-soloist.md` and `NOTICE`: usage and provenance.

No baseline librespot files were removed; they remain the explicit recovery backend for this experiment.
