# Intercom plan

A two-way call between two TECHO5 devices in the same house: the office calls the kitchen, the kitchen
answers, and they talk. It works with Home Assistant down and the internet down, the same as
announcing does.

This is a plan, not a feature yet. Nothing here is built. Agreed 2026-09-24: our own stream rather
than SIP, and Drop In off by default with a setting to turn it on.

## What is already there

- **Finding each other.** Announcing already advertises every device over mDNS (`_techo5._tcp`) and
  keeps a list of the others (`feature/announce/peers.go`). The intercom uses the same list.
- **Trust.** Announcing already has a house word, set on each device's setup page. A device with no
  word takes nothing. The intercom uses the same word, so there is nothing new to set up.
- **Call audio.** The phone feature already carries a call both ways: the microphones after echo
  cancellation out, the far end to the speaker, with a limit on how far behind the speaker may fall
  (`feature/phone/call.go`). The wake word already pauses while a call is up.
- **Call screens.** The Show and the Spot already have a call screen with answer and hang up
  (`render_phone.go`, `phone_spot.go`), and the action button already answers and hangs up.
- **Encryption.** The dashboard stream already uses Noise with a shared key (`feature/dashboard/secure.go`).

## How it works

### The connection: our own stream, not SIP

The older plan (standalone-plan.md) said to point the phone's SIP stack at the LAN. Decided instead: a
small stream of our own, because:

- **Better sound.** SIP calls here are 8 kHz phone quality. Our own stream can send the microphone's
  16 kHz as it is. On a home network the bandwidth (about 256 kbps each way) does not matter.
- **Better trust.** Announcing sends the house word in a plain header today, so anyone watching the
  network could read it. The intercom would use Noise keyed by the house word, the same way the
  dashboard stream uses its key. The word never crosses the network, the wrong word fails the
  handshake, and the audio is encrypted.
- **Less to go wrong.** No SIP dialogs, no SDP, no RTP ports. One TCP connection per call.

The cost: it only talks to TECHO5 devices. That is all it is for. Calling a real phone stays the phone
feature's job.

### A call, step by step

1. The caller opens a connection to the other device and does the handshake. A wrong house word ends
   it here.
2. The caller says who it is ("Office") and asks to ring.
3. The other device rings: the call screen with the caller's name and answer/decline, the ring tone,
   and the light ring on a Dot. It answers "busy" if it is already on a call (phone or intercom), and
   "not now" if Do Not Disturb is on.
4. Someone answers (screen, action button, or Home Assistant). Both sides start the call audio.
5. Either side hangs up, or the connection drops, and both go back to normal.

No answer in 30 seconds is "no answer" on the caller's screen.

### Drop In

Alexa's Drop In connects without anyone answering. It is handy (checking on a room) and it is also a
way to listen in on a room. So it is **off by default**, per device: "Allow Drop In", a setting on the device
(Show: Settings, General; Spot: the settings tile page) and a switch in Home Assistant. When it is on, a call to that device connects after a short chime, with the call screen showing who is
listening. When it is off, every call rings.

### Starting a call

- **From the screen** (works with Home Assistant down): a Call page with two lists.
  - **In the house:** the other devices, from the peers list. Tap one to call it over the intercom.
  - **Contacts:** the phone feature's contacts, shown only when a phone account is signed in. Tap one to
    call it over the phone line.

  One page for both, so there is one place to go to call anyone. Reaching it:
  - **A Call button on the home page.** A setting turns it on or off: Show, Settings, General, "Call
    button on the home page"; Spot, the settings tile page, the same. It is off by default, so nobody's
    home page changes on an update. It is also a switch in Home Assistant.
  - **Always in the menus** as well, button or no button: an "Intercom" tab in the Show's drawer, an
    "Intercom" item on the Spot's ring menu.
- **From Home Assistant:** an `intercom_call` action with the device's name, and `intercom_hangup`.
- **By voice** ("call the kitchen", "drop in on the kitchen"): an automation like the other voice ones in
  actions.md, which calls `intercom_call` on the device that heard it. This needs Home Assistant,
  because speech to text is Home Assistant's.
- **Dot:** no screen, so it calls by voice or from Home Assistant, and answers with the action button.

### What Home Assistant sees

- A sensor for the intercom's state (idle, ringing, calling, in a call) and who is on the other end.
- A switch for Do Not Disturb, one for Allow Drop In, and one for the Call button on the home page.
- Events on the bus, like the phone's (`ringing`, `answered`, `ended`), so automations can react.

## Limits

- **One call at a time** per device, counting phone calls. A second call gets "busy".
- **Quiet hours** do not stop a call from ringing, the same as the phone. Do Not Disturb does.
- **Rate limit:** after a call is declined, the same device cannot ring it again for 30 seconds.
- **Two devices per call.** No group calls. "Call everyone" is what announcing is for.
- **Audio only.** No video, even on the Show 8 with a camera.
- **Inside the house only.** No calling in from outside.

## The Dot

The Dot suits the intercom well: one per room, a good speaker, the best microphones. But it has the
least CPU. The wake-word loop already uses 20 to 36% of it, and a call adds echo cancellation and the
stream. The wake word pauses during a call, which frees most of that. Still, the Dot is tested
separately and gets its own go/no-go.

## Order of work

1. **Show to Show**, started from Home Assistant: the stream, the handshake, ringing, answering,
   hanging up, the entities and events. Tested on two Shows on the bench.
2. **Screens:** the Call page (devices and contacts), the home page Call button and its setting, the
   Intercom tab on the Show, the ring menu item on the Spot, the call screen showing the device's name.
3. **Dot:** answering with the action button, the ring on the light, the CPU check.
4. **Drop In** and Do Not Disturb.
5. **Docs:** the voice automation in actions.md, a section in the README, and the house word note on
   the setup page.

Each step lands on main with tests and gets a bench test before the next one starts.

## Later, not part of this

- Moving announcing onto the same Noise handshake, so its house word stops crossing the network in
  plain text.
