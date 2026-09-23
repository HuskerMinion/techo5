package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// The Alarms & Timers tab: everything on the device that goes off, each in a drawer that opens to
// change it, and a drawer that adds another. On a Dot this is the only place its alarms can be seen
// at all.

// wakeLights are the Wake with light choices, in minutes, as the screen offers them.
var wakeLights = []int{5, 10, 15, 20, 30}

func alarmsSection(w http.ResponseWriter, token string) {
	now := time.Now()
	v := alarm.Get().View(now)
	timers := timer.Get().List(now)

	fmt.Fprint(w, `<fieldset><legend>Coming up</legend>
	 <p class="note" style="margin-top:0">A reminder says its words once and stays on the screen until it is
	  dismissed; an alarm with words says them when it starts ringing.</p>`)
	shown := 0
	for _, c := range timers {
		if !c.Active {
			continue
		}
		shown++
		timerDrawer(w, token, c)
	}
	for _, a := range v.Local {
		shown++
		alarmDrawer(w, token, a)
	}
	if shown == 0 {
		fmt.Fprint(w, `<p class="note">Nothing set. Add one below, or from Home Assistant.</p>`)
	}
	if len(v.Snoozed) > 0 {
		fmt.Fprint(w, `<form method="post" action="/setup/save" class="btns">`)
		hidden(w, token, "snoozes_cancel", "alarms")
		for _, s := range v.Snoozed {
			fmt.Fprintf(w, `<span class="where">Snoozed until %s%s</span>`, clockText(s.At), labelSuffix(s.Label))
		}
		fmt.Fprint(w, `<button type="submit" class="quiet">Cancel snoozes</button></form>`)
	}
	fmt.Fprint(w, `</fieldset>`)

	addDrawer(w, token)

	if len(v.Followed) > 0 {
		fmt.Fprint(w, `<fieldset><legend>From Home Assistant</legend>
		 <p class="note" style="margin-top:0">Helpers this device follows as alarms. Change them in Home Assistant.</p>`)
		for _, f := range v.Followed {
			when := "not set"
			if !f.At.IsZero() {
				when = clockText(f.At)
			}
			state := "on"
			if !f.Armed {
				state = "off"
			}
			fmt.Fprintf(w, `<p style="margin:.3rem 0"><span class="chip">Alarm</span> <strong>%s</strong> %s <span class="where">· %s</span></p>`,
				html.EscapeString(when), html.EscapeString(cmpOr(f.Label, f.Entity)), state)
		}
		fmt.Fprint(w, `</fieldset>`)
	}

	prefsSection(w, token)
}

func timerDrawer(w http.ResponseWriter, token string, c timer.Countdown) {
	where := "set through Home Assistant"
	if c.Local {
		where = "this device"
	}
	fmt.Fprintf(w, `<details><summary><span class="chip tim">Timer</span><span class="when">%s</span><span class="what">%s</span><span class="where">left · %s</span></summary><div class="body">`,
		timeLeft(c.Left), html.EscapeString(cmpOr(c.Name, "Timer")), where)
	if c.Local {
		fmt.Fprint(w, `<form method="post" action="/setup/save" class="btns">`)
		hidden(w, token, "timer_cancel", "alarms")
		fmt.Fprintf(w, `<input type="hidden" name="id" value="%s"><button type="submit" class="quiet">Cancel timer</button></form>`,
			html.EscapeString(c.ID))
	} else {
		fmt.Fprint(w, `<p class="note">Set by voice through Home Assistant, so it is canceled the same way.</p>`)
	}
	fmt.Fprint(w, `</div></details>`)
}

