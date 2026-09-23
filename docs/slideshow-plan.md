# Idle photo slideshow — plan

Show and Spot only — the Dot has no screen, so it gets none of this: no `slideshow_mode` select, no
`home_slideshow` action, no background fetch loop (gated by a `hasScreen` build-time constant,
`screen.go`/`screen_dot.go`, the same pattern `hardware/camera`'s `Available()` already uses for
"does this device have a camera").

## Goal

While idle (no conversation, no camera view, no alarm/timer ringing, no touch), the screen shows
photos from a personal library instead of just the clock, the way a photo frame does. It steps back
to the clock the moment anything happens — the same teardown the camera view already uses.

## Photo source: configure several, pick one at a time

Realistically someone has one library they actually use, not several they want blended together.
So the design is: Home Assistant can have more than one backend set up (nothing stops it), but the
Device-tab "Slideshow source" select on each device still picks exactly **one** album/folder at a
time — the same shape as `home_weather`'s single "Weather source" select. The device doesn't know
or care which backend the current pick came from, since it only ever asks Home Assistant "what's
next" and fetches whatever URL comes back; that part is unaffected by how many backends exist.

- **Immich** has an official Home Assistant integration that exposes albums as a media source
  (`media-source://immich/...`) — the turnkey path, since Home Assistant already knows how to
  browse and resolve it, with real album/favorite/video metadata behind it.
- **A network drive** (Synology Photos' shared folder, a NAS, any SMB/NFS/mapped share) has no
  Home Assistant integration of its own, but doesn't need one: mount it into Home Assistant and add
  it as a `local` media source, and it shows up in the browse API as plain folders — this is the
  general answer to "I have photos on a network drive I could share." The tradeoff against Immich
  is metadata: a folder share has no albums, no favorites, no video/Live-Photo distinction beyond
  the file extension, no faces — just whatever's in the folder, in file order or shuffled. Fine for
  "point it at a folder of already-curated photos"; not equivalent to Immich for filtering.

Still needs a decision from the user: one shared source across all devices or a different one per
device, and how the source list should be organized if both Immich and a network share are set up
(one combined list, or picked per-backend then per-album).

## Other things a photo-frame feature usually needs

Common to slideshow/photo-frame implementations generally, worth deciding now rather than
retrofitting later:

- **Orientation and cropping.** Photos come in whatever aspect ratio they were shot in; the Show's
  screen is a fixed landscape rectangle and the Spot's is round. Decide once: letterbox (show the
  whole photo, bars on the sides) or crop-to-fill (always full-bleed, some content lost) — probably
  crop-to-fill on the Spot's round screen since letterboxing a circle looks worse than on the Show.
- **EXIF rotation.** Phone photos are frequently stored sideways with an EXIF orientation tag;
  decoding without applying it shows people on their side.
- **Order:** shuffle vs chronological, and whether it resumes from the same place after a restart
  or reshuffles.
- **Interval** between photos (a plain number, like the idle timeout).
- **Filtering:** exclude videos/Live Photos (decode as a still frame, or skip), exclude
  screenshots, exclude favorited/hidden/deleted state — Immich and most photo libraries expose
  these as separate media-source nodes or metadata; worth checking what's actually reachable
  through Home Assistant's browse API before assuming a filter is available.
- **Skip broken/unreachable images** silently and move to the next one — a library of any size will
  have at least one file that fails to decode or a network hiccup mid-slideshow; this shouldn't
  kick the device back to the clock or log noise on every occurrence.
- **Display mode**, configurable rather than fixed (see the dedicated section below): photos as a
  background behind the normal home page, or a full screensaver with the clock/date at a chosen
  size, or off.
- **Night brightness** — the Show already dims the screen from its light sensor
  (`hardware/ambient`) and the Spot presumably has the same auto-brightness path; the slideshow
  should just ride that existing behavior rather than inventing its own day/night schedule.
- **Privacy/exclusion:** an album or set of people to always leave out, independent of whatever
  album is chosen — relevant since this repo is public and screenshots/photos of the device in use
  could end up shared; this is a "don't accidentally cycle through the wrong album" safeguard for
  the user, not a repo concern.

## Display mode

Two genuinely different looks were asked for, not one on/off toggle:

