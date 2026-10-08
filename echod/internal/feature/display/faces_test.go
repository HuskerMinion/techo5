//go:build !dot

package display

import (
	"image"
	"image/color"
	"path/filepath"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestBinaryDigits(t *testing.T) {
	defer clock24.Store(false)
	for _, c := range []struct {
		h, m, s int
		h24     bool
		want    [6]int
	}{
		{14, 7, 38, false, [6]int{0, 2, 0, 7, 3, 8}},
		{14, 7, 38, true, [6]int{1, 4, 0, 7, 3, 8}},
		{0, 5, 9, false, [6]int{1, 2, 0, 5, 0, 9}}, // midnight is 12 on a 12-hour clock
		{0, 5, 9, true, [6]int{0, 0, 0, 5, 0, 9}},
		{12, 59, 59, false, [6]int{1, 2, 5, 9, 5, 9}},
		{23, 0, 0, true, [6]int{2, 3, 0, 0, 0, 0}},
		{23, 0, 0, false, [6]int{1, 1, 0, 0, 0, 0}},
	} {
		clock24.Store(c.h24)
		got := binaryDigits(time.Date(2026, 9, 16, c.h, c.m, c.s, 0, time.UTC))
		if got != c.want {
			t.Errorf("%02d:%02d:%02d (24h %v) = %v, want %v", c.h, c.m, c.s, c.h24, got, c.want)
		}
		// Each digit fits in its column's lights.
		for i, d := range got {
			if d >= 1<<binaryBits[i] {
				t.Errorf("digit %d of %v needs more than %d lights", i, got, binaryBits[i])
			}
		}
	}
}

func TestPlaceName(t *testing.T) {
	for zone, want := range map[string]string{
		"America/New_York":               "New York",
		"Europe/London":                  "London",
		"America/Argentina/Buenos_Aires": "Buenos Aires",
		"UTC":                            "UTC",
		"":                               "",
	} {
		if got := placeName(zone); got != want {
			t.Errorf("placeName(%q) = %q, want %q", zone, got, want)
		}
	}
}

func TestPlaceDay(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	honolulu, _ := time.LoadLocation("Pacific/Honolulu")
	for _, c := range []struct {
		name string
		now  time.Time
		loc  *time.Location
		want string
	}{
		{"late evening here, Tokyo is tomorrow", time.Date(2026, 9, 16, 22, 0, 0, 0, chicago), tokyo, "Tomorrow"},
		{"midday here, Tokyo is tomorrow already", time.Date(2026, 9, 16, 12, 0, 0, 0, chicago), tokyo, "Tomorrow"},
		{"early morning here, Tokyo is the same day", time.Date(2026, 9, 16, 1, 0, 0, 0, chicago), tokyo, ""},
		{"just after midnight here, Honolulu is yesterday", time.Date(2026, 9, 16, 0, 30, 0, 0, chicago), honolulu, "Yesterday"},
		{"evening here, Honolulu is the same day", time.Date(2026, 9, 16, 20, 0, 0, 0, chicago), honolulu, ""},
		{"just after midnight in Tokyo, Chicago is yesterday", time.Date(2026, 9, 17, 0, 10, 0, 0, tokyo), chicago, "Yesterday"},
		{"the same zone", time.Date(2026, 9, 16, 23, 59, 0, 0, chicago), chicago, ""},
	} {
		if got := placeDay(c.now, c.loc); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWorldPlaces(t *testing.T) {
	if _, err := time.LoadLocation("Asia/Tokyo"); err != nil {
		t.Skip("no time zone data:", err)
	}
	names := func(ps []worldPlace) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.name)
		}
		return out
	}
	same := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	for _, c := range []struct {
		zones, want []string
	}{
		{nil, []string{"New York", "London", "Tokyo"}},
		{[]string{}, []string{"New York", "London", "Tokyo"}},
		{[]string{"Europe/Paris"}, []string{"Paris"}},
		{[]string{"Europe/Atlantis", "Asia/Tokyo"}, []string{"Tokyo"}},
		{[]string{"Europe/Paris", "Asia/Tokyo", "America/Denver", "Europe/Berlin"}, []string{"Paris", "Tokyo", "Denver"}},
		{[]string{"Mars/Olympus", "Europe/Paris", "Asia/Tokyo", "America/Denver"}, []string{"Paris", "Tokyo", "Denver"}},
	} {
		got := worldPlaces(c.zones)
		if !same(names(got), c.want) {
			t.Errorf("worldPlaces(%q) = %q, want %q", c.zones, names(got), c.want)
		}
		for _, p := range got {
			if p.loc == nil {
				t.Errorf("worldPlaces(%q): %s has no zone", c.zones, p.name)
			}
		}
	}
}

