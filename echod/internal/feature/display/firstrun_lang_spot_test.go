//go:build spot

package display

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// First run fits the Spot's circle in every screen language: the step count, the title, the buttons
// and every line that is drawn (lines past the buttons' top are left off, so none may be). A line is
// as wide as the circle allows at its height, less a margin, as the round paragraphs take it. With
// SPOT_PREVIEW set, English, German and Japanese are written there to look at.
func TestSpotFirstRunFitsInEveryLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	dir := os.Getenv("SPOT_PREVIEW")
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	for _, lang := range firstRunLanguages {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		r := newRoundRenderer(img)
		r.useFont()
		if _, ok := r.small.GlyphAdvance('あ'); lang == "ja" && !ok {
			t.Log("ja: no Japanese font here (PREVIEW_FONTS_DIR), not measured")
			continue
		}
		within := func(what, s string, w, baseline, ascent int) {
			if room := chord(baseline-ascent) - 40; w > room {
				t.Errorf("%s: %s %q is %d wide, the circle there %d", lang, what, s, w, room)
			}
		}
		cases := firstRunCases(at)
		cases["hello"] = firstRunView{step: frHello, name: "Kitchen Spot"}
		cases["done"] = firstRunView{step: frDone, name: "Kitchen Spot"}
		for name, v := range cases {
			v.name = "Kitchen Spot"
			if v.number > 0 {
				count := fmt.Sprintf(tr("firstrun_step_d_of_d"), v.number, v.of)
				within(name+": the step", count, r.width(r.tiny, count), 74, r.tiny.Metrics().Ascent.Ceil())
			}
			title, lines := spotFirstRunText(v, at)
			within(name+": the title", title, r.width(r.title, title), 126, r.title.Metrics().Ascent.Ceil())
			if v.step == frHome && v.showKey && v.key != "" {
				back := tr("firstrun_tap_to_go_back")
				within(name+": the way back", back, r.width(r.tiny, back), 412, r.tiny.Metrics().Ascent.Ceil())
			} else {
				buttons := spotFirstRunButtons(v)
				rects := spotFirstRunRects(len(buttons))
				limit := 440
				if len(rects) > 0 {
					limit = rects[0].Min.Y - 8
				}
				face := r.faces().button
				for i, b := range buttons {
					if w := r.width(face, b.label); w > rects[i].Dx()-24 {
						t.Errorf("%s: %s: the button %q is %d wide, the button %d", lang, name, b.label, w, rects[i].Dx())
					}
				}
				y := 170
				for _, para := range lines {
					for _, line := range r.wrapLines(r.small, para, 340) {
						if y > limit {
							t.Errorf("%s: %s: %q is left off: the buttons start at %d", lang, name, line, limit)
						}
						within(name+": the line", line, r.width(r.small, line), y, r.small.Metrics().Ascent.Ceil())
						y += 30
					}
				}
			}
			if dir == "" || !previewLang(lang) {
				continue
			}
			r.draw(roundScene{now: at, phase: "idle", firstRunUp: true, firstRun: v})
			writeFirstRunPreview(t, filepath.Join(dir, "spot-firstrun-lang-"+langName(lang)+"-"+name+".png"), img)
		}
	}
}
