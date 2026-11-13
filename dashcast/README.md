# dashcast

Shows Home Assistant dashboards on TECHO5 screens, exactly as Home Assistant draws them, custom cards
and themes included.

An Echo Show is too small to run Home Assistant's frontend itself. dashcast runs a headless Chrome
on a machine that can, opens the dashboard at the screen's size, and sends the screen only the parts
that change, as pictures, over a connection encrypted with a key you choose. Where the screen is touched goes back and is replayed on the page. While
a page scrolls, the pictures go at half size, and the full-size ones follow once it stops.

TECHO5 can also draw a dashboard itself, with no server at all. That is quicker to the touch but
knows only the common cards. dashcast is for a dashboard that looks the way you made it.

## Running it

It needs:

- a long-lived access token from a **Home Assistant user made for this, who is not an
  administrator** (see [Security](#security)). Make the user in **Settings → People → Users → Add
  user**, with **Administrator** off. Sign in as it once, then make the token in its profile,
  **Security**, **Long-lived access tokens**. The dashboards are shown as that user sees them.
- a key of your own choosing, which each device has to present. Any long random string will do.

```sh
cat > .env <<'END'
HA_TOKEN=your-long-lived-token
DASHCAST_KEY=a-long-random-string
END
docker compose up -d
```

Set `HA_URL` in `docker-compose.yml` to Home Assistant's address as seen from this machine.

About 150 to 250 MB of memory per screen showing a dashboard, and very little CPU once a page has
loaded: nothing is sent while nothing changes.

## Pointing a device at it

Tell the device where the server is and what its key is, either on the device's **setup page**
(**Connections** tab, **Dashboard server**), or with the action **ESPHome: `<device>`_dashboard_server**.

Then, on the device's page in Home Assistant (**Settings → Devices & services → Devices →** the
device, **Configuration** card):

1. Set **Dashboard** to **Streamed**.
2. Pick the dashboard in **Dashboard to show**. Home Assistant's built-in pages (Energy, History,
   Logbook…) are at the end of the list.
3. Turn on **Dashboard when idle** to show it in place of the clock.

On an Echo Show, swipe in from the left edge of the clock to open it; on the Spot, pick Dashboard
in the ring menu. More in [docs/dashboards.md](../docs/dashboards.md).

Tapping a text field on the page brings up the Show's own keyboard: dashcast tells the device when a
field gets focus, and types what comes back into it as a keyboard would. It needs the device's
firmware from the same release or newer; an older one shows no keyboard and is otherwise unchanged.
A password field's text is never sent to the device.

## Security

The browser dashcast runs is signed in to Home Assistant with the token, and a device drives it by
touch. So treat the token and the key with care:

- **Use a user that is not an administrator.** dashcast only shows dashboards: it refuses any other
  page a device asks for (Settings, Developer Tools, add-ons, the profile), and brings the page back
  to its dashboard if it gets anywhere else. A user without administrator rights is the second wall
  behind that: Home Assistant itself will not show that user those pages, whatever happens.
- **Keep the port on your own network.** Anyone who can reach it and knows the key can see the
  dashboards and use them. The compose file can bind it to this machine's own LAN address only (for
  example `"192.168.1.20:9555:9555"`). **Never forward the port from your router.**
- **The connection is encrypted, with the key.** A device and dashcast prove to each other that
  they have the same key without sending it (a Noise handshake, as the device's own link to Home
  Assistant uses), and everything after that, pictures and touches, is encrypted. A device with the
  wrong key simply cannot connect.
- **Custom cards run with the token.** A custom card's code runs in dashcast's browser, as that
  user, exactly as it would in yours. Install custom cards you trust, as you would anyway.
- **The key.** At least 16 characters, and random: `openssl rand -base64 24` is one way. dashcast
  will not start with a shorter one.
- **Dashboards only.** Webpage dashboards are not shown, because they are often a tool (a code
  editor, the ESPHome dashboard) that anyone with the key could then use. Neither are Settings, the
  add-ons or Developer Tools.
- **Up to 8 screens at once.** Each is a browser tab of a couple of hundred megabytes; a ninth is
  told the server is full.

## Web pages

Besides dashboards, dashcast can show web pages you choose: a status page, a photo site, an art
feed, a page of your own. List them in `DASHCAST_PAGES`, by name:

```yaml
      DASHCAST_PAGES: >-
        Status=https://status.example.lan/board,
        Art=https://art.example.com/today|refresh=30m
```

Each is `name=address`, separated by commas or new lines, so write a comma in an address as `%2C`
and a `|` as `%7C`. `|refresh=10m` after an address reloads the page that often (30 seconds at the
least). Write a host with letters outside ASCII in its `xn--` form. The names, and only the names, are
what a device sees: it offers each as **Web: name** in **Dashboard to show**, and asks dashcast for it
by that name. Show one as the screensaver with **Dashboard when idle**, or open it like a dashboard.

List the address a page ends up at. One that sends the browser on to another site - `http://` to
`https://`, a bare domain to its `www.`, a sign-in page somewhere else - is refused as it leaves, and
the device says where it was going: list that address instead.

How they're kept apart:

- **Only the pages you list.** A device asks by name, never by address, so the key opens nothing
  you didn't put in the list.
- **No Home Assistant in them.** Each page opens in a browser context of its own: no token, no
  cookies and no storage shared with the dashboards.
- **Kept on its own site.** A link, a form or a script that takes the page to another site is refused
  before it goes, and a page that gets there some other way is taken back; a window it opens is closed
  as it appears; nothing it offers to download is saved. A frame inside the page can still show
  another site, as frames do.
- **Its scripts run on this machine.** A page's own code runs in dashcast's browser and can reach
  whatever this machine can on your network. List pages you trust, as you would open them in your own
  browser.
- **The log names the site only.** dashcast logs a page's address as its site alone,
  `https://status.example.lan`: a page's key is often in its path or query.

## Settings

| Variable       | Default          | What it is                                              |
| -------------- | ---------------- | ------------------------------------------------------- |
| `HA_URL`       |                  | Home Assistant's address, as this machine reaches it    |
| `HA_TOKEN`     |                  | the long-lived access token the browser signs in with   |
| `DASHCAST_KEY` |                  | what a device has to present                            |
| `LISTEN`       | `:9555`          | where devices connect                                   |
| `CHROME`       | found for itself | the browser to run                                      |
| `DASHCAST_PAGES` |                | web pages a device may be shown ([Web pages](#web-pages)) |
