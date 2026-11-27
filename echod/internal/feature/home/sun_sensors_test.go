package home

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
)

// sunState has Home Assistant send entity's state.
func sunState(t *testing.T, entity, state string) {
	t.Helper()
	if err := hastate.Get().Handle(context.Background(), nil, &api.HomeAssistantStateResponse{EntityId: entity, State: state}); err != nil {
		t.Fatal(err)
	}
}

// A sensor's state is read as Home Assistant writes a timestamp, with its offset, in the device's time
// zone; with no offset, as an input_datetime writes it, it is a time in the device's zone. A time for
// tomorrow, from a sensor of the next sunrise, stands for today's; any other day, or anything that is
// not a time, is no time at all.
func TestASunSensorsStateIsReadInTheDevicesZone(t *testing.T) {
	zone := time.FixedZone("CET", 60*60)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, zone)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 27, h, m, 0, 0, zone) }
	for _, c := range []struct {
		state string
		want  time.Time
		ok    bool
	}{
		{"2026-11-27T07:41:00+00:00", at(8, 41), true},
		{"2026-11-27T08:41:00+01:00", at(8, 41), true},
		{"2026-11-27T07:41:00Z", at(8, 41), true},
		{"2026-11-27T07:41:00.123456+00:00", at(8, 41).Add(123456 * time.Microsecond), true},
		{"2026-11-27 07:41:00+00:00", at(8, 41), true},
		{" 2026-11-27T07:41:00+00:00\n", at(8, 41), true},
		{"2026-11-27 08:41:00", at(8, 41), true},
		{"2026-11-27T08:41", at(8, 41), true},
		// Late on the 26th in UTC is early on the 27th here.
		{"2026-11-26T23:30:00+00:00", at(0, 30), true},
		// Tomorrow's, less a day.
		{"2026-11-28T07:42:00+00:00", at(8, 42), true},
		// Yesterday's, and the day after tomorrow's: a sensor no longer kept up.
		{"2026-11-26T07:40:00+00:00", time.Time{}, false},
		{"2026-11-29T07:43:00+00:00", time.Time{}, false},
		{"unavailable", time.Time{}, false},
		{"unknown", time.Time{}, false},
		{"", time.Time{}, false},
		{"08:41:00", time.Time{}, false},
		{"soon", time.Time{}, false},
	} {
		got, ok := sensorSunTime(c.state, now)
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("%q is %v (%v), want %v (%v)", c.state, got, ok, c.want, c.ok)
		}
		if ok && got.Location() != zone {
			t.Errorf("%q came back in %v, not the device's zone", c.state, got.Location())
		}
	}
}

// Each sensor stands in for its own time; what it leaves out is the worked-out time, and a day the
// sensors would end before it starts is the worked-out one.
func TestTheSunSensorsFallBackEachOnItsOwn(t *testing.T) {
	zone := time.FixedZone("CET", 60*60)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, zone)
	rise, set := time.Date(2026, 11, 27, 7, 50, 0, 0, zone), time.Date(2026, 11, 27, 16, 20, 0, 0, zone)
	hillRise, hillSet := time.Date(2026, 11, 27, 9, 5, 0, 0, zone), time.Date(2026, 11, 27, 15, 10, 0, 0, zone)
	const r, s = "2026-11-27T08:05:00+00:00", "2026-11-27T14:10:00+00:00"
	for _, c := range []struct {
		name           string
		riseSt, setSt  string
		ok             bool // worked out
		wantR, wantS   time.Time
		wantOK         bool
		workedOutAgain bool
	}{
		{"both", r, s, true, hillRise, hillSet, true, false},
		{"sunrise only", r, "unavailable", true, hillRise, set, true, false},
		{"sunset only", "", s, true, rise, hillSet, true, false},
		{"neither", "unknown", "nonsense", true, rise, set, true, true},
		{"both, nothing worked out", r, s, false, hillRise, hillSet, true, false},
		{"one, nothing worked out", r, "", false, time.Time{}, time.Time{}, false, true},
		{"sunrise after sunset", s, r, true, rise, set, true, true},
		{"sunrise after the worked-out sunset", "2026-11-27T16:00:00+00:00", "", true, rise, set, true, true},
	} {
		wr, ws := rise, set
		if !c.ok {
			wr, ws = time.Time{}, time.Time{}
		}
		gr, gs, gok := sunFromSensors(now, c.riseSt, c.setSt, wr, ws, c.ok)
		if c.workedOutAgain {
			c.wantR, c.wantS, c.wantOK = wr, ws, c.ok
		}
		if gok != c.wantOK || !gr.Equal(c.wantR) || !gs.Equal(c.wantS) {
			t.Errorf("%s: %v to %v (%v), want %v to %v (%v)", c.name, gr, gs, gok, c.wantR, c.wantS, c.wantOK)
		}
	}
}

