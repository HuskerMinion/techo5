# Tailscale plan: family devices in other homes

A TECHO5 device given to family in another home, an hour away or across the country, can call the
other family devices and be looked after from here, as if it were in the same house. Tailscale puts them all on one private network (a tailnet) without port forwarding or a
public address, and the intercom already built rides on it unchanged.

This is a plan, not a feature yet. Nothing here is built. Decided 2026-09-25: Tailscale ships in the
Show and Spot images, off until a device is given a key; a family word in addition to the house
word; Drop In from another home possible but off by default; a remote device runs from the owner's
Home Assistant by default, or from its own household's.

## What it is for

- **Calling each other for free,** device to device, the intercom's call over the family network.
- **A device that sits quietly and is there when needed.** At a parent's, the likely setup is a Show
  used as a picture frame (the slideshow and screensaver already exist) that can take and make a call
  by voice or with the Call button.
- **Looking after it from here:** seeing it is online, updating it, changing its settings, reading
  its log, without being there.
- **With the phone feature beside it:** SIP calling is a separate, existing feature, so "call my son"
  can ring a cell phone, and a family device can call local family's phones. The two work side by
  side: a family device on the Call list, a phone contact below it.

## What it is not

**Not a medical alert device.** The device cannot tell that someone has fallen. It helps only if the
person is within earshot, conscious, and able to say the wake word and a name. Voice commands also
need the internet and whichever Home Assistant runs the device's speech. The docs and the setup page
will say so plainly, and it should never be the only thing someone relies on.

**Not for 911.** The intercom cannot call 911. The phone feature can call any number, but a SIP
account's emergency address is the one registered with the provider, so a 911 call would send
responders to the wrong home unless that device had its own number registered at its own address.
Not something to set up casually, and not part of this plan.

## Which devices

**The Shows and the Spot.** Not the Dot: it has the least memory (roughly 480 MB), it is not the
device anyone would give for this, and the Dot's release stays as it is. The code is shared, so
nothing stops it later.

## How it works

### Tailscale on the device

- **In the image, off until given a key.** Tailscale ships in every Show and Spot image, pinned to a
  version TECHO5 has tested, so a family device needs nothing downloaded or installed where it ends
  up. It does nothing until a key is entered, and a **Tailscale** switch in Home Assistant and on the
  screen (Settings, Connections) turns it off and on again, with a status line (connected, the name
  on the tailnet). The cost is size: roughly 15 to 30 MB more in every Show and Spot update, for
  everyone, whether they use it or not.
- **Set up at home, before it is given:** the owner enters the device's key while it is still in the
  owner's house - one pre-approved auth key per device, made in the Tailscale admin page, entered
  the way the Home Assistant token is today (a `tailscale_login` action and a field on the setup
  page). The device arrives already on the owner's tailnet; nobody where it goes has to do anything
  with Tailscale. The key is tagged `tag:techo5`, and the device's name on the tailnet is its own name
  ("Grandma's Show"), so it reads well everywhere. No key is ever in an image or a release.
- **Wi-Fi where it goes** is the one thing left to do there: the Show's own Wi-Fi page, or the
  owner enters that network ahead of time when the password is known.
- **Kernel support:** Tailscale normally uses the kernel's TUN device. The Show 5's kernel has it; the
  Show 8's and the Spot's must be checked first (step 0). Without it, Tailscale can run on its own
  network stack instead, but whether calls can come in that way has to be proven on the device
  before it is relied on.
- **Cost on the device:** roughly 30 to 50 MB of memory and little CPU when idle. An estimate, to be
  measured on a Show 5, the smallest of the three.

### Calling across the family

- **Finding the others:** the intercom finds devices in the same home over mDNS, which does not
  cross Tailscale. Across it, the device asks its own Tailscale for the other `tag:techo5` devices
  online and adds them to the Call list, grouped as **Family**, under their tailnet names.