func alarmDrawer(w http.ResponseWriter, token string, a config.Alarm) {
	kind, chip := "Alarm", "chip"
	if a.Remind {
		kind, chip = "Reminder", "chip rem"
	}
	where := capitalize(a.When())
	switch {
	case a.Remind && len(a.RingOn) > 0:
		where += " · here and " + strings.Join(a.RingOn, ", ")
	case !a.Remind:
		if m := config.Get().Alarms.SunriseFor(a); m > 0 {
			where += fmt.Sprintf(" · light %d min", m)
		}
	}
	cls := ""
	if !a.On {
		cls, where = ` class="off"`, where+" · off"
	}
	label := html.EscapeString(a.Label)
	if a.Label == "" {
		label = `<span class="note">(no words)</span>`
	}
	fmt.Fprintf(w, `<details%s><summary><span class="%s">%s</span><span class="when">%s</span><span class="what">%s</span><span class="where">%s</span></summary><div class="body">`,
		cls, chip, kind, clockTime(a.Hour, a.Minute), label, html.EscapeString(where))

	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "alarm_edit", "alarms")
	fmt.Fprintf(w, `<input type="hidden" name="id" value="%s">`, html.EscapeString(a.ID))
	whenFields(w, "e"+a.ID, fmt.Sprintf("%02d:%02d", a.Hour, a.Minute), a.Label, a.Remind)
	daysField(w, a.Days)
	if a.Remind {
		ringOnField(w, "r"+a.ID, strings.Join(a.RingOn, ", "))
	} else {
		sunriseField(w, "s"+a.ID, a.Sunrise)
	}
	toggle := "Turn off"
	if !a.On {
		toggle = "Turn on"
	}
	fmt.Fprintf(w, `<div class="btns"><button type="submit" name="act" value="save">Save</button>
	 <button type="submit" name="act" value="toggle" class="quiet">%s</button>
	 <button type="submit" name="act" value="delete" class="quiet">Delete</button></div></form></div></details>`, toggle)
}

func addDrawer(w http.ResponseWriter, token string) {
	fmt.Fprint(w, `<details><summary><strong style="color:var(--accent)">+ Add an alarm, timer or reminder</strong></summary><div class="body">
	 <form method="post" action="/setup/save">`)
	hidden(w, token, "alarm_add", "alarms")
	fmt.Fprint(w, `<label>What</label><div class="kinds">
	 <label><input type="radio" name="kind" value="alarm" checked>Alarm</label>
	 <label><input type="radio" name="kind" value="reminder">Reminder</label>
	 <label><input type="radio" name="kind" value="timer">Timer</label></div>
	 <div class="row"><div><label for="at">At</label><input id="at" name="at" type="time"></div>
	 <div><label for="in">…or in <span class="note">(reminder or timer)</span></label>
	 <input id="in" name="in" placeholder="20 minutes" autocomplete="off"></div></div>
	 <label for="label">Words <span class="note">(said aloud; a reminder needs them)</span></label>
	 <input id="label" name="label" maxlength="80" autocomplete="off" placeholder="Take the trash out">`)
	daysField(w, config.DaysOnce)
	ringOnField(w, "addring", "")
	sunriseField(w, "addsun", 0)
	fmt.Fprint(w, `<p class="note">Repeat days are for alarms and reminders at a time of day; "in" is once. Other
	 devices are for reminders; the wake light is for alarms.</p>
	 <div class="btns"><button type="submit">Add</button></div></form></div></details>`)
}

