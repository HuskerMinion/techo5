//go:build !dot && !spot

package display

import (
	"log/slog"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Call screen: how a call being placed or up is drawn. On a page of its own over everything, as it
// always has been; or as the red Hang up button over whatever page is showing, alone or with who the
// call is with and how long it has lasted beside it (render_callbar.go). A call ringing keeps its page
// whatever this says.

type callScreenChoice struct {
	label string
	value string
	style callBarStyle
}

// callScreens are the choices, with what the config keeps for each; the page is kept as nothing.
var callScreens = []callScreenChoice{
	{"Full screen", "", callBarNone},
	{"Hang-up button", "button", callBarButton},
	{"Hang-up button and details", "details", callBarDetails},
}

// callScreen is the choice's style, kept here, as callButton is, so each frame reads it without the
// config's lock.
var callScreen atomic.Int32

// hasCallScreen is whether the setting is offered: on the Show. The Spot's round call page stays
// (callscreen_spot.go).
const hasCallScreen = true

func callScreenOptions() []string {
	out := make([]string, len(callScreens))
	for i, c := range callScreens {
		out[i] = c.label
	}
	return out
}

// callScreenIndex is the saved choice's place in callScreens; a value no choice has reads as Full
// screen.
func callScreenIndex() int {
	v := config.Get().Screen.CallScreen
	for i, c := range callScreens {
		if c.value == v {
			return i
		}
	}
	return 0
}

// callScreenSelect is the Home Assistant setting; wake redraws the screen once it changes.
func callScreenSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_call_screen",
			Name:     "Call screen",
			Icon:     "mdi:phone-in-talk",
			Category: esphome.CategoryConfig,
		},
		Options: callScreenOptions(),
	}
	s.OnCommand = func(v string) {
		for i, c := range callScreens {
			if c.label == v {
				setCallScreen(s, i)
				wake()
				return
			}
		}
	}
	return s
}

// setCallScreen saves the choice at i and shows it in Home Assistant.
func setCallScreen(s *esphome.Select, i int) {
	if i < 0 || i >= len(callScreens) {
		return
	}
	if err := config.Set().Screen().CallScreen(callScreens[i].value); err != nil {
		slog.Error("saving the call screen failed", "err", err)
		return
	}
	showCallScreen(s, i)
	slog.Info("screen: call screen", "is", callScreens[i].label)
}

// showCallScreen puts the choice at i in force without saving it: at start, from the config.
func showCallScreen(s *esphome.Select, i int) {
	callScreen.Store(int32(callScreens[i].style))
	if s != nil {
		s.Set(callScreens[i].label)
	}
}

// chooseCallScreen is the settings screen's list for it.
func (d *Display) chooseCallScreen(i int) {
	setCallScreen(d.callScreenSel, i)
	d.wake()
}