// Set and sent, the sensors' times are home's sunrise and sunset, which is what the night sky goes by:
// before the hills let the sun through it is still night. Unset, or unavailable, the worked-out times
// are back.
func TestTheSunSensorsMoveTheNightSky(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	noon, _, _ := homeOnThePlains(t)
	zone := noon.Location()
	f := &Feature{}
	early := time.Date(2026, 9, 16, 9, 0, 0, 0, zone) // after the almanac's sunrise there, before the hills'
	if got := f.SkyAt("sunny", early); got != "sunny" {
		t.Fatalf("with no sensors, nine in the morning is %q, want sunny", got)
	}
	astroRise, astroSet, _ := f.SunTimes(noon)

	const riseID, setID = "sensor.sunrise_effective", "sensor.sunset_effective"
	if err := f.SetSunEntities(" Sensor.Sunrise_Effective ", setID); err != nil {
		t.Fatal(err)
	}
	if h := config.Get().Home; h.SunriseEntity != riseID || h.SunsetEntity != setID {
		t.Fatalf("saved %q and %q", h.SunriseEntity, h.SunsetEntity)
	}
	if hasScreen && (!f.others[riseID] || !f.others[setID]) {
		t.Error("the sensors are not followed")
	}
	sunState(t, riseID, "2026-09-16T14:30:00+00:00") // 9:30 at home
	sunState(t, setID, "2026-09-17T00:10:00+00:00")  // 19:10 at home, the 16th
	rise, set, ok := f.SunTimes(noon)
	if !ok || !rise.Equal(time.Date(2026, 9, 16, 9, 30, 0, 0, zone)) || !set.Equal(time.Date(2026, 9, 16, 19, 10, 0, 0, zone)) {
		t.Errorf("with the sensors, the day is %v to %v (%v)", rise, set, ok)
	}
	if got := f.SkyAt("sunny", early); got != "clear-night" {
		t.Errorf("before the sensor's sunrise, nine in the morning is %q, want clear-night", got)
	}

	sunState(t, riseID, "unavailable")
	sunState(t, setID, "unavailable")
	if rise, set, _ := f.SunTimes(noon); !rise.Equal(astroRise) || !set.Equal(astroSet) {
		t.Errorf("with the sensors unavailable, the day is %v to %v, want the worked-out %v to %v", rise, set, astroRise, astroSet)
	}

	sunState(t, riseID, "2026-09-16T14:30:00+00:00")
	if err := f.SetSunEntities("", ""); err != nil {
		t.Fatal(err)
	}
	if rise, _, _ := f.SunTimes(noon); !rise.Equal(astroRise) {
		t.Errorf("with the sensors unset, sunrise is %v, want the worked-out %v", rise, astroRise)
	}
	if hasScreen && f.others[riseID] {
		t.Error("an unset sensor is still followed")
	}
	if err := f.SetSunEntities("sunrise at seven", ""); err == nil {
		t.Error("something that is not an entity ID was taken")
	}
}

// The sensors are household settings: a settings file carries them, and a file from before them leaves
// the worked-out times.
func TestTheSunSensorsTravelInASettingsFile(t *testing.T) {
	c := config.Defaults()
	c.Home.SunriseEntity, c.Home.SunsetEntity = "sensor.sunrise_effective", "sensor.sunset_effective"
	file, err := config.Export(c, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Restored(config.Defaults(), file, "test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Home.SunriseEntity != c.Home.SunriseEntity || r.Config.Home.SunsetEntity != c.Home.SunsetEntity {
		t.Errorf("restored %q and %q", r.Config.Home.SunriseEntity, r.Config.Home.SunsetEntity)
	}
	old, err := config.Restored(c, []byte(`{"format":"techo5-settings","version":1,"settings":{"home":{}}}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	if old.Config.Home.SunriseEntity != "" || old.Config.Home.SunsetEntity != "" {
		t.Errorf("a file from before the sensors set %q and %q", old.Config.Home.SunriseEntity, old.Config.Home.SunsetEntity)
	}
}
