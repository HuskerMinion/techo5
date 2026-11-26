# The add-on API

An add-on installed from TECHO5's catalog can talk to the device it runs on: answer commands from Home
Assistant and voice, say words, play short sounds, set timers that ring on the device, keep the screen
on, have settings of its own on the setup page, add entities to Home Assistant, and play a video or
audio. This is level 1 of the API (`api1`). The plan it was built from, and the stages after it, are in
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
| `media` | Plays video and audio from the network on this device. | `media.play`, `media.pause`, `media.resume`, `media.stop` |

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

The daemon makes `/run/techo5-addons/<id>/api.sock` before the add-on starts, in a folder owned by the
add-on's user with mode `0700`, and gives its path in `TECHO5_ADDON_API`. The socket stays through the
add-on's restarts and goes when it stops.

Who connects is the kernel's word (`SO_PEERCRED`): a user that isn't the add-on's, isn't an installed
add-on switched on, or is root, is closed at once. The add-on's id comes from that, never from a
message.

## Framing

Each message, either way, is a 4-byte big-endian length and then that many bytes of UTF-8 JSON, at most
64 KB. Messages are read strictly: a key that isn't known, a key given twice or in another case, keys
that aren't lowercase ASCII letters, digits and underscores, nesting deeper than 16, or anything after
the value refuses the message.

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
| `busy` | a queue is full, a count is reached, or it is quiet hours |
| `unavailable` | Home Assistant is not connected, or what it needs isn't there |
| `internal` | the daemon failed |

## Limits

- 64 KB a message; one that says it is longer is answered `too_large` and skipped, and one claiming
  more than 1 MB is hung up on.
- 20 requests a second on a connection, with a burst of 40.
- 4 connections per add-on, 64 requests waiting for their answers on each.
- Half a message has 5 seconds to arrive in full. An add-on that stops reading what it is sent is
  disconnected once 128 messages wait for it.
- 20 requests refused for limits within a minute disconnect the add-on, and refuse it, for a minute.
  Its card says so.

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
the clock and the ring, rings, is stopped by the stop word and the Stop button, and is kept over a
restart, as any timer set on the device. An add-on has at most 8 at once (`busy`), and lists
(`{"timers": [{"id", "name", "left", "total", "running"}]}`) and cancels (`{"id": "local:…"}`) only
its own. Its timers stay when it stops or crashes, and are canceled when it is removed.

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
a second for each entity. States are not saved: set them again after `hello`.

### media.play, media.pause, media.resume, media.stop

`media.play` `{"url": "https://…", "kind": "video", "title": "Step 3"}` plays a video full screen (not
on a Dot: `unavailable`), or with `"kind": "audio"` audio on the speaker, from an http or https
address. `media.pause`, `media.resume` and `media.stop` act on what the add-on started, while it is
still what plays, and on nothing else (`bad_request`). `busy` in quiet hours.

## Events

### command

`{"call": 3, "command": "step", "args": {"to": "3"}, "from": "action"}`: a command called from Home
Assistant, its arguments checked against its declaration. With a `call`, Home Assistant waits up to 4
seconds for a `command.reply`, and answers with its text; without one (`"from": "button"`, a press of
the command's button) nothing waits.

### timer

`{"id": "local:…", "name": "rice", "state": "ringing"}`: one of the add-on's timers rang (`ringing`),
its ring was stopped (`stopped`), or something other than the add-on canceled it (`canceled`).

### settings

`{"values": {…}}`: the owner saved new settings on the setup page.

### settings.test

`{"call": 4, "values": {…}}`: the owner pressed Test, with these values in the form (a secret left
empty is the one saved). Nothing is saved. Answer with `settings.reply` within 10 seconds.

### entity.command

`{"id": "cooking", "state": true}`, or `{"id": "next", "press": true}` for a button: Home Assistant set
one of the add-on's entities. The add-on carries it out and sets the state itself with `entity.set`,
so the entity never says what didn't happen.

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

Each command that needs no arguments has a button, `button.<device>_addon_<id>_<command>`. A
gesture on the device reaches an add-on through an automation: `esphome.techo5_gesture` triggers it,
and it calls `addon_command`.

## Storage

The daemon writes nothing per message. The socket is on `/run`, in RAM. An add-on's settings are
written to `/data/techo5-addons/<id>/settings.json` (root's, `0600`) only when Save changes them; its
timers are written when one is set and when one ends, as any timer on the device. Queues, holds and
entity states are in memory.

## The fake daemon

```
cd echod
go run ./cmd/techo5-addon-fake -manifest ../path/to/addon.json -- ../path/to/your-addon
```

It reads `addon.json` as a device would, serves the socket with the device's own server (as yourself,
not as root), grants every capability the manifest lists, logs every request and what it would have
done, and takes from the keyboard what a device would send: `command NAME [JSON]`, `press NAME`,
`entity ID JSON`, `push ID`, `settings JSON`, `test [JSON]`, `stop` and `quit`. `-settings values.json`
gives it settings to start with, and `-family spot` or `dot` changes the device hello describes.
