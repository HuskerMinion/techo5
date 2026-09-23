# Stopping a ring: a plan

What happens when an alarm or a timer goes off, and why people say it is hard to stop.

Written 2026-09-22 from a read-only survey of the code. Every claim below carries a file reference;
where something is inferred rather than read, it says so. Nothing here has been built yet.

## The complaint

Setting alarms and timers is fine. **Stopping one while it is ringing is not.** The report from
users is that saying "stop" works sometimes and not others, and that the touch target is awkward.
Voice is the interface these devices are meant to have, so a stop word that works most of the time
is the same as a stop word that cannot be trusted.

This is not a perception problem. Each half of it is a specific defect.

## What is true today

### The ring itself

Two near-duplicate engines, `feature/alarm/alarm.go:307-336` and `feature/timer/timer.go:393-422`,
each with its own goroutine and cancel. Every stop path has to call both. That is how Home Assistant
ended up able to stop an alarm but not a timer.

A ring chimes for 2 s every 2 s (`ringEvery`), at a synthesis level of 0.6, for at most 15 minutes
(`ringFor`), then gives up. It plays through `Interject`
(`hardware/speaker/driver.go:133`), which mixes into the player queue **without taking a claim**.
That is deliberate - a ring can never wedge the speaker - and it is also why nothing can turn it
down.

Loudness is the ALSA mixer step, so **volume 0 or a muted media player makes a ring completely
silent** while the screen and the LED carry on (`feature/media/player.go:555-579`). There is no
floor.

### Why "stop" is unreliable

`feature/detect/detect.go:60-73` lowers the wake threshold by `playingSlack` (0.10) while the echo
canceller is running, with a comment explaining exactly why:

> While the speaker plays and the canceller runs, what reaches the detector is the residual of the
> music plus the voice, and the word scores lower than it does in a quiet room.

**The stop word never gets that slack.** `if slot == StopSlot { return config.Get().Wake.Stop.Threshold }`
returns before the adjustment. So the one word whose whole job is to interrupt a sound is the only
word judged with no allowance for the sound. Default stop threshold is 0.7
(`config/wake.go:25`).

### Why ducking does not save it

`feature/detect/nearmiss.go` already solves this problem for music, and the reasoning is measured
rather than guessed (from a Dot, `techo5-dot docs/microphones.md`):

> over playback the wake word stops firing once the echo at the microphones passes about -60 dBFS,
> and no filter recovers it - what the canceller leaves behind is the speaker's own distortion,
> which the reference it subtracts cannot contain. Ducking as the score rises was the obvious answer
> and does not work: the score passes NearMiss only 120-140 ms before it would fire, by which time
> the word is over.

So the trigger is the **near miss**: somebody said it, the device almost heard it, they are about to
say it again, and the music drops for 4 s (`nearMissDuck`), with a 6 s cool-off.

It does nothing during a ring, for two structural reasons:

1. It ducks `Backgrounds()` only (`nearmiss.go:66`). The chime is not a background producer.
2. Its `playing()` test is `Backgrounds().Playing() != nil` (`nearmiss.go:64`), so with a ring and no
   music it **declines to duck at all**.

The first two lines of the ring loop are `sound.Backgrounds().Duck(true)` - the alarm ducks the radio
so it can be heard. Nothing ever ducks the alarm.

### The other stop paths, for completeness

- **Touch, Show**: live region is `y >= 310` only; the top 65% of the screen, including the clock and
  the label, is dead (`feature/display/render.go:167-195`, `feature/display/alarms.go:51-57`). A
  press held longer than `tapHold` (500 ms) emits **no gesture at all**
  (`hardware/touch/touch.go:350-378`), so a deliberate half-asleep press does nothing.
- **Touch, Spot**: the whole face is Stop, which is the right design - but a 450 ms press emits
  `Hold`, which `ringGesture` refuses, and control falls through to opening the ring menu underneath
  the ring face (`feature/display/display_spot.go:519-532`). If the panel goes dark after the ring
  starts, the first tap is spent lighting it.
- **Buttons**: **no button stops a ring on a Show or a Spot.** The Dot's action button is the only
  physical stop on any device, and it is swallowed while the setup page holds the press
  (`feature/voice/voice.go:76-81`) or during a call (`feature/phone/phone.go:158-168`).
- **Home Assistant**: `button.alarm_stop` and `button.alarm_snooze`. **There is no way to stop a
  timer from Home Assistant** - `feature/timer/timer.go:164` exposes one text sensor and no actions.
