package home

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/sun"
)

// sunKept is today's sunrise and sunset at home. Finding home can ask Home Assistant, which a frame
// being drawn must never wait on, so home's place is looked up off the caller's goroutine, once a day;
// the times themselves are worked out from the last place found, which is quick. A new day with the
// lookup failing still gets that day's times, from where home was yesterday.
var sunKept sunKeeper

// sunKeeper is what sunKept holds.
type sunKeeper struct {
	mu        sync.Mutex
	day       string // the day rise and set are for
	rise, set time.Time
	ok        bool

	lat, lon float64
	placed   bool   // lat, lon are known
	looked   string // the day home's place was last looked up
	asking   bool
	retry    time.Time // after a failed lookup, when to try again
}

// SunTimes is today's sunrise and sunset at home, for the Sun clock style, the night sky and the
// weather art: the sunrise and sunset sensors' times where they are set and have one for today
// (sunFromSensors), the times worked out from home's place for the rest. ok is false until there is a
// time for both, which is never on a day the sun does not rise or set at home and no sensor says.
func (f *Feature) SunTimes(now time.Time) (rise, set time.Time, ok bool) {
	rise, set, ok = sunWorkedOut(now)
	h := config.Get().Home
	if h.SunriseEntity == "" && h.SunsetEntity == "" {
		return rise, set, ok
	}
	t := hastate.Get()
	return sunFromSensors(now, t.State(h.SunriseEntity), t.State(h.SunsetEntity), rise, set, ok)
}

// sunWorkedOut is today's sunrise and sunset at home as the sun's own place says: ok is false until
// home's place is known, and on a day the sun does not rise or set there.
func sunWorkedOut(now time.Time) (rise, set time.Time, ok bool) {
	k := &sunKept
	day := now.Format("2006-01-02")
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lookUp(now, day)
	if !k.placed {
		return time.Time{}, time.Time{}, false
	}
	if k.day != day {
		k.day = day
		k.rise, k.set, k.ok = sun.Times(k.lat, k.lon, now)
	}
	return k.rise, k.set, k.ok
}

// SkyAt is a condition as the sky looks at home at now: its night form (atNight) while the sun is
// down. Night is from home's own sunrise and sunset, so it needs no Home Assistant; on a day the sun
// does not rise or set (inside the polar circles), from the sun's height at now: the polar night is
// night all day, the midnight sun day. Until home's place is known the condition is left as reported.
func (f *Feature) SkyAt(cond string, now time.Time) string {
	rise, set, ok := f.SunTimes(now)
	if !ok {
		lat, lon, placed := f.Place(now)
		if !placed || sun.Up(lat, lon, now) {
			return cond
		}
		return atNight(cond)
	}
	if !now.Before(rise) && now.Before(set) {
		return cond
	}
	return atNight(cond)
}

// SkyNow is the condition a screen shows for now: the reading, or without one today's forecast in its
// place. The reading has its night form already (Weather); the forecast's day is a daytime forecast,
// so standing in for the reading after dark it takes the night form here, or the page showed a sun
// at night. The forecast's own row keeps its day icons.
func (f *Feature) SkyNow(w Weather, days []hass.Day, now time.Time) string {
	if w.Condition != "" || len(days) == 0 {
		return w.Condition
	}
	return f.SkyAt(days[0].Condition, now)
}

// Place is home's latitude and longitude as last found, for the weather art's moon, which is worked
// out from it a minute at a time; ok is false until it is known. It looks home up as SunTimes does.
func (f *Feature) Place(now time.Time) (lat, lon float64, ok bool) {
	k := &sunKept
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lookUp(now, now.Format("2006-01-02"))
	return k.lat, k.lon, k.placed
}

// lookUp starts finding home's place, once a day, unless that is under way or waiting to try again.
// The caller holds mu.
func (k *sunKeeper) lookUp(now time.Time, day string) {
	if k.looked != day && !k.asking && now.After(k.retry) {
		k.asking = true
		safe.Go("sun times", func() { sunLookUp(day) })
	}
}

// sunLookUp finds home's place, and has the times worked out again from it.
func sunLookUp(day string) {
	k := &sunKept
	defer func() { // a lookup that panicked is tried again later, rather than never
		k.mu.Lock()
		k.asking = false
		k.mu.Unlock()
	}()
	lat, lon, err := homeLocation()
	k.mu.Lock()
	defer k.mu.Unlock()
	if err != nil {
		slog.Debug("sun times wait for home's place", "err", err)
		k.retry = time.Now().Add(10 * time.Minute)
		return
	}
	k.looked = day
	if !k.placed || lat != k.lat || lon != k.lon {
		k.lat, k.lon, k.placed = lat, lon, true
		k.day = "" // worked out again from the new place on the next ask
	}
}

