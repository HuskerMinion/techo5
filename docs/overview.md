# TECHO5 in one page

What the device does, how it is built, and where to look. The porting plan is the history; this
is the state.

## What a TECHO5 is

An Amazon Echo Show 5 (MediaTek MT8163, "cronos") running our own Linux image instead of
Android, with one Go daemon, `echod`, doing everything the device does:

- **Voice satellite for Home Assistant** over the ESPHome API: local wake word (microWakeWord on
  the CPU), streaming to Home Assistant's pipeline, spoken replies, echo cancellation so the
  wake word works over music. It sounds like Home Assistant's own voice satellites: their recorded
  sounds for a wake word, muting and a finished timer are the defaults, and TECHO5's own notes are a
  choice (the Wake sound setting, and Home Assistant sounds under Sound & Voice).
- **Screen**: clock and weather, the conversation as it happens, a now-playing page with song and
  artwork, a forecast page and a rain radar map, live views of Home Assistant cameras and of the Show's own camera, and
  a swipe-down settings screen with its categories down the left (Display, Sound, Alarms,
  Connections, Privacy, General) and each one's settings on a card beside them, a drawer in from the
  right edge with Cameras and Radio, and Wi-Fi setup with an on-screen keyboard. A first-run card
  shows once.
- **Timers and alarms**: voice timers from Home Assistant count down under the clock; alarms are set
  on the settings screen (Alarms) or with the `alarm_set` action, or followed from Home Assistant `input_datetime`
  helpers (`alarms_follow`), and ring from the device's own clock even when Home Assistant is down.
  A ringing timer or alarm takes the whole screen, lights a dark one, and offers Stop and Snooze
  (9 minutes unless changed); the alarm sound (Home Assistant, Beeps, Chimes, Bells, Gentle or Pulse) and the snooze
  length are set on the Alarms card or in Home Assistant; the stop word, the action button and Home Assistant's Stop/Snooze buttons work too.
- **Weather**: a new device shows Home Assistant's own forecast (`weather.forecast_home`), or, when
  Home Assistant has none, the first weather entity it lists; any other
  weather entity is chosen on the settings screen (General, Weather), with the "Weather source"
  select, or with the `home_weather` action, and stays chosen. The forecast page (after a weather
  question, or Show beside Weather) has a Radar button: the rain map centered on Home Assistant's home
  zone, the last hour as a loop, over NASA's Blue Marble with a GOES satellite's clouds and the towns
  named. The radar comes from the National Weather Service in the lower 48 and RainViewer anywhere
  else; the "Radar source" setting (settings screen, or the `radar_source` select) can pick either
  always. "Show the radar" asks for it directly.
- **Weather alerts** (U.S.): the National Weather Service's alerts in force at home put a badge on the
  clock (a pill on the Spot) and pills over the rain map, with the alerts' storm polygons and counties
  outlined on it; a tap opens the alert's whole text, swiped sideways to the next.
- **Radio**: the drawer's Radio side lists the stations, with a choice of list at the top. Favorites are the stations wired with
  `home_radio` (Home Assistant `input_select` lists played through a script). With a Home Assistant
  token, Local stations (within 100 km of home) and Popular worldwide come from Home Assistant's
  Radio Browser integration and play through `media_player.play_media` on the device's own player,
  with no setup. Stations play on the device's own speaker or on Bluetooth earbuds; song, artist and
  cover come from iHeartRadio or TuneIn behind the page.
- **Camera**: the front camera as a Home Assistant camera entity (plus JPEG and MJPEG over HTTP
  when switched on). Auto-exposure meters a center-weighted zone grid, so a window behind somebody
  no longer sets it, and the tone curve finds black and lifts the middle of a backlit frame. Off
  unless something is looking, and off while the mute button is engaged.
- **Phone calls** through a SIP provider (TLS and SRTP): placed from Home Assistant (`phone_call`) or
  by voice through an automation, answered on the call page, device to device calls, an
  `esphome.techo5_phone` event per step. Off until `phone_account` signs the device in. See
  [phone.md](phone.md).
- **Bluetooth audio** to earbuds or a speaker; a Bluetooth proxy for Home Assistant (scanning
  through BlueZ on the Show), off by default.
- **Security**: nothing is open to the network by default except Home Assistant's encrypted link
  and the Sendspin player. SSH (keys only), the camera page and the screen page each have a switch
  on the settings screen (Privacy) and in Home Assistant, all off on a new device. SSH keys come only from Home
  Assistant (`ssh_keys` action) and live on userdata; the image carries none.
- **Time zone**: taken from Home Assistant on each connection (the POSIX rule it sends ESPHome devices)
  and kept on userdata, so the images carry none and a unit keeps its zone while Home Assistant is
  away; a new unit is on UTC until its first connection.
