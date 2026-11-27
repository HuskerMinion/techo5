# The add-on API

An add-on installed from TECHO5's catalog can talk to the device it runs on: answer commands from Home
Assistant and voice, say words, play short sounds, set timers that ring on the device, keep the screen
on, have settings of its own on the setup page, add entities to Home Assistant, and play a video or
audio. This is level 1 of the API (`api1`). Stage 2 (`screen1`) adds a page of the add-on's own and a
card over the clock, drawn by the daemon from a scene the add-on sends: see
[Stage 2: the screen](#stage-2-the-screen). The plan it was built from, and the stages after it, are in
[addon-api-plan.md](addon-api-plan.md).

Everything here is checked by the daemon on every request. An add-on gets only the capabilities its
catalog entry lists, which the owner saw on the consent screen before ticking the box.

For Go there is a client in [`echod/pkg/addonapi`](../echod/pkg/addonapi), and a fake daemon to test
against without a device, [`echod/cmd/techo5-addon-fake`](../echod/cmd/techo5-addon-fake), which runs
the device's own server code.

## Declaring it

`addon.json` says the API level and the capabilities it uses, and declares its commands, settings and
entities:

```json
{
  "id": "meals",
  "version": "0.1.0",
  "command": ["meals"],
  "ports": [],
  "root": false,
  "memory_mb": 20,
  "api": 1,
  "capabilities": ["commands", "speak", "timers", "settings", "entities"],
  "commands": [
    {"name": "next"},
    {"name": "step", "args": {"to": {"type": "string", "max": 16, "required": true}}}
  ],
  "settings": {
    "fields": [
      {"key": "address", "label": "Mealie address", "type": "string", "max": 200},
      {"key": "token", "label": "API token", "type": "secret"}
    ],
    "test": true
  },
  "entities": [
    {"domain": "sensor", "id": "step", "name": "Step", "text": true}
  ]
}
```

The catalog entry repeats the level, the capabilities, the commands' names and the entities, and
requires `api1`:

```json
{"version": "0.1.0", "requires": ["api1"], "api": 1,
 "capabilities": ["commands", "speak", "timers", "settings", "entities"],
 "commands": ["next", "step"], "entities": ["sensor.step"], "...": "the rest as for any version"}
```

A device that does not speak `api1` skips the version rather than refusing the catalog. A manifest that
differs from its entry in any of these is refused at install. An update that adds a capability, a
command or an entity asks for the box again. `mkcatalog -manifest addon.json` checks a manifest as a
device would.

An add-on that runs as root cannot use the API: the daemon refuses root on the socket, so a root shell
can't pose as an add-on. The catalog refuses a listing that asks for both.

### Capabilities

Each is one line on the consent screen, word for word:

| Capability | On the consent screen | Ops |
| --- | --- | --- |
| `commands` | Adds commands Home Assistant and voice can call: *next, step*. | `command.reply` |
| `events` | Sends events of its own to Home Assistant. | `event.fire` |
| `speak` | Says words on this device. | `speak`, `speak.cancel` |
| `sound` | Plays short sounds on this device. | `sound.play` |
| `timers` | Sets timers that ring on this device (silent in quiet hours). | `timer.start`, `timer.cancel`, `timer.list` |
| `keepawake` | Keeps the screen on while you use it. | `awake.hold`, `awake.renew`, `awake.release` |
| `settings` | Has settings of its own on this page. | `settings.get`, `settings.reply` |
| `entities` | Adds entities to Home Assistant: *sensor.step*. | `entity.set` |
| `media` | Plays video and audio from the network on this device. | `media.play`, `media.pause`, `media.resume`, `media.stop`, `media.status` |
| `screen` | Shows a page of its own under the Add-ons tab, and a small card over the clock, which may stay over the night clock, dimmed. | `scene.set`, `scene.patch`, `screen.open`, `screen.close`, `card.clear`, `image.put`, `image.drop` (stage 2, requires `screen1`) |

`commands`, `settings` and `entities` are declared in `addon.json` exactly when their capability is
listed.

### Commands

Each has a name (lowercase letters, digits and underscores, starting with a letter, at most 32) and
arguments, each a `string` (with `max`, its longest length, at most 1024), a `number` or a `bool`, and
`required` or not. At most 32 commands with 8 arguments each.

### Settings

| Type | Notes |
| --- | --- |
| `string` | `max`: longest length |
| `secret` | never shown again on the page, never kept in a refused form, never in a backup |
| `bool` | a box |
| `number` | `min`, `max`, `step` |
| `select` | `options` |
| `entity` | a Home Assistant entity id of one of the `domains` |

At most 16 fields. With `"test": true` the card has a Test button.

### Entities

`sensor` (a number, with `unit`; or words, with `"text": true`), `binary_sensor`, `switch`, `select`
(with `options`), `button` and `number` (with `min`, `max`, `step`, `unit`), at most 16, each with an
`id`, a `name` and an `icon` (`mdi:…`) if it likes. Their object ids in Home Assistant are
`addon_<id>_<entity id>`, the add-on id's dashes as underscores, so `sensor.<device>_addon_meals_step`.

## Connecting

The daemon makes `/run/techo5-addons/<id>/api.sock` before the add-on starts, owned by the add-on's
user with mode `0600`, in a folder of root's with mode `0711` (the add-on can reach its socket, and
put nothing beside it), and gives its path in `TECHO5_ADDON_API`. The socket stays through the
add-on's restarts and goes when it stops.

