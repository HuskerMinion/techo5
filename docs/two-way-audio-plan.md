# Two-way audio: talking back through a camera

A camera's own sound is heard on the device while its view is up. This is the other direction:
somebody taps **Talk** on the camera page, speaks, and the device's microphones go out to the
camera's own speaker. Answering the doorbell without a phone in hand.

**Status: a plan, revised 2026-09-30. Nothing of it is built.** The first version was written before
the camera's own sound, the Reolink cameras and the device without Home Assistant were on main; this
one is checked against main at `7ef38f6` and against go2rtc's source at v1.9.14 and master. The
task-by-task implementation is [superpowers/plans/2026-09-30-two-way-audio.md](superpowers/plans/2026-09-30-two-way-audio.md).

## What changed since the first version

- **No ffmpeg, and no transcoding by go2rtc.** go2rtc's plain `http:` source reads a live WAV itself
  (`pkg/magic` sniffs `RIFF`, `pkg/wav` ignores the sizes), but it does **not** convert: the codec the
  device serves has to be the one the camera's backchannel takes, exactly. That is G.711 at 8 kHz on
  nearly every camera that has a backchannel (Tapo, DVRIP, ISAPI, Doorbird, Reolink's doorbell over
  RTSP). So the device downsamples to 8 kHz and encodes A-law or μ-law itself: a few dozen lines of Go
  and a lookup, cheaper than any stream go2rtc would have to start. The first version's "PCM 16 kHz,
  nothing has to be encoded" was a misreading: `pkg/pcm/backchannel.go` is the output side of
  `exec:` sources, not an input.
- **go2rtc says whether a camera can be talked to.** `GET /api/streams?src=<name>&microphone` connects
  to the camera and lists its media, and a backchannel is the one marked `sendonly`:
  `"audio, sendonly, PCMA/8000"`. The Talk control is drawn only on a view whose camera answered that
  way, which settles the first version's open question about offering Talk where it cannot work.
- **Cameras are not only Home Assistant's now.** They are `camera.*` entities, `reolink:<channel>` read
  straight from a recorder, and `local`. Talk does not need Home Assistant at all: a Reolink camera on a
  device with no Home Assistant can be talked to through a go2rtc that knows it.
- **Settings for servers other than Home Assistant live on the setup page**, with the password written
  and never shown (the chat model, SearXNG, Reolink, iCal links). go2rtc goes there too, on the
  Connections tab next to Reolink, and not in a `home_go2rtc` action.
- **Home Assistant's own go2rtc is not the go2rtc to point at.** Since 2024.11 Home Assistant runs one
  inside itself, but its API listens on a Unix socket only (`api.listen: ""`), its RTSP on 127.0.0.1, and
  its stream names are `<platform>_<unique_id>`, registered only once something has viewed the camera.
  The one reachable way in is its `debug_ui` port, which Home Assistant says is for debugging. The go2rtc
  this is for is a standalone one, the go2rtc add-on, or Frigate's with port 1984 mapped out.
- **Home Assistant still has no two-way audio of its own.** Core PR #148282 (a `TWO_WAY_AUDIO` camera
  feature and a WebRTC re-offer command) is open, not merged, as of 2026-09-16. If it lands it is a
  WebRTC route and needs a WebRTC stack on the device; it changes nothing here.
- **The wake word is not paused by the intercom**, which the first version said it was. The wake engine
  keeps scoring every frame during a call and only ignores what it hears (`feature/detect/detect.go`
  `OnDetect`, on `phone.Busy()`). Talking does the same: the wake word is ignored while somebody talks
  to a camera, because "Alexa" said to a visitor is not said to the device.
- **The echo canceller needs nothing fed to it.** Its reference is the hardware loopback
  (`hardware/mic/mic.go`), so the camera's sound playing on the device is subtracted from the
  microphones because it goes through the DAC, not because anything tells it to.

## How it works