// The sunrise and sunset sensors (#747). Where hills or buildings hide the horizon, the sun shows well
// after it is up by the almanac and goes well before it sets, and a house can keep the times it really
// appears and goes in two Home Assistant timestamp sensors. A sensor's time stands in for the worked-out
// one, each on its own, when:
//
//   - it is set, and Home Assistant has sent a state for it that is a time: ISO 8601 with its offset, as
//     a timestamp sensor's state is ("2026-11-27T07:41:00+00:00"), or a date and time with none, as an
//     input_datetime's is, taken in the device's own time zone;
//   - that time, in the device's time zone, is today, or tomorrow: a sensor of the next sunrise (the way
//     the Sun integration's are made) says tomorrow's once today's is past, and tomorrow's less a day is
//     today's to within a few minutes, where taking the almanac's instead would move the sun by however
//     far the hills put it. A time on any other day is a sensor that stopped being updated, and is not
//     used;
//   - the sunrise it leaves comes before the sunset.
//
// Otherwise the times are the worked-out ones, which also means a sensor that is unavailable, unknown,
// or not a time changes nothing. Sensors that give both times make a day even where the almanac has
// none (inside the polar circles) or before home's place is known.

// sunFromSensors is today's sunrise and sunset from the sensors' states riseState and setState, with
// the worked-out rise, set and ok for what they leave out.
func sunFromSensors(now time.Time, riseState, setState string, rise, set time.Time, ok bool) (time.Time, time.Time, bool) {
	r, rOK := sensorSunTime(riseState, now)
	s, sOK := sensorSunTime(setState, now)
	if !rOK && !sOK {
		return rise, set, ok
	}
	if !rOK || !sOK {
		if !ok {
			return rise, set, ok // half a day from a sensor, and nothing worked out for the other half
		}
		if !rOK {
			r = rise
		}
		if !sOK {
			s = set
		}
	}
	if !r.Before(s) {
		return rise, set, ok
	}
	return r, s, true
}

// sunStateLayouts are the forms a sensor's time is read in: ISO 8601 with an offset (with a T or a
// space, with or without fractions of a second), then a date and time with none.
var sunStateLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00"}
var sunLocalLayouts = []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04"}

// sensorSunTime is a sensor's state as a time today in now's time zone, or false when it is not one
// (see above).
func sensorSunTime(state string, now time.Time) (time.Time, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return time.Time{}, false
	}
	loc := now.Location()
	t, ok := parseSunState(state, loc)
	if !ok {
		return time.Time{}, false
	}
	t = t.In(loc)
	switch {
	case onSameDay(t, now):
		return t, true
	case onSameDay(t, now.AddDate(0, 0, 1)):
		return t.AddDate(0, 0, -1), true
	}
	return time.Time{}, false
}

// parseSunState reads a state in one of the layouts above; one with no offset is a time in loc.
func parseSunState(state string, loc *time.Location) (time.Time, bool) {
	for _, l := range sunStateLayouts {
		if t, err := time.Parse(l, state); err == nil {
			return t, true
		}
	}
	for _, l := range sunLocalLayouts {
		if t, err := time.ParseInLocation(l, state, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func onSameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// SunEntity is an entity id typed for the sunrise or sunset sensor, tidied: empty for none, an error
// for something that is not an entity id.
func SunEntity(typed string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(typed))
	if id == "" || entityID.MatchString(id) {
		return id, nil
	}
	return "", errors.New(typed + " is not an entity ID; use one like sensor.sunrise")
}

// SetSunEntities chooses the sensors sunrise and sunset are taken from, empty for the worked-out
// times, and has Home Assistant asked for their states at the next connection.
func (f *Feature) SetSunEntities(rise, set string) error {
	var err error
	if rise, err = SunEntity(rise); err != nil {
		return err
	}
	if set, err = SunEntity(set); err != nil {
		return err
	}
	h := config.Get().Home
	if rise == h.SunriseEntity && set == h.SunsetEntity {
		return nil
	}
	if err := config.Set().Home().SunEntities(rise, set); err != nil {
		return err
	}
	slog.Info("home: sunrise and sunset sensors", "sunrise", rise, "sunset", set)
	f.rewire()
	return nil
}
