//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"strconv"
	"testing"
)

// The Echo uses a dark accent with dark text in custom Light mode. The selected
// category label and icon must remain legible across the actual accent gradient.
func TestSettingsAccentContrastOnCustomThemes(t *testing.T) {
	saved := [roles]color.RGBA{walnut, amber, cream, dim, ember}
	t.Cleanup(func() { walnut, amber, cream, dim, ember = saved[0], saved[1], saved[2], saved[3], saved[4] })
	for _, tc := range []struct {
		name string
		pal  theme
	}{
		{"custom light", preset("Custom", 0xf8f8f8, 0x272727, 0x151515, 0x686868, 0xdedede)},
		{"custom dark", preset("Custom", 0x151515, 0xeeeeee, 0xf7f7f7, 0xadadad, 0x444444)},
		{"both palette inks dark", preset("Custom", 0x191919, 0x303030, 0x222222, 0x888888, 0x555555)},
		{"both palette inks pale", preset("Custom", 0xfafafa, 0xeaeaea, 0xf6f6f6, 0x888888, 0xcccccc)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyTheme(tc.pal)
			ink := onAccent()
			if ratio := contrastOnAccent(ink); ratio < 4.5 {
				t.Errorf("selected ink %v has contrast %.2f:1, below 4.5:1", ink, ratio)
			}
			if tc.name == "custom light" && ink.R < 180 {
				t.Errorf("selected text is still dark against the dark accent: %v", ink)
			}
		})
	}
	for _, th := range themes {
		applyTheme(th)
		if ratio := contrastOnAccent(onAccent()); ratio < 3 {
			t.Errorf("built-in theme %q has %.2f:1 contrast, below 3:1", th.name, ratio)
		}
	}
}

func TestSettingsSelectedLabelIsPainted(t *testing.T) {
	saved := [roles]color.RGBA{walnut, amber, cream, dim, ember}
	t.Cleanup(func() { walnut, amber, cream, dim, ember = saved[0], saved[1], saved[2], saved[3], saved[4] })
	for _, tc := range []struct {
		name string
		pal  theme
	}{
		{"light", preset("Custom", 0xf8f8f8, 0x272727, 0x151515, 0x686868, 0xdedede)},
		{"dark", preset("Custom", 0x151515, 0xeeeeee, 0xf7f7f7, 0xadadad, 0x444444)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyTheme(tc.pal)
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
			r.settingsScreen(sheetScene(catDisplay))
			ink := onAccent()
			top := r.navTop() + int(catDisplay)*(r.navH()+r.navGap())
			label := image.Rect(r.s(70), top, r.railW()-r.s(10), top+r.navH())
			painted := 0
			for y := label.Min.Y; y < label.Max.Y; y++ {
				for x := label.Min.X; x < label.Max.X; x++ {
					if r.dst.RGBAAt(x, y) == ink {
						painted++
					}
				}
			}
			if painted < 12 {
				t.Errorf("selected label has only %d legible ink pixels", painted)
			}
		})
	}
}

// A neutral mid-gray accent puts both black and white near the contrast
// crossover. Even a small palette shift must select the stronger fallback,
// and a repeated draw must not change the foreground unexpectedly.
func TestSettingsMidGrayAccentChoosesStableForeground(t *testing.T) {
	saved := [roles]color.RGBA{walnut, amber, cream, dim, ember}
	t.Cleanup(func() { walnut, amber, cream, dim, ember = saved[0], saved[1], saved[2], saved[3], saved[4] })
	black := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	for _, gray := range []uint8{104, 112, 120, 128, 136, 144} {
		t.Run(strconv.Itoa(int(gray)), func(t *testing.T) {
			// Keep both theme inks nearly identical to the accent so the
			// accessibility fallback has to choose black or white.
			grayInk := color.RGBA{gray, gray, gray, 255}
			walnut, amber, cream = grayInk, grayInk, grayInk
			got := onAccent()
			want := white
			if contrastOnAccent(black) >= contrastOnAccent(white) {
				want = black
			}
			if got != want {
				t.Errorf("accent %d: selected %v, want strongest fallback %v", gray, got, want)
			}
			for i := 0; i < 5; i++ {
				if repeat := onAccent(); repeat != got {
					t.Errorf("accent %d: unstable ink %v after original %v", gray, repeat, got)
				}
			}
		})
	}
}
