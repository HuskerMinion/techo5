# Setting it up

What to set up once TECHO5 is installed and the device is in Home Assistant, in the order that
works. [Getting started](getting-started.md) gets you to that point. Everything here can be changed
later, and most of it is optional.

Photos, weather, cameras, the screen and the settings lock are for devices with a screen: the Show
and the Spot. On a Dot, steps 1, 5 and 7 apply.

In the examples the device is named `office`. Use your own device's name: the actions are
`esphome.<name>_...` and the entities `switch.<name>_...` and so on.

The setup page, the camera stream and its ONVIF login, and a go2rtc server's login all cross your
network over plain HTTP or RTSP, as most home devices' do: they are made for a home network you
trust. Somebody on it who records a login can try passwords against it, so give each a long password
you use nowhere else.

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

Use Home Assistant's local IP address, as above with your own. A Show, a Spot or the Skylight
Calendar can also take a `.local` name like `homeassistant.local`: it looks the name up itself. The
Dot and the Echo 2nd gen can't, and on a panel it works only where Android looks `.local` names up.
Any address the device can reach on your own network works, including an `https://` name that your
local DNS points at Home Assistant or a reverse proxy on the LAN, as long as its certificate is valid
for that name (for a self-signed one, see "Home Assistant on HTTPS with its own certificate" in
[getting-started.md](getting-started.md)). An address that only works from outside your home, such
as a Nabu Casa URL, does not.

If a photo folder says **Couldn't open this folder**, the line under it says why: *Home Assistant
isn't set up here* is this step missing, *Address not found* a name the device can't look up (give the
IP address), *Can't reach Home Assistant* a wrong address or port, and *Home Assistant refused the
token* a token that was deleted or mistyped. A camera that shows `hass: no access configured` is this
step missing too. A panel needs this step like any other device.

