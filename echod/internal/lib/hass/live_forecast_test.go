package hass

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A forecast card's forecast is followed with weather/subscribe_forecast, which sends the forecast
// as it is and again whenever the integration updates it, as Home Assistant's own card has it (#758).
func TestAForecastIsFollowed(t *testing.T) {
	l, asked := scriptedLive(t, func(cmd map[string]any) (string, []string) {
		if cmd["type"] != "weather/subscribe_forecast" {
			return "", nil
		}
		return `null`, []string{
			`{"type":"daily","forecast":[{"datetime":"2026-11-26T12:00:00+00:00","condition":"rainy","temperature":12.4,"templow":6,"precipitation_probability":80},
				{"datetime":"2026-11-27T12:00:00+00:00","condition":"sunny","temperature":14}]}`,
			`{"type":"daily","forecast":null}`,
		}
	})
	var mu sync.Mutex
	var got [][]Day
	if err := l.SubscribeForecast(context.Background(), "weather.home", "daily", func(d []Day) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two updates", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 2
	})
	cmd := asked()[0]
	if cmd["entity_id"] != "weather.home" || cmd["forecast_type"] != "daily" {
		t.Errorf("asked %v", cmd)
	}
	mu.Lock()
	defer mu.Unlock()
	first := got[0]
	if len(first) != 2 || first[0].Condition != "rainy" || !first[0].HasHigh || !first[0].HasLow || first[0].Rain != 80 {
		t.Fatalf("first: %+v", first)
	}
	// A day with no low says so, rather than reading as 0°.
	if first[1].HasLow || !first[1].HasHigh || first[1].High != 14 {
		t.Errorf("a day with no low: %+v", first[1])
	}
	if len(got[1]) != 0 {
		t.Errorf("a forecast of none: %+v", got[1])
	}
}

// Where the subscription is refused, the forecast is read with weather.get_forecasts over the same
// connection, the response under the entity's id; a twice-daily one marks its nights.
func TestAForecastIsReadOnce(t *testing.T) {
	l, asked := scriptedLive(t, func(cmd map[string]any) (string, []string) {
		if cmd["type"] != "call_service" {
			return "", nil
		}
		return `{"context":{"id":"x"},"response":{"weather.home":{"forecast":[
			{"datetime":"2026-11-26T06:00:00Z","condition":"sunny","temperature":15,"templow":8,"is_daytime":true},
			{"datetime":"2026-11-26T18:00:00Z","condition":"clear-night","temperature":8,"is_daytime":false}]}}}`, nil
	})
	if err := l.SubscribeForecast(context.Background(), "weather.home", "twice_daily", func([]Day) {}); err == nil {
		t.Fatal("a refused subscription was not an error")
	}
	days, err := l.GetForecast(context.Background(), "weather.home", "twice_daily")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0].Night || !days[1].Night {
		t.Fatalf("days %+v", days)
	}
	cmd := asked()[1]
	if cmd["domain"] != "weather" || cmd["service"] != "get_forecasts" || cmd["return_response"] != true ||
		fmt.Sprint(cmd["target"]) != "map[entity_id:weather.home]" || fmt.Sprint(cmd["service_data"]) != "map[type:twice_daily]" {
		t.Errorf("asked %v", cmd)
	}
	if _, err := l.GetForecast(context.Background(), "weather.other", "daily"); err == nil {
		t.Error("a forecast for another entity was taken for this one's")
	}
}

// An hour whose time can't be read is put where it comes in the list, an hour apart from now.
func TestHoursWithoutTimesAreAnHourApart(t *testing.T) {
	days := forecastDays([]forecastEntry{{Datetime: "?"}, {Datetime: "?"}}, forecastStep("hourly"))
	if d := days[1].When.Sub(days[0].When); d != time.Hour {
		t.Errorf("%v apart", d)
	}
}