func prefsSection(w http.ResponseWriter, token string) {
	c := config.Get()
	fmt.Fprint(w, `<fieldset><legend>For every alarm on this device</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "alarm_prefs", "alarms")
	fmt.Fprintf(w, `<div class="row">
	 <div><label for="snooze">Snooze length, minutes</label><input id="snooze" name="snooze" type="number" min="%d" max="%d" value="%d"></div>
	 <div><label for="ringvol">Ring volume, 0 to %d</label><input id="ringvol" name="ringvol" type="number" min="0" max="%d" value="%d"></div></div>`,
		config.MinSnoozeMinutes, config.MaxSnoozeMinutes, c.Alarms.Snooze(), config.VolumeSteps, config.VolumeSteps, ring.Level())
	fmt.Fprint(w, `<div class="row"><div><label for="sound">Alarm sound</label><select id="sound" name="sound">`)
	for _, s := range speaker.AlarmSounds() {
		fmt.Fprintf(w, `<option%s>%s</option>`, selected(s == alarm.Get().Sound()), html.EscapeString(s))
	}
	fmt.Fprint(w, `</select></div><div><label for="sunrise">Wake with light, by default</label><select id="sunrise" name="sunrise">`)
	fmt.Fprintf(w, `<option value="0"%s>Off</option>`, selected(c.Alarms.SunriseMinutes <= 0))
	for _, m := range wakeLights {
		fmt.Fprintf(w, `<option value="%d"%s>%d minutes before</option>`, m, selected(c.Alarms.SunriseMinutes == m), m)
	}
	fmt.Fprint(w, `</select></div></div>
	 <p class="note">Ring volume is only for alarms and timers, not the music. At 0 they make no sound. The
	  default wake light is for alarms that don't choose their own.</p>
	 <div class="btns"><button type="submit">Save</button></div></form></fieldset>`)
}

// whenFields are an alarm's time and words.
func whenFields(w http.ResponseWriter, id, at, label string, remind bool) {
	words := "Words <span class=\"note\">(optional; said when it rings)</span>"
	if remind {
		words = "Words <span class=\"note\">(said aloud)</span>"
	}
	fmt.Fprintf(w, `<div class="row"><div><label for="t%s">Time</label><input id="t%s" name="at" type="time" value="%s"></div>
	 <div><label for="l%s">%s</label><input id="l%s" name="label" value="%s" maxlength="80" autocomplete="off"></div></div>`,
		id, id, at, id, words, id, html.EscapeString(label))
}

func daysField(w http.ResponseWriter, days uint8) {
	fmt.Fprint(w, `<label>Repeat <span class="note">(none ticked: once)</span></label><div class="days">`)
	for i, d := range []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"} {
		checked := ""
		if days&(1<<i) != 0 {
			checked = " checked"
		}
		fmt.Fprintf(w, `<label><input type="checkbox" name="day" value="%d"%s>%s</label>`, i, checked, d)
	}
	fmt.Fprint(w, `</div>`)
}

func ringOnField(w http.ResponseWriter, id, value string) {
	fmt.Fprintf(w, `<label for="%s">Also on <span class="note">(device names, comma-separated, or "everywhere"; blank: only here)</span></label>
	 <input id="%s" name="ring_on" value="%s" autocomplete="off" placeholder="Kitchen, Office">`, id, id, html.EscapeString(value))
}

func sunriseField(w http.ResponseWriter, id string, cur int) {
	fmt.Fprintf(w, `<label for="%s">Wake with light</label><select id="%s" name="alarm_sunrise">`, id, id)
	def := "off"
	if m := config.Get().Alarms.SunriseMinutes; m > 0 {
		def = fmt.Sprintf("%d min", m)
	}
	fmt.Fprintf(w, `<option value="0"%s>Same as the default (%s)</option>`, selected(cur == 0), def)
	fmt.Fprintf(w, `<option value="%d"%s>Off</option>`, config.SunriseOff, selected(cur < 0))
	for _, m := range wakeLights {
		fmt.Fprintf(w, `<option value="%d"%s>%d minutes before</option>`, m, selected(cur == m), m)
	}
	fmt.Fprint(w, `</select>`)
}

// alarmSave takes this tab's forms, and reports whether what was one of them.
func alarmSave(r *http.Request, what string, problem *string) bool {
	switch what {
	case "alarm_add":
		*problem = addFromPage(r)
	case "alarm_edit":
		*problem = editFromPage(r)
	case "timer_cancel":
		if !timer.Get().Cancel(r.PostFormValue("id")) {
			*problem = "that timer has already finished or gone"
		}
	case "snoozes_cancel":
		alarm.Get().CancelSnoozes()
	case "alarm_prefs":
		*problem = prefsFromPage(r)
	default:
		return false
	}
	return true
}

func addFromPage(r *http.Request) string {
	kind := r.PostFormValue("kind")
	at, in := strings.TrimSpace(r.PostFormValue("at")), strings.TrimSpace(r.PostFormValue("in"))
	label := strings.TrimSpace(r.PostFormValue("label"))
	days := daysOf(r)
	switch kind {
	case "timer":
		if in == "" {
			return "a timer needs a length, in the \"…or in\" box"
		}
		d, err := timer.ParseDuration(in)
		if err != nil {
			return err.Error()
		}
		timer.Get().Start(cmpOr(label, "Timer"), d)
		slog.Info("setup page: a timer was set", "for", d)
	case "reminder":
		when := at
		if in != "" {
			when = "in " + in
		}
		if when == "" {
			return "a reminder needs a time, or a length of time from now"
		}
		hour, minute, fromNow, err := alarm.ReminderTime(when, time.Now())
		if err != nil {
			return err.Error()
		}
		if fromNow && days != config.DaysOnce {
			return "a time from now happens once; give a time of day to repeat it"
		}
		if _, err := alarm.Get().SetReminder(hour, minute, days, label, names(r.PostFormValue("ring_on"))); err != nil {
			return err.Error()
		}
		slog.Info("setup page: a reminder was set")
	default:
		hour, minute, err := clockOf(at)
		if err != nil {
			return err.Error()
		}
		al, err := alarm.Get().Set(hour, minute, days, label)
		if err != nil {
			return err.Error()
		}
		if s := sunriseOf(r); s != al.Sunrise {
			al.Sunrise = s
			if err := alarm.Get().Put(al); err != nil {
				return err.Error()
			}
		}
		slog.Info("setup page: an alarm was set")
	}
	return ""
}

func editFromPage(r *http.Request) string {
	id := r.PostFormValue("id")
	var al config.Alarm
	found := false
	for _, a := range config.Get().Alarms.List {
		if a.ID == id {
			al, found = a, true
		}
	}
	if !found {
		return "that alarm is not on this device any more"
	}
	switch r.PostFormValue("act") {
	case "delete":
		if err := alarm.Get().Delete(id); err != nil {
			return err.Error()
		}
		slog.Info("setup page: an alarm was deleted")
		return ""
	case "toggle":
		al.On = !al.On
	default:
		hour, minute, err := clockOf(r.PostFormValue("at"))
		if err != nil {
			return err.Error()
		}
		al.Hour, al.Minute, al.Days = hour, minute, daysOf(r)
		al.Label = strings.TrimSpace(r.PostFormValue("label"))
		if al.Remind {
			if al.Label == "" {
				return "a reminder needs words to say"
			}
			al.RingOn = names(r.PostFormValue("ring_on"))
		} else {
			al.Sunrise = sunriseOf(r)
		}
		al.On = true
	}
	if err := alarm.Get().Put(al); err != nil {
		return err.Error()
	}
	return ""
}

func prefsFromPage(r *http.Request) string {
	snooze, err1 := strconv.Atoi(r.PostFormValue("snooze"))
	vol, err2 := strconv.Atoi(r.PostFormValue("ringvol"))
	sunrise, err3 := strconv.Atoi(r.PostFormValue("sunrise"))
	if err1 != nil || err2 != nil || err3 != nil {
		return "those are numbers"
	}
	alarm.Get().SetSnooze(snooze)
	alarm.Get().SetRingVolume(vol, false)
	if s := r.PostFormValue("sound"); s != alarm.Get().Sound() {
		alarm.Get().SetSound(s, false)
	}
	if sunrise != 0 && !contains(wakeLights, sunrise) {
		return "that is not one of the wake light choices"
	}
	if err := config.Set().Alarms().SunriseMinutes(sunrise); err != nil {
		return err.Error()
	}
	return ""
}

// clockOf reads a time box's "15:04".
func clockOf(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("that needs a time")
	}
	return t.Hour(), t.Minute(), nil
}

func daysOf(r *http.Request) uint8 {
	var days uint8
	for _, v := range r.PostForm["day"] {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < 7 {
			days |= 1 << i
		}
	}
	return days
}

func sunriseOf(r *http.Request) int {
	n, err := strconv.Atoi(r.PostFormValue("alarm_sunrise"))
	if err != nil || (n != config.SunriseOff && n != 0 && !contains(wakeLights, n)) {
		return 0
	}
	return n
}

func names(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// clockTime is a time of day in the device's own clock format.
func clockTime(hour, minute int) string {
	t := time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC)
	if config.Get().Screen.Clock24 {
		return t.Format("15:04")
	}
	return strings.ToLower(t.Format("3:04 PM"))
}

func clockText(t time.Time) string { return clockTime(t.Hour(), t.Minute()) }

func timeLeft(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60)
	}
	return fmt.Sprintf("%d:%02d", int(d/time.Minute), int(d/time.Second)%60)
}

func labelSuffix(s string) string {
	if s == "" {
		return ""
	}
	return " · " + html.EscapeString(s)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
