//go:build !dot

package display

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Sunrise and sunset sensors for the tests (#747): made-up entities, at 9:12 and 16:45 on the day.
const testRiseSensor, testSetSensor = "sensor.sunrise_effective", "sensor.sunset_effective"

var sensorRise, sensorSet = time.Date(2026, 9, 16, 9, 12, 0, 0, time.Local), time.Date(2026, 9, 16, 16, 45, 0, 0, time.Local)

// withSunSensors sets the sensors and has Home Assistant send their times, as a timestamp sensor's
// state is written: ISO 8601, in UTC with its offset.
func withSunSensors(t *testing.T) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().SunEntities(testRiseSensor, testSetSensor); err != nil {
		t.Fatal(err)
	}
	for e, at := range map[string]time.Time{testRiseSensor: sensorRise, testSetSensor: sensorSet} {
		msg := &api.HomeAssistantStateResponse{EntityId: e, State: at.UTC().Format("2006-01-02T15:04:05+00:00")}
		if err := hastate.Get().Handle(context.Background(), nil, msg); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = config.Set().Home().SunEntities("", "") })
}

// The sensors' times reach the Sun clock's arc, the night sky the weather shows and the weather art's
// part of the day: at half past eight the almanac has the sun up, the hills do not.
func TestTheSunSensorsReachTheScreen(t *testing.T) {
	withSunSensors(t)
	at := time.Date(2026, 9, 16, 8, 30, 0, 0, time.Local)

	f := styleFactsFor(styleSun, at)
	if !f.sunOK || !f.rise.Equal(sensorRise) || !f.set.Equal(sensorSet) {
		t.Errorf("the Sun clock's day is %v to %v (%v), want the sensors' %v to %v", f.rise, f.set, f.sunOK, sensorRise, sensorSet)
	}
	if got := weatherNow(home.Weather{}, forecastDays{{When: at, Condition: "sunny"}}, at); got != "clear-night" {
		t.Errorf("before the sensor's sunrise the weather shows %q, want clear-night", got)
	}
	if got := weatherNow(home.Weather{}, forecastDays{{When: at, Condition: "sunny"}}, sensorRise.Add(2*time.Hour)); got != "sunny" {
		t.Errorf("after the sensor's sunrise the weather shows %q, want sunny", got)
	}
	rise, set, ok := home.Get().SunTimes(at)
	if got := artTimeFor(at, rise, set, ok); got != artNight {
		t.Errorf("the weather art at half past eight is %v, want night (the sun 42 minutes off)", got)
	}
	if got := artTimeFor(sensorRise.Add(-20*time.Minute), rise, set, ok); got != artDawn {
		t.Errorf("the weather art 20 minutes before the sensor's sunrise is %v, want dawn", got)
	}
	if got := artTimeFor(sensorSet.Add(time.Hour), rise, set, ok); got != artNight {
		t.Errorf("the weather art an hour after the sensor's sunset is %v, want night", got)
	}
}

// setStyleForTest chooses a clock style for one test.
func setStyleForTest(t *testing.T, style string) error {
	t.Helper()
	t.Cleanup(func() { _ = config.Set().Screen().ClockStyle("") })
	return config.Set().Screen().ClockStyle(style)
}
