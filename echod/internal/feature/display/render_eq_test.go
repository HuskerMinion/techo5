//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func eqScenes(now time.Time) map[string]scene {
	listen, listenPk := eqVoice(0.62, 1)
	reply, replyPk := eqVoice(0.95, 7)
	night, nightPk := eqVoice(0.8, 11)
	think := eqFor("thinking", false, now)
	return map[string]scene{
		"eq-listening": {now: now, phase: "listening", eq: &eqView{level: listen, peak: listenPk}},
		"eq-thinking":  {now: now, phase: "thinking", heard: "What's the weather tomorrow?", eq: think},
		"eq-replying": {now: now, phase: "replying", heard: "What's the weather tomorrow?",
			reply: "Tomorrow will be sunny, with a high of 74 and a low of 51.", eq: &eqView{level: reply, peak: replyPk}},
		"eq-night": {now: now, phase: "replying", heard: "Turn off the bedroom light", reply: "Bedroom light is off.",
			eq: &eqView{level: night, peak: nightPk, night: true}},
		"eq-long": {now: now, phase: "lingering", heard: "Tell me about the Apollo program",
			reply: "The Apollo program was the United States human spaceflight program that landed the first humans on the Moon, from 1969 to 1972, with six successful landings and twelve astronauts walking on its surface.",
			eq:    &eqView{level: make([]float64, eqBands), peak: make([]float64, eqBands), quiet: true}},
	}
}

// Every equalizer page draws on both panels without going outside them; EQ_PREVIEW names a folder to
// write them to, to look at without a device.
func TestEqualizerDraws(t *testing.T) {
	dir := os.Getenv("EQ_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{
		{"", showWide, showHigh},
		{"-show8", show8Wide, show8High},
	} {
		for name, s := range eqScenes(time.Date(2026, 9, 27, 20, 0, 0, 0, time.Local)) {
			for _, style := range []string{"bars", "wave", "vu", "orb", "hal", "face"} {
				s.eq.wave, s.eq.style, s.eq.phase = style == "wave", style, s.phase
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				faceState.set = false // each a face of its own, not the last one's easing toward it
				newRenderer(img).draw(s)
				if dir == "" {
					continue
				}
				f, err := os.Create(filepath.Join(dir, style+"-"+name+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
		}
	}
}

// One replying frame, the page the bars spend longest on.
func BenchmarkEqualizerFrame(b *testing.B) {
	for _, panel := range []struct {
		name       string
		wide, high int
	}{
		{"show5", showWide, showHigh},
		{"show8", show8Wide, show8High},
	} {
		for _, style := range []string{"bars", "wave", "vu", "orb", "hal", "face"} {
			b.Run(panel.name+"-"+style, func(b *testing.B) {
				s := eqScenes(time.Now())["eq-replying"]
				s.eq.wave, s.eq.style, s.eq.phase = style == "wave", style, s.phase
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
				for i := range b.N {
					s.now = s.now.Add(66 * time.Millisecond * time.Duration(i%2))
					r.draw(s)
				}
			})
		}
	}
}
