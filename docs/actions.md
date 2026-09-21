# Home Assistant actions

TECHO5 exposes a handful of [ESPHome actions](https://esphome.io/components/api.html#actions) once
a device connects. In Home Assistant they show up under the `esphome` domain, named
`esphome.<node>_<action>` — a device named `office` exposes `esphome.office_alarm_set`, and so on.
The `<node>` prefix throughout this page is whatever name you gave the device when it was added;
substitute your own. Call any of them from **Developer Tools → Actions**, a script, or an
automation.

## Connect the device to Home Assistant

In YAML, refer to this action as `esphome.<node>_home_assistant`.

Stores the URL and a long-lived access token the device uses to call Home Assistant's REST API
directly, separately from the ESPHome connection Home Assistant uses to talk to it. This is what
lets the device fetch things on its own — a weather forecast, a camera snapshot, a media library
listing — rather than only reporting sensors and taking commands. It's normally set once, by
whatever paired the device, and isn't something you call from an automation afterwards.

> **Good to know**
>
> Several other actions depend on this being set first: `home_show_camera` calls Home Assistant
> immediately and fails right away — `hass: no access configured` — if it isn't. `home_weather`,
> `home_cameras`' automatic camera list (when called with no `cameras`), `home_slideshow`, and the
> Radio Browser stations in `home_radio` all need it too, but fetch in the background, so without it
> they simply show or offer nothing rather than raising an error at the time you call them.
>
> The URL must be one the device itself can reach — its local IP address or `homeassistant.local`,
> not an external or Nabu Casa URL — since the device calls it directly rather than through Home
> Assistant's own connection to the device.

### url (Required)

*string*

Home Assistant's base URL, reachable from the device.

### token (Required)

*string*

A long-lived access token, created from your Home Assistant user profile (**Settings → your
profile → Security → Long-lived access tokens**).

```yaml
action: esphome.office_home_assistant
data:
  url: "http://homeassistant.local:8123"
  token: !secret techo5_office_token
```

## Set an alarm

In YAML, refer to this action as `esphome.<node>_alarm_set`.

Sets an alarm on the device, or turns back on the one that already rings at that time, on those
days, with that label. Alarms set this way ring from the device's own clock, even while Home
Assistant is down.

### time (Required)

*string*

A time of day: `7:30`, `07:30`, `19:30:00`, `7:30 pm`, `7:30pm`, `3 pm`, or `3pm`.

### days (Optional)

*string*

Which days it repeats on. One of `once` (the default — rings once and turns itself off), `daily`
(or `every day`, `everyday`), `weekdays`, `weekends`, or a comma- or space-separated list of days,
matched by prefix: `mon,wed,fri`, `tue thu`.

### label (Optional)

*string*

Free text shown on the device's screen and in Home Assistant's next-alarm sensor.

```yaml
action: esphome.office_alarm_set
data:
  time: "7:30 am"
  days: weekdays
  label: Wake up
```

## Delete an alarm

In YAML, refer to this action as `esphome.<node>_alarm_delete`.

Deletes every device alarm at the given time. Fails if none is found.

### time (Required)

*string*

Same formats as `alarm_set`'s `time`.

### label (Optional)

*string*

Only delete alarms at that time whose label matches (case-insensitive). Left blank, every alarm at
that time is deleted regardless of label.

```yaml
action: esphome.office_alarm_delete
data:
  time: "7:30 am"
  label: Wake up
```

## Follow Home Assistant helpers as alarms

In YAML, refer to this action as `esphome.<node>_alarms_follow`.

Wires `input_datetime` helpers to ring as alarms on the device, alongside any set with `alarm_set`.
Each call replaces the whole followed list — to follow several helpers, list them all in one call
rather than calling this action once per helper.

### entities (Required)

*string*

A comma-separated list. Each entry is either a bare entity —

```text
input_datetime.wake
```

— which is always armed whenever the helper has a time set, or an entity paired with a second one
that arms it, written `entity=arm_entity`:

```text
input_datetime.wake=input_boolean.wake_armed
```

Here the alarm only rings while `input_boolean.wake_armed` is anything other than `off`.

```yaml
action: esphome.office_alarms_follow
data:
  entities: input_datetime.wake=input_boolean.wake_armed,input_datetime.weekend_wake
```

## Choose which cameras the device shows

In YAML, refer to this action as `esphome.<node>_home_cameras`.

Sets the list of cameras offered on the device's Cameras page and by voice ("show the front door").

### cameras (Optional)

*string*

A comma-separated list of `entity=Display Name` pairs:

```text
camera.front_door=Front door,camera.deck=Deck
```

The `=Display Name` half can be left off, in which case the entity ID is used with its `camera.`
prefix stripped. Left out entirely, the device instead lists every camera entity Home Assistant
has, refreshed every 10 minutes — this needs `home_assistant` to be set up first.

```yaml
action: esphome.office_home_cameras
data:
  cameras: camera.front_door=Front door,camera.deck=Deck
```

## Show a camera on screen

In YAML, refer to this action as `esphome.<node>_home_show_camera`.

Puts one camera's live view up on the device's screen for a while — for an automation that shows
the front door when the doorbell rings.

> **Good to know**
>
> Needs `home_assistant` set up first — this action fetches the camera's snapshot from Home
> Assistant directly, and fails with `hass: no access configured` otherwise.

### entity (Required)

*string*

The camera entity to show. Does not need to be one of the cameras set with `home_cameras`.

### seconds (Optional)

*integer*

How long to show it for. Defaults to 30 seconds if left out or zero.

```yaml
action: esphome.office_home_show_camera
data:
  entity: camera.front_door
  seconds: 60
```

## Choose the weather shown on the idle screen

In YAML, refer to this action as `esphome.<node>_home_weather`.

Sets which weather forecast the device's clock/idle screen shows. Screen devices only. Needs
`home_assistant` set up first, or no forecast will show.

### entity (Optional)

*string*

A `weather.*` entity to show, `default` (or left blank) for Home Assistant's own forecast
(`weather.forecast_home`, the one set up automatically for the home's location), or `none` to show
no weather at all. Matching is case-insensitive.

```yaml
action: esphome.office_home_weather
data:
  entity: weather.forecast_home
```

## Wire up the radio page

In YAML, refer to this action as `esphome.<node>_home_radio`.

Configures the device's Radio page: which stations it lists, what plays them, and what shows as
"now playing". Screen devices only; the change takes effect the next time the device reconnects.

> **Good to know**
>
> Favorites (`stations`) play through a Home Assistant script over the device's normal API
> connection, not through `home_assistant`. The Radio Browser stations near your home, shown
> alongside your favorites, do use `home_assistant` — without it, that part of the list is empty.

### stations (Optional)

*string*

A comma-separated list of `input_select` entities, each one a station or preset to offer on the
Radio page. Up to 20.

### now (Optional)

*string*

An entity whose state names whatever is currently playing, shown on the Radio page and the idle
screen while it's live.

### service (Optional)

*string*

The script or action that actually plays a station when one is picked on the device.

### field (Optional)

*string*

The name of the argument `service` expects the chosen station in. Defaults to `station` if left
blank.

### speaker_field (Optional)

*string*

The name of the argument `service` expects this device's media player in. Defaults to `speaker` if
left blank.

### speaker (Optional)

*string*

This device's own `media_player.*` entity, passed to `service` in `speaker_field` so the script
knows which speaker to target.

```yaml
action: esphome.office_home_radio
data:
  stations: input_select.office_radio_stations
  now: sensor.office_radio_now_playing
  service: script.play_radio_station
  field: station
  speaker_field: speaker
  speaker: media_player.office
```

## Set the slideshow's photo source

In YAML, refer to this action as `esphome.<node>_home_slideshow`.

Sets the Home Assistant media source the idle-screen slideshow shows photos from — the same setting
as the screen's own folder picker. Screen devices only. Whether the photos show behind the ordinary
clock (Background mode) or take over the whole screen after a wait (Screensaver mode), and options
like shuffle, subfolders and time per photo, are set separately as entities
(`select.<node>_slideshow`, `switch.<node>_slideshow_shuffle`, `number.<node>_slideshow_interval`,
and so on), not by this action. Needs `home_assistant` set up first, or no photos will show.

### source (Required)

*string*

A Home Assistant media source ID — the same ID browsing a media source in Home Assistant returns
for a folder (an Immich album, a network share, or anything else `media_source` can browse), e.g.
`media-source://media_source/local/Photos`. An empty value clears the source, so nothing is shown
whatever mode is selected.

```yaml
action: esphome.office_home_slideshow
data:
  source: "media-source://media_source/local/Photos"
```

## List saved voice recordings

In YAML, refer to this action as `esphome.<node>_recordings`.

Returns the IDs of the voice-turn recordings currently kept on the device, most recent first, so
Home Assistant can offer them for playback. The device keeps 0–10 of them per assistant (set with
the "keep recordings" number entity; off by default). This action answers with data — call it with
a `response_variable` rather than as a plain fire-and-forget action.

Takes no parameters.

```yaml
action: esphome.office_recordings
response_variable: recent
```

## Fetch a saved voice recording

In YAML, refer to this action as `esphome.<node>_turn_audio`.

Fetches one saved recording's audio as base64-encoded WAV, by an ID from `recordings`. A clip larger
than about 32 KiB of base64 comes back split into pages — check the response for how many pages
there are and call again for each one. This action answers with data; call it with a
`response_variable`.

### id (Required)

*string*

One of the recording IDs `recordings` returned.

### page (Optional)

*integer*

Which page of the audio to fetch, starting at 0. Defaults to 0.

```yaml
action: esphome.office_turn_audio
data:
  id: "{{ recent.ids[0] }}"
  page: 0
response_variable: clip
```

## Set the SSH authorized keys

In YAML, refer to this action as `esphome.<node>_ssh_keys`.

Replaces the device's SSH `authorized_keys`, one public key per line. This is the only way keys get
onto the device — nothing is baked into the image, and the on-screen SSH switch can only turn the
server on or off, not add anyone.

> **Good to know**
>
> This needs an API encryption key, not `home_assistant`: it's refused unless Home Assistant's
> *ESPHome* link to the device already has a real encryption key set, since otherwise the keys would
> cross the network in the clear.

### keys (Required)

*string*

Newline-separated public keys, each a full `authorized_keys` line (type, key, optional comment) —
`ssh-ed25519`, `ssh-rsa`, `ecdsa-sha2-nistp256`, `ecdsa-sha2-nistp384`, `ecdsa-sha2-nistp521`,
`sk-ssh-ed25519@openssh.com`, or `sk-ecdsa-sha2-nistp256@openssh.com`. A private key, or any line
that isn't recognized as one of these types, is rejected and none of the keys are changed. An empty
value removes every key and turns SSH off if it's running.

```yaml
action: esphome.office_ssh_keys
data:
  keys: |
    ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample don@torrent
```

## Sign a device in to a SIP account

In YAML, refer to this action as `esphome.<node>_phone_account`.

Signs the device in to a SIP provider so it can place and receive calls. The login is kept on the
device in its own owner-only file — never in the settings Home Assistant or diagnostics can read.

> **Good to know**
>
> This needs an API encryption key, not `home_assistant`: it's refused unless Home Assistant's
> *ESPHome* link to the device already has a real encryption key set, since otherwise the password
> would cross the network in the clear.

### server (Required)

*string*

The provider's SIP server (e.g. a VoIP.ms POP such as `chicago1.voip.ms`).

### username (Required)

*string*

The SIP account's username. An empty value signs the device out and removes the stored login
instead of signing in.

### password (Required)

*string*

The SIP account's password.

```yaml
action: esphome.office_phone_account
data:
  server: chicago1.voip.ms
  username: "100000_office"
  password: !secret techo5_office_sip_password
```

## Place a call

In YAML, refer to this action as `esphome.<node>_phone_call`.

Places a call from the device. Does nothing on its own — this is how an automation or voice command
dials out; the device never calls anyone by itself.

### number (Required)

*string*

A phone number or another SIP account's extension. Spaces, dashes and a leading `+` are dropped
before dialing.

```yaml
action: esphome.office_phone_call
data:
  number: "15551234567"
```

## Set the screen's contact list

In YAML, refer to this action as `esphome.<node>_phone_contacts`.

Sets the contacts a device with a screen offers to call without a voice command, up to 12. The
numbers are kept on the device in their own owner-only file, not in Home Assistant.

### contacts (Required)

*string*

A comma-, newline-, or semicolon-separated list of `Name=number` pairs:

```text
Alex=15551234567, Sam=15557654321, Kitchen=106
```

An empty value clears the list.

```yaml
action: esphome.office_phone_contacts
data:
  contacts: "Alex=15551234567, Kitchen=106"
```

## Answer a call

In YAML, refer to this action as `esphome.<node>_phone_answer`.

Answers the device's currently ringing call, the same as pressing its action button, tapping its
screen, or using the **Answer call** button Home Assistant shows while it rings.

Takes no parameters.

```yaml
action: esphome.office_phone_answer
```

## Hang up

In YAML, refer to this action as `esphome.<node>_phone_hangup`.

Ends the device's call, whether it's ringing (incoming or outgoing) or already up — the same as
pressing its action button again, the **Hang up** / **Decline** button, or a swipe or tap on its
screen.

Takes no parameters.

```yaml
action: esphome.office_phone_hangup
```

## Announce to the house

In YAML, refer to this action as `esphome.<node>_announce_house`.

Says something on every other TECHO5 device in the house. What it does depends on whether you
give it words:

- **With `text`**, it speaks those words. Devices with a screen show them; a Dot chimes and stays
  quiet, since it has no way to read them aloud.
- **With no `text`**, it opens the device's microphone, plays a tone, and sends what is said as
  audio. That is the announcement people mean: your own voice in every room, recorded on one
  device and played on the others.

The recording ends when you stop talking, or at fifteen seconds, or when somebody taps the screen
of the device that is listening. One that nobody spoke into is dropped rather than sent as a chime
and silence.

> **Good to know**
>
> Announcements go device to device over the local network, not through Home Assistant, so this
> action is a way to start one rather than a route the audio takes. They keep working with Home
> Assistant switched off; this action is simply not available then.
>
> Every device needs the same **house word** set on its setup page. A device with no word set
> neither sends announcements nor takes them, and this action will say so in the log rather than
> failing.
>
> Quiet hours are respected: inside them an announcement is shown and not sounded. Alarms, timers
> and calls are not announcements and are not affected.

### text (Optional)

*string*

What to say. Leave it out, or pass an empty string, to record instead.

```yaml
action: esphome.office_announce_house
data:
  text: "dinner is ready"
```

```yaml
# No text: opens the microphone and sends what is said.
action: esphome.office_announce_house
data:
  text: ""
```
