//go:build !dot

package dashboard

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A graph card's line is the entity's recent history: read from Home Assistant when the page opens,
// and after that added to as the state changes.

type point struct {
	at time.Time
	v  float64
}

// loadHistory is the numeric history of entities over the last hours.
func loadHistory(ctx context.Context, live *hass.Live, entities []string, hours int) map[string][]point {
	out := map[string][]point{}
	if len(entities) == 0 {
		return out
	}
	raw, err := live.Call(ctx, map[string]any{
		"type":                     "history/history_during_period",
		"start_time":               time.Now().Add(-time.Duration(hours) * time.Hour).UTC().Format(time.RFC3339),
		"entity_ids":               entities,
		"minimal_response":         true,
		"no_attributes":            true,
		"significant_changes_only": false,
	})
	if err != nil {
		return out
	}
	var got map[string][]struct {
		S  string  `json:"s"`
		LU float64 `json:"lu"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return out
	}
	for id, list := range got {
		var h []point
		for _, p := range list {
			if v, err := strconv.ParseFloat(p.S, 64); err == nil {
				sec, frac := math.Modf(p.LU)
				h = append(h, point{at: time.Unix(int64(sec), int64(frac*1e9)), v: v})
			}
			if len(h) > 2*historyMost {
				h = keep(h, hours)
			}
		}
		out[id] = keep(h, hours)
	}
	return out
}

// historyMost is how many points an entity's history keeps: far more than a graph's width, far less
// than a sensor reporting every second for a week would pile up.
const historyMost = 1500

// keep is a history trimmed to the last hours and to historyMost points: the oldest go first, and
// past the limit every other point of the older half, so the line keeps its shape.
func keep(h []point, hours int) []point {
	cut := time.Now().Add(-time.Duration(hours) * time.Hour)
	for len(h) > 2 && h[1].at.Before(cut) {
		h = h[1:]
	}
	if len(h) <= historyMost {
		return h
	}
	half := len(h) / 2
	out := make([]point, 0, historyMost)
	for i := 0; i < half; i += 2 {
		out = append(out, h[i])
	}
	return append(out, h[half:]...)
}

// buckets is a history over the last hours as n values, oldest first: each the value at the end of
// its slice of time, carried on through slices with no change, so a sensor that reports rarely is
// a flat line rather than a gap.
func buckets(pts []point, hours, n int) []float64 {
	if len(pts) == 0 || n <= 0 {
		return nil
	}
	end := time.Now()
	start := end.Add(-time.Duration(hours) * time.Hour)
	step := end.Sub(start) / time.Duration(n)
	out := make([]float64, 0, n)
	i := 0
	have := false
	var last float64
	// The value before the window starts is where the line begins.
	for i < len(pts) && pts[i].at.Before(start) {
		last, have = pts[i].v, true
		i++
	}
	for b := 1; b <= n; b++ {
		until := start.Add(step * time.Duration(b))
		for i < len(pts) && !pts[i].at.After(until) {
			last, have = pts[i].v, true
			i++
		}
		if have {
			out = append(out, last)
		} else {
			out = append(out, math.NaN())
		}
	}
	return out
}