1. Somebody opens a camera. If go2rtc is set up and the **Talk through cameras** switch is on, the
   device finds the camera's go2rtc stream (below) and asks go2rtc whether it has a backchannel, in the
   background, while the first picture loads. The answer is kept for ten minutes.
2. If it has one, a **Talk** control is drawn on the view. A tap on it starts talking; a second tap ends
   it. It is a toggle and not press-to-talk, because the echo canceller makes both directions at once
   work, and holding a finger on a picture of the person you are talking to is not how anybody wants to
   hold a conversation.
3. Talking starts by making a one-time address, `http://<device>:8181/talk/<32 hex>.wav`, opening the
   web port if nothing else had it open, and asking go2rtc to play it on the camera:

   ```
   POST http://<go2rtc>:1984/api/streams?dst=<stream>&src=http://<device>:8181/talk/<token>.wav
   ```

   `<device>` is the address the device reached go2rtc from, which is the address go2rtc can reach the
   device on. It is read off the connection, not configured.
4. go2rtc fetches the address during that POST. The device answers with a WAV header whose sizes are
   unknown (`0xFFFFFFFF`), then 20 ms at a time: the microphones' processed 16 kHz frames, filtered and
   halved to 8 kHz, encoded A-law or μ-law, flushed. Each address serves once, to the first request
   that asks for it, and 404s everything else.
5. The camera's own sound goes on playing on the device, ducked, while this happens. The canceller has
   the loopback, so the microphones send the room and not the doorbell's echo of itself.
6. While somebody is talking the view does not time out: every second it is held at least 15 s ahead.
7. Talking ends when the control is tapped again, the view closes (tapped away, replaced by another
   camera), the switch is turned off, the microphones are muted, two minutes pass, or go2rtc hangs up.
   Ending is the device closing its response: go2rtc's play loop sees the end of its source, waits a
   second for its buffer, and closes the camera's backchannel (`internal/streams/play.go`). There is no
   second API call to make and nothing to tear down in go2rtc.
8. A refusal is said on the view for five seconds ("Can't talk: …") and in the log with go2rtc's own
   words.

`DELETE /api/streams` must never be used: it deletes the stream from go2rtc's config. An empty
`src=` on the same POST stops a play, and is not needed while the source ending does the same.

## Finding the camera's go2rtc stream

Nothing in a Home Assistant camera entity says which go2rtc stream it is, and nothing in a Reolink
channel does either. So the stream is matched by name, against go2rtc's list (`GET /api/streams`), and a
per-camera name on the setup page settles the ones the match misses:

1. The name set on the setup page for this camera, if there is one — used whether or not go2rtc lists
   it, since a stream registered on demand is not listed until something has used it.
2. For `camera.<id>`: `<id>`. This is what makes Frigate cameras match, since Frigate's go2rtc streams
   are named for its cameras and so are its Home Assistant entities.
3. The camera's name as the list shows it, made a name: lower case, spaces and dashes to underscores,
   nothing but letters, digits and underscores ("Front Door" → `front_door`). This is the Reolink case.
4. The same two again ignoring case.
5. The device's own camera never has one.

## Configuration

- **go2rtc**, on the setup page's Connections tab: the address (`http://192.168.1.5:1984`; `http://`
  and `:1984` are added when missing), and a user and password for go2rtc's `api:` basic auth, which is
  off unless somebody set it. The password is written and never shown, and kept out of the diagnostics
  bundle. Saving it lists go2rtc's streams, so a wrong address is found out on the form and not at the
  door.
- **A stream name per camera**, on the same form, one row per camera on the list, empty for the match.
- **Talk through cameras**, a switch, off on a new device, in Privacy & Security on the screen, in Home
  Assistant as `talk_back`, and shown (not changed) on the setup page's Privacy tab like the others. It
  is a live microphone on the LAN, even if only for as long as somebody has tapped Talk, and only to
  whoever holds the one-time address.
- No action. Home Assistant users get the switch; the address is set once, on the page. An action can
  follow if an automation turns out to want one.

## Limits and unknowns

