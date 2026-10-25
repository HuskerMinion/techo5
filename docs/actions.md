# Home Assistant actions

TECHO5 exposes a handful of [ESPHome actions](https://esphome.io/components/api.html#actions) once
a device connects. In Home Assistant they show up under the `esphome` domain, named
`esphome.<node>_<action>` — a device named `office` exposes `esphome.office_alarm_set`, and so on.
The `<node>` prefix throughout this page is whatever name you gave the device when it was added;
substitute your own. Call any of them from **Developer Tools → Actions**, a script, or an
automation.

> **Good to know**
>
> Home Assistant registers every argument of an action as required, and refuses a call that leaves
> one out — or that sends one the action doesn't have. Where you have no opinion on an argument,
> send the value that means "no opinion" (`0` for a number, an empty string for text) rather than
> omitting it. Adding an argument to an existing action therefore breaks every automation already
> calling it, which is why a new one arrives as a new action instead.
>
> That goes for the arguments marked *Can be empty* below as well: the action does without a value
> for them, but they still have to be in the call, as `""` (or `0` for a number). Home Assistant
> answers a call without one with `required key not provided`.

## Connect the device to Home Assistant

In YAML, refer to this action as `esphome.<node>_home_assistant`.

Stores the URL and a long-lived access token the device uses to call Home Assistant's REST API
directly, separately from the ESPHome connection Home Assistant uses to talk to it. This is what
lets the device fetch things on its own — a weather forecast, a camera snapshot, a media library
listing — rather than only reporting sensors and taking commands. It's normally set once, by
whatever paired the device, and isn't something you call from an automation afterwards.

> **Good to know**
>
> Several other actions depend on this being set first: `home_show_camera` and
> `home_show_camera_sound`, `home_weather`, `home_cameras`' automatic camera list (All, when called
> with `cameras` empty), `home_slideshow`, Home Assistant's Radio Browser lists on the Radio page, and the
> player's star. They all fetch in the background, so the action itself succeeds either way. Without
> the token they show or offer nothing, and the camera actions open the view with
> `hass: no access configured` on it in place of the picture. The radio lists are the exception: the
> device then asks radio-browser.info itself, when its place is set on the setup page.
>
> The URL must be an address the device can reach on your network. The device calls it directly
> rather than through Home Assistant's own connection to the device, so an address that only works
> from outside your home, such as a Nabu Casa URL, does not work. An `https://` name that your local
> DNS points at Home Assistant or a reverse proxy on the LAN works, as long as its certificate is
> valid for that name. Its IP address works everywhere. A `.local` name like `homeassistant.local` works on a Show, a
> Spot and the Skylight Calendar, which look it up themselves, but not on the Dot or the Echo 2nd gen,
> and on a panel only where Android looks `.local` names up.

### url (Required)

*string*

Home Assistant's base URL, reachable from the device.

### token (Required)

*string*

A long-lived access token, created from your Home Assistant user profile (**Settings → your
profile → Security → Long-lived access tokens**).

```yaml
action: esphome.office_home_assistant
data:
  url: "http://192.168.1.10:8123"
  token: !secret techo5_office_token
```

## Disconnect the device from Home Assistant for good

In YAML, refer to this action as `esphome.<node>_leave_home_assistant`.

For a device you're giving to someone else. The device forgets the URL and token set with
`home_assistant`, and realtime voice's key and Home Assistant token, replaces the encryption key Home
Assistant connects with by a new random one, and restarts. Your Home Assistant can't connect to it again, and the device no longer calls your Home
Assistant. Alarms, radio stations, Wi-Fi and its other settings stay.

Afterward, delete the device from **Settings → Devices & services → ESPHome** in your Home Assistant.
The new key isn't shown anywhere. To add the device to another Home Assistant later, open its setup
page, go to **General**, and choose **Let a Home Assistant add this device**: for 15 minutes the
device has no key, and the Home Assistant that adds it sets one. Home Assistant sends that key over
your network unencrypted, as it does for ESPHome devices, so open the window only on a network you
trust. Adding a device with nothing typed needs a recent Home Assistant (tested with 2026.9).

### confirm (Required)

*string*

Must be `leave`. Anything else changes nothing, so a stray tap in the action list is harmless.

```yaml
action: esphome.office_leave_home_assistant
data:
  confirm: leave
```

## Set the network address

In YAML, refer to this action as `esphome.<node>_network_address`.