Who connects is the kernel's word (`SO_PEERCRED`): a user that isn't the add-on's, isn't an installed
add-on switched on, or is root, is closed at once. The add-on's id comes from that, never from a
message.

## Framing

Each message, either way, is a 4-byte big-endian length and then that many bytes of UTF-8 JSON, at most
64 KB. Messages are read strictly: a key that isn't known, a key given twice or in another case, keys
that aren't lowercase ASCII letters, digits and underscores, more than 256 keys in an object, nesting
deeper than 32 (a scene nests two for each level of its blocks), or anything after the value refuses
the message.

| Message | Shape |
| --- | --- |
| Request | `{"id": 7, "op": "timer.start", "args": {"name": "rice", "seconds": 600}}` |
| Response | `{"id": 7, "ok": true, "result": {"id": "local:abc"}}` |
| Refusal | `{"id": 7, "ok": false, "error": {"code": "denied", "message": "…"}}` |
| Event | `{"event": "command", "data": {…}}` |

A request's `id` is the add-on's to choose; its response carries it back. A message that can't be read
at all is answered with id 0.

## Errors

| Code | Means |
| --- | --- |
| `bad_request` | the arguments are wrong, or the first request wasn't `hello` |
| `denied` | the capability for it was not granted |
| `unknown_op` | no such op at this level |
| `too_large` | a message, or a field, past its limit |
| `rate_limited` | past a rate limit |
| `busy` | a queue is full, a count is reached, or it is quiet hours; for `screen.open`, nobody asked for the page |
| `unavailable` | Home Assistant is not connected, or what it needs isn't there |
| `internal` | the device could not do it (the reason stays in the device's log) |

## Limits

- 64 KB a message; one that says it is longer is answered `too_large` and skipped, and one claiming
  more than 1 MB is hung up on. An `image.put`, from an add-on with the screen, may be up to 352 KB
  (256 KB of image in base64, and the rest of the request); a connection has one of those read and
  answered at a time, the next waiting for it.
- 20 requests a second on a connection, with a burst of 40, counted as each message arrives, before
  it is read.
- 4 connections per add-on, 64 requests waiting for their answers on each.
- Half a message has 5 seconds to arrive in full. An add-on that stops reading what it is sent is
  disconnected once 128 messages wait for it.
- 20 requests refused for limits, or that don't read, within a minute disconnect the add-on, and
  refuse it, for a minute. Its card says so. A header claiming more than 1 MB counts as one.
- The device's log says each kind of refusal at most once a minute for each add-on; a connection
  refused while the add-on is disconnected, or has all its connections, says nothing.

## Ops

### hello

The first request on every connection. `args`: `{"api": 1}`, the level the add-on was written for.
The result:

```json
{"api": 1, "addon": "meals", "capabilities": ["commands", "speak"],
 "device": {"family": "show", "board": "cronos", "width": 960, "height": 480, "language": "en", "version": "v1.7.0"},
 "settings": {"address": "http://mealie.local:9000", "token": "…"}}
```

`width` and `height` are 0 on a device without a screen. `settings` is there with the `settings`
capability, secrets and all: they are the add-on's.

### command.reply

Answers a `command` event that has a `call`: `{"call": 3, "ok": true, "text": "Step two: stir."}`, the
text at most 1024 characters. With `ok` false, `text` is why not, which Home Assistant's action fails
with.

### event.fire

`{"name": "step_changed", "data": {"step": "3"}}` puts `esphome.techo5_addon` on Home Assistant's bus
with `addon`, `name` and the data's keys beside them (which may not be `addon` or `name`). The data is
at most 1 KB as JSON. At most 2 a second.

### speak

`{"text": "…"}`, at most 400 characters, is announced on this device by Home Assistant, as a reminder
is: in the household's voice, ducking the music, waiting behind a turn, a call or a ring, and stopped
by the stop word. The add-on's words are said one after another, at most 3 waiting (`busy` past
that); the result is how many are queued, these included: `{"queued": 2}`. `unavailable` without Home
Assistant, `busy` in quiet hours.

### speak.cancel

Drops the add-on's words not yet said: `{"dropped": 2}`.

### sound.play

`{"name": "chime"}` plays one of `chime`, `done`, `error` and `tick` over whatever is playing;
`{"file": "ding.wav"}` plays a WAV among the add-on's extra files, pinned by the catalog: mono, 16-bit
PCM, 8 to 48 kHz, at most 5 seconds and 512 KB. At most one a second; `busy` in quiet hours.

### timer.start, timer.cancel, timer.list

