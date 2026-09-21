# The setup page: a design to argue with

Nothing here is built. This is the write-up asked for before any code, because this is the first
thing on a TECHO5 device that **accepts input** rather than serving pictures, and the first that a
browser on the network can change the device with.

What it is for: the handful of settings that are miserable or impossible to set on the device itself
— a radio station's stream URL, a SIP password, the time zone on a device with no screen. On a Show
or a Spot it saves typing. **On a Dot it is the only interface there is.**

## What has to move first

The web server today lives inside `feature/camera` and starts only when `camera.Available()` is
true. On a Dot that is `false`, so **a Dot runs no web server at all**. The server has to come out
into a feature of its own that runs everywhere, with each path gated by its own switch:

| Path | Gate | Today |
|---|---|---|
| `/camera.jpg`, `/camera.mjpeg` | Camera web access | in `feature/camera` |
| `/screen.png` | Screen web access | in `feature/camera` |
| `/setup` and its posts | Setup page, new | — |

One port (8181), three gates, none of them on by default, and the port closed when all three are off.
That is how it behaves now and it should keep behaving that way.

## Getting in: a press on the device

No password, no code to read off a screen a Dot does not have. The page asks you to **press the
button on the device**, and the press is what authorizes the browser.

1. A browser asks for `/setup`. Nothing is readable yet: the page says *press the action button on
   your Kitchen device*, and waits.
2. The device says so too — a line on the screen where there is one, the light ring pulsing where
   there is not — so that a page asking for a press cannot be mistaken for something else. **Anybody
   in the room can see that a browser is asking.** That is the point of it.
3. A press within sixty seconds issues a session cookie to *that* browser. No press, no session, and
   the page says so and offers to ask again.
4. The session lasts while setup mode is on and dies with it, with the daemon, or after an hour.

Why a press rather than a code: it proves the same thing — someone is standing at the device — and it
works on every device, including the one with no display. It also cannot be shoulder-surfed or read
off a photograph.

Rules that go with it:

- **One press, one session.** A press authorizes the browser that was waiting, not every browser that
  happens to be asking. If two are waiting, the press is refused and both are told why: ask again,
  one at a time.
- **A press is not a login.** It cannot be replayed: the pending request has a nonce, it expires with
  the sixty seconds, and it is forgotten once used.
- **Setup mode is off by default**, turned on from the device's own screen (Settings → Privacy), from
  Home Assistant where there is one, and — on a Dot — by holding the action button, since there is
  nowhere else to ask.
- **It turns itself off.** Fifteen minutes from being switched on, or five from the last request,
  whichever is sooner. A page left open for a year is a different thing from a page open while you
  set a device up.

## What it may write, and what it may never touch

Allowed:

- Radio favorites: a name and a stream URL, a handful of them, reorderable.
- The time zone.
- The device's name.
- The SIP account and the contact list — the reason the page is worth building at all, since a SIP
  password on a five-inch screen is the worst typing in this project.
- Wi-Fi, eventually, though provisioning over Bluetooth is the better answer for a device that has no
  network yet (this page needs one to be reachable).

Never, whatever the request says:

- **SSH keys.** They arrive from Home Assistant and nowhere else. Unchanged.
- **The Home Assistant encryption key.**
- **Anything that runs a command**, uploads a file that gets executed, or writes outside the settings
  the page lists.
- **Firmware.** Updates keep coming from signed releases, checked against the manifest, as they do
  now. A web page on the LAN must never be a way to put code on the device.

Every write is logged as what changed, never with a secret's value.

## The shape of it

- **One page, served from the binary.** No framework, no fonts or scripts fetched from the internet,
  nothing that stops working when the device has no route out. The whole thing should be a few
  kilobytes of HTML.
- **Forms that post and reload.** It should work in a browser with JavaScript turned off. Polling for
  the press is the one place a little script earns its keep, with a "I pressed it" button as the
  fallback.
- **Plain HTTP on the LAN.** A device with no name has nothing to put a certificate on, and a
  self-signed one teaches people to click through warnings. This is stated in the documentation
  rather than implied away: anybody who can see your network traffic can see what you type on this
  page, including a SIP password.
- **Same-origin only.** The session cookie is `SameSite=Strict`, every write is a POST carrying a
  token from the page it came from, and a request without one is refused — so a page on another site
  cannot make your browser change your device.
- **Small and bounded.** A request body has a low limit, the favorites list has a maximum, and a URL
  is checked for being an `http`/`https` URL before it is stored.

## What it must do when things go wrong

- **A bad value never breaks the device.** Settings are validated before they are written, and a
  write that fails says which field and why, on the page.
- **A radio URL that does not play** is the device's problem to report, not the page's to guess:
  saving is not testing, and the page says so rather than implying a working station.
- **Too many attempts** to authorize are refused for a while, and the refusal is logged.
- **A restart forgets sessions.** That is a feature, not a limitation worth fixing.

## Open questions

1. **Does the setup switch need to be in Home Assistant at all?** It is genuinely useful for a Dot
   across the house. It is also a way to open the page from an automation, which is exactly what the
   press is meant to prevent — the press still gates entry, so it may be fine.
2. **How long should a session last** when the page is being used for a long job, like typing in a
   dozen radio stations? Five minutes idle may be too short and rude.
3. **Should the page show the current values of anything sensitive?** Showing a SIP username is
   convenient; showing it to whoever is on the network is a choice. The password is never shown.
4. **Does a Dot get the hold-to-open shortcut?** It is the only way in on a device with no screen,
   and it is also a button anyone can hold.
5. **Where does a fresh Dot with no network stand?** The page needs a network; Bluetooth
   provisioning is the answer, and it is a separate piece of work.
