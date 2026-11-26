# TECHO5 add-on API plan

A local interface that installed add-ons use to extend the device: commands Home Assistant and voice
can call, words said on the device, its own timers, settings on the setup page, entities, and later a
screen drawn by the daemon. Meals, the Mealie add-on from #720, is the first user.

**Status (2026-11-26):** stage 1 (`api1`) built on `feat-addon-api1`, as written down in
[addon-api.md](addon-api.md); stages 2 and 3 not started. Where stage 1 differs from this plan, the
section [Stage 1 as built](#stage-1-as-built) at the end says how and why.

## What is already there

- **Safe running.** `feature/addons` installs only from the signed catalog and runs each add-on as a
  uid of its own, with no_new_privs, ulimits, its ports in `TECHO5-ADDONS` and a bounded log. Consent
  is a hash of the listing (`catalog.Shown`); an update that asks for more (`Asks.More`) asks again.
- **Knowing the caller.** `lib/peeruid` (`InstalledUID`) and `lib/panelsock` (`SO_PEERCRED`).
- **`requires` in catalog format 2**, so API levels can come one at a time.
- **Ways in and out.** Actions (`esphome.<device>_<action>`), bus events (`component.Fire`, as
  `esphome.techo5_gesture`), entities, followed states (`feature/hastate`). Entity, action and state
  lists are read once per connection, so a change takes a reconnect, as an install already does.
- **Device timers** (`timer.Start`, `Cancel`, `List`), rung by `ring`, stopped by the stop word, on the
  clock and the LED ring, saved only when one is set or ends.
- **Speech** through Home Assistant's announce on this device (`remind.Say`), under a speech claim that
  ducks the music and yields to the stop word.
- **A drawn screen** on the Show 5 (960×480), Show 8 (1280×800) and Spot (480×480). Drawer tabs come
  and go with what is set up (To-do), the model for an add-on's tab.
- **A signed local API**, `feature/remote`, and its rule: nothing that opens a door, listens, watches,
  records, updates or restarts.

## Goals and non-goals

Goals:

1. **What Kiosk Satellite plugins can do, for add-ons like Cook Mode:** commands from voice and Home
   Assistant, speech, timers, keep-awake, settings, entities, a screen of its own and a card over the
   clock.
2. **Safer than Kiosk Satellite.** Its plugins run inside the app with no sandbox, can draw over the
   system's own screens, and write to Home Assistant as the app's user with no scope. Here an add-on
   stays a separate process under its own uid, every capability is shown on the consent screen and
   enforced by the daemon, and the daemon draws every pixel.
3. **No new device settings.** What an add-on adds appears when it is installed and goes when it is
   removed. Its own settings belong to it, not to `state.json`.
4. **Each stage ships alone.** Stage 1 is useful with no screen at all, and on a Dot.

Not goals: add-ons drawing pixels themselves, a web view, code loaded into the daemon, add-ons
installed from Home Assistant or by voice, and reading the microphone or camera through the API.

## Transport and identity

**The socket.** For each running add-on with an `api` in its manifest, the daemon makes
`/run/techo5-addons/<id>/`, owned by the add-on's uid and `0700`, and listens on `api.sock` inside it.
The path goes to the add-on in `TECHO5_ADDON_API`. `/run` is RAM, so nothing touches the flash. The
folder goes when the add-on stops.

**Who is calling.** On every accept the daemon reads `SO_PEERCRED`. A uid that isn't the one this
socket was made for, or isn't an installed add-on that is switched on, is closed at once. Root is
refused too: an add-on that asks for root still talks as its id, through its socket, but a root shell
can't pose as it. The add-on id comes from the uid, never from anything in a message.

**Framing.** Each message is a 4-byte big-endian length, then that many bytes of UTF-8 JSON. Strict
decoding, as the catalog: unknown keys are refused.

| Message | Shape |
| --- | --- |
| Request (add-on to daemon) | `{"id": 7, "op": "timer.start", "args": {…}}` |
| Response | `{"id": 7, "ok": true, "result": {…}}` or `{"id": 7, "ok": false, "error": {"code": "denied", "message": "…"}}` |
| Event (daemon to add-on) | `{"event": "command", "data": {…}}` |

The first request on a connection is `hello`, with the add-on's API version; the daemon answers with
its own, the capabilities granted, and the device's family, screen size and language.

**Errors** are a fixed set: `bad_request`, `denied` (capability not granted), `unknown_op`,
`too_large`, `rate_limited`, `busy` (the speaker or screen is taken), `unavailable` (Home Assistant not
connected), `internal`.

**Limits.** At most 64 KB a message (256 KB for an image), 20 requests a second with a burst of 40,
4 connections per add-on, 64 requests outstanding. Past a limit the request is answered
`rate_limited` or `too_large`; an add-on that keeps on is disconnected for a minute, and its card says
so. The daemon reads each connection on its own goroutine with a deadline, so one slow add-on can't
hold up another or the daemon.

**Versioning.** `addon.json` gains `"api": 1`. The catalog names what a version needs in `requires`:
`api1` for stage 1, `screen1` for stage 2, `voice1` for stage 3. A device skips a version that requires
a level it doesn't speak, so an add-on can ship a newer version without breaking older devices. A new
op inside a level is additive; changing one is a new level.

## Capabilities

`addon.json` names what the add-on uses in `capabilities`, and the catalog entry repeats them, so
`Manifest.Matches` refuses one that differs. Each is one line on the consent screen, joining ports,
root and extra files in `catalog.Shown` and `Asks`: an update that adds one, or widens one (another
entity, another service), asks again. The daemon checks each request against them; none is a label.

| Capability | On the consent screen |
| --- | --- |
| `commands` | Adds commands Home Assistant and voice can call: *next step, start timer…* |
| `speak` | Says words on this device |
| `sound` | Plays short sounds on this device |
| `timers` | Sets timers that ring on this device |
| `keepawake` | Keeps the screen on while it is in use |
| `settings` | Has settings of its own on this page |
| `entities` | Adds entities to Home Assistant: *list* |
| `ha.read` | Reads these from Home Assistant: *entity ids or domains* |
| `ha.call` | Asks Home Assistant to run these actions: *services* |
| `screen` | Adds a page to this device's screen |
| `card` | Can show a card over the clock |
| `voice.events` | Knows when you talk to the device (and, if asked, *what you said*) |

`voice.events` with transcripts, `ha.call` and `ha.read` of anything outside the add-on's own entities
are shown in bold, as root is today.

## Stage 1: no drawing (`api1`)

**Commands.** `addon.json` declares them, each with a name and its arguments (string, number, bool;
required or not; a string's longest length):

```json
"commands": [
  {"name": "step", "args": {"to": {"type": "string", "max": 16}}},
  {"name": "start_cooking"}
]
```

They can be called three ways, and each reaches the add-on as a `command` event with the arguments
checked against the schema:

- **An action**, `addon_command` (`esphome.<device>_addon_command`, with `addon`, `command` and `args`
  as JSON), which a sentence trigger, a script or an intent calls. The action answers with what the
  add-on answers (up to 1 KB of text), so "next step" can have Home Assistant read the step aloud on
  the device that heard it. A blueprint in `docs/` makes the sentence triggers.
- **A button** entity per command with no required arguments (`button.<device>_<id>_<command>`).
- **Gestures**: there is no direct binding, and there won't be one, which would be a setting. The hold
  on the clock, the palm and the double clap already go to Home Assistant as `esphome.techo5_gesture`,
  and an automation calls `addon_command` from there.

An add-on not running answers `unavailable`, so a voice command says it rather than nothing happening.

**Events out.** `event.fire` puts `esphome.techo5_addon` on Home Assistant's bus with `addon`, `name`
and up to 1 KB of data, at most 2 a second.

**Speak.** `speak` takes up to 400 characters. The daemon asks Home Assistant to announce it here, as a
reminder does, so it is spoken by the household's TTS, ducks the music, waits behind a turn, a call or
a ring, and is stopped by the stop word. One add-on has at most 3 queued; `speak.cancel` drops them.
It needs Home Assistant (`unavailable` without it). Not at all in quiet hours (`busy`).

**Sound.** `sound.play` plays one of a fixed set (`chime`, `done`, `error`, `tick`) through the speaker's
chime path, or a WAV from the add-on's extra files (mono 16-bit, at most 5 seconds and 512 KB), pinned
by the catalog like any extra file. At most one a second; never in quiet hours.

**Timers.** `timer.start` (name, duration up to 24 hours), `timer.cancel` and `timer.list`. They are
the device's own timers: on the clock and the LED ring, rung by `ring`, stopped by the stop word and the
Stop button, and saved like the others, once when set and once when they end. Each carries its owner,
so an add-on lists and cancels only its own, at most 8 at once, and gets a `timer` event when one
rings and when it is stopped. Removing the add-on cancels its timers.

**Keep-awake.** `awake.hold` keeps the screen from dimming and going out for up to 30 minutes and
answers a handle; `awake.renew` extends it, `awake.release` ends it. It ends by itself when the time
runs out, the connection drops or the add-on stops, so a crashed add-on can't keep a screen on at
night. Night mode and a closed cover still win.

**Settings.** `addon.json` declares a schema; the setup page draws it on the add-on's card:

| Type | Notes |
| --- | --- |
| `string` | longest length |
| `secret` | never shown back, never exported or in diagnostics; the field says "set" or is empty |
| `bool` | |
| `number` | min, max, step |
| `select` | options from the schema |
| `entity` | a Home Assistant entity of the listed domains |

A `test` button, when declared, sends a `settings.test` event with the values in the form, and shows
the add-on's answer (ok, or up to 200 characters of why not). The values are kept in
`/data/techo5-addons/<id>/settings.json`, owned by root and `0600`, written only when Save changes
something, and sent to the add-on in `hello` and as `settings` when they change. They are the add-on's,
not the device's: not in `state.json`, not in Home Assistant, kept with the state folder when it is
removed with the box unticked. Changing them goes behind the setup page's Allow like everything else
there.

**Entities.** `addon.json` declares `sensor`, `binary_sensor`, `switch`, `select`, `button` and `number`
entities, at most 16, each with an object id that the daemon prefixes with the add-on's id. The add-on
sets their states (`entity.set`, at most 5 a second each) and gets `entity.command` events. They
appear on install and go on removal, each by a reconnect, as the update entity does. Their states are
not saved: an add-on sets them again after `hello`.

## Stage 2: the screen (`screen1`)

The add-on sends a **scene**: a tree of building blocks, which the daemon lays out and draws in its
own theme, fonts and colors, at the Show 5, Show 8 or Spot's size. No pixels from the add-on, except
images.

| Block | Holds |
| --- | --- |
| `column`, `row` | children, spacing, weight |
| `list` | sections with a heading, items; scrolls |
| `card` | children, an accent from the theme's palette |
| `text` | words, size (`small`, `body`, `title`, `huge`), weight, max lines |
| `image` | a URL the daemon fetches (http(s), on the LAN or Home Assistant) or bytes; JPEG or PNG, at most 256 KB and 1280×800 |
| `button`, `chip` | a label, an icon (Material Design Icons, as dashboards use), an id |
| `progress` | a value, or a countdown tied to one of the add-on's timers |
| `divider`, `spacer` | |

Every block that can be pressed has an id. A tap comes back as `touch` with the id; a swipe left or
right, and the back gesture, come back as `swipe` and `back`. Scrolling a list is the daemon's.
`scene.patch` replaces one block by its id, so a timer chip or a checked item doesn't resend the page.
At most 64 KB a scene, 500 blocks, 10 updates a second; a faster sender has its frames merged.

**Where it shows.**

- **A page.** One full-screen page per add-on, reached from a tab in the drawer named for the add-on,
  there while it is installed and running. `screen.open` from a command brings it up, so "start
  cooking" opens the recipe; it waits behind whatever is more pressing (a ring, a call, a turn, the
  settings), as the dashboard and Cast do. The page's top bar always carries the add-on's name and icon,
  drawn by the daemon.
- **A card.** One small card per add-on (at most a third of the screen, text, a chip, a progress, one
  image) that may stay over the clock: the step and its timer while cooking. One at a time on the
  screen, newest wins, the others keep their place and come back. The card is labeled with the
  add-on's name too, and goes with a swipe.

**What it can never do.** The daemon reserves the PIN pad and everything behind the settings lock,
the settings sheet, the setup page's and DLNA video's questions, Cast's Accept or Decline, alarm and
timer ring screens, the call screens, the camera's live view and first-run. When one is up, an
add-on's page and card are not drawn and get no touches, and nothing an add-on draws may sit over
them. A scene can't use the colors and layout of those questions: the theme offers no accept or
decline pair to a block, and the name bar can't be hidden. That is what keeps a page from passing for
a question the system asks.

**Memory.** Scenes and images are kept in RAM only, at most 8 MB of images per add-on, dropped first
under pressure and when the page goes; a URL is fetched again when needed. Nothing about a scene is
written down.

## Stage 3: voice and Home Assistant reads (`voice1`)

**Voice state.** With `voice.events`, the add-on gets `voice` events: `listening`, `thinking`,
`replying`, `idle`, as the screen's cue follows them. With transcripts granted as well (a separate,
bold line), it gets the words heard and the reply, as `esphome.techo5_voice` already gives Home
Assistant. Never audio. Privacy: what someone says in the kitchen reaches an add-on's author's code,
so the line says that plainly, and the card shows the add-on is listening to transcripts.

