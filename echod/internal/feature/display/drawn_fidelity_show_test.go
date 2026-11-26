//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// Drawn dashboard cards drawn closer to Home Assistant's own: a markdown card with its structure
// (#757). Each draws whether or not SHOW_PREVIEW is set, and with it set is written there for the
// golden check (tools/golden).

var fidelityAt = time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)

func drawnScene(d dashboard.Drawn) scene {
	return scene{now: fidelityAt, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: d}
}

// markdownView is #757's card beside two tiles, in a two-section view.
func markdownView() dashboard.Drawn {
	text := "## Today\n**Partly cloudy.** Light winds, turning **gusty** after dark.\n\n- High 24°\n- Rain 20%, mostly in the evening hours after the commute\n\nsnake_case and 2 * 3 stay as written."
	return dashboard.Drawn{Sections: []dashboard.Section{
		{Blocks: []dashboard.Block{{Heading: "Weather"}, {Text: []string{"x"}, Marked: dashboardMarked(text)}}},
		{Blocks: []dashboard.Block{{Heading: "Hallway"}, {Tiles: []dashboard.Tile{
			{Name: "Ceiling", Icon: "lightbulb", Value: "On · 80%", On: true},
			{Name: "Porch", Icon: "outdoor-lamp", Value: "Off"},
		}}}},
	}}
}

// dashboardMarked is the dashboard's own reading of a markdown card's content.
func dashboardMarked(md string) []dashboard.TextLine { return dashboard.MarkText(md) }

func TestDrawnMarkdownPreview(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := goldenDraw(t, "dash-markdown-show8", show8Wide, show8High, drawnScene(markdownView()))
	// The body is drawn smaller than a tile's name, and a heading larger than the body.
	mf := r.markedFaces()
	if mf.body.Metrics().Height >= r.faces().label.Metrics().Height {
		t.Fatal("the markdown body is not smaller than a tile's name")
	}
	if mf.heading(2).Metrics().Height <= mf.body.Metrics().Height {
		t.Fatal("a heading is not larger than the body")
	}
}

// A bullet's words wrap under its words, not under its dot, and a gap is half a line.
func TestDrawnMarkdownLayout(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	lines := dashboard.MarkText("one\n\ntwo")
	_, h := r.layoutMarked(lines, 400)
	step, _ := r.lineStep(r.markedFaces().body)
	if h != 2*step+step/2 {
		t.Fatalf("two lines and a gap came to %d, want %d", h, 2*step+step/2)
	}
	wrapped := r.wrapRuns(r.markedFaces(), []dashboard.TextRun{{Text: "a few "}, {Text: "bold", Bold: true}, {Text: " words that go on and on past the edge"}}, 200)
	if len(wrapped) < 2 {
		t.Fatalf("mixed runs did not wrap: %+v", wrapped)
	}
	for _, line := range wrapped {
		w := 0
		for _, wd := range line {
			f := r.markedFaces().body
			if wd.bold {
				f = r.markedFaces().bold
			}
			w += r.width(f, wd.s)
		}
		if w > 200 {
			t.Fatalf("a wrapped line is %d wide, over 200: %+v", w, line)
		}
	}
}