// A swipe turns to the next style or the one before, round past either end, and says the new one's
// name for a moment.
func TestStepClockStyle(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().ClockStyle("") })
	d := &Display{}
	last := len(clockStyles) - 1

	d.stepClockStyle(-1)
	if got := clockStyleIndex(); got != last {
		t.Fatalf("back from the first style is style %d, want the last (%d)", got, last)
	}
	if got := d.styleName(time.Now()); got != clockStyles[last].label {
		t.Errorf("the name said is %q, want %q", got, clockStyles[last].label)
	}
	if got := d.styleName(time.Now().Add(styleNameFor + time.Millisecond)); got != "" {
		t.Errorf("the name is still said after it is due to go: %q", got)
	}
	d.stepClockStyle(+1)
	if got := clockStyleIndex(); got != 0 {
		t.Fatalf("on from the last style is style %d, want the first", got)
	}
	d.stepClockStyle(+1)
	d.stepClockStyle(+1)
	if got := clockStyleIndex(); got != 2 {
		t.Errorf("two on from the first is style %d, want 2", got)
	}
	d.stepClockStyle(-1)
	if got, want := config.Get().Screen.ClockStyle, clockStyles[1].value; got != want {
		t.Errorf("the saved style is %q, want %q", got, want)
	}
}

// The Glow style's own stretch looks the same as a general bilinear scaler's, and leaves alone what its
// mask keeps out.
func TestGlowStretch(t *testing.T) {
	const w, h = 960, 480
	var g glowBuffers
	colors := [3]color.RGBA{{255, 170, 60, 255}, {200, 80, 40, 255}, {120, 110, 100, 255}}
	field := g.glowField(w/glowCell+1, h/glowCell+1, time.Date(2026, 9, 16, 14, 7, 38, 0, time.UTC), color.RGBA{30, 24, 20, 255}, colors)
	ours := image.NewRGBA(image.Rect(0, 0, w, h))
	g.stretch(ours, field, nil)
	theirs := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(theirs, theirs.Rect, field, field.Bounds(), xdraw.Src, nil)
	worst := 0
	for i := range ours.Pix {
		d := int(ours.Pix[i]) - int(theirs.Pix[i])
		worst = max(worst, d, -d)
	}
	if worst > 8 {
		t.Errorf("the stretch is up to %d off a bilinear scale", worst)
	}

	// Through a mask: none of it where the mask is clear, all of it where it is full, half at half.
	under := color.RGBA{0, 0, 200, 255}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	mask := image.NewAlpha(dst.Rect)
	for i := range dst.Pix {
		dst.Pix[i] = [4]uint8{under.R, under.G, under.B, under.A}[i%4]
	}
	mask.SetAlpha(100, 100, color.Alpha{255})
	mask.SetAlpha(200, 200, color.Alpha{128})
	g.stretch(dst, field, mask)
	if got := dst.RGBAAt(5, 5); got != under {
		t.Errorf("outside the mask the stretch drew %v", got)
	}
	if got, want := dst.RGBAAt(100, 100), ours.RGBAAt(100, 100); got != want {
		t.Errorf("inside the mask the stretch drew %v, want %v", got, want)
	}
	if got, want := dst.RGBAAt(200, 200).B, (int(ours.RGBAAt(200, 200).B)*128+200*127)/255; int(got) != want {
		t.Errorf("at half the mask blue is %d, want %d", got, want)
	}
}

// One Glow field and its stretch over a Show 8's panel, the work the style adds to a frame.
func BenchmarkGlowStretch(b *testing.B) {
	const w, h = 1280, 800
	var g glowBuffers
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	now := time.Date(2026, 9, 16, 14, 7, 38, 0, time.UTC)
	colors := [3]color.RGBA{{255, 170, 60, 255}, {200, 80, 40, 255}, {120, 110, 100, 255}}
	ground := color.RGBA{30, 24, 20, 255}
	b.Run("field", func(b *testing.B) {
		for b.Loop() {
			now = now.Add(time.Second)
			g.glowField(w/glowCell+1, h/glowCell+1, now, ground, colors)
		}
	})
	b.Run("stretch", func(b *testing.B) {
		field := g.glowField(w/glowCell+1, h/glowCell+1, now, ground, colors)
		for b.Loop() {
			g.stretch(dst, field, nil)
		}
	})
}