- **Latency is unmeasured.** Reading go2rtc gives a floor of 40 ms (its WAV reader's block) plus 128 ms
  for RTSP cameras taking G.711 (it regroups into 1024-byte packets for the Reolink doorbell's sake,
  `pkg/rtsp/consumer.go`), plus the device's 20 ms frames, plus whatever the camera buffers. It wants
  measuring at a real door before anybody designs around it; the last task does that and writes the
  numbers here.
- **go2rtc has to reach the device** on port 8181. On one LAN it does. Across a VLAN or a Docker bridge
  without host networking it may not, and then go2rtc's POST fails with its own error, which the view
  shows. The routes that reach out from the device instead (an RTSP client offering go2rtc
  `?backchannel=1`, or WebRTC) are the fallback, and are not built here.
- **Cameras whose backchannel is not G.711 at 8 kHz** (Ring and Roborock take Opus; some RTSP cameras
  offer 16 kHz PCM) get no Talk control. go2rtc's `ffmpeg:` source can transcode a 16 kHz WAV to them,
  at the cost of an ffmpeg start and its probing per talk; that is a later task once somebody has one.
- **Reolink** is RTSP backchannel only, on firmware that offers it: the doorbell since 2023 does; reports
  on the 2025 PoE doorbell are mixed (go2rtc #331, still open). go2rtc has no Baichuan source, and
  Reolink's HTTP API has no talk command that anybody documents. Frigate's advice is a second, plain
  `rtsp://` stream in go2rtc for talking, which is what the per-camera name is for.
- **One talker at a time.** A device talks to one camera; two devices talking to the same camera each
  replace the other's play, which is go2rtc's behaviour and is the right one.
- **The Dot** has no camera page, so it has no Talk control and no switch; this is a no-op there.
- **CPU.** One more microphone listener, a 31-tap filter at 16 kHz and a table lookup per sample: well
  under the intercom's cost, which also encodes. Measured with the latency.
- **Muted is muted.** A muted microphone sends zeros to every listener; a talk ends on mute instead of
  sending a camera silence and letting somebody believe they are heard.

## Order of work

1. **G.711 and a filter** (`lib/g711`): A-law, μ-law, the 16→8 kHz halving, the WAV header go2rtc
   reads. Pure functions, tested against the reference encoder's values.
2. **A go2rtc client** (`lib/go2rtc`): list streams, ask for a camera's backchannel, play a source,
   and the device's own address as go2rtc sees it. Tested against an `httptest` go2rtc.
3. **Settings**: go2rtc's address and account, per-camera stream names, and the switch.
4. **The talk itself** (`feature/home/talk.go`): the stream match, the backchannel probe on view-up, the
   one-time WAV on the web port, the start, the watcher that ends it, and the view held while talking.
   Testable end to end with a fake go2rtc that fetches the address the way the real one does.
5. **The Show's camera page**: the Talk control beside Mute, the note, the tap.
6. **The Spot's round camera page**: a second bar above Mute.
7. **The switch and the setup page**: Privacy & Security row, Home Assistant switch, the Connections
   form, the Privacy tab's line, the diagnostics summary; the wake word ignored while talking.
8. **At a real door**: a camera with a backchannel, a person listening, latency and CPU measured and
   written above.

## Smaller than this, and worth having

**Playing a message to the camera** — "I'll be right there" — is the same POST with a TTS or file URL
as `src`, through `ffmpeg:` because a TTS URL is MP3 and the camera takes G.711. No microphones, no
switch, no one-time address. It could land on its own as an action once the client exists.

## Later, not part of this

- **go2rtc as the view's picture source** (`/api/frame.jpeg`, or MJPEG): smoother than Home Assistant's
  camera proxy for every camera, and a different piece of work.
- **The device dialling out** — an RTSP client on `?backchannel=1`, or WebRTC — for a go2rtc that cannot
  reach the device. WebRTC would also be the route to Home Assistant's own two-way audio if it lands.
- **Talking through the intercom to a camera**: the transport is not the hard part, the camera is.