- **Background** — the normal home page (clock, date, weather, timers) stays exactly as it is
  today, always in front; the photo just replaces the flat background it's currently drawn over.
  This maps directly onto how the renderer already works: `render.go`'s `draw()` starts every frame
  by filling the whole screen with a flat color (`walnut`) before drawing whatever page applies —
  in this mode, the idle/default page's fill would be the current slideshow photo instead of that
  flat color. No idle timeout applies here; it's just what the home page looks like, all the time,
  the same as it's always on when the screen itself is on.
- **Screensaver** — a full-screen photo takes over after the idle timeout, the same teardown rule
  as the camera view (any tap, wake word, or other page ends it immediately). The clock/date
  overlay on top of it is a separate, three-way choice:
  - **Off** — photo only, nothing overlaid.
  - **Small** — the time alone, small, top right — this is `cornerClock`, which already exists and
    is already used while a reply lingers on screen, so it's a direct reuse, not new drawing code.
    (Earlier draft of this doc said "top center" with a date — that was wrong; `cornerClock` is
    time-only, top-right, and M2 kept it exactly as it already was rather than changing it.)
  - **Normal** — full-size clock and date, centered — this is `bigClock`, the same layout the
    ordinary idle page already uses, just drawn over the photo instead of the flat background.
  Weather and timers stay off the screensaver regardless of overlay size; those belong to the
  Background mode / normal home page, not the photo-frame look.

Both modes and the idle timeout are Device-tab settings, off by default like everything else new.

## Design (mirrors existing patterns)