- **Muting the microphone** tears down every wake-word backend including the stop model
  (`feature/detect/engine.go:463-478`), so a muted device cannot be stopped by voice at all.

### States with no way out

1. **Ringing timer during a phone call, any device.** The call page takes every tap, the stop word is
   suppressed for the whole call (`feature/detect/detect.go:77-80`), and Home Assistant has no timer
   stop. Fifteen minutes, guaranteed.
2. **Show with Home Assistant never connected.** `feature/display/display.go:1056` gates leaving the
   splash on HA's voice pipeline subscribing, with no timeout, so the ringing page is never drawn.
   The tap band still works, but nothing on screen says where.
3. **Show whose framebuffer failed to open.** `d.r == nil` makes the tap bail, and a Show has no
   button.
4. **Dot with the microphone muted and the setup page holding the press.**

## Principles

1. **Silence is the urgent act; the decision can wait.** What somebody wants at 6am is quiet, not a
   choice between Stop and Snooze. Stop the noise on the first input, then offer the choice.
2. **A stop path may not depend on anything that can be down.** No network, no Home Assistant, no
   voice pipeline. Anything that needs those is a convenience, not a stop path.
3. **An alarm may not fail silently.** Every other failure is recoverable; this one is the whole
   purpose of the feature.
4. **Persist the moment, not the countdown.** An absolute finish time survives a restart and a power
   cut; "27 minutes left" does not.
5. **A dropped ring must leave a trace.** Better to say "your timer finished at 14:03" than to
   vanish.

## The work, in order

R1 and R2 are built, along with decision 3's header. R3 onwards is still a plan.

### R1 - Make the stop word as easy to hear as every other word - BUILT

One line: let `StopSlot` take `playingSlack` the way the other slots do, while the canceller runs.
Nothing else in this plan is as cheap or as likely to matter.

Test: a unit test on `Threshold(StopSlot)` with the canceller on and off.

Built as `thresholdFor` in `feature/detect/detect.go`, pulled out of the closure so it is testable
without the singletons the closure reaches for. `threshold_test.go` pins the stop word taking the
same drop as slot 0, the floor, and the other slots keeping their own thresholds.

### R2 - Duck the ring for the stop word - BUILT

Give the ring loop a duck state it reads each time round, and drop or skip the chime for ~3 s when
it is set. Trigger it from a near miss **on the stop slot** - the engine already reports near misses
per slot (`feature/detect/engine.go:70-73`), and today that signal is thrown away.

Also make the ducker willing to act during a ring: its `playing()` test has to count a ring as
something worth ducking, or it declines before it starts.

The screen and the LED carry on through the quiet window, so the alarm stays obviously alive.

Test: the ducker is deliberately built with the speaker and the clock injectable
(`feature/detect/nearmiss.go:49-56`) precisely so this is testable with no hardware. Follow that.

Built as a package, `feature/ring`, rather than a flag copied into both engines - R7 blames that
duplication for the Home Assistant gap, so this is a first piece of the unification rather than a
third copy. It holds two things:

- `Sounding` / `IsSounding`, a **count** and not a flag, so an alarm and a timer going off together
  do not have the first to finish report silence.
- `Hush` / `Hushed`, a **deadline** and not an on/off. A hush nobody lifts expires by itself, so no
  bug can leave an alarm permanently silent - which for this feature is the failure that matters.

The ducker hushes on a near miss on the stop slot only, and **not** gated on `Media.DuckOnNearMiss`:
that setting is about music getting out of the way, where this is about an alarm being stoppable at
all. Rate limited to once per `3 x ring.HushFor`, so a room that keeps nearly saying it can quiet at
most a third of a ringing alarm.

### R3a - The header, from decision 3 - BUILT

`header` in `feature/display/render.go`, deferred from the first line of `draw` so it runs last and
lands on every page, including the ones that return early - the ringing page, the call page, pairing,
wifi, setup, the camera and the radar, none of which the footer ever reached.

Two things the pixels said that reading the code did not:

- The claim that no page draws above the margin is **false**. The weather page's art and the settings
  sheet's card both reach the top edge, so a band across the panel cuts a slot through them. It is a
  pill behind the words instead, which takes only the space the words need and is the background
  colour on a plain page.
- The settings sheet is skipped on purpose. It already has a Microphone row reading "Muted" or
  "Listening" with the switch beside it, which says it better than a line would, and a pill there
  would cut the top off its card.

