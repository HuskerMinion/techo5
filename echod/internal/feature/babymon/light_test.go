package babymon

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// listeners sets how many are listening on the shared monitor, for the screen's and the ring's questions.
func listeners(t *testing.T, n int) {
	f := Get()
	f.mu.Lock()
	was := f.n
	f.n = n
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.n = was
		f.mu.Unlock()
	})
}

func TestLightNames(t *testing.T) {
	for _, v := range []string{config.BabyLightNormal, config.BabyLightDim, config.BabyLightOff} {
		if got, ok := LightValue(LightName(v)); !ok || got != v {
			t.Errorf("%q named %q comes back as %q, %v", v, LightName(v), got, ok)
		}
	}
	if _, ok := LightValue("Bright"); ok {
		t.Error("an unknown choice was taken")
	}
	if LightName("bogus") != "Normal" {
		t.Error("an unknown saved value is not read as Normal")
	}
	if got, ok := LightValue("off"); !ok || got != config.BabyLightOff {
		t.Error("a value is not taken as itself")
	}
}

// Normal shows the mark as before, Dimmest shows it dim, Off not at all; nothing shows with nobody
// listening.
func TestTheLightChoiceDecidesWhatShows(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := Get()
	for _, c := range []struct {
		light      string
		n          int
		shown, dim bool
		ring       string
	}{
		{"Normal", 1, true, false, "normal"},
		{"Dimmest", 1, true, true, config.BabyLightDim},
		{"Off", 2, false, false, ""},
		{"Normal", 0, false, false, ""},
		{"Dimmest", 0, false, false, ""},
	} {
		if err := f.SetLight(c.light); err != nil {
			t.Fatal(err)
		}
		listeners(t, c.n)
		if Shown() != c.shown || Dim() != c.dim || ringFor(c.n > 0, light()) != c.ring {
			t.Errorf("%s with %d listening: shown %v dim %v ring %q", c.light, c.n, Shown(), Dim(), ringFor(c.n > 0, light()))
		}
	}
	if err := f.SetLight("Bright"); err == nil {
		t.Error("an unknown choice was saved")
	}
	if config.Get().Security.BabyLight != config.BabyLightDim {
		t.Errorf("saved %q after a refused choice", config.Get().Security.BabyLight)
	}
}

// Dimmest is one segment, steady, darker than the pulse's amber.
func TestTheDimmestRing(t *testing.T) {
	frame := dimmestFrame()
	lit := 0
	for _, c := range frame {
		if c != (led.Color{}) {
			lit++
		}
	}
	if len(frame) != led.Segments || lit != 1 || dimmestColor.R >= listeningColor.R/4 {
		t.Errorf("%d segments, %d lit, color %+v", len(frame), lit, dimmestColor)
	}
}
