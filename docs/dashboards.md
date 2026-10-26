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
your device. In the **Configuration** card (expand it if it ends in "+ N entities not shown"). The
setup page's **Screen & Photos** tab has the same settings, except which dashboard to show:

- **Dashboard**: *Off*, *Drawn on the device* or *Streamed*, and on the Show, the Skylight and the Panel app *Tessera*
  ([Tessera screens](#tessera-screens-preview)).
- **Dashboard to show**: which dashboard, from a list of yours and their views. The list updates by
  itself within five minutes of a dashboard being added, renamed or removed. *Automatic* is the Rooms
  dashboard when drawn, and your default dashboard when streamed. Home Assistant's built-in pages are
  at the end, marked *(streamed only)*, and after them the web pages your dashcast server lists, as
  *Web: name* ([Web pages](#web-pages)).
- **Dashboard when idle**: shows the dashboard instead of the clock whenever nothing else is on the
  screen.
- **Dashboard returns to the clock after**: how long a dashboard you opened stays up untouched before
  the clock comes back: 30 seconds, 1, 2, 5 or 10 minutes, or *Never*. It's 10 minutes until you
  choose. A dashboard Home Assistant put up (`dashboard_show`) stays until it's hidden.
- **Dashboard tiles** (Show only, drawn only): *Normal*; *Large*, taller tiles across the whole
  width; or *Fill the screen*, which lays a view of up to nine tiles out over the whole page, like
  four lights in a 2×2 grid. A view with anything other than tiles on it is drawn *Large*.
- **Dashboard without its header** (streamed only): hides Home Assistant's top bar, which on a small
  screen takes a real slice of it. It works on your dashboards and on the built-in pages (Energy,
  History, Logbook and the rest), with nothing to install and no change to the dashboards themselves,
  so phones and tablets still see them as they were. On Energy the bar also holds its view tabs, so
  they go with it. It needs a dashcast image newer than v0.9.5; an older one shows the bar.

  If you would rather hide the bar in a dashboard's own configuration, the
  [kiosk-mode](https://github.com/NemesisRE/kiosk-mode) card (HACS) works with dashcast too:
  `kiosk_mode:` with `kiosk: true` at the top of the dashboard's YAML. That applies to everyone who
  opens that dashboard, so give the screen a copy of its own.

Each device has its own settings, so the kitchen and a bedroom can show different dashboards.

## Using it

**Echo Show**

- Swipe in from the **left edge** of the clock to open the dashboard. The same swipe, or saying
  "go home", takes it away. When the dashboard is the idle page, that brings the clock up for two
  minutes.
- Or set **Tap on the clock** to *Dashboard* (on the screen under Settings → Display, on the setup
  page, or in Home Assistant), and a tap on the clock opens it. With **Dashboard returns to the clock
  after** set to a minute, that makes a hallway panel: a tap for the dashboard, the clock back when
  you walk away, and the wake word for Assist. At night, a tap on the night light or the night clock
  opens it too, at the night light's level (to turn on a light in the dark, say), and the night
  screen comes back when it closes. With any other choice, the night screen ignores a tap.
- The screen's own edges still work over it: down from the **top** is the settings, in from the
  **right** the drawer. A finger that starts on a tile always goes to the tile, even at an edge.

**Echo Spot**

- Hold a finger on the screen for the ring menu, and pick **Dashboard**. Pick it again (it then says
  *Clock*) to go back.
- On the dashboard, a finger held still and then lifted brings the ring menu up.

**Drawn dashboards**

- **Tap** a tile or a row to do what it says: lights, switches and fans toggle, covers open or close,
  players play or pause, scenes and scripts run, a card's own tap action does what it is set to.
- **Slide** a finger along a light, cover, thermostat or media player to set its brightness,
  position, temperature or volume. The tile fills as you slide, and it is set when you let go.
- **Tap a thermostat** whose card sets no tap of its own:
  - on the Show (a long press too), for a sheet over the page: the temperature where it is, the one
    it is set to with a step down and up (the low and the high of a range in heat/cool), its modes,
    and **Done**.
  - on the Spot, which has no room for a sheet, a tap on the left third of its tile sets it a step
    cooler, and on the right third a step warmer; the middle does nothing. A thermostat that is off
    or keeps a range has no one temperature to step.
- **Long press** (Show only; on the Spot a long press is the ring menu):
  - on a light that has whites or colors, for a sheet of them. The light follows your finger.
  - on a media player, for a sheet with its controls, its volume, the speakers it can be grouped
    with, and, with Music Assistant, your favorite playlists and radio stations. The favorites need
    the device's token to belong to a Home Assistant administrator; with any other token the sheet
    says so and shows the rest.
- **Drag** up and down to scroll.

**Streamed dashboards** work as the page itself does: tap, and drag to scroll.

## What drawn mode draws

| Home Assistant card | On the device |
|---|---|
| Tile, button, entity, light, thermostat, and the Mushroom entity cards | A tile: icon, name, state, tap; a slider for lights, covers, thermostats and media players |
| Entities | A card of rows, with a switch for anything on or off |
| Glance, Mushroom chips | Tiles |
| Heading, Mushroom title | A heading |
| Markdown | A card of text (formatting reduced to plain words) |
| Mushroom template | A tile, with its templates rendered by Home Assistant |
| Sensor, history graph, statistics graph, mini-graph-card, apexcharts-card | A graph of the recent history |
| Gauge | A gauge, with its severity colors |
| Picture entity (a camera), picture glance, picture | The picture; a camera updates every few seconds. A tap does what its `tap_action` says, such as opening a room's view. The only card of a view fills the screen, the whole picture shown, kept as sharp as the screen. A picture that won't load says so on its card and is tried again in 30 seconds, then less often; an image picked in Home Assistant's media browser (`media-source://`) is fetched through Home Assistant |
| Picture elements | Its picture (`image` or `camera_image`) drawn whole, at its own shape; as the only card of a view it fills the screen. Each element is a tap where its `style` puts it, with `left` and `top` in % as the element's center, the way Home Assistant places it, and `width` and `height` in % when given. A `transform` replaces that centering, as in Home Assistant: `translate(x, y)`, `translateX(x)` and `translateY(y)` in % move it on their own axes, and a move in pixels counts as none. A tap does what the element's `tap_action` says; a service button runs its own `action` (or `service`) with its `data` and `target`. An icon shows its icon, in the style's `color` when it gives one; a state icon and a state label show their entity, the label's words on a see-through pill so they read on any picture; a service button shows its title. An image and a state badge are only their tap, the picture being what shows them. An element whose style makes it invisible (`opacity: 0`, a transparent `color` such as `transparent`, `#00000000` or `rgba(0, 0, 0, 0)`) is drawn as nothing and is still a tap; one with `display: none` or `visibility: hidden` is neither drawn nor a tap, as in Home Assistant. Conditional elements follow their conditions; custom elements, and ones placed in pixels or with `right` or `bottom` rather than `left` and `top`, are left out. A picture up to 4096 by 4096 is shown (a 16-bit PNG up to about 2900 by 2900); one larger, or one that won't load, says so, and its elements are still placed and tapped |
| Any card whose `tap_action` has a `confirmation` | Shown, but a tap does nothing: there is no way to ask first on the device, so it is not done on one tap |
| Grid, vertical and horizontal stack, layout-card | Their cards, in place. A grid of pictures with `columns` set is a gallery, up to three across, one per row in a narrow column; a grid without `columns` stays one under another here, where Home Assistant would put three across |
| Conditional, and any card's or section's *visibility* | Hidden when its conditions are not met, as Home Assistant would, including screen-width rules. A state or number condition names its entity as `entity` or `entity_id`; one that names no entity is about the card's own entity, as in Home Assistant. One that names no entity where the card has none of its own (a section, a conditional card), or a state condition with neither `state` nor `state_not`, is never met, so the card stays hidden |
| Anything else | A tile naming the card, saying it is shown when streamed |

Sections are laid out in columns, as Home Assistant lays out a sections view: two on an Echo Show,
one on the Spot. A dashboard Home Assistant generates by itself (the default *Overview*, until you
take control of it) is drawn as the Rooms dashboard: every area, with its lights, switches,
thermostats, covers, players, locks, doors and windows.

A change you save in Home Assistant's dashboard editor shows on the device within a second or two.

A tap that opens another view keeps the page you tapped on up until the new view and its
full-screen pictures are ready, for a second and a half at most. The last few pictures are kept, so
going back to a view you just left draws it at once.

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

### Web pages

dashcast can also show web pages that aren't Home Assistant's: a status page, a photo site, an art
feed, a page of your own. Whoever runs dashcast lists them by name in `DASHCAST_PAGES`
([`dashcast/README.md`](../dashcast/README.md#web-pages)), and each appears in **Dashboard to show**
as *Web: name*. Choose one with **Dashboard** set to *Streamed*:

- **As the screensaver:** turn on **Dashboard when idle**, and the page shows in place of the clock
  whenever nothing else is on the screen.
- **As a page of your own:** open it the way you open a dashboard. Touches go to the page, except at
  the screen's edges, which take it away as they do a dashboard.

A device asks for a page by its name only, and dashcast opens it apart from Home Assistant, with no
sign-in in it, and keeps it on its own site. A web page can't be drawn on the device: it needs
*Streamed*. If the screen says a page's address "goes on to" another site, list that address in
`DASHCAST_PAGES` instead.

## Tessera screens (preview)

**Preview.** Tried with the Tessera add-on on a Show 5: the layout arrives, the page draws it and taps
work. Not every kind of tile is drawn yet (see the end of this section).

[Tessera](https://github.com/MaxGramser/homeassistant_espscreen) is a Home Assistant add-on that lays
out pages of tiles in an editor and sends them to the screens it knows. The Echo Show can be one of
those screens, and so can the Skylight and a tablet with the Panel app. Not the Spot: Tessera has no
layout for its round screen.

You need the Tessera add-on installed in Home Assistant.

1. On the device's page in Home Assistant, turn on **Tessera screen** in the **Configuration** card.
   The device restarts. After that it has a second device in Home Assistant, "*name* tessera", with
   the entities Tessera looks for: **Tile settings**, **Screen firmware**, **Device name**,
   **Screen layout** and **Screen features**.
2. Open Tessera. The device is listed as a screen. Its grid is 4 by 2 on the Show 5, 4 by 3 on the
   Show 8 and 5 by 3 on the Skylight. On a tablet it follows the screen: 3 cells on the short side
   (2 below 600 pixels) and as many on the long side as fit, so 4 by 3 on a 2560x1600 tablet. A
   tablet's first start with Tessera on tells the add-on a Show 8's grid until the screen opens.
   For more or bigger tiles, pick another grid in **Tessera grid** (the device's **Configuration**
   card): it offers the grids whose cells stay at least 120 pixels each way, such as 6 by 3 or 8 by 4
   on the Show 5. *Automatic* goes back to the screen's own. Re-arrange the tiles in Tessera after a
   change.
3. Add tiles to its pages and save. **Tile settings** on the tessera device says *Synced* once the
   layout has arrived.
4. Set **Dashboard** to *Tessera*. The layout is now the dashboard page: swipe in from the left edge
   to open it, or turn on **Dashboard when idle** to have it in place of the clock.

Taps call Home Assistant actions from the device, so the device needs **Allow the device to perform
Home Assistant actions** turned on in its ESPHome integration options.

On the page:

- The top bar has the page's title, its items from Tessera, and the time.
- Swipe left or right to go to the next page. The dots along the foot show where you are. A detail
  page has a back arrow at the top left.
- Tap a tile to do what it's for: lights, switches, fans, input booleans and automations turn on or
  off; a cover opens, closes or stops; a scene or script runs; a button is pressed; a media player
  plays or pauses (on Music Assistant's player when it is the one playing, as its play/pause key does).
  Sensors show their value and unit, large.
- Tap a thermostat to open its controls: the temperature with − and +, and its modes.
- Locks: a tap on an unlocked lock locks it. A tap on a locked lock asks first: the tile turns red
  and says *Tap again to unlock*. Tap again within 4 seconds to unlock. If the lock's tile in
  Tessera is set to lock only, a tap never unlocks it.
- Garage doors, gates and doors (a cover whose device class is garage, gate or door) ask first in the
  same way: a tap on a closed one, or on its open key, turns the tile red and says *Tap again to open*.
  Tap again within 4 seconds to open it. Closing one takes a single tap.
- Press and hold a tile to open the same sheets the drawn dashboard has: a light's whites and colors,
  a thermostat's controls, a media player's controls, volume, Music Assistant favorites and speakers.
  The favorites and speakers are read with the device's Home Assistant token, as on the drawn
  dashboard, and the favorites need it to belong to an administrator.
- Slide a finger along a light to set its brightness, along a fan to set its speed, along a cover to
  set its position, or along a thermostat to set its temperature.
- A camera's tile set in Tessera to show the camera (its display `live`) shows it filling the tile, or
  all of the picture with fit `contain`, with its name along the foot over a shade (none with overlay
  `none`). It is the camera's latest snapshot, fetched with the device's Home Assistant token every few
  seconds while the page is up (the tile's refresh, where it gives one), so it needs the
  `home_assistant` action, as the drawn dashboard does. Where the camera view plays the camera live
  over RTSP (**Live camera video** on, and go2rtc's stream or a picture address on the setup page; see
  [Cameras](setup.md#4-cameras)), the tile plays it live too, at a few frames a second. Until the first
  picture comes, and for a camera tile not set to show the camera, it is an icon and a name. Tap it to
  put the camera up on its own. The device says `own_pictures` in its **Screen features**, which is
  what has the add-on offer cameras for it; an add-on that does not know the word yet offers them only
  to its own boards that draw pictures, and turns a layout with a camera down.
- A weather tile two cells wide or two rows high shows the forecast: days side by side, or one a line
  on a tile one cell wide.
- Bigger tiles show the controls Tessera gives them (the tile's **Controls** in the editor): a
  thermostat's − and + and its modes, a media player's back, play/pause and next or its volume, a
  cover's open, stop and close or its position, a light's brightness, a fan's speed. A tile two or
  more cells wide and one row high gets its first control beside its name. Press and hold still opens
  the full sheet.
- The play/pause, next and back keys, on a tile or its sheet, go to Music Assistant's player when it is the
  one playing, as on the drawn dashboard, so every grouped speaker follows.
- A tile shows what you asked for at once, then whatever Home Assistant says. Tile backgrounds from
  Tessera are drawn as tints, in the device's own colors.
- A tile too low for its icon above its words, on a grid of many rows, has its words beside the icon.
- A tap beside the tiles, where there is none, starts a voice turn while **Tap on the clock** is
  *Assist*, as a tap on the clock does.
- Two of Tessera's plugins are drawn by the device itself: **World clock** (up to three places, each
  with its dial, or its time in digits, and how far it is from here; a tap on the tile turns the dials
  to digits and back) and **Custom card** (the parts a template sensor lists in its `elements`
  attribute: values, bars, rings, rows and text). The device names them as its own when the add-on
  asks, so the add-on sends it their tiles; install the plugin in Tessera to have it in the editor. A
  tile of any other plugin says *Plugin missing*, and a Custom card's `image` part shows its number,
  as the device takes no pictures from plugins.

What the device tells Tessera it takes: every tile size its grid has room for (single, wide, tall,
square, the whole page, and spans like 3x2), and up to eight pages. Tessera leaves out everything
else. A tile Tessera sets to open its card on a tap opens the sheet, where there is one.

Not drawn yet: graphs, album art, meters, the vacuum and alarm cards, Tessera's
alerts and screensaver. The sheets' changes go to Home Assistant as the device's own actions, the
same way taps do. The layout is kept on the device, so a restart doesn't lose it.

Turn **Tessera screen** off to stop. The device restarts and no longer offers the tessera device.
If Home Assistant still lists it as unavailable, delete it from the device's page.