Test: `header_test.go` compares the top strip muted against unmuted on every page - it must differ on
all of them, and must **not** differ on the settings sheet. `SHOW_PREVIEW=<dir> go test -run
TestShowScenesDraw` writes the pages as PNGs; the muted scenes were added to that set, and looking at
them is what caught both points above.

### R3 - A second word: snooze

The stop word is a single always-loaded model, not language. "Snooze" can be another one, on the
same mechanism, with no network and no Home Assistant. That gives the two things somebody actually
needs at 6am, both working with everything else down.

Decision needed: which words. "Stop" and "snooze" are the obvious pair.

### R4 - Any button stops a ring

First press of volume-up, volume-down or mute silences a ring; it does not change the volume or the
microphone that time. Every device family has all three buttons, so this is the only local stop that
works with no screen and no microphone.

Decision needed: does the first press **stop** or **snooze**? Stopping surprises somebody who
reached over to turn a loud alarm down; snoozing means an ignored alarm comes back. Suggested: it
silences and offers "Snooze 9 min?" for a few seconds, which separates the urgent act from the
decision (principle 1).

### R5 - The touch targets

- Show: make the whole screen a stop target the way the Spot's already is, and accept `Hold` and
  `Release` as stops on both. Today a long press on a Show emits nothing at all.
- Spot: take `Hold` in `ringGesture` rather than letting it fall through to the menu, and relight the
  panel for a ring rather than spending the first tap on it.

### R6 - Survive a disruption

- Persist timers and snoozes as absolute finish times. Alarms already persist; timers and snoozes are
  memory-only and a restart loses both.
- **Do not auto-resume a ring on start-up.** A crash loop would become a device that screams every
  time it boots. Resume the alarm, not the ring: if the finish time is more than a minute or two
  past, show what was missed instead of ringing.
- Fix the snooze that a forward clock jump drops silently (`feature/alarm/schedule.go:43-53`, the
  `stale` window) - it should leave a trace, not vanish.

### R7 - The remaining holes

- A timer stop for Home Assistant, or fold timers into `alarm_stop`. Consider unifying the two ring
  engines; the duplication is what let this gap exist.
- Let a ring be stopped from the call page, and stop suppressing the stop word for a ringing timer
  during a call.
- Do not let the boot splash gate the ringing page.

## Decisions

Settled 2026-09-23, except the fourth.

1. **A ring has its own level, not the media volume.** A ring no longer inherits the media volume, so
   leaving music muted can never make an alarm silent, and there is a separate explicit and visible
   ring mute for somebody who really wants one. Not a floor on the media volume: a floor overrides a
   deliberate mute, where an own level means the deliberate mute was never the alarm's to begin with.
   Applies to alarms and timers both - a timer nobody can hear is the same failure.
2. **A button press silences, then offers a snooze.** Per principle 1: the first press stops the
   noise and offers "Snooze N min?" for a few seconds; ignoring the offer leaves it stopped, because
   a snooze nobody confirmed is the surprising outcome. **N is configurable** - in settings and on
   the offer itself - rather than fixed at 9: five minutes and fifteen are both wanted.
3. **Muting the microphone keeps cutting the microphone, and the screen says so.** The mute is a
   promise and stays whole; what is missing is that nothing tells anybody. On a Show the
   `microphone off` line **moves from the footer to a header**, so it is drawn on every page rather
   than only the two the footer reaches — the ringing page among them, which said nothing at all
   about mute. The Dot is left as it is: the mute button's own LED lights from the mute line itself,
   which is the right signal and is not echod's to change.

   Not quiet hours, which already exist (`config/quiet.go`) and already exempt a ring on purpose:
   the setting says so to the user in as many words, "Alarms and timers always sound". Worth knowing
   while reading this plan: **"quiet" means four unrelated things in this tree** - quiet hours, the
   microphone cut, the media volume at zero, and `Engine.Quiet` tearing the wake backends down
   because the microphone was cut.
4. **Are "reminders" wanted as a concept?** Still open, and being looked at elsewhere. There is none
   in the code today. An alarm with a label already is one, and a third ring engine would be a third
   thing to stop - so if they are wanted they should land on one unified engine (R7), not beside two.

## What this plan is not

Not a change to how alarms and timers are set - that part works. Not richer voice phrasing ("snooze
the alarm for ten minutes"): that needs the wake word, the network, Home Assistant and an intent, so
it can never be more reliable than the pipeline. It is worth having, on top of R1-R4, never instead.
