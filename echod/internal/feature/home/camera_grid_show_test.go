//go:build !dot && !spot

package home

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Home Assistant's home_show_cameras puts the grid up for an hour at most, whatever it asks (#748).
func TestHomeAssistantsGridIsUpAnHourAtMost(t *testing.T) {
	f, _ := gridHouse(t, []config.Camera{{Entity: "camera.a", Name: "A"}})
	c, err := esphome.NewCall(map[string]any{"cameras": "camera.a", "seconds": 48 * 3600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.gridActions()[0].Run(c); err != nil {
		t.Fatal(err)
	}
	v, _ := f.Camera()
	if left := time.Until(v.Until); left > MaxGridFor {
		t.Errorf("up for %v, more than %v", left, MaxGridFor)
	}
}