Sets how the device gets its address on Wi-Fi: from the router (DHCP), or fixed. It is the
**Connections → Network address** setting of the setup page, explained in
[Setting it up](setup.md#9-a-fixed-address). The device answers first and changes its address two
seconds later.

A new fixed address is tried before it is kept: if another device answers for it, or the gateway does
not answer from it, the device keeps the setting it had, and the setup page (and the device's log) says
why. Calling it with the setting already in use, and in place, changes nothing; called while the device
uses DHCP in its place, it tries the address again. Home Assistant follows the device to its new
address by itself, about two minutes after the change. The device's IP address sensor shows the address
in use. The action is on the Echo Show and the Echo Spot, not on the Echo Dot.

Whoever can use the device's ESPHome API can call it, and so can set the gateway and the DNS servers the
device uses, and with them where its traffic and its name lookups go: keep the API key to the Home
Assistant you trust.

### address (Required)

*string*

The address, like `192.168.1.50`, or with its prefix, like `192.168.1.50/24` (`/24` when left out).
Empty switches back to DHCP, and the other two are then ignored.

### gateway (Required)

*string*

The router, like `192.168.1.1`. It must be in the address's network. Empty when `address` is empty.

### dns (Required)

*string*

Up to three DNS servers, separated by commas. Empty uses the gateway, and is ignored when `address`
is empty. All three empty switches the device to DHCP.

```yaml
action: esphome.office_network_address
data:
  address: 192.168.1.50/24
  gateway: 192.168.1.1
  dns: ""
```

Back to DHCP:

```yaml
action: esphome.office_network_address
data:
  address: ""
  gateway: ""
  dns: ""
```

## Set an alarm

In YAML, refer to this action as `esphome.<node>_alarm_set`.

Sets an alarm on the device, or turns back on the one that already rings at that time, on those
days, with that label. Alarms set this way ring from the device's own clock, even while Home
Assistant is down.

### time (Required)

*string*

A time of day: `7:30`, `07:30`, `19:30:00`, `7:30 pm`, `7:30pm`, `3 pm`, or `3pm`.

### days (Can be empty)

*string*

Which days it repeats on. One of `once` (the default — rings once and turns itself off), `daily`
(or `every day`, `everyday`), `weekdays`, `weekends`, or a comma- or space-separated list of days,
matched by prefix: `mon,wed,fri`, `tue thu`.

### label (Can be empty)

*string*

Free text shown on the device's screen and in Home Assistant's next-alarm sensor. When the alarm
rings, the device also has Home Assistant say it out loud once (this needs **Allow the device to
perform Home Assistant actions**; without it the alarm just rings).

```yaml
action: esphome.office_alarm_set
data:
  time: "7:30 am"
  days: weekdays
  label: Wake up
```

## Set a silent alarm

In YAML, refer to this action as `esphome.<node>_alarm_set_silent`.

The same as `alarm_set`, with the same `time`, `days` and `label`, for an alarm that makes no sound
and shows no ring: it only fires the `silent` [alarm event](#alarm-events), for an automation to wake
the house its own way (the radio, the blinds, a light). Setting the same alarm again with
`alarm_set` makes it ring again.

```yaml
action: esphome.office_alarm_set_silent
data:
  time: "6:45 am"
  days: weekdays
  label: Bedroom
```

## Set a light-only alarm

In YAML, refer to this action as `esphome.<node>_alarm_set_light_only`.

The same as `alarm_set`, with the same `time`, `days` and `label`, for a wake light and nothing
else: the light comes up before it as for any alarm with **Wake with light** (the device's default,
or 30 minutes where that is off), and at its time there is no sound and nothing to dismiss. The night
ends instead, as the **Night mode** switch turned off does, so the screen is back as by day until the
night hours next start. The light, and **Wake light brightness** with it, stays at full for 30
minutes after the alarm's time, then goes off; **Stop alarm or timer**, the stop word, the action
button or a tap on the light's page turns it off sooner. On a Dot there is no screen: the light ring
comes up, holds for 30 minutes, and that is all. It fires the `light_only`
[alarm event](#alarm-events). Setting the same alarm again with `alarm_set` makes it ring again.
**Light only** in an alarm's editor on the device, and on the setup page, does the same.

```yaml
action: esphome.office_alarm_set_light_only
data:
  time: "6:30 am"
  days: weekdays
```

## Alarm events

Each step of an alarm fires `esphome.techo5_alarm` on Home Assistant's bus, with:

| Field | |
|---|---|
| `event` | `ringing`, `silent` (a silent alarm went off), `light_only` (a light-only alarm's time came and the night ended), `snoozed`, or `stopped` (by a press, by voice, from Home Assistant, or after ringing its course) |
| `id` | The alarm's id, the same for an alarm and its snoozes |
| `label` | The alarm's label |
| `due` | When it was due, like `2026-10-01T06:45:00-06:00` |
| `device` | The device's name |

A snoozed alarm fires `ringing` again when it comes back. Reminders fire nothing here. The device
needs **Allow the device to perform Home Assistant actions** turned on in its ESPHome integration
options for events to arrive.

```yaml
triggers:
  - trigger: event
    event_type: esphome.techo5_alarm
    event_data:
      event: silent
      label: Bedroom
actions:
  - action: script.good_morning
```

## Follow the wake light

Two sensors show the wake light (Wake with light) as it is now, on every device:
`sensor.kitchen_echo_wake_light_brightness`, from 0 to 100 percent, and
`sensor.kitchen_echo_wake_light_color`, the light's color at full strength like `#ff9b1a`, empty while
it is off. They change at most every 5 seconds while the light comes up, and at once when it comes on
or goes off. To bring a room light up with it:

```yaml
triggers:
  - trigger: state
    entity_id: sensor.kitchen_echo_wake_light_brightness
conditions:
  - condition: numeric_state
    entity_id: sensor.kitchen_echo_wake_light_brightness
    above: 0
actions:
  - action: light.turn_on
    target:
      entity_id: light.bedroom
    data:
      brightness_pct: "{{ states('sensor.kitchen_echo_wake_light_brightness') | int }}"
      rgb_color: >-
        {% set c = states('sensor.kitchen_echo_wake_light_color') %}
        {{ [c[1:3] | int(base=16), c[3:5] | int(base=16), c[5:7] | int(base=16)] }}
```

**Replace `kitchen_echo` with your device's own prefix, and `light.bedroom` with your light.**

## Voice request events

Each finished voice request (a turn: wake, listen, answer) fires `esphome.techo5_voice` on Home
Assistant's bus, with:

| Field | |
|---|---|
| `ended` | `answered`, `no_speech` (nothing said, or only what speech-to-text writes for noise), `canceled` (stopped, interrupted, cut off by another sound, or answered by another device), or `error` |
| `error` | Only when `ended` is `error`: Home Assistant's error code, like `stt-stream-failed`, or `timeout` when the pipeline took longer than the slot allows |
| `trigger` | What opened it: `wake_word`, `button`, `follow_up` (listening again after a reply), or `home_assistant` (an announcement that asked for an answer) |
| `wake_word` | The slot's wake word. It picked the pipeline, so it is there for a button or follow-up too |
| `slot` | The wake word slot, `1` or `2` |
| `heard` | What speech-to-text heard, up to 1000 characters |
| `reply` | The reply's text, up to 1000 characters |
| `listen_ms`, `think_ms`, `reply_ms` | How long each phase took, in milliseconds. A phase the turn never reached is left out |
| `audio_seconds` | Seconds of recording kept for this turn, when one was |
| `id` | The turn's id |
| `version` | `1`; bumped if a field changes meaning |
| `device` | The device's name |

All values are text, as with every ESPHome event. No audio is sent. The device needs **Allow the
device to perform Home Assistant actions** turned on, as for alarm events.

```yaml
triggers:
  - trigger: event
    event_type: esphome.techo5_voice
    event_data:
      ended: error
actions:
  - action: persistent_notification.create
    data:
      title: "Voice request failed on {{ trigger.event.data.device }}"
      message: >-
        Heard "{{ trigger.event.data.heard | default('') }}",
        error {{ trigger.event.data.error }}.
```

Events do not show in the logbook by themselves. To keep a line there for each request, add an
automation that triggers on `esphome.techo5_voice` with no `event_data` and runs `logbook.log` with
`name: "{{ trigger.event.data.device }}"` and `message: "heard {{ trigger.event.data.heard | default('nothing') }}, {{ trigger.event.data.ended }}"`.

Beside it, each request also fires `esphome.echolocal_turn`, the same request as EchoLocal reported
it, for EchoLocal's Home Assistant integration. Home Assistant's recorder keeps both events in its
database. If you don't use EchoLocal's integration, leave that one out in `configuration.yaml`:

```yaml
recorder:
  exclude:
    event_types:
      - esphome.echolocal_turn
```

## Command word events

Each time a command word acts (the [volume down word](setup.md#the-volume-down-word), or the [volume
up or next song word](setup.md#the-volume-up-and-next-song-words)), it fires `esphome.techo5_command`
on Home Assistant's bus, with:

| Field | |
|---|---|
| `command` | What the word did: `volume_down`, `volume_up` or `next` |
| `word` | The id of the model that heard it, as on the setup page |
| `score` | How sure the model was when it acted, `0.50` to `1.00` |
| `volume` | The volume step it went to, `0` to `30` (`volume_down` and `volume_up` only) |
| `asked` | `next` only: `true` when the device passed the skip to the sender's controls (Music Assistant, or a phone over AirPlay or Bluetooth), `false` when there was nobody to ask. `true` doesn't promise a skip: a radio stream Music Assistant can't skip, or an AirPlay sender that gave the device no remote control, won't take it |
| `device` | The device's name |

The volume words have acted already: the event says so, for an automation or a dashboard to follow.
All values are text, and the device needs **Allow the device to perform Home Assistant actions** turned
on, as for alarm events.

A `next` with `asked: false` is the one to act on: nobody was asked, so for music Home Assistant plays
on the device a track at a time, skip on it. `asked: false` also comes for the device's own radio
stations and for senders it can't ask, such as Spotify Connect, so the automation checks that its queue
is what's playing first:

```yaml
triggers:
  - trigger: event
    event_type: esphome.techo5_command
    event_data:
      command: next
      asked: "false"
      device: Kitchen
conditions:
  - condition: state
    entity_id: media_player.kitchen_queue
    state: playing
actions:
  - action: media_player.media_next_track
    target:
      entity_id: media_player.kitchen_queue
```

**Replace `Kitchen` with your device's name, and `media_player.kitchen_queue` with the media player
that plays the queue.**

## Show an effect on the ring during a request

In YAML, refer to this action as `esphome.<node>_voice_effect`.

Puts an effect on the ring for the step the running voice request is in, for an automation to show
what the request is waiting on: a slower speech-to-text taking over, a long answer. Nothing is saved:
the next step, and the next request, show their own effects as ever. With no request running it does
nothing.

### effect (Required)

*string*

An effect the ring has, as the effect lists in Home Assistant name them, or `None` to turn the ring
off for the step, or `Default` to put back the step's own effect. Any other name is refused.

```yaml
action: esphome.kitchen_echo_voice_effect
data:
  effect: Ice Comet
```

## Set a reminder

In YAML, refer to this action as `esphome.<node>_reminder_set`.

Sets a reminder: at its time the device chimes, says the label out loud once, and leaves it on the
screen until somebody taps it or presses **Stop alarm or timer**. It can go off on other devices in the
house as well, and stopping it on any one of them stops it on all of them. Reminders sound during
quiet hours, like alarms. A Dot has no screen, so there it is the chime and the words, and nothing is
left to dismiss afterwards. Answers with the new reminder's `id` if called with a `response_variable`.

> **Good to know**
>
> The device can't turn words into speech itself, so it asks Home Assistant to say the label, in the
> voice your assistant already uses. That needs **Allow the device to perform Home Assistant
> actions** turned on for each device (see [getting started](getting-started.md)). Without it, or
> with Home Assistant down, a reminder still chimes and shows its label; it just isn't spoken.
>
> Going off on other devices uses the same device-to-device link as announcements, so every device
> needs the same **house word** set on its setup page. A device that hasn't been updated yet shows
> the reminder as an announcement instead.

### time (Required)

*string*

A time of day, in the same formats as `alarm_set`'s `time`, or a time from now: `in 20 minutes`,
`1 hour and 30 minutes`, `1h30m`. A time from now is rounded up to the next whole minute and
happens once. It must be less than a day away: for tomorrow at the same time or later, give a time
of day instead.

### days (Can be empty)

*string*

Same as `alarm_set`'s `days`. Leave it empty, or use `once`, for a time from now. For a one-off on a particular day, give the day instead: `today`, `tomorrow`, or a date written
`2026-09-29`. A day whose time has already gone by is refused.

### label (Required)

*string*

What to say, such as `Take the medication`. A reminder with nothing to say is refused.

### ring_on (Can be empty)

*string*

Other devices it goes off on too, by the names they have in TECHO5, comma-separated
(`Kitchen, Office`), or `everywhere` for every device in the house. Left blank, it goes off only on
this device. It always goes off on this device as well.

```yaml
action: esphome.office_reminder_set
data:
  time: "in 20 minutes"
  label: Take the pasta off
  ring_on: Kitchen
response_variable: set
```

## Stop a ring and set reminders by voice

Home Assistant keeps no alarms or reminders of its own, so "stop the alarm" or "remind me to..." said
to a TECHO5 device reaches Home Assistant with nothing there to act on, and it answers "OK" having
done nothing. Three automations hand those sentences back to the device that heard them. Add each one
in Settings > Automations & scenes > Create automation > Edit in YAML.

The device stops a ring by itself as well, without these: while an alarm or timer rings, the wake
word silences it, and a sentence that only asks to stop ("stop", "stop the alarm", "turn it off")
ends it. A sentence that asks to snooze ("snooze", "snooze for ten minutes", "snooze the alarm
another 5 minutes") snoozes it, for that long or for the device's snooze length, from 1 to 30
minutes; a ringing timer cannot be put off, so it stops. The automations are what make Home Assistant
answer "Stopped." or "Snoozed until 7:42 AM." rather than not understanding.

```yaml
alias: TECHO5 - stop alarm or timer by voice
triggers:
  - trigger: conversation
    command:
      - "stop [the] (alarm|timer)"
      - "turn off [the] (alarm|timer)"
      - "stop [the] ringing"
conditions:
  - "{{ device_entities(trigger.device_id) | select('search', 'stop_alarm') | list | count > 0 }}"
actions:
  - action: button.press
    target:
      entity_id: "{{ device_entities(trigger.device_id) | select('search', 'stop_alarm') | first }}"
  - set_conversation_response: "Stopped."
mode: queued
```

```yaml
alias: TECHO5 - snooze an alarm by voice
triggers:
  - trigger: conversation
    command:
      - "snooze [the] [alarm|it]"
      - "snooze {rest}"
conditions:
  - "{{ device_entities(trigger.device_id) | select('search', 'stop_alarm') | list | count > 0 }}"
actions:
  - action: esphome.{{ device_attr(trigger.device_id, 'name') | slugify }}_alarm_snooze_for
    data:
      sentence: "{{ trigger.sentence }}"
    response_variable: snooze
    continue_on_error: true
  - if:
      - condition: template
        value_template: "{{ snooze is defined and snooze.snoozed | default(false) }}"
    then:
      - set_conversation_response: "Snoozed until {{ snooze.until }}."
    else:
      - set_conversation_response: "{{ 'Stopped.' if snooze is defined and snooze.stopped | default(false) else 'Nothing is ringing.' }}"
mode: queued
```

```yaml
alias: TECHO5 - set a reminder by voice
description: 'Home Assistant has no reminders of its own, so it hands "remind me..." to the TECHO5 device that heard it. The sentence is taken apart here: a time however it is written, or a length of time, days and weekly/daily, and the words.'
triggers:
- trigger: conversation
  command:
  - remind me {rest}
  - set [a|an] [new] [weekly|daily|recurring] reminder {rest}
  - (create|add|make) [a|an] [new] [weekly|daily|recurring] reminder {rest}
conditions:
- condition: template
  value_template: '{{ device_entities(trigger.device_id) | select(''search'', ''stop_alarm'') | list | count > 0 }}'
actions:
- variables:
    p: |2-

      {%- set raw = (trigger.slots.rest | default('')) | replace('A.M.','am') | replace('a.m.','am') | replace('P.M.','pm') | replace('p.m.','pm') | replace('A.M','am') | replace('a.m','am') | replace('P.M','pm') | replace('p.m','pm') | replace('AM','am') | replace('PM','pm') -%}
      {%- set whole = (trigger.sentence ~ ' ' ~ raw) | lower -%}
      {%- set clock = (raw | regex_findall('(?i)(?:^|[^0-9])([0-9]{1,2}(?:[.:-][0-9]{2})? ?(?:am|pm)|[0-9]{1,2}[.:-][0-9]{2}|noon|midnight)(?=[^0-9a-z]|$)') | first) if raw | regex_search('(?i)(?:^|[^0-9])([0-9]{1,2}(?:[.:-][0-9]{2})? ?(?:am|pm)|[0-9]{1,2}[.:-][0-9]{2}|noon|midnight)(?=[^0-9a-z]|$)') else '' -%}
      {%- set span = (raw | regex_findall('(?i)(?:^|[^a-z0-9])((?:[0-9]+|an?|one|two|three|four|five|ten|fifteen|twenty|thirty|half an?) ?(?:seconds?|secs?|minutes?|mins?|hours?|hrs?))(?=[^a-z]|$)') | first) if raw | regex_search('(?i)(?:^|[^a-z0-9])((?:[0-9]+|an?|one|two|three|four|five|ten|fifteen|twenty|thirty|half an?) ?(?:seconds?|secs?|minutes?|mins?|hours?|hrs?))(?=[^a-z]|$)') else '' -%}
      {%- set ns = namespace(days=[]) -%}
      {%- for d in ['monday','tuesday','wednesday','thursday','friday','saturday','sunday'] if d in whole -%}{%- set ns.days = ns.days + [d] -%}{%- endfor -%}
      {%- set repeat = 'weekly' in whole or 'every ' in whole or 'each ' in whole -%}
      {%- set days = 'daily' if ('daily' in whole or 'every day' in whole or 'each day' in whole) else ('weekdays' if 'weekday' in whole else ('weekends' if 'weekend' in whole else (ns.days | join(',') if ns.days and repeat else ''))) -%}
      {%- set when = (clock | replace('.', ':') | replace('-', ':')) if clock else ('in ' ~ span if span else '') -%}
      {%- set cut = raw -%}
      {%- if clock -%}{%- set cut = cut | replace(clock, ' ') -%}{%- endif -%}
      {%- if span -%}{%- set cut = cut | replace(span, ' ') -%}{%- endif -%}
      {%- set label = cut
        | regex_replace('(?i)(^|[ ,])(?:on |every |each )?(?:mondays?|tuesdays?|wednesdays?|thursdays?|fridays?|saturdays?|sundays?|weekdays?|weekends?|day)(?=[ ,.!?]|$)', ' ')
        | regex_replace('(?i)(^|[ ,])(?:weekly|daily|every week|each week)(?=[ ,.!?]|$)', ' ')
        | regex_replace('(?i)(^|[ ])(?:at|for|in|after|by|on)(?=[ ]*([.,!?]|$))', ' ')
        | regex_replace('(?i)(^|[ ])(?:at|for|in|after|by|on) (?=(at|for|in|after|by|on|to) )', ' ')
        | regex_replace('[ ]+', ' ') | trim
        | regex_replace('^[.,!?;: ]+', '') | regex_replace('[.,!?;: ]+$', '')
        | regex_replace('(?i)^(?:(?:at|for|in|by|on) )+', '')
        | regex_replace('(?i)^(?:to|that|for|about|of) ', '') | trim -%}
      {%- set label = label | regex_replace('(?i) (?:at|for|in|by|on)$', '') | trim -%}
      {{ {'when': when, 'days': days, 'label': label, 'oneoff': (ns.days | count > 0 and not repeat)} }}
- choose:
  - conditions:
    - condition: template
      value_template: '{{ p.oneoff }}'
    sequence:
    - set_conversation_response: I can't set a one-time reminder for a particular day yet. Say weekly, or every Tuesday, to repeat it, or give just a time for the next time it comes round.
  - conditions:
    - condition: template
      value_template: '{{ p.when == '''' }}'
    sequence:
    - set_conversation_response: What time should I remind you? Try at 7:30 PM, or in 20 minutes.
  - conditions:
    - condition: template
      value_template: '{{ p.label == '''' }}'
    sequence:
    - set_conversation_response: What should I remind you about?
  default:
  - action: esphome.{{ device_attr(trigger.device_id, 'name') | slugify }}_reminder_set
    data:
      time: '{{ p.when }}'
      days: '{{ p.days }}'
      label: '{{ p.label }}'
      ring_on: ''
    response_variable: set
    continue_on_error: true
  - if:
    - condition: template
      value_template: '{{ set is defined and set.id is defined }}'
    then:
    - set_conversation_response: 'OK, I''ll remind you to {{ p.label }} {{ p.when if p.when.startswith(''in '') else ''at '' ~ p.when }}{{ {''daily'': '' every day'', ''weekdays'': '' on weekdays'', ''weekends'': '' on weekends'', '''': ''''}.get(p.days, '' every '' ~ (p.days | replace('','', '', '') | title)) }}.'
    else:
    - set_conversation_response: Sorry, I couldn't set that reminder. Try a time like 7:30 PM, or in 20 minutes.
mode: queued
```

All three find the device that heard the sentence, so one of each covers every TECHO5 device in the
house, and a speaker that is not a TECHO5 device is left alone. The snooze automation passes the whole
sentence to `esphome.<name>_alarm_snooze_for`, which reads the length from it the same way the device
does and answers with `snoozed`, `minutes` and `until` (or `snoozed: false`, with `stopped` if a timer
was stopped instead). Like the reminder's, its action name comes from the device's name in Home
Assistant.

The reminder automation takes the whole sentence apart itself, because speech to text is not
consistent about how it writes a time: "8.14am", "8.14 a.m." and "8-18 AM" all turn up. It takes a
time of day or a length of time ("for 3 minutes", "in 20 minutes"), days with "weekly", "every" or
"daily" ("every weekday", "weekly ... on Wednesday"), and the words to say, and it answers with what
it set. It also catches "set a reminder...", which Home Assistant would otherwise take as a timer. A
one-time reminder on a particular day ("for Tuesday at 6 PM") is not something the device can hold
yet, and it says so. The reminder's action name comes from the
device's name in Home Assistant (`esphome.<name>_reminder_set`); if you renamed the device there,
put its action name in by hand.

## Set, cancel and list alarms, and change the volume, by voice

Home Assistant's own voice commands know timers but not clock alarms, so "set an alarm for 6:30",
"cancel the alarm" and "what alarms do I have" get "Sorry, I don't know" or fall through to an AI
agent if you have one. Its volume command works, but it changes every speaker in the room's area,
so in a room with a TV or another speaker, "volume down" can turn down the wrong one. These three
automations answer those sentences on the TECHO5 device that heard them. Add each one in Settings >
Automations & scenes > Create automation > Edit in YAML. The time can come from speech-to-text as
digits or as words ("6:30" or "six thirty"); both work.

```yaml
alias: TECHO5 - set an alarm by voice
triggers:
  - trigger: conversation
    command:
      - "set [a|an|my] alarm (for|at) {when}"
      - "wake me [up] at {when}"
      - "alarm for {when}"
actions:
  - variables:
      techo5: >-
        {{ trigger.device_id is not none and
           device_entities(trigger.device_id) | select('search', 'stop_alarm') | list | count > 0 }}
      node: "{{ device_attr(trigger.device_id, 'name') | slugify if techo5 else '' }}"
      spoken: "{{ (trigger.slots.when | default('')) | lower | replace('.', '') | trim }}"
      pm: "{{ 'pm' in spoken or 'p m' in spoken }}"
      am: "{{ 'am' in spoken or 'a m' in spoken }}"
      nums: >-
        {%- set w = {'one':1,'two':2,'three':3,'four':4,'five':5,'six':6,'seven':7,'eight':8,'nine':9,'ten':10,'eleven':11,'twelve':12,'thirteen':13,'fourteen':14,'fifteen':15,'sixteen':16,'seventeen':17,'eighteen':18,'nineteen':19,'twenty':20,'thirty':30,'forty':40,'fifty':50,'oh':0,'o':0} -%}
        {%- set ns = namespace(n=[], tens=false) -%}
        {%- for t in spoken | regex_findall('[a-z]+|[0-9]+') -%}
          {%- if t.isdigit() -%}{%- set ns.n = ns.n + [t | int] -%}{%- set ns.tens = false -%}
          {%- elif t in w -%}
            {%- set v = w[t] -%}
            {%- if ns.tens and v > 0 and v < 10 -%}{%- set ns.n = ns.n[:-1] + [ns.n[-1] + v] -%}{%- set ns.tens = false -%}
            {%- else -%}{%- set ns.n = ns.n + [v] -%}{%- set ns.tens = v in [0, 20, 30, 40, 50] -%}{%- endif -%}
          {%- endif -%}
        {%- endfor -%}
        {{- ns.n -}}
      # Speech-to-text can write 6:45 as "645": one number past 99 is the hour and the minutes.
      n: "{{ [nums[0] // 100, nums[0] % 100] + nums[1:] if nums and nums[0] >= 100 else nums }}"
      h: "{{ n[0] | int(-1) if n else -1 }}"
      m: "{{ n[1] | int(0) if n | count > 1 else 0 }}"
      hh: "{{ h + 12 if pm and h < 12 else (0 if am and h == 12 else h) }}"
  - choose:
      - conditions: "{{ not techo5 }}"
        sequence:
          - set_conversation_response: "Say that to the TECHO5 device you want the alarm on."
      - conditions: "{{ h < 0 or hh > 23 or m > 59 }}"
        sequence:
          - set_conversation_response: "Sorry, I didn't catch what time you wanted."
    default:
      - action: "esphome.{{ node }}_alarm_set"
        data:
          time: "{{ '%02d:%02d' | format(hh, m) }}"
          days: once
          label: Alarm
      - set_conversation_response: >-
          Alarm set for {{ '%d:%02d %s' | format(hh % 12 or 12, m, 'AM' if hh < 12 else 'PM') }}.
mode: queued
```

```yaml
alias: TECHO5 - cancel or list alarms by voice
triggers:
  - trigger: conversation
    id: cancel
    command:
      - "(cancel|delete|remove|clear) [the|my] alarm"
      - "(cancel|delete|remove|clear) [all] [of] [the|my] alarms"
      - "(cancel|delete|remove|clear) [the|my] {when} alarm"
      - "(cancel|delete|remove|clear) [the|my] alarm (for|at) {when}"
  - trigger: conversation
    id: list
    command:
      - "what alarms [do I have|are set|have I set]"
      - "what are my alarms"
      - "(list|tell me) [all] [of] my alarms"
      - "do I have any alarms [set]"
      - "(when|what time) is my [next] alarm [set for]"
actions:
  - variables:
      techo5: >-
        {{ trigger.device_id is not none and
           device_entities(trigger.device_id) | select('search', 'stop_alarm') | list | count > 0 }}
      node: "{{ device_attr(trigger.device_id, 'name') | slugify if techo5 else '' }}"
      spoken: "{{ (trigger.slots.when | default('')) | lower | replace('.', '') | trim }}"
      everything: "{{ 'all' in trigger.sentence | lower or 'alarms' in trigger.sentence | lower }}"
      pm: "{{ 'pm' in spoken or 'p m' in spoken }}"
      am: "{{ 'am' in spoken or 'a m' in spoken }}"
      nums: >-
        {%- set w = {'one':1,'two':2,'three':3,'four':4,'five':5,'six':6,'seven':7,'eight':8,'nine':9,'ten':10,'eleven':11,'twelve':12,'thirteen':13,'fourteen':14,'fifteen':15,'sixteen':16,'seventeen':17,'eighteen':18,'nineteen':19,'twenty':20,'thirty':30,'forty':40,'fifty':50,'oh':0,'o':0} -%}
        {%- set ns = namespace(n=[], tens=false) -%}
        {%- for t in spoken | regex_findall('[a-z]+|[0-9]+') -%}
          {%- if t.isdigit() -%}{%- set ns.n = ns.n + [t | int] -%}{%- set ns.tens = false -%}
          {%- elif t in w -%}
            {%- set v = w[t] -%}
            {%- if ns.tens and v > 0 and v < 10 -%}{%- set ns.n = ns.n[:-1] + [ns.n[-1] + v] -%}{%- set ns.tens = false -%}
            {%- else -%}{%- set ns.n = ns.n + [v] -%}{%- set ns.tens = v in [0, 20, 30, 40, 50] -%}{%- endif -%}
          {%- endif -%}
        {%- endfor -%}
        {{- ns.n -}}
      # Speech-to-text can write 6:45 as "645": one number past 99 is the hour and the minutes.
      n: "{{ [nums[0] // 100, nums[0] % 100] + nums[1:] if nums and nums[0] >= 100 else nums }}"
      h: "{{ n[0] | int(-1) if n else -1 }}"
      m: "{{ n[1] | int(0) if n | count > 1 else 0 }}"
      hh: "{{ h + 12 if pm and h < 12 else (0 if am and h == 12 else h) }}"
      # With no AM or PM, "the 6 alarm" matches 6:00 AM and 6:00 PM.
      times: >-
        {{ [] if h < 0 else (['%02d:%02d' | format(hh, m)] if am or pm
           else ['%02d:%02d' | format(h % 12, m), '%02d:%02d' | format(h % 12 + 12, m)]) }}
  - if: "{{ not techo5 }}"
    then:
      - set_conversation_response: "Ask the TECHO5 device that holds the alarms."
      - stop: Not said to a TECHO5 device
  - action: "esphome.{{ node }}_alarms_list"
    response_variable: listed
  - variables:
      # Reminders are in the same list; these sentences leave them alone.
      alarms: "{{ listed.alarms | default([]) | rejectattr('reminder') | list }}"
      picked: "{{ (alarms | selectattr('time', 'in', times) | list) if times else alarms }}"
      said: >-
        {% set ns = namespace(out=[]) %}
        {% for a in (picked if trigger.id == 'cancel' and times else alarms) %}
          {% set t = a.time.split(':') | map('int') | list %}
          {% set ns.out = ns.out + ['%d:%02d %s' | format(t[0] % 12 or 12, t[1], 'AM' if t[0] < 12 else 'PM')] %}
        {% endfor %}
        {{ ns.out[:-1] | join(', ') ~ ' and ' ~ ns.out[-1] if ns.out | count > 1 else ns.out | join }}
  - choose:
      - conditions: "{{ alarms | count == 0 }}"
        sequence:
          - set_conversation_response: "You don't have any alarms set."
      - conditions: "{{ trigger.id == 'list' }}"
        sequence:
          - set_conversation_response: >-
              You have {{ 'one alarm' if alarms | count == 1 else (alarms | count) ~ ' alarms' }}: {{ said }}.
      - conditions: "{{ times | count > 0 and picked | count == 0 }}"
        sequence:
          - set_conversation_response: "You don't have an alarm at that time."
      - conditions: "{{ times | count > 0 or everything or alarms | count == 1 }}"
        sequence:
          - repeat:
              for_each: "{{ picked | map(attribute='id') | list }}"
              sequence:
                - action: "esphome.{{ node }}_alarm_delete_id"
                  data:
                    id: "{{ repeat.item }}"
          - set_conversation_response: >-
              {{ 'Canceled your ' ~ (picked | count) ~ ' alarms.' if picked | count > 1
                 else 'Canceled the ' ~ said ~ ' alarm.' }}
    default:
      - set_conversation_response: >-
          You have {{ said }}. Which one? For example, say cancel the
          {{ said.split(' and ')[0].split(',')[0] }} alarm.
mode: queued
```

```yaml
alias: TECHO5 - volume on the speaker that heard it
triggers:
  - trigger: conversation
    command:
      - "[turn [the]] volume {rest}"
      - "set [the] volume {rest}"
      - "[turn|set] [the] volume (up|down)"
actions:
  - variables:
      speaker: >-
        {{ (device_entities(trigger.device_id) | select('match', 'media_player[.]') | list + [''])
           | first if trigger.device_id is not none else '' }}
      words: "{{ (trigger.sentence | lower | regex_replace('[^a-z0-9 ]', ' ')).split() }}"
      direction: "{{ 'up' if 'up' in words else ('down' if 'down' in words else '') }}"
      named: >-
        {% set map = {'one': 1, 'two': 2, 'three': 3, 'four': 4, 'five': 5,
                      'six': 6, 'seven': 7, 'eight': 8, 'nine': 9, 'ten': 10} %}
        {% set ns = namespace(n='') %}
        {% for w in words if ns.n == '' %}
          {% if w is match('^[0-9]+$') %}{% set ns.n = w | int %}
          {% elif w in map %}{% set ns.n = map[w] %}{% endif %}
        {% endfor %}
        {{ ns.n }}
      now: "{{ state_attr(speaker, 'volume_level') | float(0) if speaker else 0 }}"
  - choose:
      - conditions: "{{ speaker == '' }}"
        sequence:
          - set_conversation_response: "Say that to the speaker you want changed."
      - conditions: "{{ direction == '' and named == '' }}"
        sequence:
          - set_conversation_response: "How loud? Say volume and a number from 1 to 10."
    default:
      - variables:
          # "volume 4" is level 4 of 10, and 11 to 100 is a percent. "Up" or "down" moves one
          # level, or as many as you say ("volume down 3"); "down to 2" sets level 2.
          target: >-
            {% if named != '' and (direction == '' or 'to' in words) %}
              {{ named / 10 if named <= 10 else named / 100 }}
            {% else %}
              {{ now + (0.1 if direction == 'up' else -0.1) * (named if named != '' else 1) }}
            {% endif %}
          level: "{{ [[target | float, 0] | max, 1] | min | round(2) }}"
      - action: media_player.volume_set
        target:
          entity_id: "{{ speaker }}"
        data:
          volume_level: "{{ level }}"
      - set_conversation_response: "Volume {{ (level * 10) | round | int }}."
mode: queued
```

Like the two above, each one finds the device that heard the sentence, so one of each covers every
TECHO5 device in the house. The volume one works for any voice satellite that has its own media
player. "Cancel the alarm" cancels
the only alarm there is, or lists them and asks which when there are several; "cancel all alarms"
cancels every one; "cancel the 6 AM alarm" cancels the ones at that time. Reminders are never
canceled by these. These sentences now belong to the automations wherever they are said, so from the
Home Assistant app, which has no alarms or speaker of its own, they answer by saying which device
to ask. The alarm actions' names come from the device's name in
Home Assistant (`esphome.<name>_alarm_set`); if you renamed the device there, put its action name in
by hand.

## Delete an alarm

In YAML, refer to this action as `esphome.<node>_alarm_delete`.

Deletes every device alarm at the given time. Fails if none is found.

### time (Required)

*string*

Same formats as `alarm_set`'s `time`.

### label (Can be empty)

*string*

Only delete alarms at that time whose label matches (case-insensitive). Left blank, every alarm at
that time is deleted regardless of label.

```yaml
action: esphome.office_alarm_delete
data:
  time: "7:30 am"
  label: Wake up
```

## Delete one alarm by its ID

In YAML, refer to this action as `esphome.<node>_alarm_delete_id`.

Deletes exactly one device alarm or reminder, picked by the ID `alarms_list` gives it. Use this when two alarms
share a time and you want only one of them gone. Fails if there is no alarm with that ID.
It does not cancel timers: a timer's `id` from `alarms_list` goes to `timer_cancel` instead.

### id (Required)

*string*

An alarm's `id` from `alarms_list`.

```yaml
action: esphome.office_alarm_delete_id
data:
  id: "{{ listing.alarms[0].id }}"
```

## List alarms and timers

In YAML, refer to this action as `esphome.<node>_alarms_list`.

Answers with everything on the device that will or might ring: its own alarms, snoozed alarms,
the Home Assistant helpers it follows, the timers counting down, and whatever is ringing right now.
This action answers with data; call it with a `response_variable`.

Takes no parameters.

```yaml
action: esphome.office_alarms_list
response_variable: listing
```

The answer looks like this. Times are in the device's time zone; `next` is empty for an alarm that
is off or will not ring again.

```yaml
ringing: null            # or {label: "Wake up", since: "2026-09-24T06:45:00-05:00"}
alarms:
  - id: "m1x2y3"
    time: "06:45"
    days: weekdays
    label: Wake up
    "on": true
    next: "2026-09-24T06:45:00-05:00"
    reminder: false      # true for one set with reminder_set
    ring_on: []          # the other devices a reminder goes off on
    silent: false        # true for one set with alarm_set_silent
    light_only: false    # true for a wake light alone (alarm_set_light_only)
snoozed: []              # each {label, at}
followed: []             # each {entity, label, armed, next}
timers:
  - id: "local:n4k2"
    label: Pasta
    left_seconds: 272
    total_seconds: 600
    running: true
    local: true          # false for a timer set by voice through Home Assistant
```

## Start a timer

In YAML, refer to this action as `esphome.<node>_timer_start`.

Starts a timer that belongs to the device: it counts down, shows and rings here, and keeps going
with Home Assistant down. Answers with the new timer's `id`, for `timer_cancel`, if called with a
`response_variable`; it works without one too.

### duration (Required)

*string*

How long: `10 minutes`, `1 hour and 30 minutes`, `90 seconds`, `in 20 minutes`, `1h30m`,
`00:10:00` (what Home Assistant's duration selector gives), `1:30` (hours and minutes), or a bare
number, which is minutes. At most 24 hours.

### label (Can be empty)

*string*

A name shown on the screen while it counts down and when it rings, such as `Pasta`.

```yaml
action: esphome.office_timer_start
data:
  duration: "10 minutes"
  label: Pasta
response_variable: started
```

## Cancel a timer

In YAML, refer to this action as `esphome.<node>_timer_cancel`.

Cancels one of the device's own timers, the ones started with `timer_start` or on its screen.
Timers set by voice through Home Assistant's Assist are canceled by voice, the same way they were
set, and this action refuses them with a message saying so.

To silence a timer that is already ringing, press the device's **Stop alarm or timer** button entity
instead; it stops alarms and timers alike.

### id (Required)

*string*

A timer's `id`, from `timer_start`'s answer or `alarms_list`'s `timers`, or `all` to cancel every
timer of the device's own.

```yaml
action: esphome.office_timer_cancel
data:
  id: "{{ started.id }}"
```

## Pause or resume a timer

In YAML, refer to these actions as `esphome.<node>_timer_pause` and `esphome.<node>_timer_resume`.

Pause stops one of the device's own timers where it is; resume sets it counting again from there. A
paused timer is listed as `running: false` in `alarms_list`, shows in the device's **Timers** sensor
as `Bread paused (4:05 left)` after the running ones (as do Assist's timers paused by voice), and
stays paused across a restart. A timer that has just run out is left to ring rather than paused.
Pausing a paused timer, or resuming a running one, changes nothing. Timers set by voice through Home
Assistant's Assist are paused and resumed by voice, and these actions refuse them.

### id (Required)

*string*

A timer's `id`, from `timer_start`'s answer or `alarms_list`'s `timers`, or `all` for every timer of
the device's own.

```yaml
# Pause the kitchen's timers while the doorbell camera is up.
action: esphome.kitchen_timer_pause
data:
  id: all
```

## Add or take time off a timer

In YAML, refer to these actions as `esphome.<node>_timer_add` and `esphome.<node>_timer_remove`.

Gives one of the device's own timers more time, or less, whether it is running or paused. Taking off
more than the timer has left is refused (cancel it instead), as is going past 24 hours or changing a
timer that has just run out, which is left to ring. Assist's timers are changed by voice, as above.

### id (Required)

*string*

A timer's `id`, from `timer_start`'s answer or `alarms_list`'s `timers`. These take one timer, not
`all`.

### duration (Required)

*string*

How much, written the way `timer_start` takes it: `5 minutes`, `1h30m`, `00:05:00` or a bare number
of minutes.

```yaml
action: esphome.office_timer_add
data:
  id: "{{ started.id }}"
  duration: 5 minutes
```

## Follow Home Assistant helpers as alarms

In YAML, refer to this action as `esphome.<node>_alarms_follow`.

Wires `input_datetime` helpers to ring as alarms on the device, alongside any set with `alarm_set`.
Each call replaces the whole followed list — to follow several helpers, list them all in one call
rather than calling this action once per helper.

### entities (Required)

*string*

A comma-separated list. Each entry is either a bare entity —

```text
input_datetime.wake
```

— which is always armed whenever the helper has a time set, or an entity paired with a second one
that arms it, written `entity=arm_entity`:

```text
input_datetime.wake=input_boolean.wake_armed
```

Here the alarm only rings while `input_boolean.wake_armed` is anything other than `off`.

```yaml
action: esphome.office_alarms_follow
data:
  entities: input_datetime.wake=input_boolean.wake_armed,input_datetime.weekend_wake
```

## Cameras from go2rtc

In YAML, refer to this action as `esphome.<node>_home_go2rtc`.

Reads the streams of a go2rtc server into the device's camera list, as cameras of their own named as
go2rtc names them (`go2rtc:porch`), and answers how many there are. Called again, it reads the list
again; with an empty address, it takes them away. See [Setting it up](setup.md#cameras-from-go2rtc).

### address (Required)

*string*

The go2rtc server, as a host, host:port (go2rtc's own port, 1984, when none is given) or URL, with a
user and password in it if go2rtc has them; empty to take the cameras away. Home Assistant's logbook
and traces can keep an action's arguments, so give go2rtc a user of its own that can only view, or
pass the address from a secret.

```yaml
action: esphome.kitchen_home_go2rtc
data:
  address: 192.168.x.y:1984
response_variable: go2rtc
```

## Choose which cameras the device shows

In YAML, refer to this action as `esphome.<node>_home_cameras`.

Sets which of Home Assistant's cameras the device offers on its Cameras page, in the camera grid,
on the setup page's Talk through cameras list and by voice ("show the front door"). There are three
choices, also on the setup page under Connections → Home Assistant cameras:

- **None**: no Home Assistant camera at all.
- **All**: every camera entity Home Assistant has, refreshed every 10 minutes. This is the default for
  a device that was never set, and needs `home_assistant` to be set up first.
- **These**: only the cameras listed, with the names given.

A device that shares a Home Assistant with other homes should be set to None or a list: with All it
offers every camera in that Home Assistant, the other homes' too. Cameras set up on the device itself
(a Reolink recorder, go2rtc) and the device's own camera are offered whichever is chosen.

### cameras (Can be empty)

*string*

`none` (any case) for None, empty (`""`) for All, or a comma-separated list of
`entity=Display Name` pairs for These:

```text
camera.front_door=Front door,camera.deck=Deck
```

The `=Display Name` half can be left off, in which case the entity ID is used with its `camera.`
prefix stripped.

```yaml
action: esphome.office_home_cameras
data:
  cameras: camera.front_door=Front door,camera.deck=Deck
```

```yaml
# No Home Assistant cameras on this device.
action: esphome.office_home_cameras
data:
  cameras: none
```

## Show a camera on screen

In YAML, refer to this action as `esphome.<node>_home_show_camera`.

Puts one camera's live view up on the device's screen for a while — for an automation that shows
the front door when the doorbell rings. The camera's audio follows the device's own **Camera
sound** setting; `home_show_camera_sound` is the same view with the sound decided by the caller.

A camera's sound plays over whatever the device is playing rather than instead of it: the music
carries on underneath, quieter, and comes back up when the view ends. Nothing is taken from the
room's music, so a Music Assistant group is not left.

> **Good to know**
>
> Needs `home_assistant` set up first. This action fetches the camera's snapshot from Home
> Assistant directly. Without it the action still succeeds, but the view shows
> `hass: no access configured` in place of the picture.
>
> With the device's **Live camera video** switch on (off on a new device), a camera Home Assistant can
> stream is shown as live video decoded on the device, from Home Assistant's stream or the camera's own
> RTSP address; other cameras, and any whose stream fails, are shown from snapshots, a few a second. See
> [Cameras](setup.md#4-cameras) for the details.

### entity (Required)

*string*

The camera entity to show. Does not need to be one of the cameras set with `home_cameras`: an
automation can show any camera Home Assistant has, even on a device set to None.

### seconds (Required)

*integer*

How long to show it for. `0` means the default, 30 seconds.

```yaml
action: esphome.office_home_show_camera
data:
  entity: camera.front_door
  seconds: 60
```

## Show several cameras at once

In YAML, refer to this action as `esphome.<node>_home_show_cameras`. On the Echo Show only.

Puts up to six cameras on the screen at once, two or three across, each refreshed from its snapshot
about once a second, or as often as fetching the others allows. A tap on one puts that camera up on
its own, live where Live camera video is on; a tap on the strip at the top or the foot closes the
grid. The cameras' own sound is not played in the grid. The same grid is the **All cameras** row at
the top of the Cameras drawer, when there are two cameras or more.

### cameras (Required)

*string*

The cameras' entity ids, comma separated, in the order they go in: left to right, then down. Empty for
the first six of the device's list (`home_cameras`: None, All or These). Cameras named here are
shown even when they are not on the device's list. The device's
own camera is not shown in the grid, and only cameras are: Home Assistant's (`camera.`), a Reolink
recorder's, and go2rtc's streams (`go2rtc:name`).

### seconds (Required)

*int*

How long the grid stays up, an hour at most; 0 for 30 seconds.

```yaml
# Somebody at the door after dark: the door, the drive and the yard together.
action: esphome.kitchen_home_show_cameras
data:
  cameras: camera.front_door, camera.driveway, camera.yard
  seconds: 60
```

## Show a camera with its sound decided here

In YAML, refer to this action as `esphome.<node>_home_show_camera_sound`.

Shows a camera exactly as `home_show_camera` does, and decides whether its audio plays while the
view is up: a doorbell automation can ask for the front door to be heard whatever the device's own
setting says, or refuse a camera in a room somebody is sleeping in, for that one view. An
automation with no opinion about sound should keep calling `home_show_camera`.

> **Good to know**
>
> Everything `home_show_camera` says applies, `home_assistant` included. The sound additionally
> needs a camera Home Assistant can stream. With **Live camera video** on it is decoded on the device,
> from the camera's own RTSP address where one is set for talking through it, else from Home
> Assistant's stream, about as far behind the camera as the picture; with it off, or when neither
> brings sound, Home Assistant plays the camera's stream to this device and converts it on the way,
> the same way it plays a radio station, some twenty seconds behind. A camera that cannot be streamed
> simply stays silent, and the picture is unaffected.

### entity (Required)

*string*

The camera entity to show. Does not need to be one of the cameras set with `home_cameras`.

### seconds (Required)

*integer*

How long to show it for. `0` means the default, 30 seconds.

### sound (Required)

*string*

Whether to play the camera's audio with the view: `on` or `off`. Anything else — an empty string
included — leaves it to the device's own **Camera sound** setting, off on a new device.

The sound is heard over whatever the device is playing, which carries on underneath and comes back
up when the view ends — further down than it goes for an answer, since a camera's own audio is what
its microphone hears and is lost under a room's music otherwise. A **Mute** control on the view silences it without closing it: the stream
keeps arriving and what arrives is thrown away rather than played, so the music comes back up to its
own level and **Unmute** brings the sound back at once. The control reads Unmute whenever there is
nothing to silence — silenced from the screen, taken by an answer or an announcement, or never
arrived. A reply or an announcement takes the speaker from the camera for as long as it lasts, and
the view's sound comes back after it. The sound stops when the view does.

```yaml
action: esphome.office_home_show_camera_sound
data:
  entity: camera.front_door
  seconds: 60
  sound: "on"
```

## Buttons on a camera's view

In YAML, refer to this action as `esphome.<node>_home_camera_buttons`. On devices with a screen,
not the Dot.

Puts up to four buttons on one camera's view that each run a Home Assistant action, such as
**Open gate** or **Unlock**. Each call sets that camera's whole set, in the order given, and replaces
what it had. The same buttons are on the setup page under Connections → Camera buttons. See
[Camera buttons](setup.md#camera-buttons).

> **Good to know**
>
> Home Assistant runs the buttons' actions as the device's own, so they need **Allow the device to
> perform Home Assistant actions** turned on for the device in its ESPHome integration's options.
> Without it Home Assistant refuses them and says so only in its own log; a button whose device has
> no Home Assistant connected says so on the button for a few seconds.
>
> Every button asks for a second tap: the first lights it up with "Tap again: …", and a second tap
> within 4 seconds runs it. A tap on a button never closes the view or starts Talk.

### camera (Required)

*string*

The camera's entity as the device's camera list has it: `camera.front_door` for one of Home
Assistant's, `go2rtc:name` for a go2rtc stream, `reolink:0` for a camera on a Reolink recorder, or
`local` for the device's own.

### buttons (Can be empty)

*string*

The buttons, split by commas or new lines, each `Label=domain.service`, or
`Label=domain.service:domain.object` to run the action on an entity. A label is one line of 24
characters at most; the action and the entity are Home Assistant ids (lowercase letters, digits and
underscores). A list with any bad button is refused whole, with the reason. Empty (`""`) takes the
camera's buttons off.

```yaml
action: esphome.office_home_camera_buttons
data:
  camera: camera.front_door
  buttons: Unlock=lock.unlock:lock.front_door, Open gate=script.open_gate
```

```yaml
# No buttons on the front door's view.
action: esphome.office_home_camera_buttons
data:
  camera: camera.front_door
  buttons: ""
```

## Show a notification

In YAML, refer to this action as `esphome.<node>_notify`.

Puts a notification up on this device's screen: a card in the middle of the screen with a heading (an
icon and a title, either optional) and a message of up to three lines. It can chime as it comes up and
can be said aloud. It stays until somebody taps it or its time runs out (12 hours at most when no time
is given).

Every TECHO5 has its own `notify`, and a call shows on that device alone. To reach several devices,
call each one's action; see [Notifying every device](#notifying-every-device).

> **Good to know**
>
> Home Assistant requires every argument on every call. To leave one out, pass `""` (or `0` for
> `seconds`). An automation that omits an argument fails before it reaches the device.
>
> `speak: "on"` needs **Allow the device to perform Home Assistant actions** turned on for the device
> in Home Assistant's ESPHome integration (**Settings → Devices & services → ESPHome → the device →
> Configure**), as reminders do. Without it the notification still shows and chimes; it just isn't said.
>
> `chime`, `speak` and `sound` take on and off the way Home Assistant sends them. An unquoted `off` in
> YAML is a boolean and arrives as `False`; that works, and so does an unquoted `on` (`True`).
>
> A Dot has no screen: it chimes, says the words if asked to, fires the events, and shows nothing. With
> no `seconds`, its notification ends (`expired`) once the chime and the words are done. Quiet hours
> keep it quiet (a Dot has no night hours of its own).

### Arguments

| Argument | Type | Accepted values | Empty / `0` means |
|---|---|---|---|
| `message` | string | Any text. Up to 240 bytes; longer is cut short. New lines and tabs become spaces, other control characters are dropped. Three lines fit on a Show; more ends in "…". | Not allowed: the call fails with *the message is empty*. |
| `title` | string | Any text. Up to 40 bytes; longer is cut short. Shown in capitals above the message. | No heading text. |
| `icon` | string | A [Material Design Icons](https://pictogrammers.com/library/mdi/) name with its prefix, such as `mdi:washing-machine` or `mdi:bell-ring`. Shown left of the title. An unknown name shows no icon. | No icon. |
| `chime` | string | `"on"`, `true`, `yes` or `1` to chime; `"off"`, `false`, `no` or `0` to come up without one. Any letter case; any other text chimes. | The usual chime. |
| `speak` | string | `"on"`, `true`, `yes` or `1` to have the title and message said aloud after the chime; `"off"`, `false`, `no` or `0` to keep them to the screen. Any letter case; any other text leaves them unsaid. | Not said. |
| `tag` | string | Any text you choose, carried back as `tag` on this notification's events so an automation can tell its notification from others. | No tag (`""` in the events). |
| `seconds` | integer | How long it stays up, in seconds. At most a day (86400); anything longer is held to a day. | Until somebody taps it, but no longer than 12 hours, then it ends as `expired`. A negative number means the same. On a Dot, until its chime and words are done. |

Emoji aren't drawn: none of the screen's fonts have them, so one shows as an empty box. Use `icon` instead.

### What happens

- **One at a time.** A new notification, of either kind, takes the place of the one up. The one it
  replaced reports `replaced`.
- **Sound.** The chime plays as it comes up, then the words if `speak` is `"on"`, joined as one
  sentence: title `Doorbell` and message `Someone is here` are said as "Doorbell. Someone is here".
  A chime that comes while the device is answering or announcing waits for it to finish (up to ten
  seconds, then it is left out) rather than cutting it off. A camera's sound does not hold it back:
  the chime plays over it at once.
- **Night and quiet hours.** No chime, no words and no camera sound, whatever `chime`, `speak` and
  `sound` say. At night a notification does not replace the night screen (on a Show, dark or the red clock; on a
  Spot, dark or dimmed to its night level): it
  waits, and shows after the first touch on the screen. That touch does nothing else. By day it lights
  a dark screen, once per notification; the screen can still be turned off again by an empty room or
  from Home Assistant while the notification is up.
- **What it sits under and over.** A ringing alarm or timer, a call, a reminder, the setup page's
  question and the PIN pad are shown over it. It is shown over the dashboard and the deck, and puts
  the Spot's menu away.
- **On a Spot** it takes the whole face: icon, title, message, and "tap to dismiss". A camera view
  (`home_show_camera`) is shown over a text card nobody has read; the card shows when the view ends. A
  picture notification replaces the card instead.

### The event

Each notification fires an `esphome.techo5_notification` event on Home Assistant's bus: once as it
comes up, and once more as it goes.

| Field | |
|---|---|
| `event` | `shown` as it comes up; then `dismissed` (somebody tapped it, or its picture's camera page was closed), `expired` (its time ran out) or `replaced` (a newer notification, or a camera put up over its picture, took its place) |
| `id` | The notification's own, unique on its device: the numbers count up from a random start each time the device starts, so one from before a restart is very unlikely to come again. Pair it with `device` |
| `tag` | The `tag` the call passed, `""` for none |
| `kind` | `text` for `notify`, `picture` for `notify_picture` |
| `title` | The title as shown (clipped), `""` for none |
| `message` | The message as shown (clipped), `""` for none |
| `device` | The device's own name, as set on the device (`Kitchen`), not the node in the action's name (`kitchen`). A name given to the device in Home Assistant's own settings is not seen by the device, so the event still carries the device's own |

### Examples

```yaml
# The washer is done: a card with an icon, the usual chime, for 10 minutes.
action: esphome.kitchen_notify
data:
  message: "The washing machine is done"
  title: "Washer"
  icon: "mdi:washing-machine"
  chime: ""
  speak: ""
  tag: "washer"
  seconds: 600
```

```yaml
# Said aloud as well as shown: "Garage. The garage door has been open for 10 minutes".
action: esphome.kitchen_notify
data:
  message: "The garage door has been open for 10 minutes"
  title: "Garage"
  icon: "mdi:garage-open"
  chime: ""
  speak: "on"
  tag: "garage"
  seconds: 300
```

```yaml
# A quiet note: no chime, no words, no title, up for an hour.
action: esphome.office_notify
data:
  message: "Trash goes out tonight"
  title: ""
  icon: "mdi:trash-can"
  chime: "off"
  speak: ""
  tag: "trash"
  seconds: 3600
```

### Notifying every device

There is no "all devices" action. Each device shows what it is sent, so a script that calls every
device's action does it, and an automation calls the script once. (To say words on every other
device at once, see [Announce to the house](#announce-to-the-house); a Dot chimes there rather than
speaking the text.)

```yaml
script:
  notify_techo5s:
    fields:
      message: {}
      title: {}
      icon: {}
    sequence:
      - parallel:
          - action: esphome.kitchen_notify
            data: {message: "{{ message }}", title: "{{ title | default('') }}", icon: "{{ icon | default('') }}", chime: "", speak: "", tag: "", seconds: 0}
          - action: esphome.office_notify
            data: {message: "{{ message }}", title: "{{ title | default('') }}", icon: "{{ icon | default('') }}", chime: "", speak: "", tag: "", seconds: 0}
```

### Following up

```yaml
# Somebody saw the washer card and tapped it away: nothing more to do. Nobody did in its 10 minutes:
# tell a phone. (A card with no seconds ends as expired after 12 hours at most.) The tag and the
# device pick out this card, so a notification on another device does not match.
alias: Washer card missed
triggers:
  - trigger: event
    event_type: esphome.techo5_notification
    event_data:
      event: expired
      tag: washer
      device: Kitchen
actions:
  - action: notify.mobile_app_phone
    data:
      message: "The washer finished and nobody saw it ({{ trigger.event.data.device }})"
```

## Show a picture notification

In YAML, refer to this action as `esphome.<node>_notify_picture`.

Puts a picture up full screen on this device, with a notification's words over it: a camera's live
view for a doorbell, a snapshot an automation saved, or any picture Home Assistant or another web
server serves. It uses the camera page, so a tap anywhere closes it, and it stays until then or until
its time runs out. It chimes, speaks, replaces and reports exactly as `notify` does (see
[Show a notification](#show-a-notification)), with `kind: picture` in its event. It takes `chime`, `speak`, `sound`, `tag` and `seconds` as set out below.

> **Good to know**
>
> **Set up `home_assistant` first.** A camera, an image entity and a Home Assistant path are fetched
> by the device itself, from Home Assistant's REST API, with the URL and long-lived access token that
> [Connect the device to Home Assistant](#connect-the-device-to-home-assistant)
> (`esphome.<node>_home_assistant`) stores. The ESPHome connection Home Assistant uses to reach the
> device carries no token the other way. Until that action has been called on the device, the
> notification still comes up, with its words, but with `hass: no access configured` in place of the
> picture. Only an `http(s)://` address on another server works without it.
>
> Home Assistant requires every argument on every call. To leave one out, pass `""` (or `0` for
> `seconds`).
>
> A camera's own sound is decided by `sound`, as on `home_show_camera_sound`: it plays over whatever
> the device is playing, the Mute/Unmute control on the page silences it, and words said with
> `speak: "on"` take the speaker for as long as they last and hand it back. A camera already up with
> its sound keeps it.
>
> A still picture does not take the page from a camera somebody is watching (one put up with
> `home_show_camera`, or from the screen): it waits for that view to end and comes up then, if its
> time has not run out meanwhile. A camera picture of the camera already up puts its words over that
> view without cutting its time short, and is not held back at night, since the camera was up anyway.
>
> `speak: "on"` needs **Allow the device to perform Home Assistant actions**, as for `notify`.
>
> A Dot has no screen. It checks the picture the same way, chimes, speaks if asked, fires the events,
> and shows nothing.

### Arguments

| Argument | Type | Accepted values | Empty / `0` means |
|---|---|---|---|
| `message` | string | Any text, up to 240 bytes; one line under the title on a Show, clipped to the screen's width, and up to two lines on a Spot. New lines and tabs become spaces. | No message line. |
| `title` | string | Any text, up to 40 bytes. Shown small, in capitals, above the message. On a Spot it replaces the camera's name at the top. | No title. A camera or image entity is then named on the page as Home Assistant names it (its friendly name). |
| `picture` | string | One of the four kinds below. Anything else fails the call with *not a camera, an image entity, a path on Home Assistant or an http(s) address*, and nothing is shown. | Not allowed. |
| `chime` | string | `"on"`, `true`, `yes` or `1` to chime; `"off"`, `false`, `no` or `0` to come up without one. Any letter case; any other text chimes. | The usual chime. |
| `speak` | string | `"on"`, `true`, `yes` or `1` to have the title and message said aloud after the chime; `"off"`, `false`, `no` or `0` to keep them to the screen. Any letter case; any other text leaves them unsaid. | Not said. A picture with no words never says anything. |
| `sound` | string | A camera's own audio, as `home_show_camera_sound` takes it: `"on"` (or `true`, `yes`, `1`) to play it, `"off"` (or `false`, `no`, `0`) to keep it silent. Any other text uses the device's setting. Only a camera Home Assistant can stream has any; a still and a Reolink camera ignore it. Never at night or in quiet hours. | The device's own **Camera sound** setting, off on a new device. |
| `tag` | string | Any text you choose, carried back as `tag` on this notification's events. | No tag (`""` in the events). |
| `seconds` | integer | How long it stays up, in seconds, counted from when the picture arrives (a camera that is slow to start loses none of it). At most a day. | With `0` or a negative number: a live camera stays 30 seconds, as `home_show_camera` treats `0`; a still stays until somebody taps it, with "tap to close" and no countdown, but no longer than 12 hours, then `expired`; on a Dot, until its chime and words are done. |

With neither a `title` nor a `message`, it is **the picture alone**: no name, clock or caption over it,
only "tap to close" at its foot.

The kinds of `picture`:

| `picture` | Example | Shown as | Fetched from | Needs `home_assistant` |
|---|---|---|---|---|
| A camera entity | `camera.front_door` | Live: frames one after another while it is up | `/api/camera_proxy/<entity>` on Home Assistant | Yes |
| An image entity | `image.doorbell_snapshot` | A still, fetched once | `/api/image_proxy/<entity>` on Home Assistant | Yes |
| A path on Home Assistant | `/local/snapshots/door.jpg` | A still, fetched once | Home Assistant, with the token. `/local/` is Home Assistant's `config/www` folder | Yes |
| An `http://` or `https://` address | `http://192.168.1.10:8123/local/door.jpg`, `https://example.com/porch.jpg` | A still, fetched once | Home Assistant's own address (the one `home_assistant` stores) is fetched with the token, as a path. Any other host is fetched without it, under the video player's rule (the same one, in `lib/netguard`): anything that is not `http` or `https` is refused, and so are loopback, link-local, multicast and this device's own addresses, judged on the address actually connected to, after DNS and after every redirect. A camera or NAS that needs a login must carry it in the address | Only for Home Assistant's own address |

A still that is up already, by the same `picture`, stays on the screen when the next notification
comes over it while the picture is fetched again, so a run of notifications over one picture doesn't
flicker, and a picture that changes behind one address (an image entity's newest snapshot, or a file
saved over the last) still shows its newest version.

A still is JPEG or PNG, at most 8 MB and about 12 megapixels, and is scaled to fit the screen. A picture
that cannot be fetched (not found, not a picture, too big, no `home_assistant`) still comes up with its
words, and the reason where the picture would be. A login in an address
(`user:pass@`), its query and its fragment are never shown on the screen or written to a log: they are
taken off first, even in an error.

### Examples

```yaml
# A camera: the doorbell, live, with words and said aloud, for a minute.
action: esphome.kitchen_notify_picture
data:
  message: "Someone is at the front door"
  title: "Doorbell"
  picture: camera.front_door
  chime: ""
  speak: "on"
  sound: ""
  tag: "doorbell"
  seconds: 60
```

```yaml
# The doorbell with its own sound: whoever is at the door is heard in the room, whatever the device's
# Camera sound setting says.
action: esphome.kitchen_notify_picture
data:
  message: "Someone is at the front door"
  title: "Doorbell"
  picture: camera.front_door
  chime: ""
  speak: ""
  sound: "on"
  tag: "doorbell"
  seconds: 60
```

```yaml
# The camera alone: no words, no chime, for the default 30 seconds (a live camera given 0).
action: esphome.kitchen_notify_picture
data:
  message: ""
  title: ""
  picture: camera.driveway
  chime: "off"
  speak: ""
  sound: ""
  tag: "driveway"
  seconds: 0
```

```yaml
# An image entity: a doorbell's last snapshot (many doorbell integrations provide one).
action: esphome.office_notify_picture
data:
  message: "A package was left at the door"
  title: "Delivery"
  picture: image.front_door_doorbell_snapshot
  chime: ""
  speak: ""
  sound: ""
  tag: "delivery"
  seconds: 120
```

```yaml
# A snapshot saved to Home Assistant's www folder, shown by its path. The automation saves it first
# (camera.snapshot writes config/www/snapshots/porch.jpg, which Home Assistant serves as
# /local/snapshots/porch.jpg).
- action: camera.snapshot
  target:
    entity_id: camera.porch
  data:
    filename: /config/www/snapshots/porch.jpg
- action: esphome.kitchen_notify_picture
  data:
    message: "Motion on the porch"
    title: "Porch"
    picture: /local/snapshots/porch.jpg
    chime: ""
    speak: ""
    sound: ""
    tag: "porch"
    seconds: 45
```

```yaml
# A picture on another server, fetched without Home Assistant's token. Anything the server needs to
# let the device in has to be in the address.
action: esphome.kitchen_notify_picture
data:
  message: "Tonight's radar"
  title: "Weather"
  picture: "https://example.com/radar/latest.png"
  chime: "off"
  speak: ""
  sound: ""
  tag: "radar"
  seconds: 300
```

A whole doorbell automation, which tells a phone when nobody answered:

```yaml
alias: Doorbell on the kitchen Show
triggers:
  - trigger: state
    entity_id: binary_sensor.front_door_doorbell
    to: "on"
actions:
  - action: esphome.kitchen_notify_picture
    data:
      message: "Someone is at the front door"
      title: "Doorbell"
      picture: camera.front_door
      chime: ""
      speak: "on"
      sound: ""
      tag: "doorbell"
      seconds: 60
  # Wait for it to go: tapped away (dismissed), not (expired), or taken over by a newer notification
  # (replaced), which is somebody else's doing and not an unanswered door. Waiting for any event would
  # catch its "shown" first. The tag and the device pick out this notification, not another one's.
  - wait_for_trigger:
      - trigger: event
        event_type: esphome.techo5_notification
        event_data: {tag: doorbell, device: Kitchen, event: dismissed}
      - trigger: event
        event_type: esphome.techo5_notification
        event_data: {tag: doorbell, device: Kitchen, event: expired}
      - trigger: event
        event_type: esphome.techo5_notification
        event_data: {tag: doorbell, device: Kitchen, event: replaced}
    timeout: "00:02:00"
  - if:
      - condition: template
        # Only a door nobody answered: tapped away or replaced is not.
        value_template: "{{ wait.trigger is none or wait.trigger.event.data.event == 'expired' }}"
    then:
      - action: notify.mobile_app_phone
        data:
          message: "Nobody answered the door"
```

## Choose the calendars shown

In YAML, refer to this action as `esphome.<node>_calendar_sources`.

Chooses which of Home Assistant's calendars this device shows, for its calendar page and event
pop-ups. Each device keeps its own: one on a desk can show its owner's calendar, one in a kitchen the
family's. Screen devices only. The calendars come from whatever Home Assistant connects to - Google
Calendar, iCloud through CalDAV, an Outlook calendar through Microsoft 365 or a published link, its
own Local Calendar - so no calendar password is ever on a device. Needs `home_assistant` set up first.

### calendars (Required)

*string*

Calendar entities, separated by commas, in the order to show them. Empty shows no calendar.

```yaml
action: esphome.office_calendar_sources
data:
  calendars: calendar.family, calendar.work
```

## Show the calendar

In YAML, refer to this action as `esphome.<node>_calendar_show`.

Opens the calendar page on a Show, as a tap on the date under the clock does: on this month, or on
the view its **Opens on** setting names. The page
shows the month with each day's events on it; a tap on a day lists its events, and a tap on an event
opens its details. It closes after two minutes untouched, or with Done. At night it lights a screen
the night has put out, at the night light's level (or at half brightness with no night light), and
shows over the red night clock; the night comes back once the page closes. A screen somebody turned
off stays off: the action still works, and the page is there if the screen is turned on before it
closes. Fails on a device that shows no calendar (choose one with `calendar_sources`, or Settings,
General, Calendars). No arguments.

```yaml
action: esphome.office_calendar_show
```

## Show the forecast or the rain map

In YAML, refer to these actions as `esphome.<node>_weather_show` and `esphome.<node>_radar_show`.

`weather_show` opens the forecast page on a Show, as a tap on the weather on the home screen does;
`radar_show` opens the rain map, as the page's Radar button does. The button turns between the two,
a tap anywhere else puts the page away, and it closes on its own after 30 seconds. Either one takes
the place of an open weather alert page, and of the calendar. At night it lights a screen the night
has put out, at the night light's level (or at half brightness with no night light), and shows over
the red night clock; the night comes back once the page closes. A screen somebody turned off stays
off: the action still works, and the page is there if the screen is turned on before it closes. Fails
on a device set to no weather (choose a source with `home_weather`, the "Weather source" select, or
Settings, General, Weather). No arguments.

```yaml
action: esphome.office_weather_show
```

```yaml
# A storm warning: the rain map up on the kitchen Show.
action: esphome.kitchen_radar_show
```

## Choose the calendars that pop up

In YAML, refer to this action as `esphome.<node>_calendar_popup_sources`.

With **Event pop-ups** on, an event coming up shows over the screen: a timed event a while before it
starts (the **Pop up** select, a quarter hour by default), an all-day event once in the morning, as
the night hours end. At night and in quiet hours it makes no sound and does not light a dark screen.
Each pop-up fires an `esphome.techo5_calendar` event with `event: popup`, `summary`, `calendar`,
`start` and `device`. This action chooses which of the device's calendars pop up. A Show only.

### calendars (Required)

*string*

Calendar entities, separated by commas. Empty is every calendar the device shows.

```yaml
action: esphome.office_calendar_popup_sources
data:
  calendars: calendar.family
```

## Set the night hours

In YAML, refer to this action as `esphome.<node>_screen_night_hours`.

Sets when the night starts and ends on a Show or a Spot: the hours the screen goes dark, or down to
its night light or night clock, by itself. Any times, to the minute, and the night may cross midnight. The same can be set on
the screen (Settings, Display, Night hours, Custom) and with the **Night hours**, **Night starts**
and **Night ends** selects, which offer quarter hours.

### start (Required)

*string*

When the night starts, in 24-hour time: `19:00`.

### end (Required)

*string*

When it ends: `09:30`. Both empty turns the night off (on a Spot too: it does not go back to 10 PM to
7 AM).

```yaml
action: esphome.office_screen_night_hours
data:
  start: "19:00"
  end: "09:30"
```

### Turning the night on from an automation

A Show or a Spot also has a **Night mode** switch, on while it is night. Turn it on or off from an automation,
a "house to sleep" scene for example, to start or end the night now. With night hours set, the switch
holds until the hours next start or end the night, then the hours take over again. To leave the night
to Home Assistant entirely, set **Night hours** to **Controlled by Home Assistant**: the hours are then
ignored and it is night only while the switch is on.

```yaml
action: switch.turn_on
target:
  entity_id: switch.office_night_mode
```

## Point the device at a dashboard server

In YAML, refer to this action as `esphome.<node>_dashboard_server`.

Where the dashcast server is, for a streamed dashboard, and the key it asks for. The same can be set
on the device's setup page, which is easier for a long key. See [Dashboards](dashboards.md).

### address (Required)

The server's address and port, like `192.168.1.20:9555`.

### key (Required)

The key the server was started with (`DASHCAST_KEY`).

## Choose the dashboard shown

In YAML, refer to this action as `esphome.<node>_dashboard_path`.

Which dashboard the screen shows, as its path in Home Assistant's address bar. The device's
**Dashboard to show** list sets the same thing, and is usually easier. See [Dashboards](dashboards.md).

### path (Can be empty)

`lovelace/0`, `dashboard-kitchen/lights`, `energy`, … Empty is the Rooms dashboard when drawn, and
the default dashboard when streamed.

## Show or hide the dashboard

In YAML, refer to these actions as `esphome.<node>_dashboard_show` and `esphome.<node>_dashboard_hide`.

`dashboard_show` puts the dashboard up, the same as swiping it in (on a Spot, the Dashboard item in
the ring menu). It stays up until `dashboard_hide`, a swipe or "go home" takes it down. It doesn't
time out the way one opened by hand does (after **Dashboard returns to the clock after**, 10 minutes
unless changed). If the settings, a camera or a call has the screen, the dashboard comes up once
they're done. The **Dashboard** setting must not be **Off**.

`dashboard_hide` goes back to the clock. With **Dashboard when idle** on, the clock stays for 2
minutes, then the dashboard comes back, the same as swiping it away.

For example, show a Now Playing dashboard while a speaker plays:

```yaml
triggers:
  - trigger: state
    entity_id: media_player.living_room_sonos
    to: playing
    id: playing
  - trigger: state
    entity_id: media_player.living_room_sonos
    from: playing
    for: "00:05:00"
    id: stopped
actions:
  - if:
      - condition: trigger
        id: playing
    then:
      - action: esphome.office_dashboard_show
    else:
      - action: esphome.office_dashboard_hide
```

## Press a deck button

In YAML, refer to this action as `esphome.<node>_deck_press`.

Does what a TECHO5 Deck button does, as if it were pressed on the screen: an automation can switch
an OBS scene or start the stream with it. Show only. See [TECHO5 Deck](deck.md).

### page (Required)

The page, counting from 1.

### button (Required)

The button on that page, counting from 1, row by row: on a 4-across deck the second row starts at 5.

## Play a video

In YAML, refer to this action as `esphome.<node>_play_video`.

Plays a video full screen, with its sound, in place of any video already playing. Show and Spot,
and only while the **Video** switch is on. See [Video](video.md).

### url (Required)

*string*

An `http://` or `https://` address: MP4, MKV, MPEG-TS or HLS, best as H.264 at 720p or less. Other
kinds of address (files, `rtsp://`) are refused.

### title (Required)

*string*

What the screen and the **Video title** sensor call it. `""` for none: they show the address's host
instead.

```yaml
action: esphome.office_play_video
data:
  url: "http://192.168.1.20:8096/Videos/clip.mp4"
  title: "Front door"
```

## Stop, pause or carry on a video

In YAML, refer to these actions as `esphome.<node>_stop_video`, `esphome.<node>_pause_video` and
`esphome.<node>_resume_video`. They take nothing, and do nothing when no video is playing. Stop also
takes down a DLNA video's question on the screen.

```yaml
action: esphome.office_stop_video
data: {}
```

## Choose the weather shown on the idle screen

In YAML, refer to this action as `esphome.<node>_home_weather`.

Sets which weather forecast the device's clock/idle screen shows. Screen devices only. Needs
`home_assistant` set up first, or no forecast will show.

### entity (Can be empty)

*string*

A `weather.*` entity to show, `default` (or left blank) for Home Assistant's own forecast
(`weather.forecast_home`, the one set up automatically for the home's location), or `none` to show
no weather at all. Matching is case-insensitive.

```yaml
action: esphome.office_home_weather
data:
  entity: weather.forecast_home
```

## Show chips along the foot of the clock

In YAML, refer to this action as `esphome.<node>_home_glance`.

Sets the glance strip: Home Assistant entities shown as chips (a pill with the entity's icon and a
short line) along the foot of the clock page, each only while it has something to say. The Echo Show
only; the Spot and the Dot don't have it. The list persists and the device follows the entities itself, so a chip comes and goes with its
entity's state and nothing has to be sent again. A value that leaves the chips as they were
does not redraw the screen.

What each entity shows:

- **Off, idle, closed, locked, empty, zero (`0`, `0.0`), `unknown` or `unavailable`**: nothing.
- **A switch-like entity** (`binary_sensor`, `input_boolean`, `switch`, `light`, `lock`, `cover`, …)
  that is on or open: its name.
- **A number**: its name, the value and the unit ("Kitchen 21.5°C").
- **Anything else**: the state itself, which is how a template sensor made for the strip reads
  ("Washer done", "6:00 PM: water the plants"). A state name reads as words: `not_home` shows as
  "Not home".

The icon is the entity's own, or one for its domain. Chips that don't fit are left for when there is
room, in the order given. A running timer or the music strip takes the foot of the page instead.

> **Good to know**
>
> A number's chip shows its value, so the screen redraws every time the value changes. A power or
> energy sensor that reports every few seconds redraws the clock page every few seconds. For those,
> make a template sensor that says only what matters ("Washer running", or nothing) and put that in
> the list instead.

### entities (Required)

*string*

The entities, comma separated, in the order to show them: up to 8, and each once (a repeat and any
past the eighth are left out). Empty takes the strip away.

```yaml
action: esphome.office_home_glance
data:
  entities: input_boolean.guest_mode, sensor.washer_status, binary_sensor.front_door
```

## Wire up the radio page

In YAML, refer to this action as `esphome.<node>_home_radio`.

Configures the device's Radio page: which stations it lists, what plays them, and what shows as
"now playing". Screen devices only; the change takes effect the next time the device reconnects.

Sample scripts, for stream addresses, Radio Browser and TuneIn stations, are under
[Radio](install.md#radio) in the install guide.

> **Good to know**
>
> - Favorites (`stations`) play through the script in `service`, which the device calls over its
>   ESPHome connection, not through `home_assistant`. That needs **Allow the device to perform Home
>   Assistant actions** turned on in the device's ESPHome entry (Configure). Without it, picking a
>   favorite does nothing, and nothing on the device says why.
> - The Radio Browser stations near your home, shown alongside your favorites, come through
>   `home_assistant`. Without it, the device asks radio-browser.info itself, for stations near the
>   place set on its setup page, and plays them itself.
> - Call this once. The device keeps the wiring, and follows the entities from then on; call it again
>   only to change something. Every argument goes in the call, as `""` for the ones you leave out.
> - The device only reads `now`. Your play script, or an automation, has to set it to the station's
>   name each time one starts. When the device cannot name a stream itself, which is most of the
>   time, it is the name shown while a station plays and the name the player's star saves.
> - The star on the player calls a script named `script.radio_favorite_add_station`, with one field,
>   `station`, through `home_assistant`. There is an example under [Radio](install.md#radio) in the
>   install guide.

### stations (Can be empty)

*string*

A comma-separated list of `input_select` entities, each one a station or preset to offer on the
Radio page. Up to 20.

### now (Can be empty)

*string*

An entity whose state names whatever is currently playing, shown on the Radio page and the idle
screen while it's live.

### service (Can be empty)

*string*

The script or action that actually plays a station when one is picked on the device.

### field (Can be empty)

*string*

The name of the argument `service` expects the chosen station in. Defaults to `station` if left
blank.

### speaker_field (Can be empty)

*string*

The name of the argument `service` expects this device's media player in. Defaults to `speaker` if
left blank.

### speaker (Can be empty)

*string*

This device's own `media_player.*` entity, passed to `service` in `speaker_field` so the script
knows which speaker to target.

```yaml
action: esphome.office_home_radio
data:
  stations: input_select.office_radio_stations
  now: sensor.office_radio_now_playing
  service: script.play_radio_station
  field: station
  speaker_field: speaker
  speaker: media_player.office
```

## Play rain sounds by voice

The device has no sounds of its own to fall asleep to. What it does is leave "play the sounds of
rain" alone, so the sentence isn't taken as a weather question, and name a sound file on the
now-playing page. The sound itself comes from Home Assistant. This automation plays a free online
rain stream on the TECHO5 device that heard the sentence, so there is no file to host. Add it in
Settings > Automations & scenes > Create automation > Edit in YAML.

```yaml
alias: TECHO5 - rain sounds by voice
triggers:
  - trigger: conversation
    command:
      - "play [the] sound[s] of rain"
      - "play rain [sounds]"
actions:
  - variables:
      speaker: >-
        {{ device_entities(trigger.device_id) | select('match', 'media_player\.') | first
           if trigger.device_id is not none else '' }}
  - if: "{{ not speaker }}"
    then:
      - set_conversation_response: "Say that to the speaker you want the rain on."
      - stop: No speaker heard it
  - action: media_player.play_media
    target:
      entity_id: "{{ speaker }}"
    data:
      media_content_type: music
      media_content_id: https://maggie.torontocast.com:2020/stream/natureradiorain
  - set_conversation_response: "Playing rain."
mode: single
```

"Stop" ends it like any other music. To play a recording of your own instead, put the file in
Home Assistant's `www` folder (inside its config folder) and use
`http://<home-assistant-address>:8123/local/<file>.mp3` as `media_content_id`; the now-playing
page shows the file's name. A long recording works best, since a file stops when it ends.

## Center the rain map and weather alerts somewhere else

In YAML, refer to this action as `esphome.<node>_home_location`.

The rain map and the weather alerts are centered on Home Assistant's home. A device that lives
somewhere else, with family in another town, can be given a zone of its own instead: create the zone in
Home Assistant (**Settings → Areas, labels & zones → Zones**), then give its entity here. Screen devices
only. Set its weather with [`home_weather`](#choose-the-weather-shown-on-the-idle-screen) as well, for
a forecast for the same place.

### zone (Required)

*string*

A `zone.*` entity, or empty (or `home`) for Home Assistant's home again. A zone Home Assistant doesn't
know leaves the rain map and alerts saying the location isn't known, rather than showing home's.

```yaml
action: esphome.office_home_location
data:
  zone: zone.cabin
```

## Set the slideshow's photo source

In YAML, refer to this action as `esphome.<node>_home_slideshow`.

Sets the Home Assistant media source the idle-screen slideshow shows photos from — the same setting
as the screen's own folder picker. Screen devices only. Whether the photos show behind the ordinary
clock (Background mode) or take over the whole screen after a wait (Screensaver mode), and options
like shuffle, subfolders and time per photo, are set separately as entities
(`select.<node>_slideshow`, `switch.<node>_slideshow_shuffle`, `number.<node>_slideshow_interval`,
and so on), not by this action. Needs `home_assistant` set up first, or no photos will show.

### source (Required)

*string*

A Home Assistant media source ID — the same ID browsing a media source in Home Assistant returns
for a folder (an Immich album, a network share, or anything else `media_source` can browse), e.g.
`media-source://media_source/local/Photos`. An empty value clears the source, so nothing is shown
whatever mode is selected.

```yaml
action: esphome.office_home_slideshow
data:
  source: "media-source://media_source/local/Photos"
```

The source that is already showing, given again, reads it again: the device gathers its photos afresh
(subfolders too, unless **Include subfolders** is off) and shows the next photo from the new list
within half a minute, without clearing the source first. The photo up stays on the screen until the
next one is ready, so the screen does not go dark in between. This works the same for a Home
Assistant folder, an Immich album or library, and the device's own photos. Use it after something
outside fills or changes the folder; on its own the device reads the list again every hour.
A slideshow in order carries on from the photo up when it is still in the folder. Given again while
the photo the last re-read brought up is still showing, the folder is read again but that photo
keeps its time, so a script that asks after each photo it copies in changes the photo once, not each
time.

An automation that reads the folder again once a script has put new photos in it and said so over
MQTT:

```yaml
triggers:
  - trigger: mqtt
    topic: photos/ready
actions:
  - action: esphome.office_home_slideshow
    data:
      source: "media-source://media_source/local/Photos"
```

## List saved voice recordings

In YAML, refer to this action as `esphome.<node>_recordings`.

Returns the IDs of the voice-turn recordings currently kept on the device, most recent first, so
Home Assistant can offer them for playback. The device keeps 0–10 of them per assistant (set with
the "keep recordings" number entity; off by default). This action answers with data — call it with
a `response_variable` rather than as a plain fire-and-forget action.

Takes no parameters.

```yaml
action: esphome.office_recordings
response_variable: recent
```

## Fetch a saved voice recording

In YAML, refer to this action as `esphome.<node>_turn_audio`.

Fetches one saved recording's audio as base64-encoded WAV, by an ID from `recordings`. A clip larger
than about 32 KiB of base64 comes back split into pages — check the response for how many pages
there are and call again for each one. This action answers with data; call it with a
`response_variable`.

### id (Required)

*string*

One of the recording IDs `recordings` returned.

### page (Can be 0)

*integer*

Which page of the audio to fetch, starting at 0. Defaults to 0.

```yaml
action: esphome.office_turn_audio
data:
  id: "{{ recent.ids[0] }}"
  page: 0
response_variable: clip
```

## List saved wake word clips

In YAML, refer to this action as `esphome.<node>_wake_clips`.

Returns the wake word clips kept on the device, newest first, with what the detector made of each.
Clips are only kept while the device's **Keep wake clips** switch is on (off by default; see
[Wake word clips](setup.md#wake-word-clips)); while it is off, this action and `wake_clip_audio`
answer with an error. Each entry has:

- `id`: what to pass to `wake_clip_audio`.
- `kind`: `wake` (a wake not taken as a mistake within 30 seconds), `mistake`, `noise` or `nearmiss`.
- `at`: when, in UTC.
- `slot`: which assistant's wake word, 1 or 2.
- `model`: the wake word model's ID.
- `score`: the highest score the word reached.
- `cutoff`: the threshold it was judged against.

The answer lists 50 clips at a time; `pages` says how many pages there are and `total` how many clips.
This action answers with data; call it with a `response_variable`.

### kind (Required)

*string*

Only this kind of clip: `wake`, `mistake`, `noise` or `nearmiss` (`near miss`, as **Wake clips by
kind** writes it, works too). An empty string lists every kind.

### page (Required)

*integer*

Which page of the list, starting at 0.

```yaml
action: esphome.office_wake_clips
data:
  kind: "mistake"
  page: 0
response_variable: clips
```

## Fetch a saved wake word clip

In YAML, refer to this action as `esphome.<node>_wake_clip_audio`.

Fetches one wake word clip as base64-encoded WAV (16 kHz mono 16-bit), by an ID from `wake_clips`.
The answer has the same shape as `turn_audio`'s: a clip is about 128 KB, so it comes back in pages,
and the response says how many there are. This action answers with data; call it with a
`response_variable`.

### id (Required)

*string*

One of the clip IDs `wake_clips` returned.

### page (Required)

*integer*

Which page of the audio to fetch, starting at 0.

```yaml
action: esphome.office_wake_clip_audio
data:
  id: "{{ clips.clips[0].id }}"
  page: 0
response_variable: audio
```

## Set the camera stream's login

In YAML, refer to this action as `esphome.<node>_camera_credentials`.

Sets the user name and password a recorder uses for [this device's camera
stream](setup.md#recording-this-devices-camera) (RTSP and ONVIF), by the same rules as the setup page.
A new login lets go of whoever is watching with the old one.

> **Good to know**
>
> It works only once **Camera login from Home Assistant** (a switch of the device's, under its
> configuration) is on; it is off by default. Like `ssh_keys`, it is also refused unless Home
> Assistant's *ESPHome* link to the device has a real encryption key set, so the password never
> crosses the network in the clear.

### username (Can be empty)

*string*

One line, with no quotes and no colon. Empty is `camera`.

### password (Can be empty)

*string*

12 characters or more, one line, with no quotes. Empty keeps the password set now.

```yaml
action: esphome.office_camera_credentials
data:
  username: frigate
  password: a-long-password-here
```

## Set the SSH authorized keys

In YAML, refer to this action as `esphome.<node>_ssh_keys`.

Replaces the device's SSH `authorized_keys`, one public key per line. This is the only way keys get
onto the device — nothing is baked into the image, and the on-screen SSH switch can only turn the
server on or off, not add anyone.

> **Good to know**
>
> This needs an API encryption key, not `home_assistant`: it's refused unless Home Assistant's
> *ESPHome* link to the device already has a real encryption key set, since otherwise the keys would
> cross the network in the clear.

### keys (Required)

*string*

Newline-separated public keys, each a full `authorized_keys` line (type, key, optional comment) —
`ssh-ed25519`, `ssh-rsa`, `ecdsa-sha2-nistp256`, `ecdsa-sha2-nistp384`, `ecdsa-sha2-nistp521`,
`sk-ssh-ed25519@openssh.com`, or `sk-ecdsa-sha2-nistp256@openssh.com`. A private key, or any line
that isn't recognized as one of these types, is rejected and none of the keys are changed. An empty
value removes every key and turns SSH off if it's running.

```yaml
action: esphome.office_ssh_keys
data:
  keys: |
    ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample you@your-pc
```

## Join a tailnet

In YAML, refer to this action as `esphome.<node>_tailscale_login`.

Joins the device to your Tailscale network with an auth key, and turns its **Tailscale** switch on.
The key is handed to Tailscale once and not kept: after that the device rejoins by itself. It waits
up to 30 seconds to see the device connect, so a key that's used up or expired is an error here
rather than a device that quietly never joins. Echo Shows, the Echo Spot and the Skylight only. See
[Tailscale: reaching a device from away](tailscale.md).

> **Good to know**
>
> This needs an API encryption key: it's refused unless Home Assistant's *ESPHome* link to the device
> already has a real encryption key set, since otherwise the key would cross the network in the clear.

### auth_key (Required)

*string*

An auth key from the Tailscale admin console (`tskey-auth-…`), pre-approved and tagged `tag:techo5`.

```yaml
action: esphome.office_tailscale_login
data:
  auth_key: tskey-auth-example
```

## Set the settings lock's PIN

In YAML, refer to this action as `esphome.<node>_settings_lock_pin`.

Sets the PIN the device asks for before its settings change (Show and Spot). Everything else on the
device works without it: the clock, music, the voice assistant, calls, and the everyday settings
(alarms and timers, the volume, the brightness; see [the settings lock](setup.md#8-settings-lock)).
The **Settings lock** switch shows whether a PIN is set; turning it off removes the PIN, which is
the way back in if it is forgotten. The PIN can also be set on the device (Settings → Privacy &
Security → Settings lock) and on the setup page.

> **Good to know**
>
> Like `ssh_keys`, this is refused unless Home Assistant's *ESPHome* link to the device already has a
> real encryption key set, so the PIN never crosses the network in the clear.

### pin (Required)

*string*

4 to 8 digits. Empty removes the PIN and the lock.

```yaml
action: esphome.office_settings_lock_pin
data:
  pin: "2468"
```

## Sign a device in to a SIP account

In YAML, refer to this action as `esphome.<node>_phone_account`.

Signs the device in to a SIP provider so it can place and receive calls. The login is kept on the
device in its own owner-only file — never in the settings Home Assistant or diagnostics can read.

> **Good to know**
>
> This needs an API encryption key, not `home_assistant`: it's refused unless Home Assistant's
> *ESPHome* link to the device already has a real encryption key set, since otherwise the password
> would cross the network in the clear.

### server (Required)

*string*

The provider's SIP server (e.g. a VoIP.ms POP such as `chicago1.voip.ms`).

### username (Required)

*string*

The SIP account's username. An empty value signs the device out and removes the stored login
instead of signing in.

### password (Required)

*string*

The SIP account's password.

```yaml
action: esphome.office_phone_account
data:
  server: chicago1.voip.ms
  username: "100000_office"
  password: !secret techo5_office_sip_password
```

## Place a call

In YAML, refer to this action as `esphome.<node>_phone_call`.

Places a call from the device. Does nothing on its own — this is how an automation or voice command
dials out; the device never calls anyone by itself.

### number (Required)

*string*

A phone number or another SIP account's extension. Spaces, dashes and a leading `+` are dropped
before dialing.

```yaml
action: esphome.office_phone_call
data:
  number: "15551234567"
```

## Place a call to someone by name

In YAML, refer to this action as `esphome.<node>_phone_call_named`.

The same as [Place a call](#place-a-call), with the name of whom it calls: the screen and the **Phone
call** sensor show the name with the number ("Alex (15551234567)"), as they show a caller whose name the
network passes on. Without a name, a call shows only the number it dials. The `esphome.techo5_phone`
events carry the name the same way. For an automation or an assistant that knows whom it is calling
from somewhere else (a contact list of its own, a FRITZ!Box's phone book), so the device's own phone
book isn't needed.

It is an action of its own rather than a second argument to `phone_call`, because Home Assistant
requires every argument of a device's action: a new one would have broken every automation that calls
`phone_call` with a number alone.

### number (Required)

*string*

As for `phone_call`.

### name (Required)

*string*

Whom the call is to, up to 40 characters. `""` shows the number alone, as `phone_call` does.

```yaml
action: esphome.office_phone_call_named
data:
  number: "15551234567"
  name: "Alex"
```

## Call another device in the house

In YAML, refer to this action as `esphome.<node>_intercom_call`.

Calls another TECHO5 device in the house over the intercom: it rings there with this device's name,
and once someone answers, the two talk. No phone account, Home Assistant or internet is involved in
the call itself. Answering and hanging up are the same as for a phone call (below), and the call
fires the same `esphome.techo5_phone` events, with `kind: intercom`.

> **Good to know**
>
> Both devices need the same house word, set on each device's setup page (the one announcing uses).
> A device with no house word takes no calls, and one with a different word refuses them.
>
> The device called decides how it takes the call. With **Intercom do not disturb** on, it turns the
> call away (`reason: do not disturb`). With **Allow Drop In** on, it chimes and connects by itself,
> with no one answering, and its screen says **Drop In**. Both are off by default, and are switches
> in Home Assistant and settings on a screen (Sound & Voice, and Privacy & Security). After a call is
> declined, the same device cannot ring it again for 30 seconds.

### device (Required)

*string*

The name of the device to call, as it shows in Home Assistant ("Kitchen"). Capitals do not matter.
The device has to be on and on the same network; the call fails at once if it is not found.

```yaml
action: esphome.office_intercom_call
data:
  device: Kitchen
```

## Set the phone book

In YAML, refer to this action as `esphome.<node>_phone_contacts`.

Replaces the device's phone book, up to 12 contacts: the screen's Call list, and the names "call Mom"
said to the device calls ([phone.md](phone.md#calling-a-contact-by-voice)). The setup page sets the same
list. The numbers are kept on the device in their own owner-only file, not in Home Assistant.

### contacts (Required)

*string*

A comma-, newline-, or semicolon-separated list of `Name=number` pairs. Other names for a contact,
which voice can use and the screen does not show, go after the first, each after a `|`:

```text
Mom|Mother=15551234567, Sam=15557654321, Kitchen=106
```

A name can be used only once in the whole list, whatever its capitals; a list with one used twice is
refused. An empty value clears the list.

```yaml
action: esphome.office_phone_contacts
data:
  contacts: "Mom|Mother=15551234567, Kitchen=106"
```

## Answer a call

In YAML, refer to this action as `esphome.<node>_phone_answer`.

Answers the device's currently ringing call, phone or intercom, the same as pressing its action button, tapping its
screen, or using the **Answer call** button Home Assistant shows while it rings.

Takes no parameters.

```yaml
action: esphome.office_phone_answer
```

## Hang up

In YAML, refer to this action as `esphome.<node>_phone_hangup`.

Ends the device's call, whether it's ringing (incoming or outgoing) or already up — the same as
pressing its action button again, the **Hang up** / **Decline** button, or a swipe or tap on its
screen.

Takes no parameters.

```yaml
action: esphome.office_phone_hangup
```

## Announce to the house

In YAML, refer to this action as `esphome.<node>_announce_house`.

Says something on every other TECHO5 device in the house. What it does depends on whether you
give it words:

- **With `text`**, it speaks those words. Devices with a screen show them; a Dot chimes and stays
  quiet, since it has no way to read them aloud.
- **With no `text`**, it opens the device's microphone, plays a tone, and sends what is said as
  audio. That is the announcement people mean: your own voice in every room, recorded on one
  device and played on the others.

The recording ends when you stop talking, or at fifteen seconds, or when somebody taps the screen
of the device that is listening. One that nobody spoke into is dropped rather than sent as a chime
and silence.

> **Good to know**
>
> Announcements go device to device over the local network, not through Home Assistant, so this
> action is a way to start one rather than a route the audio takes. They keep working with Home
> Assistant switched off; this action is simply not available then.
>
> Every device needs the same **house word** set on its setup page, capitals included: the word is
> case-sensitive. A device with no word set neither sends announcements nor takes them, and this
> action will say so in the log rather than failing. A device that refuses announcements for having
> a different word is named on the sender's setup page, next to the house word.
>
> Quiet hours are respected: inside them an announcement is shown and not sounded. Alarms, timers
> and calls are not announcements and are not affected.

### text (Can be empty)

*string*

What to say. Pass an empty string to record instead.

```yaml
action: esphome.office_announce_house
data:
  text: "dinner is ready"
```

```yaml
# No text: opens the microphone and sends what is said.
action: esphome.office_announce_house
data:
  text: ""
```

## Copy settings to other devices

In YAML, refer to this action as `esphome.<node>_copy_settings`.

Copies this device's settings to other devices, once, with no link: the way to make a change on one
device and send it to the others. Settings sharing has to be on, on this device and on each device
that is to take them ([Sharing settings](setup.md#sharing-settings-with-the-other-devices)). A device
that keeps a setting its own, has sharing off or has no such setting is left alone, and the log says
which. A copy counts as the latest change where it lands, and is passed on to the setting's links
there.

### settings (Required)

*string*

The settings, by their entities' object IDs (`screen_theme` for `select.office_screen_theme`),
separated by commas, or `all` for every setting that can be shared.

### devices (Required)

*string*

The devices, by name, separated by commas, or `all` for every other device on the network now.

```yaml
action: esphome.office_copy_settings
data:
  settings: screen_theme, screen_clock_style, night_volume
  devices: Kitchen, Bedroom
```
