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
