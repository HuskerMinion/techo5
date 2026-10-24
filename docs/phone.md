# Phone calls

A TECHO5 device can be a speakerphone: it signs in to a SIP provider of your choice, places the calls
Home Assistant asks for, rings for calls that come in, and carries the call through its own
microphones (with echo cancellation) and speaker. Calls between your own devices work the same way,
so one device can call another in the house.

It is off until it has a login, and it never dials anything on its own: every call is placed by you,
from Home Assistant, the screen, "call" and a name from its phone book, a voice command you set up, or
by answering one.

> **Not an emergency phone.** Whether a call to an emergency number works, and whether it reaches
> the right place, depends entirely on your provider and the address registered with it. Set your
> automations up to call people, not emergency services.

## What you need

- A SIP account per device at a provider that allows your own devices (a "bring your own device"
  or SIP sub-account). [VoIP.ms](https://voip.ms) is what this was built and tested with; anything
  that speaks standard SIP should do.
- Encryption turned on for each account (at VoIP.ms: the sub-account's **Encrypted SIP Traffic**).
  Calls go over TLS with SRTP media. At VoIP.ms, use a POP server whose name ends in a number
  (`chicago1.voip.ms`, not `chicago.voip.ms`).
- Home Assistant's link to the device encrypted with its API key (every TECHO5 device has one). The
  password is refused otherwise.

The device does not need a port forwarded: it keeps its registration open from the inside.

## Signing a device in

In Home Assistant, **Developer Tools → Actions**, for each device:

```yaml
action: esphome.<device>_phone_account
data:
  server: chicago1.voip.ms
  username: "100000_kitchen"
  password: "<the sub-account's password>"
```

The **Phone** sensor goes from *Not set up* to *Ready* within a few seconds. An empty `username` signs
the device out and removes the login. If the provider refuses the login, the sensor says *Login
refused* and the device stops trying until it is given a new one: a wrong password tried again and
again gets a home's address blocked by most providers.

The login is kept on the device in its own owner-only file, never in the settings Home Assistant or
diagnostics can read, and never in an image or a release.

### A SIP server at home (FRITZ!Box)

A SIP server on the home network, such as a FRITZ!Box's own registrar for its IP phones, usually
offers no TLS. For one of those, turn on **Phone without encryption** (a configuration switch of the
device, off by default): the device then signs in over plain UDP on port 5060, and calls go without
SRTP. Turning it on or off signs the device in again at once, which ends a call in progress. While it is off, the device signs in
with TLS and encrypts calls as before; leave it off for a provider on the internet.

On a FRITZ!Box: **Telefonie → Telefoniegeräte → Neues Gerät einrichten → Telefon (mit und ohne
Anrufbeantworter) → LAN/WLAN (IP-Telefon)**. Give it a name, and note the username and password it
asks for. Then:

```yaml
action: esphome.<device>_phone_account
data:
  server: fritz.box          # or the FRITZ!Box's address, e.g. 192.168.178.1
  username: "<the IP phone's username>"
  password: "<the IP phone's password>"
```

Its internal numbers (`**610`, `**620` …) and outside numbers both work, as from any IP phone on the
box.

> **Without encryption,** the call's audio and the SIP login cross the network unencrypted, and so
> does the password's digest. Keep it to a SIP server on your own network. With a provider on the
> internet, leave it off.

## Using it

| | |
|---|---|
| **Place a call** | `esphome.<device>_phone_call` with `number`: a phone number (`15551234567`) or another account's extension. Spaces, dashes and a leading `+` are dropped. The screen shows the number. |
| **Place a call to someone by name** | `esphome.<device>_phone_call_named` with `number` and `name`: the same call, and the screen and the **Phone call** sensor show "Alex (15551234567)". For an automation or assistant with a contact list of its own. A call placed by voice from the phone book (below) shows the contact's name the same way. |
| **While it rings out** | Whatever the far end's network sends before the answer is played: its ringing tone, or an announcement such as "the number you have dialed is not available". A network that only says the far end is ringing gets the device's own ringing tone (425 Hz, a second on, four off). |
| **Answer** | The action button on a Dot, a tap on a Show or Spot, the **Answer call** button in Home Assistant, `esphome.<device>_phone_answer`, or by voice with **Wake word during calls** on (below). |
| **Decline or hang up** | The same button again, a sideways swipe on the Spot, **Decline** / **Hang up** on the Show, the **Hang up** button, `esphome.<device>_phone_hangup`, the hang-up word, or by voice with **Wake word during calls** on (below). |
| **Say something into a call** | `assist_satellite.announce` on the same device while a call is up: the message plays in the room and goes into the call. |

### The phone book

Each device keeps a phone book of up to 12 contacts. The screen's **Call** list shows them (the Show's
drawer, the Spot's dial), and "call Mom" said to the device calls them (below). Set it on the setup
page (**Sound & Voice**, **Phone book**), one contact to a line:

```text
Mom|Mother = 15551234567
Alex = 15557654321
Front Desk = 106
```

or from Home Assistant, with the same list on one line:

```yaml
action: esphome.<device>_phone_contacts
data:
  contacts: "Mom|Mother=15551234567, Alex=15557654321, Front Desk=106"
```

- A name, `=`, and the number or extension. Spaces, dashes and a leading `+` in the number are dropped.
- Other names for the same contact go after the first, each after a `|`. The first is the one the
  screen shows; voice can use any of them.
- A name can be used only once in the whole book, other names included, whatever its capitals. A list
  with one used twice is refused, and nothing is saved.
- An empty list clears the book. The action and the setup page replace the whole book each time.

Like the login, the book is kept in its own owner-only file on the device. Saving it puts no number in
a Home Assistant entity or the device's log, only how many contacts there are. A call placed to one
shows its number as any call does, in the **Phone call** sensor and the call's events.

### Calling a contact by voice

Say the wake word and "call" and a name in the phone book, and the device places the call itself:

- "Call Mom", "phone Alex", "ring Mom", "dial Alex", "call up Mom", "call the Front Desk"
- "Give Mom a call"
- With "please", "hey", "can you", "could you" or "would you" before it, and "please", "now", "for me"
  or "on the phone" after it: "Can you call Mother for me, please?"

The name has to be a contact's, or one of its other names, exactly: "call Tom" does not call Mom, and
"call Mom's cell" calls nobody. Anything else in the sentence and it is left to Home Assistant, as
usual. "The" before a name is optional.

The device says "Calling Mom." and shows the call's page, then waits 3 seconds after the words before
it dials. To call it off in that time, say the stop word (or the hang-up word), press the action
button, tap **Hang up** on the screen, or press **Hang up** in Home Assistant. Nothing is dialed, and
no `dialing` event fires, until the wait is over.

- It is handled on the device: Home Assistant's own answer to the sentence is not played.
- Home Assistant says the words, so the device needs **Allow the device to perform Home Assistant
  actions** turned on. A call whose "Calling Mom." is never said is not placed: nobody heard whom it
  would call.
- The phone has to be signed in. With an account that is not signed in, the device says "The phone
  isn't signed in." With no phone account at all, the sentence goes to Home Assistant.
- It does not apply in a follow-up or in a conversation Home Assistant started, where what you say is
  an answer, or while an alarm or timer rings, or during a call. Nor with realtime voice, whose model
  answers from your voice.
- **Call contacts by voice** (the switch in Home Assistant, or the checkbox under the phone book on the
  setup page) turns it off and leaves these sentences to Home Assistant. It is on by default. It can't be
  changed through the device's remote API or by restoring settings from a file.

**With a Home Assistant automation for calls.** The device answers a sentence that names a contact in
its phone book before Home Assistant can, so an automation for the same names (the one below, or the
[call by voice blueprint](../blueprints/automation/techo5/call_by_voice.yaml)) does not get to place
the call: its answer is not played, and its own call is refused while the device's is under way. Use
one or the other for a name. The blueprint is still what calls another device in the house (`call the
kitchen` with Kitchen a device, not a contact) and names that are not in the phone book; keep a contact
from having a device's name, or "call the kitchen" dials the contact.

While a call rings or is up, the wake word is ignored (the far end talking through the speaker is not
someone in the room), unless **Wake word during calls** is on (below). Music and other sounds pause
for a call. A ringing call lights the Show's and
the Spot's screen and pulses the Dot's ring green.

### A hang-up word

Choose a word in **Hang-up word** (the device's microphone settings in Home Assistant) and saying it
during a call hangs the call up, or declines one ringing here. It is heard on the device, so it works
with Home Assistant down. It is **None**, off, until chosen. The list has **Stop**, which has the stop
word do it, and every wake word the device has but the ones chosen as wake words, so one of your own
("hang up", say) can do it, added the way a wake word of your own is (setup.md).

- A call being placed is never ended this way, only one ringing here or one that is up. The
  exception is a call by voice that has not been dialed yet (above), which the word calls off.
- It is not acted on while the other party is talking, or for a moment after: their voice comes out
  of the speaker. Wait for a gap.
- A talk through a camera is not ended this way, and neither is a call while the device is making a
  sound of its own (an announcement, the assistant's answer, a chime): the word in it is the device's.
- With **Stop**, an alarm or timer ringing during the call is stopped first, and the next "stop" hangs
  up. "Stop" comes up in conversation, so it can hang up by mistake; a word of your own that does not
  come up is less likely to. It is heard at **Stop word threshold**, and does nothing with the stop word
  turned off (that threshold at its top).
- Another word is listened for only while a call rings or is up, or while a call by voice waits to
  be dialed, at **Hang-up word threshold**, a wake word's 0.85 until set. Higher hears it less often,
  by mistake or not.
- During a call the stop word and the hang-up word are held to their full threshold, without the
  allowance words get while the speaker plays; the stop word gets it back over a ring.
- A word of your own chosen here can't be removed on the setup page or by **Purge cache** while it is
  the hang-up word, and the setting can't be changed through the device's remote API or by restoring
  settings from a file.

### Answering and hanging up by voice

Turn on **Wake word during calls** and the wake word opens a turn while a call rings here or is up, as
it does at any other time. What it does is Home Assistant's: an automation like the one below answers
or hangs up the call on the device that heard it. The call carries on while you speak, both ways.

- A wake word heard while the other party was talking, or for a moment after, is ignored: their voice
  comes out of the speaker. Wait for a gap.
- A call being placed (still ringing out) is ended with the button, not by voice.
- The device on the call answers a wake word another device nearby heard about as well, but not one
  another device heard clearly better, which was somebody talking to that one.
- While the turn listens, the other party is not played in the room, so they cannot speak into it.
  They are heard again once it has heard you.
- A call ringing here keeps ringing once the turn is over, if it has not been answered.
- The stop word does not open a turn during a call: it stops an alarm or timer, or hangs up if it is
  the hang-up word. In a turn opened during a call it cancels the turn and leaves the call up.

Two more switches decide how the turn sounds:

| | |
|---|---|
| **Assistant replies go into calls** | The assistant's answer is sent to the other party as well as played in the room. Off, only the room hears it, and the other party hears a moment of quiet. Either way the room's microphones are not sent while the answer plays, so it does not reach them a second time through the speaker. Announcements go into the call either way, over the room. |
| **Hear the call under the assistant** | The other party keeps playing, quieter, under the chime and the answer. Off, they are not heard in the room until the answer is over. |

All three are off unless chosen. Home Assistant's own wake word engine (**Wake word on Home Assistant**)
stays off during a call: only the device's own wake word is heard then.

```yaml
alias: TECHO5 - answer or hang up a call by voice
mode: parallel
triggers:
  - trigger: conversation
    command:
      - "answer [the] [phone] [call]"
      - "pick up"
    id: answer
  - trigger: conversation
    command:
      - "hang up"
      - "end [the] call"
    id: hangup
conditions:
  - "{{ device_entities(trigger.device_id) | select('search', '^sensor[.].*_phone$') | list | count > 0 }}"
actions:
  - action: "esphome.{{ device_attr(trigger.device_id, 'name') | slugify }}_phone_{{ trigger.id }}"
  - set_conversation_response: ""
```

The condition passes only for a TECHO5 device with a phone, as the automations in actions.md check
theirs, so a request from another satellite does not reach for an action it does not have. The empty
response keeps the assistant from talking over the call it just answered, or into the room after
hanging up.

### Both at once

The hang-up word and **Wake word during calls** can both be on. A word of your own such as "hang up",
heard in a turn, cancels the turn and hangs up, so "Okay Nabu, hang up" ends the call on the device
whether the word or Home Assistant hears it first. While the assistant's answer is playing the hang-up
word is not acted on, so an answer that says it does not end the call. "Stop" in a turn only cancels
the turn.

## Events

Each step of a call fires `esphome.techo5_phone` on Home Assistant's bus, with:

| Field | |
|---|---|
| `event` | `dialing`, `ringing`, `answered`, `not_answered`, `declined`, `missed`, `ended` |
| `device` | The device's name |
| `peer` | The other party: a number, an extension, or a caller ID name and number |
| `direction` | `incoming` or `outgoing` |
| `kind` | `phone`, or `intercom` for a call between two devices in the house |
| `by`, `seconds` | On `ended`: who hung up (`device` or `far end`) and how long the call lasted |

The device needs **Allow the device to perform Home Assistant actions** turned on in its ESPHome
integration options for events to arrive.

## Example automations

Replace the names and numbers with your own. These keep your contacts in Home Assistant, not on the
devices. For names in a device's own phone book, the device places the call itself (above), before an
automation for them can.

### "Call Alex" from whichever device heard it

```yaml
alias: Call by voice
mode: parallel
triggers:
  - trigger: conversation
    command:
      - "call [the] {who}"
variables:
  book:
    alex: "15551234567"
    kitchen: "106"
  number: "{{ book.get(trigger.slots.who | lower | trim, '') }}"
actions:
  - if: "{{ number == '' or trigger.device_id is none }}"
    then:
      - set_conversation_response: "I don't have a number for {{ trigger.slots.who }}."
      - stop: unknown
  - action: "esphome.{{ device_attr(trigger.device_id, 'name') | slugify }}_phone_call"
    continue_on_error: true
    data:
      number: "{{ number }}"
  - set_conversation_response: "Calling {{ trigger.slots.who }}."
```

### "I need help": alert phones and call people in turn

```yaml
alias: Help by voice
mode: single
triggers:
  - trigger: conversation
    command: ["I need help", "help me", "emergency"]
variables:
  device: "{{ trigger.device_id }}"
  room: "{{ device_attr(device, 'name') }}"
  call: "esphome.{{ room | slugify }}_phone_call"
  hangup: "esphome.{{ room | slugify }}_phone_hangup"
  satellite: "{{ device_entities(device) | select('match', 'assist_satellite\\.') | first }}"
actions:
  - action: notify.mobile_app_alex_phone
    data:
      title: Help needed
      message: "{{ room }}: someone asked for help."
      data: { ttl: 0, priority: high, channel: alarm_stream }
  - set_conversation_response: Calling for help now.
  - repeat:
      for_each: ["15551234567", "15557654321"]
      sequence:
        - action: "{{ call }}"
          data: { number: "{{ repeat.item }}" }
        # dialing arrives first and decides nothing: wait for what does.
        - wait_for_trigger:
            - { trigger: event, event_type: esphome.techo5_phone, event_data: { device: "{{ room }}", event: answered } }
            - { trigger: event, event_type: esphome.techo5_phone, event_data: { device: "{{ room }}", event: not_answered } }
            - { trigger: event, event_type: esphome.techo5_phone, event_data: { device: "{{ room }}", event: ended } }
          timeout: 70
        - if: "{{ wait.trigger is not none and wait.trigger.event.data.event == 'answered' }}"
          then:
            - delay: 1
            - action: assist_satellite.announce
              target: { entity_id: "{{ satellite }}" }
              data:
                message: "This is a help call from the {{ room }}. Someone here asked for help."
                preannounce: false
            - stop: answered
          else:
            - action: "{{ hangup }}"
```

A voicemail box answering counts as answered, so the list stops there.

### Missed calls

```yaml
alias: Missed call
triggers:
  - trigger: event
    event_type: esphome.techo5_phone
    event_data: { event: missed }
actions:
  # Every device in a ring group reports the call; one of them may have answered it.
  - delay: 3
  - condition: template
    value_template: "{{ states.sensor | selectattr('entity_id', 'search', '_phone$') | selectattr('state', 'eq', 'In call') | list | count == 0 }}"
  - action: notify.mobile_app_alex_phone
    data:
      title: Missed call
      message: "Missed call from {{ trigger.event.data.peer }}."
```

## Ringing every device

At VoIP.ms, give your phone number a **ring group** with each device's sub-account in it and a
failover (your mobile, or voicemail) for when nobody answers. Every device rings; the first to answer
takes the call and the others stop.

## Calling another device in the house

The intercom calls one TECHO5 device from another with no phone account at all: the
`intercom_call` action ([actions.md](actions.md#call-another-device-in-the-house)) with the other
device's name. It rings, answers and hangs up like a phone call, on the same page and the same
button, and fires the same events with `kind: intercom`. Both devices need the same house word from
the setup page. The sound is the microphones' own 16 kHz rather than a phone line's 8 kHz, encrypted
between the two devices with the house word as the key. What is planned next is in
[intercom-plan.md](intercom-plan.md).

**Calling from the screen.** On a Show, the drawer (swipe in from the right edge) has a **Call** tab:
the other devices in the house first, then your contacts, each with a Call button. On a Spot, the
ring menu's **Call** item opens the same list. Devices are listed once a house word is set; contacts
once the phone is signed in. To put a green Call button on the home screen that opens the list, turn
on **Call button** in Settings, Display (or the **Call button on the home screen** switch in Home
Assistant). It is off by default.

**Do Not Disturb and Drop In.** Each device decides how it takes intercom calls. **Do not disturb**
(Settings, Sound & Voice, or the **Intercom do not disturb** switch) turns them away and tells the
caller why. **Allow Drop In** (Settings, Privacy & Security, or the **Allow Drop In** switch) lets a
call connect by itself after a short chime, with **Drop In** and the caller's name on the screen; it is
a way to listen in on a room, so it is off unless you turn it on. Drop In only answers by itself for
another TECHO5 device the house already knows, calling from that device's own address; any other
caller rings as usual. The chime plays at a minimum volume, so a turned-down room still hears it.
Announcements between devices are signed with the house word rather than carrying it, so it never
crosses the network. Devices from before that change still send it as it is, so update every device
together: a device on the older release also turns away announcements from an updated one. On a Dot,
which has no screen, both are the Home Assistant switches, and the action button answers and hangs up.

## How it works

- `echod/internal/feature/phone`: the component. SIP and RTP through
  [diago](https://github.com/emiago/diago) and [sipgo](https://github.com/emiago/sipgo) (pure Go),
  G.711 at 8 kHz. The microphones' output after echo cancellation is filtered and halved to 8 kHz on
  the way out; the far end is doubled back to 16 kHz and played as the speaker's voice, which makes it
  the echo canceller's reference like any other sound.
- The speaker is held for the call. An announcement takes it for as long as it plays and the call
  takes it back; if the speaker falls more than 300 ms behind the network, the backlog is dropped so
  the call stays live.
- VoIP.ms offers only RSA key exchange on its TLS port, which Go leaves out unless asked for; the
  device asks for it after the forward-secret suites. The connection is still encrypted and the
  certificate still checked.
- Each call's log ends with a line counting the audio frames sent and received and their level, so
  a call where one side heard nothing says which way the audio stopped.
