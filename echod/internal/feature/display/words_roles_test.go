//go:build !dot

package display

import (
	"image/color"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The line before the hour by role: one part in the text color while the colors are off, as it always
// was; with them on, "kurz vor" quiet, the count in the accent and the link between, empty parts left out.
func TestWordsRoles(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	quiet, accent, text := color.RGBA{1, 1, 1, 255}, color.RGBA{200, 0, 0, 255}, color.RGBA{0, 0, 200, 255}
	link := lerpRGB(accent, text, wordsLinkMix)
	kurz := locale.SaidTime{Approx: "kurz vor", Count: "Viertel", Link: "nach", Hour: "elf"}
	half := locale.SaidTime{Link: "halb", Hour: "zwölf"}
	english := locale.SaidTime{Count: "thirteen", Link: "past", Hour: "eleven"}
	onHour := locale.SaidTime{Hour: "elf Uhr"}

	if got := wordsRoles(kurz, quiet, accent, text); len(got) != 1 || got[0] != (wordsPart{"kurz vor Viertel nach", text}) {
		t.Errorf("off: %v", got)
	}
	if got := wordsRoles(onHour, quiet, accent, text); len(got) != 0 {
		t.Errorf("off, on the hour: %v", got)
	}
	if err := config.Set().Screen().WordsColors(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().Screen().WordsColors(false) })
	for _, c := range []struct {
		said locale.SaidTime
		want []wordsPart
	}{
		{kurz, []wordsPart{{"kurz vor", quiet}, {"Viertel", accent}, {"nach", link}}},
		{half, []wordsPart{{"halb", link}}},
		{english, []wordsPart{{"thirteen", accent}, {"past", link}}},
		{locale.SaidTime{Approx: "kurz nach", Hour: "sieben Uhr"}, []wordsPart{{"kurz nach", quiet}}},
		{onHour, nil},
	} {
		got := wordsRoles(c.said, quiet, accent, text)
		if len(got) != len(c.want) {
			t.Errorf("%+v: %v, want %v", c.said, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%+v: %v, want %v", c.said, got, c.want)
				break
			}
		}
	}
}
