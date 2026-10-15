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

   **Immich without Home Assistant.** The device can read an Immich server itself. On the setup page,
   **Screen & Photos**, **Photos from Immich**: the server's address (like `http://192.168.1.20:2283`)
   and an API key made in Immich under **Account Settings > API Keys**, then **Save**. Pick **The whole
   library** or an album under **Photos to show**, then **Show these photos**. The key stays on the
   device and the page never shows it again; **Forget this server** removes it. Once set up, Immich and
   its albums are in the device's own **Photo folder** list too. Photos only, no videos; each is shown
   as Immich's preview of it.
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

With **Live camera video** on (a switch of the device's in Home Assistant, under its configuration;
off on a new device), a camera Home Assistant can stream (one that plays live in its own dashboard,
from an RTSP camera or an integration with a stream) is shown as live video, decoded on the device from
Home Assistant's HLS stream. Snapshots keep coming until the first moving frame, which takes a few
seconds, more when Home Assistant still has to start the camera's stream. The picture is a few seconds
behind the camera: HLS comes in parts a keyframe interval long. Other cameras, and any whose stream
fails or that the device cannot decode (H.265, or a stream over 1080p), are shown from snapshots, a few
a second, as with the switch off. A streamed camera's sound, when the view has one, comes from the same
stream, decoded on the device: as far behind the camera as the picture, where the stream converted by
Home Assistant and sent back was some twenty seconds. A stream with no sound in it falls back to Home
Assistant's way. A camera's sub stream (often called *fluent* or *sub*) is the better choice: it is
sharper than the panel needs anyway, starts sooner and costs less.

Where the image's decoder reads RTSP, a camera given an RTSP address for talking through it (below) is
heard from that address instead: live, a word said at the device comes back out of it through a
doorbell's own microphone in two to three seconds, against eight from the stream. A camera's picture
can be read from its own address too, beside the talking one on the setup page: give it a stream about
the screen's size (for a Reolink, `rtsp://<address>:554/h264Preview_01_sub`). Both use the cameras'
login, so both need **Talk through cameras** allowed, and both are used only with **Live camera
video** on: with it off the camera page shows snapshots and the sound comes through Home Assistant,
whatever addresses are set. The login is sent only protected (Digest): a camera that offers only Basic
(the password in the clear) is not read this way. Where an address cannot be read, the device falls
back to Home Assistant's stream, and on a device whose kernel cannot keep the decoder off its own
network (the Spot) it uses that stream only, and the setup page offers no picture address.
Each of these is a connection of its own to the camera: with Talk in use as well, a view can hold three
(picture, sound, Talk) on top of Home Assistant's own. A camera that allows fewer
refuses the last to ask: a picture or a sound then falls back as above, and Talk does not connect.

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

### A doorbell

The device can be a Reolink doorbell's chime, with no Home Assistant needed: a press of the button
rings the device and puts the doorbell's camera up for a minute (a Dot only rings). It rings for
every press, at any hour, like any doorbell, and for nothing else: somebody walking up to the door
does not ring it.

1. Put the doorbell on a Reolink Home Hub or NVR. On its own, a Reolink battery doorbell does not
   report its button to anything but the Reolink app; through a Hub it does.
2. In the Reolink app, on the Hub or NVR (not the doorbell), turn on **ONVIF** under
   `Settings > Network > Advanced > Server Settings`, and note its port (8000 unless changed).
3. On the device's setup page, under **Connections**, set up the Hub or NVR in **Reolink cameras**,
   then choose the doorbell's camera under **Doorbell**. If the Hub's ONVIF port is not 8000, enter
   it there too. Save.

The Doorbell section then says whether the device is listening, and when the button was last
pressed. **Play the chime** plays it once, to hear it without going to the door. The chime is a
mechanical door chime's three tones, played twice, at the alarm volume or the volume the device is
at, whichever is louder. During a call there is no chime and no camera: the call's page says
"Someone's at the door" instead (a Dot stays silent). Each press also puts an
`esphome.techo5_doorbell` event on Home Assistant's bus, with `camera` and `device`, for an
automation that wants to know too.

