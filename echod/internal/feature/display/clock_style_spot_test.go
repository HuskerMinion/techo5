//go:build spot

package display

import (
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Every clock style draws on the round face, alone and with a timer running; with SPOT_PREVIEW set,
// each is written there to look at.
func TestSpotClockStylesDraw(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().ClockStyle("") })
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "sunny"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(72 - 3*i), Low: float64(51 - 2*i)})
	}
	facts := styleFacts{
		rise: time.Date(2026, 9, 16, 6, 48, 0, 0, time.Local), set: time.Date(2026, 9, 16, 19, 5, 0, 0, time.Local), sunOK: true,
		days: week,
		next: []hass.Event{
			{Summary: "Dentist", Start: at.Add(83 * time.Minute), End: at.Add(143 * time.Minute)},
			{Summary: "Soccer practice at the park", Start: at.Add(233 * time.Minute), End: at.Add(293 * time.Minute)},
		},
		places: worldPlaces(nil),
	}
	named := facts
	named.named = "Binary"
	running := []timer.Countdown{{Name: "Pasta", Left: 4*time.Minute + 32*time.Second, Total: 10 * time.Minute, Active: true}}
	scenes := map[string]roundScene{
		"":        {now: at, phase: "idle", weather: sky, style: facts},
		"-timer":  {now: at, phase: "idle", weather: sky, style: facts, timers: running},
		"-oclock": {now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local), phase: "idle", weather: sky, style: facts},
		"-24h":    {now: at, phase: "idle", weather: sky, style: facts},
		"-named":  {now: at, phase: "idle", weather: sky, style: named},
	}
	dir := os.Getenv("SPOT_PREVIEW")
	for _, st := range clockStyles {
		if err := config.Set().Screen().ClockStyle(st.value); err != nil {
			t.Fatal(err)
		}
		for suffix, s := range scenes {
			clock24.Store(suffix == "-24h")
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			newRoundRenderer(img).draw(s)
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, "style-"+st.label+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
	clock24.Store(false)
}

// The Sun style's rim is drawn every frame the face is up: one pass over the ring.
func BenchmarkSunRim(b *testing.B) {
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	s := roundScene{now: at, phase: "idle", style: styleFacts{rise: at.Add(-7 * time.Hour), set: at.Add(5 * time.Hour), sunOK: true}}
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	for b.Loop() {
		r.sunRim(s)
	}
}

func BenchmarkPlainRim(b *testing.B) {
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	for b.Loop() {
		r.arc(rimIn, rimOut, 0, 2*math.Pi, colTrack)
	}
}