The device never talks to Immich, Synology, or anything else directly — only to Home Assistant,
through the same long-lived token already used for weather and the camera proxy. This matches
`feature/home/camera.go` (fetches Home Assistant's camera snapshots) and `feature/home/radar.go`
(fetches and caches RainViewer tiles): a `home.Slideshow` component would poll Home Assistant for
"what's the current/next photo", fetch the bytes over HTTP, decode, and scale to the panel — no
local caching to disk, same as camera and radar.

- **Home Assistant surface, per device:** a `select` for the source/album (same pattern as
  `home_weather`'s "Weather source" select), a `select` for display mode (Off / Background /
  Screensaver), a `select` for the screensaver overlay (Off / Small / Normal), and an idle-timeout
  number — added next to the existing Device-tab settings.
- **Fetch loop:** `home.Slideshow`, built like `home/camera.go`'s `CameraView` — asks Home
  Assistant for the next resolved media URL, fetches, decodes, and hands a scaled `*image.RGBA` to
  the display, regardless of which display mode is active.
- **Display side:** in Background mode, `render.go`'s `draw()` swaps its opening flat-color fill
  for the current photo on the idle/default branch only (camera, weather, settings, calls, etc.
  keep their own full-screen pages exactly as now). In Screensaver mode, a new render path
  (`render_slideshow.go`, next to `render_camera.go`) takes the whole screen after the idle
  timeout — full photo, then `cornerClock`, `bigClock`, or nothing drawn on top depending on the
  overlay setting — torn down immediately by a tap, wake word, or any other page taking over, the
  same rule `CameraView` already follows for `cameraShow`.
- **Spot:** round-screen variant, same as `display_spot.go`/`render_spot.go` do for its other pages
  — do this after the Show path is proven, not in parallel.

## Progress

**M3 done 2026-09-17** (Spot's round screen, both modes): the Spot has its own, separate rendering
system (`roundRenderer`/`roundScene` in `render_spot.go`, not the Show's `renderer`/`scene`) — so
this needed its own files (`render_slideshow_spot.go`, `spot`-tagged) and its own idle tracking
(`display_spot.go`'s `slideshowIdleSince`, mirroring `display.go`'s `boring` condition against the
Spot's own set of "something else is showing" flags: call, ringing, volume, camera, now-playing,
menu open). One real fix needed along the way: `cropToFill` was cropping every device's photo to
`artW`/`artH` (960×480, the Show's landscape panel) regardless of device, which on the Spot would
crop to landscape *then* get cropped again to the round 480×480 panel at draw time — cropping
twice, off-center. Added device-specific `slideshowW`/`slideshowH` constants (`panel_cronos.go`
960×480, `panel_spot.go` 480×480, the same per-device-file pattern `cameraFrameW`/`H` already uses)
so the crop happens once, to the panel's actual shape. The screensaver's Normal/Small overlays
reuse `clockFace`'s existing time+date lines via a new shared `screensaverClock` helper (stripped of
weather/timers/status label, same as the Show's `timeAndDate` extraction did for `bigClock`) rather
than duplicating layout code. Live-tested all four combinations (Background; Screensaver
Normal/Small/Off) on Kitchen via the same bind-mount/revert process — all rendered correctly,
including a properly-centered square crop-to-fill on the round panel. Fully reverted after.

**Live end-to-end test 2026-09-17**: deployed the built binary as a temporary test daemon on
one of the Office Shows (bind-mount over `/usr/local/bin/techo5`, gone at reboot), pointed it at a
wallpaper folder on the network share, and confirmed Background mode
renders correctly on real hardware via the screen web endpoint: the photo full-bleed behind the
clock, cropped to fill, with the wash for legibility, exactly as designed. Fully reverted after
(`umount -l` + `killall techo5`; MD5 back to the original release binary, entity gone from Home
Assistant again).

**Cross-fade transition, added same session**: `slideshowEvery` is now 1 minute (was 5); a new
photo cross-fades in over the last one (`slideshowFade`, 900ms, plain per-byte lerp between two
identically-cropped, fully-opaque buffers — `crossfade` in `slideshow.go`), with the display
redrawing at `home.SlideshowFrame` (80ms) instead of the usual once-a-second idle pace while a fade
is in progress (`display.go`'s pacing, gated by the new `home.Feature.SlideshowTransitioning()`).
Verified live that photos now rotate on the 1-minute cadence (caught a bee-wallpaper → flamingo
change during a screenshot burst); did not manage to catch a mid-fade frame in the burst (the fade
is short and screenshot polling landed on either side of it), so the blend itself is verified by
code inspection (straightforward linear interpolation) rather than by a caught frame. Multiple
distinct transition *styles* (wipe, slide, etc.) were considered and deliberately not built — a
plain cross-fade already gives visual variety since it's always a different photo, and several
animation styles would be a much bigger lift on this renderer for a background feature.

**Network share confirmed live 2026-09-17**: checked Home Assistant's media browser directly —
the user's network photo share is now mounted and browsable at
`media-source://media_source/photos/<album>`, one folder per album, each browsing down to
individually playable images. This is exactly the interface `home.Slideshow` already knows how to
consume (`Browse` → `ResolveMedia` → `FetchURL`), so this half of M0 is done. **Immich: the user
has decided not to set it up** ("I don't really know what Immich is") — it stays documented above
as an option for other TECHO5 users, not something this household needs. The network-share backend
is the only one this household will actually use.

**M1 done 2026-09-17** (Show only, Background mode): `home.Slideshow` (`echod/internal/feature/home/slideshow.go`) browses a configured media source, resolves and fetches one photo at a time (`hass.Client.Browse`/`ResolveMedia`/`FetchURL`, the last two new), crops it to fill the panel (`cropToFill`, the same rule `meta.go`'s cover-art fetch already uses), and skips broken entries up to `slideshowRetries` per advance. `render_slideshow.go` draws it under the ordinary idle clock with the same walnut wash `render_nowplaying.go` uses over cover art, for legibility — reusing an existing, already-proven technique rather than inventing a new one. A `select` (`slideshow_mode`, Off/Background) and a `home_slideshow(source)` action are wired in; `config.Home.Slideshow` persists both. Builds clean cross-compiled for Show, Dot and Spot (`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build`, all three `-tags`); new `hass` tests (`TestResolveMedia`) pass. Not yet run on Windows' native `go test`: `home`'s test binary fails to build on Windows for a pre-existing, unrelated reason (`hardware/camera` uses raw Unix syscalls with no Linux build tag) — confirmed via `git stash` that this predates the slideshow work and isn't something to fix as part of it.

**M2 done 2026-09-17** (Screensaver mode, Show only): the mode select is now a real three-way
choice (`Off`/`Background`/`Screensaver`, `config.SlideshowScreensaver` added), plus a new overlay
select (`slideshow_screensaver_overlay`, `Off`/`Small`/`Normal`, defaulting to `Normal`) and an
idle-wait number (`slideshow_screensaver_idle`, 1–60 minutes, default 5,
`slideshowIdleDefault`/`SlideshowIdleTimeout()`). `display.go` tracks how long the screen has held
the plain idle page continuously (a `boring` condition mirroring every early-return `render.go`'s
`draw()` already checks — call, ring, bt pairing, wifi, sheet, camera, radar, weather, now-playing
— so Screensaver only arms in the exact same moment `bigClock` alone would otherwise show); once
that holds past the idle wait, `render_slideshow.go`'s new `slideshowScreensaverPage` takes the
whole screen, with the same wash technique Background mode uses. Extracted a shared
`timeAndDate(now, base, dateSuffix)` out of `bigClock` so the screensaver's Normal overlay reuses
the exact same clock layout without also pulling in weather/timers/alarm, matching the "photo-frame
look, not the ordinary idle page" design decision. `Small` overlay reuses `cornerClock` exactly as
it already was — corrected an earlier draft of this doc that wrongly described it as top-center
with a date; it's time-only, top-right.

Live-tested all three overlay states on an Office Show (same bind-mount/revert process as M1): the
photo source used for M1's test (a wallpaper folder) no longer existed — **the user's network
share had been reorganized in between test sessions** (flat named albums → year-numbered folders);
not a code issue, just picked a fresh folder (`2015/Camera Roll`) once found to actually contain
playable images rather than just sub-folders. Normal, Small and Off overlays all confirmed
rendering correctly; the test photo appeared sideways, which is the already-known EXIF-rotation gap,
not a new bug. Fully reverted after.

## Milestones

- **M0** — get Immich and at least one network share reachable as Home Assistant media sources,
  and confirm each actually resolves to a stable, fetchable URL through Home Assistant's REST API
  with the existing token; decide with the user how source lists should be organized, and default
  interval/order/filtering.
- ~~**M1** — `home.Slideshow` (fetch loop, skip-on-error) plus Background mode on the Show only.~~
  Done 2026-09-17, live-tested on real hardware — see Progress above. The 1-minute interval and a
  cross-fade transition are done too (also above). Orientation (EXIF rotation), a *configurable*
  interval (currently a fixed constant, not a Device-tab setting — that's M4) and shuffle are still
  plain TODOs inside the code, deferred rather than blocking this slice.
- ~~**M2** — Screensaver mode on the Show.~~ Done and live-tested 2026-09-17 — see Progress above.
- ~~**M3** — Spot's round-screen variant (crop-to-fill) for both modes.~~ Done and live-tested
  2026-09-17 on Kitchen — see Progress above.
- **M4** — mode select, overlay select and idle-wait number are done (M2, above). **Done
  2026-09-18 (Show v0.7.2):** a folder picker on the Show's settings screen (Display → Photo
  folder: tap through Home Assistant's media folders, Use this folder at each level); subfolders
  included by default, gathered breadth first over one websocket (`hass.Client.BrowseTree`,
  within 1000 folders and 20000 photos, again hourly), shuffled by default, both switchable on the
  screen and in Home Assistant (`slideshow_subfolders`, `slideshow_shuffle`); EXIF orientation
  honored, so portrait photos stand up. **Done 2026-09-20:** how long a photo stays up is a setting
  too - Time per photo on the settings screen (15 seconds to an hour) and `slideshow_interval` in
  Home Assistant, which takes any number of seconds from 5 to 3600; `slideshow_folder` names the
  chosen folder there, since it is picked on the screen. Also that day: three looks in a row that
  find nothing and the device waits fifteen minutes and says why on the screen instead of asking
  every thirty seconds for ever. (The folder picker was never Show-only: `sheet_photos.go` is built for
  both screens, so the Spot has had Photo folder and now Time per photo too - an earlier note here said
  otherwise and was wrong.) Still open: leaving folders out of a whole-library pick (a library can hold
  scanned paperwork), user docs.

## Home Assistant changes needed (for the Home Assistant session)

1. Confirm the Immich integration is installed and pointed at the library; note the album's
   `media-source://immich/...` identifier(s) to hand to TECHO5.
2. Mount whatever network share holds the photos (Synology Photos' shared folder, or any other
   NAS/SMB/NFS share) into Home Assistant and add it as a `local` media source (no first-party
   integration exists for a plain share); report back the resulting media-source path(s).
3. Check what Home Assistant's browse API actually exposes for each backend — specifically whether
   videos/Live Photos, screenshots, and favorited/hidden state show up as filterable for Immich
   (a plain network share won't have this metadata at all), since that determines what TECHO5 can
   actually offer as a filter.
4. Build whatever helper hands TECHO5 the list of source choices per device — one list if sources
   should be picked from a single combined list, or separate per-backend lists if not (the existing
   `home_radio` favorites use a single `input_select` the same way).
5. No automations required — this is media-source and helper configuration, not automation logic.
