//go:build !dot && !spot

package display

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A new text size is drawn from the next frame on, with no restart, on every panel: the renderer
// makes its faces again, the settings screen's too, and the clock styles' are let go.
func TestTheShowTakesANewTextSizeAtOnce(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	s := scene{now: time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local), phase: "idle", weather: home.Weather{Condition: "sunny", Temp: "72°"}}
	for _, panel := range showPanels() {
		_ = config.Set().Screen().TextSize(0)
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		r.draw(s)
		before := bytes.Clone(img.Pix)
		body, sheet := r.body.Metrics().Height, r.faces().label.Metrics().Height

		if err := config.Set().Screen().TextSize(125); err != nil {
			t.Fatal(err)
		}
		r.draw(s)
		if r.textSize != 125 || r.body.Metrics().Height <= body || bytes.Equal(before, img.Pix) {
			t.Errorf("%s: the frame after the change is not at the new size", panel.name)
		}
		if r.faces().label.Metrics().Height <= sheet {
			t.Errorf("%s: the settings screen kept the old size", panel.name)
		}
	}
}

// At the largest step and the smallest, the words that sit in boxes of a fixed size still fit them:
// the clock across the screen, and the settings rows, whose labels and values are cut to their room
// rather than run into each other.
func TestTheLargestTextSizeStillFits(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, size := range []int{textSteps[0], textSteps[len(textSteps)-1]} {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range showPanels() {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			// The widest time there is, with its AM/PM.
			if w := r.width(r.clock, "12:48") + r.s(18) + r.width(r.ampm, "PM"); w > r.w-2*r.margin {
				t.Errorf("%d%% on the %s: the clock is %d wide, on a panel %d wide", size, panel.name, w, r.w)
			}
			// The Display card's rows: each label and line under it keeps clear of its control, cut
			// short rather than run under it, and the new row's own words are not cut at all.
			card := r.nextCard(r.w, r.h)
			fc := r.faces()
			rows, _ := categoryRows(sheetView{st: settings{cat: catDisplay}})
			for _, row := range rows {
				if row.kind == ctlHeading {
					continue
				}
				label, sub, end := r.rowWords(card, row, fc.label)
				if end+r.s(rowGap)+r.controlWidth(row) > card.Max.X-r.s(26) && row.kind != ctlSwatches && row.kind != ctlDays {
					t.Errorf("%d%% on the %s: %s runs under its control", size, panel.name, row.id)
				}
				if row.id == "textsize" && (label != row.label || sub != row.sub) {
					t.Errorf("%d%% on the %s: the text size row is cut to %q / %q", size, panel.name, label, sub)
				}
			}
		}
	}
}

// The time stands over the date. The digits are centered on the screen and the AM/PM hangs to their
// right, wherever that leaves the AM/PM inside the margin; where it would not, a wide time at a large
// text size, the two are centered as one, as they always were. As drawn, the digits' middle is the
// screen's: centered with their AM/PM they sat some fifty pixels left of the date's on a Show 5.
func TestTheTimeIsOnTheDatesAxis(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range showPanels() {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			gap, aw := r.s(18), r.width(r.ampm, "PM")
			for _, hm := range []string{"2:07", "12:48"} {
				hw := r.width(r.clock, hm)
				x, mid, group := r.clockLeft(hw, gap, aw), (r.w-hw)/2, (r.w-hw-gap-aw)/2
				switch fits := mid+hw+gap+aw <= r.w-r.margin; {
				case fits && x != mid:
					t.Errorf("%d%% on the %s: %s starts at %d, centered it starts at %d", size, panel.name, hm, x, mid)
				case !fits && x != group:
					t.Errorf("%d%% on the %s: %s starts at %d and its AM/PM passes the margin", size, panel.name, hm, x)
				}
			}
		}
	}

	_ = config.Set().Screen().TextSize(0)
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle"})
		// The digits are the only thing in the text color on their rows; the AM/PM is in the accent.
		base := panel.high/2 + r.s(60)
		left, right := panel.wide, 0
		for y := base - capHeight(r.clock); y < base; y++ {
			for x := range panel.wide {
				if img.RGBAAt(x, y) == cream {
					left, right = min(left, x), max(right, x)
				}
			}
		}
		if off := (left+right)/2 - panel.wide/2; right == 0 || abs(off) > r.s(8) {
			t.Errorf("%s: the digits' middle is %d from the screen's", panel.name, off)
		}
	}
}

// With SHOW_PREVIEW set, the clock and the Display settings are written there at each step, on every
// panel and on a 1366x768 one, to look at.
func TestTextSizePreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	previewFont(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	scenes := map[string]scene{
		"clock":            {now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}},
		"settings-display": {now: at, phase: "idle", showSheet: true, sheet: settings{cat: catDisplay, brightness: 70}},
	}
	panels := append(showPanels(), struct {
		name       string
		wide, high int
	}{"1366x768", 1366, 768})
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		for _, panel := range panels {
			// Scrolled to the Look rows, the font and the text size among them; the scroll is in the
			// panel's pixels, so it is the Show 5's scaled to the panel.
			scenes["settings-display-look"] = scene{now: at, phase: "idle", showSheet: true,
				sheet: settings{cat: catDisplay, brightness: 70, cardScroll: 330 * panel.wide / showWide}}
			for name, s := range scenes {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(s)
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("text-%d-%s-%s.png", size, name, panel.name)))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
