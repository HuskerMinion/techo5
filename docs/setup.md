# Setting it up

What to set up once TECHO5 is installed and the device is in Home Assistant, in the order that
works. [Getting started](getting-started.md) gets you to that point. Everything here can be changed
later, and most of it is optional.

Photos, weather, cameras, the screen and the settings lock are for devices with a screen: the Show
and the Spot. On a Dot, steps 1, 5 and 7 apply.

In the examples the device is named `office`. Use your own device's name: the actions are
`esphome.<name>_...` and the entities `switch.<name>_...` and so on.

## 1. Give the device access to Home Assistant

Do this first. Photos, cameras, weather and the radio lists all need it. The device fetches them from
Home Assistant itself, so it needs a token of its own. The ESPHome connection Home Assistant uses to
talk to the device is not enough for this.

1. In Home Assistant, open your profile (bottom left), then **Security**, and under **Long-lived
   access tokens** create one. Copy it; it is shown only once.
2. In **Developer Tools → Actions**, switch to YAML mode and run:

   ```yaml
   action: esphome.office_home_assistant
   data:
     url: "http://192.168.1.10:8123"
     token: "paste the token here"
   ```

Use Home Assistant's local IP address, as above with your own. The device can't look up `.local`
names like `homeassistant.local`, and an external or Nabu Casa address does not work either.

If a photo folder says **Couldn't open this folder**, or a camera shows `hass: no access configured`,
this step is missing or the address is wrong.

