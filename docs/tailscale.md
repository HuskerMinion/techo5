# Tailscale: reaching a device from away

A TECHO5 device can join your [Tailscale](https://tailscale.com) network (your *tailnet*). That lets
a device in another house, a relative's or a vacation home, stay in your own Home Assistant: added
once by its tailnet name, it keeps working wherever it is plugged in, with no ports opened on
anybody's router.

Every Echo Show, Echo Spot and Skylight image carries Tailscale, off until you give it a key. It needs
the device's 64-bit kernel: an Echo Show 8 2nd gen still on Amazon's kernel doesn't offer it. The Echo
Dots don't carry it: they stay in the house.

## What it does, and what it doesn't

- **Home Assistant over the tailnet.** Home Assistant reaches the device on its usual encrypted
  ESPHome connection (port 6053), by the device's tailnet name. Everything works as it does at home:
  the entities, the actions, voice through your Assist pipeline.
- **The setup page, only when you open it from Home Assistant.** Over the tailnet, the device's web
  port answers nothing: no camera, no screen, no announcements or calls between devices, no shared
  settings. The one exception is **Remote setup** (below), for as long as you turn it on.
- **SSH, if you turned it on.** The device's SSH server answers on the tailnet too, with the keys you
  gave it (`ssh_keys`) and nothing else. It is off unless you turn it on.
- **Nothing else gets in.** The device's firewall lets the tailnet reach only those three ports (6053,
  8181 and 22) and ping. AirPlay, Spotify Connect, the Music Assistant player and the camera stream
  answer only the house the device is in.
- **The device takes nothing from the tailnet.** No DNS, routes or exit node; no Tailscale SSH or
  Taildrop; and it sends Tailscale no logs. Those parts are left out of the build. Like any Tailscale
  device, it does tell Tailscale's coordination server its name and its network addresses, so its
  name shows in your admin console.
- **Announcements, Drop In and shared settings stay in each house.** A device from away is never
  one of this house's devices, even on the same tailnet.

## Set it up

Do this while the device is still at home, where you can stand at it.

1. **Make sure Home Assistant's link to the device has an encryption key** (the API encryption key
   you added it with). Without one the device won't turn Tailscale on: over the tailnet, its Home
   Assistant connection would be open in the clear.
2. **Make an auth key.** In the Tailscale admin console, **Settings → Keys → Generate auth key**.
   Make it **pre-approved**, **single-use**, with a short expiry, and give it the tag **`tag:techo5`**
   (add the tag to your access rules first, below). A tagged device doesn't expire off the tailnet, so
   it stays on until you remove it in the admin console.
3. **Give the device the key.** Either:
   - the setup page: **Connections → Tailscale**, paste the key, **Join**. The setup page is plain
     HTTP on your home network, which is one more reason for a single-use key; or
   - Home Assistant: the [`tailscale_login` action](actions.md#join-a-tailnet).

   The key is used once, to join, and isn't kept. The device's **Tailscale status** sensor then says
   *Connected as kitchen (100.x.y.z)*, and the screen says it's connected under **Settings →
   Connections**.
4. **Add the device to Home Assistant by its tailnet name.** The Home Assistant host has to be on
   the tailnet too: Tailscale on the host, or Tailscale's Home Assistant add-on. In **Settings →
   Devices & services → ESPHome**, add the device with its tailnet name (`kitchen`, or
   `kitchen.your-tailnet.ts.net`) and the same encryption key. Remove the old entry that used its
   home address.
5. **Take it where it goes,** and set up the Wi-Fi there on the device's own Wi-Fi page. That's all
   anyone there has to do: it rejoins the tailnet by itself.

The **Tailscale** switch, in Home Assistant and on the screen, stops it and starts it again; stopped,
the device keeps its place on the tailnet and rejoins when turned back on.

## Remote setup

The setup page normally needs a press on the device. For a device nobody can stand at:

1. Turn on **Remote setup** in Home Assistant. It opens the setup page and lets the tailnet reach it,
   for this opening of the page only. It needs an API encryption key.
2. Open `http://<the device's tailnet address>:8181/setup` in a browser on the tailnet. Use the
   address (`100.x.y.z`), not the name: the page refuses a request by name from the tailnet.
3. The page asks to be let in. **Setup page asking** in Home Assistant says where the browser is
   asking from. If that's yours, press **Let in from Home Assistant**. The device's screen says a
   browser was let in, so whoever is in the room knows.
4. When you're done, turn **Remote setup** off, or just close the page. Either way every browser it
   let in is let go, and the next time the page opens it's the house's own again.

## Access rules

Without rules of your own, every device on your tailnet can reach those three ports. These rules let
only your Home Assistant host reach the ESPHome connection, and only the devices of your tailnet's
admins reach the setup page and SSH. Tag the Home Assistant host `tag:homeassistant`. A new tailnet's
policy starts with a rule that lets everything reach everything: take that rule out, or these add
nothing. Then adjust to taste:

```json
{
  "tagOwners": {
    "tag:techo5":        ["autogroup:admin"],
    "tag:homeassistant": ["autogroup:admin"]
  },
  "grants": [
    {"src": ["tag:homeassistant"], "dst": ["tag:techo5"], "ip": ["tcp:6053"]},
    {"src": ["autogroup:admin"],   "dst": ["tag:techo5"], "ip": ["tcp:8181", "tcp:22"]}
  ]
}
```

## Good to know

- **Cost on the device:** about 13 MB in every Show, Spot and Skylight image, used or not, and some
  memory while it runs. Off, nothing runs.
- **Voice from away goes through your Home Assistant,** so if your internet or Home Assistant is down,
  the device far away can't answer. Its clock, alarms and timers keep working.
- **A diagnostics bundle leaves out** the device's tailnet name and address.
- **Turning Tailscale off** in Home Assistant from far away cuts your only way to it. Turning it back
  on then needs somebody there: the switch on the screen.
- **A device that goes missing** stays on your tailnet until you remove it in the admin console's
  **Machines**. Remove it there, and it's off for good.
- Tailscale is built from its own source by
  [tools/linux/build-tailscaled.sh](../tools/linux/build-tailscaled.sh), pinned to a tested version.
  Its license (BSD-3-Clause) is in the image at `/usr/share/licenses/tailscale/LICENSE`.
