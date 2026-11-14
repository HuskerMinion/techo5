//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// An event's box on the day's hours keeps its words inside it, however tall the box is, on every
// panel and at every text size: an hour and a half on a Show 8 is 48 of the Show 5's pixels tall, and
// the second of its two lines, written 48 down, hung out of the foot of the box. A box of two hours
// still has both lines. Boxes are tried from a line of the small words up: the least an event is drawn
// (minBlockPx) is shorter than that at the larger text sizes, and a tail shows under it.
func TestADaysEventKeepsItsWordsInItsBox(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	start := time.Date(2026, 10, 18, 17, 30, 0, 0, time.Local)
	ev := hass.Event{Summary: "Soccer practice (jersey)", Calendar: lookOrder[0], Start: start, End: start.Add(90 * time.Minute), Location: "Quarry field"}
	v := lookView()
	paper := image.NewUniform(color.RGBA{255, 0, 255, 255})
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range showPanels() {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			draw.Draw(img, img.Rect, paper, image.Point{}, draw.Src)
			top, room := r.s(40), r.s(30)
			line := r.micro.Metrics().Ascent.Ceil() + r.micro.Metrics().Descent.Ceil()
			for h := max(r.s(minBlockPx), line); h <= r.s(100); h++ {
				b := image.Rect(r.s(80), top, r.w-r.s(80), top+h)
				r.timeBlock(v, r.look(), ev, b, false)
				out := 0
				for y := b.Min.Y - room; y < b.Max.Y+room; y++ {
					for x := 0; x < panel.wide && (y < b.Min.Y || y >= b.Max.Y); x++ {
						if img.RGBAAt(x, y) != paper.C {
							out++
						}
					}
				}
				if out > 0 {
					t.Errorf("%d%% on the %s: a box %d tall has %d pixels of its words outside it", size, panel.name, h, out)
				}
				draw.Draw(img, image.Rect(0, b.Min.Y-room, panel.wide, b.Max.Y+room), paper, image.Point{}, draw.Src)
			}
			// Two hours, as the day's page draws them: the title's line and the time's under it.
			b := image.Rect(r.s(80), top, r.w-r.s(80), top+2*r.dayHourHeight()-r.s(3))
			r.timeBlock(v, r.look(), ev, b, false)
			sub, lines := evSub(v.color(ev.Calendar)), 0
			for y := (b.Min.Y + b.Max.Y) / 2; y < b.Max.Y && lines == 0; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if img.RGBAAt(x, y) == sub {
						lines++
						break
					}
				}
			}
			if lines == 0 {
				t.Errorf("%d%% on the %s: two hours have no second line", size, panel.name)
			}
		}
	}
}

// Coming up's day headings stand clear of the rule under them, tails and all: no word of a heading is
// on the rule's rows, at any text size on any panel. And only today's heading is in the accent; the
// other days' are in the text color, so the events are what the page is about.
func TestComingUpsHeadingsClearTheirRule(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range showPanels() {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(calendarScenes()["calendar-agenda"])
			heads := 0
			for _, h := range r.calHits {
				if h.kind != calDay {
					continue
				}
				heads++
				today := sameDay(h.day, lookNow)
				// The heading's words are at its left; its weather, at the right, has colors of its own.
				words := image.Rect(h.r.Min.X, h.r.Min.Y, h.r.Min.X+r.width(r.tiny, agendaDayName(h.day, lookNow)), h.r.Max.Y)
				var inAccent, inText, onRule int
				for y := words.Min.Y; y < words.Max.Y; y++ {
					for x := words.Min.X; x < words.Max.X; x++ {
						switch c := img.RGBAAt(x, y); {
						case y >= h.r.Max.Y-r.s(3):
							// The rule and its soft edges are within a step of the ground; words are not.
							if colorContrast(c, walnut) > 2 {
								onRule++
							}
						case c == amber:
							inAccent++
						case c == cream:
							inText++
						}
					}
				}
				if onRule > 0 {
					t.Errorf("%d%% on the %s: the heading for %s has %d pixels of its words on the rule", size, panel.name, h.day.Format("Jan 2"), onRule)
				}
				if today && (inAccent == 0 || inText > 0) || !today && (inText == 0 || inAccent > 0) {
					t.Errorf("%d%% on the %s: the heading for %s has %d pixels in the accent and %d in the text color", size, panel.name, h.day.Format("Jan 2"), inAccent, inText)
				}
			}
			if heads < 2 {
				t.Errorf("%d%% on the %s: %d headings were drawn", size, panel.name, heads)
			}
		}
	}
}
