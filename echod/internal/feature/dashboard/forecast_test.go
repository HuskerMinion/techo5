//go:build !dot

package dashboard

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

func compileCard(card raw) (node, needs) {
	c := &compiler{seen: map[string]bool{}, need: needs{graphs: map[string]int{}}}
	return c.card(card), c.need
}

// #758's card is the weather now over the coming days, which it asks for by its forecast_type, rather
// than a tile of the weather alone.
func TestAForecastCardAsksForItsDays(t *testing.T) {
	n, need := compileCard(raw{"type": "weather-forecast", "entity": "weather.forecast_home", "forecast_type": "hourly"})
	f, ok := n.(forecastNode)
	if !ok {
		t.Fatalf("compiled to %T", n)
	}
	if len(need.forecasts) != 1 || need.forecasts[0] != (forecastWant{"weather.forecast_home", "hourly"}) || !f.current || f.max != forecastSlots {
		t.Fatalf("node %+v, needs %+v", f, need.forecasts)
	}
	if len(need.entities) != 1 || need.entities[0] != "weather.forecast_home" {
		t.Errorf("follows %v", need.entities)
	}
	// Daily when it says nothing; forecast_slots and show_current as it says.
	n, need = compileCard(raw{"type": "weather-forecast", "entity": "weather.home", "forecast_slots": 3.0, "show_current": false})
	if f := n.(forecastNode); f.want.kind != "daily" || f.max != 3 || f.current {
		t.Errorf("node %+v", f)
	}
	// show_forecast false is the tile it was, and asks for no forecast.
	n, need = compileCard(raw{"type": "weather-forecast", "entity": "weather.home", "show_forecast": false})
	if _, ok := n.(listNode); !ok || len(need.forecasts) != 0 {
		t.Errorf("no forecast: %T, %v", n, need.forecasts)
	}
	if n, _ := compileCard(raw{"type": "weather-forecast"}); n != nil {
		t.Errorf("a card with no entity: %+v", n)
	}
}

// The card's block has the weather now as its tile would say it, and the days from today on: a day
// already over, and a twice-daily forecast's nights, are left out.
func TestAForecastCardsBlock(t *testing.T) {
	now := time.Now()
	day := func(offset int, cond string) hass.Day {
		return hass.Day{When: now.AddDate(0, 0, offset), Condition: cond, High: 20, HasHigh: true, Low: 10, HasLow: true}
	}
	night := day(1, "clear-night")
	night.Night = true
	w := forecastWant{"weather.home", "twice_daily"}
	l := look{
		states: map[string]hass.LiveEntity{"weather.home": {ID: "weather.home", State: "partlycloudy",
			Attrs: map[string]any{"friendly_name": "Forecast Home", "temperature": 22.0}}},
		forecasts: map[string][]hass.Day{w.key(): {day(-1, "rainy"), day(0, "sunny"), night, day(1, "cloudy")}},
	}
	b := forecastNode{item: tileItem{entity: "weather.home"}, want: w, current: true, max: 5}.blocks(l)
	if len(b) != 1 || b[0].Forecast == nil {
		t.Fatalf("blocks %+v", b)
	}
	f := b[0].Forecast
	if f.Name != "Forecast Home" || f.Condition != "Partly cloudy" || f.Temp != "22°" || f.Icon != "weather-partly-cloudy" || !f.Current {
		t.Errorf("now: %+v", f)
	}
	if len(f.Days) != 2 || f.Days[0].Condition != "sunny" || f.Days[1].Condition != "cloudy" {
		t.Errorf("days %+v", f.Days)
	}
	// Before the forecast has come, the card is the weather now.
	l.forecasts = nil
	if b := (forecastNode{item: tileItem{entity: "weather.home"}, want: w, current: true}).blocks(l); len(b[0].Forecast.Days) != 0 {
		t.Errorf("days before any came: %+v", b[0].Forecast.Days)
	}
}

// An hour is over once the next has begun.
func TestAnHourIsOverAtTheNext(t *testing.T) {
	now := time.Date(2026, 11, 26, 14, 30, 0, 0, time.Local)
	if !over(hass.Day{When: now.Add(-90 * time.Minute)}, true, now) || over(hass.Day{When: now.Add(-20 * time.Minute)}, true, now) {
		t.Error("hours")
	}
	if over(hass.Day{When: time.Date(2026, 11, 26, 0, 0, 0, 0, time.Local)}, false, now) || !over(hass.Day{When: now.AddDate(0, 0, -1)}, false, now) {
		t.Error("days")
	}
	if over(hass.Day{}, false, now) {
		t.Error("a day with no time is never over")
	}
}

// A weather tile's icon is its condition's.
func TestAWeatherTilesIconIsItsCondition(t *testing.T) {
	if got := describe(hass.LiveEntity{ID: "weather.home", State: "rainy", Attrs: map[string]any{}}, "", nil).Icon; got != "weather-rainy" {
		t.Errorf("icon %q", got)
	}
}
