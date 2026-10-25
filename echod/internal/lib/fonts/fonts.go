// Package fonts is the screen's text fonts: Go, which the screen has always drawn with and still does
// unless another is chosen, and the others offered in its place. Icons are not text and keep their
// own font (lib/mdi).
//
// The others are static Regular and Bold cuts of open-licensed fonts from Google Fonts, made smaller
// by tools/fonts/make_fonts.py, each with its license beside it in files/. Only the screen's builds
// import this package, so the Dot, which has no screen, carries none of them.
package fonts

import (
	"embed"
	"errors"
	"fmt"
	"path"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

//go:embed files/*/*.ttf
var files embed.FS

// Family is a font on offer: its name as the screen and Home Assistant say it, and its value as the
// config keeps it ("" for Go, the default, so a saved file without one keeps today's look).
type Family struct {
	Name  string
	Value string

	// scale sizes the family's letters against Go's at the same size: some fonts draw much larger or
	// smaller letters than others. It is chosen so that a line of words is about as wide as it is in
	// Go, which every layout on the screen was made with, so the same text fills the same space
	// (measured over the clock's figures, the date and settings rows; see fonts_test.go).
	scale float64
}

// Go is the screen's own font, the default.
const Go = ""

// families are the choices, in the order they are offered; Go first.
var families = []Family{
	{"Go", Go, 1},
	{"Inter", "inter", 0.95},
	{"MuseoModerno", "museomoderno", 0.92},
	{"Exo", "exo", 1},
	{"Overpass", "overpass", 1},
	{"Saira", "saira", 0.98},
	{"Comic Neue", "comicneue", 1.06},
	{"Open Sans", "opensans", 0.98},
	{"Google Sans Flex", "googlesansflex", 1},
	{"Go Mono", GoMono, 0.755},
}

// skinFonts are fonts a screen skin draws in that are not on offer by themselves: sized for the skin's
// own layouts rather than to keep Go's line widths. Antonio, tall and narrow, is LCARS's.
var skinFonts = []Family{
	{"Antonio", Antonio, 1.0},
}

// Antonio is the LCARS skin's font.
const Antonio = "antonio"

// family is the family saved as value, on offer or a skin's, and whether there is one.
func family(value string) (Family, bool) {
	for _, list := range [][]Family{families, skinFonts} {
		for _, f := range list {
			if f.Value == value {
				return f, true
			}
		}
	}
	return Family{}, false
}

// GoMono is Go's own monospaced cut, the terminal look; built in, like Go.
const GoMono = "gomono"

// Families are the fonts on offer, Go first.
func Families() []Family { return families }

// Names are the fonts' names, in the order Families has them.
func Names() []string {
	out := make([]string, len(families))
	for i, f := range families {
		out[i] = f.Name
	}
	return out
}

// Index is the place among Families of the font saved as value; one not on offer (from a newer build,
// or a typo in the file) is Go's, 0.
func Index(value string) int {
	for i, f := range families {
		if f.Value == value {
			return i
		}
	}
	return 0
}

// Known is whether value is a font on offer.
func Known(value string) bool {
	for _, f := range families {
		if f.Value == value {
			return true
		}
	}
	return false
}

// parsed is each family's fonts once read, regular then bold.
var parsed struct {
	mu    sync.Mutex
	fonts map[string][2]*sfnt.Font
}

// fontsOf is the family saved as value, read once: Go's own for Go or anything not on offer.
func fontsOf(value string) ([2]*sfnt.Font, error) {
	if _, ok := family(value); !ok {
		value = Go
	}
	parsed.mu.Lock()
	defer parsed.mu.Unlock()
	if f, ok := parsed.fonts[value]; ok {
		return f, nil
	}
	var src [2][]byte
	switch value {
	case Go:
		src = [2][]byte{goregular.TTF, gobold.TTF}
	case GoMono:
		src = [2][]byte{gomono.TTF, gomonobold.TTF}
	default:
		for i, name := range []string{"Regular.ttf", "Bold.ttf"} {
			b, err := files.ReadFile(path.Join("files", value, name))
			if err != nil {
				return [2]*sfnt.Font{}, err
			}
			src[i] = b
		}
	}
	var out [2]*sfnt.Font
	for i, b := range src {
		f, err := opentype.Parse(b)
		if err != nil {
			return [2]*sfnt.Font{}, fmt.Errorf("font %q: %w", value, err)
		}
		out[i] = f
	}
	if parsed.fonts == nil {
		parsed.fonts = map[string][2]*sfnt.Font{}
	}
	parsed.fonts[value] = out
	return out, nil
}

// Font is the font saved as value, regular or bold, as it is drawn without the fallback: for checking
// which characters it has.
func Font(value string, bold bool) (*sfnt.Font, error) {
	f, err := fontsOf(value)
	if err != nil {
		return nil, err
	}
	if bold {
		return f[1], nil
	}
	return f[0], nil
}

// Face is the font saved as value at size pixels (points at 72 DPI), regular or bold, hinted fully as
// the screen's text always has been.
//
// Go, with no fonts of the owner's own, is Go's face alone, exactly as the screen drew before there
// was a choice. Anything else is a Stack: the family at its own scale of size first, then Go at size
// for the characters it has no glyph for, then the owner's own fonts (own.go) at size for those Go has
// none for either, with Go's line metrics at size, so lines are spaced as every layout expects.
func Face(value string, bold bool, size float64) (font.Face, error) {
	goFonts, err := fontsOf(Go)
	if err != nil {
		return nil, err
	}
	w := 0
	if bold {
		w = 1
	}
	goFace, err := opentype.NewFace(goFonts[w], &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	fam, ok := family(value)
	if !ok {
		fam = families[0]
	}
	var layers []Layer
	if fam.Value != Go {
		f, err := fontsOf(fam.Value)
		if err != nil {
			return nil, err
		}
		face, err := opentype.NewFace(f[w], &opentype.FaceOptions{Size: size * fam.scale, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		layers = append(layers, Layer{Face: face, Font: f[w]})
	}
	layers = append(layers, Layer{Face: goFace, Font: goFonts[w]})
	for _, e := range ownFonts() {
		// One weight each: a font of that kind is too big to keep two of.
		face, err := ownFace(e.font, size)
		if err != nil {
			continue
		}
		f := e.font
		layers = append(layers, Layer{Face: face, Font: f, Broke: func(why string) { markBad(f, why) }})
	}
	if len(layers) == 1 {
		return goFace, nil
	}
	return NewStack(goFace, layers...), nil
}

// ownFace is a face of one of the owner's own fonts at size; should making it panic (a broken file),
// the font is no longer used (markBad).
func ownFace(f *sfnt.Font, size float64) (face font.Face, err error) {
	defer func() {
		if p := recover(); p != nil {
			why := fmt.Sprint(p)
			markBad(f, why)
			face, err = nil, errors.New(why)
		}
	}()
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}
