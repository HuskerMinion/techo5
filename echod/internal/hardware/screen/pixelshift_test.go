//go:build !dot && !panel

package screen

import "testing"

func TestPixelShift(t *testing.T) {
	for _, c := range []struct {
		name             string
		r, g, b, a, alen uint32
		want             [4]uint
	}{
		{"argb8888 (simplefb a8r8g8b8)", 16, 8, 0, 24, 8, [4]uint{16, 8, 0, 24}},
		{"abgr8888", 0, 8, 16, 24, 8, [4]uint{0, 8, 16, 24}},
		// DRM fbdev emulation: XRGB8888 reports transp offset 0, length 0
		{"xrgb8888 (DRM fbdev)", 16, 8, 0, 0, 0, [4]uint{16, 8, 0, 24}},
		{"xbgr8888", 0, 8, 16, 0, 0, [4]uint{0, 8, 16, 24}},
	} {
		v := varInfo{
			Red:    [3]uint32{c.r, 8, 0},
			Green:  [3]uint32{c.g, 8, 0},
			Blue:   [3]uint32{c.b, 8, 0},
			Transp: [3]uint32{c.a, c.alen, 0},
		}
		got := pixelShift(&v)
		if got != c.want {
			t.Errorf("%s: shift %v, want %v", c.name, got, c.want)
		}
		if pixFmt(got) == "" {
			t.Errorf("%s: no frame layout for shift %v", c.name, got)
		}
	}
}
