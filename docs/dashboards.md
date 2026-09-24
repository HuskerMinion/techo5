# Home Assistant dashboards on the screen

The Echo Show and the Echo Spot can show a Home Assistant dashboard, as a page you open or in place
of the clock. There are two ways to do it, and they are for different things. Pick the one that
matches what you want before you set anything up.

| | **Drawn on the device** | **Streamed** |
|---|---|---|
| What you see | Your dashboard's content, drawn by the device in its own style, in your Home Assistant theme's colors | Your dashboard exactly as Home Assistant draws it |
| Custom cards, card-mod, button-card's JavaScript | Not drawn: a placeholder tile says which card it is | All of them |
| Taps | Instant | About a fifth of a second |
| Scrolling | Smooth | Soft while moving, sharp when it stops |
| Needs | Nothing else | A small server, dashcast, running next to Home Assistant |
| Built-in pages (Energy, History, Logbook…) | No | Yes |

**Drawn is not a copy of your dashboard.** It reads the dashboard's cards and draws the kinds it
knows. For drawn mode, build a dashboard for the screen out of those cards. Don't expect it to look
like a dashboard built for a browser. If you want your dashboard exactly as you made it, use
Streamed.

## Turning it on

Everything is on the device's page in Home Assistant: **Settings → Devices & services → Devices →**
your device. In the **Configuration** card (expand it if it ends in "+ N entities not shown"):

- **Dashboard**: *Off*, *Drawn on the device* or *Streamed*.
- **Dashboard to show**: which dashboard, from a list of yours and their views. The list updates by
  itself within five minutes of a dashboard being added, renamed or removed. *Automatic* is the Rooms
  dashboard when drawn, and your default dashboard when streamed. Home Assistant's built-in pages are
  at the end, marked *(streamed only)*.
- **Dashboard when idle**: shows the dashboard instead of the clock whenever nothing else is on the
  screen.

Each device has its own settings, so the kitchen and a bedroom can show different dashboards.

## Using it

**Echo Show**

- Swipe in from the **left edge** of the clock to open the dashboard. The same swipe, or saying
  "go home", takes it away. When the dashboard is the idle page, that brings the clock up for two
  minutes.
- The screen's own edges still work over it: down from the **top** is the settings, in from the
  **right** the drawer. A finger that starts on a tile always goes to the tile, even at an edge.

**Echo Spot**

- Hold a finger on the screen for the ring menu, and pick **Dashboard**. Pick it again (it then says
  *Clock*) to go back.
- On the dashboard, a finger held still and then lifted brings the ring menu up.

**Drawn dashboards**

- **Tap** a tile or a row to do what it says: lights, switches and fans toggle, covers open or close,
  players play or pause, scenes and scripts run, a card's own tap action does what it is set to.
- **Slide** a finger along a light, cover or thermostat to set its brightness, position or
  temperature. The tile fills as you slide, and it is set when you let go.
- **Drag** up and down to scroll.

**Streamed dashboards** work as the page itself does: tap, and drag to scroll.

## What drawn mode draws

| Home Assistant card | On the device |
|---|---|
| Tile, button, entity, light, thermostat, and the Mushroom entity cards | A tile: icon, name, state, tap; a slider for lights, covers and thermostats |
| Entities | A card of rows, with a switch for anything on or off |
| Glance, Mushroom chips | Tiles |
| Heading, Mushroom title | A heading |
| Markdown | A card of text (formatting reduced to plain words) |
| Mushroom template | A tile, with its templates rendered by Home Assistant |
| Sensor, history graph, statistics graph, mini-graph-card, apexcharts-card | A graph of the recent history |
| Gauge | A gauge, with its severity colors |
| Picture entity (a camera), picture glance, picture | The picture; a camera updates every few seconds |
| Grid, vertical and horizontal stack, layout-card | Their cards, in place |
| Conditional, and any card's or section's *visibility* | Hidden when its conditions are not met, as Home Assistant would, including screen-width rules |
| Anything else | A tile naming the card, saying it is shown when streamed |

Sections are laid out in columns, as Home Assistant lays out a sections view: two on an Echo Show,
one on the Spot. A dashboard Home Assistant generates by itself (the default *Overview*, until you
take control of it) is drawn as the Rooms dashboard: every area, with its lights, switches,
thermostats, covers, players, locks, doors and windows.

A change you save in Home Assistant's dashboard editor shows on the device within a second or two.

### A dashboard for the screen

The simplest way to get a good drawn dashboard is to make one for it: in Home Assistant, **Settings
→ Dashboards → Add dashboard → New dashboard from scratch**. Use a sections view, and the cards
from the table above. Then pick it in **Dashboard to show**. A card's visibility can hide it on
narrow screens, so one dashboard can show more on a phone than on the Show.

## Setting up Streamed

Streamed needs **dashcast**, a small server that runs a headless Chrome next to Home Assistant. It
opens the dashboard at the device's screen size and sends the device only what changes, as pictures.
It sends where the screen was touched back, and replays it on the page. The connection is encrypted
with the key you give both ends, and dashcast only ever shows dashboards: Settings and the other
admin pages are refused. See
[`dashcast/README.md`](../dashcast/README.md) to run it with Docker.

Then tell the device where it is, either:

- on the device's **setup page**, **Connections** tab, **Dashboard server**: the address
  (`host:9555`) and the key; or
- with the action **`esphome.<node>_dashboard_server`**, `address` and `key`.

…and set **Dashboard** to **Streamed**.

The streamed page is your real dashboard, with everything on it. The device's demo mode for
screenshots does not cover it.
