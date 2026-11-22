# Plex

TECHO5 can be a player a Plex app casts to. Link the device to a Plex account and it appears in the
Plex apps -- Plex Web, Plex Desktop, Plex for Android TV, Plexamp -- beside the other players in the
house, ready to take a song, an album or a film.

It plays what it is sent with its own parts: music becomes a received track, so it is on Now Playing
with the cover and the title, a voice reply ducks it and an alarm sounds over it, and a film plays full
screen in the device's video player. What is playing is reported back to the Plex server, so the
server's Now Playing page and the phone's transport stay in step.

Only a device with a screen is a player. The Echo Dot and the Echo have no build of this at all
(`//go:build !dot`), no port, no discovery answer, no card on the setup page: a speaker is not a
screen to watch a film on.

## Linking an account

Two places, the same code behind both:

- **On the device:** Settings → Connections → **Plex** → *Link Account*. The screen shows four
  characters as large as it can, and *Go to plex.tv/link and type it in*. Type them at
  [plex.tv/link](https://plex.tv/link) on a phone or a computer; the screen notices within a few
  seconds and says *Linked as your-name*. *Cancel* puts the card away and ends the link.
- **On the setup page:** Sound → **Plex** → *Link Account*. The card shows the code with a link to
  plex.tv/link, and reloads itself once the account is linked.

Unlinking is the same row: *Unlink Account* on the screen, or the button on the setup page. The token
is forgotten, the player stops listening at once and the device is taken out of the account's list.
The device's identifier stays, because it is the device's name rather than the account's, so linking
the same account again comes back as the same player.

The password is never typed into the device. It asks plex.tv for a code, waits for the token that
comes back once somebody has entered the code, and keeps the token and the account's name. Nothing
else of plex.tv's is kept, and the token is never shown on a screen, on the page or in the log.

## What plays

- **Music** (Plex and Plexamp): MP3, FLAC and WAV files are fetched as they are. Anything else -- AAC,
  ALAC, Ogg -- is asked for as MP3 from the server, which is what the device's decoder plays, rather
  than leaving a library of AAC silent. Cover art is fetched for Now Playing at the same time.
- **Films and episodes**: the server is asked for a stream this hardware decodes -- H.264 at up to
  720p, or 1080p on the Skylight Calendar, with AAC, MP3 or AC-3 sound, in an MPEG-TS over HLS -- and
  the film plays full screen, with its sound and with subtitles burned into the picture when the app
  has them on. A 1080p film on a Show is sent downscaled rather than in frames the device would drop.
- **Pause, resume, stop, seek, skip.** Pausing on the screen pauses the phone's transport too, and the
  other way round: the timeline is read from the device's own player every second, so a pause from a
  voice turn or an alarm shows up in Plex. A seek restarts the stream at the new second (audio) or
  asks the server for a new transcode at it (video), which costs a second of buffering. Skip next and
  previous walk the play queue the app made.
- **Volume** is the device's own: the phone's slider, the speaker's buttons and Home Assistant's
  volume control are one control.
- **Sound and subtitle choice** is passed to the server, which remembers it for the next play, and the
  film restarts at the same second with the other track, since a track cannot be changed inside an
  HLS stream.

Video is off unless the device's own **Video** switch is on (Settings → Screen, or the setup page),
the same switch Home Assistant's `play_video` action needs.

## How it is found

Two ways, and both are used by Plex's own clients:

- **On the home network**, Plex's discovery ("GDM", which is not SSDP): a controller sends a bare
  `M-SEARCH * HTTP/1.0` to `239.0.0.250:32412`, and the device answers it from that same port with
  what it is and where its control server listens. It also announces itself (`HELLO`) to
  `239.0.0.250:32413` when it starts and every 30 seconds after, so a controller that started later
  still finds it. This needs no account at all -- but the device only answers searches while an
  account is linked, since that is when it is a player.
- **Through plex.tv**, for an app that is not on the same network: the device keeps a socket open
  from the account's side, and the commands a phone sends down it are run against the same handlers
  the network's commands use.

The control server is on **TCP 32500**, discovery on **UDP 32412-32413**. All three are opened in the
device's firewall only while an account is linked (`feature/plexcast` opens them by name, and the
Alpine image's `techo5-firewall` opens the same ports at boot). Nothing listens on any of them with no
account linked.

## Limits

- **One thing at a time**, as a player has: a second cast replaces the first.
- **Photos** are not shown. The timeline advertises video and music; a photo cast is accepted and
  does nothing.
- **720p** is the cap on the Show and the Spot, and 1080p on the Skylight Calendar: the panel decides
  it, and a board with a faster decoder and a bigger screen asks for more (`transcode_board.go`). The
  device does not do the transcoding: the Plex server does. A server too slow to transcode will
  stutter a film, which is the server's doing and not the Show's.
- **H.264 only**, even on the Skylight, whose decoder also plays H.265: a server may encode to any codec its
  client lists, and Plex's HEVC encoding is hardware-only, needs a Plex Pass and is still experimental,
  under which a board that listed it could be sent a film it cannot play. The gain HEVC would bring is
  on the server's side, and this device asks for 720p (1080p on the Skylight) over a home network.
- **Plex's Companion protocol is not a published API.** What is implemented is what Plex's own
  players answer (see the notes at the top of `echod/internal/feature/plexcast`), verified against
  them. A future Plex client that expects something else would need this code changed; it is written
  so that the commands, the discovery answer and the transcode profile are each in one place.

## If it does not work

- **It is not in the Plex apps' player list.** Check the account is linked (Settings → Connections →
  Plex says *Linked as ...*). On the same network, a firewall between the phone and the device that
  drops multicast or UDP 32412 is the usual cause; Plex Web and Desktop can also be pointed at the
  device by address, which needs TCP 32500 only.
- **It casts but nothing plays.** For a film, check **Video** is on. For music, the device's log
  (`/var/log/techo5.log`, `grep plex:`) says what the server answered: a library whose files the
  server cannot reach is answered with an error, not a stream.
- **The phone does not see it from away.** Registering the device's address with the account is best
  effort (Plex has no published API for it); casting from the same network always works, and casting
  from away needs the account's socket, which is opened as soon as an account is linked.
- **It disappeared after a router reboot.** It comes back: the discovery answer retries until it can
  bind, the socket reconnects with a backoff, and a new network address on the device restarts both.
