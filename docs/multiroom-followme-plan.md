# Multi-room / follow-me audio — plan

## What already exists

Multi-room sync is already shipped, not a new feature: every TECHO5 device runs
`echod/internal/feature/sendspin`, is on by default (`config.Sendspin{Enabled: true}`), and
advertises itself over mDNS as `_sendspin._tcp` — a room waiting for a server to dial in and start
a synced stream. This is already documented as the multi-room story:
`docs/getting-started.md` — "multi-room audio through
[Music Assistant](https://www.music-assistant.io/) (each device is a Sendspin player)". Nothing
device-side is missing for plain grouping (playing the same thing, in sync, in several rooms at
once).

What's genuinely not started is **follow-me**: moving the *active* session from room to room as a
person moves through the house, without them doing anything. That's orchestration on top of the
sync that already exists, not a new sync protocol — it belongs in Home Assistant, using Music
Assistant's player-group control, not in `echod`.

## M0 — checked live 2026-09-17: more done than the plan assumed

Checked Home Assistant's states directly rather than assuming: Music Assistant is already running
and every device is already connected — `sensor.<device>_playback_sendspin_state` reads `joined`
and `switch.<device>_playback_sendspin` is `on` for all six, and there's already an
`automation.music_assistant_reconnect_sendspin`. Garage's `joined` timestamp is 17:00:41, exactly
when it rebooted for the `update-boot.py` test — it rejoined on its own, no automation needed
beyond the one that already exists. So the transport is not just "documented as available", it's
live in production right now.

What's still actually untested: every device's Music-Assistant-facing player
(`media_player.<device>_speaker_2`) shows `group_members: []` — nobody has actually put two or more
of them into one group and played something across them in sync. The connection layer works; a real
synced multi-room *group* has not been tried. That's the real remaining M0: group two or three
devices in Music Assistant (a Show, a Dot, the Spot) and listen for sync tightness, join/leave
behavior, and what a mid-session Wi-Fi drop does — cheap to do, and everything else depends on it.

**M0 group test run 2026-09-17: done, and it works.** Grouped Kitchen (Spot) and the new Living Room
(Dot) in Music Assistant and played media to the group. Living Room played audibly, in sync,
immediately. Kitchen initially seemed to produce no sound at all, which looked like a real bug —
`techo5.log` showed a completely healthy Sendspin session throughout (`sendspin group state=playing`,
FLAC decoding with `undecoded=0`, `dropped=0`, steady ~30s buffer, tiny clock corrections,
telemetry indistinguishable from Living Room's), volume commands reaching the device correctly
(`volume step=N of=30`), and the same hardware clearly capable of sound (`playing announcement
samples=N` entries succeeding for TTS/wake sounds). Added temporary diagnostics (removed again after)
confirming the render state (`ready=true held=false gain=1`) and the ALSA write loop advancing in
exact real-time lockstep — every introspectable layer was correct. **The actual cause: the Spot's
volume curve isn't tuned for its own hardware** — `hardware/speaker/paths_spot.go`'s own comment
already says so ("Not yet tuned on the Spot: the Show's curve... starts quiet rather than loud").
Step 15/30 (50%) is -24 dB there, which is close to inaudible in a normal room; at full volume
(step 30, 0 dB) the user confirmed Kitchen played clearly. So Sendspin sync/grouping itself is
confirmed working correctly on both a Dot and a Spot — **M0 is done**. The Spot's volume curve
being miscalibrated is a real, separate, lower-priority issue (also affects any other loud-enough-
to-hear-normally use of the Spot's speaker at moderate volumes, not just Sendspin) worth its own
task, not blocking follow-me.

## Adding a new device to Music Assistant isn't automatic

Living Room needed manual steps that the original five devices apparently never did (or did so long
ago it wasn't obvious): its Sendspin state stayed `waiting` — never `joined` — until the user found
it in Music Assistant's own **Settings → Players** list and added it there by hand; Home Assistant's
side (`switch.<device>_playback_sendspin` on, the integration reload) had no effect, and neither did
restarting the whole Music Assistant container on its own — the container restart was needed too,
just not sufficient by itself. Separately, its native ESPHome `media_player.living_room_speaker`
entity had `should_expose` (conversation) set to `false` by default (unlike the other devices',
which were `true`) — fixed directly via `config/entity_registry/update`. That entity still doesn't
show up as a *second*, redundant "Home Assistant Media Player" entry in Music Assistant next to its
native Sendspin one, unlike the older devices — but Music Assistant explains why when you try:
"This device is already in Music Assistant as a native player," refusing the add. That's Music
Assistant correctly deduplicating, not a bug — the other devices' double-listing is most likely a
leftover from before their Sendspin players existed, not something to replicate. So: **a new TECHO5
device needs (1) a manual add in Music Assistant's Players/Sendspin settings, not just the on-device
switch, and (2) its native media_player entity's conversation-expose flag turned on** if it should
ever appear standalone — the second part turned out to not matter once (1) was done.

## Presence signal: track the phone, not a fixed sensor

The user's preference: follow-me should go off a phone moving through the house, not a
room-by-room sensor — the mmWave `room-presence-sensor` is confirmed to be the only one in the
house, and it only covers its own desk area, so it can't carry whole-house follow-me anyway.

Phone-based room presence is realistic here because the infrastructure already exists, just not
wired together yet:

- **Bluetooth proxy coverage already exists.** Home Assistant already sees five standalone ESPHome
  Bluetooth/BLE proxies (one per room, named
  `bluetooth-proxy-<room>` and `ble-proxy-<room>`) — presumably already
  placed for exactly this kind of tracking. These relay every BLE advertisement they hear, with
  signal strength, to Home Assistant.
- **Every TECHO5 device is also a Bluetooth proxy** (`feature/bluetooth`, `esphome.BluetoothProxy`
  under the `bluetooth_proxy` switch) — the same standard ESPHome proxy the standalone ones are,
  which is a real asset here: each one sits exactly where a room's speaker is, which is exactly
  where follow-me needs a presence reading. **Current state (2026-09-18): all seven TECHO5 devices
  have it on** — Laundry Room, Garage, Bathroom, Kitchen and Living Room turned on 2026-09-17;
  the two Office Shows (initially left off) turned on 2026-09-18 per the user. Combined
  with the 5 standalone proxies, that's 12 active Bluetooth proxies covering the whole house,
  including both desks in the office.
- **What's missing:** nothing yet resolves those raw advertisements to "this is the user's phone,
  in this room." No `private_ble_device` or Bermuda-style trilateration entities exist in Home
  Assistant today (checked live — none found). That needs the phone to be BLE-discoverable in a
  way Home Assistant can key on: the Home Assistant Companion App's Bluetooth/iBeacon transmitter,
  paired with either core Home Assistant's own nearest-proxy area assignment or the Private BLE
  Device integration (which resolves a modern phone's rotating BLE MAC via its IRK, exchanged once
  from the companion app) — either gives a "nearest proxy" reading per phone, refreshed as it moves.

This is a real presence signal (the actual person's phone), not a proxy for one, and it scales to
the whole house as proxy coverage grows — unlike the mmWave sensor, which is stuck at one desk.

**Before turning on more `bluetooth_proxy` switches:** unlike the SSH switch (a five-minute
maintenance toggle), this is a standing behavior change — continuous passive scanning of every
BLE device in range, not just the user's phone. Worth confirming with the user first, room by
room, rather than flipping all five remaining switches on speculatively.

TECHO5's weak fallback, for whatever the phone/proxy approach doesn't reach: the `assist_satellite`
state (idle/listening/processing/responding) and `feature/activity`'s `last_wake_word`/`last_heard`
text sensors say "someone just spoke to this device," not "someone is in this room" — good enough
as a last resort, not a real substitute.

## Design direction

Because the connection layer already works, this is almost entirely a Home Assistant automation
design:
1. Track "which room is active" from the phone's nearest Bluetooth proxy (once Private BLE
   Device/area assignment is set up), falling back to the mmWave sensor at its desk or the
   per-device voice-activity signal where proxy coverage doesn't reach yet.
2. On a room change, use Music Assistant's group commands to move the active player group so
   playback follows — add the new room's Sendspin player to the group and drop rooms that are no
   longer occupied, rather than stopping and restarting playback.
3. Decide the policy for ambiguous cases (nobody detected anywhere, multiple rooms active at once,
   a manually-started group that shouldn't be touched) — this needs the user's input once the real
   group test below shows how group changes actually feel in practice (any audible glitch on
   join/leave, latency).

No echod changes are anticipated unless the group test surfaces a real sync problem.

## Milestones

- ~~**M0** — connection layer confirmed live, and a real synced group test, both 2026-09-17.~~ Done
  — see above. A Dot and a Spot played correctly grouped and in sync; what looked like a Spot bug
  turned out to be its uncalibrated volume curve, not a Sendspin problem. Still not tried: a Show in
  the group (never exercised either) — worth a quick check before assuming it behaves the same, but
  not blocking, since the Dot and Spot's underlying mechanism is now proven.
- ~~**M1** — set up phone presence.~~ Done for the first phone, 2026-09-18 — see above (Companion App
  BLE Transmitter + Bermuda BLE Trilateration, not Private BLE Device). All 7 TECHO5 devices now have
  `bluetooth_proxy` on (12 proxies total in the house). Still open: the same setup for the second
  phone, and calibrating Bermuda's reference power for better area accuracy.
- **M2** — Home Assistant automation: room-active tracking from the Bermuda area sensor(s) (mmWave
  sensor and TECHO5 voice-activity as fallbacks where proxy coverage is thin) + Music Assistant
  group follow; start with a couple of rooms. Not started — waiting on both phones being tracked.
- **M3** — extend to all six TECHO5 devices; tune the ambiguous-case policy from real use.

## Home Assistant changes needed (for the Home Assistant session)

1. ~~In Music Assistant, group two or three TECHO5 players and actually play something across
   them.~~ Done 2026-09-17 — see M0 above.
2. ~~Set up phone BLE presence.~~ **Working for the first phone, done 2026-09-18.** Private BLE Device
   was abandoned — both phones are Android, and getting the IRK means sniffing with Wireshark/an
   nRF Sniffer or pulling it from a bug report, not worth it. What actually worked:
   a. Companion App → Settings → **Manage Sensors** → enable **"BLE Transmitter"**. This makes the
      phone broadcast a fixed iBeacon (UUID stays constant; Android still rotates the radio-layer
      MAC underneath it, which is why the same beacon showed up under several different MACs before
      Bermuda's UUID-based grouping was found). Confirms as `sensor.<phone>_ble_transmitter =
      Transmitting`, and its `id` attribute (`<uuid>_<major>_<minor>`) is the exact string to search
      for elsewhere.
   b. Installed **Bermuda BLE Trilateration** via HACS (already installed) — this is the piece that
      actually turns "several proxies hearing a signal at different strengths" into "which room."
      Home Assistant's core Bluetooth integration doesn't do this on its own; it only handles
      specifically-supported device types.
   c. In Bermuda's **Settings → Devices & Services → Bermuda → select devices to track**, searched
      for the UUID prefix (the first eight hex digits) from the sensor's `id` attribute and found the
      entry literally labeled **"iBeacon"** — enabled that one specifically (not the raw-MAC entries
      also matching the same UUID, which are the same beacon seen under different rotated MACs and
      would need re-picking every rotation). Searching by the minor number alone was too loose and
      matched unrelated devices (a porch light) by coincidence — the UUID prefix is the reliable
      search.
   d. Result: `sensor.bermuda_<uuid>_<major>_<minor>_area` reads **"Office"** right now, plus a
      matching `device_tracker` and a distance estimate. Live, working room-level presence for the
      first phone. Distance calibration (`calibration_ref_power_at_1m`) is still at its uncalibrated
      default (0), which may make the area assignment a little rough near room boundaries until
      tuned, but the core signal works.
   e. **The second phone still needs the same "BLE Transmitter" sensor enabled in its Companion
      App**, then finding and enabling that phone's own iBeacon entry in Bermuda the same way (it'll
      share the same UUID prefix but have a different major/minor than the first phone's).
3. Write the automation that moves Music Assistant group membership on a room change (the Bermuda
   area sensor first, mmWave sensor and TECHO5 voice-activity sensors as fallbacks), rather than
   stopping/restarting playback. Not started — worth doing once the second phone is tracked too, so
   the automation handles both from the start rather than being retrofitted.
4. No TECHO5-side changes are being requested with this — flag back here if anything turns up a gap
   only the device side can fix (e.g., a device that won't rejoin a Music Assistant group cleanly).