`timer.start` `{"name": "rice", "seconds": 600}` (more than 0, at most 24 hours; a name of at most 64
characters) sets one of the device's own timers and answers `{"id": "local:…"}`. It counts down on
the clock and the ring, shown as the add-on's ("Meals: rice"), rings, and is stopped by the stop word
and the Stop button. In quiet hours it runs out without a sound. An add-on has at most 8 at once
(`busy`), starts and cancels them 8 at once and then one every 10 seconds (`rate_limited`), and lists
(`{"timers": [{"id", "name", "left", "total", "running"}]}`) and cancels (`{"id": "local:…"}`) only
its own. Its timers stay when it stops or crashes, and are canceled when it is removed. They are not
written down: after the daemon restarts, `timer.list` is empty and the add-on sets them again.

### awake.hold, awake.renew, awake.release

`awake.hold` `{"seconds": 900}` (at most 30 minutes) keeps the screen from dimming after idle and
going out for an empty room, and answers `{"handle": "…", "until": "…"}`. `awake.renew`
`{"handle": "…", "seconds": 900}` makes it last that long from now, and `awake.release`
`{"handle": "…"}` ends it. A hold ends by itself when its time runs out, its connection drops or the
add-on stops. At most 4 at once. Night mode and a closed cover still win.

### settings.get, settings.reply

`settings.get` answers `{"values": {…}}`, as saved. `settings.reply` answers a `settings.test`:
`{"call": 4, "ok": false, "text": "Mealie says the token is wrong"}`, at most 200 characters, which
the card shows.

### entity.set

`{"id": "step", "state": "Chop the onions"}` sets one of the add-on's entities: a number for a
sensor (words, at most 255 characters, for a text one), `true` or `false` for a binary sensor or a
switch, one of a select's options, a number within a number's bounds. A button has no state. At most 5
a second for each entity, and 5 a second for all of them together; setting the state an entity has
already costs nothing and is not sent on. States are not saved: set them again after `hello`.

### media.play, media.pause, media.resume, media.stop

`media.play` `{"url": "https://…", "kind": "video", "title": "Step 3"}` plays a video full screen (not
on a Dot: `unavailable`), or with `"kind": "audio"` audio on the speaker, from an http or https
address, never from the device itself, loopback or link-local, whatever the name resolves to. At most
3 at once, then one every 30 seconds. It answers `{"media": "7"}`, the id the `media.state` events of
what it started carry. `media.pause`, `media.resume` and `media.stop` act on what the add-on started,
while it is still what plays, and on nothing else (`bad_request`). `media.play` and `media.resume` are
`busy` in quiet hours.

### media.status

Answers what the add-on's latest media is doing, as a `media.state` event says it: the latest it
started that still plays, else how the latest ended, else `{"state": "idle"}` when it has started
nothing since the daemon started. It never says anything about media the add-on did not start.

## Events

### command

`{"call": 3, "command": "step", "args": {"to": "3"}, "from": "action"}`: a command called from Home
Assistant, its arguments checked against its declaration. With a `call`, Home Assistant waits up to 4
seconds for a `command.reply`, and answers with its text; without one (`"from": "button"`, a press of
the command's button) nothing waits.

### timer

`{"id": "local:…", "name": "rice", "state": "ringing"}`: one of the add-on's timers rang (`ringing`),
its ring was stopped (`stopped`), or something other than the add-on canceled it (`canceled`). One
that ran out in quiet hours is `ringing` with `"quiet": true`: it made no sound, and no `stopped`
follows.

### settings

`{"values": {…}}`: the owner saved new settings on the setup page.

### settings.test

`{"call": 4, "values": {…}}`: the owner pressed Test, with these values in the form (a secret left
empty is the one saved). Nothing is saved. Answer with `settings.reply` within 10 seconds.

### media.state

What media the add-on started is doing, sent as it changes, with the `media` capability. It is only
ever about the add-on's own media: never about what else plays, and an add-on whose media something
else replaced is told `replaced`, not by what.

```json
{"media": "7", "kind": "video", "state": "playing", "position_ms": 754000, "duration_ms": 7260000}
{"media": "7", "kind": "video", "state": "stopped", "reason": "device", "position_ms": 761000}
```

| Field | Means |
| --- | --- |
| `media` | `media.play`'s id for it |
| `kind` | `video` or `audio` |
| `state` | `buffering` (started, or moved through, and not yet seen or heard), `playing`, `paused`, or `stopped` |
| `reason` | with `stopped`: `ended` (played to its end), `device` (stopped on the device: its Stop button, the stop word, Home Assistant, the sleep timer, the Video switch), `replaced` (by the add-on's own next `media.play` of that kind, or by anything else), `error` (could not be fetched or played), `addon` (the add-on's own `media.stop`) |
| `position_ms` | how far into it the device is, in milliseconds, when it can tell |
| `duration_ms` | how long it is, when it says: a video that has a length; audio and live streams have none |

A change of state is sent within half a second, changes that come together as one event (the latest),
so at most two a second besides each media's end. While it plays, its position is sent at most every
10 seconds, and not at all while it is paused. Its end, `stopped`, is sent once, at once, and nothing
of that media after it: an add-on whose next `media.play` replaced its last hears of that end before
anything of the next. A video paused because something covers it (a call, a ring) is `paused`, and
`playing` again when it carries on. The events go to every connection that has said hello; one that
connects later asks `media.status`.

### entity.command

`{"id": "cooking", "state": true}`, or `{"id": "next", "press": true}` for a button: Home Assistant set
one of the add-on's entities. The add-on carries it out and sets the state itself with `entity.set`,
so the entity never says what didn't happen.

## Stage 2: the screen

With the `screen` capability an add-on has a **page** of its own, a tile under the Add-ons tab that
opens full screen under a name bar, and a **card**, a small one over the clock (at most a third of the
screen), that may stay over the night clock, drawn in its dim colors. It draws neither: it sends a
**scene**, a tree of blocks, and the daemon lays it out and draws it in the device's own theme, fonts
and colors, at the Show 5's, the Show 8's or the Spot's size. No pixels come from the add-on but its
images.

**Where it shows.** On a Show the drawer (a swipe in from the right edge of the clock) has an
**Add-ons** tab while an add-on with the screen runs, with a row for each; Open brings its page up. On a
Spot the ring menu (a long press) has an **Add-ons** item: with one such add-on it opens its page, with
more a dial of them, each marked by its mark (below). The page fills the screen under the name bar: on a
Show along the top, with the way out (×) at its end; on a Spot a band across the top of the circle,
with the way out at the foot. What the scene holds is laid out inside the room left, each block inside
the room its column, row or card gives it, never drawn over the block beside or below it, over the bar
or the way out. Where a column has too little room, its gaps narrow and its pictures shrink (to a third
at the least); a page's column that still has more than its room scrolls, as a list does (a swipe up or
down on it, a thumb and faded edges while there is more, its blocks cut cleanly at its edges, a tap only
on what is seen, and a gutter at its right edge for the thumb, which never lies over a block), so
every button, stepper and toggle on a page can be reached. In a list or a scrolling column, words and
controls are drawn, and take a finger, only whole: one the view's edge would cut waits until it is
scrolled fully into view, so the edges never show a sliver of a line; a picture is cut cleanly. The card, a glance at
the clock, does not scroll: what does not fit whole in it is left out (its chip row, say, rather than
half a chip), and it is as tall as what it keeps, at most a third of the screen. On the page:

