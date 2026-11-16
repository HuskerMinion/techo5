//go:build !dot && !spot

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

// drawsJapanese is whether the fonts in use draw Japanese: without the font first run downloads for it
// (cjkfont), its widths are the missing glyph's, which say nothing about the fit.
func drawsJapanese(r *renderer) bool {
	_, ok := r.small.GlyphAdvance('あ')
	return ok
}

// First run fits the Show 5 in every screen language: a title and the step count side by side, the
// buttons inside the card, the lines above the buttons (or the rooms), the keyboard's title and the
// welcome card's words. With SHOW_PREVIEW set, English, German and Japanese are written there to look at.
func TestFirstRunFitsInEveryLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	for _, lang := range firstRunLanguages {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		r := newRenderer(img)
		r.useFont()
		if lang == "ja" && !drawsJapanese(r) {
			t.Log("ja: no Japanese font here (PREVIEW_FONTS_DIR), not measured")
			continue
		}
		left, right := r.margin+r.s(40), r.w-r.margin-r.s(40)
		for name, v := range firstRunCases(at) {
			title, lines, note := firstRunText(v, at)
			count := fmt.Sprintf(tr("firstrun_step_d_of_d"), v.number, v.of)
			if w := r.width(r.title, title) + r.s(24) + r.width(r.small, count); w > right-left {
				t.Errorf("%s/%s: the title %q and %q are %d wide, the card %d", lang, name, title, count, w, right-left)
			}
			buttons := firstRunButtons(v)
			rects := r.frButtonRects(buttons)
			if len(rects) > 0 && rects[0].Min.X < left {
				t.Errorf("%s/%s: the buttons %+v start at %d, the card at %d", lang, name, buttons, rects[0].Min.X, left)
			}
			// The lines as firstRun lays them out, smaller if they have to be, above what is under them.
			room := right - left
			keyView := v.step == frHome && !v.home && v.adopt.IsZero() && v.showKey && v.key != "" && v.wifi.Address != ""
			if keyView {
				room = r.w - r.margin - r.s(40) - r.s(200) - left - r.s(24)
			}
			floor := r.frFloor(v, buttons)
			placed, next := r.frText(lines, note, room, floor)
			for _, l := range placed {
				if r.width(l.face, l.text) > room {
					t.Errorf("%s/%s: %q is wider than the card", lang, name, l.text)
				}
			}
			if len(placed) > 0 && placed[0].face != r.small {
				if lang == "" {
					t.Errorf("%s: English is drawn smaller", name)
				}
				t.Logf("%s/%s: drawn smaller to fit", lang, name)
			}
			last := 0
			if n := len(placed); n > 0 {
				last = placed[n-1].y
			}
			if keyView {
				_, step, parts := r.frKeyLines(v.key, room)
				last = next + r.s(6) + (len(parts)-1)*step
			}
			if last+r.s(10) > floor {
				t.Errorf("%s/%s: the lines reach %d, what is under them starts at %d", lang, name, last+r.s(10), floor)
			}
			if dir == "" || !previewLang(lang) {
				continue
			}
			fillCanvas(img, walnut)
			r.firstRun(scene{now: at}, v)
			writeFirstRunPreview(t, filepath.Join(dir, "firstrun-lang-"+langName(lang)+"-"+name+".png"), img)
		}
		for _, room := range frRoomLabels() {
			if r.width(r.tiny, room) > r.frRoomRects()[0].Dx()-r.s(10) {
				t.Errorf("%s: the room %q is wider than its key", lang, room)
			}
		}
		for _, title := range []string{tr("firstrun_name_this_device"), tr("firstrun_the_room_it_is_in"), tr("firstrun_type_a_room")} {
			if r.width(r.small, title) > r.w-2*r.margin {
				t.Errorf("%s: the keyboard's %q is wider than the screen", lang, title)
			}
		}
		if r.width(r.title, tr("firstrun_hello")) > right-left {
			t.Errorf("%s: the welcome's title is wider than the card", lang)
		}
		for _, hint := range []string{tr("firstrun_tap_to_begin"), tr("firstrun_tap_to_finish")} {
			if r.width(r.body, hint) > right-left {
				t.Errorf("%s: %q is wider than the card", lang, hint)
			}
		}
		// The welcome card's lines, as welcomeCard lays them out, end above its hint, with a microphone
		// to speak to and without.
		for _, mic := range []bool{true, false} {
			was := hasMicrophone
			hasMicrophone = func() bool { return mic }
			placed := r.welcomeText(welcomeLines())
			if n := len(placed); placed[n-1].y > r.welcomeFloor() {
				t.Errorf("%s: the welcome's lines reach %d, its hint is under %d", lang, placed[n-1].y, r.welcomeFloor())
			}
			if placed[0].face != r.small {
				if lang == "" {
					t.Error("the English welcome is drawn smaller")
				}
				t.Logf("%s: the welcome (microphone %v) is drawn smaller to fit", lang, mic)
			}
			fillCanvas(img, walnut)
			r.welcomeCard(tr("firstrun_tap_to_begin"))
			hasMicrophone = was
			if dir != "" && previewLang(lang) && mic {
				writeFirstRunPreview(t, filepath.Join(dir, "firstrun-lang-"+langName(lang)+"-hello.png"), img)
			}
		}
	}
}
