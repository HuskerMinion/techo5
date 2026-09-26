# Home Assistant actions

TECHO5 exposes a handful of [ESPHome actions](https://esphome.io/components/api.html#actions) once
a device connects. In Home Assistant they show up under the `esphome` domain, named
`esphome.<node>_<action>` — a device named `office` exposes `esphome.office_alarm_set`, and so on.
The `<node>` prefix throughout this page is whatever name you gave the device when it was added;
substitute your own. Call any of them from **Developer Tools → Actions**, a script, or an
automation.

## Connect the device to Home Assistant

In YAML, refer to this action as `esphome.<node>_home_assistant`.

Stores the URL and a long-lived access token the device uses to call Home Assistant's REST API
directly, separately from the ESPHome connection Home Assistant uses to talk to it. This is what
lets the device fetch things on its own — a weather forecast, a camera snapshot, a media library
listing — rather than only reporting sensors and taking commands. It's normally set once, by
whatever paired the device, and isn't something you call from an automation afterwards.

> **Good to know**
>
> Several other actions depend on this being set first: `home_show_camera`, `home_weather`,
> `home_cameras`' automatic camera list (when called with no `cameras`), `home_slideshow`, and the
> Radio Browser stations in `home_radio`. They all fetch in the background, so the action itself
> succeeds either way. Without the token they show or offer nothing, and `home_show_camera` opens
> the camera view with `hass: no access configured` on it in place of the picture.
>
> The URL must be one the device itself can reach — its local IP address or `homeassistant.local`,
> not an external or Nabu Casa URL — since the device calls it directly rather than through Home
> Assistant's own connection to the device.

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
  url: "http://homeassistant.local:8123"
  token: !secret techo5_office_token
```

## Set an alarm

In YAML, refer to this action as `esphome.<node>_alarm_set`.

Sets an alarm on the device, or turns back on the one that already rings at that time, on those
days, with that label. Alarms set this way ring from the device's own clock, even while Home
Assistant is down.

### time (Required)

*string*

A time of day: `7:30`, `07:30`, `19:30:00`, `7:30 pm`, `7:30pm`, `3 pm`, or `3pm`.

### days (Optional)

*string*

Which days it repeats on. One of `once` (the default — rings once and turns itself off), `daily`
(or `every day`, `everyday`), `weekdays`, `weekends`, or a comma- or space-separated list of days,
matched by prefix: `mon,wed,fri`, `tue thu`.

### label (Optional)

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

### days (Optional)

*string*

Same as `alarm_set`'s `days`. Leave it out, or use `once`, for a time from now. For a one-off on a particular day, give the day instead: `today`, `tomorrow`, or a date written
`2026-09-29`. A day whose time has already gone by is refused.

### label (Required)

*string*

What to say, such as `Take the medication`. A reminder with nothing to say is refused.

### ring_on (Optional)

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
done nothing. Two automations hand those sentences back to the device that heard them. Add each one
in Settings > Automations & scenes > Create automation > Edit in YAML.

The device stops a ring by itself as well, without these: while an alarm or timer rings, the wake
word silences it, and a sentence that only asks to stop ("stop", "stop the alarm", "turn it off")
ends it. The automation is what makes Home Assistant answer "Stopped." rather than "OK".

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

Both find the device that heard the sentence, so one of each covers every TECHO5 device in the house,
and a speaker that is not a TECHO5 device is left alone.

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
Automations & scenes > Create automation > Edit in YAML.

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
      nums: "{{ spoken | regex_findall('[0-9]{1,2}') }}"
      h: "{{ nums[0] | int(-1) if nums else -1 }}"
      m: "{{ nums[1] | int(0) if nums | count > 1 else 0 }}"
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
      nums: "{{ spoken | regex_findall('[0-9]{1,2}') }}"
      h: "{{ nums[0] | int(-1) if nums else -1 }}"
      m: "{{ nums[1] | int(0) if nums | count > 1 else 0 }}"
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

### label (Optional)

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

### label (Optional)

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

## Choose which cameras the device shows

In YAML, refer to this action as `esphome.<node>_home_cameras`.

Sets the list of cameras offered on the device's Cameras page and by voice ("show the front door").

### cameras (Optional)

*string*

A comma-separated list of `entity=Display Name` pairs:

```text
camera.front_door=Front door,camera.deck=Deck
```

The `=Display Name` half can be left off, in which case the entity ID is used with its `camera.`
prefix stripped. Left out entirely, the device instead lists every camera entity Home Assistant
has, refreshed every 10 minutes — this needs `home_assistant` to be set up first.

```yaml
action: esphome.office_home_cameras
data:
  cameras: camera.front_door=Front door,camera.deck=Deck
```

## Show a camera on screen

In YAML, refer to this action as `esphome.<node>_home_show_camera`.

Puts one camera's live view up on the device's screen for a while — for an automation that shows
the front door when the doorbell rings.

> **Good to know**
>
> Needs `home_assistant` set up first. This action fetches the camera's snapshot from Home
> Assistant directly. Without it the action still succeeds, but the view shows
> `hass: no access configured` in place of the picture.

### entity (Required)

*string*

The camera entity to show. Does not need to be one of the cameras set with `home_cameras`.

### seconds (Optional)

*integer*

How long to show it for. Defaults to 30 seconds if left out or zero.

```yaml
action: esphome.office_home_show_camera
data:
  entity: camera.front_door
  seconds: 60
```

## Set the night hours

In YAML, refer to this action as `esphome.<node>_screen_night_hours`.

Sets when the night starts and ends on a Show: the hours the screen goes dark, or down to its night
light, by itself. Any times, to the minute, and the night may cross midnight. The same can be set on
the screen (Settings, Display, Night hours, Custom) and with the **Night hours**, **Night starts**
and **Night ends** selects, which offer quarter hours.

### start (Required)

*string*

When the night starts, in 24-hour time: `19:00`.

### end (Required)

*string*

When it ends: `09:30`. Both empty turns the night off.

```yaml
action: esphome.office_screen_night_hours
data:
  start: "19:00"
  end: "09:30"
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

### path (Optional)

`lovelace/0`, `dashboard-kitchen/lights`, `energy`, … Empty is the Rooms dashboard when drawn, and
the default dashboard when streamed.

## Choose the weather shown on the idle screen

In YAML, refer to this action as `esphome.<node>_home_weather`.

Sets which weather forecast the device's clock/idle screen shows. Screen devices only. Needs
`home_assistant` set up first, or no forecast will show.

### entity (Optional)

*string*

A `weather.*` entity to show, `default` (or left blank) for Home Assistant's own forecast
(`weather.forecast_home`, the one set up automatically for the home's location), or `none` to show
no weather at all. Matching is case-insensitive.

```yaml
action: esphome.office_home_weather
data:
  entity: weather.forecast_home
```

## Wire up the radio page

In YAML, refer to this action as `esphome.<node>_home_radio`.

Configures the device's Radio page: which stations it lists, what plays them, and what shows as
"now playing". Screen devices only; the change takes effect the next time the device reconnects.

> **Good to know**
>
> Favorites (`stations`) play through a Home Assistant script over the device's normal API
> connection, not through `home_assistant`. The Radio Browser stations near your home, shown
> alongside your favorites, do use `home_assistant` — without it, that part of the list is empty.

### stations (Optional)

*string*

A comma-separated list of `input_select` entities, each one a station or preset to offer on the
Radio page. Up to 20.

### now (Optional)

*string*

An entity whose state names whatever is currently playing, shown on the Radio page and the idle
screen while it's live.

### service (Optional)

*string*

The script or action that actually plays a station when one is picked on the device.

### field (Optional)

*string*

The name of the argument `service` expects the chosen station in. Defaults to `station` if left
blank.

### speaker_field (Optional)

*string*

The name of the argument `service` expects this device's media player in. Defaults to `speaker` if
left blank.

### speaker (Optional)

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

### page (Optional)

*integer*

Which page of the audio to fetch, starting at 0. Defaults to 0.

```yaml
action: esphome.office_turn_audio
data:
  id: "{{ recent.ids[0] }}"
  page: 0
response_variable: clip
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

## Set the screen's contact list

In YAML, refer to this action as `esphome.<node>_phone_contacts`.

Sets the contacts a device with a screen offers to call without a voice command, up to 12. The
numbers are kept on the device in their own owner-only file, not in Home Assistant.

### contacts (Required)

*string*

A comma-, newline-, or semicolon-separated list of `Name=number` pairs:

```text
Alex=15551234567, Sam=15557654321, Kitchen=106
```

An empty value clears the list.

```yaml
action: esphome.office_phone_contacts
data:
  contacts: "Alex=15551234567, Kitchen=106"
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
> Every device needs the same **house word** set on its setup page. A device with no word set
> neither sends announcements nor takes them, and this action will say so in the log rather than
> failing.
>
> Quiet hours are respected: inside them an announcement is shown and not sounded. Alarms, timers
> and calls are not announcements and are not affected.

### text (Optional)

*string*

What to say. Leave it out, or pass an empty string, to record instead.

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
