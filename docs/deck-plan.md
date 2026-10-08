# TECHO5 Deck plan

A Show as a stream deck: a grid of touch buttons that switch OBS scenes, start and stop a stream,
press keyboard shortcuts on a computer, open apps and run scripts. The presses go straight to OBS
or the computer, not through Home Assistant, so they feel instant and work without it.

**Status (2026-10-05):** design agreed with the user. Step 1 (the deck page and OBS) built and tested on a Show. Steps 2 and 3 (the agent for Windows, macOS and Linux) built on branch `deck`: it lives in `echod/cmd/techo5-deck`, beside the daemon, so both ends share one protocol package (`internal/lib/deckwire`); a console window for now, the tray later. Decided: the name is TECHO5 Deck
(`techo5-deck` for files and the program); it opens with a swipe up from the bottom edge; the agent
lives in this repository; the Show only (no Spot for now, maybe later).

**Where the build differs from this plan** (2026-10-05): `deck_press` takes page and button
numbers, not a name; the agent lives in `echod/cmd/techo5-deck` (one protocol package for both
ends) and is a console program for now, with no tray and no self-update yet; macOS ships two files
(Intel, Apple silicon) and presses keys through osascript, Linux through uinput (X11 and Wayland
alike), not CGEvent or XTest; the deck closes with a swipe down and has no time of its own to go back
to the clock, but steps aside for the camera, the settings, alerts and pages asked for by voice, and
closes on "go home" and a dashboard Home Assistant asks for.

## What is already there

- **Drawn screens.** The daemon already draws dashboards itself, with tiles in Normal, Large or
  Fill the screen sizes (`feature/dashboard/drawn*.go`, `tiles_show.go`). A deck page is the same
  kind of grid with a different job.
- **Edges already taken on the clock.** Left edge in: the dashboard. Right edge in: the drawer. Top edge
  down: the settings sheet. A swipe up or down anywhere else is the volume (`display.go`). The top
  edge is the model for the bottom one: a swipe that starts in the strip along the edge does the edge's
  job, and anywhere else it is still the volume.
- **Encryption with a shared key.** The dashcast stream uses Noise NNpsk0 keyed by a shared secret
  (`feature/dashboard/secure.go`), the same family as the link to Home Assistant. The computer
  agent uses the same code.
- **Finding things on the network.** Announcing already does mDNS (`feature/announce/peers.go`).
- **Settings in three places.** Every setting already lives in `state.json` and shows on the screen's
  settings, the setup page and Home Assistant.

## What a user gets

1. **OBS with nothing to install.** OBS 28 and later has a WebSocket server built in (Tools →
   WebSocket Server Settings). Give the Show its address and password and the deck's OBS buttons
   work: switch scene, start or stop the stream, start or stop recording, mute or unmute a source,
   show or hide a source. The buttons show what OBS is doing: the live scene lit, the stream button
   red while live, a muted source crossed out.
2. **Shortcuts and apps with one small program on the computer.** The **TECHO5 Deck agent**
   (`techo5-deck`) is one file for Windows, macOS or Linux, with no installer and no runtime. Run it, pair it with the Show,
   and the deck can press shortcuts (Ctrl+Shift+M), media keys (play/pause, next, volume), type text,
   open a website or file, launch an app, and run the scripts the agent's own list allows.
3. **Home Assistant is optional.** A button can still call a Home Assistant script, for lights in
   the same grid, but nothing in the deck needs Home Assistant.

## How it works

### The deck on the Show

- **A page of buttons**, drawn by the daemon: 3×2 up to 5×3 on a Show 5 (960×480), up to 6×4 on a
  Show 8. Each button has a label, an icon (the Material Design icons the dashboards already use) and
  a color. Several pages, with dots at the bottom and a swipe between them, like a Stream Deck's
  folders.
- **Opening it:** a swipe **up from the bottom edge** of the clock, the top edge's swipe mirrored. It
  only counts when it starts in the strip along the bottom edge (90 of the Show 5's 480 rows, thinner than the top edge's band so the volume keeps most of the screen), and
  only once a deck is set up: a Show without one keeps swipe up as volume up everywhere. A swipe down,
  or the back gesture the dashboard uses, puts it away. Tap on the clock gains **Deck** beside Assist,
  Dashboard and Nothing. The deck stays up until it's put away, or returns to the clock after a time
  of its own (Never by default; a deck is usually left showing).
- **Not on the Spot** for now, and never on the Dot.
- **Pressing:** a press shows at once (the button dims), the action goes out, and the button flashes
  red briefly if it failed (OBS not running, agent asleep). No sound by default.
- **Feedback:** buttons that mirror state (OBS scene, streaming, recording, mute) are updated from
  OBS's own events, not by polling.

### OBS (no install)

