//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Model full-width Japanese glyphs at the settings label's 29px size, regardless
// of whether the machine running the tests has a Japanese font installed.
type deviceCJKFace struct{ font.Face }

func (deviceCJKFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) { return fixed.I(29), true }
func (deviceCJKFace) Kern(rune, rune) fixed.Int26_6             { return 0 }

func TestShow5VoiceLabelKeepsRoomBeforeLongProviderName(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	_ = config.Set().Screen().Language("ja")
	r := testRenderer()
	card := r.nextCard(960, 480)
	row := settingRow{id: "ttsvoice", label: tr("ui_speaking_voice"), sub: tr("ui_how_answers_sound"), kind: ctlChoice, value: strings.Repeat("Long provider voice name ", 8)}
	face := deviceCJKFace{basicfont.Face7x13}
	label, _, end := r.rowWords(card, row, face)
	if label != row.label {
		t.Fatalf("voice label was clipped: %q", label)
	}
	if end >= card.Max.X-r.s(26+58) {
		t.Fatal("label consumed the control's room")
	}
	if r.width(face, label) != 6*29 {
		t.Fatalf("unexpected Japanese label width: %d", r.width(face, label))
	}
	found := false
	for _, row := range themeRows() {
		if row.id == "colors" {
			found = true
			if row.sub != "テーマの各部分の色を変更" {
				t.Fatal(row.sub)
			}
		}
	}
	if !found {
		t.Fatal("custom color row was missing")
	}
}

func TestCalendarDetailWrapsJapaneseWithinItsLineBudget(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
	face := deviceCJKFace{basicfont.Face7x13}
	title := "来週の水曜日は家族と一緒に買い物に出かける予定です"
	width := 20 * 29
	lines := r.calendarDetailLines(face, title, width, 3)
	if len(lines) < 2 || strings.Join(lines, "") != title {
		t.Fatalf("detail title did not wrap intact: %q", lines)
	}
	for _, line := range lines {
		if !utf8.ValidString(line) || r.width(face, line) > width {
			t.Errorf("invalid line %q", line)
		}
	}
	long := strings.Repeat(title, 4)
	lines = r.calendarDetailLines(face, long, width, 3)
	if len(lines) != 3 || !strings.HasSuffix(lines[2], "…") {
		t.Fatalf("long text ignored the line budget: %q", lines)
	}
	if got := r.calendarDetailLines(face, title, width, 0); len(got) != 0 {
		t.Fatal("text was drawn with no room")
	}
	if room := calendarDetailRoom(354, 372, 32); room != 0 {
		t.Fatalf("description would overlap the close button: %d lines", room)
	}
	if room := calendarDetailRoom(354, 308, 32); room != 2 {
		t.Fatalf("unexpected location room: %d", room)
	}
}
