package alarm

import (
	"log/slog"
	"slices"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// What an alarm says as it starts ringing. It has always said its own words, when it had some; it can
// also say the time first, so an alarm with no words still says something, or say nothing at all. The
// words are made by Home Assistant (remind.Say), so without it the ring is all there is either way.

// sayChoice is the choice in Home Assistant.
func (a *Alarms) sayChoice() *esphome.Select {
	sel := &esphome.Select{
		Base: esphome.Base{ObjectID: "alarm_words", Name: "Alarm words", Icon: "mdi:account-voice", Category: esphome.CategoryConfig},
	}
	component.Bind(sel, config.AlarmSays(), func(v config.AlarmSay) config.AlarmSay { return v }, config.Set().Alarms().Say)
	return sel
}

// Say is what alarms say as they start ringing.
func (a *Alarms) Say() config.AlarmSay { return config.Get().Alarms.Say }

// SetSay chooses what alarms say as they start ringing, from the screen or the setup page.
func (a *Alarms) SetSay(v config.AlarmSay) {
	if !slices.Contains(config.AlarmSays(), v) {
		slog.Warn("unknown alarm words choice", "value", v)
		return
	}
	if err := config.Set().Alarms().Say(v); err != nil {
		slog.Error("saving the alarm words failed", "err", err)
		return
	}
	a.words.Set(v.Label())
	slog.Info("alarm words", "using", v)
}

// spokenNow is what an alarm with this label says, ringing now, as the device is set.
func spokenNow(label string, now time.Time) string {
	c := config.Get()
	return spoken(label, now, c.Alarms.Say, c.Screen.Clock24, english(c.Screen.Language))
}

// english is whether the screen's language is English, the only one AM and PM are said in.
func english(lang string) bool { return lang == "" || lang == "en" || strings.HasPrefix(lang, "en-") }

// spoken is what an alarm says as it rings at now: empty for nothing worth saying.
//
// The time is written the way the clock writes it, twelve hour or twenty-four, and with no words around
// it, so it is said in whatever language Home Assistant's voice speaks. AM and PM are English, and a
// voice in another language spells them out, so they are only said in English: in another language a
// twelve hour time is the hour and the minutes alone, which is the time anybody woken by it needs.
func spoken(label string, now time.Time, say config.AlarmSay, clock24, inEnglish bool) string {
	words := strings.TrimSpace(label)
	// Words that only say what it is add nothing to the chime, and holding the chime back for them is
	// nine seconds of an alarm not sounding.
	switch strings.ToLower(words) {
	case "alarm", "an alarm", "the alarm", "wake up alarm", "timer", "reminder":
		words = ""
	}
	switch say {
	case config.SayNothing:
		return ""
	case config.SayTime:
		at := now.Format("3:04 PM")
		switch {
		case clock24:
			at = now.Format("15:04")
		case !inEnglish:
			at = now.Format("3:04")
		}
		if words == "" {
			return at
		}
		return at + ". " + words
	}
	return words
}

// riseChoice is how long an alarm takes to come up from quiet to its ring volume, in Home Assistant.
func (a *Alarms) riseChoice() *esphome.Select {
	sel := &esphome.Select{
		Base: esphome.Base{ObjectID: "alarm_gets_louder", Name: "Alarm gets louder", Icon: "mdi:volume-plus", Category: esphome.CategoryConfig},
	}
	component.Bind(sel, config.AlarmRises(), func(v config.AlarmRise) config.AlarmRise { return v }, config.Set().Alarms().Rise)
	return sel
}

// Rise is how long alarms take to come up from quiet.
func (a *Alarms) Rise() config.AlarmRise { return config.Get().Alarms.Rise }

// SetRise chooses how long alarms take to come up from quiet, from the screen or the setup page.
func (a *Alarms) SetRise(v config.AlarmRise) {
	if !slices.Contains(config.AlarmRises(), v) {
		slog.Warn("unknown alarm rise choice", "value", v)
		return
	}
	if err := config.Set().Alarms().Rise(v); err != nil {
		slog.Error("saving the alarm rise failed", "err", err)
		return
	}
	a.rise.Set(v.Label())
	slog.Info("alarm gets louder", "over", v.Duration())
}
