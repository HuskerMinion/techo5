package alarm

import (
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Event is fired on Home Assistant's bus at each step of an alarm, for an automation to start the day
// with: the radio, the blinds, a light. Its event field is
//
//   - ringing: an alarm went off and is ringing
//   - silent: a silent alarm went off; it makes no sound and has nothing to stop, so this is all of it
//   - light_only: a light-only alarm's time came; its light has been up, the night has ended, and
//     there is nothing to stop
//   - snoozed: a ringing alarm was put off; it rings again later, with another ringing
//   - stopped: a ringing alarm was stopped, by a press, by voice, from Home Assistant, or by running
//     its course
//
// with the alarm's id and label, the time it was due, and the device it went off on: every device's
// events arrive on the one bus under one name. A ring's (ringing, snoozed, stopped) also says what it
// wakes with (wake_with: config.WakeWith's String). Reminders are not alarms and fire nothing here.
const Event = "esphome.techo5_alarm"

// fireEvent puts one step of an alarm on the bus. key is the alarm's, with a snooze's prefix taken off
// so that an alarm and its snoozes share an id.
func fireEvent(event, key, label string, due time.Time) {
	data := map[string]string{
		"event":  event,
		"id":     strings.TrimPrefix(key, "snooze:"),
		"label":  label,
		"due":    due.Format(time.RFC3339),
		"device": config.Get().Device.Name,
	}
	// What a ring wakes with, as chosen (#651): it says so whether or not the music came, for an
	// automation that starts the day with it.
	if event == "ringing" || event == "snoozed" || event == "stopped" {
		data["wake_with"] = wakeOf(key).String()
	}
	component.Fire.Emit(component.Event{Name: Event, Data: data})
}

// setFromHA is SetOn with the alarm made the kind the action asking for it says: setting the same
// alarm again with another of the actions changes which kind it is.
func (a *Alarms) setFromHA(hour, minute int, days uint8, label, date string, k alarmKind) (config.Alarm, error) {
	return a.setOn(hour, minute, days, label, date, &k)
}

// endNight ends the screen's night at a light-only alarm's time, as the Night mode switch turned off
// does: it holds until the night hours next start, or, with the night left to Home Assistant, until
// Home Assistant turns it on again. The screen listens to Changed, so it wakes to the day and says
// so on the switch at once, as the switch's own command does.
func (a *Alarms) endNight(now time.Time) {
	if err := config.Set().Screen().NightOverride("off", now.Unix()); err != nil {
		slog.Warn("ending the night for a light-only alarm failed", "err", err)
		return
	}
	slog.Info("night ended by a light-only alarm")
	a.Changed.Emit(struct{}{})
}
