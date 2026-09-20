# Standing on its own: a plan

A TECHO5 device today is a Home Assistant satellite. Turn Home Assistant off and most of it stops:
the clock keeps time and alarms still ring, and that is the lot. This is a plan for the parts that
could work without Home Assistant at all — a clock radio with timers and alarms — so that a used Echo
Show is worth reflashing to someone who does not run Home Assistant, and so that an outage leaves a
useful device rather than a clock.

Nothing here is started. It is written to be argued with.

## What already stands alone

Checked in the code, not remembered:

- **The clock.** `ntpd` against `pool.ntp.org` sets the time at boot (`boot.sh`, with `NTP_SERVER`
  overriding the pool), and it free-runs from there. Needs the internet, not Home Assistant. What is
  missing is not the time but the **zone**, below; and a network that blocks outbound 123 or runs its
  own server has nowhere to say so, so the NTP server wants to be a setting alongside the zone.
- **Alarms.** Set on the screen, kept in the device's own config, and rung from the device's own
  clock — `feature/alarm` says so in as many words, and it was built that way on purpose.
- **Updates.** `update.Fetch` reads the release from GitHub directly. The update entity is a Home
  Assistant convenience; the General card's *Check now* is not.
- **Wi-Fi.** Joined from the screen, no Home Assistant involved — on a Show or a Spot. A Dot has no
  screen, so its network can only be set over USB at install time (`install-dot.py`).
- **Voice**, if the pipeline is local — but that is a Home Assistant pipeline, so it is not standalone.

## What does not, and why

- **Timers.** `feature/timer`'s first line: *Home Assistant keeps the timers, the device counts them
  down.* The counting and the ringing are already local; only the list is not. Nothing on the device
  creates a timer.
- **Radio.** The device streams the audio itself, but the station list comes over Home Assistant's
  websocket (`feature/home/local.go`, Radio Browser through Home Assistant) and playing one is
  `hass.PlayMedia` against this device's player. No Home Assistant, no radio.
- **The time zone.** `feature/timezone` takes the zone from Home Assistant and from nowhere else. A
  device that has never met a Home Assistant is on UTC, and there is no way to change that from the
  screen. This is a gap in its own right, and the first thing a standalone device would show wrong.
- Weather, cameras, the slideshow, voice: all of them are Home Assistant's, by design. Out of scope.

## M1 — Timers on the device

The easiest, and the one that makes the least noise. The ringing, the countdown ring, the stop word
and the stop button all exist; what is missing is a list of the device's own timers and a way to
start one.

- A local timer in the config beside the alarms: label, length, when it was started.
- A screen to set one — the alarm editor's shape, minus the days of the week.
- `feature/timer` merges the device's own with Home Assistant's, so a house with both sees one list
  in the order they finish.
- "Set a timer for ten minutes" by voice still goes to Home Assistant, because the sentence does.
  Nothing about voice changes.

