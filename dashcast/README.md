# dashcast

Shows Home Assistant dashboards on TECHO5 screens, exactly as Home Assistant draws them, custom cards
and themes included.

An Echo Show is too small to run Home Assistant's frontend itself. dashcast runs a headless Chrome
on a machine that can, opens the dashboard at the screen's size, and sends the screen only the parts
that change, as pictures. Where the screen is touched goes back and is replayed on the page. While
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
- **The connection is not encrypted.** The key, the pictures and the touches cross your network as
  they are, like most things on a home network. Keep it on a network you trust.
- **Custom cards run with the token.** A custom card's code runs in dashcast's browser, as that
  user, exactly as it would in yours. Install custom cards you trust, as you would anyway.
- **The key.** Make it long and random: `openssl rand -base64 24` is one way.

## Settings

| Variable       | Default          | What it is                                              |
| -------------- | ---------------- | ------------------------------------------------------- |
| `HA_URL`       |                  | Home Assistant's address, as this machine reaches it    |
| `HA_TOKEN`     |                  | the long-lived access token the browser signs in with   |
| `DASHCAST_KEY` |                  | what a device has to present                            |
| `LISTEN`       | `:9555`          | where devices connect                                   |
| `CHROME`       | found for itself | the browser to run                                      |
