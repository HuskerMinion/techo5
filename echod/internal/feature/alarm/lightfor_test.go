package alarm

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Wake light minutes in Home Assistant sets the default wake light to one of the choices the screen
// offers, and follows it however it was set.
func TestWakeLightMinutesInHomeAssistant(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	if a.lightFor.Category != a.snoozeFor.Category || a.lightFor.Unit != "min" || a.lightFor.Min != 0 || a.lightFor.Max != 30 {
		t.Errorf("the entity is %+v", a.lightFor)
	}
	for _, c := range []struct{ set, want int }{{15, 15}, {12, 10}, {25, 30}, {-5, 0}, {99, 30}, {0, 0}} {
		a.lightFor.OnCommand(float32(c.set))
		if got := config.Get().Alarms.SunriseMinutes; got != c.want || a.lightFor.Get() != float32(c.want) {
			t.Errorf("set to %d: saved %d, the entity says %v, want %d", c.set, got, a.lightFor.Get(), c.want)
		}
	}
	if err := config.Set().Alarms().SunriseMinutes(20); err != nil {
		t.Fatal(err)
	}
	a.Restore(config.Get())
	if a.lightFor.Get() != 20 {
		t.Errorf("after a restart the entity says %v", a.lightFor.Get())
	}
}