> **Good to know**
>
> Reolink serves ONVIF over plain HTTP. The password itself is never sent, only a one-time digest of
> it, but anybody on the network can see the traffic, and could try to guess a weak password from
> it. A strong password, on a login of its own with as few rights as the Hub allows, is the tidy
> way. Whether a view-only user may follow the Hub's events has not been tried: if the Doorbell
> section says the login was refused, use an administrator's.

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
  now playing, with the song's cover from Spotify. The Spotify app's volume slider and the device's
  volume are one: the slider sets the device to where it is, and the device's buttons, Home Assistant
  and night volume move the slider. With AirPlay, play, pause, next and previous on the device's
  screen, and play and pause from Home Assistant, reach the iPhone, and the song stays on the screen
  while it is paused, until half an hour after the iPhone lets go of the device (it does a few seconds
  after a pause); a pause or a play on the phone shows on the device too, and Stop pauses the phone as
  well. With Spotify Connect, and with an AirPlay app that gives the device no way back to it, a pause
  on the device stops the stream there; the phone keeps going until it is paused too. Anyone on the
  same network can play to the device while one is on, as with any AirPlay or Spotify speaker, and
  Spotify Connect keeps the login a phone hands it until it is turned off. Both have been tried on a
  Show, AirPlay with an iPhone. If something does not work, open an issue.
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
- **A phone over Bluetooth (new).** On the Show, the Spot and the Dot, the **Bluetooth phone**
  switch, in Home Assistant, lets a phone play to the device as to any Bluetooth speaker. For two
  minutes the device can be found and paired with, with no scan for speakers, and a phone connects
  from its own Bluetooth settings, a new one or one paired before; on the Dot the ring pulses blue
  meanwhile. Once the phone's audio is up the window closes, the phone is remembered, and the switch
  stays on while it is connected: turning it off disconnects the phone. The **Bluetooth audio**
  sensor names the connected phone, as the phone calls itself (often with its owner's name), and a
  phone paired in pairing mode shows there and turns the switch on too. A phone that went away may
  not come back by itself: it can connect from its own Bluetooth settings, and **Bluetooth reconnect
  phone** calls the remembered phones until one answers. Anyone nearby can pair while the switch is
  on, as in pairing mode, and a phone that joined stays remembered, so **Bluetooth forget phones**
  forgets every phone and keeps the speakers and earbuds. On the Dot a phone may not get in while
  the Dot is connected to a Bluetooth speaker. Tried on a Dot, a Show and a Spot.
- **Bluetooth, the rest of it (new).** A phone playing to the device has one volume, the device's: its
  volume slider moves the device's volume, and the device's buttons and Home Assistant move the
  slider (night volume still caps a raise from the phone). With two phones, the last to start playing
  has the speaker, and the first takes it back when the second stops, if it is still playing; not
  after AirPlay, Spotify Connect or Music Assistant played, or after a stop on the device. While
  earbuds or a speaker are connected, a phone's audio goes on to them. A short rising tone says a
  Bluetooth device connected and a falling one that it went, except in quiet hours and during a
  call. **Forget**, under the earbuds or speaker in **Settings > Connections** (it asks twice), or the
  **Bluetooth forget** button in Home Assistant on any device, forgets them, so new ones can be
  paired.
- **Radio.** The Radio drawer and its favorites are set with
  [the radio actions](actions.md#wire-up-the-radio-page). While a station plays, the **Radio station**,
  **Radio artist** and **Radio title** sensors say what's on (the artist and title when the station's
  service reports them), for an automation or a dashboard to use.
- **Now playing.** What the device is playing has sensors of its own, for an automation or a dashboard
  to use, since the media player itself cannot carry a song's name: **Now playing source** (AirPlay,
  Spotify, DLNA, Music Assistant, Bluetooth or Radio), **Now playing title**, **Now playing artist**,
  **Now playing album** and **Now playing state** (playing, paused or idle). The names are as the app
  or the station's service sent them. A phone over Bluetooth sends none the device reads, so only the
  source is filled in for it; Radio is whatever the **Radio station** sensor names, with its title and
  artist. While the device holds a track the state says whether it is playing or paused; once it lets
  the track go the others are empty and the state reads idle. When an app is paused on the phone, the
  device keeps the track paused or lets it go a few seconds after the phone stops sending, depending
  on the app, so an automation should go by the state rather than by how long a pause takes. A Music
  Assistant track paused or stopped while it played here stays named, with the state paused, for up to
  half an hour from the pause, as Now Playing keeps it with play on it until **Done**, though the media
  player reads idle meanwhile. Something else played to the device in that time is named while it
  plays, and the paused track comes back once it ends. A video, and a followed player's song (below),
  are not named here. The Spot has no AirPlay or Spotify Connect, so those never show there. Anyone
  who can play to the device can put a title there, and the titles go into Home Assistant's history
  and logbook like any sensor's.
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
  alone. During the night only a call wakes the screen, or a tap when **Tap on the clock** is set to
  *Dashboard*, which opens the dashboard ([docs/dashboards.md](dashboards.md)).
