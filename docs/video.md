# Video

An Echo Show or Spot can play a video full screen, with its sound. Home Assistant sends it an address, or a
DLNA app on your phone or computer sends it one, and the Show plays it.

The Echo Show 5, the Show 8, the Spot and the Skylight Calendar. The Dot has no screen. On the Spot's round screen a wide
picture fills the width, and the edge of the glass takes its corners.

## What plays

- **Addresses:** `http://` and `https://` only. MP4, MKV, MPEG-TS and HLS (`.m3u8`) streams.
- **Picture:** H.264 (and old MPEG-4). 720p or less plays best. 1080p plays on a Show 5 but uses
  almost all of it. AV1 doesn't play anywhere.
- **H.265 (HEVC), VP8 and VP9:** on the Echo Show 8 2nd gen and the Skylight Calendar only, on
  TECHO5's own kernel (an Echo Show 8 2nd gen still on Amazon's kernel plays what the other Shows do). Each plays only
  as fast as the device decodes it in time; a faster one is refused rather than played behind. The
  Show 8 2nd gen plays H.265 up to 1080p at 30 frames a second, and VP8 and VP9 up to 1080p at 120. The
  Skylight plays H.265, VP8 and VP9 up to 1080p at 30. The other Shows and the
  Spot don't play them.
- **Sound:** AAC, MP3, Opus, AC-3 and E-AC-3 (Dolby Digital), surround mixed down to stereo. A
  video whose sound is something else (DTS, TrueHD) plays without it.
- **Loudness:** films are mixed much quieter than music, so the Show evens a video's sound out to
  about music's level as it plays: the volume steps mean the same for both.
- **Not:** protected video (Netflix, Prime Video, Disney+ and the like), local files, `rtsp://`
  cameras. The YouTube app's Cast button won't find the Show either: that's Google Cast.

Ask your media server for H.264 at 720p or less. In Jellyfin, Plex or BubbleUPnP Server, that's the
transcoding or quality setting for the client.

## Turn it on

Videos are off on a new device. On the device's setup page, **Screen → Video**:

1. Tick **Play videos** and **Save**. Home Assistant can now send videos.
2. For DLNA apps too, tick **DLNA video** as well. DLNA itself has to be on
   (**Sound → Play to this device → DLNA**).

Home Assistant has the same two switches: **Video** and **DLNA video**.

## Play a video from Home Assistant

