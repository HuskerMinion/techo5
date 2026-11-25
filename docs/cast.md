# Cast: send a phone's screen or a video to the Show

An Android phone can send a video, or its whole screen with the sound, straight to the Show. Nothing runs
in between: the phone decodes the video and sends small JPEG frames and audio over your own Wi-Fi, encrypted,
and the Show draws them and plays the sound. The app is **TECHO5 Cast**, from
[asmsaifs/techo5-cast](https://github.com/asmsaifs/techo5-cast), which also holds the protocol
([docs/protocol.md](https://github.com/asmsaifs/techo5-cast/blob/main/docs/protocol.md)).

It is **off until you turn it on.** A port that takes pictures and sound from the network is not something to
have open by default.

## Turn it on and pair a phone

1. On the Show, swipe down for the settings, open **Connections**, and turn **Cast from a phone** on. (Or
   use the **Cast** switch on the device's page in Home Assistant, under Configuration.)
2. Tap **Show** on **Pairing code**. A QR code comes up with the Show's address and key in letters beside it.
   A touch, or two minutes, puts it away.
3. On the phone, in TECHO5 Cast: **Add a Show → Scan the code**, then **Check connection** and **Save**.

There is no key to think up: the Show makes a random 80-bit one the first time Cast is turned on, and the
pairing code carries it to the phone. The code holds the key, so it is only on the screen while somebody has
asked for it and is standing there.

After that, in YouTube (or any video app) tap **Share → TECHO5 Cast**, or use **Cast a file**, **Paste a
link** or **Mirror screen** in the app.

## Accepting a phone

The Show always asks before a phone casts: **Cast to this screen?**, the phone's name, and **Decline** and
**Accept** where every page's answers are. Nobody answering for 20 seconds declines. A phone is asked every
time it connects, including after a Wi-Fi drop: the name is the phone's own word and every phone shares the
key, so there is nothing to tell a phone you accepted from another one. Turning Cast off puts a question that
is still up away.

While a cast is on, a swipe in from the left edge of the screen ends it. A second phone is turned away while
one is casting, and a call, a ring or a turn of the assistant takes the screen and the sound for as long as it
needs, with the cast carrying on underneath.

## What the Show shows

- **Over the picture**, for the first four seconds: "Casting: *title*", when the phone says what is playing.
- **In the log** (`/data/techo5-linux/techo5.log`), every five seconds while casting:
  `cast decoded_fps=… painted_fps=… audio_late=… audio_dropped=…`, and why any frames were dropped
  (`late_on_arrival`, `overflow`, `skipped`, `late_after_decode`, `bad`). The phone gets the same counts once a
  second and shows **Smooth** or **Struggling**, and eases its frame rate down when frames are lost.
- **In Home Assistant**: the **Cast** switch and the **Cast state**
  diagnostic sensor: `off`, `waiting`, `asking: <phone>`, or `casting: <phone>`.

## What it can do

The phone does the hard part, so the Show's limit is decoding JPEGs. Measured on an Echo Show 5 (2nd gen) over
Wi-Fi with the daemon and wake word running, real 720p video:

| Phone sends | The Show shows | Bandwidth |
|---|---|---|
| Full-size frames, 15 to 24 fps | about 12.5 fps (decode-bound) | 8 to 11 Mbit/s |
| Half-size frames, 30 fps | 30 fps, no drops | about 4.3 Mbit/s |

The app sends half-size frames at 30 fps by default. The sound is 48 kHz stereo and stays in step: the
speaker's clock runs about 200 ppm off the phone's (12 ms a minute), and the Show corrects for that as
sendspin does, a frame at a time, so a long video doesn't drift.

What can't be cast: protected video (Netflix, Prime Video, Disney+ and other DRM) gives the phone nothing to
play, and mirroring it shows black. YouTube's own Cast button doesn't list the Show: that is Google's
protocol, and a Show can't be a receiver. Use Share.

## Security

- The phone and the Show talk TCP on **port 8940**, encrypted with Noise `NNpsk0` keyed by the pairing key
  (`Noise_NNpsk0_25519_ChaChaPoly_SHA256`). A phone with the wrong key fails the handshake, and the key never
  crosses the network between the phone and the Show. (Choosing one with the `cast_key` action sends it
  over Home Assistant's link to the device, which is in the clear unless that link has an encryption key.)
- On Android the port is opened in the firewall only while Cast is on. On the Linux image the firewall rule
  is always there, and it is the listener that follows Cast: nothing answers on the port while Cast is off.
- The Show advertises `_techo5cast._tcp` over mDNS so the app can list it. A network that blocks mDNS can
  still add the Show by address.
- A stranger on the network who does not have the key gets nothing: no handshake, no screen, no sound. Someone
  who has it still has to be accepted at the screen, every time.
- One phone casts at a time. A connection that has not yet proven the key does not take that place: at most
  4 are in the handshake at once, and one from each address, and each is closed if it has not proven the key
  and said hello within 3 seconds. So somebody without the key who keeps reconnecting cannot keep your phone
  out. A second phone that has the key, while one is casting or being asked about, is told the Show is busy.
- When somebody taps Decline, or nobody answers within 20 seconds, the Show turns that phone away for 30
  seconds without asking again, so the question can't be put up on the screen over and over.
- Memory is bounded: a video frame over 1 MB is refused, frames waiting to be shown are held to 8 MB (the
  oldest goes first), and a frame stamped more than 2 seconds ahead is dropped.

## Choosing the key yourself, or changing it

Usually never needed. The [`cast_key`](actions.md#set-the-cast-key) action sets a key you choose (16 characters
or more, long and random), or, called with no key, throws the old one away and makes a new random one. Either
**unpairs every phone**: they have to scan the pairing code again.

## Troubleshooting

| You see (on the phone) | What it means |
|---|---|
| "Can't reach *Show*. Is Cast on, and is the phone on the same Wi-Fi?" | Cast is off, or the phone is on a different network, or a guest Wi-Fi that isolates devices. Turn **Cast from a phone** on; or add the Show by its address. |
| "The key doesn't match *Show*." | The key was changed (`cast_key`, or the Show was reset): scan the pairing code again. |
| "*Show* refused: busy with another phone" | Another phone is casting, or is waiting to be accepted. Try again when it is done. |
| "*Show* refused: declined" | Somebody tapped Decline, or nobody tapped Accept within 20 seconds. |
| "*Show* refused: declined a moment ago; try again in a little while" | The phone was declined less than 30 seconds ago. Wait a little and try again. |
| "*Show* refused: the screen is not ready" | The Show is still starting. Try again in a moment. |
| Picture stutters, the pill says **Struggling** | Weak Wi-Fi or a busy Show. The app lowers its frame rate by itself. |

Nothing about Cast in Settings → Connections means the build has no receiver: the Echo Dot and Echo Spot
builds have none, and neither does a device that has not had a release that includes it. The Panel builds
(the Skylight and Android tablets) include it, as the Shows do.

## Trying a build that isn't released yet

A Show only has the receiver once a TECHO5 release that includes it is installed. To try it from source, build
the daemon and bind it in place until the next reboot ([building.md](building.md), section 3). The Show has no
sftp server, so where `scp` fails, stream the file:

```
ssh -i techo5_ed25519 root@<address> 'cat > /tmp/echod-test' < bin/echod-arm
ssh -i techo5_ed25519 root@<address> 'chmod +x /tmp/echod-test && mount --bind /tmp/echod-test /usr/local/bin/techo5 && killall techo5'
```

The receiver is `echod/internal/feature/cast`; its `secure.go` and `proto.go` are copies of the
[techo5-cast](https://github.com/asmsaifs/techo5-cast) `wire/` package and have to match it on the wire.
