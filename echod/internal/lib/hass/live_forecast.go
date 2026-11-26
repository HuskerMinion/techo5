package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// A weather entity's forecast over the live connection, for a forecast card (#758). A weather
// entity's state has carried no forecast since Home Assistant 2024.3: it is asked for, by type
// (daily, hourly or twice_daily), and followed as the integration updates it.

// SubscribeForecast calls got with the entity's forecast of a kind as it is now, and again whenever the
// integration updates it (weather/subscribe_forecast, which Home Assistant's own forecast card uses), on
// the connection's reader, so it must not block. A forecast the entity has none of just now arrives as
// no days.
func (l *Live) SubscribeForecast(ctx context.Context, entity, kind string, got func([]Day)) error {
	step := forecastStep(kind)
	handle := func(raw json.RawMessage) {
		var ev struct {
			Forecast []forecastEntry `json:"forecast"`
		}
		if json.Unmarshal(raw, &ev) == nil {
			got(forecastDays(ev.Forecast, step))
		}
	}
	_, ch, err := l.send(map[string]any{"type": "weather/subscribe_forecast", "entity_id": entity, "forecast_type": kind}, handle)
	if err != nil {
		return err
	}
	select {
	case r := <-ch:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return errors.New("hass: the connection ended")
	}
}

// GetForecast is the entity's forecast of a kind as it is now (weather.get_forecasts), for a Home
// Assistant that will not take a subscription to it.
func (l *Live) GetForecast(ctx context.Context, entity, kind string) ([]Day, error) {
	raw, err := l.Call(ctx, map[string]any{"type": "call_service", "domain": "weather", "service": "get_forecasts",
		"service_data": map[string]any{"type": kind}, "target": map[string]any{"entity_id": entity}, "return_response": true})
	if err != nil {
		return nil, err
	}
	var res struct {
		Response map[string]struct {
			Forecast []forecastEntry `json:"forecast"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	fc, ok := res.Response[entity]
	if !ok {
		return nil, fmt.Errorf("hass: no forecast for %s", entity)
	}
	return forecastDays(fc.Forecast, forecastStep(kind)), nil
}
