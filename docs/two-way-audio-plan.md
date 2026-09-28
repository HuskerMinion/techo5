# Two-way audio: talking back through a camera

A camera's own sound is heard on the device while its view is up. This is the other direction:
somebody presses a control on the camera page, speaks, and the device's microphones go out to the
camera's own speaker. Answering the doorbell without a phone in hand.

**Status: a plan. Nothing of it is built.** It follows the camera's own sound (see `actions.md`), and
the maintainer's note on that work, which asked for this as a separate piece.

## What is already there

Most of the hard parts, and none of them in the way this usually is:

- **The microphones**, 16 kHz mono, S24_3LE, 20 ms frames (`hardware/mic`, `Rate` and `FrameSamples`),
  through a beamformer, a denoiser and **echo cancellation against the speaker's own output**
  (`hardware/mic/cancel.go`, and `mic/webrtc.go` for WebRTC's canceller in a helper process). Echo
  cancellation is the thing that makes this a conversation rather than a walkie-talkie: the camera's
  audio is playing *while* somebody talks over it, and the canceller has the loopback reference to
  subtract. Every other part of the daemon that speaks already feeds it.
- **Capture to the network**: the house intercom already takes those frames, frames them (`msgAudio`,
  20 ms of 16 kHz, `feature/phone/intercom.go`), sends them over a connection and plays what comes
  back the other way. That is the nearest thing to this that is already built — see
  [intercom-plan.md](intercom-plan.md) — and the *capture → framed PCM → out* path in it is built and
  tested on four kinds of device.
- **The camera's own sound**, which is the same session's other half: Home Assistant converts the
  camera's stream to a live WAV, the device plays it over whatever is playing, ducked, with a Mute
  control on the view. Talking back has to coexist with it, not replace it.
- **A web port with switches in front of everything it serves** (`feature/web`): the camera's stills,
  the panel screenshot, the setup page, and the intercom's own HTTP path. Each has its own switch,
  off on a new device.
- **The camera page's control plumbing**: the Mute control records where it was drawn and tells a tap
  from the tap that closes the view (`display/render_camera.go`, and the Spot's own pair of the same
  functions). A Talk control is the same machinery with a different label.

## What go2rtc gives us

go2rtc is already the thing most Home Assistant camera users have in front of their cameras, and its
**backchannel** is the return path: audio in, to the camera's speaker, transcoded to whatever codec
that camera takes. Three ways in, in the order worth considering:

1. **Stream to camera** — go2rtc *pulls* an audio source and plays it on the camera:

   ```
   POST http://<go2rtc>:1984/api/streams?dst=<camera>&src=ffmpeg:<uri>#audio=<codec>#input=file
   ```

   The source can be a file, a URL, or a live stream, and ffmpeg transcodes it to the camera's codec.
   **This is the route to build first**, because the source can be *this device serving its own
   microphone*: nothing new on the wire, no protocol to speak, and go2rtc does the codec work.

2. **RTSP server backchannel** (go2rtc #1432): with `backchannel=1` on a stream, a client may send
   audio *into* go2rtc over RTSP. That is the tidier shape for a long session — one connection, no
   per-press API call — but it means an RTSP client on the device that sends backchannel RTP, and a
   codec chosen by negotiation rather than by us.

3. **`exec:` backchannel** — go2rtc pipes the incoming audio to a command's stdin as PCMA or PCM
   48000. Only useful when the audio is produced *on the go2rtc host*, which ours is not. Worth
   knowing so nobody re-derives it.

The browser's own two-way audio — the WebRTC card, and Home Assistant's newer native support — is the
same backchannel fed from a dashboard microphone. Good for a person at a computer; no use to a device
in a hall, and it is why this is the device's own job.

Two details that make route 1 fit this device exactly:

- The `pcm` source's backchannel defaults to **PCM 16 kHz** (`pkg/pcm/backchannel.go`), which is the
  microphones' native rate and format. Nothing has to be encoded. The camera's own codec (G.711,
  Opus, whatever it speaks) is go2rtc's problem.
- A live WAV whose sizes were written before the length was known is exactly what Home Assistant hands
  the device for the camera's own audio, and the device reads it today (`readOverOnce`). Serving one
  is the same file read the other way round.

## How it would work

1. Somebody opens a camera and presses **Talk** on the view.
2. The device starts serving its microphones as a live WAV on its own web port, behind the same kind
   of switch as the rest of it, and for as long as the talk lasts.
3. The device asks go2rtc to play that URL on the camera: the `POST /api/streams` call above, with the
   camera's stream name from its own list of streams, or from configuration where the name is not
   what the camera is called.
4. go2rtc pulls the stream, transcodes PCM 16 kHz to the camera's codec, and the person at the door
   hears the room.
5. All of the above happens *while* the camera's own audio is playing, ducked, on the device. The
   echo canceller has the loopback, so the microphones hear the room rather than the doorbell.
6. Talk is released, the WAV stream ends, the ffmpeg pull ends with it, and the camera's speaker falls
   silent. Nothing has to be torn down on the go2rtc side if the source is what ends.

Hearing and talking at once is the point, and it is the one thing this design gets for free that a
phone-based doorbell usually does not.

## Configuration and where it lives

**One go2rtc base URL per device, and one stream name per camera** — not one URL per camera. The URL
and its credentials are set the way the Home Assistant URL and token are: an action (`home_go2rtc`)
and a settings row. Credentials, where go2rtc's API has them, are stored as a secret and kept out of
log lines.

The stream name is the only per-camera thing, and it is mostly discoverable rather than configured:

- `GET <go2rtc>/api/streams` lists every registered stream, keyed by name. Through Frigate it is
  `GET <frigate>:5000/api/go2rtc/streams`, and there the names are Frigate's own camera names —
  which is why a camera called `back_door` in Frigate is a stream called `back_door`.
- Match that list against the Home Assistant camera by entity id, then object id, and keep a
  per-camera override for the ones the match misses. **Nothing in a camera entity says which go2rtc
  stream it is**: Home Assistant does not publish camera stream sources over its API, which is why
  community integrations and an architectural proposal for exactly that exist, and why a match by
  name plus an override is the honest design rather than a lookup.

Where the camera's *RTSP link* comes from is go2rtc's business, not this device's: its own `streams:`
config, Frigate's camera config, or its `hass:` source, which imports camera links out of Home
Assistant's config files. So the one setting somebody has to make, once per camera, is outside this
device — a camera that exists only in Home Assistant and nowhere in go2rtc has no stream to talk
through until somebody gives it one.
- **A switch**, off on a new device, for serving the microphones over the network at all. It belongs
  with the other Privacy switches, not with Display: this one is a live microphone on the LAN.
- The **Talk control** is on the camera page, in the corner opposite the Mute control, and only while
  the view is up.

## Limits and unknowns

- **Latency is unmeasured.** The path is microphone → device → go2rtc → transcode → camera → its
  speaker, and nothing in that chain is a long buffer, but "should be a few hundred milliseconds" is
  not a measurement. It wants timing on real hardware with a real doorbell before any of it is
  designed around.
- **The picture stays a slideshow.** The view is Home Assistant snapshots at a few frames a second,
  which is what this panel can decode. A conversation does not need more, but nobody should expect
  go2rtc's WebRTC latency on the *picture* because its backchannel is being used for sound.
- **Half of the cameras cannot be talked to at all.** The backchannel exists only where go2rtc's
  source supports it (ONVIF Profile T, Tapo, DVRIP, ISAPI, Doorbird, Ring, Wyze, Tuya, Xiaomi). A
  camera that cannot is found out by asking, not by pre-checking: the Talk control should not be
  offered on a view whose camera the device has no reason to think it can talk through, and the log
  should say plainly when go2rtc refuses.
- **One talker at a time**, and for a reason: two of them are two streams into one camera speaker.
- **CPU.** The intercom costs a Dot 15–20% more of a budget that is already the tightest of any
  device, and it pauses the wake word to do it. The same trade applies here, and the Dot gets its own
  go/no-go.
- **A talk that outlives its view.** The view can time out, another camera can replace it, or the
  screen can be tapped away while somebody is mid-sentence. Whatever ends the view ends the
  microphones going out, the same way it ends the camera's sound.
- **Whether go2rtc lists a camera's stream at all before something has played it.** If the Home
  Assistant integration registers sources on demand, a first Talk press would have nothing to match
  against — in which case the per-camera override is not an escape hatch but the answer, and the
  settings row should say so.
- **The camera's own audio going out of sync with the picture** — the sound is a live stream and the
  picture is snapshots — is already true of the one-way feature and is not made worse here.

## Order of work

1. **T1 — the microphones as a stream.** Serve them as a live WAV on the web port, behind a switch,
   and prove it with `curl | wavstats` and by listening to it. This is the piece everything else
   needs, it is testable with no camera at all, and on its own it already makes the device a camera
   whose audio can be pulled by anything on the LAN.
2. **T2 — the Talk control and the call.** The control on the camera page in the Mute control's
   pattern, the go2rtc URL and stream name in configuration, and the `POST /api/streams` when it is
   pressed. Tried against a camera with a backchannel and a person listening at the door.
3. **T3 — what the world sees**: the action, the settings row, the switch in Privacy, and the docs.
4. **T4 — the other shapes of device**: the Spot's round page (its own control geometry) and the Dot
   (CPU, and the wake-word pause). A Dot has no camera page at all, so this may be a no-op there.
5. **T5 — measure latency** on real hardware, with the numbers written into this document rather than
   into a commit message.

## Smaller than this, and worth having

**Playing a message to the camera** — "I'll be right there" — is the same go2rtc call with a file or a
Home Assistant TTS URL as the source, and no microphones, no switch and no Talk control. It could
land first, on its own, as an action: show the camera, hear it, and press a button that says one
thing to whoever is at the door.

## Later, not part of this

- **The go2rtc stream as the view's own video source**: `/api/frame.jpeg` snapshots, or an MJPEG
  pull, would be smoother and lower-latency than Home Assistant's camera proxy. It is a different
  piece of work with a different cost (JPEG decoding on the device), and it would help every camera
  view, not just this one.
- **WebRTC on the device.** It would make the device a first-class go2rtc client in both directions
  and remove the served-microphone trick. It is also a stack, a negotiation and a codec — a
  project, not a step.
- **Talking through the intercom to a camera** — a device-to-device call that ends at a camera
  instead of another device. The transport is not the hard part there; the camera's backchannel is.
