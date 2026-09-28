package home

import (
	"log/slog"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// PTZFor is what Home Assistant turns for the camera shown, or "" when it has nothing and the
// turning button is not drawn.
func PTZFor(entity string) string { return config.Get().Home.CameraPTZ[entity] }

// cameraPTZAction names, for a camera shown, what Home Assistant turns for it. The picture and the
// turning often come through different entities — a restream for the one, the ONVIF integration for
// the other — and nothing in Home Assistant links the two, so it is said here once. The target is an
// ONVIF camera entity, or a script.* for a camera onvif.ptz cannot turn, called with direction up,
// down, left or right. An empty ptz takes the turning button away from that camera again.
//
// It is an action of its own rather than an argument on home_show_camera: Home Assistant registers
// an action's arguments as a closed, all-required set, so an argument added there would break every
// automation already calling it. And it is said once, not on every view, because which entity turns
// a camera does not change from one view to the next.
func (f *Feature) cameraPTZAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_camera_ptz",
		Args: []esphome.Arg{{Name: "entity", Type: esphome.ArgString}, {Name: "ptz", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			entity, ptz := strings.TrimSpace(c.String("entity")), strings.TrimSpace(c.String("ptz"))
			if err := config.Set().Home().CameraPTZ(entity, ptz); err != nil {
				return nil, err
			}
			slog.Info("home: camera turning set", "entity", entity, "ptz", ptz)
			f.Changed.Emit(struct{}{})
			return nil, nil
		},
	}
}
