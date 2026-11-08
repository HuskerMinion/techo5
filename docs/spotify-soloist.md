# Spotify Soloist migration investigation

Historical investigation of the unmodified image, November 8, 2026. **The later
[working device proof of concept](spotify-soloist-poc.md) supersedes the original stop decision.**
It adds a private runtime, native ARM64 Soloist, soloistd, PCM bridging and an API-key setup field.
The limitations below explain why a stock image cannot run the official client directly; they
are not a claim that the experimental integration is impossible. No Spotify executable or personal
key is included in the repository.

## What users can do today

The current Spotify Connect switch is under Settings → Sound, the setup page's Sound & Voice
section, and Home Assistant. It still uses librespot and can encounter the
[upstream audio-key failure](https://github.com/librespot-org/librespot/issues/1649).
That failure does not mean a Soloist API key is missing; the current receiver does not use one.

The existing [setup instructions](setup.md) describe Music Assistant's Soloist-based Spotify
Connect plugin playing to TECHO5 through Sendspin as an alternative. That runs Soloist on the
Music Assistant host, not directly on TECHO5. This investigation did not test that arrangement.

## Compatibility findings

TECHO5's image uses Alpine 3.24.2, musl, and BusyBox init. The normal image is armv7, including on
several devices with 64-bit kernels; `mkrootfs.sh` also supports aarch64. The package list includes
ALSA tools and BlueALSA, but neither PulseAudio nor PipeWire. The daemon opens ALSA through its own
Go implementation and performs mixing, ducking, routing and software volume itself.

Spotify publishes ARM32, ARM64 and x86_64 Linux archives, but matching the CPU is insufficient:

| Inspected official archive | ELF interpreter | Required glibc symbols |
| --- | --- | --- |
| `soloist_release_arm32.tar.gz` | `/lib/ld-linux-armhf.so.3` | Through `GLIBC_2.38` |
| `soloist_release_arm64.tar.gz` | `/lib/ld-linux-aarch64.so.1` | Through `GLIBC_2.38` |

The ARM32 executable identifies itself as
`soloist 1.3.10.51 build 1793988108 (20261106) (g19412df6ea) (linux/arm)` when run under
QEMU with an ARM glibc runtime. Against the exact Alpine minirootfs pinned in `tools/techo5lib.py`,
it fails before startup:

```text
qemu-arm: Could not open '/lib/ld-linux-armhf.so.3': No such file or directory
```

Adding Alpine's signed `gcompat` 1.1.0-r4 and `libatomic` 15.2.0-r5 does not supply the required ABI.
Invoking the musl loader directly with `libgcompat.so.0` preloaded (to avoid QEMU's nested-exec
limitation with the gcompat loader) reports missing symbols including `__fcntl_time64`,
`__pthread_cond_timedwait64`, `__isoc23_strtol`, `__isoc23_sscanf` and `__openat64_2`.
The executable never reaches its version output in this environment. This is an emulated loader
test, not a physical-device test; the direct-loader method is diagnostic, not a supported launcher.

There is a separate audio integration problem. Soloist's documented Linux outputs are
[PipeWire and PulseAudio](https://developer.spotify.com/documentation/soloist/reference/command-line).
Its CLI has no documented raw-PCM stdout/FIFO backend or direct ALSA output. Its WebSocket API
exchanges playback state and commands, not audio. Sending Soloist directly to an ALSA device would
bypass TECHO5's mixer and its voice/alarm arbitration and may contend for the same device.

The [public Soloist repository](https://github.com/spotify/soloist) contains a README, not the
client implementation or a build system. Cross-compiling a musl client or adding a PCM backend
from that repository is therefore unavailable. The shipped third-party notices do not provide
the proprietary client's source code.

These findings prevent a working drop-in replacement on the current image. They do **not** prove
that a larger platform integration is impossible: an isolated, maintained glibc runtime plus a
PulseAudio sink feeding TECHO5's PCM input is a possible research path. It needs verified packages,
glibc-compatible client libraries (musl audio libraries cannot simply be loaded into Soloist),
resource measurements, and device tests. Replacing system musl or adding loader symlinks alone is
not a solution. No speculative runtime or audio bridge has been installed by this change.

## Existing integration inventory

| Area | Files and behavior |
| --- | --- |
| Receiver | `echod/internal/feature/streaming/spotify.go`: pipe-backend launch arguments, metadata event script/FIFO, volume FIFO, legacy volume fallback, private login cache, filtered logging. Uses avahi discovery and TCP 4070. |
| Lifecycle | `streaming.go`, `group.go`, `proc.go`, `avahi.go`: network component, settings restore, start after avahi is ready, terminate/restart receiver group if avahi fails, crash backoff up to one minute, SIGTERM then forced shutdown, cleanup of orphaned receivers through `/proc/*/exe`. |
| PCM and volume | `audio.go`: 44.1 kHz stereo S16 input into `media.PlayReceived`; compensate old librespot scaling. `librespot-volume-fifo.patch`: accept device volume and output unscaled PCM. Device volume, phone volume and night limits are synchronized. |
| Media control | `streaming.go` registers `media.StopOnPause` for Spotify. Current device pause drops the received track; it does not remotely pause Spotify. Track/cover events reach media/home. Shared media/speaker code also handles Bluetooth output and voice/alarm arbitration. |
| Settings | `echod/internal/config/streaming.go`: enable, 96/160/320 kbit/s, normalization. `config/store.go` persists through the state store; `config/export.go` separates secrets and device-local enable switches from portable exports. |
| Setup UI | `echod/internal/feature/setup/streaming.go`: existing Sound & Voice form, Save button, enable/quality/normalization choices. `setup.go` and HTTP handlers provide temporary browser access authorized by a physical button press and session/form tokens. |
| Device and HA UI | `echod/internal/feature/display/sheet.go`: Sound toggle; `streaming.go`: ESPHome `spotify_connect` switch. Remote settings allowlists and component tests include that entity. `lib/locale/text.go` contains the device-screen description. |
| Login state | `spotifyCache()` uses `techo5-spotify` beside `layout.StateDir` on persistent `/data`, mode 0700, owned by the dedicated `streaming` user. Turning Spotify off calls `forgetSpotify()`. `feature/firstrun/reset.go` removes the cache on reset. |
| Source build | `tools/linux/build-librespot.sh`: checksum-verified crates.io librespot 0.8.0, locked Cargo dependencies, static armv7/musl cross-build, pipe backend, avahi and Rust TLS, local volume patch. |
| Packaging | `deploy-rootfs.sh` stages `bin/techo5-librespot-arm`; `mkrootfs.sh` installs it and creates receiver/inet permissions. `packages-rootfs.txt` supplies shairport/avahi and audio dependencies; they also serve AirPlay and must not all be removed. |
| Boot/update | `rootfs/etc/inittab` respawns `techo5-run`; echod supervises receivers, not a separate librespot init service. Root slots are read-only except controlled updates; `/data` is persistent. Binary-only echod updates cannot add missing runtime/audio libraries. |
| Network | `rootfs/usr/local/sbin/techo5-firewall` permits mDNS UDP 5353 and librespot TCP 4070. Soloist discovery/listening ports must be measured; keeping the librespot port rule is not evidence of Soloist discovery. |
| Release/licenses | `tools/release.ps1` checks shared image binaries; `third-party-sources.sh` bundles source/patch/build script; `tools/linux/licenses/librespot/LICENSE`, `NOTICE`, `docs/building.md` describe distribution. |
| Existing tests | `streaming_test.go`, `proc_log_test.go`, shared media/receiver tests and configuration export/remote/component tests cover lifecycle, PCM, volume and persistence. Spotify-specific tests currently test librespot behavior; the setup form has no Spotify-specific tests. |

The daemon has Android layout support as well as the Linux images. Android's bionic runtime and
vendor audio are not a glibc/PulseAudio environment either; Linux CPU support is not Android support.
The Panel app explicitly does not offer these local receivers. Dot/Spot distribution also involves
their separate image repositories, which were not modified.

## soloistd source review

Reviewed commit
[`ccf073a24d682249de8e74b8f81ab5de169457a6`](https://github.com/MopigamesYT/soloistd/tree/ccf073a24d682249de8e74b8f81ab5de169457a6).

* `soloistd run` is a foreground supervisor. `service.go` explicitly supports using it under another
  process manager; BusyBox init is not itself a blocker. TECHO5 could supervise this command without
  installing a systemd service or forking upstream. The source needs Go 1.24 and can target ARM;
  the README's prebuilt release binary is amd64, so it should not be copied to TECHO5.
* `download.go` selects official ARM32, ARM64 or amd64 archives. It compares CDN ETag, then CRC32C
  metadata, then Last-Modified/length, and fetches when changed or untracked. These metadata values
  identify releases; the code does not cryptographically verify the downloaded bytes against them.
  Downloads use HTTPS, bounded archive extraction, a temporary binary and a `--version` check before
  atomic rename. It does not provide an independent release signature or a previous-binary rollback.
* `supervisor.go` checks at startup and every six hours. A successful update stops Soloist and restarts
  it with the same configuration/data paths. Proactive check/download failures leave the running
  process alone. Exit 10 forces a fresh download and restart; another expiry within five minutes
  fails rather than looping. If that download fails, the supervisor exits and requires an outer
  process manager to retry. Other unexpected exits also need the outer restart policy.
* `config.go` writes a 0600 JSON file in a 0700 directory via a temporary file and rename. `paths.go`
  honors XDG config/data/cache directories. Those paths must be explicitly set to writable persistent
  `/data` locations for TECHO5's `streaming` user, whose home is `/var/empty`. Cache size must be bounded;
  upstream's default is unlimited. Cache and credentials should have separate reset semantics.
* The API key and Connect session are outside the managed executable, so normal binary replacement
  preserves them. Upstream setup validates required fields, not whether Spotify accepts the key.
* `config.soloistArgs()` passes the API key as an argument to Soloist. Private configuration keeps it
  out of the unit file, but not process arguments. The documented Soloist CLI offers no secret-file
  or stdin option. A private environment file expanded into an argument does not remove this
  exposure. A future integration must restrict process visibility and diagnostics, redact all child
  output, and describe the remaining same-user/root access rather than promise secrecy from `ps`.
* Shutdown allows Soloist ten seconds after SIGTERM. TECHO5's existing child shutdown timeout is
  three seconds: it would need coordination so it does not kill soloistd before the child is reaped.

No upstream source was changed, vendored or forked.

## Build expiry, keys and sessions are different

[Spotify documents](https://developer.spotify.com/documentation/soloist) a 90-day build lifetime;
[exit code 10](https://developer.spotify.com/documentation/soloist/reference/command-line) means the
executable expired. soloistd handles this as described above. It cannot update an incompatible ABI
into compatibility, and future releases may add runtime requirements.

The [authentication documentation](https://developer.spotify.com/documentation/soloist/concepts/authentication)
separately describes a personal API key generated by a Premium account at the
[Soloist key page](https://developer.spotify.com/dashboard/soloist), required at every startup.
Revoking or replacing it requires restarting with the replacement. The reviewed documentation does
not specify a 90-day API-key lifetime or promise that a key lasts forever. soloistd cannot generate
or renew personal keys. Do not infer API-key expiration from build expiration.

Selecting the device in Spotify on the same LAN creates the Connect session, restored on subsequent
starts if the data directory remains the same. No routine re-pair is documented for executable
updates, but session revocation or rejection can require pairing again. No independent session/token
lifetime or refresh guarantee is published in the reviewed documentation. An integration must
handle loss of authentication; it cannot promise permanent unattended access.

## Implementation sequence after compatibility is resolved

1. Demonstrate an official executable starting and playing on each supported device/runtime. Either
   obtain a supported musl/PCM build from Spotify, or validate and maintain an isolated glibc runtime
   and audio-server bridge. Test CPU, RAM, idle behavior, storage and update compatibility. Keep
   proprietary binaries out of distributed images: Spotify explicitly prohibits redistribution;
   download on the device from the official source instead.
2. Cross-build unmodified, pinned soloistd source. Use `soloistd run` under TECHO5's lifecycle with
   crash backoff, child cleanup and coordinated shutdown. Reuse its update logic. Store managed
   executable, release metadata, config and sessions on persistent writable storage. Keep its
   configuration/session separate from the old librespot cache.
3. Add Soloist configuration to the existing authorized setup form. Use a blank password input with
   Save/Replace and explicit Remove actions; render only whether a key is configured, never its
   value. Blank Save preserves the key. Explain how to generate a personal key and link the official
   instructions. Enable without a key should show “API key required” and start no child. Report
   starting, available, signed in, authentication rejected, offline and update errors separately;
   a running process is not proof that Spotify accepted the key.
4. Persist secrets atomically with restricted ownership/modes, exclude them from exports/diagnostic
   bundles and never log form contents, child argv or unredacted error output. Preserve keys when
   disabled, rebooted or updated; explicitly remove credentials on Remove/factory reset. Review the
   setup page's transport security before transmitting a personal key.
5. Bridge PCM into `media.PlayReceived`, connect metadata and playback controls over a loopback-only
   WebSocket, and verify bidirectional volume without double attenuation or feedback. Soloist's
   WebSocket has no built-in authentication/TLS/Origin protection and must not be exposed to the LAN.
   Do not carry over unsupported librespot bitrate/normalization CLI options.
6. Only after a working replacement is tested, stop the old child and remove its binary, build
   script, volume patch, license packaging and dead tests/options. Keep a narrowly scoped migration
   cleanup for old binaries/orphans; there is no librespot systemd unit to disable. Reuse the device
   name and enable preference, preserve unrelated settings, and require a new key and Connect pairing:
   librespot's cached authentication is not Soloist's session format. Update the firewall to actual
   discovery ports and complete both fresh-image and existing-slot/binary-update paths.

## Validation performed and still required

Executed locally in WSL:

* Official ARM32 and ARM64 archive static inspection with `file`/`readelf`.
* Alpine base SHA-256 checked against the repository's pin:
  `5552f1ef2398cee46ef2cf733f8f1258f39a77ad065dfd47282fc0df9fe1976c`.
* ARM32 `--version` against the pinned musl base: failed (missing loader).
* ARM32 musl/gcompat loader test with Alpine libatomic: failed (unresolved symbols).
* ARM32 `--version` with an ARM glibc runtime under QEMU: passed. This establishes executable
  viability on a different runtime, not authentication, audio, device-kernel or network compatibility.
* Inspection helper: Bash syntax, help, missing-argument/missing-file handling, non-ELF rejection,
  and complete ARM32/ARM64 reports passed. `git diff --check` passed for tracked changes.

Downloaded archive hashes, recorded for reproducibility rather than claimed upstream signatures:

```text
ARM32 7115ce838ca08bf3c69587e147217c8023e0f739674b2f79cd806edc1b4c6bb1
ARM64 27e85740d33e9e1dfa5f3ccc999230b13eb8a2c8c65a1413a3fd85be77cdf96c
```

Archives were fetched directly over HTTPS from `soloist-builds.spotifycdn.com`; no third-party
executable mirror was used. No API key was requested, supplied or logged.

Use the added static inspection helper on future downloads (Linux/WSL, binutils required):

```sh
bash tools/linux/inspect-soloist.sh /path/to/extracted/soloist
```

It neither downloads nor executes the supplied file. Its SHA-256 records the input, not authenticity.
It lists direct ELF dependencies; audio libraries loaded dynamically require separate inspection.

No fresh-image build, Go suite or soloistd suite was run: runtime Go code and image packaging were
not changed. There is no physical TECHO5 or Spotify credential available for integration tests.
The following remain **unvalidated**, and no pass is implied by the version smoke test:

| Test | Required evidence before shipping |
| --- | --- |
| Fresh install / upgrade | Required runtime installed; one receiver only; unrelated settings unchanged. |
| Missing / invalid key | No launch without key; real Spotify rejection shown without leaking credentials. |
| Startup / discovery | Accept key, select device on LAN, verify mDNS and listening ports through firewall. |
| Audio / controls | PCM through TECHO5 mixer; pause/resume/next/previous; both volume directions, caps, ducking, alarms and Bluetooth output. |
| Reboot / persistence | Same key, name and session restored across reboot and both kinds of TECHO5 update. |
| Network interruption / crash | Reconnect, bounded retry, no orphan child, no busy loop or runaway cache. |
| Automatic / expired update | CDN change and exit 10 recover; same credentials/session and correct restart. |
| Failed update | Partial/invalid/incompatible download preserves running binary; retry and user-visible error; no false success. |
| Shutdown / Remove / reset | Children reaped, announcements gone, intended credentials removed, unrelated state retained. |

The migration must remain incomplete until the runtime and audio requirements above are satisfied.
