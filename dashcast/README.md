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

- a long-lived access token: in Home Assistant, your profile, **Security**, **Long-lived access
  tokens**. The dashboards are shown as that user sees them, so use an account that should see them.
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

Anyone who can reach the port and knows the key can see and use the dashboard as the token's user.
Keep the port on your own network, and the key to yourself.

## Settings

| Variable       | Default          | What it is                                              |
| -------------- | ---------------- | ------------------------------------------------------- |
| `HA_URL`       |                  | Home Assistant's address, as this machine reaches it    |
| `HA_TOKEN`     |                  | the long-lived access token the browser signs in with   |
| `DASHCAST_KEY` |                  | what a device has to present                            |
| `LISTEN`       | `:9555`          | where devices connect                                   |
| `CHROME`       | found for itself | the browser to run                                      |
