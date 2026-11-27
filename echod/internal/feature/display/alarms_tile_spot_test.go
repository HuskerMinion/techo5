//go:build spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A card that navigates to #techo5-alarms opens the settings on Alarms & Timers (#651), as on the Show.
func TestTheAlarmsTileOpensTheAlarms(t *testing.T) {
	d := &Display{poke: make(chan struct{}, 1)}
	d.tileAction(&dashTile{action: &dashboard.Action{View: dashboard.AlarmsPath}})
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sheetOpen || d.sheetGrid || d.cat != catAlarms {
		t.Errorf("sheet %v, on the six %v, on %v", d.sheetOpen, d.sheetGrid, d.cat)
	}
}

// A card that navigates to #techo5-weather opens the weather face on the forecast, and one that
// navigates to #techo5-radar on the rain map (#772), as the weather in the ring menu does. On a device
// set to no weather they do nothing.
func TestTheWeatherTilesOpenTheWeather(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{poke: make(chan struct{}, 1)}
	for _, c := range []struct {
		view  string
		radar bool
	}{{dashboard.WeatherPath, false}, {dashboard.RadarPath, true}, {dashboard.WeatherPath, false}} {
		d.tileAction(&dashTile{action: &dashboard.Action{View: c.view}})
		d.mu.Lock()
		open, mode, radar, up := d.menuOpen, d.menuMode, d.radar, time.Now().Before(d.weatherUntil)
		d.closeMenu()
		d.mu.Unlock()
		if !open || mode != modeWeather || radar != c.radar || !up {
			t.Errorf("%s: menu %v on %v, radar %v, up %v", c.view, open, mode, radar, up)
		}
	}

	if err := config.Set().Home().Weather(config.WeatherOff); err != nil {
		t.Fatal(err)
	}
	d.weatherUntil = time.Time{}
	d.tileAction(&dashTile{action: &dashboard.Action{View: dashboard.RadarPath}})
	if d.menuOpen || !d.weatherUntil.IsZero() {
		t.Errorf("with no weather: menu %v, up until %v", d.menuOpen, d.weatherUntil)
	}
}