The daemon keeps one connection to OBS's WebSocket (protocol v5, port 4455 by default) while the
deck is set up:
- It logs in with OBS's challenge (SHA-256 of the password with OBS's salt and challenge), so the
  password never crosses the network.
- It subscribes to scene, stream, record and input-mute events.
- It sends `SetCurrentProgramScene`, `ToggleStream`, `ToggleRecord`, `ToggleInputMute` and
  `SetSceneItemEnabled`.

It reconnects on its own after OBS restarts. The setup page lists OBS's scenes and sources when you
make a button, so nobody types scene names.

OBS's WebSocket isn't encrypted. The login is safe, but scene names and commands are readable on the
network. That's acceptable on a home network; the docs will say so.

### The computer agent

One Go program, built for Windows (amd64, arm64), macOS (universal) and Linux (amd64, arm64).

- **Pairing:** on first run the agent makes a key and shows it: a tray icon on Windows and macOS,
  the terminal on Linux. On the Show's setup page, **Deck → Computers** lists the agents it finds
  on the network (mDNS `_techo5-deck._tcp`). Pick one and type its key. The agent remembers that
  Show; a second Show pairs the same way.
- **The connection** is Noise NNpsk0 with that key: the dashcast code, a new prologue
  (`techo5-deck/1`). Wrong key: the handshake fails, and nothing is said. The agent listens on the
  local network only and refuses public addresses.
- **What the Show may ask for:**
  - **Key presses and media keys**, as the Show sends them.
  - **Typing text.**
  - **Opening a URL, file or app.** Apps by name from a list the agent sends (Start menu entries,
    `/Applications`, `.desktop` files); URLs only http and https.
  - **Running a script, only by name, from the agent's own list.** The list is a small file next to
    the agent, which only someone at the computer can edit. A paired Show can't run a command the
    computer's owner didn't put there. This is the one rule that keeps a lost or hacked Show from
    becoming a way into the computer.
- **Per OS:**
  - **Windows:** `SendInput` for keys; nothing to approve.
  - **macOS:** CGEvent for keys, which needs Accessibility permission once (System Settings → Privacy
    & Security → Accessibility). The agent tells you when it's missing.
  - **Linux:** XTest on X11. On Wayland, key presses need the `uinput` device (the user in the
    `input` group). The docs will say so, and the agent says which it found.
- **Start at login:** a tray option on Windows and macOS, and a systemd user unit on Linux.
- **Updates:** the agent checks this repository's releases and installs only a build signed with the
  release key, like the devices do. Not automatic: it asks.

### Settings and storage

- `state.json` gets a `deck` section:
  - pages and buttons: label, icon, color, action;
  - OBS address and password;
  - paired agents: name, address and key.
- The setup page gets a **Deck** section in Screen & Photos:
  - a grid editor: tap a square, choose an action, then pick from OBS's scenes or the agent's app
    and script lists;
  - OBS settings;
  - Computers.
- Home Assistant gets only what makes sense there: **Tap on the clock** gains Deck, and an
  `esphome.<device>_deck_press` action presses a button by name, so an automation can trigger one.
  The grid itself is edited on the setup page.
- The OBS password and agent keys show in diagnostics only as "set".

## What is left out, on purpose

- **Streaming the computer's screen or app windows onto the Show.** The fork does this. It's a
  different, much bigger job (video encoding, audio, input mapping) and isn't what a deck needs.
  Dashcast already streams web pages for anyone who wants that.
- **Arbitrary commands from the Show.** See the agent's script list above.
- **The Spot.** Not now; maybe later as a 2×2 deck on its round screen.
- **The Dot:** no screen.

## Steps

1. **The deck page and OBS.** Drawn grid, pages, press feedback, the bottom-edge swipe and Tap on the
   clock → Deck, the OBS client with events, the setup page editor with OBS's scenes. Useful on its own for anyone who
   streams. Tested against OBS on Windows and macOS.
2. **The agent, Windows first:** pairing, Noise, keys, media keys, typing, URLs, apps, the script
   list, the tray. Then the setup page's Computers list and agent actions in the editor.
3. **macOS and Linux agents**, with the permission checks and the docs.
4. **Releases for the agent:** built by CI from a tag, attested, and signed with the release key,
   like echod. It lives in this repository (`agent/`) so the protocol and both ends change together.
5. **Docs:** `docs/deck.md` with the OBS setup, the agent per OS, the script list and the security
   notes.

Rough size: step 1 is a few days, step 2 about as much again, steps 3 to 5 a few more.

## Decided (2026-10-05)

1. **Name:** TECHO5 Deck in text and on screen, `techo5-deck` for files, the program and the mDNS
   service (`_techo5-deck._tcp`).
2. **Opening:** a swipe up from the bottom edge, only once a deck is set up, so nothing else changes.
3. **The agent:** in this repository, under `agent/`.
4. **Spot:** not now; maybe later.
