# NetBird: reaching a device from away

A TECHO5 device can join your [NetBird](https://netbird.io) network, the other way, beside
[Tailscale](tailscale.md), to keep a device in another house in your own Home Assistant. Use NetBird's
own service or a management server you run yourself.

It's off until you turn it on, and nothing of it is in the image. The first time you join, the device
downloads NetBird's client (about 14 MB, 40 MB unpacked). That download is checked against the size and
SHA-256 the device's own software was built with. It's offered wherever Tailscale is: Echo Shows, the
Echo Spot and the Skylight, on a 64-bit kernel. The Echo Dots don't offer it.

## What it does, and what it doesn't

The same as Tailscale, by the same rules (see [what Tailscale does](tailscale.md#what-it-does-and-what-it-doesnt)):

- **Home Assistant over NetBird.** Home Assistant reaches the device on its usual encrypted ESPHome
  connection (port 6053), at its NetBird name or address.
- **The setup page, only with Remote setup.** Over NetBird the web port answers nothing else.
- **SSH, if you turned it on.**
- **Nothing else gets in.** The device's own firewall lets NetBird's interface reach only ports 6053,
  8181 and 22, and ping. NetBird's access policies narrow that further, per peer: which peers see the
  device, and which of those ports and protocols each of them reaches. A policy can never open more:
  see [Access policies](#access-policies).
- **The device takes nothing from the network.** No DNS, no routes, no IPv6, no NetBird SSH server. The
  client sends NetBird no metrics. It does talk to your management server, and to NetBird's signal and relay servers, which is
  how peers find each other.
- **Announcements, Drop In and shared settings stay in each house.**

## Set it up

1. **Make sure Home Assistant's link to the device has an encryption key.** Without one, the device
   won't turn NetBird on.
2. **Make a setup key** in your NetBird dashboard: **Setup Keys → Create Setup Key**. Make it one-off,
   with a short expiry. Give it a group (say `techo5`) so your access policies can name the devices.
3. **Give the device the key.** Either:
   - the setup page: **Network → NetBird**, paste the key, and if you run your own NetBird, put
     your management server's URL (`https://netbird.example.com`). Leave it empty for NetBird's own.
     Then **Join**. One Join is enough. The first time, the client is downloaded first; the device
     holds the key in memory only, joins by itself when the download is done, and the page shows the
     download and then the joined state. If the join fails, or the device restarts or the download
     fails before then, the page says why and you give the key again. Use an `https://`
     address: with `http://` the setup key crosses the network unencrypted.
   - Home Assistant: the [`netbird_login` action](actions.md#join-a-netbird-network).

   The key is used once, to join, and isn't kept. The **NetBird status** sensor then says
   *Connected as kitchen (100.x.y.z)*.
4. **Add the device to Home Assistant by its NetBird name.** The Home Assistant host has to be on the
   same NetBird network. In **Settings → Devices & services → ESPHome**, add the device as
   `<name>.<your peer domain>` (the name is on the dashboard's **Peers** page, the domain under **DNS → Settings**),
   with the same encryption key, and remove the old entry that used its home
   address. Prefer the name to the mesh address: the address changes when the peer registers again,
   and the name keeps working.
5. **Take it where it goes,** and set up the Wi-Fi there. It rejoins by itself.

The **NetBird** switch, in Home Assistant and on the screen, stops it and starts it again; stopped,
the device keeps its place on the network and rejoins when turned back on. Remote setup works over
NetBird the same way as over Tailscale: see [Remote setup](tailscale.md#remote-setup).

## Access policies

The device's own firewall decides which ports can be reached over NetBird at all: only 6053, 8181 and
22, and ping, on the NetBird interface. The policies in **Access Control → Policies** narrow that per
peer: which peers can see the device, and which of those ports and protocols each of them reaches. The
NetBird client on the device enforces them with its own firewall, under the device's, so a policy can
never open more. A device joined before the client did this turns it on when it next starts.

A new NetBird network lets every peer reach every other. Let only your Home Assistant host's group
reach the `techo5` group on TCP 6053, and only your own devices reach TCP 8181 and 22, and keep the
setup page and SSH off unless you need them.

## Leaving the mesh

Turning **NetBird** off, on the screen or in Home Assistant, stops it but keeps the peer: the device
stays on your network and rejoins when turned back on. To leave for good, turn NetBird off, then remove
the device under **Peers** in your NetBird dashboard.

## Good to know

- **Tailscale or NetBird, not both on the same device.** Both number their devices in 100.64.0.0/10, and
  two at once can claim the same addresses.
- **Cost on the device:** nothing until you join, then about 40 MB of storage, some memory while it
  runs, and a little CPU: on an Echo Show 8 (2nd gen), about 1.5% of one core when idle, and about 4.5%
  with some 50 packets a second going through, of which NetBird's own firewall takes 0.3 to 0.5 points.
- **Turning NetBird off** from far away cuts your way to it. Turning it back on then needs somebody
  there: the switch on the screen.
- **A device that goes missing** stays on your network until you remove it under **Peers** in the
  dashboard.
- **A diagnostics bundle leaves out** the device's NetBird name and address.
- The client is built from NetBird's own source by
  [tools/linux/build-netbird.sh](../tools/linux/build-netbird.sh), pinned to a tested version. Its
  client is BSD-3-Clause; the license comes with it, beside the client in the device's NetBird folder.