**Home Assistant reads.** `ha.read` names entity ids or whole domains. The daemon follows them through
`hastate` (declared before a connection, so an install reconnects), and sends `ha.state` events; an
`ha.get` answers the last known state. The daemon's Home Assistant sign-in and the ESPHome link are
never given to an add-on. `ha.call` (stage 1 or 3, see the questions) lets an add-on ask for one of
its listed services, which goes through the device's existing service call path, and only when Home
Assistant lets the device perform actions.

## Security

| Threat | Answer |
| --- | --- |
| Another process poses as an add-on | `SO_PEERCRED` uid against the installed list; socket folder `0700` under that uid; root refused |
| One add-on acts for another | id from the uid only; timers, entities, cards and settings carry their owner |
| An add-on gains more than agreed | capabilities pinned by the catalog and the consent hash; checked per request |
| The Home Assistant token leaks | never handed over; reads and calls go through the daemon, listed ones only |
| The daemon is flooded | size caps, rate limits, outstanding-request cap, per-connection goroutines and deadlines, disconnect on abuse |
| The screen is used to trick | daemon draws everything, name bar always shown, system questions reserved and never imitable |
| Microphone and transcripts | no audio ever; transcripts only with their own bold line |
| A crash leaves things on | keep-awake handles expire; timers and cards go with the add-on |
| Speech or sounds at 3 a.m. | quiet hours win; the stop word stops it |

