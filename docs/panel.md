# TECHO5 Panel: Android tablets and the Meta Portal

TECHO5 Panel turns an Android tablet or a Meta Portal into a TECHO5 device. It's an ordinary
Android app with this repo's daemon inside, so no unlock and no root. Home Assistant adds it the way
it adds an Echo running TECHO5 (the ESPHome API), and it has the same wake words, screens, timers,
alarms, intercom and announcements as the Echo Shows.

The app, its install guide and its releases are in
[HuskerMinion/techo5-panel](https://github.com/HuskerMinion/techo5-panel). Start there.

Tested on:

| Device | Android |
|---|---|
| Amazon Fire HD 8 (10th gen) | Fire OS 7 |
| Samsung Galaxy Tab S6 Lite | Android 14 |
| Meta Portal (2nd gen) | Android 10 |

Other ARM tablets with Android 8.0 or later may work. They haven't been tried yet, so please say how
it goes in an issue on the app's repo.

## What's different from an Echo

The daemon is the same one, built with one more device type, so a fix lands on every device. What
Android does for the app instead:

- **Microphone and speaker.** The app records and plays through Android and passes the sound to the
  daemon over local sockets. Echo cancellation runs in the daemon, against what it played.
- **Volume.** The tablet's own volume: the daemon's volume steps set Android's media volume, so the
  tablet's buttons, its slider and Home Assistant all move the same level. Mute stays in the daemon,
  so Android's volume never goes to zero, and an alarm, a timer or a ring raises Android's volume to
  its own level while it sounds and puts it back after. A muted or quiet panel still rings.
- **Screen and touch.** The daemon draws its own screens and the app shows them, scaled to the
  tablet.
- **Updates.** The app updates itself, daemon and all, from its own releases. There's no Stable or Dev
  channel and no A/B slots on a panel. The update card in Home Assistant shows the app's version.
  Checking only looks; Install asks the app to install. With **Install updates automatically** on,
  it installs by itself in the same night hours as an Echo (2 to 5 am), when nothing is playing.
- **What it leaves out.** No LED ring and no camera of its own: a tablet on the Show 5's chip (the
  8th gen Fire HD 8) simply has no camera, rather than trying the Show's and failing. No button
  events: the app passes on no keys, so the four button event entities aren't there. No Bluetooth
  at all: no Bluetooth proxy and no Bluetooth speaker or phone (Android keeps the radio). No video player, live camera
  video or camera sound (the Echos' video decoder isn't in the app; cameras show Home Assistant's
  snapshots). No AirPlay or Spotify Connect, no remote adb, and no firewall of its own. A call to a
  SIP account rings only when it comes from the provider.
- **`.local` names.** A Show looks up a name like `homeassistant.local` itself. A panel leaves
  names to Android, so a `.local` address works only where Android itself resolves it, which older
  versions don't. Home Assistant's IP address always works.
- **Microphone mute.** The daemon's own, as on the Spot: while it's on, what the app records is handed
  on as silence, so nothing is heard, kept or sent. Android still shows the microphone in use.
- **Microphone silenced.** Android can take the microphone away from an app (its privacy switch,
  another app). When the app has heard nothing but silence for 20 seconds, Home Assistant shows a
  Microphone silenced problem, which clears as soon as there's sound again.

## The panel build

For building the APK yourself, the app repo's
[docs/building.md](https://github.com/HuskerMinion/techo5-panel/blob/main/docs/building.md) has the
steps. It builds the daemon from a checkout of this repo. What that build is:

- **The `panel` Go build tag**, the Show's build with the hardware swapped out
  (`echod/internal/layout/device_panel.go` and the `_panel.go` files beside the Echos' ones).
- **GOOS=android, with the Android NDK's clang**, for `arm64` and for `arm` (GOARM=7), at API 26.
  A plain linux build does not work inside an app: Android's seccomp filter kills it the first time
  it starts a process (Go probes `pidfd_open`), and only GOOS=android steps around that.
- **Inside the APK** as `lib/arm64-v8a/libechod.so` and `lib/armeabi-v7a/libechod.so`, with the
  version stamped through `layout.Version`.
- **The app's sockets.** The daemon dials four abstract sockets the app serves: `@techo5.mic`,
  `@techo5.spk`, `@techo5.screen` and `@techo5.ctl`. Both sides check the other runs as the app's own
  user id, so a socket another app took first is refused.
- **Files from the app.** Android hides the network interfaces from apps, so the app writes them to
  `files/net`; the wake word models come in the APK and go to `files/models`.
- **`panel_debug`** adds the debug tools for a debug APK only (an echo path recorder for measuring).
  Never in a release.

Its tests run on a PC like the Echos' (`go test -tags panel ./...`, GOOS=linux). A `panel-vX.Y.Z`
tag has CI build and attest both libraries, like the Echos' binaries, and an app release carries
those two as they are.