- a tap on a button, a chip or a list item with an id sends `touch`, and on a stepper's − or + or a
  toggle `change`, for what the finger was on: a tap
  the device read off a drawing of the page from before a change to that block (a new `scene.set`, or a
  patch of it, of something in it or of something it is in) is not sent, as the page under the finger is
  no longer the one drawn;
- a swipe up or down scrolls the list (or the column that has more than its room) under the finger (the
  first, if it is on none), which the daemon
  draws with a thumb and faded edges while there is more; a new `scene.set` starts the page's lists at
  their tops again (a patch leaves them where they are);
- a swipe right that starts at the left edge is the back gesture (`back`); any other swipe left or
  right is `swipe`;
- the way out puts the page away whatever the add-on says, and so does the add-on stopping, or the
  page being left untouched as long as a dashboard opened by hand is (the Dashboard returns setting,
  10 minutes unless chosen), unless something holds the screen awake (`awake.hold`).

The card sits along the foot of the clock, where Next up is (the Next up card and the glance chips wait
under it until it goes, and the clock, its date and its timers' line move up clear of it, the clock
smaller where it must be, as they do for Next up, in every clock style; a style with lines too many for
the room left, on a Show 5, leaves some out (the Agenda's date, the Sun's date, the Dashboard's weather
line, the Analog's own weather line, which the weather corner then has), a clock style at the foot is
drawn above the card as at the center, and on a Spot every face but the classic one, laid out for the
whole circle, is drawn whole and smaller above the card), as tall as what it holds and never more than a third of the screen, under the
add-on's name; on a Spot along the foot of the face. Of several add-ons' cards the one set last shows.
A tap on a chip sends `touch`; a tap anywhere else on the card brings the add-on's page up (and counts as
somebody asking, below); a swipe across it puts it away (`screen.state` with `dismissed`). It is drawn
anew at most once a second, and once in five seconds over the night clock, however often the add-on
sets or patches it: a card patched ten times a second (a changing picture, say) would otherwise keep the
device drawing the clock all night. The page, which somebody is looking at, is drawn up to 10 times a
second. Over the
night clock it is drawn in the clock's dim red, nothing on it brighter than the clock itself, its name
in the clock's own red so it can always be read, and takes no finger: a touch on a night light is the
night light's, as everywhere. So its chips are not drawn there (nor a row left empty without them), and
it never shows something to press that would do nothing.

A version that uses it requires `screen1` beside `api1` in the catalog, and names no Dot, which has no
screen; the API level in `addon.json` stays 1.