Not copied from Kiosk Satellite: code in the daemon's process, a layer over system screens, writes to
Home Assistant as the device with no list, an unsigned store, and timers or keep-awake built by each
plugin on its own.

## Storage wear

The daemon writes nothing per message. Settings are written when Save changes them; timers when set
and when they end, as now; installing and removing write what they write today. Scenes, images, entity
states and queues live in RAM, and the socket is on `/run`. What an add-on writes itself is counted by
the write meter (branch `feat-addon-writes`), which warns above 50 MB a day.

## Languages

What an add-on sends (scene text, speech, setting labels, command answers) is the add-on's, in the
language it chooses; `hello` tells it the screen's language. What the daemon draws around it (the name
bar, the drawer tab's fallback, the consent lines, errors on the card) is localized like the rest of
TECHO5.

## Testing

- **A fake daemon**, `echod/pkg/addonapi/fake` (or `cmd/techo5-addon-fake`), that speaks the protocol on
  a socket, logs every request, fires commands and touches from the keyboard, and renders a scene to a
  PNG with the real layout code, so an author sees a page without a device.
- **The protocol written down** in `docs/addon-api.md`, with each op, event, error and limit.
- **A small Go client package**, shared by the fake and the daemon as `deckwire` is by the deck.
- **Conformance tests** in the daemon: peer check, framing, every cap and limit, capability refusal,
  handle expiry, cleanup on stop and removal.
