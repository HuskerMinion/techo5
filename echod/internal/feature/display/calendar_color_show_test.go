//go:build !dot && !spot

package display

import (
	"image/color"
	"testing"
)

// A color picked for a calendar in Home Assistant reads on every theme as well as our own calendars'
// colors do: a pale yellow and a white on a light theme, a navy and a black on a dark one, are moved
// toward black or white until each of their contrasts is at least the least of ours. One that already
// reads, as ours all do, is left as it was picked.
func TestPickedCalendarColorsRead(t *testing.T) {
	saved := [roles]color.RGBA{walnut, amber, cream, dim, ember}
	t.Cleanup(func() { walnut, amber, cream, dim, ember = saved[0], saved[1], saved[2], saved[3], saved[4] })

	picked := []color.RGBA{
		{255, 255, 153, 255}, // pale yellow
		{255, 255, 0, 255},   // yellow
		{0, 0, 128, 255},     // navy
		{0, 0, 255, 255},     // blue
		{255, 255, 255, 255}, // white
		{0, 0, 0, 255},       // black
		{0x16, 0xa7, 0x65, 255},
		{0xe6, 0x7c, 0x73, 255},
	}
	for _, th := range themes {
		applyTheme(th)
		least := calendarReads(calendarColors[0])
		for _, own := range calendarColors[1:] {
			for i, v := range calendarReads(own) {
				least[i] = min(least[i], v)
			}
		}
		for _, own := range calendarColors {
			if got, ok := readableCalendarColor(own); !ok || got != own {
				t.Errorf("%s: our color %v became %v (%v)", th.name, own, got, ok)
			}
		}
		for _, c := range picked {
			got, ok := readableCalendarColor(c)
			if !ok {
				t.Errorf("%s: %v could not be made to read", th.name, c)
				continue
			}
			for i, v := range calendarReads(got) {
				if v < least[i] {
					t.Errorf("%s: %v as %v has a contrast of %.2f where ours have %.2f", th.name, c, got, v, least[i])
				}
			}
		}
	}

	// Google's green reads on Walnut as it was picked; a pale yellow on Paper is deepened, not replaced.
	applyTheme(themes[themeIndex("Walnut")])
	green := color.RGBA{0x16, 0xa7, 0x65, 255}
	if got, _ := readableCalendarColor(green); got != green {
		t.Errorf("a green that reads on Walnut became %v", got)
	}
	applyTheme(themes[themeIndex("Paper")])
	pale := color.RGBA{255, 255, 153, 255}
	if got, _ := readableCalendarColor(pale); got == pale || got.R < got.B || got.G < got.B {
		t.Errorf("a pale yellow on Paper is %v, want a deeper yellow", got)
	}
}