Use the `play_video` action ([Actions](actions.md#play-a-video)):

```yaml
action: esphome.office_play_video
data:
  url: "http://192.168.1.20:8096/Videos/clip.mp4"
  title: "Front door, 7:42"
```

`stop_video`, `pause_video` and `resume_video` do what they say. The **Video state** sensor says
`idle`, `asking`, `loading`, `playing` or `paused`, and **Video title** names what's playing.

`play_video` answers as soon as the video starts loading. One that turns out not to play (the
server says no, the file is HEVC, the connection drops) says why on the screen for a few seconds and
in the **Video error** sensor, which clears once a video plays; it isn't an error on the action.
A refused address (not `http` or `https`, or the device itself) is an error on the action, and goes
in the sensor too.

The sound plays through the Show's speaker like music: the Show's volume, Home Assistant's media
player and its pause all work on it. Starting a video stops music that's playing, and music or radio
started afterward takes the speaker and ends the video. A video left paused for 30 minutes stops.

Home Assistant's own media player (`media_player.play_media`) can't send a video this way: Home
Assistant turns what it sends into sound first. Use `play_video`, or the Show's DLNA renderer (Home
Assistant finds it as a DLNA media player).

## Play a video from an app (DLNA)

With **DLNA video** on, the Show shows up as a DLNA renderer that takes video. BubbleUPnP on
Android, Jellyfin's apps, Kodi and VLC's renderer lists, a Plex Media Server's DLNA server through
any of those, and Windows' *Cast to device* can send to it. So can Home Assistant's own DLNA media
player for the Show, once Home Assistant has found it.

The first video from each address asks on the screen: **Show a video from 192.168.1.30?** Tap
**Allow** to play it and remember that address. **Not now** leaves it, and that address isn't asked
again for ten minutes. Nobody answering in 30 seconds is Not now. While the question is up, a video
from another address is turned away rather than put in its place. Home Assistant's `play_video`
never asks, but Home Assistant's DLNA media player is a DLNA controller like any other: the first
video it sends asks once too. A program on the Show itself that sends to the renderer at `127.0.0.1` doesn't ask either,
when it runs as root (one started from `rc.local`: whoever put it there already had a root shell on
it) or is an add-on installed from the setup page's Add-ons card, which you agreed to and whose
warning says it can drive the device. A program on the device running as any other user is asked
about, named as "a program on this device (user N)". Such a program speaks for whatever it acts on: one that passes on what
other devices on the network ask of it has to decide for itself which of them may put a video on the
screen, since the question will not be asked for them.

An allowed address is remembered for 30 days after it last sent a video: one that hasn't sent any
for that long asks again (a phone's address on your network can go to another device). The Video
section on the setup page lists them with the day each was last used. To make every address ask
again, tick **Forget the … addresses** and **Save**.

Play, pause, stop and the volume work from the app, and so does moving through the video (the app's
slider, DLNA's Seek): the player starts the video again from the new place, saying it's loading
until the first picture from there, and a paused video stays paused. When you drag a slider, only
where you let go is played, about a third of a second after the last move. That needs a video that
says how long it is (a file, or an HLS playlist of a whole film, as Plex, Jellyfin and Emby send).
A live stream can't be moved through: the app is told so, and it plays on.

## On the screen

- **Tap** shows the controls for a few seconds: play or pause, stop, quieter and louder, with the
  title and the time. On the Spot they're across the bottom of the circle.
- **Back and forward** are there too for a DLNA video whose app says it acts on them (it subscribes
  to the renderer's events and says so, as a Plex add-on does; most apps don't): the app is told and
  plays the one before or after ([how it is told](setup.md#dlna-next-and-previous)). On the Spot, swipe left for the next and
  right for the one before.
- **A bar** runs above the controls for a video that can be moved through: one from Home Assistant
  that says how long it is, or a DLNA video the renderer can move through, or whose app says it acts on
  seeks (the app is then asked to, [the same way](setup.md#dlna-next-and-previous)). Tap it to go to that place,
  or slide a finger along it: the time shows where you are, and the video moves there when you let
  go. A live stream has no bar.
- **The picture button**, next to quieter (between stop and quieter on the Spot), changes how the
  picture fills the screen, for a badly framed or oddly shaped video. Each tap moves on to the next of
  three: **fit** shows the whole picture with black bars (the usual), **fill** fills the screen and
  cuts off what doesn't fit, top and bottom or the sides, and **stretch** pulls the whole picture to
  the screen's shape. Its icon shows which one is on: a screen with the picture fitted in it, a crop
  mark, or a picture pulled out to the edges. The picture starts again where it was, after a moment
  of loading. It's for the video that's playing only, and isn't a setting: the next video starts
  with fit again. It's there for a video the Show can move through itself, one that says how long it
  is. A live stream, or a Plex transcode that only its app can move through, doesn't have it, since
  starting one again would start it wherever its server does.
- **Swipe down**, or **Stop**, ends the video. The screen goes back to where it was.
- A call, an alarm or a timer ringing, the settings, a camera or a voice turn goes on top. The video
  pauses under it and goes on once it's gone.
- **At night** a video plays at the night light's level (half brightness if you haven't chosen a
  night light), not full brightness, even if the screen was already on. Its controls work as by day:
  a tap brings them up, a swipe down ends it.
  The Spot is never brighter than 30% at night anyway, video or not.

## Good to know

- **The Show fetches whatever address it's given.** That's already true of DLNA music. Only
  Home Assistant, and DLNA apps you allowed, can send one.
- The video player is a separate program (ffmpeg) that the device runs only while a video plays. It
  runs as a user that can't change anything on the device, with limits on its memory, and it can
  only open network addresses, never a file on the device. It can't reach the device itself either:
  an address whose name, or a redirect, leads back to the device is refused, by the device's
  firewall on the Show and by a proxy in the daemon that the player goes through on the Spot.
- An `https://` address has its certificate checked, unless **Skip certificate checks** is on (a
  diagnostic switch in Home Assistant).
- A Show 5 uses about one core for a 480p video and one and a half for 720p, with the rest of the
  Show still answering.
- **Over Bluetooth headphones or a speaker**, the picture may run ahead of the sound: the player
  times the picture by the sound leaving the Show, and Bluetooth adds its own delay after that. (A
  theory, not measured yet.)
