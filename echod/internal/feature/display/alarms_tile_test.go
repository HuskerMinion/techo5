//go:build !dot && !spot

package display

import (
	"fmt"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A card that navigates to #techo5-alarms opens the settings on Alarms & Timers (#651), over the
// dashboard, as a card navigating to #techo5-library opens the library.
func TestTheAlarmsTileOpensTheAlarms(t *testing.T) {
	d := &Display{poke: make(chan struct{}, 1)}
	d.dash = true
	d.tileAction(&dashTile{action: &dashboard.Action{View: dashboard.AlarmsPath}})
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sheet || d.cat != catAlarms || d.draft != nil {
		t.Errorf("sheet %v on %v, draft %v", d.sheet, d.cat, d.draft)
	}
}

// A card that navigates to #techo5-weather opens the forecast on the device it was tapped on, and one
// that navigates to #techo5-radar the rain map (#772), over the dashboard and over the settings, as a
// tap on the home screen's weather does. A screen already lit is not taken over for the night.
func TestTheWeatherTilesOpenTheWeather(t *testing.T) {
	d := nightTestDisplay(t)
	d.dash, d.dashShowing, d.on = true, true, true
	for _, c := range []struct {
		view  string
		radar bool
	}{{dashboard.WeatherPath, false}, {dashboard.RadarPath, true}, {dashboard.WeatherPath, false}} {
		d.mu.Lock()
		d.sheet = true
		d.mu.Unlock()
		d.tileAction(&dashTile{action: &dashboard.Action{View: c.view}})
		d.mu.Lock()
		up, radar, sheet, asked := time.Now().Before(d.weatherUntil), d.radar, d.sheet, d.askedNight
		d.mu.Unlock()
		if !up || radar != c.radar || sheet || asked {
			t.Errorf("%s: up %v, radar %v, settings %v, held for the night %v", c.view, up, radar, sheet, asked)
		}
	}
}

// On a device set to no weather the cards do nothing, as weather_show refuses there.
func TestTheWeatherTilesWithNoWeather(t *testing.T) {
	d := nightTestDisplay(t)
	if err := config.Set().Home().Weather(config.WeatherOff); err != nil {
		t.Fatal(err)
	}
	d.dash, d.dashShowing, d.on = true, true, true
	for _, view := range []string{dashboard.WeatherPath, dashboard.RadarPath} {
		d.tileAction(&dashTile{action: &dashboard.Action{View: view}})
	}
	if !d.weatherUntil.IsZero() || d.radar {
		t.Errorf("the weather page went up anyway: until %v, radar %v", d.weatherUntil, d.radar)
	}
}

// On the dashboard a tap on the night light opened, the rain map a card opens is held over the night as
// a page Home Assistant asks for is, so the red night clock does not take its place, and it stays at
// the night light's level. Once it has gone, the night has the screen back.
func TestTheRadarTileOnTheNightDashboard(t *testing.T) {
	d := nightTestDisplay(t)
	h := time.Now().Hour()
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", (h+23)%24, (h+2)%24)); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().NightLight(true); err != nil {
		t.Fatal(err)
	}
	d.on, d.nightGlow, d.ceiling = true, true, 100
	d.dash, d.dashNight, d.dashShowing = true, true, true
	d.tileAction(&dashTile{action: &dashboard.Action{View: dashboard.RadarPath}})
	d.mu.Lock()
	asked, on, glow, radar := d.askedUp(time.Now()), d.on, d.nightGlow, d.radar
	d.mu.Unlock()
	if !asked || !on || !glow || !radar {
		t.Fatalf("held for the night %v, on %v, night light %v, rain map %v", asked, on, glow, radar)
	}
	d.mu.Lock()
	d.weatherUntil = time.Time{}
	asked = d.askedUp(time.Now())
	d.mu.Unlock()
	if asked {
		t.Error("the hold outlasted the rain map")
	}
}
