# TECHO5 Cast plan

Put a video, or a computer's or phone's screen, on a Show. Our own version, built on what the daemon
already has, not a merge of a fork.

**Status (2026-10-06):** steps 0, 1 and 2 built on branch `cast` and tried on a Show 5 (2nd gen):
on-device ffmpeg, the video page, Home Assistant's `play_video`, and DLNA video with the question on
the screen. User doc: [Video](video.md). Steps 3 and 4 not started. See "What step 0 measured" and
"Where the build differs from this plan" below; the rest of this page is the plan as it was written.

## What step 0 measured (2026-10-06)

On a Show 5 (2nd gen): MT8163, four Cortex-A53 at 1.3 GHz, armv7 userspace on Alpine 3.24. MediaTek's
hotplug keeps two cores online at idle and brings the others up under load.

- **ffmpeg 8.1.2, our own build** (`tools/linux/build-ffmpeg.sh`): 3.5 MB stripped, the libraries
  static, only musl, libssl.so.3, libcrypto.so.3 and libz.so.1 shared (all already in the rootfs).
  Decoders H.264, MPEG-4, AAC, MP3, Opus; demuxers MP4, Matroska, MPEG-TS, HLS, AAC, MP3, Ogg, WAV;
  protocols pipe, HTTP, HTTPS, TCP, TLS, HLS, crypto (and udp, which ffmpeg 8's TLS links against).
  **No file protocol at all.** No ffprobe: ffmpeg's own `-i` answers the probe.
- **The framebuffer:** mtkfb, 480×960 portrait, 32 bpp BGRA, stride 1920, three pages, flipped with
  FBIOPAN_DISPLAY (waits for vsync, about 60 Hz).
- **The pipeline that holds 30 fps:** an even frame rate first, then scale to the picture's place in
  4:2:0 with `fast_bilinear`, pad, `transpose=1`, and only then convert to BGRA. The scaler is the
  cost; scaling and converting in one step is slow.
- **Costs at 30 fps with the daemon running:** 360p/480p about 0.9 to 1.1 cores, 540p 1.2, 720p 1.45 to
  1.65, 1080p about 2.6 to 3 (four threads; best effort). The daemon idles at 0.3 to 0.5 cores; AAC to
  PCM costs about 1.5% of a core.

## Where the build differs from this plan (2026-10-06)

- **Names:** "Video" for steps 1 and 2 (the Video and DLNA video switches, the Video and Video title
  sensors). No "Cast" anywhere a user sees it.
- **The decoder** runs as its own user (`techo5-video`, uid 89, in the inet group; `nobody` on an
  older image, never root), niced by 5, with limits on its address space (640 MB), file size (0: it
  writes no file), core dumps and open files, a protocol whitelist of
  `http,https,tcp,tls,hls,crypto`, and `-rw_timeout` so a silent connection ends the video. HTTPS
  certificates are checked unless Skip certificate checks is on.
- **One decoder process** gives both pipes: the picture on fd 3, the sound on its standard output.
  The picture's frames come already scaled, letterboxed and turned for the panel; the daemon copies
  each into the next framebuffer page and pans (`screen.PresentFrame`), blending the controls over it
  while they're up. It held 30 fps in Go; no C helper was needed.
- **The clock** is the sound as heard: what the media player queued, less what is still queued or
  in the card (`media.Player.Heard`). A frame shows when the clock reaches it and is dropped when the
  next one is already due. A video with no sound, or whose sound runs out first, runs on the wall
  clock. The media player now keeps what a received track reads while it is held up rather than
  dropping it, so the sound can't run ahead of the picture after a pause.
- **What goes over the video** (a call, a ring, the PIN pad, the setup page's question, the pairing
  and Wi-Fi pages, a camera, the settings, a voice turn) pauses it, and it goes on afterward. A voice
  turn pauses rather than ducks: the turn's page is on top, and a picture nobody can see is not worth
  playing on.
- **The Spot:** skipped. Its round screen would want its own layout; nothing of the player is built
  for it, and its DLNA renderer offers no video.
- **Home Assistant's `media_player.play_media`** can't route a video here: Home Assistant converts
  what it sends to the ESPHome media player into audio first (its ffmpeg proxy). Left out on purpose;
  `play_video` and the DLNA renderer are the ways in.
- **play_video's `title`** is required by Home Assistant (ESPHome actions take every argument); `""`
  is none.
- **DLNA asking:** the first video from an address asks; Allow is remembered until the setup page
  forgets the list (up to 32 addresses), Not now refuses that address for a minute, 30 seconds with no
  answer is Not now. Seeking is still not supported.
- **1080p:** the plan said no; it plays on a Show 5 at about 2.6 decoder cores with this build.
  ffmpeg 8 refuses `-skip_loop_filter` on this command line (the picture's output then fails to open),
  so there is no cheaper mode for it.
- **Not done:** the settings screen on the device has no Video row (the setup page and Home Assistant
  have the switches); the DLNA renderer does not send a ConnectionManager event when DLNA video is
  turned on or off (controllers read GetProtocolInfo when they connect); the camera page's RTSP idea
  (question 8) is untouched.

## What a user gets, in the end

1. **"Play this video" from Home Assistant.** An automation or a dashboard button sends a video
   address to the Show, and it plays full screen with its sound: a clip from a NAS, a Jellyfin or
   Plex link, a doorbell recording, a YouTube video through Home Assistant's Media Extractor.
2. **Video from the apps people already have, with nothing to install.** The Show's DLNA renderer
   takes video as well as music, so BubbleUPnP, Jellyfin, Kodi-style controllers, Windows' *Cast to
   device*, and Home Assistant's own DLNA media player can send a video to it.
3. **A computer's screen or one browser tab on the Show.** Open the Show's page in Chrome, Edge or
   Firefox, type the code the Show displays, and pick a window or tab. No program to install.
4. **Later, maybe: a phone's screen.** That needs an app on the phone (see Step 4). It is the last
   step, and the maintainer decides whether we do it at all.

Protected video (Netflix, Prime Video, Disney+ and other DRM) can't be shown by any of these. The
YouTube app's own Cast button won't list the Show either: that is Google Cast, and only certified
devices can be receivers.

## The forks

Only one fork of this repository has Cast: the one linked in issue #90. It is 51 commits ahead of
main on its `main` branch (Cast is 26 of them, on `feature/cast`; the rest is a Xiaozhi voice
backend and an update channel of its own). A second repository by the same author, a Stream Deck
for the Show, streams computer windows with sound over the dashcast wire. Nothing else among the
29 forks, their branches, or the copies of this repository on GitHub casts video. (Several copies
carry dashcast, which is ours.)

How the fork's Cast works:

- **The phone does all the decoding.** An Android app (Kotlin, Media3) plays the video, or grabs
  the screen with MediaProjection, and sends **JPEG frames** and **48 kHz stereo PCM**, each stamped
  with the phone's clock. YouTube and other sites go through yt-dlp inside the app.
- **Transport:** TCP port 8940, one phone at a time, the port opened in the firewall only while Cast
  is on. mDNS `_techo5cast._tcp` so the app finds the Show.
- **Encryption:** Noise `NNpsk0_25519_ChaChaPoly_SHA256`, keyed by a random 80-bit key the Show makes
  when Cast is turned on. The key reaches the phone through a QR code on the Show's screen. Same kind
  as our dashcast and deck links.
- **Consent:** on by default, the Show asks "Cast to this screen?" with Accept and Decline. Twenty
  seconds without an answer declines. A phone accepted in the last five minutes gets back in
  without asking.
- **On the Show:** Go's `image/jpeg` decodes each frame, drawn as a full-screen page. Frames are shown
  by their stamp and late ones dropped. Audio goes to the speaker with a drift correction like
  sendspin's (about 200 ppm between the phone and the Show).
- **Measured by the fork on a Show 5 (2nd gen):** full-size 960x480 frames decode at about 12.5 fps,
  limited by decoding. Half-size frames (480x240, drawn doubled) reach 30 fps with no drops at about
  4.3 Mbit/s. The app sends half size by default.
- **Takes the screen** as a page over the others. A call, a ring or an assistant turn goes on top
  with the cast running underneath. A swipe in from the left edge ends it.
- **Licenses:** the daemon code and the wire protocol are MIT. **The Android app is GPL-3.0**
  (it includes youtubedl-android). We copy nothing from either. If we ever choose to speak the same
  protocol, we write the receiver from its published spec, not from its code.

What it tells us: JPEG frames at half size are the cheapest way to get moving pictures onto this
SoC from Go, and they are good enough for a phone's screen. They are not enough for a film at full
sharpness, and they need an app on the sender, because no phone or browser sends JPEG frames by
itself.

## What the Show can decode

- **The SoC:** MediaTek MT8163, four Cortex-A53 cores at up to 1.3 GHz, 32-bit userspace, 1 GB of RAM.
  Show 5 panels are 960x480; the Show 8 is 1280x800. The daemon draws on the framebuffer and rotates
  each frame onto the portrait panel itself.
- **No video decoder today.** The rootfs has no ffmpeg or GStreamer. The daemon is pure Go
  (`CGO_ENABLED=0`). The camera page shows Home Assistant snapshots, a few a second, decoded by Go's
  JPEG decoder (`feature/home/camera.go`). Camera sound is converted by Home Assistant's own ffmpeg on
  the way in.
- **Hardware decode is out of reach.** The kernel has `CONFIG_MTK_VIDEOCODEC_DRIVER` and
  `CONFIG_MTK_JPEG`, but MediaTek's video decoder on this kernel is driven by Android's closed
  userspace libraries, not V4L2. Getting it running would be a reverse-engineering project of its
  own. (A theory, not checked against the kernel source. The hardware JPEG block might be easier, and
  could double the JPEG frame rate; also untested.)
- **Software decode with ffmpeg is the realistic path.** ffmpeg's H.264 decoder has NEON code for
  32-bit ARM and uses several cores. My estimate, from what other A53 boards of this class manage,
  and to be measured in step 0:
  - 480p H.264 at 30 fps: about one core. Comfortable.
  - 720p at 30 fps: two to three cores. Borderline next to the wake word, which already uses about a
    third of the machine. Probably fine at 24 fps, or with ffmpeg's fast-decode options.
  - 1080p: no. Apps and servers have to send 720p or less.
  - HEVC (H.265): too slow above 480p. VP9 and AV1: no.
  - Scaling and color conversion to the screen size cost about another half core.
- **Painting** a full 960x480 frame 30 times a second is about 55 MB/s of copying and rotating. The
  daemon already pages and rotates the framebuffer; step 0 measures how far it gets.

So the Show can play ordinary 480p and 720p H.264 video on its own, if ffmpeg comes along. That is
the question the whole plan turns on, and why step 0 is a measurement.

## The approaches, rated

| Approach | Needs on the phone or PC | The Show decodes | Fits these devices | Effort | Security |
|---|---|---|---|---|---|
| (a) DLNA video | Any DLNA app (BubbleUPnP, Jellyfin, Windows Cast to device) | H.264 file or stream, up to 720p | Good, if ffmpeg fits | Small, once (f) exists | DLNA has no login. Off by default, ask at the screen |
| (b) AirPlay mirroring | Nothing (iPhone, Mac) | H.264 from the phone, plus Apple's FairPlay handshake | Possible at 720p and below. Heavy | Large. Would mean a GPL-3 receiver (UxPlay) as its own process | Reverse-engineered protocol. Apple can break it in an update |
| (c) Miracast | Windows (Win+K), some Samsung phones. Not Pixels or iPhones | H.264 over RTP | Poor. Needs Wi-Fi Direct from the MT7668 vendor driver, unknown | Large | Weak PIN pairing. A second radio role |
| (d) Google Cast | Nothing | VP8/H.264 | No | Not possible | Receivers need Google-signed device certificates |
| (e) Browser screen sharing | Desktop Chrome, Edge or Firefox. Phones can't (no screen capture in mobile browsers) | JPEG frames the page makes itself | Good: the fork's numbers apply | Medium | The Show's page needs HTTPS. A code shown on the Show is the key and the consent |
| (f) Home Assistant "play this video" | Nothing | H.264 file or stream, up to 720p | Good, if ffmpeg fits | Medium. The player is the core everything else uses | The ESPHome link is already authenticated |
| Phone app (the fork's way) | An app we would have to write, sign and ship, or the fork's | JPEG frames | Good, proven by the fork | Large, and ongoing for an app store | Noise with a pairing key, ask at the screen |

Notes on a few rows:

- **(a) and (f) are one feature.** Home Assistant finds DLNA renderers by itself and makes each one a
  media player. Once the Show's renderer takes video, `media_player.play_media` with a video works
  through it, and so does Media Extractor (yt-dlp on the Home Assistant machine) for YouTube links.
  The Show's ESPHome media player can't do this: Home Assistant converts whatever is sent to it into
  audio first.
- **(b) AirPlay** is the only way to mirror an iPhone with nothing installed. It reuses the decoder from
  (f). It is a lot of protocol work, and the FairPlay part exists only as reverse-engineered GPL code.
  Not now.
- **(e) needs HTTPS**, because browsers only allow screen capture on a secure page. The Show would
  serve its own certificate and the browser would warn once. A page hosted elsewhere over HTTPS can't
  talk to the Show over plain `ws://` (mixed content), so this can't be avoided.
- **A server in between** is another way round the decoder: a machine already running dashcast could
  decode a video and send frames over the dashcast wire, as the Stream Deck repository does for
  windows. That is the fallback if ffmpeg turns out too big or too slow for the Show.

## Recommendation

A staged plan. Video first, from Home Assistant and DLNA, decoded on the Show by ffmpeg. Desktop
screen sharing from the browser second. Phone mirroring last, and only if the maintainer wants it.

Why this order:

- Steps 1 and 2 need **nothing new on any phone or computer**, and cover what most people mean by
  "cast": put this video on that screen.
- The video player is also the piece AirPlay mirroring, a smoother camera page (RTSP or go2rtc
  streams instead of snapshots) and any later phone app would build on.
- Screen sharing from a browser reuses the fork's proven idea (half-size JPEG frames) with no app.

## How it works

### Step 0: measure (before anything else)

- Build a small ffmpeg for armv7: only the H.264, MPEG-4, AAC, MP3 and Opus decoders, the MP4, MKV,
  MPEG-TS and HLS demuxers, HTTP and HTTPS, the scaler. No encoders, no devices, no filters beyond
  scaling. My guess is 5 to 8 MB. Alpine 3.12's own package pulls in far more and is years out of date.
- On a bench Show 5 and the Show 8, with the wake word running: decode 480p, 720p and 1080p H.264 to
  raw frames at screen size, and record fps and CPU. Then the same through the daemon's paint path.
- Decide: on-device ffmpeg, or the dashcast-server fallback. Also check the rootfs and A/B partition
  have room.

### Step 1: the video page and "play this video"

- **A video page** in `feature/display`, full screen like the camera page. It sits over the clock,
  dashboards and the deck. A call, a ring, an alarm or an assistant turn goes on top, and the video
  pauses under it (music-style ducking for a turn, a pause for a call).
- **Controls:** a tap shows a strip with pause, stop, the time and the volume for a few seconds. The
  back gesture the dashboard uses ends the video. When the video ends, the screen goes back to where
  it was.
- **The player:** a new `feature/video` runs ffmpeg as a child process, at low priority, as an
  unprivileged user. ffmpeg hands back frames already scaled to the screen (or half size on the Show 8
  if step 0 says so) and 48 kHz stereo PCM, with timestamps. The daemon owns the screen and the
  speaker as it does today. The sound is the master clock: frames are shown when their time comes and
  dropped when late.
- **The sound** plays through the media player as a received track, like DLNA music, so volume, night
  volume, mute and "whatever starts next takes the speaker" all work as they do now.
- **Home Assistant:** an `esphome.<device>_play_video` action (`url`, optional `title`) and
  `esphome.<device>_stop_video`, and a **Video** state sensor (`idle`, `playing: <title>`,
  `paused`). The actions come over the ESPHome link, which is already authenticated, so they don't
  ask at the screen.
- **What it accepts:** `http`, `https` and HLS addresses only. No files, no `rtsp` at first.
- **Not on the Spot at first** (its round 480x480 screen needs its own layout), never on the Dot.

### Step 2: DLNA video

- The renderer (`feature/dlna`) adds `video/mp4`, `video/x-matroska` and `video/mp2t` to its sink
  protocols, only while **DLNA video** is on. A controller that sends a video gets the video page;
  music works as before.
- Play, pause, stop, the position and the volume go both ways, as they do for music. Seeking is still
  not supported at first.
- Media servers that transcode (Jellyfin, Plex, BubbleUPnP Server) should be asked for H.264 at 720p
  or less. The docs say how.
- **Ask at the screen.** DLNA has no login, so anyone on the network can send a video. The first video
  from an address asks "Show a video from *name*?" with Accept and Decline, like the fork's question.
  Accepted addresses get in without asking for a while. Home Assistant's own DLNA player is a
  controller like any other, so it is asked once too, unless the address is remembered.

### Step 3: screen sharing from a browser

- A **Share your screen** page on the Show's web server, served over HTTPS with a certificate the Show
  makes for itself. The browser warns once.
- The person taps **Share screen** on the Show (or in Home Assistant), and the Show shows a six-digit
  code for two minutes. Typing it on the page is both the key and the consent: someone who can't see
  the Show can't share to it.
- The page captures a window, tab or screen with `getDisplayMedia`, scales it to half the Show's size,
  makes JPEG frames in the browser, and sends them over a WebSocket on the same HTTPS connection.
  Tab or system sound, where the browser offers it, goes as Opus or PCM; the daemon already decodes
  Opus.
- The Show draws them on the video page. Expect the fork's numbers: 30 fps at half size, a latency a
  little over one frame plus the network, about 4 Mbit/s. Good for slides, photos, a recipe; soft for
  small text.
- Desktop browsers only. Chrome on Android and Safari on iPhone don't offer screen capture to pages.

### Step 4 (maybe): a phone's screen

Three ways, each a decision for the maintainer:

1. **Our own Android app.** Full control, but a second product to build, sign, update and support in
   an app store. The largest cost of the four steps, and it never ends.
2. **Speak the fork's protocol** so its existing app works with our Shows. We would write our own
   receiver from its MIT protocol spec, and depend on someone else's GPL app staying alive and
   compatible.
3. **AirPlay mirroring** for iPhones and Macs, on top of the step 1 decoder. No app, but the heaviest
   protocol work and a reverse-engineered handshake.

My suggestion is to stop after step 3, see who asks for phones, and decide then.

### Settings and storage

- `state.json` gets a `video` section: **Play videos** (Home Assistant and DLNA), **DLNA video**,
  **Ask before showing**, **Screen sharing**, and the remembered addresses.
- The settings sheet's Connections and the setup page show the same switches. Home Assistant gets the
  switches, the actions and the state sensor.
- Diagnostics show "set" for the certificate and nothing of remembered addresses beyond a count.

## Security

- **Everything is off by default.** Three separate switches: Play videos, DLNA video, Screen sharing.
  The ports for screen sharing open in the firewall only while it is on.
- **Ask before showing** is on by default for DLNA and for screen sharing (the code is the asking).
  Home Assistant's own action doesn't ask: it is already authenticated.
- **ffmpeg parses untrusted files.** A media file is a classic way into a decoder. So: our own current
  ffmpeg build, not Alpine 3.12's; run as an unprivileged user with no write access, with limits on
  its memory, files and cores and a lower priority (nice; there is no CPU limit), killed when the
  video ends; a protocol whitelist of `http,https,tcp,tls,hls,crypto`, so a playlist can't make it
  read local files. (Built: and no file protocol compiled in at all.)
- **Names and redirects that lead to the device** (built): ffmpeg resolves names and follows
  redirects itself, so checking the address first is not enough. On the Show the kernel refuses
  anything the decoder's user sends to loopback or link-local (the firewall's TECHO5-VIDEO chain, by
  uid). The Spot's kernel has no owner match, so there the decoder goes through a proxy in the daemon
  that makes every connection itself and refuses the device's own addresses, after redirects too.
- **The Show fetches whatever address it is given.** That is already true of DLNA music. Video makes
  it no worse, but the docs say so.
- **No new keys to manage** for steps 1 and 2. Step 3's code lives for one session.

## What is left out, on purpose

- **Google Cast.** Not possible without Google's certification.
- **DRM video.** Not possible.
- **Miracast.** Few senders left, and the Wi-Fi driver is an unknown.
- **Copying the fork's code or app.** Our own implementation throughout.
- **1080p and HEVC on the Show.** Senders transcode.

## Steps and size

0. **Measure:** a minimal ffmpeg for armv7, decode and paint numbers on a Show 5 and a Show 8. One to
   two days. Decides the rest.
1. **The video page, the player and the Home Assistant actions.** About a week, most of it the timing
   (sound as the clock, dropping late frames) and the screen's priorities.
2. **DLNA video and asking at the screen.** Two to three days, tested with BubbleUPnP, Jellyfin,
   Windows' Cast to device and Home Assistant's DLNA player.
3. **Screen sharing from a browser**, with HTTPS on the Show. Three to five days.
4. **A phone's screen:** only after a decision, and sized then.
5. **Docs:** `docs/cast.md` with each source, what to send (720p H.264), and the security notes.

## Open questions for the maintainer

1. **Is ffmpeg on the Show acceptable?** A C program of 5 to 8 MB in the image, run only while a video
   plays. If not, the fallback is decoding on a dashcast server, which means a server for video.
2. **Room in the image:** is there space in the rootfs and the A/B slots for it?
3. **Name:** "Cast" (as the forks say, though it is not Google Cast), or "Video" for steps 1 and 2 and
   "Screen sharing" for step 3?
4. **DLNA asking:** ask once per address (and remember for how long), or never for addresses the user
   lists?
5. **Self-signed HTTPS on the Show** for step 3: acceptable, with the browser's warning once?
6. **Phones:** stop after step 3, write our own app, speak the fork's protocol, or try AirPlay?
7. **The Spot:** a round video page later, or never?
8. **The camera page:** once the player exists, should it play RTSP or go2rtc streams for smooth live
   video instead of snapshots? (Its own small plan.)