- **Trust:** the intercom already requires the house word, and the handshake refuses a caller
  without it. Family devices also share a **family word**, in addition to each home's own house word:
  a call from a device in the same home is sealed with the house word, a call across the tailnet with
  the family word, and the device answering takes either. It tries both words on the caller's first
  handshake message, so no round trip is added, and it knows which one the caller used, which is
  what the family settings below go by. Households keep their own house words.
- **What crosses homes and what doesn't:** calls do. Announcements do not: "announce dinner" stays
  in its own house, so nobody's broadcast lands in someone else's living room. Drop In from another
  home is **off** unless that home turns it on separately ("Allow Drop In from family"), since it is
  listening into someone else's room.
- **Bandwidth:** a call is about 250 kbps each way, and Tailscale connects the devices directly or,
  when routers make that impossible, through its relay servers. A slow connection (a rural link far away) is
  the thing to test.

### Home Assistant

Either, with the owner's as the default:

1. **The owner's Home Assistant runs it** (the default). The device is added to the owner's Home
   Assistant **by its tailnet name**, not its address on the owner's network, while it is still in
   the owner's house, so the same entry keeps working after it moves. That needs the Home Assistant
   host on the tailnet too (Tailscale on the host, or its add-on). Its voice commands then use the
   owner's pipeline, and the owner manages it from the owner's dashboard; this suits a parent with no
   Home Assistant of her own. The cost: its voice commands stop if the owner's internet or Home
   Assistant is down (calls placed from the screen do not).
2. **Its own household's Home Assistant runs it,** set up there as usual. Tailscale is then only for
   calling and for remote help.

Either way, Tailscale's own Home Assistant integration shows each device online or not, and the
device's switch and status sensor show its side of it.

**Voice:** "call my son" works the way the other voice commands do (docs/actions.md): an automation
that hears the sentence and calls `intercom_call` (a family device) or `phone_call` (a phone number)
on the device that heard it.

### Security

- Only devices on your tailnet can reach them at all, and the intercom's audio is encrypted end to end
  with the family word.
- The devices' other ports (ESPHome 6053, the web port 8181, SSH when it is on) would be reachable
  from the whole tailnet. The docs will give a ready-made ACL: `tag:techo5` devices reach each other's
  intercom port only, and only your Home Assistant host reaches 6053.
- An auth key is a secret: entered over the encrypted Home Assistant link or on the setup page, stored
  owner-only on the device, never logged or shown again.

## Costs

- **Tailscale:** the free Personal plan covers this (up to 6 users and unlimited devices, as its pricing
  page says today). All the devices can sit on your one tailnet as tagged devices.
- **SIP phone calls,** where wanted: a provider account, a few dollars a month (docs/phone.md).

## Order of work

0. **Check the kernels:** TUN support on a Show 8 and a Spot, and whether the userspace fallback
   takes incoming calls. Decides whether the rest needs a fallback at all.
1. **Tailscale on the device:** in the Show and Spot images (pinned), started and stopped with the
   switch, `tailscale_login`, status sensor, the setup page field. Measured on a Show 5, and the
   image's size checked.
2. **Family calls:** tailnet peers in the Call list as Family, the family word, calls across the
   tailnet, announcements kept local, Drop In from family as its own setting.
3. **Docs:** setting up a family device end to end, the ACL, adding a remote device to your Home
   Assistant, the "call …" automation, and the limits above in plain words.
4. **A real test:** a Show on a phone's hotspot, calling home through Tailscale, including over a
   slow connection.

## Decided

- Tailscale **in the Show and Spot images**, pinned, off until a key is entered; set up at the owner's
  before the device is given.
- A **family word in addition to** the house word; the answering device takes either.
- **Drop In from another home** can be allowed, and is **off by default**, a setting of its own.
- A remote device runs from **the owner's Home Assistant by default**, added by its tailnet name, or
  from its own household's.