- **Golden screens** (`tools/golden`) for each block on the Show 5, Show 8 and Spot, light and dark, and
  for the reserved screens with an add-on page under them.

## Rollout and Meals

| Stage | `requires` | Targets | Devices |
| --- | --- | --- | --- |
| 1: commands, events, speak, sound, timers, keep-awake, settings, entities | `api1` | the first Show release after it is built and tested, then the Spot and Dot | Show, Spot, Dot (keep-awake and screen-free parts) |
| 2: page, card | `screen1` | a later release, after stage 1 has been used | Show, Spot |
| 3: voice events, HA reads | `voice1` | after stage 2 | all |

How Meals maps:

- **Stage 1:** Mealie address and token as settings (token a `secret`, a `test` button that reaches
  Mealie); "start cooking", "next", "back", "go to step 3" and "start the timer" as commands, each
  answering the step text for Home Assistant to read; step timers on the device; a sensor with the
  current step. Useful by voice alone, with the step said aloud.
- **Stage 2:** the Shopping, Plan and Cooking views as one page with three tabs inside it, photos by
  URL, the step and its timer as a card over the clock, keep-awake while cooking.
- **Stage 3:** not needed; it might use voice state to pause a step's read-out while someone talks.

## Open questions for the user

1. Is `ha.call` in stage 1 (Meals doesn't need it), stage 3, or left out?
2. Should `speak` go through Home Assistant's TTS only (as reminders), or also take a WAV from the add-on?
3. Is one drawer tab per add-on right, or one **Add-ons** tab that lists them?
4. May a card sit over the clock at night, or only by day?
5. Does `voice.events` with transcripts ship at all?
6. Should the fake daemon and client live in this repository (as the deck agent does), or in a repository of their own?
7. Which release should stage 1 aim for, and should nachtschatt3n get the protocol draft on #720 before it is built?

## Decisions (2026-11-26)

- **Home Assistant calls:** not in stage 1. Stage 1 adds a local `media` capability instead: play a video or audio URL on this device, with pause, resume and stop, shown on the consent screen. `ha.call` comes in stage 3, limited to the services an add-on declares, each named on the consent screen; nothing touching users, auth or the device's own settings.
- **One Add-ons tab** in the drawer, with each installed add-on as a tile in it. An add-on's command may open its own page directly (stage 2), so voice lands on the right page without the extra tap.
- **Night:** an add-on's card may stay over the night clock, drawn in the night clock's dim colors.
- **Transcripts:** never by default; only behind a separate bold consent line, text only, never audio.
- **Process:** the draft goes to nachtschatt3n on #720 first; it can change from there.

## Stage 1 as built

What [addon-api.md](addon-api.md) describes is what was built. It follows the plan above and the
decisions, with these changes, each made for the reason given:

- **An `events` capability** for `event.fire`, with the line "Sends events of its own to Home
  Assistant." The plan put events in stage 1 without a capability, and every capability being a line
  that is enforced means putting something on Home Assistant's bus needs one too.
- **A `media` capability**, from the decisions: "Plays video and audio from the network on this
  device." It plays through `play_video`'s player and the media player, acts only on what the add-on
  started while that still plays, is refused in quiet hours (`busy`) as speech and sounds are, and has
  no video on a Dot.
- **Object ids are `addon_<id>_<name>`**, not `<id>_<name>`: under the prefix the add-on's update
  entity already has, so an add-on's entity or command button can never take the name of one of the
  device's own. A command and an entity of one add-on can't share a name.
- **Root add-ons cannot use the API.** The plan says root is refused on the socket and also that an
  add-on asking for root still talks through it; the two can't both hold, since the kernel says root
  for both the add-on and a root shell. So `api` with `"root": true` is refused in the manifest and in
  the catalog, and a device that cannot drop root for an add-on serves it no socket.
- **Timers stay when an add-on stops or crashes**, and are canceled when it is removed: they are the
  device's timers, and a kitchen timer should still ring when the add-on that set it restarts.
  They are **not written down**, unlike the plan's "saved like the others": each start and end
  rewrote and synced the whole config, as often as an add-on liked. After a daemon restart the add-on
  sets them again. They are shown as the add-on's ("Meals: rice"), start and cancel 8 at once and then
  one every 10 s, and **run out silently in quiet hours** (the add-on is told). The consent line says
  so. Keep-awake holds end when their connection drops, as planned.
- **The socket's folder is root's, 0711**, not the add-on's, 0700: only the socket is the add-on's,
  so it cannot fill /run, which is RAM.
- **Nothing is logged per message or connection**: each kind of line once a minute per add-on.
- **Entities** are set at most 5 times a second all together as well as each, and a state set again
  is not sent on. Entities are read again when an add-on is installed or removed (the list was read
  once, at start, which left the existing update entities waiting for a restart too); one whose key
  another has is left out rather than failing the link, and an add-on whose names would be another's
  is refused at install.
- **Media** plays at most 3 at once, then one every 30 s; an add-on's audio is fetched with a dialer that refuses the
  device itself, loopback and link-local; resume is refused in quiet hours too.
- **`media.state` and `media.status`**, under `media`, with no line of their own: an add-on that plays
  a film could not tell what the player was doing, so one stopped with the device's own Stop button
  went on counting as watched (the Plex add-on). The daemon tells it what its own media does (buffering,
  playing, paused, stopped with a reason: ended, device, replaced, error, addon), with the position and
  length when known, and `media.status` answers the same. Only its own media, never what else plays or
  what replaced it: telling one add-on what the room plays would be a capability of its own. Changes are
  gathered for half a second, a playing position said at most every 10 s, each end once, all in
  memory and nothing logged above Debug. `media.play` answers the media's id, which each event carries,
  so an add-on whose next play replaced its last can tell the two apart.
- **`speak.cancel` drops the words still waiting**; the ones being said are stopped by the stop word,
  as any announcement. Words are paced by the device's own sounding state, so the next waits for the
  last to be heard (at most a minute; 10 seconds when nothing sounds at all).
- **The `addon_command` action waits 4 seconds** for an answer: it runs on Home Assistant's
  connection, which waits with it. A settings test waits 10.
- **An event's data is strings by key**, at most 1 KB, and may not use `addon` or `name`, which say
  whose event it is.
- **"Keeps on" past a limit** is 20 requests refused for limits within a minute, which disconnects
  the add-on and refuses it for a minute; half a message has 5 seconds to arrive, and an add-on that
  leaves 128 messages unread is disconnected.
- **Entity commands from Home Assistant** reach the add-on as `entity.command`, and the entity keeps
  its state until the add-on sets it, so it never shows what didn't happen.
- **A WAV** may be 8 to 48 kHz, mono, 16-bit; the device resamples it.
- **Not built yet:** the blueprint for sentence triggers in `docs/`, the fake daemon's PNG of a scene
  (stage 2), and golden screens (stage 1 draws nothing on the device's screen).

Storage, per day on a device with one add-on in use: nothing per message, nothing per timer, and
nothing logged per message; a settings write each time Save changes them (a few hundred bytes, so a
handful of writes in a day of setting up, none after). What the add-on writes itself is its own.
