//go:build spot

package display

// The Spot is not offered the next alarm on its night clock (night_alarm.go): it is the Echo Show 5's
// and Show 8's.
func nightAlarmHere() bool { return false }

func (d *Display) setNightAlarm(bool) {}
