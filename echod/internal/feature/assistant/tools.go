package assistant

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/llm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/openmeteo"
)

// tool is one thing the model may do on the device. Run gets the model's arguments and returns what
// happened, for the model to put into words.
type tool struct {
	llm.Tool
	Run func(args map[string]any) (string, error)
}

func specs(ts []tool) []llm.Tool {
	out := make([]llm.Tool, len(ts))
	for i, t := range ts {
		out[i] = t.Tool
	}
	return out
}

func object(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

func argString(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return strings.TrimSpace(v)
}

func argNumber(args map[string]any, k string) (float64, bool) {
	switch v := args[k].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// toolList is the tools a turn may use; a variable for the tests.
var toolList = tools

// tools is what the model may do, as the device is now: the lists it reads are read when asked.
func tools() []tool {
	return append(append(append(append(append(append(deviceTools(), radioTools()...), newsTools()...), musicTools()...), screenTools()...), webTools()...), todoTools()...)
}

func deviceTools() []tool {
	return []tool{
		{llm.Tool{Name: "start_timer", Description: "Start a countdown timer on this device.",
			Parameters: object(map[string]any{
				"seconds": num("How long, in seconds."),
				"label":   str("What it is for, like pasta; empty if not said."),
			}, "seconds")},
			func(a map[string]any) (string, error) {
				s, ok := argNumber(a, "seconds")
				if !ok || s < 1 || s > 24*3600 {
					return "", errors.New("seconds must be between 1 and 86400")
				}
				d := time.Duration(math.Round(s)) * time.Second
				label := argString(a, "label")
				timer.Get().Start(label, d)
				if label == "" {
					return "started a " + words(d) + " timer with no label", nil
				}
				return fmt.Sprintf("started a %s timer labeled %q", words(d), label), nil
			}},

		{llm.Tool{Name: "list_timers", Description: "List the timers running on this device and how long each has left.",
			Parameters: object(map[string]any{})},
			func(map[string]any) (string, error) {
				list := timer.Get().List(time.Now())
				if len(list) == 0 {
					return "no timers", nil
				}
				var s []string
				for _, t := range list {
					name := fmt.Sprintf("%q", t.Name)
					if t.Name == "" {
						name = "a timer with no label"
					}
					s = append(s, fmt.Sprintf("%s with %s left", name, words(t.Left)))
				}
				return strings.Join(s, "; "), nil
			}},

		{llm.Tool{Name: "cancel_timers", Description: "Cancel timers: the one with this label, or all of them.",
			Parameters: object(map[string]any{"label": str("The timer's label; empty cancels all.")})},
			func(a map[string]any) (string, error) {
				label := strings.ToLower(argString(a, "label"))
				list := timer.Get().List(time.Now())
				n := 0
				for _, t := range list {
					if label == "" || strings.ToLower(t.Name) == label {
						if timer.Get().Cancel(t.ID) {
							n++
						}
					}
				}
				// "Cancel the timer" with one running means that one, whatever the model took its
				// label to be.
				if n == 0 && len(list) == 1 && timer.Get().Cancel(list[0].ID) {
					n = 1
				}
				switch n {
				case 0:
					return "no timer matched, and none was canceled", nil
				case 1:
					return "canceled one timer", nil
				}
				return fmt.Sprintf("canceled %d timers", n), nil
			}},

		{llm.Tool{Name: "set_alarm", Description: "Set an alarm on this device.",
			Parameters: object(map[string]any{
				"time":  str("The time as the person said it: 6:30, 6:30 pm, 18:30, 7 am. Keep am or pm only if said."),
				"days":  str("once (the next time it comes round), daily, weekdays, weekends, or days like mon,wed,fri. Default once."),
				"label": str("What it is for; empty if not said."),
			}, "time")},
			func(a map[string]any) (string, error) {
				h, m, err := alarmClock(argString(a, "time"))
				if err != nil {
					return "", err
				}
				// A one-time alarm goes off the next time its hour comes round, which is what "today"
				// and "tomorrow" mean whenever they are said; models reach for them.
				d := argString(a, "days")
				if l := strings.ToLower(d); l == "today" || l == "tomorrow" {
					d = "once"
				}
				days, err := config.ParseDays(d)
				if err != nil {
					return "", err
				}
				al, err := alarm.Get().Set(h, m, days, argString(a, "label"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("alarm set for %02d:%02d (%s)", al.Hour, al.Minute, config.DaysLabel(al.Days)), nil
			}},

		{llm.Tool{Name: "list_alarms", Description: "List the alarms and reminders set on this device.",
			Parameters: object(map[string]any{})},
			func(map[string]any) (string, error) {
				var s []string
				for _, al := range config.Get().Alarms.List {
					kind := "alarm"
					if al.Remind {
						kind = "reminder"
					}
					state := "on"
					if !al.On {
						state = "off"
					}
					line := fmt.Sprintf("%s at %02d:%02d, %s, %s", kind, al.Hour, al.Minute, config.DaysLabel(al.Days), state)
					if al.Label != "" {
						line += fmt.Sprintf(", labeled %q", al.Label)
					}
					s = append(s, line)
				}
				if len(s) == 0 {
					return "no alarms or reminders", nil
				}
				return strings.Join(s, "; "), nil
			}},

		{llm.Tool{Name: "delete_alarm", Description: "Delete an alarm or reminder, found by its time or label. Use list_alarms first when unsure.",
			Parameters: object(map[string]any{
				"time":  str("Its time of day, 24-hour, as HH:MM; empty to go by the label."),
				"label": str("Its label; empty to go by the time."),
			})},
			func(a map[string]any) (string, error) {
				list := config.Get().Alarms.List
				label := strings.ToLower(argString(a, "label"))
				var h, m int
				byTime := argString(a, "time") != ""
				if byTime {
					var err error
					if h, m, err = clock(argString(a, "time")); err != nil {
						return "", err
					}
				}
				var found []config.Alarm
				for _, al := range list {
					if byTime && (al.Hour != h || al.Minute != m) {
						continue
					}
					if label != "" && strings.ToLower(al.Label) != label {
						continue
					}
					found = append(found, al)
				}
				// "Delete the alarm" with one set means that one.
				if !byTime && label == "" && len(list) == 1 {
					found = list
				}
				switch len(found) {
				case 0:
					return "no alarm matched; nothing was deleted", nil
				case 1:
					if err := alarm.Get().Delete(found[0].ID); err != nil {
						return "", err
					}
					return fmt.Sprintf("deleted the %02d:%02d one", found[0].Hour, found[0].Minute), nil
				}
				return fmt.Sprintf("%d alarms matched; ask which one, nothing was deleted", len(found)), nil
			}},

		{llm.Tool{Name: "stop", Description: "Stop whatever is ringing or playing on this device right now: a ringing alarm or timer, the radio or music. It does not delete alarms or cancel timers.",
			Parameters: object(map[string]any{})},
			func(map[string]any) (string, error) {
				rang := ring.End()
				media.Get().Stop()
				if rang {
					return "stopped the ringing and anything playing", nil
				}
				return "stopped anything playing", nil
			}},

		{llm.Tool{Name: "set_volume", Description: fmt.Sprintf("Set the speaker volume, 0 to %d.", media.VolumeSteps),
			Parameters: object(map[string]any{"level": num(fmt.Sprintf("0 to %d.", media.VolumeSteps))}, "level")},
			func(a map[string]any) (string, error) {
				v, ok := argNumber(a, "level")
				if !ok {
					return "", errors.New("level is a number")
				}
				media.Get().Set(int(math.Round(v)))
				return fmt.Sprintf("volume is %d of %d", media.Get().Volume(), media.VolumeSteps), nil
			}},

		// Not "call": llama.cpp's grammar for a tool call has a rule by that name, and a tool called the
		// same makes the whole list of tools unusable.
		{llm.Tool{Name: "call_device", Description: "Call another device (an intercom call to a room or a family member's device). Use a name from list_callees.",
			Parameters: object(map[string]any{"who": str("Who or which room to call.")}, "who")},
			func(a map[string]any) (string, error) {
				want := strings.ToLower(argString(a, "who"))
				for _, c := range phone.Get().Callees() {
					if c.Device && strings.ToLower(c.Name) == want {
						if err := phone.Get().CallDevice(c.Name); err != nil {
							return "", err
						}
						return "calling " + c.Name, nil
					}
				}
				return "", fmt.Errorf("nobody called %q; call list_callees", want)
			}},

		{llm.Tool{Name: "list_callees", Description: "List the devices this one can call.",
			Parameters: object(map[string]any{})},
			func(map[string]any) (string, error) {
				var s []string
				for _, c := range phone.Get().Callees() {
					if c.Device {
						s = append(s, c.Name)
					}
				}
				if len(s) == 0 {
					return "no other devices can be called", nil
				}
				return strings.Join(s, "; "), nil
			}},

		{llm.Tool{Name: "calendar", Description: "The events on this device's calendars for a day or a few days.",
			Parameters: object(map[string]any{
				"date": str("The first day, as YYYY-MM-DD. Default today."),
				"days": num("How many days, 1 to 14. Default 1."),
			})},
			func(a map[string]any) (string, error) {
				if len(home.Get().CalendarSources()) == 0 {
					return "", errors.New("this device shows no calendars")
				}
				now := time.Now()
				first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
				if d := argString(a, "date"); d != "" {
					t, err := time.ParseInLocation("2006-01-02", d, time.Local)
					if err != nil {
						return "", fmt.Errorf("%q is not a date as YYYY-MM-DD", d)
					}
					first = t
				}
				n := 1
				if v, ok := argNumber(a, "days"); ok {
					n = int(max(1, min(14, math.Round(v))))
				}
				var s []string
				read := true
				for i := range n {
					day := first.AddDate(0, 0, i)
					evs, ok := home.Get().EventsOn(day)
					read = read && ok
					var items []string
					for _, e := range evs {
						when := "all day"
						if !e.AllDay {
							when = e.Start.Format("3:04 PM")
						}
						items = append(items, when+" "+e.Summary)
					}
					if len(items) == 0 {
						items = []string{"nothing"}
					}
					s = append(s, day.Format("Monday January 2")+": "+strings.Join(items, ", "))
				}
				if !read {
					s = append(s, "(the calendars are still being read; this may be incomplete)")
				}
				return strings.Join(s, "; "), nil
			}},

		{llm.Tool{Name: "weather_elsewhere", Description: "The weather forecast for another place, up to 14 days ahead: a town, city or ZIP code anywhere.",
			Parameters: object(map[string]any{
				"place": str("The place, like Lincoln, Nebraska."),
				"date":  str("The day: today, tomorrow, a weekday like Saturday, or YYYY-MM-DD; empty for the next few days."),
			}, "place")},
			func(a map[string]any) (string, error) { return weatherAt(argString(a, "place"), argString(a, "date")) }},

		{llm.Tool{Name: "weather", Description: "The weather where this device is, now and for the next days. Only this device's own location: for anywhere else use weather_elsewhere.",
			Parameters: object(map[string]any{})},
			func(map[string]any) (string, error) {
				w := home.Get().Weather()
				days := home.Get().Forecast()
				if w.Condition == "" && len(days) == 0 {
					return "", errors.New("this device has no weather source")
				}
				var s []string
				if w.Condition != "" {
					s = append(s, fmt.Sprintf("now: %s, %s", home.ConditionWords(w.Condition), w.Temp))
				}
				for i, d := range days {
					if i >= 5 {
						break
					}
					day := d.When.Format("Monday")
					if i == 0 {
						day = "today"
					}
					line := fmt.Sprintf("%s: %s, high %.0f, low %.0f", day, home.ConditionWords(d.Condition), d.High, d.Low)
					if d.Rain >= 0 {
						line += fmt.Sprintf(", %d%% chance of rain or snow", d.Rain)
					}
					s = append(s, line)
				}
				return strings.Join(s, "; "), nil
			}},
	}
}

// weatherAt is Open-Meteo's forecast for a place looked up by name.
func weatherAt(place, date string) (string, error) {
	if place == "" {
		return "", errors.New("no place was named")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	country := ""
	if len(place) == 5 && strings.IndexFunc(place, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		country = "US"
	}
	found, err := openmeteo.Find(ctx, place, country)
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no place called %q was found", place)
	}
	p := found[0]
	date, err = dayOf(date, time.Now())
	if err != nil {
		return "", err
	}
	_, days, err := openmeteo.Forecast(ctx, p.Lat, p.Lon, config.Get().Home.Fahrenheit(), 14)
	if err != nil {
		return "", err
	}
	var s []string
	for i, d := range days {
		if date == "" && i >= 4 {
			break
		}
		if date != "" && d.When.Format("2006-01-02") != date {
			continue
		}
		line := fmt.Sprintf("%s: %s, high %.0f, low %.0f", d.When.Format("Monday January 2"), home.ConditionWords(d.Condition), d.High, d.Low)
		if d.Rain >= 0 {
			line += fmt.Sprintf(", %d%% chance of rain or snow", d.Rain)
		}
		s = append(s, line)
	}
	if len(s) == 0 {
		return "", fmt.Errorf("no forecast for %s on %s: the forecast goes 14 days ahead", p.Name, date)
	}
	return "forecast for " + p.Name + ": " + strings.Join(s, "; "), nil
}

// dayOf reads a day as a person or a model names it - today, tomorrow, a weekday (the next one to
// come, today included), or YYYY-MM-DD - as YYYY-MM-DD; empty stays empty. Models count days badly: a
// "Saturday" worked out by one landed on a Wednesday.
func dayOf(s string, now time.Time) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "":
		return "", nil
	case "today":
		return now.Format("2006-01-02"), nil
	case "tomorrow":
		return now.AddDate(0, 0, 1).Format("2006-01-02"), nil
	}
	for i := range 7 {
		d := now.AddDate(0, 0, i)
		if strings.HasPrefix(strings.ToLower(d.Weekday().String()), strings.TrimPrefix(v, "this ")) && len(v) >= 3 {
			return d.Format("2006-01-02"), nil
		}
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t.Format("2006-01-02"), nil
	}
	return "", fmt.Errorf("%q is not a day as today, tomorrow, a weekday or YYYY-MM-DD", s)
}

// words says a duration the way it is said: "2 minutes", "1 hour 5 minutes", "40 seconds".
func words(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	var out []string
	part := func(n int, unit string) {
		if n == 1 {
			out = append(out, "1 "+unit)
		} else if n > 1 {
			out = append(out, fmt.Sprintf("%d %ss", n, unit))
		}
	}
	part(h, "hour")
	part(m, "minute")
	if h == 0 {
		part(s, "second")
	}
	if len(out) == 0 {
		return "0 seconds"
	}
	return strings.Join(out, " ")
}

// alarmClock reads an alarm's time as it was said. Without am or pm, an hour from 4 to 11 is the
// morning and one from 1 to 3 the afternoon; twelve is noon. Left to the model this came out as the
// evening as often as not.
func alarmClock(s string) (int, int, error) {
	v := strings.ToLower(strings.Join(strings.Fields(s), ""))
	v = strings.NewReplacer("a.m.", "am", "p.m.", "pm").Replace(v)
	pm, am := strings.HasSuffix(v, "pm"), strings.HasSuffix(v, "am")
	v = strings.TrimSuffix(strings.TrimSuffix(v, "pm"), "am")
	hs, ms, ok := strings.Cut(v, ":")
	if !ok {
		hs, ms = v, "0"
	}
	h, err1 := strconv.Atoi(hs)
	m, err2 := strconv.Atoi(ms)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("%q is not a time of day", s)
	}
	switch {
	case (am || pm) && (h < 1 || h > 12):
		return 0, 0, fmt.Errorf("%q is not a time of day", s)
	case pm && h < 12:
		h += 12
	case am && h == 12:
		h = 0
	case !am && !pm && h >= 1 && h <= 3:
		h += 12
	}
	return h, m, nil
}

// clock reads HH:MM, 24-hour.
func clock(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a time as HH:MM", s)
	}
	return t.Hour(), t.Minute(), nil
}

// station finds a station by name the way somebody says it: case aside, and a part of the name will
// do when only one station has it.
func station(want string) (string, bool) {
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "" {
		return "", false
	}
	list := home.Get().Radio().Stations
	for _, s := range list {
		if strings.ToLower(s) == w {
			return s, true
		}
	}
	var found []string
	for _, s := range list {
		if strings.Contains(strings.ToLower(s), w) {
			found = append(found, s)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return "", false
}