Also turn on **Allow the device to perform Home Assistant actions** on the device's ESPHome entry
(Configure), as in [Getting started](getting-started.md#after-installing-every-device).

## 2. Photos

The slideshow shows photos from Home Assistant's media library. Nothing is stored on the device.

1. **Put the photos where Home Assistant's Media page can see them.** The simplest is a folder in
   Home Assistant's media folder:
   - The **Samba share** add-on shows it as its own share called `media`.
   - The **Terminal & SSH** and **Studio Code Server** add-ons show it as `/media`.
   - Make a folder there, `photos` for example, and copy the pictures in.

   The **File editor** add-on only sees the config folder. A `media` folder made there is not
   Home Assistant's media folder, and the photos will not show up.

   Anything else the Media page can browse works too, such as an Immich album or a network share.
2. **Check** that the photos show in Home Assistant under **Media → My media**. If Home Assistant
   can see them, the device can too.
3. **On the device**, swipe down for Settings, then **Display**:
   - **Slideshow**: **Background** puts the photos behind the clock. **Screensaver** takes over the
     whole screen after a while with nothing happening.
   - **Photo folder**: pick the folder, then **Use this folder**.
   - **Time per photo**, **Shuffle photos** and **Include subfolders** are optional.
   - **Show whole photo** shows each photo in full, with a blurred copy of it filling the sides.
     Off, photos fill the screen, which crops tall ones.

The same settings are entities in Home Assistant, and the folder can be set with the
[`home_slideshow` action](actions.md#set-the-slideshows-photo-source).

## 3. Weather

With access set up (step 1), the clock shows Home Assistant's own forecast for your home with no
setup at all. To use a different `weather.*` entity, or none, use
[`home_weather`](actions.md#choose-the-weather-shown-on-the-idle-screen). Settings → Display also
has **Weather animation**, **Radar source** and **Weather alerts** (alerts are U.S. only, from the
National Weather Service).

## 4. Cameras

With access set up, the Cameras drawer (swipe in from the right edge) lists every camera Home
Assistant has. To show only some, with friendlier names, use
[`home_cameras`](actions.md#choose-which-cameras-the-device-shows).

To show a camera when something happens, like the doorbell, call
[`home_show_camera`](actions.md#show-a-camera-on-screen) from an automation:

```yaml
action: esphome.office_home_show_camera
data:
  entity: camera.front_door
  seconds: 60
```

### Talking through a camera

On the Show and the Spot, **Talk** on the camera page sends the device's microphones to the camera's
own speaker, for answering the door from the kitchen. It goes straight to the camera over its RTSP
stream (ONVIF two-way audio), so Home Assistant, go2rtc and Frigate are not needed for it. It works
with cameras that have a speaker and take G.711 audio, which includes most Reolinks.

1. Turn on **Talk through cameras** under Settings → Privacy & Security (**Talk to cameras** on the
   Spot), or its switch in Home Assistant. It is off on a new device.
2. Cameras on a Reolink recorder set up on the device (setup page → Connections → Reolink cameras)
   need nothing more, as long as RTSP is turned on in the recorder's network settings. For any other
   camera, give its RTSP address under **Talk through cameras** on the same tab, with the cameras'
   login. For a Reolink camera that is `rtsp://<address>:554/h264Preview_01_main`. A camera left
   empty gets no Talk. The login is only sent protected (digest); a camera that asks for it in the
   clear is refused.

A tap on **Talk** starts it and another ends it. While it runs the button is red and counts down, the
view stays up, and neither the wake word nor the action button starts a question; a press of the
action button ends the talk. Music and radio are turned down while it runs, and the screen stays lit.
The microphones go out 12 dB quieter than they are for the voice assistant, since a doorbell's small
speaker distorts at that level and its own microphone picks it back up.
The room is only sent while the camera page is on the screen, so the talk also ends when the view
closes or anything covers it (a call, a ring, an announcement, the settings), when the microphones
are muted, the switch goes off, the camera hangs up (a camera's own app taking its speaker does
that), or after two minutes. If the camera will not take it, the page says why for a few seconds:
a camera on the recorder that has no speaker shows Talk too, and says it has no talk-back channel.

Cameras read straight from a Reolink recorder have no sound on the device, so for those Talk is
one-way: you are heard at the door, but the visitor is not heard on the device.

## 5. Music

- **Music Assistant.** Each device is a Sendspin player, on from the first boot. Music Assistant
  finds it on the network with nothing to set up. Several devices can play in sync as a group.
  Read [what this trusts](getting-started.md#after-installing-every-device) first if your Wi-Fi has
  guests on it. **Music Assistant player** under Settings → Sound turns it off.
- **Music by voice, and any station.** With a Music Assistant set up on the device (setup page →
  Sound → Music Assistant), "play some Eagles" plays from it, and a station in a format the device
  cannot play itself (most commercial radio streams) is played through it instead. The setup page
  says whether Music Assistant is reachable and the device connected to it. A device farther away,
  over a VPN, connects to Music Assistant itself; it needs to reach ports 8095 and 8927 on its host.
  A station asked for by name or frequency is looked for near the device first. **Music source**,
  under the same section, picks a service to search first (YouTube Music, Spotify, a folder of files);
  the rest of the library is searched only when it has nothing by that name. Naming the service picks
  one for that request: "play Taylor Swift on YouTube Music". This is for a device whose voice
  assistant answers directly; under Home Assistant, its own Music Assistant support decides. It needs
  Music Assistant 2.10 or later.
- **AirPlay and Spotify Connect (new).** On the Show and the Dot, two switches make the device a
  speaker other apps play to, under its own name: **AirPlay** from an iPhone, iPad or Mac, and
  **Spotify Connect** from the Spotify app (Spotify Premium). Both are off until turned on, under
  Settings → Sound, on the setup page (Sound & Voice), or in Home Assistant. What they play shows as
  now playing, with the song's cover from Spotify. The Spotify app's volume slider turns the device's
  volume up and down by as much as it moves; the device's own buttons do not move the app's slider. A
  pause on the device stops the stream there; the phone keeps going until it is paused too. Anyone on
  the same network can play to the device while one is on, as with any AirPlay or Spotify speaker, and
  Spotify Connect keeps the login a phone hands it until it is turned off. Spotify Connect has been
  tried on a Show; AirPlay has not been tried with an iPhone yet. If something does not work, open an
  issue.
- **DLNA (new).** On every device, the **DLNA** switch (Settings → Sound, the setup page's Play to
  this device box, or Home Assistant) makes the device a DLNA speaker, a "media renderer", under its
  own name. Music apps and servers on the network find it and play to it: BubbleUPnP on a phone,
  Jellyfin, Plex or Emby, a NAS's music app, foobar2000, Windows' Cast to device. It plays MP3, FLAC
  and WAV; most apps and servers convert anything else when asked. The song's name, artist and cover
  show as now playing, the app's volume slider sets the device's volume, and play, pause and stop go
  both ways. Skipping within a song is not supported yet. It is off until turned on: anyone on the
  same network can play to it while it is on, as with any DLNA speaker, and can also ask what it is
  playing, including the song's address (some servers put a login token in it, as with any DLNA
  speaker). With **Video** and **DLNA video** on as well (Show and Spot), it takes videos too
  ([Video](video.md)).
- **Radio.** The Radio drawer and its favorites are set with
  [the radio actions](actions.md#wire-up-the-radio-page). While a station plays, the **Radio station**,
  **Radio artist** and **Radio title** sensors say what's on (the artist and title when the station's
  service reports them), for an automation or a dashboard to use.
- **Now playing follows** (also the **Now Playing follows** select in Home Assistant): another of Home
  Assistant's media players, a Sonos in the same room for example. While this device plays nothing of
  its own, Now Playing shows that player's song and cover, and its buttons control it. **Done** puts it
  away until the next song without stopping it. Needs the home_assistant action (address and token).
  On the Spot, a swipe across a followed player's page skips to the next or previous song. **Stop**
  on a followed page puts it away and leaves the other player playing.
- **Lyrics** (Show, also a switch in Home Assistant, off by default): the words of the song on Now
  Playing, the line being sung and the next one, in time with the music. They come from
  [LRCLIB](https://lrclib.net), a free lyrics database, so the song's title and artist are sent there.
  Words are only kept in time for music whose position is known: Music Assistant, DLNA when the app
  says how long the song is, and a followed player. Radio stations, AirPlay and Spotify show the song
  without words.

## 6. The screen

These are on the Show under Settings → **Display**, and are entities in Home Assistant. Most are on
the Spot too, in its settings.

### Night

- **Night hours**: when the screen dims by itself. **At night** picks dark, a faint glow, or a clock
  alone. During the night only a call wakes the screen.
- **Night mode** switch in Home Assistant: start or end the night now, from a bedtime automation for
  example. Set Night hours to **Controlled by Home Assistant** to leave the night to the switch alone.
  See [Turning the night on from an automation](actions.md#turning-the-night-on-from-an-automation).

### Clock and home screen

- **Clock format**, **Clock position** (center, or a smaller clock in a bottom corner so a photo
  stays in view) and **Date color**.
- **Clock style**: how the clock looks all day. *Classic*, *Big*, *Flip*, *LED*, *Analog*, *Words*
  (the time in words), *Sun* (the sun's path from sunrise to sunset), *Dashboard* (the next events
  and the week's weather), *Binary* (the time in lights, one column per digit, counted 1, 2, 4, 8
  from the bottom), *World* (the time here and in other places), *Agenda* (today's and tomorrow's
  events beside the time) and *Glow* (soft colors drifting behind the time). Swipe left or right
  across the clock for the next style or the one before; its name shows for a moment, and a swipe
  the other way puts the last style back. The night clock keeps its own look. Also in Home Assistant
  and on the setup page's Screen & Photos tab.
- **Swipe between clock styles**: on by default. Turn it off (setup page, Screen & Photos, or the
  switch in Home Assistant) so a swipe across the clock leaves its style alone. It's off whenever
  **Tap on the clock** is *Nothing*, too.
- **World clock places** (setup page, Screen & Photos tab): the World style's places, as time zone
  names with commas between them, like `America/Chicago, Europe/Paris, Asia/Tokyo`. Up to three; the
  Spot shows the first two. Leave it empty for New York, London and Tokyo.
- **Theme**, **Answer time** and **Now playing** (the full page, or a strip over the clock).
- **Tap on the clock** (Show): *Assist*, as it always was, *Dashboard* to open the dashboard, *Deck*
  to open TECHO5 Deck (until a deck has buttons, a tap still starts Assist), or *Nothing*, for a panel
  you only talk to. A voice request under way still
  takes the tap. Also on the setup page's Screen & Photos tab. See [Dashboards](dashboards.md) for the
  rest of a panel setup.
- **TECHO5 Deck** (Show): pages of buttons for OBS Studio, opened with a swipe up from the bottom
  edge of the clock. Set up on the setup page's Screen & Photos tab. See [TECHO5 Deck](deck.md).
- **Video** (Show and Spot): videos full screen from Home Assistant's `play_video` action and, with **DLNA
  video** on, from DLNA apps. Off on a new device. On the setup page's Screen & Photos tab. See
  [Video](video.md).
- **Screen language** (Settings → General): the clock's day and date, the forecast's days, the
  weather's words and the alarm after the date are written in it: German, Spanish, French, Italian or
  Dutch, and English for *Match all* or *English*. It also picks which words the screen listens for.
  It doesn't change what the assistant understands or says, and the rest of the screen stays in
  English.
- **Turn screen**: how a voice request looks. **Classic** is the words, **Wave** is glowing lines and
  **Bars** is an LED-style equalizer, both moving with the voice. This one is on the Spot too.
- **Subtle mute ring** (Spot, in Home Assistant): the red ring shown while the microphones are muted
  is drawn thin and in a dimmer red, so it doesn't light up a dark room.

### Presence and gestures

- **Presence detection** (Show and Spot, off by default; Settings → Display → Presence, or the
  **Presence detection** switch in Home Assistant): the camera notices somebody moving near the device.
  Home Assistant gets a **Presence** sensor (occupancy) for automations, and **Screen off when nobody
  is near** (5 minutes to start with, or Never) puts the screen out once the room has been empty that
  long and lights it again when somebody comes near. Never at night, and never during a conversation,
  a call, an alarm or with the settings open. Nothing the camera sees is kept or sent: a couple of
  times a second the newest frame is compared with the last as a small grid of brightness, on the
  device. The mute button and the lens shutter stop it, and then the screen is left as it is. On a
  Show 8 or 1st gen Show 5 with a boot image older than v1.0.1, a quick tap of the mute button to
  unmute leaves the camera switched off inside Amazon's kernel: **hold the mute button for a second**
  (it chimes and stays unmuted) and the camera, presence and camera stills come back, or restart.
  The v1.0.1 boot image fixes it ([updating the boot image](install.md#updating-the-boot-image)); with
  it, holding the button mutes like a tap. The 2nd gen Show 5 is not affected.
- **Gestures** (Show and Spot, experimental, off by default; the **Gestures** switch in Home
  Assistant): cover the camera with your palm, hand on or almost on the lens, for half a second to
  three seconds. It stops a ringing alarm or timer, and Home Assistant gets an `esphome.techo5_gesture`
  event (`gesture: cover`, `device`) for automations. A hand held a few inches away is not counted:
  the camera cannot tell it from somebody leaning in. While Gestures is on the camera looks eight
  times a second, which costs a little more than presence alone.

## 7. Voice

Under Settings → **Sound**, or on the device's Assist satellite in Home Assistant:

- **Wake word**: Okay Nabu, Hey Jarvis, Alexa and others. Choosing none is fine if you only want
  the screen and music.
- **Wake word sensitivity**: raise it if the device wakes by mistake.
- **Quiet hours** and **Do not disturb**.

On the setup page (Sound & Voice) or in Home Assistant, **Night volume** turns the device down to that
level as quiet hours start, if it is louder, and back to where it was as they end. Turned up or down
during the night, it stays where it was put. Changing Night volume during the night moves the device to
the new level, and 0 (no night volume) puts it straight back. A muted device stays muted. Alarms and
timers keep their own **Ring volume**. The Spotify and Music Assistant volume sliders do not move when
night volume turns the device down.

### A wake word of your own

Any microWakeWord model works, from a collection like
[TaterTotterson/microWakeWords](https://github.com/TaterTotterson/microWakeWords) or one you train
yourself. Each comes as two files, a `.json` and a `.tflite` with the same name (for example
`echo.json` and `echo.tflite`).

1. Put both files in Home Assistant's `custom_wake_words` folder, inside its config folder (next
   to `configuration.yaml`). Make the folder if it isn't there.
2. Restart Home Assistant. It reads the folder once and keeps what it found, so a word added later
   only shows up after a restart; reloading the device's ESPHome entry is not enough.
3. Pick the new wake word in the device's **Wake word** list, on the Assist satellite in Home
   Assistant. The device downloads it from Home Assistant and keeps it.

### Sounds of your own

The sounds a voice request makes can be recordings of your own. Put a WAVE file named for the sound
in the device's sounds folder, over SSH
([Set the SSH authorized keys](actions.md#set-the-ssh-authorized-keys)). The folder is
`/data/misc/echolocal/sounds` on an Echo Dot, and `/data/misc/techo5/sounds` on everything else.

| File | Plays in place of |
|---|---|
| `wake_word_triggered.wav` | the wake sound, and a follow-up's, where **Wake sound** is Home Assistant |
| `failure.wav` | the falling notes when a request cannot be served |
| `canceled.wav` | the notes when a request is dropped |
| `timer_finished.wav` | a finished timer, and the Home Assistant alarm sound |
| `mute_switch_on.wav`, `mute_switch_off.wav` | muting and unmuting the microphones |

The timer and mute files play where **Home Assistant sounds** is on, and an alarm set to the
Home Assistant sound plays `timer_finished.wav` either way. A file has to be 16-bit, at
48 kHz, mono or stereo, and at most 10 seconds long. It plays at the level it was recorded at, as the
stock sounds do. Keep the wake sound short: the device listens for the request only once the wake
sound fades, so a long one cuts off the first words. ffmpeg converts anything else:

```sh
ffmpeg -i chime.flac -ac 1 -ar 48000 -c:a pcm_s16le wake_word_triggered.wav
```

A new or changed file plays the next time its sound does, with no restart, and deleting it brings the
stock sound back. A file the device cannot play leaves the stock sound playing, and the log says why.

Alarms and timers work by voice, on the screen, and from Home Assistant. See
[docs/actions.md](actions.md) for all of them.

## 8. Settings lock

A **settings lock** (Show and Spot, off by default) is a PIN the device asks for before its settings
open, so guests and children can use everything else without changing anything. Set it under
Settings → Privacy & Security, on the setup page (Privacy), or with the
[settings_lock_pin action](actions.md#set-the-settings-locks-pin). Five wrong tries in a row make
the device wait before it takes another. The **Settings lock** switch in Home Assistant shows
whether it is on; turning it off removes the PIN.

While the lock is on, the PIN is also asked for when Home Assistant opens the settings, and when
**Allow** is pressed on the setup page.

## More

- [Dashboards](dashboards.md): Home Assistant dashboards on the screen.
- [Phone calls](phone.md) through your own SIP provider.
- [Actions](actions.md): everything Home Assistant can ask the device to do, with examples.

## Questions and problems

Open an [issue on GitHub](https://github.com/HuskerMinion/techo5/issues). Say which device and
version (Settings → General → Updates shows it), what you did and what happened. Answers there help the next person
too.
