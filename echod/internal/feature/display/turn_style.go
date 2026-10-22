//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// What a voice turn looks like on the screen: Classic, the title and the words, or a picture moving
// with the voice being heard and the answer being spoken: the wave, the bars, VU meters, the orb,
// HAL 9000's eye or a face (turn_draw.go, turn_visual.go, turn_face.go). Or a cue: the page stays as
// it was and the turn is a light along its edge, with the words as captions over the page's foot if
// asked for (turn_cue.go); or Rainbow, a glow of colors round the whole edge with the words on a card
// of glass (render_rainbow.go).
// Classic until somebody chooses.

// turnStyle is a choice, with what the config keeps for it.
type turnStyle struct {
	label string
	value string
}

// turnStyles are the choices; Classic is kept as nothing. Rainbow only where the screen offers it.
var turnStyles = offered([]turnStyle{
	{"Classic", ""},
	{"Wave", "wave"},
	{"Bars", "equalizer"},
	{"VU meters", "vu"},
	{"Orb", "orb"},
	{"HAL 9000", "hal"},
	{"Face", "face"},
	{"Cue light", "cue"},
	{"Cue and captions", "captions"},
	{"Rainbow", "rainbow"},
})

// offered is styles without those this screen does not offer.
func offered(styles []turnStyle) []turnStyle {
	out := styles[:0]
	for _, t := range styles {
		if t.value != "rainbow" || hasRainbow {
			out = append(out, t)
		}
	}
	return out
}

func turnStyleOptions() []string {
	out := make([]string, len(turnStyles))
	for i, t := range turnStyles {
		out[i] = t.label
	}
	return out
}

// turnStyleIndex is the saved choice's place in turnStyles; a value no choice has reads as Classic.
func turnStyleIndex() int {
	v := config.Get().Screen.TurnStyle
	for i, t := range turnStyles {
		if t.value == v {
			return i
		}
	}
	return 0
}

// setTurnStyle saves the choice at i and shows it in Home Assistant.
func setTurnStyle(s *esphome.Select, i int) {
	if i < 0 || i >= len(turnStyles) {
		return
	}
	if err := config.Set().Screen().TurnStyle(turnStyles[i].value); err != nil {
		slog.Error("saving the turn screen failed", "err", err)
		return
	}
	if s != nil {
		s.Set(turnStyles[i].label)
	}
}