**The icon.** A screen version's catalog entry may have an `icon`, chosen by whoever reviews the
listing, not by the author: `"icon": "mdi:chef-hat"`, `mdi:` and the name of a
[Material Design Icons](https://pictogrammers.com/library/mdi/) icon, as Home Assistant names one. It is
the add-on's mark on its row under the Add-ons tab, its item on the Spot's dial, its page's name bar and
its card's label; without one the mark is its initials. `mkcatalog` refuses, before it signs, a name
the device's icon font does not have; a device reading a catalog refuses only any other spelling
(`hass:`, a bare name, capitals) and an icon on a version without the screen capability, and draws the
initials for an icon its font lacks, so a catalog signed against a newer font never leaves an older
device without a catalog. The add-on's name on the name bar, its row and its card is its listing's,
which `mkcatalog` holds to words with no control character and none that is not seen or reorders words
(as a scene's words, below); a device draws it without any such character. Nothing in `addon.json` or anything the add-on sends can set or change
it. It is a field of the version, not of the add-on, so that it rides on `screen1`: every device before
stage 2 skips such a version whole, where a key it does not know beside the add-on's name would have it
refuse the whole catalog, and `catalog.json` (format 1) never has it. Like everything else in a version,
it is fixed once published; a new icon comes with a new version.

```json
{"version": "0.2.0", "requires": ["api1", "screen1"], "api": 1,
 "capabilities": ["commands", "screen", "timers"], "icon": "mdi:chef-hat", "...": "the rest as for any version"}
``` A device without the screen's ops skips the version. On
a device with no screen for add-ons, every screen op is answered `unavailable`.

**What a page can never do.** The name bar over the page, with the add-on's name and its mark, is the
daemon's and cannot be hidden or covered. The mark is the icon the catalog's reviewer chose for the
version (below), or without one the add-on's initials; never an icon the add-on picks, which could pass
for one of the device's own (a padlock, a phone). Nothing in a block names a color, a position or a size in
pixels: a card is marked with one of the theme's own colors by name, and there is no accept or decline
color. The PIN pad and everything behind the settings lock, the settings sheet, the setup page's and
DLNA video's questions, Cast's Accept or Decline, alarm and timer ring screens, the call screens, the
camera's live view and first-run are the device's: while one is up, an add-on's page and card are not
drawn and get no touches. That is what keeps a page from passing for a question the device asks.

### Scenes

A scene is one block, the root, holding the rest. Every block has a `type`, and may have an `id`
(lowercase letters, digits and underscores, starting with a letter, at most 32), unique in its scene,
by which `scene.patch` replaces it. A block may have only its type's fields; any other is refused, as an
unknown key is.

| Block | Fields | Notes |
| --- | --- | --- |
| `column`, `row` | `children`, `spacing` | top to bottom, start to end |
| `list` | `sections`: each `{"heading": "…", "items": [blocks]}` | scrolls, which is the daemon's; no list inside a list; a `row` that is an item may have a picture or an icon at its start (below) |
| `card` | `children`, `spacing`, `accent` | `accent` is one of the theme's: `accent`, `dim`, `rules` |
| `text` | `text`, `size`, `bold`, `align`, `max_lines` | `size`: `small`, `body` (the default), `title`, `huge`; `align`: `start`, `center`, `end`; line breaks kept |
| `image` | `url`, `name` or `data`, `fit` | one of the three; `fit`: `contain` (the default) or `cover` |
| `button` | `label`, `icon`, an `id` | a tap sends `touch` |
| `chip` | `label`, `icon`, `selected`, an `id` | a tap sends `touch`; `selected` for a row of tabs, the add-on's to set |
| `stepper` | `label`, `value`, `min`, `max`, `step`, `unit`, an `id` | a number from `min` to `max` in steps of `step` (1 if none), drawn as − value unit +; a tap on − or + sends `change` |
| `toggle` | `label`, `on`, an `id` | an on and off switch beside its label; a tap sends `change` |
| `progress` | `value` or `timer` | `value` from 0 to 1, or `timer`, one of the add-on's own timers by `timer.start`'s id, counted down with the clock's |
| `divider`, `spacer` | | |

**Steppers and toggles** take a number or a choice without keys. A stepper's `value`, `min` and `max`
are required, and `step` is 1 unless given; each is finite, at most 1e9 either way and of at most 2
decimal places, `min` is less than `max`, `value` lies between them, `step` is more than 0, and the range
holds at most 1000 steps; its `unit` is words of at most 16 characters ("people", "min"). − and + are
round buttons of a button's size, the one at the end of the range drawn faint and doing nothing. A tap
on − or + moves the number one step, held to the range, and a tap anywhere on a toggle flips it: the
device shows the new number or state at once and sends `change` (below). The add-on may patch the block
to set it otherwise (to refuse 13 servings, say), and what it patches is what shows; a new `scene.set`
shows what it sets. Both are pressable, so neither is drawn on the card over the night clock, and
neither counts as a digit key, whatever its label.

**Picked chips.** A chip's `selected` draws it as the theme's picked chip (the accent, as the device's
own tabs are), never a color of its own. A tap does not pick it: it sends `touch`, and the add-on patches
the chips of the group, picked and not, so a row of tabs or a single choice is the add-on's to keep.

**A list row's picture or icon.** A `row` that is a list's item may have, at its start, a small square
picture, by `name` (one put with `image.put`) or `url` (by an image's rules, counted among the page's
images and its 8 MB), or an `icon` (`mdi:…`; one the device's font does not have is drawn as nothing);
not both. The row is tapped by its `id` as before. A row anywhere else has neither.

`spacing` is `none`, `small`, `normal` (the default) or `large`. Any child of a row or a column may have
a `weight`, from 1 to 12: its share of the room the children without one leave. A button or a chip has
a label, an icon (`mdi:…`, as Home Assistant names one) or both, and must have an `id`. Buttons and
chips can be tapped, and so can a list's item that has an `id`, as a whole, whatever its type: a row of
a recipe's picture and name, say. A button or a chip inside such an item is tapped as itself; a tap
anywhere else on the item sends the item's id.

An image is one of three:

- `name`: one the add-on put on the device with `image.put` under that name (below). This is the way
  for a photo the add-on has: sent once, shown by every scene that names it. A scene may name one that
  is not put yet; its place is drawn empty until it is.
- `url`: fetched by the device, never by the add-on's code, from an http or https address on the LAN or
  at Home Assistant's own host, with no user or password in it. The rule is held on the address
  actually dialed, whatever a name resolved to and wherever a redirect led: a private address (10/8,
  172.16/12, 192.168/16, fc00::/7) or one in 100.64/10, as Tailscale uses, and never the device itself,
  loopback, link-local or multicast; only the host Home Assistant was given as may be another address.
  The device's Home Assistant sign-in is never sent with it. It may take 10 seconds and 256 KB; one
  that fails is tried again after a minute, and its place is drawn empty meanwhile.
- `data`: a JPEG or a PNG's bytes (base64 in JSON), which count toward the scene's 64 KB, so bytes suit
  an icon; the header is checked when the scene is set.

Each is at most 256 KB and 1280 by 800 pixels, either way up, and the device decodes it away from
drawing, so a page is drawn at once with the places of images still to come left empty.

```json
{"type": "column", "children": [
  {"type": "row", "children": [
    {"type": "chip", "id": "shopping", "label": "Shopping"},
    {"type": "chip", "id": "cooking", "label": "Cooking", "selected": true}]},
  {"type": "text", "id": "step", "text": "Step 2 of 5\nStir the onions", "size": "title", "max_lines": 3},
  {"type": "image", "url": "http://192.168.1.20:9000/api/media/recipes/1/images/original.webp", "fit": "cover"},
  {"type": "progress", "id": "timer", "timer": "local:abc"},
  {"type": "row", "children": [
    {"type": "button", "id": "back", "label": "Back", "icon": "mdi:arrow-left"},
    {"type": "spacer", "weight": 1},
    {"type": "button", "id": "next", "label": "Next", "icon": "mdi:arrow-right"}]}]}
```

**No PIN pad.** A scene, page or card, with ten or more buttons or chips anywhere in it whose labels
are each a single digit, in any script, is refused (`bad_request`, saying so): that is a PIN pad (the
device's own has 1 to 9, then back, 0 and OK), and the PIN pad is the device's own. A keycap ("1️⃣") counts
as its digit: variation selectors and the enclosing keycap are not counted. So a calculator's ten digit
keys are refused too; a number pad an add-on needs is a list or chips of other labels ("10", "+1").
Labels are otherwise free text.

**The card** holds only `column`, `row`, `text` (not `huge`, at most 3 lines and 200 characters),
`chip`, `progress`, `stepper`, `toggle`, one `image` and `spacer`: at most 12 blocks, 3 deep. Nothing on it scrolls or is a
button; it is a glance at the clock, and the page is where the add-on is used. A tap on one of its chips
sends `touch`, and on a stepper's − or + or a toggle `change`; a swipe puts the card away. Of several add-ons' cards the newest shows, and the others
keep their place behind it.

### Limits

| Limit | |
| --- | --- |
| A scene, as JSON | 64 KB, after a patch as well as when set (and a message is 64 KB too) |
| Blocks | 500 on a page, 12 on a card, every one counted |
| Depth | 12 on a page, the root being 1, a child one more, a list's item two more than its list; 3 on a card |
| Words | a text block 1000 characters (200 on a card), a label 40, a heading 64; no control characters (C0, DEL, C1) but a text's line breaks, none that reorder text (U+202A to U+202E, U+2066 to U+2069, U+061C), no format characters (Unicode's Cf: the zero-width characters, U+2060 to U+2064, the soft hyphen, U+180E, U+FEFF, the tag characters), but a zero-width non-joiner or joiner (U+200C, U+200D) between two characters that are seen, as Persian and emoji sequences need; no line or paragraph separator (U+2028, U+2029), and at most 4 combining marks on a character |
| Digit keys | fewer than 10 buttons or chips in a scene labeled with one digit each (a PIN pad's keys, which is the device's own) |
| Lines | `max_lines` at most 20 (3 on a card) |
| Images | 32 on a page, 1 on a card; 256 KB and 1280 by 800 pixels each; an address at most 2048 characters |
| Images kept | 8 MB an add-on, those put and those fetched together, each counted as its pixels at four bytes (or its bytes, when more); 64 put at once |
| Drawing | the page at most 10 times a second; the card at most once a second, once in 5 seconds over the night clock |
| Decoding | an add-on's images are decoded for the screen 8 at once, then 2 a second; the rest are drawn empty until their turn |
| `screen.open` | one for each time a person asks; 3 at once, then one every 10 seconds |

A scene past a size, a count, a depth or a length is answered `too_large`; any other fault, `bad_request`.
Either way nothing changes. An add-on may set and patch as fast as its connection allows (20 a second):
each is checked and kept at once, and what is drawn is the latest, at most 10 times a second, so a
faster sender's frames are merged, never queued.

### Ops

#### scene.set

`{"on": "page", "scene": {…}}` sets the whole scene of the add-on's page, or with `"on": "card"` its
card, which shows over the clock at once. A `progress` tied to a timer must name one of the add-on's
own.

#### scene.patch

`{"on": "page", "id": "timer", "block": {"type": "progress", "timer": "local:def"}}` replaces the block
of that id, wherever it is in the scene, so a chip's time or a ticked item does not send the page again.
The new block keeps the id (it may leave it out, and may not give another). The patched scene is held to
every limit; a patch of an id the scene does not have is `bad_request`.

#### screen.open, screen.close

`screen.open` asks for the add-on's page to be brought up, in answer to somebody: so "start cooking"
opens the recipe. It is allowed only for 5 seconds after a person asked something of the add-on, which
is one of its commands sent to it (from Home Assistant, by voice, or by its button), or a tap on its
tile, its card or one of its blocks; at any other time it is answered `busy`, and nothing is brought
up. Each time a person asks allows one open: an open that is answered `ok` uses it up, and so does the
owner putting the page away (the name bar's way out, or the back gesture), so a page somebody just
closed is not brought straight back. A command counts as somebody asking whoever runs it: a Home
Assistant automation that runs one of the add-on's commands (a schedule, a sensor) is a request too,
and its open lights the screen at night like any other. That is the owner's automation, and theirs to
choose; an add-on cannot run its own commands. Allowed at night, it lifts the night light, as a long press does, since somebody asked for the
page; and where the night turns the screen off (Screen at night set to off) it turns the screen on for
the page, as a tap on the dark screen does. Either way the night takes the screen back by its usual
rules, once the room has been quiet as long as after a touch. It still waits behind whatever is more pressing (a ring, a call, a turn, the settings, any
screen the device reserves), as the dashboard and Cast do, and `screen.state` says when it is in view.
`bad_request` with no page set. `screen.close` asks for it to be put away.

#### image.put, image.drop

`{"name": "photo_1", "data": "…"}` keeps a JPEG or a PNG (base64, at most 256 KB and 1280 by 800
pixels either way up, its header checked) on the device under the name, for image blocks of that
`name` to show; a put of the name again replaces it, and every scene that shows it is drawn again.
`{"name": "photo_1"}` to `image.drop` lets it go. Both answer `{"used": 2000000, "budget": 8388608}`:
how much of its 8 MB the add-on's images take now, each counted as its pixels at four bytes (or its
bytes, when that is more), which is what it takes decoded.

A put that would take the add-on past 8 MB is refused, `too_large`, and nothing it put is let go to
make room: drop one first. Images the device fetched by address count toward the same 8 MB, but are
its own copies, which it can fetch again, so they make room for a put or another fetch, the least
recently drawn first, but never one the page or the card shows now, nor one drawn in the last 5
seconds: that would leave a hole in what is on the screen. One let go is not fetched again for 10
seconds. An image by address with no room left (the page shows more than 8 MB of them) is not kept,
its place drawn empty, and tried again after a minute, as one that failed is. Images are kept in RAM only, never written to the device's storage, and go with the add-on's
last connection as its scenes do.

#### card.clear

Takes the add-on's card away from over the clock. It is out of view at once: no tap reaches it, nor a
card set after it until the device has drawn that one.

### Events

#### touch

`{"id": "next", "on": "page"}`: a tap on one of the add-on's buttons or chips, or on a list's item with
an id, on its page or its card. Only for what is in view.

#### change

`{"id": "servings", "on": "page", "value": 5}` or `{"id": "warm", "on": "card", "value": true}`: a tap
that changed one of the add-on's steppers (the number it made it, held to its range and places) or
toggles (its new state) on its page or its card. One shape for both: `value` is a number for a stepper
and true or false for a toggle. Only for what is in view, and only off a drawing of the block as it is
now, as for `touch`. A tap on a stepper or a toggle is a tap on the add-on's block, so, as any such tap,
it counts as a person asking (`screen.open`); the add-on's patch after it does not.

#### swipe, back

`{"direction": "left"}` or `"right"`: a swipe across the page in view. `back` (no data): the back
gesture on the page in view. What it does is the add-on's: a step back within its page, or
`screen.close`. The name bar's own way out is the daemon's, and always leaves.

#### screen.state

`{"on": "page", "shown": true}`: the add-on's page came into view, or went out of it (`false`), as when
the owner leaves it or a ring covers it. A page brought up before the add-on set one (its tile tapped as
it starts) comes into view the moment it is set. `{"on": "card", "shown": false, "dismissed": true}`: the
owner swiped the card away; it is gone until the next `scene.set` of the card. Of the card nothing else
is said: it comes and goes with the clock, and when it is in view would tell an add-on when somebody is
in the room looking at the clock.

### What stays

Scenes and images are in memory only, the add-on's own: another add-on can neither see nor patch them,
and touches go only to the add-on whose block was tapped. The device's decoded copies of its images are
kept under its id and the bytes' SHA-256, so one add-on's image never stands for another's, and go with
it too. Its page, its card and its images go when its last connection closes,
when it stops and when it is removed, so a crashed add-on leaves no buttons that nothing answers; it
sets them again after `hello`. Its tile is under the Add-ons tab while it runs.

## From Home Assistant

The `addon_command` action (`esphome.<device>_addon_command`) calls a command:

```yaml
action: esphome.kitchen_addon_command
data:
  addon: meals
  command: step
  args: '{"to": "3"}'
response_variable: answer
```

Home Assistant passes every argument on every call, so `args` is `""` for none. The answer is
`{"text": "…"}`, which a sentence trigger can have the device say. An add-on that is not running
fails the action with `unavailable`, so a voice command says so rather than nothing happening.

A command sent this way counts as somebody asking something of the add-on, whatever sent it, a
person, a voice command or an automation: for 5 seconds after it, an add-on with the screen may bring
its page up (`screen.open`), and at night that lights the screen. An automation that runs an add-on's
command at 3 a.m. can light the screen at 3 a.m.; that is the owner's choice, made in the automation.

Each command that needs no arguments has a button, `button.<device>_addon_<id>_<command>`. A
gesture on the device reaches an add-on through an automation: `esphome.techo5_gesture` triggers it,
and it calls `addon_command`.

## Storage

The daemon writes nothing per message, and logs nothing per message or connection. The socket is on
`/run`, in RAM. An add-on's settings are written to `/data/techo5-addons/<id>/settings.json` (root's,
`0600`) only when Save changes them. Its timers, queues, holds, entity states, scenes and what its
media is doing are in memory only; nothing about `media.state` is logged above Debug, and nothing about
a scene at all. Its images, put or fetched, are in memory only too, never written; a fetch that fails
is said in the log at most once a minute for each add-on.

## The fake daemon

```
cd echod
go run ./cmd/techo5-addon-fake -manifest ../path/to/addon.json -- ../path/to/your-addon
```

It reads `addon.json` as a device would, serves the socket with the device's own server (as yourself,
not as root), grants every capability the manifest lists, logs every request and what it would have
done, and takes from the keyboard what a device would send: `command NAME [JSON]`, `press NAME`,
`entity ID JSON`, `push ID`, `settings JSON`, `test [JSON]`, `stop` and `quit`. `-settings values.json`
gives it settings to start with, and `-family spot` or `dot` changes the device hello describes (a dot
refuses the screen, as a Dot does).

Scenes are checked as a device checks them, and each change is said in the log with its size in blocks
and bytes; `scene page` or `scene card` prints one as it is now. The fake has nothing more pressing to
show, so `screen.open` brings the page up at once, when it may (within 5 seconds of a command or a
tap, as on a device), and every card shows as it is set. From the keyboard, `tile` (a tap on the
add-on's tile, which brings its page up), `touch ID [page|card]`, `swipe left|right`, `back`, `shown page|card on|off` (a ring covering the page,
say; the add-on is told of its page, and nothing of its card), `change ID VALUE [page|card]` (a tap on a stepper, VALUE the number it makes it, or on a toggle, on or off), `dismiss` (the card swiped away) and
`close` (the name bar's way out) do what the owner would. `tile` before the add-on has set its page
brings the waiting page up, and the page is in view as soon as it is set, as on a device.

`-png page.png` draws the page to `page.png` and the card to `page-card.png` each time they change,
with the device's own layout code, fonts and theme, at the size `-size` names: `show5` (960 by 480),
`show8` (1280 by 800, the default) or `spot` (480 by 480, the circle's outside black; the default with
`-family spot`). Images put are drawn as the device draws them, and one by address once the fake has
fetched it (by the device's rules, from the LAN), which draws the scene again. The name bar says the
add-on's id, where a device says the name its catalog entry gives it, and the colors are the Show's
theme; built with `-tags spot`, the fake draws in the Spot's.

Media the add-on plays buffers for a second and then plays, its position moving on as a device's
does, and `media.state` is sent with the device's own timing. From the keyboard, `media playing`,
`media paused` or `media buffering`, each with a position in seconds if wanted, `media duration
SECONDS`, and an end, `media ended`, `media device`, `media replaced` or `media error`, say what it
does next.

In Go, `Client.StartMedia` answers the media's id, `Client.MediaStatus` asks `media.status`, and
`Client.WatchMedia` has `media.state` events arrive decoded on a channel of their own instead of on
`Events`. `Client.SetScene`, `PatchScene`, `OpenScreen`, `CloseScreen`, `ClearCard`, `PutImage` and `DropImage` are the screen's
ops, `Client.WatchScreen` has its events arrive decoded on a channel of their own, and `Column`, `Row`,
`Card`, `List`, `Text`, `Title`, `Button`, `Chip`, `Stepper`, `Toggle`, `Progress`, `Countdown`, `Image`, `ImageNamed`,
`ImageData`, `Divider` and `Spacer` build blocks; a `change` event's `Number` and `Bool` read its value. `SetScene` checks a scene as the device does before sending it.