- **Updates**: two root filesystem slots with a trial and automatic fallback; a release that
  carries a rootfs tarball installs over the air from Home Assistant's update entity.

## How it is built

| Layer | What | Where |
|---|---|---|
| Bootloader | Amazon's LK, patched for our logo and unlocked with the amonet/kaeru chain | `tools/linux/patch-lk-logo.py`, `docs/hardware.md` |
| Kernel | LineageOS cronos 4.9 rebuilt with Bluetooth and MODVERSIONS, vendor Wi-Fi/BT modules | `tools/linux/build-kernel.sh`, `patch-dtb.py`, `build-image.sh` |
| Root filesystem | Alpine armv7 plus our overlay, built in WSL, two slots on the userdata partition | `tools/linux/mkrootfs.sh`, `wsl-build.sh`, `deploy-rootfs.sh`, `rootfs/` overlay, `slotctl` |
| Daemon | Go, one binary, features as components registered at init | `echod/` |
| Audio | ALSA directly on the vendor PCM devices; MAX98396 amplifier controls; echo canceller in `hardware/mic` | `echod/internal/hardware/{speaker,mic}` |
| Screen | Framebuffer, our own renderer, touch from the input device | `echod/internal/hardware/{screen,touch}`, `feature/display` |
| Camera | Sensor interface, CSI-2 receiver, ISP timing generator and DMA programmed from userspace through the ISP driver's register windows | `echod/internal/hardware/camera`, `docs/camera-research.md` |
| Bluetooth | Vendor `/dev/stpbt` bridged to `/dev/vhci` by `cmd/btbridge`; BlueZ and bluez-alsa | `cmd/btbridge`, `echod/internal/lib/{bluez,bluealsa}`, `feature/btaudio` |
| Home Assistant | ESPHome device API (go-esphome-device), state subscription, device actions, REST with a long-lived token for weather and camera proxies | `feature/api`, `feature/hastate`, `feature/home`, `lib/hass` |
| Updates | Manifest with binaries and a rootfs; slot devices install through `slotctl` | `echod/internal/update`, `tools/release.ps1`, `echod/cmd/mkmanifest` |

## Working on it

- **Build the daemon**: `GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build ./cmd/echod` in
  `echod/`. `-tags dot` builds the Echo Dot variant.
- **Get in**: send your public key with the `esphome.<device>_ssh_keys` action and turn on the
  SSH switch (Home Assistant, or Privacy on the settings screen). Switching it off stops the listener; open
  sessions stay.
- **Try a build on the bench**: copy the binary over SSH to `/usr/local/bin/techo5` (remount `/`
  read-write first) and kill the running daemon; the supervisor restarts it. `deploy-rootfs.sh`
  is the real path: it builds a rootfs and installs it into the spare slot, on trial.
- **See the screen from the PC**: turn on Screen web access, then `http://<device>:8181/screen.png` with `?sheet=<category>`
  (or `cameras`/`radio` for the drawer), `?list=<row>` for a row's list of choices, `?theme=<name>`, `?radio=<station>`, `?wifi=list|keyboard` to put pages up first.
  The plain screenshot needs only the switch; the options that put those pages up change what the
  device is doing, so they need a browser the setup page has let in — open the setup page in the same
  browser and press the button on the device first, or they answer 403.
- **Logs**: `/data/techo5-linux/techo5.log` on the device (`echod.log` on the Dot); `dmesg` for the
  kernel.
- **Slots**: `slotctl status`, `slotctl install <tar.gz>`, `slotctl switch <a|b>`; a trial slot
  commits after five minutes of a healthy daemon.
- **Release**: `tools/release.ps1 -Version vX.Y.Z -Notes "..." -Rootfs <tarball> [-Boot <image>]` builds the
  daemon, writes the manifest, and publishes the release Home Assistant will offer. `-Boot` attaches a
  boot image built with `build-image.sh --no-key` for new units, and refuses one that carries a key.

## Known gaps

- The Bluetooth proxy on the Show only scans: BlueZ owns the controller, so there is no beacon
  and no active connections for Home Assistant. It stays off by default until it has run a while.
- The camera and screen pages have no login: they are for a trusted network, and off until
  switched on. The rescue environment (boot image) still trusts the key built into that image.
- Song metadata rests on two undocumented service endpoints; when one changes shape the page
  falls back to the station logo.
- Exposure knows the middle of the frame, not faces: somebody off to one side of a window is still
  metered as the edge.
- Camera stills can show a faint horizontal seam near the top edge; not yet investigated.
- The Wi-Fi keyboard offers letters, digits and common symbols; no other alphabets.
