//go:build !dot && !spot

package display

// The Show's skins at work (skin.go, skin_looks.go): each frame, after the theme, the skin's own palette
// and its filter over the panel's rows; on the home clock, what it draws behind the time
// (skin_backdrops.go) and LCARS's frame (skin_lcars.go).

// skinBackdrops are what the skins draw behind the home clock, by the value the config keeps.
var skinBackdrops = map[string]func(r *renderer, s scene){
	skinMatrix:    (*renderer).matrixRain,
	skinSynthwave: (*renderer).synthwaveSun,
	skinHAL:       (*renderer).halBackdrop,
}

// applySkin puts the skin in force on the palette and the panel.
func applySkin(p skinPanel, w, h int) {
	applyTheme(current()) // the skin's palette, if it has one (applyTheme)
	if p != nil {
		p.SetFilter(skin(), skinFilterFor(w, h))
	}
}

// skinBackdrop draws the skin's backdrop behind the home clock, if it has one.
func (r *renderer) skinBackdrop(s scene) {
	if b := skinBackdrops[skin()]; b != nil {
		b(r, s)
	}
}
