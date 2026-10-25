//go:build spot

package display

import "image/color"

// The Spot's skins (skin.go, skin_looks.go): the skin's palette taken into the round screen's own
// colors, and its filter over the panel's rows. The rings that say what the device is doing (listening,
// thinking, replying, muted) and the camera's mark keep their colors, as they mean something: the
// palette never takes the muted reds (colMuted, colMutedSoft) or the camera's green, and while the
// microphones are off, the camera is on or the red night clock is up, the filter is off too
// (unskinned), so that red stays red. Listening, thinking and replying turn to the skin's color with
// everything else.

// spotColors are the round screen's colors as they are without a skin.
var spotColors = struct{ ground, text, dim, track, accent, timer color.RGBA }{
	colBackground, colText, colDim, colTrack, colAccent, colTimer,
}

// applySkin puts the skin in force on the round screen's colors and the panel.
func applySkin(p skinPanel, w, h int) {
	b := spotColors
	colBackground, colText, colDim, colTrack, colAccent, colTimer = b.ground, b.text, b.dim, b.track, b.accent, b.timer
	if t := skinLooks[skin()].theme; t != nil {
		c := t.colors
		colBackground, colText, colDim, colTrack = c[roleGround], c[roleText], c[roleDim], c[roleRules]
		colAccent, colTimer = c[roleAccent], c[roleAccent]
	}
	if p != nil {
		p.SetFilter(skin(), skinFilterFor(w, h))
	}
}