- **Night mode** switch in Home Assistant: start or end the night now, from a bedtime automation for
  example. Set Night hours to **Controlled by Home Assistant** to leave the night to the switch alone.
  See [Turning the night on from an automation](actions.md#turning-the-night-on-from-an-automation).

### Clock and home screen

- **Clock format**, **Clock position** and **Date color** (Date color on the Show only). Clock
  position puts a smaller clock in a bottom corner, so a photo stays in view. Every style goes there
  except *Dashboard*, *Agenda* and *World*, which keep the whole screen. On the Spot, Clock position is
  *Center* or *Bottom*: the *Classic*, *Flip*, *LED*, *Sun* and *Glow* styles go small at the foot of
  the circle, and the others stay in the middle. Also in Home Assistant.
- **Clock style**: how the clock looks all day. *Classic*, *Big*, *Flip*, *LED*, *Analog*, *Words*
  (the time in words), *Sun* (the sun's path from sunrise to sunset), *Dashboard* (the next events
  and the week's weather), *Binary* (the time in lights, one column per digit, counted 1, 2, 4, 8
  from the bottom), *World* (the time here and in other places), *Agenda* (the coming week's
  events beside the time, each day under its heading) and *Glow* (soft colors drifting behind the time). Swipe left or right
  across the clock for the next style or the one before; its name shows for a moment, and a swipe
  the other way puts the last style back. The night clock keeps its own look. Also in Home Assistant
  and on the setup page's Screen & Photos tab.
- **Font**: the font of every word the screen draws: the clock, the date, the weather, the settings
  and the rest (not the icons). *Go*, the screen's own and the default, *Inter*, *MuseoModerno*,
  *Exo*, *Overpass*, *Saira*, *Comic Neue*, *Open Sans* or *Google Sans Flex*. Each is sized so a line
  takes about the room it takes in Go, and a new choice shows at once. A character a font doesn't
  have, like the ♪ in "♪ playing", is drawn in Go. Under Look, also **Screen font** in Home Assistant
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
  weather's words and the alarm after the date are written in it: German, Spanish, French, Italian,
  Dutch or Russian, and English for *Match all* or *English*. It also picks which words the screen
  listens for. It doesn't change what the assistant understands or says, and the rest of the screen
  stays in English.
- **Turn screen**: how a voice request looks. **Classic** is the words, **Wave** is glowing lines and
  **Bars** is an LED-style equalizer, both moving with the voice. This one is on the Spot too.
- **Subtle mute ring** (Spot, in Home Assistant): the red ring shown while the microphones are muted
  is drawn thin and in a dimmer red, so it doesn't light up a dark room.

### Fonts of your own

The screen's fonts draw Latin letters, and Go adds Greek and Cyrillic. Words in other scripts, such as
a Korean song title or a Japanese calendar event, show as boxes. A font of your own fills that in: the
screen draws any character its font and Go don't have from it. It's never the font for everything;
that's **Font**, above.

On the setup page's Screen & Photos tab, under **Fonts of your own**, choose a `.ttf`, `.otf` or
`.ttc` file to send it to the device, and use **Remove** to delete one. Or put the file in the
device's fonts folder over SSH ([Set the SSH authorized keys](actions.md#set-the-ssh-authorized-keys)):
`/data/misc/techo5/fonts`. The Echo Dot has no screen and no fonts folder.

- [Noto Sans CJK](https://github.com/notofonts/noto-cjk) has Korean, Chinese and Japanese. Use one of
  its static fonts (`Sans/OTF/Korean/NotoSansCJKkr-Regular.otf` and the like, about 16 MB), not a
  variable font: a variable font is drawn at its default weight, which is often the thinnest.
- A file can be at most 32 MB, and the fonts together 96 MB. Up to 8 fonts are used, tried in the
  order of their file names, so name them `1-korean.otf`, `2-japanese.otf` to choose.
- From a collection (`.ttc`) the first font is used.
- A font is read from the device's storage as letters are drawn, not loaded into memory whole.
- A new, replaced or removed font shows within a few seconds, with no restart. A file that isn't a
  font the device can draw is left unused, and so is one that goes wrong while drawing; the setup page
  lists it and says why.

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
  (it chimes and stays unmuted; if it mutes instead, tap it to unmute) and the camera, presence and
  camera stills come back, or restart.
  The v1.0.1 boot image fixes it ([updating the boot image](install.md#updating-the-boot-image)); with
  it, holding the button mutes like a tap. The 2nd gen Show 5 is not affected.
- **Gestures** (Show and Spot, experimental, off by default; the **Gestures** switch in Home
  Assistant): cover the camera with your palm, hand on or almost on the lens, for half a second to
  three seconds. It stops a ringing alarm or timer, and Home Assistant gets an `esphome.techo5_gesture`
  event (`gesture: cover`, `device`) for automations. A hand held a few inches away is not counted:
  the camera cannot tell it from somebody leaning in. While Gestures is on the camera looks eight
  times a second, which costs a little more than presence alone.

## 7. Voice

**Speaker gain** is an optional amplifier boost on Show 5 2nd gen units with a TI TAS5805M.
It appears beside the speaker controls in Settings → **Sound**, on the setup page under
**Sound & Voice**, and in Home Assistant's **Playback** configuration. Units with another
amplifier do not offer it. The setting is saved and restored before audio starts, including
after reboots and software updates.

The default is **0 dB**, the amplifier's normal gain. Choose 0 to reset it, or raise it in
1 dB steps up to +6 dB. This boosts all internal-speaker audio, including voice, music and
alarms; the usual volume and night volume still work. Higher gain can distort or crackle,
especially at high volume or with raised bass or treble. Start low and reduce the boost if
that happens; +6 dB is a setting limit, not a guarantee of clean output on every track.

Under Settings → **Sound**, or on the device's Assist satellite in Home Assistant:

- **Wake word**: Okay Nabu, Hey Jarvis, Alexa and others. Choosing none is fine if you only want
  the screen and music.
- **Wake word threshold** (0.5 to 0.99): the score the wake word has to reach. Higher wakes by
  mistake less often; lower hears you more easily. If it wakes by mistake, raise it a step at a time
  (0.90, 0.93, 0.95) and watch **Last wake score** on the device in Home Assistant: a false wake that
  scored 0.91 is gone at 0.93. Called Wake word sensitivity before v1.2.8.
- **Wake word on Home Assistant** and **Confirm wake with Home Assistant** (both off by default):
  let Home Assistant's own wake word engine listen as well as the device, or check a wake the device
  wasn't sure of. See [Wake word on Home Assistant](#wake-word-on-home-assistant) below.
- **Wake**, a button for each wake word, and **Cancel request**, in Home Assistant. Wake starts
  listening for that assistant, as its wake word would. Cancel request ends a request that is
  running, listening, thinking or replying, as a tap on the action button does, and with it
  anything else playing at that moment, like an announcement. With no request running it does
  nothing. The device's Assist satellite says whether one is running, so a tile can show it and start
  or stop it.
- **Quiet hours** and **Do not disturb**.

On the setup page (Sound & Voice) or in Home Assistant, **Night volume** turns the device down to that
level as quiet hours start, if it is louder, and back to where it was as they end. Turned up or down
during the night, it stays where it was put. Changing Night volume during the night moves the device to
the new level, and 0 (no night volume) puts it straight back. A muted device stays muted. Alarms and
timers keep their own **Ring volume**. The Spotify app's volume slider moves down with the device when
night volume turns it down, and during quiet hours it cannot turn the device up past the night volume.
The Music Assistant volume slider cannot turn the device up past the night volume during quiet hours
either, but it does not move when night volume turns the device down.

### A wake word of your own

Any microWakeWord model works, from a collection like
[TaterTotterson/microWakeWords](https://github.com/TaterTotterson/microWakeWords) or one you train
yourself ([training-wake-word.md](training-wake-word.md) covers how, other languages too). Each comes as two files, a `.json` and a `.tflite` with the same name (for example
`echo.json` and `echo.tflite`).

1. Put both files in Home Assistant's `custom_wake_words` folder, inside its config folder (next
   to `configuration.yaml`). Make the folder if it isn't there.
2. Restart Home Assistant. It reads the folder once and keeps what it found, so a word added later
   only shows up after a restart; reloading the device's ESPHome entry is not enough.
3. Pick the new wake word in the device's **Wake word** list, on the Assist satellite in Home
   Assistant. The device downloads it from Home Assistant and keeps it.

### Wake word clips

For training a wake word model of your own on what your room really says and hears, the device can
keep short clips of the wake word itself. Turn on **Keep wake clips** in Home Assistant, on the
device's microphone (for example "Office microphone"). It is off by default, and set for each device.

While it is on, the device keeps the four seconds of audio up to each of these, sorted by kind:

- **wake**: a wake not taken as a mistake (below) within 30 seconds. Mostly the word said on purpose,
  but not always: a false wake followed by talk, from a TV that kept going, for example, lands here too.
- **mistake**: a wake with nothing said after it, so most likely a false wake.
- **noise**: a wake followed only by what speech-to-text writes for noise ("Thank you."). Mostly false
  wakes too, but listen first: somebody may have said the word.
- **nearmiss**: a word that scored at least 0.5 but stayed under the threshold. Somebody not being
  heard, or a sound like the word; listen to each before using it.

Each clip comes with its score, the threshold it was judged against, the wake word model and the time
in UTC. The device keeps the newest 100 of each kind (about 51 MB at most) and deletes the oldest as
new ones come in. Nothing is kept during a call, the intercom or a talk through a camera, for a few
seconds after one starts or ends, or for a few seconds after the stop word. A clip of less than a second
of audio (just after a mute, for example) isn't kept either. **Wake clips** and **Wake clips by kind** show how many there are.

**Turning Keep wake clips off deletes every clip on the device.** Copy off the ones you want first:
while it is off, the actions below answer with an error. If the device can't save the setting (a
full or failing state folder), the clips are deleted all the same, but the next start finds the switch
still on and keeps clips again: turn it off once more after the restart.

To get them off the device, call the `wake_clips` action to list them and `wake_clip_audio` to fetch
each one, the same way kept voice recordings are fetched; see [actions.md](actions.md#list-saved-wake-word-clips).
With SSH on, they're also in `wake-clips/` in the device's state folder (`/data/misc/techo5`, or
`/data/misc/echolocal` on a Dot), one folder for each kind. Each `.wav` has a `.json` beside it with the
same details, and the file name holds the time, the assistant, the score and the threshold:
`20261012-213301-123-s1-p0.79-c0.90.wav` (UTC, to the millisecond).

The clips are what the microphones heard in your room. They stay on the device until you copy them
off; keep them on your own machine.

### Wake word on Home Assistant

The device listens for the wake word itself. Two switches on the device in Home Assistant let Home
Assistant's own wake word engine help. Both are off by default. Use them when the device's models
don't hear you well, for example when they were trained on voices that don't sound like yours, or
when you want a model the device can't run.

Home Assistant needs a wake word engine first: add one, such as the openWakeWord add-on or its
Wyoming integration, and pick it and the wake word in the voice assistant the device uses (Settings →
Voice assistants). Home Assistant listens for one wake word per voice assistant.

- **Wake word on Home Assistant**: while the device is idle, it sends its microphone audio to Home
  Assistant all the time, and Home Assistant's engine listens for the wake word too. It uses the first
  voice assistant's wake word. When Home Assistant hears it, the device chimes and the request goes on
  as usual, with no extra delay. Wait for the chime before you speak, as with the device's own wake
  word. The device's own wake words still work alongside: a wake the device hears itself first closes
  the stream, which can add up to about a second before the chime, and a word both hear opens one
  request. After a request, an announcement or a question from Home Assistant, the stream starts again
  on its own.
- **Confirm wake with Home Assistant**: the device keeps listening, and only asks Home Assistant
  about a wake of the first wake word that it wasn't sure of. A wake that scores at least **Confirm
  threshold** (0.70 to start with) but under the **Wake word threshold** sends Home Assistant about two
  to three and a half seconds of audio: the two seconds before the wake, and up to one and a half after
  while Home Assistant listens. If its engine hears the wake word, the request goes on. If not, nothing
  happens: no chime and nothing on the screen. A wake over the Wake word threshold works as before and
  sends nothing extra. On a Raspberry Pi 5 with openWakeWord, Home Assistant answered in about half a
  second, so a confirmed wake chimes about that much later. **Last wake score** shows a confirmed
  wake's score, which helps when setting the thresholds.

  A steady sound that scores close to the wake word, like the TV, could be asked about over and over,
  so checks are spaced out: at least 5 seconds apart, and longer after each one Home Assistant doesn't
  hear, up to a minute. A wake the device or Home Assistant does hear starts that over. Nothing is asked
  about for a few seconds after a call or a talk through a camera, so none of it is sent.

What to know before turning them on:

- **Privacy.** With Wake word on Home Assistant, everything said in the room goes to Home Assistant
  while the device is idle, not just what follows the wake word. Confirm sends about two to three and a
  half seconds at a time, only for a wake the device wasn't sure of, and spaced out as above.
- **Network.** Wake word on Home Assistant sends about 32 KB a second from each device, all day.
  Home Assistant runs its engine once for every device streaming to it.
- **What stays on the device.** The stop word, a ringing alarm or timer, announcements, calls and
  talking through a camera are always handled on the device. A wake word Home Assistant hears over a
  ringing alarm or timer silences it, as the device's own does. Nothing is sent during a call, a talk
  through a camera, or while the microphones are muted, and turning a switch off stops sending at once.
- **If Home Assistant can't listen** (it's restarting, or its voice assistant has no wake word
  engine), the device goes back to listening for itself and tries Home Assistant again later.
- Confirm does nothing while Wake word on Home Assistant is on, since Home Assistant is already
  listening.

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
sound fades, so a long one cuts off the first words. The wake word is not acted on while the unmute
sound plays and for a moment after, so the device does not take its own sound for the word: a long
`mute_switch_off.wav` keeps it deaf for as long. ffmpeg converts anything else:

```sh
ffmpeg -i chime.flac -ac 1 -ar 48000 -c:a pcm_s16le wake_word_triggered.wav
```

A new or changed file plays the next time its sound does, with no restart, and deleting it brings the
stock sound back. A file the device cannot play leaves the stock sound playing, and the log says why.

Alarms and timers work by voice, on the screen, and from Home Assistant. See
[docs/actions.md](actions.md) for all of them.

## 8. Settings lock

A **settings lock** (Show and Spot, off by default) is a PIN the device asks for before its settings
change, so guests and children can use the device without changing how it is set up. The settings
screen still opens without it, and what is part of using the device stays open to everyone: alarms
and timers, the volume, bass and treble, the headphone output, the microphone, the wake sound, the
sleep timer, and the brightness. Every other setting shows a padlock after its name and asks for the
PIN when it is tapped. The PIN pad shows which setting it is for, and the setting changes once the
PIN is right. Once the PIN is in, the rest stay open until the settings close or are left alone for
two minutes. Set it under Settings → Privacy & Security, on the setup page (Privacy), or with the
[settings_lock_pin action](actions.md#set-the-settings-locks-pin). Five wrong tries in a row make
the device wait before it takes another. The **Settings lock** switch in Home Assistant shows
whether it is on; turning it off removes the PIN.

While the lock is on, the PIN is also asked for when **Allow** is pressed on the setup page.

## 9. A fixed address

The device gets its address from the router (DHCP). On an Echo Show or an Echo Spot (not an Echo
Dot) it can be given a fixed one instead: for example from a range the router does not hand out,
open the setup page, go to **Connections → Network address**, choose **Fixed**, and type the
address (`192.168.1.50`, or `192.168.1.50/24` with its prefix), the gateway, and DNS servers if they are not the gateway. On an Echo Show the
same is on the screen, under Settings → Connections → **Network address**, with a keypad; the
[network_address action](actions.md#set-the-network-address) does it from Home Assistant.

A new fixed address is tried before it is kept. If another device already answers for it, or the
gateway does not answer from it, the device keeps the setting it had, and the setup page says why. A
restart in the middle of the trial comes back on the setting that worked last. Once kept, the address
is used as it is at every start, the way any device with a fixed address does, even when the router is
still starting up.

The address belongs to the Wi-Fi it was set on. On another network, or when its gateway has not
answered for five minutes (a router moved to another subnet, say), the device takes an address from
the router instead, so it can still be found; the setting itself is kept. It goes back to the fixed
address by itself once it is on that Wi-Fi again and the gateway answers, checked every minute (after
an attempt that did not hold, half an hour later). Saving the same address again tries it again at
once, and on another network makes it that network's address.

After a change the setup page is at the new address, and the browser has to be let in there again.
Home Assistant follows the device by itself, about two minutes after the change: it first has to notice
that the old connection is gone. To go back to DHCP, choose **Automatic**.

## More

- [Dashboards](dashboards.md): Home Assistant dashboards on the screen.
- [Phone calls](phone.md) through your own SIP provider.
- [Actions](actions.md): everything Home Assistant can ask the device to do, with examples.

## Questions and problems

Open an [issue on GitHub](https://github.com/HuskerMinion/techo5/issues). Say which device and
version (Settings → General → Updates shows it), what you did and what happened. Answers there help the next person
too.

Attach the device's diagnostics if you can: open its setup page (Settings → Privacy → Setup page, or
the device's **Setup page** switch in Home Assistant on a Dot), go to **General** and press
**Download diagnostics**. It holds the last of the device's log, its recent wake word detections and
near misses, and what it is set to. Addresses, network names, keys and
serial numbers are replaced before you get it, but give it a read before you post it.
