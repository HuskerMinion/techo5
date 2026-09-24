# Phone calls

A TECHO5 device can be a speakerphone: it signs in to a SIP provider of your choice, places the calls
Home Assistant asks for, rings for calls that come in, and carries the call through its own
microphones (with echo cancellation) and speaker. Calls between your own devices work the same way,
so one device can call another in the house.

It is off until it has a login, and it never dials anything on its own: every call is placed by you,
from Home Assistant, a voice command you set up, or by answering one.

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

## Using it

| | |
|---|---|
| **Place a call** | `esphome.<device>_phone_call` with `number`: a phone number (`15551234567`) or another account's extension. Spaces, dashes and a leading `+` are dropped. |
| **Answer** | The action button on a Dot, a tap on a Show or Spot, the **Answer call** button in Home Assistant, or `esphome.<device>_phone_answer`. |
| **Decline or hang up** | The same button again, a sideways swipe on the Spot, **Decline** / **Hang up** on the Show, the **Hang up** button, or `esphome.<device>_phone_hangup`. |
| **Say something into a call** | `assist_satellite.announce` on the same device while a call is up: the message plays in the room and goes into the call. |

### Contacts on a screen

A device with a screen can call without a voice command: the Spot's dial has **Call**, listing the
contacts Home Assistant gives it, up to 12:

```yaml
action: esphome.<device>_phone_contacts
data:
  contacts: "Alex=15551234567, Sam=15557654321, Kitchen=106"
```

An empty `contacts` clears the list. Like the login, the numbers are kept in their own owner-only file
on the device.

While a call rings or is up, the wake word is ignored (the far end talking through the speaker is not
someone in the room), and music and other sounds pause for it. A ringing call lights the Show's and
the Spot's screen and pulses the Dot's ring green.

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
devices.

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
