package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// How long a camera opened from the screen stays up: a minute unless somebody chose otherwise, up to
// staying until it is tapped closed, for a screen kept as a camera monitor. A camera asked for by voice
// keeps its own shorter time, and Home Assistant's home_show_camera its own seconds.

// cameraTimes are the choices, in the order the screen and Home Assistant offer them. Minutes 0 is
// the default of one minute; -1 is until tapped.
var cameraTimes = []struct {
	label   string
	minutes int
}{
	{"1 minute", 0},
	{"5 minutes", 5},
	{"15 minutes", 15},
	{"1 hour", 60},
	{"Until tapped", -1},
}

// untilTapped is how long "until tapped" keeps a camera up: a year, which a tap ends long before.
const untilTapped = 365 * 24 * time.Hour

// cameraTimeOptions are the choices' labels.
func cameraTimeOptions() []string {
	out := make([]string, len(cameraTimes))
	for i, t := range cameraTimes {
		out[i] = t.label
	}
	return out
}

// cameraTimeIndex is the saved choice's place in cameraTimes; a value no choice has reads as the default.
func cameraTimeIndex() int {
	m := config.Get().Screen.CameraMinutes
	for i, t := range cameraTimes {
		if t.minutes == m {
			return i
		}
	}
	return 0
}

// cameraScreenTime is how long a camera opened from the screen stays up.
func cameraScreenTime() time.Duration {
	switch m := cameraTimes[cameraTimeIndex()].minutes; {
	case m < 0:
		return untilTapped
	case m == 0:
		return time.Minute
	default:
		return time.Duration(m) * time.Minute
	}
}

// setCameraTime saves the choice at i and shows it in Home Assistant.
func setCameraTime(s *esphome.Select, i int) {
	if i < 0 || i >= len(cameraTimes) {
		return
	}
	if err := config.Set().Screen().CameraMinutes(cameraTimes[i].minutes); err != nil {
		slog.Error("saving the camera time failed", "err", err)
		return
	}
	s.Set(cameraTimes[i].label)
}

// cameraTimeSelect is the Home Assistant setting.
func cameraTimeSelect() *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_camera_time",
			Name:     "Camera time on screen",
			Icon:     "mdi:cctv",
			Category: esphome.CategoryConfig,
		},
		Options: cameraTimeOptions(),
	}
	s.OnCommand = func(v string) {
		for i, t := range cameraTimes {
			if t.label == v {
				setCameraTime(s, i)
				return
			}
		}
	}
	return s
}
