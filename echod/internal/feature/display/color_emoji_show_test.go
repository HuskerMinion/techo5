//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An emoji in the Next up card on the red night clock is in the night's red like the rest of it, on
// every style.
func TestAnEmojiOnTheNightClockIsInTheNightsRed(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	colorEmojiFace(t)
	at := time.Date(2026, 11, 2, 3, 47, 0, 0, time.Local)
	panel := showPanels()[0]
	for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
		s := nightInfoFrame(at, style, true, false)
		plain := drawNight(panel.wide, panel.high, s)
		s.nextUp.ev.Summary = "🔴 " + s.nextUp.ev.Summary
		got := drawNight(panel.wide, panel.high, s)
		if samePixels(got, plain) {
			t.Fatalf("%s: the emoji was not drawn", style)
		}
		if p, c, bad := brighterThanNight(got, style == nightStyleFlip); bad {
			t.Errorf("%s: a pixel the night cannot make at %v: %v", style, p, c)
		}
	}
}
