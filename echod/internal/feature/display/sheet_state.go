//go:build !dot

package display

import (
	"fmt"
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// sheetVolumeSteps is how many steps the Volume row counts.
const sheetVolumeSteps = media.VolumeSteps

// restartWindow is how long a second tap on Restart (or Delete, on an alarm, or Forget) is honoured
// after the first.
const restartWindow = 4 * time.Second

// sheetCtl is where the settings screen is, kept by the Show's and the Spot's Display under their mu:
// the open category, a list of choices open over it, how far each is scrolled, and the editors.
type sheetCtl struct {
	cat                    category
	picker                 string // the row whose list of choices is open, or empty
	cardScroll, pickScroll int    // how far the card and an open list are scrolled, in pixels

	// openedBy is where the swipe that opened the screen started, so its last notches are ignored;
	// dragFrom and dragScroll are a finger dragging the page, on a screen that follows fingers.
	openedBy   image.Point
	dragFrom   int
	dragScroll int
	dragging   bool

	// checking is an update check asked for from the screen, still out; colors the custom colors
	// editor open on the Display card; folder the slideshow's folder list, as far as it has been
	// opened; draft the alarm open in the Alarms card's editor.
	checking bool
	colors   bool
	folder   folderView
	draft    *alarmDraft
}

// settings is what the settings screen shows, gathered by the display each frame.
type settings struct {
	cat         category
	picker      string // the row whose list of choices is open, or empty
	cardScroll  int    // how far the card is scrolled, in pixels
	pickScroll  int    // how far an open list is scrolled
	brightness  int    // ceiling, percent
	auto        bool
	muted       bool
	wakeWord    string
	weather     string // the weather source's name
	volume      int    // step out of media.VolumeSteps
	night       string
	wifi        string
	wifiName    string // the network joined, or what the Wi-Fi is doing
	wifiOK      bool   // Wi-Fi is managed here, so it can be changed
	btProxy     bool
	checking    bool       // an update check from the screen is out
	colors      bool       // the custom colors editor is open
	demo        bool       // placeholders for the owner's details, for published screenshots
	folder      folderView // the slideshow folder list, while it is open
	name        string
	version     string
	slot        string
	address     string
	sendspin    bool
	insecureTLS bool
	restartArm  time.Time // set after a first tap on Restart
	forgetArm   time.Time // set after a first tap on Forget, where there is one
	now         time.Time
}

// sheetView is everything a settings card is made from: the gathered settings and the features'
// states it shows.
type sheetView struct {
	st       settings
	security security.State
	bt       btaudio.State
	alarms   alarm.View
	draft    *alarmDraft
	snooze   int
	now      time.Time
	radio    home.Radio

	// timers are what is counting down, the device's own and Home Assistant's alike, soonest first.
	timers []timer.Countdown
}

// alarmDraft is an alarm being set in the Alarms card's editor: a new one, or a copy of one being changed.
type alarmDraft struct {
	alarm     config.Alarm
	isNew     bool
	deleteArm time.Time // the first of the two taps Delete wants
}

// EditNewAlarm opens the alarm editor on a new alarm at the next whole hour.
func (d *Display) EditNewAlarm() {
	next := time.Now().Add(time.Hour)
	d.mu.Lock()
	d.draft = &alarmDraft{isNew: true, alarm: config.Alarm{Hour: next.Hour(), Minute: 0, Days: config.DaysOnce, On: true}}
	d.cat, d.picker, d.cardScroll = catAlarms, "", 0
	d.mu.Unlock()
	d.wake()
}

// alarmRows is the list the Alarms card shows, in order: the device's alarms, the helpers followed, and
// the row that adds one.
type alarmRow struct {
	snoozed  *alarm.Upcoming
	local    *config.Alarm
	followed *alarm.Followed
	add      bool
}

func alarmRows(v alarm.View) []alarmRow {
	var rows []alarmRow
	for i := range v.Snoozed {
		rows = append(rows, alarmRow{snoozed: &v.Snoozed[i]})
	}
	for i := range v.Local {
		rows = append(rows, alarmRow{local: &v.Local[i]})
	}
	for i := range v.Followed {
		rows = append(rows, alarmRow{followed: &v.Followed[i]})
	}
	return append(rows, alarmRow{add: true})
}

// repeats are the choices the Repeat row walks through.
var repeats = []uint8{config.DaysOnce, config.DaysEvery, config.DaysWeekdays, config.DaysWeekends}

// nightPresets are the Screen off at night list's choices.
var nightPresets = []string{"", "22-6", "23-6", "0-7", "21-7", "23-8"}

// nightWindow reads a night setting, "22-6": the hour the screen goes dark and the hour it comes back.
func nightWindow(v string) (from, to int, ok bool) {
	if _, err := fmt.Sscanf(v, "%d-%d", &from, &to); err != nil || from < 0 || from > 23 || to < 0 || to > 23 || from == to {
		return 0, 0, false
	}
	return from, to, true
}

// clockTime is an hour and minute the way the clock shows the time.
func clockTime(hour, minute int) string {
	return clockText(time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC))
}

// cmpOr is a, or b when a is empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