Open: whether a device timer should appear in Home Assistant as a countdown entity (nice, more
surface) or stay invisible to it (simpler, and honest — it is the device's).

## M2 — Radio without Home Assistant

The device already opens the stream, so this is about knowing what to open.

- **Favorites kept locally**: name, stream URL, optional logo. The Favorites list plays them directly
  rather than calling a Home Assistant script.
- **Home Assistant still wins where it is present.** A device with Home Assistant keeps today's lists
  and its Radio Browser stations; the local list is what a device falls back to, and what a
  standalone device has.
- **Finding stations without Home Assistant.** Radio Browser has a public API
  (`all.api.radio-browser.info`, mirrors found over DNS SRV) that Home Assistant's integration is
  itself a client of. The device can search it directly: by name, by country and state, by tag.
  - *Location.* Today's "stations near home" uses Home Assistant's own coordinates. Standalone, the
    device does not know where it is. Options, least creepy first: the owner picks country and state
    (or types a city or postal code) in setup; or the device asks an IP geolocation service, which
    means telling a third party its address — **opt-in at most, never the default**, and it says so
    on the screen.
  - *Caveat to check before promising it:* Radio Browser supports a geographic search, but a station
    only turns up in it if whoever added it filled in coordinates, and many have not. Country and
    state may well beat distance in practice. Worth measuring against a few real places before the
    UI implies distance is meaningful.
  - Ask for `hidebroken=true`, keep the list to a sane size, cache it, and send the User-Agent
    Radio Browser asks clients to send.

Open: whether to lean on Radio Browser at all when standalone, or to treat a hand-entered list as the
only local source and leave discovery to Home Assistant. Discovery is what makes it pleasant; a
dependency on one volunteer-run service is what makes it fragile.

## M3 — A setup page on the device

Typing a stream URL on a five-inch screen is miserable, so: a small web server on the device, on the
port that already serves the camera and the screenshot (8181).

The shape:

- **Off by default**, like the camera and screen pages. A switch on the device — Settings → Privacy —
  and in Home Assistant where there is one.
- **It turns itself off again.** A setup page that is on for fifteen minutes after you ask for it is
  a different risk from one that is on for a year.
- **A code shown on the device's own screen** to get in. Not a password to remember; a six-digit
  number on the panel, proving whoever is configuring it is standing in front of it. Rate-limited,
  and a new one each time the page is turned on.
- **Plain HTTP on the LAN.** No certificate that would be worth the trouble on a device with no name.
  Say so plainly in the docs rather than implying it is private.
- **It writes only what it says it writes**: radio favorites, the time zone, the name, maybe the
  Wi-Fi. Never SSH keys, never the Home Assistant key.
- Small enough to serve from the binary: one page, no framework, no fonts to fetch.

This is the piece with real security surface, so it is the one to design slowly and to write down
before writing code.

## M4 — The time zone on the device

A standalone device has to be told where it is, once. Either a picker on the screen (long list,
awkward, but no server needed) or a field on the setup page, with Home Assistant still overriding it
when a Home Assistant is present. Small, and it blocks M1 and M2 being any use in practice — a clock
radio on UTC is a broken clock radio.

## Wi-Fi for a device you are handing to someone else

The case: a device set up here, wiped of its network, and given away. A Show or Spot can join a new
network from its own screen, so this is a convenience there and a genuine hole on a Dot.

What the image already carries: `wpa_supplicant`, `wpa_cli`, `wpa_passphrase` and the whole `iw` and
`iwlist` set. **No `hostapd`, no `dnsmasq`** — a hotspot is not a configuration change, it is new
software in the image, and it only works at all if the vendor driver does AP mode. That driver is
Amazon's `mt76x8_wlan.ko` and an Echo Show never offered tethering, so it may simply not be built in.
Run on a Show, 2026-09-20, the driver says:

    Supported interface modes: managed, AP, P2P-client, P2P-GO
    valid interface combinations: #{ managed, AP, P2P-client, P2P-GO } <= 2, total <= 2, #channels <= 2

It registers three phys, and an **`ap0` interface already exists in AP mode** beside `wlan0`. Two
interfaces on two channels are allowed at once, so a hotspot could run *while* the device stays on
the house network rather than instead of it — which makes a "join my hotspot to move me to your
Wi-Fi" flow possible without dropping off the network first. Still to check on a **Dot**, which is
the device that actually needs this and may not carry the same module.

Three answers to the same problem, cheapest first:

1. **More than one saved network.** `wpa_supplicant` takes several network blocks; the screen only
   ever writes one. Adding a second — their network, before the device leaves the house — solves the
   handing-over case with no new software and no hotspot at all.
2. **Bluetooth provisioning (the Improv standard).** A phone or a Chrome tab hands the credentials
   over BLE; the device already runs BlueZ and its own scanner. This is the only one of the three
   that helps a **Dot**, which is where the hole actually is.
3. **A hotspot to join, with a page to fill in** — conditional on the `iw list` answer above, and the
   most moving parts by far: hostapd, a DHCP server, a captive page, and a rule for when to stop
   being an access point and try the real network again. It pairs with the setup page in M3, since
   both want the same small web server.

## Talking to the other devices in the house

Two features, one shape apart.

**Announce to all** is one-way and short: a message plays in every room. Live audio to six devices at
once is a lot of streams for something nobody talks back to, so record the clip on the device that is
speaking, push it to each of the others, and let them play it behind a chime. A moment's delay before
it starts and nothing to go wrong after; a device that is asleep, busy or on a call takes it when it
can, or refuses and says so.

**Intercom** is two-way and one-to-one, and most of it already exists. The phone feature runs a full
SIP stack with SRTP, and SIP needs no provider: one device can invite another by address with no
registrar in the middle. Today's "calls between your own devices" go out to the provider and back,
which is the wrong shape for two devices ten feet apart — the same stack pointed at the LAN is a
house intercom that works with the internet down.

**Both want the devices to know each other.** They already advertise themselves over mDNS for Home
Assistant and for multi-room audio; a third record, or a flag on an existing one, is enough to build
the list without Home Assistant being asked.

**The part to get right is trust.** A device that plays audio it was sent is a device anything on the
network can make talk. A secret shared by the house, written at install and again from the setup
page, held by every device and required on both ends. Nothing plays from a peer that cannot show it.
Beyond that: a name for each device that the announcement says it came from, a way to refuse
(Do Not Disturb), and a limit on how often a peer may interrupt.

**What this does not need:** Home Assistant, the internet, or an account. That is the point of it.

## The phone, once Home Assistant is gone

Worth writing down, because it is better than expected. The SIP account is the daemon's own — it is
kept in its own file and registers with the provider directly, with no Home Assistant in the path —
and contacts are a file on the device, so an unpaired device **still rings for incoming calls and can
still call the names it already has** from the screen.

What it cannot do alone is provisioning: the account (`phone_account`) and the contact list
(`phone_contacts`) are both set by Home Assistant actions, and dialing by voice needs a pipeline.
Signing in and editing contacts therefore belong on the setup page — a SIP password is the worst
thing anyone will ever type on a five-inch screen.

## M5 — What it then says on the tin

If M1 to M4 land, the pitch changes: *a local clock radio with alarms and timers, which becomes a
Home Assistant voice satellite if you run Home Assistant*. That is a much easier first step than
"set up a voice pipeline", and it is true of a device that has never seen Home Assistant.

The README, the getting started guide and the installer all assume Home Assistant today, down to the
installer printing an encryption key and waiting to be paired. A standalone path means an install
that skips the key, and a first-run screen that offers *set this up on its own* beside *connect it to
Home Assistant*.

## Non-goals

- Reimplementing Home Assistant on the device. Weather, cameras, media libraries and voice stay
  Home Assistant's.
- A general web UI. The setup page configures the few things that cannot be typed on a five-inch
  screen; it is not a second front end for the device.
- Cloud anything. No account, no telemetry, and no phoning an IP geolocation service unless the owner
  asked for it.

## Order, and why

M1 first because it is small, wanted by people who already run Home Assistant, and touches nothing
risky. M4 next because everything else is wrong without it. Then M2, which is the feature people
would actually notice, with M3 as its awkward prerequisite. M5 is documentation and a conversation
about what this project claims to be.
