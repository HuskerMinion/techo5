# Plan: calendar, event pop-ups, home screen taps, and the night

Five changes to the screen devices, planned together because they share the home screen and its
settings. This is a plan, not a feature yet. Decided 2026-09-26: each device shows its own chosen
calendars; the Shows get a month view with events on their days; event pop-ups are off until turned on, and configurable; night hours can be set to any
time; a red night clock is one of the choices for the night.

The Show gets all of it. The Spot gets what fits its small round screen (below). The Dot has no
screen and gets none of it.

## 1. A calendar, from Home Assistant

**Where the events come from.** Home Assistant, not the device. Home Assistant already connects to
the calendars people use, and each shows up there as a `calendar.*` entity:

- **Google (Gmail):** the Google Calendar integration.
- **Apple iCloud:** the CalDAV integration, with an app-specific password from the Apple account.
- **Outlook:** Outlook can publish a calendar as a link, which the Remote Calendar integration reads.
  As far as we know there is no built-in Outlook integration; the link is read-only and refreshes on
  a delay. To be tested before the docs promise it.
- **Anything else** Home Assistant has: its own Local Calendar, other CalDAV servers, ICS links.

The device reads events through Home Assistant's calendar API (`/api/calendars/<entity>`, a start
and an end) with the access it already has (`home_assistant`). One way in for every provider, and
nobody's calendar password is on a device.

**Each device shows its own calendars.** Chosen per device from the calendars Home Assistant has: on
the screen (Settings, a Calendar card), on the setup page, and from Home Assistant (a
`calendar_sources` action taking a list of entities, like `home_cameras` does for cameras). None
chosen: no calendar page, and the date on the home screen does nothing.

**The calendar page (Show 5 and Show 8).** Two views, and a detail window:

- **Month:** the month as a grid, today marked. Each day shows its events as far as they fit: a word
  or two of the title in the calendar's color where there is room, and where there is not, a small
  mark for each event (or a count, "+3", when there are more than fit). The Show 8's bigger screen
  fits more words than the Show 5's. Swipe or arrows for the next and previous month.
- **Day list:** tapping a day opens that day's events as a list: the time, the title and the
  calendar, each calendar in its own color, all-day events at the top. The same list is the "Today"
  view that opens first when there is nothing else asked for.
- **Details:** tapping an event, in the list or on the month, opens a window with all of it: the
  title, the day and time, the calendar, the location and the description when there are any. A tap
  outside it, or Close, returns to where it was opened from.

Events refresh every few minutes while the page is up, and every 15 minutes behind the scenes so it
opens without waiting.

- **Opened by:** tapping the date on the home screen, a Calendar tab in the drawer, by voice ("what's
  on my calendar", "what do I have tomorrow") through an automation like the other voice ones, and a
  `calendar_show` action for automations.

**The Spot.** Its round screen is small, so no full page: "Today" and "Next" (the next one or two
events) on a page reached like its other pages, and the pop-ups below.

## 2. Taps on the home screen

- **The weather** opens the forecast page, the same page a weather question already brings up.
  Small: the page exists, and only the tap is new.
- **The date** opens the calendar page, once there is one. Before a calendar is chosen, a tap there
  does nothing, as now.
- Both already sit on the soft dark backing the home screen gives the weather and the date, so they
  look like something to press. Their touch areas are sized for a finger, and a tap anywhere else on
  the home screen keeps doing what it does today.

## 3. Event pop-ups

**Off until turned on**, per device. When on, an event coming up shows on the screen over whatever is
there, like a timer does: its time, title and calendar, and a tap dismisses it.

Settings, on the screen, the setup page and in Home Assistant:

- **Pop-ups:** off (default) or on.
- **Which calendars** pop up: all of the device's calendars (default when on) or a chosen few.
- **When:** at the start, or 5, 10, 15 (default when on), 30 minutes or an hour before.
- **Sound:** a chime (default when on) or silent. The chime follows the device's volume floor for
  alerts, so it is heard.
- **All-day events:** once, in the morning when the night ends (default when on), or never.
- **At night:** a pop-up during night hours shows but makes no sound and does not light a dark
  screen; it is waiting when the screen next wakes.

A pop-up stays up until tapped or until the event has started and 10 minutes have passed. Home
Assistant gets an event (`esphome.techo5_calendar`) when one shows, for anyone who wants to add to
it.

**Optional, off by default:** a "Next:" line under the date on the home screen, with the next event
of the day.

## 4. Night hours, any time

Today the night is one of six presets on whole hours (10 PM to 6 AM and so on). Somebody who goes to
bed at 7 PM, or whose household wakes at different times, cannot say so.

- **Any start and any end,** to the quarter hour: 7:00 PM to 9:30 AM, say. On the screen, the Night
  hours row opens a picker for the start and one for the end. The presets stay as quick choices at
  the top of the list, with "Custom" below them.
- **Home Assistant:** the Night hours select keeps its presets and gains "Custom", and two new
  selects, **Night starts** and **Night ends**, hold the custom times in quarter-hour steps. (The
  ESPHome library the daemon uses has no time or text entities, only selects, numbers, switches and
  buttons, so a select is the way to offer a time.) A `screen_night_hours` action takes a start and
  an end ("19:00", "09:30") for automations.
- **Stored** as `HH:MM-HH:MM`. Today's `22-6` is still read, so nothing changes on an update.
- Both the Show and the Spot.

## 5. A red night clock

A third choice for the night on the Show, beside "Screen off" and "Night light": **Red clock**. The
clock alone, large, in dim red on black, with the backlight at its lowest. Red keeps a dark room dark
and is easy on eyes adjusted to it.

- **The backlight cannot go fully off** while anything shows: the Show's screen is an LCD, and an
  LCD needs its backlight to show anything at all. Lowest backlight plus dim red on black is the
  darkest a visible clock can be. How dark that looks on each panel (Show 5, Show 8) is tested
  before it ships.
- **Its brightness** is the same Night light level setting (1 to 10) the night light uses.
- **A touch** brings the normal screen up at night brightness for a moment, as the night light does,
  then it returns to the red clock.
- On the screen (Display, "At night") and in Home Assistant (`select.<node>_screen_at_night` gains
  "Red clock").
- The Spot's night only dims today; a red clock could come to it later if wanted.

## Order of work

Small first, so each can go out in a release on its own:

1. **Night hours, any time** (4). Small, self-contained.
2. **Red night clock** (5). Small; the panel test decides how it looks.
3. **Weather tap** (2, the weather part). Very small.
4. **Calendar** (1), with the date tap. The largest: reading Home Assistant's calendars, the choice
   of calendars per device, the month view, the day list and the details window.
5. **Event pop-ups** (3), on top of the calendar.

## Open questions

- Outlook through a published link: how long Outlook and Home Assistant take to show a new event.
  Tested with a real Outlook calendar before the docs say it works.
- How dark the red clock really is on each panel.
