# A Home Assistant dashboard for your TECHO5 devices

A ready-made dashboard for Home Assistant that shows each device with its main controls and its
health sensors, and a script that stops every device at once. Both use only standard cards and
actions; nothing to install.

This is a dashboard *about* the devices, for a phone or a browser. For a dashboard shown *on* a
device's screen, see [Dashboards](dashboards.md).

## Entity ids

The examples use a device named **Kitchen Echo**, so its entities start with `kitchen_echo_`, like
`media_player.kitchen_echo_speaker`. **Replace `kitchen_echo` with your device's own prefix.** To
check an id, open **Settings → Devices & services → Devices →** your device, click the entity, then
the gear: the id is under **Entity ID**. If you renamed an entity, its id may differ.

The microphone mute switch is on its own sub-device, **Kitchen Echo microphone**, so Home Assistant
names it from that: `switch.kitchen_echo_microphone_microphone_mute`. Ids can differ between Home
Assistant versions; find yours on that sub-device's page, linked from your device's page.

Leave out the `light.kitchen_echo_screen` row on an Echo Dot, which has no screen.

## The dashboard

**Settings → Dashboards → Add dashboard → New dashboard from scratch**, open it, then **⋮ → Edit
dashboard → ⋮ → Raw configuration editor**, and paste:

```yaml
title: TECHO5
views:
  - title: Devices
    cards:
      # One vertical stack per device. Copy it for each device and change the prefix.
      - type: vertical-stack
        cards:
          - type: media-control
            entity: media_player.kitchen_echo_speaker  # replace kitchen_echo
          - type: entities
            title: Kitchen Echo
            entities:
              - switch.kitchen_echo_microphone_microphone_mute
              - button.kitchen_echo_cancel_request
              - button.kitchen_echo_stop_alarm_or_timer
              - light.kitchen_echo_screen  # not on an Echo Dot
              - sensor.kitchen_echo_next_alarm
              - sensor.kitchen_echo_last_heard
              - sensor.kitchen_echo_last_reply
              - update.kitchen_echo_firmware
          - type: glance
            title: Health
            columns: 4
            entities:
              - entity: sensor.kitchen_echo_wifi_signal
                name: Wi-Fi
              - entity: sensor.kitchen_echo_cpu_temperature
                name: CPU
              - entity: sensor.kitchen_echo_cpu_usage
                name: CPU usage
              - entity: sensor.kitchen_echo_wake_word_load
                name: Wake word
              - entity: sensor.kitchen_echo_memory_available
                name: Memory
              - entity: sensor.kitchen_echo_free_space
                name: Disk free
              - entity: sensor.kitchen_echo_last_boot
                name: Booted
                format: relative
```

The health sensors are diagnostic entities, updated every **Metrics interval** (five minutes unless you
change it). **CPU temperature** shows *Unknown* on a device whose kernel reports no CPU temperature.
**CPU usage** is how busy the processor was since the last reading. **Load average** is there too, but
reads high on these devices, 3 to 8 on an idle speaker: it counts threads asleep in the vendor's
drivers, which use almost no processor time, so don't go chasing it. **Wake word load** is the share of
a core the wake word takes, updated every 30 seconds: about 10 percent for Okay Nabu, Hey Jarvis, Hey
Mycroft and Alexa, and around 75 percent for the other built-in words on a Dot or Spot. **Wake frames**
should read 50 a second, and **Worst wake frame** shows stalls, the slowest frame in milliseconds.
**Last boot** is the time the device started, which the card shows as "3 hours ago".
**Last boot reason** says why: `update`, `restart` (asked for, from the device's settings or a reboot over
SSH), `recovery` (the device restarted itself to get out of a fault, such as no network for
15 minutes), `watchdog` (it stopped responding and was reset), `panic` (the kernel crashed), `power`
(it was plugged in or the power came back), or `unknown` where the board can't tell. An alert on
`watchdog` and `panic` catches the restarts nobody asked for.

## Stop every device at once

This script stops whatever every TECHO5 device is doing: a voice request and anything playing. It
finds the devices by their manufacturer, so it needs no editing and picks up a new device by itself.

**Settings → Automations & scenes → Scripts → Add script → ⋮ → Edit in YAML**, and paste:

```yaml
alias: Stop every TECHO5 device
mode: single
sequence:
  - variables:
      techo5: >-
        {{ integration_entities('esphome')
           | select('is_device_attr', 'manufacturer', 'TECHO5') | list }}
  - action: button.press
    target:
      entity_id: >-
        {{ techo5 | select('search', '_cancel_request$') | list }}
    continue_on_error: true
  - action: media_player.media_stop
    target:
      entity_id: "{{ techo5 | select('match', 'media_player[.]') | list }}"
    continue_on_error: true
```

It leaves alarms alone because **Stop alarm or timer** pressed on a device with nothing ringing
cancels its snoozed alarms and turns off a wake light held for the next alarm. To stop ringing
alarms and timers as well, make the button step's `entity_id`:

```yaml
        {{ techo5 | select('search', '_(cancel_request|stop_alarm_or_timer)$') | list }}
```

To mute instead of stopping, replace the last step with:

```yaml
  - action: media_player.volume_mute
    target:
      entity_id: "{{ techo5 | select('match', 'media_player[.]') | list }}"
    data:
      is_volume_muted: true
```

Run it from the script's page, put it on the dashboard as a button card, or call it from a voice
command or an automation.
