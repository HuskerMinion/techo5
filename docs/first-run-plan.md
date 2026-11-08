# First run: a device that sets itself up

A plan, not built. The goal: TECHO5 goes onto a device, the device goes to someone else, and they plug
it in and are walked through the rest, the way a new Echo is. Wi-Fi is the bare minimum. Everything
after it is there to make the device right for that home without a computer or the installer.

## What there is today

- **Shows and the Spot** can join a network from their own screen.
- **The setup page** (setup-page-design.md) sets the time zone and Wi-Fi, opened from Privacy on the
  screen, from Home Assistant, or by holding a Dot's action button. A press on the device lets one
  browser in. It needs the device on a network already.
- **A Dot** gets its first network from the installer over USB, or over Bluetooth with Improv (#355,
  merged 2026-11-08; it advertises through the Bluetooth proxy's radio, since the 3.18 kernels cannot
  through bluetoothd).
- **Home Assistant** is assumed from the start. The installer writes an encryption key to the
  computer it ran on, and whoever adds the device needs that key.
- **Nothing runs on first boot.** The device comes up as if it were already set up.

## The flow

One routine, run on first boot and again after a reset. It can be left at any step and picked up
where it stopped; each step that is done stays done.

1. **Wi-Fi.**
   - Show and Spot: the network list on the screen, a keyboard for the password, and a check that it
     joined before going on.
   - Dot and Echo 2nd gen: Improv from a phone (Home Assistant's app finds it, or improv-wifi.com in
     Chrome). The ring says where it is: a slow color while it waits, a spin while it joins, green
     when it is on.
2. **Time zone and language.** A guess from the network it joined, shown to confirm or change.
3. **Name and room.** A short list of rooms to pick from, or typed.
4. **Home Assistant, or on its own.**
   - Home Assistant: the device shows its own encryption key (on the screen, or on the setup page
     for a Dot), so nobody needs the file from the installer. It waits for Home Assistant to add it
     and says when that happened.
   - On its own: alarms, timers, radio and the clock, with no key. Home Assistant can still be added
     later from the setup page (standalone-plan.md).
5. **A quick check.** A short sound at a sensible volume ("can you hear this?"), then "say the wake
   word" with a pass or a retry, so a bad speaker or microphone shows up now and not a week later.
6. **Updates.** Look for the latest release and fetch it in the background (see Decided), and turn automatic updates on, since the person it
   was given to will not be watching the release page.
7. **Done.** What to say to it, how to open setup again, and how to reset it.

## The Dot without a screen

The flow is the same, said instead of shown. The Dot has no speech of its own today, so the prompts
are clips in the image, one per step and per language: "Open the Home Assistant app on your phone to
give me your Wi-Fi", "I'm on your Wi-Fi", "Say the wake word". The action button answers yes, a long
press goes back a step, and the ring shows which step it is on.

## On the installer's side

A **gift install**: `--for-someone-else` (Dot, Spot and Show installers alike) skips the Wi-Fi and the
key, leaves no network saved, and marks the device for the first-run routine. A device installed the
usual way skips the routine, as it does today.

A **reset** from the device (a long hold on a Dot's buttons, a page on a Show) clears the network,
name and key and runs the routine again, for passing a device on a second time.

## Order

1. Improv for the Dot (#355), with a path that works on the 3.18 kernels. Done, merged 2026-11-08.
2. The routine on the Shows and the Spot, which have most of the pieces: the Wi-Fi page, the time
   zone, the setup page. New: the steps joined up, the key on screen, the check, and the done screen.
3. The gift install and the reset.
4. The Dot's spoken prompts.

## Built so far

- **Improv for the Dot** (#355): merged 2026-11-08. Tested on a Dot 2nd gen, last on a start with no
  network (offered by itself, joined, DHCP started, IPv4), and on a Dot 3rd gen with dot-v1.0.58 the same
  way (2026-11-08). Not yet tried on an Echo 2nd gen.
- **The routine, on every device** (feature/firstrun holds the steps, where they are saved, and what
  they do; each face draws them): Wi-Fi, time zone, name and room (the room as Home Assistant's
  suggested area), Home Assistant (added with nothing typed, or the key with a QR code, or on its
  own), updates (automatic updates on; an update installs that night), a sound check and the done
  card. Faces: a Show's screen (tried on a Show 8 2nd gen), the Spot's round screen, a Panel's screen
  (no Wi-Fi or Updates step), and the setup page, which is the Dot's and works on any device. In
  English only so far.
- **One done flag for every device**, set by finishing first run anywhere. A device in use before
  first run (a Show whose welcome card was put away, or one Home Assistant has had) is done from the
  start. Overnight automatic updates wait until first run is done.
- **The Dot's ring** breathes orange while first run is to do and nothing is connected, and a short
  press of the action button then opens the setup page. No spoken prompts: the Dot has no speech of
  its own yet, so its steps are the setup page's.
- **The gift install, device side**: the installers' `--for-someone-else` leaves a `firstrun` marker
  in the state directory, no network and no key. The device starts first run from the beginning,
  whatever its settings say, on the Wi-Fi step when no network is saved, and any Home Assistant can
  add it with nothing typed until one does. The marker goes when first run finishes.
- **Reset for someone else**: the setup page, Settings → General on a screen, Home Assistant's
  `reset_for_someone_else` action, and holding action and volume down for 10 seconds on a Dot (back
  since Improv is in). What it clears is listed in setup.md. A gift install's first start wipes what
  an earlier owner left, as a reset does.
- **The one-time code**: the setup page takes `/setup?code=...`, a code the daemon mints (setup's
  `MintCode`), good for one browser for 10 minutes while first run is to do. Improv (#355) is to hand
  it to the phone; nothing mints one until then.

## Decided

- **A Dot hands over its key through Improv.** Once it has joined the network, Improv's result is an
  address the phone opens: the setup page, with a one-time code in it, good for first run only and
  for about ten minutes. Whoever gave the Dot its Wi-Fi was in Bluetooth range moments before, which
  is the trust a press on the device gives, so this first visit needs no press. The page shows the
  key with a copy button, and the name and room. After first run the page opens the usual way, with
  a press, and can show the key again. A gift install can also print a card with a QR code for the
  key and the page, for the box.
- **The update is installed after setup.** Step 6 looks for one and downloads it in the background;
  it goes on at the next quiet time once setup is done, and the done step says so ("I'll update
  myself tonight" on a Dot). Setup never reboots the device.
- **No setup hotspot for now; the Dot uses Improv.** A hotspot (an open "TECHO5-Setup" network with
  the setup page behind it, the way a new Echo sets up) was built and tried on a Dot. The Dot's Wi-Fi
  driver starts the access point, but the whole device freezes as soon as a phone joins it, and the
  watchdog restarts it about 30 seconds later. That happened with home Wi-Fi on beside it and with it
  fully off. The kernel logs nothing, so the freeze is in the driver. The code is kept on a local
  branch (`hotspot`) in case another device's driver takes it.
