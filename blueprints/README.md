# Home Assistant blueprints

Ready-made automations and a script for TECHO5 devices. Each import button opens your Home Assistant
and adds the blueprint; then create an automation from it in **Settings > Automations & scenes >
Blueprints**. To import by hand, paste the file's GitHub address into **Import blueprint** there.

Most of the voice ones need nothing set: one automation covers every TECHO5 device in the house,
answers on the device that heard the sentence, and leaves other voice satellites alone. Their action
names come from each device's own name (`esphome.<name>_alarm_set`), so renaming a device in Home
Assistant is fine, but a device whose name has letters outside a to z (Café) needs its action names
put in by hand. The event ones need **Allow the device to perform Home Assistant
actions** turned on in the device's ESPHome integration options.

If you already added the same automations from [actions.md](../docs/actions.md) or
[phone.md](../docs/phone.md) by pasting YAML, delete those first, or both will answer.

## Voice

| | | |
|---|---|---|
| [Stop alarm or timer](automation/techo5/stop_alarm_by_voice.yaml) | "Stop the alarm", "turn off the timer" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fstop_alarm_by_voice.yaml) |
| [Snooze an alarm](automation/techo5/snooze_alarm_by_voice.yaml) | "Snooze for ten minutes" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fsnooze_alarm_by_voice.yaml) |
| [Set an alarm](automation/techo5/set_alarm_by_voice.yaml) | "Wake me up at half past six", "set a silent alarm for 6:45", "wake me with the light at 6:30" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fset_alarm_by_voice.yaml) |
| [Cancel or list alarms](automation/techo5/cancel_or_list_alarms_by_voice.yaml) | "Cancel the 6 AM alarm", "what alarms do I have" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fcancel_or_list_alarms_by_voice.yaml) |
| [Set a reminder](automation/techo5/set_reminder_by_voice.yaml) | "Remind me to take out the trash every Tuesday at 8:30 PM" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fset_reminder_by_voice.yaml) |
| [Volume on the speaker that heard it](automation/techo5/volume_by_voice.yaml) | "Volume 4", "volume down 2" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fvolume_by_voice.yaml) |
| [Rain sounds](automation/techo5/rain_sounds_by_voice.yaml) | "Play the sounds of rain" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Frain_sounds_by_voice.yaml) |
| [Call by voice (intercom and phone)](automation/techo5/call_by_voice.yaml) | "Call the kitchen", "call Alex" (names in a device's own phone book are called by the device) | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fcall_by_voice.yaml) |
| [Answer or hang up a call](automation/techo5/answer_or_hang_up_by_voice.yaml) | "Answer the call", "hang up the office" | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fanswer_or_hang_up_by_voice.yaml) |
| [Help by voice](automation/techo5/help_by_voice.yaml) | "I need help": alert phones, then call people in turn | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fhelp_by_voice.yaml) |

## Events

| | | |
|---|---|---|
| [When an alarm goes off](automation/techo5/alarm_event.yaml) | Run actions when an alarm rings, goes off silently, is snoozed or stopped | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Falarm_event.yaml) |
| [Follow the wake light](automation/techo5/follow_wake_light.yaml) | Bring a room light up with the wake light | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Ffollow_wake_light.yaml) |
| [Show a camera when something happens](automation/techo5/show_camera.yaml) | A camera on TECHO5 screens when a door or gate opens, or any trigger | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fshow_camera.yaml) |
| [Announce when something happens](automation/techo5/announce.yaml) | Words said on every device in the house, from any trigger | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fannounce.yaml) |
| [Night mode with the house](automation/techo5/night_mode.yaml) | Night on TECHO5 screens while a bedtime helper or schedule is on | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fnight_mode.yaml) |
| [Timer while an appliance runs](automation/techo5/appliance_timer.yaml) | A device timer when the washer starts, canceled if it stops first | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fappliance_timer.yaml) |
| [Doorbell picture](automation/techo5/doorbell_picture.yaml) | Door camera on TECHO5 devices when the bell rings; act when nobody answered | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fdoorbell_picture.yaml) |
| [Notification nobody saw](automation/techo5/notification_missed.yaml) | Act when a tagged notification runs out unseen | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fnotification_missed.yaml) |
| [Missed call](automation/techo5/missed_call.yaml) | Act on a phone call nobody answered | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fmissed_call.yaml) |
| [Dashboard while a speaker plays](automation/techo5/dashboard_while_playing.yaml) | Now Playing dashboard on a Show | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fdashboard_while_playing.yaml) |
| [Voice request failed](automation/techo5/voice_request_failed.yaml) | A notification when a voice request errors; optional logbook line | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Ftecho5%2Fvoice_request_failed.yaml) |

## Scripts

| | | |
|---|---|---|
| [Notify devices](script/techo5/notify_devices.yaml) | A card, or a picture or camera, on chosen or every TECHO5 device, for any automation to call | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fscript%2Ftecho5%2Fnotify_devices.yaml) |
| [Stop every device](script/techo5/stop_every_device.yaml) | Stop every TECHO5 device's voice request and playback | [![Import](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FHuskerMinion%2Ftecho5%2Fblob%2Fmain%2Fblueprints%2Fscript%2Ftecho5%2Fstop_every_device.yaml) |