Also turn on **Allow the device to perform Home Assistant actions** on the device's ESPHome entry
(Configure), as in [Getting started](getting-started.md#after-installing-every-device).

## 2. Photos

The slideshow shows photos from one of three places: Home Assistant's media library, an Immich
server the device reads itself, or photos kept on the device. Home Assistant's media library is the
usual one, and the steps below start with it.

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

   Under the album, on the same page, Immich's own choices narrow what shows: **Favorites only**,
   **Taken from** and **Taken through** (whole days on the device's clock; leave either empty for no
   limit), and **Of this person** (a name as Immich's People page has it). **Date and place** writes
   when and where each photo was taken at its foot on the screensaver, as Immich knows it. All are off
   until set, and are also entities in Home Assistant (**Immich favorites only**, **Immich first day**,
   **Immich last day**, **Immich person**, **Slideshow date and place**).

   **Photos on this device.** On the setup page, **Screen & Photos**, **Photos on this device**,
   **Add photos** takes pictures from a phone or computer. They are made screen-sized there before
   they are sent, and the device keeps up to 1,000. The slideshow shows them when it has no Home
   Assistant photos to show. **Remove all photos** deletes them.
2. **Check** that the photos show in Home Assistant under **Media → My media**. If Home Assistant
   can see them, the device can too.
3. **On the device**, swipe down for Settings, then **Display**:
   - **Slideshow**: **Background** puts the photos behind the clock. **Screensaver** takes over the
     whole screen after a while with nothing happening.
   - **Photo folder**: pick the folder, then **Use this folder**.
   - **Time per photo**, **Shuffle photos** and **Include subfolders** are optional.
   - **Show whole photo** shows each photo in full, with a blurred copy of it filling the sides.
     Off, photos fill the screen, which crops tall ones.
   - **Photo pairs** (the Show) puts two tall photos side by side, each filling half the screen,
     in place of one with blurred sides. A tall photo with no other near it in the list shows alone.
   - With an Immich source, **Date and place** and **Favorites only** are here too.

The same settings are entities in Home Assistant, and the folder can be set with the
[`home_slideshow` action](actions.md#set-the-slideshows-photo-source). Given the folder that is
already showing, the action reads it again, for photos added since; the photo up stays until the
next one from the new list is ready. Otherwise the device reads the list again every hour.

## 3. Weather

With access set up (step 1), the clock shows Home Assistant's own forecast for your home with no
setup at all. To use a different `weather.*` entity, or none, use
[`home_weather`](actions.md#choose-the-weather-shown-on-the-idle-screen). Settings → Display also
has **Weather animation**, **Radar source** and **Weather alerts** (alerts are U.S. only, from the
National Weather Service).

## 4. Cameras

With access set up, the Cameras drawer (swipe in from the right edge) lists every camera Home
Assistant has. That is the default (All) for a device never set. To show only some, with friendlier
names (These), or none of them (None), use the setup page (Connections → Home Assistant cameras) or
[`home_cameras`](actions.md#choose-which-cameras-the-device-shows). A device that shares a Home
Assistant with other homes should be set to None or a list, so it does not offer the other homes'
cameras. An automation calling `home_show_camera` can still show any camera.

The setup page's **Camera list** (Connections) sets the order of the drawer and the grid: the
cameras by name, one a line, the first at the top. Cameras not in the box go after the others, and an
empty box puts them back in the usual order. It also has **List this device's own camera**, on by
default. Turned off, the device's own camera is not in the drawer or the grid, and "show" does not
find it; an automation can still put it up.

To show a camera when something happens, like the doorbell, call
[`home_show_camera`](actions.md#show-a-camera-on-screen) from an automation:

```yaml
action: esphome.office_home_show_camera
data:
  entity: camera.front_door
  seconds: 60
```

With **Live camera video** on (a switch of the device's in Home Assistant, under its configuration; off
on a new device), a camera Home Assistant can stream (one that plays live in its own dashboard, from an
RTSP camera or an integration with a stream) is shown as live video, decoded on the device from Home
Assistant's HLS stream. Snapshots keep coming until the first moving frame, which takes a few seconds,
more when Home Assistant still has to start the camera's stream. The picture is a few seconds behind the
camera: HLS comes in parts a keyframe interval long. Other cameras, and any whose stream fails or that
the device cannot decode, are shown from snapshots, a few a second, as with the switch off. That's H.265
on most devices, and any stream over 1080p. The Echo Show 8 2nd gen and the Skylight Calendar play H.265
cameras live too (on TECHO5's own kernel), both up to 1080p at 30 frames a second, so pick a camera's
sub stream when its main one is bigger. A streamed camera's sound, when the view has one,
comes from the same stream, decoded on the device: as far behind the camera as the picture, where the
stream converted by Home Assistant and sent back was some twenty seconds. A stream with no sound in it
falls back to Home Assistant's way. A camera's sub stream (often called *fluent* or *sub*) is the better
choice: it is sharper than the panel needs anyway, starts sooner and costs less.

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

### How the camera view looks

Three settings choose how a camera put up on the screen looks. They are on the device's own settings
(Display → Pop-ups, under Camera time) and in Home Assistant, in the device's **Configuration** card.
Not on the Spot.

- **Camera picture**: *Whole picture* (the default) shows all of the camera's picture, as large as the
  screen takes it, with bars where its shape is not the screen's. *Fill the screen* fills the screen
  and cuts off what does not fit, at the top and the foot or at the sides; live video too. *Camera's
  own size* shows a snapshot at the size the camera sends it, in the middle of the screen, when it is
  smaller than the screen; a bigger one, and live video, are shown as with *Whole picture*.
- **Camera strips**: the strips at the top (the camera's name and the time) and at the foot (*tap to
  close*). *Solid* (the default) is a band of shade. *Fade* is a shade that fades into the picture,
  with the words centered in it, as far from the sides as from the top and the foot.
- **Camera strips shade**: how dark the strips are: *Light*, *Medium* (the default) or *Strong*.

The pictures in notifications are not changed by them.

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

### Camera buttons

On the Show and the Spot, a camera's view can have up to four buttons that each run a Home Assistant
action: **Open gate** on the gate's camera, **Unlock** on the front door's. They sit along the foot of
the view, above the strip with Talk (on the Spot, stacked above Talk). They open gates and doors, so
every one asks first: the first tap lights it up with "Tap again: Open gate", and a second tap within
4 seconds runs it. The button says **Sent** for a moment once it has gone to Home Assistant, or why it
could not. A tap on a button never closes the view.

Set them on the setup page under **Connections** → **Camera buttons**, one camera a line: the camera's
entity as the camera list has it (the section lists them), a colon, then the buttons split by commas,
each `Label=domain.service`, or `Label=domain.service:domain.object` for an action on an entity:

```text
camera.front_door: Unlock=lock.unlock:lock.front_door, Porch light=light.toggle:light.porch
go2rtc:gate: Open gate=script.open_gate
```

A label is 24 characters at most. An empty box takes every button off. An automation can set them with
[`home_camera_buttons`](actions.md#buttons-on-a-cameras-view). Home Assistant runs the actions as the
device's own, so they need **Allow the device to perform Home Assistant actions** turned on for the device
in its ESPHome integration's options; without it Home Assistant refuses them, and says so only in its own
log. The buttons stay on the device: a settings file neither carries them nor brings any in.

### Cameras from go2rtc

A [go2rtc](https://github.com/AlexxIT/go2rtc) server, which many homes run beside Home Assistant (or
inside Frigate), turns cameras of every kind into streams anything can read. Give the device its
address on the setup page (**Connections**, **go2rtc cameras**, like `192.168.x.y:1984`) or with
the [`home_go2rtc`](actions.md#cameras-from-go2rtc) action, and every stream it has is a camera on
the device, by its name in go2rtc: on the Cameras drawer, and by voice ("show the porch"). Its
picture comes from go2rtc, and with **Live camera video** on, live video from go2rtc's RTSP port
(8554), with no camera's own login. Home Assistant is not asked, so they work with it down. Their
sound is not played yet.

Each stream can have a name of its own on the setup page, like "Doorbell" for `front_door_sub`. That
name is shown in the drawer and is what "show the doorbell" listens for. Nothing changes in go2rtc,
so other things that use the stream's name keep working. Left empty, the stream is called by its
name in go2rtc. Reading go2rtc's list again keeps the names.

A go2rtc with a login takes it in the address, as `http://user:password@host:1984`; the setup page
does not show the password again, and saving the address as shown keeps it. Saving the address again
reads go2rtc's list again; clearing it takes the cameras away. Live video uses the same login on
go2rtc's RTSP port, and only with Digest: a go2rtc whose RTSP login differs from its API's, or whose
RTSP offers only Basic, shows snapshots instead. The login goes to go2rtc's API over plain
HTTP, so give go2rtc a password of its own.

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
> way. A view-only user may not be allowed to follow the Hub's events: if the Doorbell section says
> the login was refused, use an administrator's.

### Recording this device's camera

The camera of a Show or a Spot can be recorded like any other camera, by Frigate, go2rtc or an NVR, from
`rtsp://<device address>:8554/camera`. It is off on a new device.

1. On the setup page, under **Privacy & Security** → **Camera streaming**, set a password (12
   characters or more) and, if you like, a user name (`camera` unless changed). Save. The password is
   never shown again; leave the field empty to keep it. Home Assistant can set the same login with the
   device's `camera_credentials` action (`username`, `password`), by the same rules, once you turn on
   **Camera login from Home Assistant**, a switch of the device's under its configuration (off by
   default), and only while the device has its encryption key, so the password never crosses the
   network in the clear.
2. Turn on **Camera streaming**, a switch of the device's in Home Assistant, under its configuration.

It never runs without a password. Turning it off, saving a new login and closing the lens cover each
cut off whoever is watching; the mute button stops the picture, as it stops the camera everywhere else.
While somebody is being sent the camera, a Show draws a small green dot in the top right corner of the
screen, over everything; a Spot shows its camera dot, as it does whenever its camera runs. With the
screen off, it lights at its dimmest to show the dot alone on black, so the room can still tell.

Two viewers at most watch at once, at up to 10 pictures a second. The login is checked with Digest, so
the password does not cross the network in the clear, but the picture itself is not encrypted, and
Digest is MD5: somebody on your network who records a login can try passwords against it offline. Use a
long password of its own, not one you use anywhere else. An address that gets the login wrong 10 times
is refused for 15 minutes; a public IPv6 address is counted with the rest of its /64, since a device
can pick many there. After 50 wrong logins from anywhere within 10 minutes, every new login is refused
for 15 minutes, so a guesser who keeps changing addresses is stopped too. A recorder that logged in within
the last day is not kept out by that, only by wrong logins from its own address; one that shares an
address with a guesser (behind the same router, say) is refused with it.

The device has no video encoder, so the stream is a series of JPEGs (RTP/JPEG, over TCP only), not
H.264. ffmpeg, and Frigate and go2rtc with it, read it; a recorder that wants H.264 has to re-encode it,
which in Frigate is an ffmpeg output argument:

```yaml
cameras:
  kitchen_show:
    ffmpeg:
      inputs:
        - path: rtsp://camera:<password>@<device address>:8554/camera
          input_args: -rtsp_transport tcp
          roles: [detect, record]
      output_args:
        record: -c:v libx264 -preset veryfast -an -f segment -segment_time 10 -segment_format mp4 -reset_timestamps 1 -strftime 1
```

A recorder that adds cameras by ONVIF can take it from `http://<device address>:8181/onvif/device_service`
with the same login: one profile, its stream, and its snapshot while **Camera web access** is on. The
snapshot is the camera page's `/camera.jpg`, which takes the same login and lights the green dot while
it is read. The camera pages (`/camera.jpg` and `/camera.mjpeg`, there while Camera web access is on)
ask for that login too, so set a camera password before you use them; a browser the setup page has let
in needs none. **Since v1.2.24:** a Home Assistant Generic Camera, MJPEG camera or recorder that read
`/camera.jpg` or `/camera.mjpeg` with no login gets "401" until you set a camera password and give it the
user and password with **Digest** authentication (Basic is not taken). Changing the password ends the
pages' open streams, and so does closing the lens cover. The device is not found by ONVIF discovery
unless you turn on **Camera discovery (ONVIF)**, a switch of the device's in Home Assistant, under its
configuration: then, while the stream is on, it answers WS-Discovery searches (3702/udp), so Home
Assistant's ONVIF integration lists it among the discovered devices. The answer tells anyone on your
network who asks the device's name, model and MAC address, which is why it is off by default; without
it, give the recorder the device's address. ONVIF and the pages are a login digest over plain HTTP, as on most
cameras: the password itself is never sent, but anyone who can watch your Wi-Fi traffic could replay a
captured login to the same page for a few minutes, and could see the pictures anyway. `/screen.png`
(Screen web access) has no login: if the screen is showing the camera, a screenshot shows it too.

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
  assistant [answers directly](#voice-without-home-assistant); under Home Assistant, its own Music Assistant support decides. It needs
  Music Assistant 2.10 or later.
- **AirPlay and Spotify Connect.** On every device but the Panel app, two switches make the device a
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
  Spotify Connect keeps the login a phone hands it until it is turned off. Where the image carries the
  AirPlay 2 receiver (every image from v1.6.8 and dot-v1.0.35), an iPhone can group the
  device with other AirPlay 2 speakers (a HomePod, a Sonos)
  and play to all of them at once; without it, or when it will not start, the device is a classic
  AirPlay speaker, which plays on its own; so is one whose kernel can't take the firewall's AirPlay 2
  rules (the firewall's log says so). A third switch there, **AirPlay 2** (on unless turned off),
  keeps it to classic AirPlay: for a group it will not play in step with, since it can still drift a few
  milliseconds from the other speakers, more just after it joins one that is already playing. If something does not work, open an issue.
- **DLNA.** On every device, the **DLNA** switch (Settings → Sound, the setup page's Play to
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
- **A phone over Bluetooth.** On the Show, the Spot and the Dot, the **Bluetooth phone**
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
  **Turn the Bluetooth proxy off** on a device a phone plays to: the proxy keeps scanning on the same
  radio, and the phone's sound stutters or drops while it does (#105).
- **Bluetooth, the rest of it.** A phone playing to the device has one volume, the device's: its
  volume slider moves the device's volume, and the device's buttons and Home Assistant move the
  slider (night volume still caps a raise from the phone), unless **Bluetooth sets the volume** is
  off (below). Pause and play on the device, by voice or
  from Home Assistant, reach the phone over Bluetooth, and so do next and previous on the screen of
  a Show or a Spot; a pause or a play on the phone is followed here, so the song picks up where it
  was paused whichever side plays it again, for half an hour after the pause. A phone takes the
  speaker once it plays: after a second and a half of music, which is not heard, over anything
  playing or paused here (Music Assistant, AirPlay, Spotify Connect, DLNA, the radio, another phone).
  An app that opens the phone's audio without playing anything (a browser does, coming back to the
  screen) or a click does not take it, and with nothing loaded a phone takes it at once. A phone
  still playing when something else takes the speaker is paused, another phone included, and playing
  it again on the phone takes the speaker back; with two phones, the last to start playing has it. A
  phone that ignored the pause takes the speaker back once the phone that has it is paused on the
  phone or stops, but not after AirPlay, Spotify Connect or Music Assistant played, or after a pause
  or a stop on the device. While earbuds or a speaker are connected, a phone's audio goes on to them.
  A short rising tone says a Bluetooth device connected and a falling one that it went, except in
  quiet hours and during a call. **Forget**, under the earbuds or speaker in **Settings >
  Connections** (it asks twice), or the **Bluetooth forget** button in Home Assistant on any device,
  forgets them, so new ones can be paired.
- **Bluetooth sets the volume (new).** On the Show, the Spot and the Dot, the **Bluetooth sets the
  volume** switch (the setup page's Connections tab, or Home Assistant) is on by default, as it
  always was: a phone's or a computer's volume slider is the device's volume. **The device jumps to
  full volume when a computer plays to it: turn it off.** A Linux computer (PipeWire or PulseAudio)
  often reports full volume once and then turns its own sound down itself, which took the device to
  100% and left it there, for Music Assistant afterward too (#245). Off, the device keeps its own
  volume: the phone's or the computer's slider only makes its own sound quieter, never louder than the
  device's volume, nothing a phone sends changes the device's volume, and the device's buttons and
  Home Assistant no longer move the phone's slider. Turning it either way takes effect at once, on
  a phone that is playing too. Turned off while a phone plays, the phone's slider stays where it was
  and now turns its own sound down from there; turned on, the phone's slider jumps to the device's
  volume. Shared with the other devices like the rest of the speaker settings, when sharing is on.
- **Bluetooth Secure Connections (new).** On the Show, the Spot and the Dot, the **Bluetooth Secure
  Connections** switch (Settings > Connections, the setup page's Connections tab, or Home Assistant)
  is on by default, as it always was. **A phone pairs, then drops after a second or two: turn off
  Bluetooth Secure Connections.** Some newer phones do that with it on (a POCO F8 Pro on Android 16,
  #183); with it off they pair the older way and stay. What it costs: a phone set to pair
  only with Secure Connections cannot pair at all, and Low Energy pairing falls back to the older,
  weaker kind too. The device itself pairs only over classic Bluetooth (speakers, earbuds and phones
  playing to it), and the Bluetooth proxy only listens, so nothing of the device's own uses LE
  pairing. Changing it turns Bluetooth off and on again: whatever is connected drops for a moment and
  may need connecting again. It is this device's own, never shared with the other devices or carried
  by a settings file. The 2nd gen Show 8 on TECHO5's own kernel has it off already, and no switch.
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
  half an hour from the pause, held with play on it until **Done**, though the media player reads idle
  meanwhile. The full Now Playing page puts itself away 30 seconds after the pause (see **Now playing**
  under the screen settings). Something else played to the device in that time is named while it
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
- **Sleep sounds.** Two selects in Home Assistant, **White noise layer 1** and **White noise layer
  2**, each play a sound the device makes itself: white, pink or brown noise, rain, ocean, a brook,
  wind, fire, crickets, a fan or a cabin. Pick one alone, or one in each to mix them, crickets under
  wind say. They are made as they play, so they never loop and take no storage. **None** in both
  stops them.
- **Sleep timer** (Settings > Sound, or the **Sleep timer** select in Home Assistant): whatever is
  playing stops after 15 minutes to 2 hours. Nothing rings when it does. It isn't kept over a
  restart, so a device that restarts in the night comes back quiet.
- **Bass and treble** (Settings > Sound, or **Bass** and **Treble** in Home Assistant): -6 to +6 dB
  each. They change the speaker's own tuning, so they need **Speaker EQ** on, a switch in Home
  Assistant that is on unless you turn it off.

## 6. The screen

These are on the Show under Settings → **Display**, and are entities in Home Assistant. Most are on
the Spot too, in its settings.

### The screen and the room's lights

The screen is a light in Home Assistant (`light.<node>_screen`), in the device's area. Assist picks the
lights a sentence like "lights off" or "turn off the kitchen lights" means by their domain and area,
so a screen that is exposed to Assist goes out with the lamps, on every device in the room.

There are two ways to keep the screen out of them:

- **Untick the screen's light** under **Settings → Voice assistants → Expose** in Home Assistant (or on
  the entity's own **Voice assistants** settings). Assist then leaves the screen alone, and nothing
  else about the light changes.
- **Turn on Screen ignores room light commands** (off at first; on the Show and the Spot under
  Settings → **Display** as **Ignore room lights**, on the setup page's **Screen & Photos** tab, and in
  Home Assistant). The screen's light keeps its entity ID and does what it always did, but Home
  Assistant shows it under the device's **Configuration**, and does not expose a configuration entity
  to Assist when it first sees it. Home Assistant also leaves a configuration entity out of what an
  area, a device or a label targets, and out of the dashboards it makes itself: an automation that
  turns off a room's lights no longer turns off the screen, while one that names `light.<node>_screen`
  by its entity ID still does. Use **Screen power**, or the light's entity ID, where the screen should
  still go out with the room. The device reconnects to Home Assistant to tell it, once it is idle.

A device Home Assistant already knows keeps the exposure Home Assistant chose for its screen before:
it does not look again when the entity changes. So with the setting on, untick the screen's light
once under **Settings → Voice assistants → Expose** as well, and keep Screen power exposed.

**Screen power** (`switch.<node>_screen_power`) is there whichever way the setting is. It turns the
screen off and on again at its brightness. It is a switch, not a light, so "lights off" never reaches
it, and it is the one to name by voice: "turn off the kitchen echo screen power", or give it an alias
like "kitchen screen" under its **Voice assistants** settings in Home Assistant.

### Night

- **Night hours**: when the night starts and ends. **At night** picks what the night does: on a Show,
  dark, a faint glow, or a clock alone; on a Spot, dimmed, a clock alone, or dark. The clock alone is the
  night clock, the time in dim red on black (**Night clock** picks *Red*, *Red LED* or *Flip*), with
  **Night light brightness** in Home Assistant setting how bright it is; on a Spot also **Night clock
  brightness** under Display, 1 to 10 from a faint glow, shown on the screen as it is
  changed at night. During the night only a call
  wakes a Show's screen, or a tap when **Tap on the clock** is set to *Dashboard*, which opens the
  dashboard ([docs/dashboards.md](dashboards.md)). A press and hold on a Show's glow or night clock
  brings the screen back as by day, and so does a tap when **Tap lifts the night light** is on (off at
  first; it comes before the dashboard's tap); the night light comes back after a minute and a half
  with nobody touching the screen. On a Spot a tap lights the screen, dimmed, and a
  press and hold lights it and opens the ring menu; the night clock comes back, or the screen goes off
  again, after a minute and a half. When the night starts, Night clock is chosen, or the settings or
  the menu close, the Spot's night clock comes up after 10 seconds instead. A Spot with no hours set has a night of 10 PM to 7 AM.
- **Night warmth** (Show, a switch in Home Assistant, off at first): a warmer, lamp-like white on the
  screen through the night hours. Not on the Spot or the Panel app.
- **Night mode** switch in Home Assistant: start or end the night now, from a bedtime automation for
  example. Set Night hours to **Controlled by Home Assistant** to leave the night to the switch alone.
  See [Turning the night on from an automation](actions.md#turning-the-night-on-from-an-automation).

### Clock and home screen

- **Clock format**, **Clock position** and **Date color** (Date color on the Show only). Clock
  position puts a smaller clock at the foot of the screen, in a bottom corner or between them
  (*Bottom left*, *Bottom center*, *Bottom right*), so a photo stays in view. Every style goes there
  except *Dashboard*, *Agenda* and *World*, which keep the whole screen. On the Spot, Clock position is
  *Center* or *Bottom*: the *Classic*, *Flip*, *LED*, *Sun* and *Glow* styles go small at the foot of
  the circle, and the others stay in the middle. Also in Home Assistant.
- **Weather position** (on the Show): *Top left*, where the weather has always been, or *Top right*.
  At the top right, a weather alert's badge sits beside it (the weather shortens to the temperature and
  its icon to make room) and a Corner timer goes under it. Also in Home Assistant.
- **Clock style**: how the clock looks all day. *Classic*, *Big*, *Flip*, *LED*, *Analog*, *Words*
  (the time in words), *Sun* (the sun's path from sunrise to sunset), *Dashboard* (the next events
  and the week's weather), *Binary* (the time in lights, one column per digit, counted 1, 2, 4, 8
  from the bottom), *World* (the time here and in other places), *Agenda* (the coming week's
  events beside the time, each day under its heading), *Glow* (soft colors drifting behind the
  time), *Nixie* (each figure glowing orange in a glass tube), *Stack* (the hours over the minutes,
  large) and *Overlap* (very large figures, each laid a little over the next). Swipe left or right
  across the clock for the next style or the one before; its name shows for a moment, and a swipe
  the other way puts the last style back. The night clock keeps its own look. Also in Home Assistant
  and on the setup page's Screen & Photos tab.
- **Auto-brightness** (Settings > Display, or **Screen auto-brightness** in Home Assistant): on a
  device with a light sensor, the screen follows the room's light, up to the brightness you set. On
  the Show, **Dimmest** (**Auto-brightness dimmest** in Home Assistant) sets how dark it goes in a
  dark room.
- **Font**: the font of every word the screen draws: the clock, the date, the weather, the settings
  and the rest (not the icons). *Go*, the screen's own and the default, *Inter*, *MuseoModerno*,
  *Exo*, *Overpass*, *Saira*, *Comic Neue*, *Open Sans*, *Google Sans Flex* or *Go Mono*. Each is sized so a line
  takes about the room it takes in Go, and a new choice shows at once. A character a font doesn't
  have, like the ♪ in "♪ playing", is drawn in Go. Under Look, also **Screen font** in Home Assistant
  and on the setup page's Screen & Photos tab.
- **Skin** (Show, Spot and the Panel app; Settings > Display > Look, or **Screen skin** in Home
  Assistant): a whole look over the theme and the font. The Panel app wears skins as the Show does. On
  the Spot a skin brings its colors, font and filter; the frames, backdrops and the eye drawn around
  the clock are the Show's. The theme and custom colors you chose, and your font when the skin has
  its own, are kept for when the skin is taken off, and their rows say so. The red night clock stays
  red under any skin, and on the Spot so do the muted ring and the camera's mark: while one of them is
  up, the one-color skins' tint is off. On the Show it's the same for the "microphone off" pill at the
  top and the red Talk button while a camera's Talk is on: while either is on the screen, the tint is
  off. **Pip-Boy** turns the screen into a green phosphor terminal: every screen in one
  green, in Go Mono, with faint scanlines and edges darker like an old tube's. Photos and cameras turn
  green too; a video plays in its own colors. **LCARS** is a starship console: black, with orange,
  peach, lavender and blue, the tall narrow Antonio font, and the console's frame of elbows and colored
  bars around the home clock. **Amber terminal** is Pip-Boy in amber. **E-ink** is ink on paper, and
  photos turn black and white. **Blueprint** is white on blueprint blue over a faint grid. **Game Boy**
  is the handheld's four greens. **Matrix** is green on black with code falling behind the clock.
  **Synthwave** is a neon sunset over a grid, the sun only behind a clock in the middle. **HAL 9000** is
  black and red with the computer's eye at the top right (not behind the Overlap style, whose figures
  reach into that corner). **Commodore 64** is the home computer's blue screen in its light blue
  border, in an 8 by 8 pixel font drawn in its style: the home clock is its start-up screen, the
  banner, READY., the time as PRINT TI$ answers it in big characters, the date and weather, a line for
  each timer and the next alarm, and the cursor blinking as the computer's does (it redraws the screen
  three times a second for that). The border is thin and even all round, and the screen fills the
  rest; the characters are always a whole number of the panel's pixels, so they stay sharp and square,
  with more columns on a wide panel and the clock as big as fits. **TECHO5
  BASIC** is the same screen with TECHO5's banner: the version running, the device's memory and how
  much of it is free. On the Spot both bring their colors and font. On the turn screens a skin brings
  its ground and colors too, except at
  night. **None** puts back the theme and font you chose. It costs a little time on each frame.
- **Swipe between clock styles**: on by default. Turn it off (setup page, Screen & Photos, or the
  switch in Home Assistant) so a swipe across the clock leaves its style alone. It's off whenever
  **Tap on the clock** is *Nothing*, too.
- **Swipe for volume**: on by default. Turn it off (setup page, Screen & Photos, or the switch in Home
  Assistant) so a swipe up or down on the clock, now playing, the weather or the Bluetooth page leaves
  the volume alone, for a screen that hands keep brushing. The volume buttons still work, and so do
  the swipe down from the top for Settings and the swipe up from the bottom for the Deck.
- **World clock places** (setup page, Screen & Photos tab): the World style's places, as time zone
  names with commas between them, like `America/Chicago, Europe/Paris, Asia/Tokyo`. Up to three; the
  Spot shows the first two. Leave it empty for New York, London and Tokyo.
- **Answer time** and **Now playing** (the full page, or a strip over the clock). Once the music is
  paused or stopped for 30 seconds, the full page goes by itself: to the strip when the setting uses
  one, otherwise back to the clock, or to the dashboard when **Dashboard when idle** is on (the Spot
  has no strip, so its clock face). The track is still held
  for half an hour, so play picks it up again and the page comes back. A page you bring back while it
  is paused, with the clock's word for the music or a tap on the strip's song, stays another 30 seconds.
- **Theme** (Show): a preset, or **Custom colors** of your own. On the screen, tap a color for each part.
  On the setup page's Screen & Photos tab, **Themes**, each of the five colors has a color picker and a
  `#rrggbb` box; they start from the theme the screen wears now. **Match the accent** fills in the
  ground, text, dim text and rules from the accent's hue, for a dark or a light theme, and you can
  change any of them after. The dark and light themes each keep a Custom of their own.
- **Theme mode** (Show): the screen has a dark theme (the Theme it always had) and a light theme
  (*Paper* until you choose another), both set on the setup page. *Dark*, the default, wears the dark
  theme as before. *Light* wears the light one. *Auto* follows the light sensor: the light theme in a
  lit room, the dark one in a dim room, once the room has stayed that way about a minute. In Home
  Assistant: **Theme mode** (*Dark*, *Light*, *Auto*), **Theme** (the dark theme) and **Light theme**;
  neither theme select changes when the mode does. On the screen, Settings > Display > Theme sets the
  theme it is wearing. The night clock keeps its own look.
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
  Dutch, Portuguese or Russian, and English for *Match all* or *English*. It also picks which words
  the screen listens for. It doesn't change what the assistant understands or says, and the rest of
  the screen stays in English.
- **Turn screen**: how a voice request looks. This one is on the Spot too. **Classic** is the words,
  **Wave** is glowing lines, **Bars** is an LED-style equalizer, **VU meters** are two moving-needle
  meters, **Orb** is a ball of light (cool while it listens, violet while it thinks, warm while it
  answers) and **HAL 9000** is the computer's red eye, all moving with the voice. **Face** is a little
  face of glowing strokes with the words beside it (under it on the Spot): wide eyes while it listens,
  a squint and a sideways mouth while it thinks, a mouth moving with the answer, and smiling eyes after
  ([see it move](screenshots/turn-face.gif)). **Cue light** leaves
  the screen as it is (the clock, the dashboard, the music, the photos, the night clock) and shows the
  request as a light along the top edge instead, with a microphone on it while the device listens: it
  breathes while the device listens, runs while it thinks and stays on while it answers. On the Spot
  it's the rim. **Cue and captions** adds what was heard and the answer as captions at the bottom of
  the screen, up to four lines (three on the Spot), cut with "…" if the answer is longer. They sit
  above the music strip, the Now Playing buttons and the Tessera page's dots rather than over them.
  **Rainbow** (the Show; not the Spot or the Panel app) is a cue too: a glow of
  pinks, violets, blues and oranges round the whole edge of the screen that moves with the voice,
  along the top and bottom only while the device thinks, and what was heard and the answer on a card
  of frosted glass a little above the middle. A request opens with a gold globe at the right edge and
  a wave of pink crossing the screen. During night hours it is Cue and captions instead (red over the
  night clock), so the colors don't light the room.
  The screen under a cue keeps working: a tap there does what it always does, and a tap on the
  captions (or Rainbow's card) puts them away. Stop a request by voice or with the stop word. A page
  with buttons to answer (a call, a ringing alarm, the settings, a camera) gets neither the light nor
  captions over it.
- **Timers** (Show; **Timers on the clock** in Home Assistant): how running timers show on the clock.
  **Line** is the soonest under the date with a thin bar, as before. **Ring** is a ring for each (up
  to three) that empties as it runs, and on the Analog style a wedge on the dial from the minute hand
  to where it will be when the soonest ends. **Corner** is a small badge at the top right, so the
  clock keeps the whole screen. **Banner** is a card for each across the foot. **Big** puts the
  soonest's countdown in place of the clock, with the time of day under it. A finished timer rings
  the same way in every style.
- **Subtle mute ring** (Spot, in Home Assistant): the red ring shown while the microphones are muted
  is drawn thin and in a dimmer red, so it doesn't light up a dark room.

### Calendar

The calendar page opens from the date under the clock. Under Settings → **Display** → **Calendar**,
once a calendar is shown, and on the setup page's Weather & Calendar tab. Each is also in Home
Assistant.

- **Opens on**: the view the date opens, and the voice assistant and `calendar_show` too: *Month*
  (the default), *Week*, *Day* (today) or *Coming up*. *Week* opens the month while Week view is off.
  The next event on the clock still opens its own day.
- **Day timeline**: a day on its hours, with the day's weather, the now line on today, ‹ › and a
  swipe for the day before or after. Off shows the day as a list.
- **Coming up, one line each**: one line per event, each day with its weather.
- **Month details**: *Full* shows start times and the coming week's weather on the month, and event
  icons on screens bigger than the Show 5. *Simple* shows events of several days as spanning bars,
  the past days dimmed and today's number in a pill, and the rest as bars. It starts at Full on the
  Show 8 and bigger, Simple on the Show 5.
- **Event icons**: a small picture before a title like "Birthday", "Trash day" or "Dentist".
- **Countdowns**: "in 25 min" on today's events.
- **Week view**: seven days on their hours, and a Day, Week and Month switch over the month. It
  starts on for the Show 8 and bigger, off for the Show 5.
- **Next up on the clock**: the next event at the foot of the clock, with its countdown, when the
  glance strip has nothing else to show. A tap opens its day.
- **Weather in the calendar**: the forecast on the days.

**Calendars from Home Assistant.** Every calendar Home Assistant already has is offered here, with
nothing more to set up on the device: Google Calendar, CalDAV (iCloud and others), Microsoft 365, a
Remote Calendar, its own Local Calendar, each a `calendar.` entity. Pick them under Settings →
**General** → **Calendars**, or with the
[`calendar_sources` action](actions.md#choose-the-calendars-shown). Each device keeps its own list, so a
desk can show one person's calendar and the kitchen the family's. The device reads them with the
token from [step 1](#1-give-the-device-access-to-home-assistant); no calendar password is ever on it.

**Calendars from a link.** A calendar that isn't in Home Assistant can be added by its iCal
address, on the setup page's **Weather & Calendar** tab, under **Calendars from a link**: a name and
the address. Google Calendar has it under Settings, the calendar, **Secret address in iCal format**;
iCloud gives a link when you share a calendar as a **Public Calendar**; Outlook has **Publish a
calendar** under Settings, Calendar, Shared calendars (the ICS link). Anyone with the address can
read the calendar, so the page never shows it again. The device reads it itself every 15 minutes.

Sharing Week view or Month details from a big screen sets that value on a linked Show 5 too, as if
it had been chosen there.

With more than one calendar, the chips over the month turn a calendar off or on for this screen:
its events leave every view, the clock and its event pop-ups. With many calendars, "+N" opens a list
of them all. That choice stays on the device: it isn't shared or saved in a settings file.

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
  Home Assistant gets a **Motion** sensor (motion) for automations. It sees movement, not somebody
  sitting still, and turns off 90 seconds after the last movement. **Presence sensitivity** (1 to
  100, 50 to start with) sets how much has to move: above 50 it also counts smaller changes, so
  somebody across the room is seen, at the cost of more false triggers. It was named Presence before; on
  a device already set up its entity ID still ends in `_presence`, so automations keep working.
  **Screen off when nobody is near** (5 minutes to start with, or Never) puts the screen out once the
  room has been empty that long and lights it again when somebody comes near. Never at night, and never during a conversation,
  a call, an alarm or with the settings open. Nothing the camera sees is kept or sent: a couple of
  times a second the newest frame is compared with the last as a small grid of brightness, on the
  device. The mute button and the lens shutter stop it, and then the screen is left as it is. On a
  Show 8 or 1st gen Show 5 with a boot image older than v1.0.1, a quick tap of the mute button to
  unmute leaves the camera switched off inside Amazon's kernel: **hold the mute button for a second**
  (it chimes and stays unmuted; if it mutes instead, tap it to unmute) and the camera, presence and
  camera stills come back, or restart.
  The v1.0.1 boot image fixes it ([updating the boot image](install.md#updating-the-boot-image)); with
  it, holding the button mutes like a tap. The 2nd gen Show 5 is not affected.
- **Person detection** (Show and Spot, off by default; the **Person detection** switch in
  Home Assistant, which works only with Presence detection on): tells a person from anything else
  that moves, such as a curtain, a pet or the light changing, and can see somebody sitting still.
  While it is on, a small model looks at the middle square of the camera's picture every second and a
  half, whether anything moved or not, as 96 by 96 shades of gray, on the device, and Home Assistant
  gets a **Person** sensor (occupancy). It turns on when the last three looks score 0.6 or more on
  average, where 1 is surely a person, and stays on for a minute after the last time they did.
  **Person confidence** (diagnostic, 0 to 100%) is that average; it is unknown while the switch is
  off or the camera is not watching. The model is TensorFlow's person detection example: small
  enough for these processors, and a modest judge. It often misses a person far from the camera or
  half out of the picture.
  Nothing it sees is kept or sent.
- **Gestures** (Show and Spot, off by default; the **Gestures** switch in Home
  Assistant): cover the camera with your palm, hand on or almost on the lens, for half a second to
  three seconds. It stops a ringing alarm or timer, and Home Assistant gets an `esphome.techo5_gesture`
  event (`gesture: cover`, `device`) for automations. A hand held a few inches away is not counted:
  the camera cannot tell it from somebody leaning in. While Gestures is on the camera looks eight
  times a second, which costs a little more than presence alone.

- **Clap gesture** (every device, off by default; the **Clap gesture** switch in Home
  Assistant): two sharp claps 0.15 to 0.7 seconds apart, then quiet, send Home Assistant an
  `esphome.techo5_gesture` event with `gesture: double_clap` and the `device`, for an automation to
  act on. One clap, applause, a knock at the door, a door, footsteps or somebody talking is not two
  claps. It does not listen while the device plays music or a sound of its own, rings, or is in a
  request or a call. Only the event leaves the device: one microphone is read on it, as the wake
  word already reads them.
- **Hold on the clock** (Show, off by default; the **Hold on the clock** switch in Home Assistant):
  a finger held on the clock for half a second sends the same event with `gesture: hold`. Only on
  the clock itself, with nothing else on the screen (not over the screensaver, a card or the night
  clock): every other page keeps its taps as they are. With it on, a slow tap on the clock is a
  hold, so tap quickly to open what a tap opens.

## 7. Voice

**Speaker gain** is an optional amplifier boost on Show 5 2nd gen units with a TI TAS5805M,
and on the Show 8 2nd gen, whose TAS5805M takes the same 1 dB steps on both channels.
It appears beside the speaker controls in Settings → **Sound**, on the setup page under
**Speaker and voice**, and in Home Assistant's **Playback** configuration. Other Show units
do not offer it. The setting is saved and restored before audio starts, including
after reboots and software updates.

The default is **0 dB**, the amplifier's normal gain. Choose 0 to reset it, or raise it in
1 dB steps up to +6 dB. This boosts all internal-speaker audio, including voice, music and
alarms; the usual volume and night volume still work. Higher gain can distort or crackle,
especially at high volume or with raised bass or treble. Start low and reduce the boost if
that happens; +6 dB is a setting limit, not a guarantee of clean output on every track.

On the **Echo Dot (2nd gen) and the Echo (2nd gen)** the same setting raises the speaker driver
in the audio chip, in Home Assistant's **Playback** configuration and on the setup page (the Dot
has no screen). It is off (0 dB) by default. Each dB is added at every volume step, most
noticeable near the top. It applies to the speaker only: the headphone jack and Bluetooth speakers are not changed.
High settings can distort, and they make the wake word harder to catch over loud music, since
the music reaching the microphones is louder too.

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
- **Wake word effect**, **Thinking effect** and **Replying effect**, for each wake word in Home
  Assistant: what the ring shows while the device listens, waits on an answer and replies. An
  automation can show another for the step a request is in, without changing these, with
  [`voice_effect`](actions.md#show-an-effect-on-the-ring-during-a-request).
- **Quiet hours** and **Do not disturb**.

On the setup page (Sound & Voice) or in Home Assistant, **Night volume** turns the device down to that
level as quiet hours start, if it is louder, and back to where it was as they end. Turned up or down
during the night, it stays where it was put. Changing Night volume during the night moves the device to
the new level, and 0 (no night volume) puts it straight back. A muted device stays muted. Alarms and
timers keep their own **Ring volume**. The Spotify app's volume slider moves down with the device when
night volume turns it down, and during quiet hours it cannot turn the device up past the night volume.
The Music Assistant volume slider cannot turn the device up past the night volume during quiet hours
either, but it does not move when night volume turns the device down.

### Two wake words, two assistants

The device listens for two wake words at once, and each one starts its own voice assistant. Use one
for a local assistant and the other for a cloud one, or one for each person in the house.

On the device's page in Home Assistant, the Assist satellite has an **Assistant** and a **Wake word**
for each of the two. Pick the first pair, then the second. A wake word left empty is off. The screen
and the setup page choose the first wake word; the second is chosen in Home Assistant.

Each wake word's own settings are on a sub-device of its own in Home Assistant, **<name> assistant
1** and **<name> assistant 2**:

- **Wake**: a button that starts that assistant by hand, as its wake word would.
- **Wake word threshold**: how sure the device has to be (see above).
- **Wake word tone**: the sound it makes when it wakes.
- **Wake word effect**, **Thinking effect** and **Replying effect**: what the ring shows.
- **Reply delivery**: the answer as a whole file, or streamed as it comes.
- **Follow-up time**, **Follow-ups in a row** and **Follow-up tone**: listening after an answer,
  below.
- **Max listening time** (5 to 60 seconds) and **Max thinking time** (5 to 300 seconds): how long a
  request may listen, and how long it waits for an answer.
- **Recordings kept**: how many of the last requests are kept as audio
  ([actions.md](actions.md#list-saved-voice-recordings)).

Either wake word can also be answered by [realtime voice](#realtime-voice) instead. The
[voice request events](actions.md#voice-request-events) say which one was heard (`slot` is `1` or
`2`).

**Listening after an answer.** After an answer, the device can listen again without the wake word.
**Follow-up time** (0 to 30 seconds) is how long, and **Follow-ups in a row** (0 to 10, 0 for no
limit) how many times. At 0 seconds it listens again only when the assistant asks a question, which
it always does. **Follow-up tone** is the sound it makes as it listens again. For the first wake
word, the setup page has the same under **Sound & Voice**, **Listening after an answer**.

**The stop word.** Say "stop", with no wake word, while the device is making a sound: it stops a
ringing alarm or timer, an answer, an announcement or the music. After an alarm it also lets go of a
wake light held on. With nothing playing it does nothing, so "stop the timer" said to Home Assistant
isn't cut off. **Stop word threshold** (on the device's microphone in Home Assistant, 0.70 to start
with) sets how sure it has to be. At the top of its range, the stop word is off.

**What was said, in Home Assistant.** The **Last wake word**, **Last heard** and **Last reply**
sensors say which wake word started the last request, what the device heard and what it answered,
for the logbook or a dashboard. Like any sensor, they go into Home Assistant's history.

### Realtime voice

A wake word can be answered by a speech-to-speech model at OpenAI, xAI (Grok) or Google (Gemini) in
place of its usual assistant: answers start almost as soon as you stop, in a natural voice. **It sends
what you say, as audio, to that company while you speak, and they bill for it, by the amount of audio
heard and spoken.** It is off until you choose a wake word for it, on the setup page only: Home
Assistant has no switch for it.

1. Get a key from the provider (an OpenAI or xAI API key, or a Gemini API key from Google AI Studio).
2. On the setup page, under **Sound**, **Realtime voice**: choose the provider, paste the key, set the
   most minutes a day (30 to start with), and tick the wake word to answer with it. Save.

The key is never shown again; leave the field empty to keep it. Choosing another provider or relay
clears it, so a key is never sent where it was not given for. **Relay** is optional: a server on your
network in place of the provider's address, which can hold the key itself; it has to be a `wss://`
address, since your voice goes through it. A settings file never chooses a wake word for realtime
voice, and one that moves it to another provider or relay leaves every wake word off it. A file saved
before realtime voice existed leaves the device's realtime voice as it is.

Each turn has a session of its own, so the model does not remember the last turn, and the device
does not listen again after a realtime answer, even with listening after an answer set for the wake
word: say the wake word again for the next question. The device decides when you have finished
speaking, whatever **End of speech on the device** is set to, and at the wake word's listening limit
what it heard so far is answered; the stop word, the action button and another wake word stop an answer as they stop any
other. Its speech plays as any
answer does, so an alarm, a ring or a call cuts it off. A turn ends after five minutes whatever
happens. The minutes a day count all the time a session is open, not only speech, and once they are
used up the wake word chimes and sounds its failure tone until the next day. The setup page shows the
minutes used today. The provider's model and voice are its own unless you name them; the device names
gpt-realtime and marin for OpenAI, grok-voice and Ara for xAI, and Gemini's native audio model and
Puck.

To do things in the house, add Home Assistant's **Model Context Protocol Server** integration, make a
Home Assistant user of its own for it with no administrator rights, and give a long-lived token of that
user in **Home Assistant token for its MCP server**: the model then has the tools Assist has, for the
entities exposed to Assist. The token is for that Home Assistant only: when the device is given
another Home Assistant's address, the token is cleared. Leaving Home Assistant clears it too. Without it, it has the device's own abilities (timers, alarms, the radio,
the volume). Either way the same rule as for the device's own assistant holds: once a turn has read
the web, a calendar or anything else from outside, nothing that changes anything runs in it, and none
of the provider's own tools (its web search, code, files) are ever turned on.

An entity whose state is free text written by someone outside your home can carry instructions aimed
at the model: a media player's title (an internet radio station names its songs), the companion app's
last notification sensor, a mail or feed sensor, a text helper, a calendar. Home Assistant hands those
to the model with the rest of the house when it reads the house's state, which this device does not
treat as outside words. Do not expose such entities to Assist.

### Voice without Home Assistant

The device can answer with no Home Assistant at all: your own speech servers and a chat model of
your choice. What you say goes to a speech-to-text server, the words to the chat model, and the
answer to a text-to-speech server, then out of the speaker as any answer is.

- The speech servers are Wyoming ones, the kind Home Assistant uses: faster-whisper for speech to
  text and Piper for text to speech, each given as `host:port`.
- The chat model is any server with an OpenAI-style endpoint, such as llama.cpp's server or
  Ollama, given as an address like `http://192.168.x.y:8080/v1`, with a model name if the server has
  several, and a key if it needs one.

What the model can do is the device's own: start, list and cancel timers; set, list and delete
alarms and reminders; stop what is ringing or playing; set the volume; find, play and keep radio
stations; play music from Music Assistant, once one is set up on the device; call another device in
the house; read the device's calendars and the weather, here or somewhere else; and put the
forecast, the rain map or the calendar on the screen. It remembers the conversation for five minutes,
for a follow-up. The town set under **Where this device is** goes into what the model is told, so
its answers and web searches are about your town unless you name another place.

With a [SearXNG](https://github.com/searxng/searxng) server's address it can also look things up on
the web, like scores, news or opening hours, and read the pages it finds. SearXNG needs its JSON
format turned on (`search.formats` in its `settings.yml`). Once a request has read the web or a
calendar, nothing in that request can change anything, so words from outside can't make it act.

To set it up, open the setup page, **Sound & Voice**, **Voice assistant**:

1. **Answered by**: *Speech and a chat model, directly*.
2. **Speech to text** and **Text to speech**: the two Wyoming servers. **Voice** is the Piper voice
   (like `en_US-lessac-medium`; empty is the server's own), and **Language** what you speak (empty
   is English).
3. **Chat model endpoint**, and **Model** and **Key** if the server needs them. The key is never
   shown again; **Remove the key** clears it.
4. **Web search**, if you want it: the SearXNG server's address. **Page reader**, if you want that
   too: a [page reader](../tools/page-reader/README.md)'s address. A page is read through it when it
   has almost no text as it comes (it fills itself in with scripts, like a store's hours) or the site
   answers with an error, as some do to anything that is not a browser.
5. **Anything the assistant should know**: a name, a tone, or what the house wants it to know.
6. **Save**. All three servers are needed, or the page says so and keeps what you had.

Every wake word is answered this way then, except one set for [realtime voice](#realtime-voice).
**Answered by** *Home Assistant* puts it back on Assist.

What it doesn't do: control the house. It has no lights, switches or other Home Assistant entities,
only what the device itself can do. For the house, use Home Assistant's Assist, or realtime voice
with Home Assistant's MCP server.

### A wake word of your own

Most microWakeWord models work, from a collection like
[TaterTotterson/microWakeWords](https://github.com/TaterTotterson/microWakeWords) or one you train
yourself ([training-wake-word.md](training-wake-word.md) covers how, other languages too). Each comes as two files, a `.json` and a `.tflite` with the same name (for example
`echo.json` and `echo.tflite`).

Not every one does: the device runs microWakeWord models with its own small engine, which doesn't
have every operation ESPHome's does. A model that needs one it lacks is refused when you add it, with
the operation named ([#190](https://github.com/HuskerMinion/techo5/issues/190)). Models built with
residual connections or pooling (`ADD`, `AVERAGE_POOL_2D`, `MAX_POOL_2D`) run.

The quickest way is the device's setup page:

1. Open the setup page and go to **Sound & Voice**, **Wake words of your own**.
2. Choose the `.json` and the `.tflite`, then **Add wake word**. The device checks that it can run the
   model before it keeps it, and says why if it can't. The phrase in the `.json` (`wake_word`) is what
   it is called.
3. Pick it in **Wake word** on the setup page or on the device straight away. Home Assistant picks it
   up once the device is idle, usually within seconds (it reconnects to the device, so it waits for
   any request, call or alarm to finish), and then has it in the device's **Wake word** list on the
   Assist satellite.

Only microWakeWord models are taken (`"type": "micro"` in the `.json`), with a `.tflite` of at most
1 MB, and up to 20 sent this way. **Remove** takes one sent from the page off the device; the wake
words that came with the device or from Home Assistant can't be removed there. A wake word the device
is listening for can't be removed or sent again either: pick another one first. Neither can one
chosen as the [volume down word](#the-volume-down-word), the [volume up or next song
word](#the-volume-up-and-next-song-words) or the [hang-up word](phone.md#a-hang-up-word), even while
it's off (for being a wake word too, or with no music or call): set that word's list to **None** or
another word first. The page says "listening for that wake word" in each case.

Or, through Home Assistant:

1. Put both files in Home Assistant's `custom_wake_words` folder, inside its config folder (next
   to `configuration.yaml`). Make the folder if it isn't there.
2. Restart Home Assistant. It reads the folder once and keeps what it found, so a word added later
   only shows up after a restart; reloading the device's ESPHome entry is not enough.
3. Pick the new wake word in the device's **Wake word** list, on the Assist satellite in Home
   Assistant. The device downloads it from Home Assistant and keeps it.

### The volume down word

Music loud enough can drown out the wake word. A word of your own can turn it down with no wake
word before it, and with the music down the wake word is heard again.

- **Volume down word** (on the device's microphone in Home Assistant) picks it from the wake word
  models the device has. **None**, the default, is off. Send yours from the setup page first
  ([A wake word of your own](#a-wake-word-of-your-own)). Home Assistant reads the list as it connects:
  one sent from the setup page shows up within seconds, but one Home Assistant downloaded (by choosing
  it as a wake word) only at the next connection, so reload the device's ESPHome entry to see it
  sooner. One word can't do both: while the word chosen is also a wake word (or has a wake word's
  phrase), it is off as the volume down word, and it comes back once it isn't.
- Heard while music plays, it turns the volume down five steps, with the volume chime. Said again, it
  goes down again. It does nothing during a call, a talk through a camera or a request, or while an
  alarm or timer rings (say "stop" to end a ring or a request), and nothing with the speaker muted or
  with only the device's own white noise playing.
- It is listened for only while music plays, and for ten seconds after, so the gap between two
  tracks doesn't turn it off. With nothing playing, it costs nothing. If its model goes from the
  device, the list shows **None** and the word is off, but the choice is kept: send the model again
  and it comes back. One that fails to load, or fails on the music, is left off: the
  list still shows it and the device's log says why. It comes back when you choose it again (pick
  **None**, then the word), when a model is sent or removed, or when the device starts again.
- **Volume down word threshold** (0.90 to start with) sets how sure it has to be. It gets none of the
  allowance the wake words and the stop word get while the speaker plays: it is only ever said over
  music, and it acts at once (a moment later with the volume up or next song word listening too: see
  below). At the top of its range, it is off. Its scores don't show in **Last wake score**, and **Keep
  wake clips** keeps none of it: the device's log has each one (`wake detected` and `wake near miss`,
  with `slot=103`).

Each time it acts, it fires a [command word event](actions.md#command-word-events). Train it the
way [Training a wake word of your own](training-wake-word.md) describes, with the device's own music
recorded as a negative set: that is what it has to tell the word apart from.

### The volume up and next song words

Two more words act on the music the same way, with no wake word: **Volume up word** turns it up five
steps, and **Next song word** skips to the next song. Each has its own list and threshold next to
**Volume down word** on the device's microphone, works as the volume down word does (heard only while
music plays, never during a call, a talk through a camera, a request or a ring, nothing with the
speaker muted, no allowance while the speaker plays), and is off until you choose a word.

- **Next song word** asks whoever is sending the music for the next song when the device can ask:
  Music Assistant, or a phone playing over AirPlay or Bluetooth. With nobody to ask (a client that
  sends one track at a time, the device's own radio stations, or a sender like Spotify Connect or DLNA that
  can't be asked) it only fires the [command word event](actions.md#command-word-events), with
  `asked: false`, and whoever plays the music skips on that.
- One model can't be two of these: chosen for more than one, it listens only as the first, in the
  order volume down, volume up, next song. The others keep their choice and listen again once it's
  free. A wake word can't be one either, as for the volume down word.
- With more than one of them listening, a word acts a moment later (about half a second): one thing
  said can reach two of them ("turn it down" scoring for the volume up word too), so the words heard
  together wait until each has peaked, and only the surest acts. The device's log names the one left
  out.
- Each device acts on what it hears by itself. Devices playing together in one Music Assistant or
  AirPlay group each ask for the next song when they hear the word, so with the next song word on more
  than one of them, one "next song" can skip more than once. Choose it on one device in the group.
- Each one loaded is one more model running while music plays. The device's log has their scores
  with `slot=104` (volume up) and `slot=105` (next song).

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

### The nearest device answers

**Nearest device answers** (a switch of the device's in Home Assistant, under its configuration; on
by default, and it does nothing until the devices share a house word). With two devices in earshot, Home Assistant answers whichever reaches it first,
which is the faster one and not always the one you spoke to. With this on, a device that hears its
wake word tells the others how clearly it heard it and waits a moment to hear from them, and only
the one that heard it best answers. The others go back to listening without a sound. It is on on
every device unless you turn it off; they need the same house word, set on each device's setup page,
which signs what they tell each other so nothing else on the network can silence them. Make it a long
phrase, 16 characters or more: anything on the network that hears the devices can try words until one
fits. The house word is case-sensitive: it has to match exactly, capitals included. When another device refuses this one's
announcements for having a different word, the setup page names it next to the house word for an
hour, and the log says so.

How clearly is how far the wake word stood out from that device's own room, so a device with hotter
microphones or in a louder room does not win for that alone. Nothing you say during the wait is
lost: it is sent with the rest. A device that has already answered keeps the turn, even if a device
that heard you better speaks up a moment later; and a device that could not answer just then (Home
Assistant out of reach, say) does not take part, so it never silences one that could. If no other
device answers in time, for whatever reason, the device answers as it would without this, so the
worst a fault can do is what happened before: two devices hear you and Home Assistant picks the
faster. A wake word heard by Home Assistant's own engine (above) is settled by Home Assistant as
before.

**Nearest device window** (100 to 500 ms, 300 to start with) is how long a device waits to hear from
the others. Every wake word waits this long. Raise it if two devices sometimes both answer, or a
nearer one loses; lower it if the wait is noticeable. **Nearest device offset** (-30 to 30 dB, 0 to
start with) is added to how clearly this device heard you: raise it on a device that loses when you
speak right beside it, lower it on one that answers from across the room. The log shows each
device's score on every wake word. If the device that heard you best loses Home Assistant during the
wait, the others have already stood down: you hear that one's error, as you would with only one
device, and say it again.

### Sounds of your own

The sounds a voice request makes can be recordings of your own. On the setup page, go to **Sound &
Voice**, **Sounds of your own**. Each sound has **Play**, which plays what the device plays now,
quietly; a file to choose and **Upload** to send a recording for it; and, once it has one, **Use the
device's own** to go back to the stock sound. The device checks a file as it arrives and refuses one it can't play, with
the reason, so the sound before it stays. Nothing is converted on the device: send a WAVE file in the
format below.

Or copy a WAVE file named for the sound into the device's sounds folder over SSH
([Set the SSH authorized keys](actions.md#set-the-ssh-authorized-keys)). The folder is
`/data/misc/echolocal/sounds` on an Echo Dot, and `/data/misc/techo5/sounds` on everything else.

| File | Plays in place of |
|---|---|
| `wake_word_triggered.wav` | the wake sound, and a follow-up's, where **Wake sound** is Home Assistant |
| `failure.wav` | the falling notes when a request cannot be served |
| `canceled.wav` | the notes when a request is dropped |
| `timer_finished.wav` | a finished timer, and the Home Assistant alarm sound |
| `alarm.wav` | an alarm, where **Alarm sound** is **Your own** (without one it rings with the Home Assistant sound) |
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
[docs/actions.md](actions.md) for all of them. Snoozed by mistake? Turn the alarm off, and its snooze
goes with it; turn it back on and it rings at its next time. A one-time alarm is already off once it
has rung, so delete it instead, or cancel the snoozes: the Snoozed row on the screen, or Cancel snoozes
on the setup page. An alarm followed from a Home Assistant helper works the same way when its on/off
switch is turned off.

**Alarm sound** (the Alarms card, the setup page, or Home Assistant) picks what alarms ring with.
**Your own** rings with `alarm.wav`, sent from **Sounds of your own** above; it can be up to 10 seconds,
and plays again after a short pause for as long as the alarm rings.

**Gets louder** (the Alarms card, the setup page, or **Alarm gets louder** in Home Assistant) starts an
alarm quiet, 30 dB under its ring volume, and brings it up evenly to the ring volume over 30 seconds, 1,
2 or 5 minutes. **Full at once**, the default, rings at the ring volume from the first chime. Timers
always ring at full.

**Said as it rings** (the Alarms card, the setup page, or **Alarm words** in Home Assistant) is what
an alarm says after its first chime: **Its words** (the default) says the words an alarm was given,
**The time and its words** says the time first ("7:05 AM. Take the trash out"), so an alarm with no
words still says something, and **Nothing** leaves the ring alone. The time follows the clock's 12
or 24 hour setting; AM and PM are said only with the screen language on English or all languages,
and otherwise a 12 hour time is the hour and minutes alone. A Dot has no clock setting, so it says a
12 hour time. Words that only say "alarm" are not said. The chime holds back for up to nine seconds
while they are said. Home Assistant speaks them, in its assistant's voice and language, so with Home
Assistant out of reach the alarm rings without them.

**Wake with light** lights the room for a while before an alarm: the screen on a Show or a Spot, the
light ring on a Dot. Set the default on the Alarms card or the setup page, and give an alarm its own
in its editor (5 to 30 minutes, or off). Once an alarm with a wake light rings, the light stays at
full while it rings and while it is snoozed. When you stop it, the light stays at full until the
next alarm if that alarm's own wake light has already started; otherwise it goes off. So with half
an hour of light, alarms at 8:00 and 8:30 are one light from 7:30 until the second is stopped, and
an 8:00 alarm snoozed until 8:36 carries the light on to a 9:05 alarm. A stop with nothing ringing
turns a held light off: the stop word, the action button, a tap on the light's page, or **Stop alarm
or timer** in Home Assistant. That is also why a stray "stop" the device hears with nothing playing
lets a held light go. On the device a snoozed alarm still comes back, just without the light
held until then; Home Assistant's Stop cancels the snoozes as well. Turning a Show's screen off while
the light is held keeps it off until that light is over or the next alarm rings. **Wake light
brightness** (percent) and **Wake light color** (like `#ff9b1a`, empty while off) show it in Home
Assistant, for room lights to follow; see [docs/actions.md](actions.md#follow-the-wake-light).

**Light only** in an alarm's editor (and on the setup page) makes it a wake light and nothing more:
the light comes up as above, and at the alarm's time there is no sound and nothing to dismiss; the
night ends instead, so the screen is back as by day until the night hours next start. The light
stays at full for 30 minutes after the alarm's time, so room lights following it do not go dark at
wake time, then goes off. A stop with nothing ringing turns it off sooner, as above, and so do
an alarm that rings in that half hour ending, stopped or rung out (a snooze keeps it),
and turning the light-only alarm off or deleting it. The half hour is not saved: a restart inside
it turns the light off. A
light-only alarm always has a light: its own, the default, or 30 minutes when both are off; its
editor's **Wake with light** row shows the minutes it gets. On a Dot, which has no screen, it is the
light ring's sunrise and nothing else: no sound, and no night to end.

### How loud the room is

**Ambient noise sensor** (a switch of the device's in Home Assistant, under its configuration; off
on a new device) gives Home Assistant an **Ambient noise** sensor: how loud the room has been over
the last five seconds, in dBFS, rounded to a whole decibel and sent when it moves by 2 or more. Use
it to turn the voice down when the house is quiet at night, or to notice the TV is on in another
room. Only the number leaves the microphones, never sound, and with the switch off the sensor reads
unknown.

It holds its last reading while the device makes a sound of its own (an answer, music, an
announcement, a ring) and for a second after, and through a voice request, a call or a talk through
a camera, so the device's own voice and yours talking to it never show in it. It reads unknown while
the microphones are muted (**Microphone mute** on, red light lit). If it stays unknown with the
microphones live, the device log says why each time the reason changes: look for `ambient noise has no
reading`.

dBFS is measured against the loudest sound the microphones can take, which is 0, so a room reads
below it, and a quieter room reads lower. It is not a calibrated sound level. **Microphone gain**
moves it by the same number of decibels, and **Microphone mixing** moves it too: **All microphones**
and **Delay and sum** read a steady room lower than **Center mic** does, and **Beamformer** (offered
on some devices) leans toward the loudest thing in the room. Set both first, then watch the sensor
for a day to learn your room's quiet and loud readings. Sound from another speaker in the room
counts, like any other noise.

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

## 10. Saving the settings

On the setup page, **General**, **Download settings** saves everything the device is set to as a
file, `techo5-settings.json`. **Restore settings from a file** takes one back: from this device
after a reinstall, or from another device to start this one off the same way. You are shown which
parts would change first, and nothing changes until you press **Restore and restart**; the device
then restarts to use them and is back in about a minute. Anything changed on the device while you
were deciding stays.

The file leaves out what should not travel: keys and passwords (Home Assistant's, Music Assistant's,
Immich's, the doorbell recorder's, the dashboard server's, the assistant's, OBS's and paired
computers'), cameras' stream addresses and their login, calendars' private addresses, the settings
lock's PIN, the house word, and any user, password or key written into an address. A restore keeps
the device's own, but only for the same server: if the file points a setting at a different server,
that setting's key or password is left empty for you to enter again. The page shows every server
address the file changes, old and new, and names what will need entering again, including calendar
links and paired computers that only the device the file came from has.

A restore never opens a door on the device or has it listen or watch. Its privacy switches (SSH, the
camera and screen pages, talking through cameras), AirPlay, Spotify Connect, DLNA, Sendspin, the
Bluetooth proxy, video and the addresses allowed to send it video, the diagnostics settings, Drop
In, sending the room to Home Assistant's wake word engine (streaming or confirming), keeping wake
word clips and recordings, presence and gestures, the remote API, camera streaming and its login, the
ambient noise sensor, the clap gesture, nearest device answers, hold on the clock, the update channel
and automatic updates, live camera video, and a choice of no wake word all stay as they are, and so do its
paired Bluetooth speaker, its amplifier's gain and anything it is in the middle of (a snooze, a
timer). Wi-Fi networks, SSH keys and a fixed address are not part of the settings and are never in
the file.

The file is still personal: it holds where the device is (for the weather), addresses on your home
network, user names and the assistant's instructions. Keep it somewhere private, and don't post it
or attach it to an issue; the diagnostics download is the one made for that.

### Sharing settings with the other devices

Settings can be kept in step between devices, setting by setting, each with the devices you choose,
or copied to other devices once. It is off on every device until you turn it on, on each one that is
to take part: **Settings sharing** in Home Assistant, or **Shared settings** under **General** on the
setup page. Every device needs the same house word (Connections), since what one device sends another
is signed with it.

What turning it on allows, plainly: any device of the house can then copy the settings listed below
to this one, except those this one keeps, and can ask to link a setting with it. Nothing links this
device by itself: a link is made here, by ticking a device, or by accepting another device's request
here.

- **Linked settings.** On the setup page, open a group (Speaker, Screen, Night and so on) and tick,
  for a setting, the devices it is to be kept in step with, then save. Each device ticked is asked;
  until it accepts, its box says *asked*. On that device, **Asked to link** lists the request with
  **Accept** and **Decline**. Once accepted, the two settle on whichever has the more recent change,
  and from then on a change to that setting on either, from the screen, Home Assistant or the setup
  page, reaches the other within a few seconds. A request that could not be delivered, the device
  being off say, is sent again, soon at first and then less often, up to once a day, and whenever you
  save; after a week with no answer it says *no answer* and is not sent again. A declined request is
  remembered: the same device asking again for the same setting is turned down without being shown,
  for good. Only the device that declined can undo it, by ticking the other device for that setting
  itself. A device linked with several passes a change on to the rest, so a
  group stays in step. There is no main device: every change is numbered one past the last, and the
  latest number wins, whatever any device's clock says. A device that was off or unreachable when a
  change was made is sent it again, soon at first and then less often, and as soon as it is back on
  the network, every linked setting is sent to it, so the two end with the later of each. **Last
  from** shows which device a value came from. Untick a device to take it out of the link; it is told,
  and unlinks too. A setting is linked with 16 devices at most.
- **Keep mine.** Ticked for a setting, nothing another device sends changes it here, linked or copied;
  the device that sent it is told it was kept.
- **Copy once.** Under **Copy once**, tick the settings and the devices, and press **Copy**: this
  device's values go to them once, with no link, and the page shows what each device took. Home
  Assistant can do the same with [`copy_settings`](actions.md#copy-settings-to-other-devices). A copy
  counts as the latest change on the device it lands on, so where that setting is linked there, it is
  passed on to the rest of the link as a change made there would be.

Only everyday settings can be shared: the speaker's sound, how an answer is shown and spoken, alarms'
sound and snooze, the screen, night, photos' timing and look, calendar pop-up timing and the calendar's look, and the
dashboard's behavior. Never what opens the device to the network or has it listen or
watch (the same list a restore leaves alone, above), and never a password, a key, a server's address
or the device's name. Also never anything about the microphones: what they hear, how readily and how
long they listen, and how readily a stop word stops something. Nor the signs that they are listening
or muted (wake word tones and effects, the follow-up tone, the mute light, quiet hours), how loud an
alarm rings, severe weather alerts, do not disturb for calls, which photos and calendar events a screen
shows, or which outside services are told where the house is or what is playing (the weather and rain
map sources, lyrics). A device that has no such setting (a Dot has no
screen) says so and is left alone, and so does one with sharing off.

## 11. The remote API

For scripts and tools that are not Home Assistant. It reads what the device is and what its settings
say, changes its everyday settings, and follows its log as it is written. It never changes a privacy or
security setting (SSH, the camera and screen pages, the setup page, Drop In, talking through cameras,
presence and gestures, sending the room to Home Assistant, keeping clips, calls, Bluetooth pairing and
the proxy, AirPlay, Spotify Connect, DLNA, video, Sendspin, the settings lock, updates, restarts, camera
streaming, the ambient noise sensor, the clap gesture, hold on the clock, nearest device answers, or the
API itself): those it can read, not change. It never runs Home Assistant actions, and it carries none
of what was said to the device or what it answered.

1. On the setup page, **Privacy & Security**, **Remote API**, press **Make a key**. The key is shown
   once, on that page (the one time it crosses your network): copy it. The device does not keep the
   key, only a secret made from it, in its own settings file, which never goes out in the diagnostics
   download or a saved settings file. A lost key cannot be shown again; make a new one, which stops
   the old one working and ends any log being followed with it.
2. Turn on **Remote API** in Home Assistant (a switch of the device's, under its configuration), or on
   the screen (Show and Spot, **Privacy & Security**). Off on a new device.

It is on the device's web port, `http://<device>:8181/api/v1/`, on your own network and without
encryption, so every request is signed with the key rather than carrying it: a request seen on the
network cannot be sent again or changed, and the key is not in it. A request is signed over its
method, its path and query, the time, a fresh nonce and its body, and the device refuses one more than
30 seconds from its own clock. In Python:

```python
import hashlib, hmac, json, secrets, time, urllib.request

KEY = "the key from the setup page"
DEVICE = "http://192.168.x.y:8181"

def call(method, path, body=b""):
    secret = hmac.new(b"techo5-remote v1", KEY.encode(), hashlib.sha256).digest()
    ts, nonce = str(int(time.time())), secrets.token_hex(12)
    msg = "\n".join(["v1", method, path, ts, nonce, hashlib.sha256(body).hexdigest()]) + "\n"
    sig = hmac.new(secret, msg.encode(), hashlib.sha256).hexdigest()
    req = urllib.request.Request(DEVICE + path, data=body or None, method=method,
                                 headers={"X-Techo5-Remote": f"v1 {ts} {nonce} {sig}"})
    with urllib.request.urlopen(req) as r:
        return r.read()

print(call("GET", "/api/v1/device"))
call("POST", "/api/v1/entities/night_volume", json.dumps({"state": 3}).encode())
```

What it answers:

| Request | What it does |
| --- | --- |
| `GET /api/v1/device` | The device's name, model, software and how long it has been running. |
| `GET /api/v1/entities` | Every setting and reading the device has, as Home Assistant sees them: `id`, `name`, `kind` (switch, number, select, text, button, sensor, binary_sensor, text_sensor), `state`, whether it is `writable` here, and a number's `min`, `max` and `unit` or a select's `options`. |
| `POST /api/v1/entities/<id>` | Changes one: `{"state": true}` for a switch, a number for a number, one of the options for a select, a string for a text, and nothing for a button, which presses it. A setting that is not writable here answers 403. |
| `GET /api/v1/logs` | The log as server-sent events: the last 200 lines, then each line as it is written, for up to 30 minutes; two at a time at most. |

The log has the device's name, serial number, Wi-Fi networks, Bluetooth devices, passwords and keys
taken out, as the diagnostics download does, along with addresses and logins, and what was said and
answered replaced by how long it was; it still names rooms and other devices, so treat it as private. Turning the API off or making a new key ends a log being followed. An address that
sends requests that do not check out is turned away for a minute after ten of them; nobody else is,
except that a public IPv6 address is counted with the rest of its /64.

## 12. Reaching the device from away

An Echo Show, Echo Spot or Skylight can join your Tailscale network, so a device in another house
stays in your own Home Assistant, and its setup page can be opened from afar with **Remote setup**.
Off until you give it a key: see [Tailscale: reaching a device from away](tailscale.md).

## More

- [Dashboards](dashboards.md): Home Assistant dashboards on the screen.
- [A dashboard for your devices](ha-dashboard.md): controls and health sensors for every device in
  Home Assistant, and a script that stops them all.
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
