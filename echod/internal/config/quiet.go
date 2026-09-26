package config

import "time"

// Quiet hours: the device makes no sound of its own between these hours.
//
// What it silences is what the device starts by itself — an announcement from another device in the
// house, the tone it makes when it hears its name, and the noises it makes about itself when
// something fails or a turn is dropped. What it does not silence is anything somebody asked for: an
// alarm, a timer, a phone ringing, a reply to a question, or anything Home Assistant was told to
// say. A quiet hour that swallowed an alarm would be a broken alarm clock, and one that swallowed a
// doorbell announcement would be worse.
//
// Nor does it silence an answer to somebody standing at the device — the mute button's tone, the
// volume beep, a pairing chime. That is a person being told what they just did, not the device
// talking to a room that is trying to sleep.
//
// Nothing visual goes with it: the ring still shows the effect it would have shown, and a failure
// still flashes. A quiet house is never a device that says nothing happened.

// QuietHours is the window, as "22-7" — from ten at night to seven in the morning — or "22:30-06:45",
// or empty for never. The same shape as the screen's night hours, and usually the same hours.
func (s Speaker) Quiet(now time.Time) bool { return InWindow(s.QuietHours, now) }

// Quiet is whether the device should keep to itself now.
func Quiet() bool { return Get().Speaker.Quiet(time.Now()) }
