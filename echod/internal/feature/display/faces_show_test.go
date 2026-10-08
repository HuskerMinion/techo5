//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// One frame of the Glow style, the whole clock page, on the Show 5's and the Show 8's panels.
func BenchmarkGlowFrame(b *testing.B) {
	config.Use(filepath.Join(b.TempDir(), "state.json"))
	b.Cleanup(func() { _ = config.Set().Screen().ClockStyle("") })
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	for _, style := range []string{styleGlow, styleClassic} {
		if err := config.Set().Screen().ClockStyle(style); err != nil {
			b.Fatal(err)
		}
		name := style
		if name == "" {
			name = "classic"
		}
		for _, panel := range []struct {
			name       string
			wide, high int
		}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
			b.Run(name+"-"+panel.name, func(b *testing.B) {
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
				s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "sunny", Temp: "72°"}}
				for b.Loop() {
					s.now = s.now.Add(time.Second)
					r.draw(s)
				}
			})
		}
	}
}
