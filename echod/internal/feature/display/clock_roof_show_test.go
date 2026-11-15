//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The classic clock's digits never stand up into the weather's line, at any text size on any panel,
// with the next-up card under them or not: where the text size's face would, a smaller one is drawn.
// At 150% on a Show 5 the digits ran over the weather. The digits are the only words in the text
// color above the card.
func TestTheClockNeverReachesTheWeather(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) })
	scenes := map[string]scene{
		"clock":         {now: lookNow, phase: "idle", weather: hassSunny()},
		"clock-next-up": {now: lookNow, phase: "idle", weather: hassSunny(), nextUp: lookCard()},
	}
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range showPanels() {
			for name, s := range scenes {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				r := newRenderer(img)
				r.draw(s)
				foot := r.clockRoof(s) - r.s(4)
				top := panel.high
				for y := 0; y < panel.high/2 && top == panel.high; y++ {
					for x := range panel.wide {
						if img.RGBAAt(x, y) == cream {
							top = y
							break
						}
					}
				}
				if top < foot {
					t.Errorf("%d%% on the %s, %s: the digits reach y=%d, the weather's line ends at %d", size, panel.name, name, top, foot)
				}
			}
		}
	}
}

// The next-up card's title keeps a few words' room at every text size: the time after it and then
// "Next:" give way first. At 150% it was cut to "Pu…". The title is the card's only words in the text
// color on its first line.
func TestTheNextUpTitleKeepsItsRoom(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) })
	card := lookCard()
	card.ev.Summary = "Pumpkin patch and hayride with the neighbors"
	s := scene{now: lookNow, phase: "idle", weather: hassSunny(), nextUp: card}
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range showPanels() {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(s)
			box, _ := r.nextUpRect(s)
			left, right := panel.wide, 0
			for y := box.Min.Y; y < box.Min.Y+r.s(50); y++ {
				for x := box.Min.X; x < box.Max.X; x++ {
					if img.RGBAAt(x, y) == cream {
						left, right = min(left, x), max(right, x)
					}
				}
			}
			if want := r.atTextSize(r.s(220)) - r.s(20); right-left < want {
				t.Errorf("%d%% on the %s: the title is %d wide, want %d at least", size, panel.name, right-left, want)
			}
		}
	}
}
