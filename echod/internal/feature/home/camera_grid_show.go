//go:build !dot && !spot

package home

import (
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"
)

// gridActions puts the camera grid up from Home Assistant: the cameras named, comma separated, or the
// device's list when none are, for seconds (the default for none). On the Show only: the Spot's round
// panel is one camera's.
func (f *Feature) gridActions() []*esphome.Action {
	return []*esphome.Action{{
		Name: "home_show_cameras",
		Args: []esphome.Arg{{Name: "cameras", Type: esphome.ArgString}, {Name: "seconds", Type: esphome.ArgInt}},
		Run: func(c esphome.Call) (any, error) {
			var entities []string
			for _, e := range strings.Split(c.String("cameras"), ",") {
				if e = strings.TrimSpace(e); e != "" {
					entities = append(entities, e)
				}
			}
			return nil, f.ShowGrid(entities, min(cameraSeconds(c), MaxGridFor))
		},
	}}
}
