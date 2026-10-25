//go:build !dot && !spot

package display

import (
	"image"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// hasMicrophone is whether this screen has anyone to speak to: every Show but the Skylight Calendar,
// which was built without microphones. A variable so a preview can draw both.
var hasMicrophone = func() bool { return !layout.Skylight() }

// welcome is the first-run card: what to say, where the settings are, and that a tap begins.
// It shows until it is tapped once, then never again.
func (r *renderer) welcome(s scene) {
	r.useFont()
	name := config.Get().Device.Name
	if name == "" {
		name = "TECHO5"
	}
	// Every position is in the Show 5's pixels, scaled (r.s) like the fonts are: unscaled, the lines
	// of a wider panel's larger type ran into each other.
	r.bevel(image.Rect(r.margin, r.s(40), r.w-r.margin, r.h-r.s(40)), shift(walnut, 10), true)
	r.text(r.title, "Hello", r.margin+r.s(40), r.s(118), amber)
	word := strings.ReplaceAll(config.Get().Wake.Slot(0).ID, "_", " ")
	if word == "" {
		word = "the wake word"
	} else {
		word = strings.ToUpper(word[:1]) + word[1:]
	}
	lines := []string{
		"This is " + name + ". Say \"" + word + "\" and ask for anything.",
		"Swipe down from the top for settings: Wi-Fi, Bluetooth, cameras, radio, themes.",
		"Say \"show the front door\" or \"go home\" to move the screen.",
	}
	if !hasMicrophone() {
		// Nothing to say anything to: only where things are.
		lines = []string{
			"This is " + name + ".",
			"Swipe down from the top for settings: Wi-Fi, Bluetooth, cameras, radio, themes.",
			"Home Assistant can show cameras, dashboards and photos here.",
		}
	}
	y := r.s(190)
	for _, para := range lines {
		for _, line := range r.wrap(r.small, para, r.w-2*r.margin-r.s(80)) {
			r.text(r.small, line, r.margin+r.s(40), y, cream)
			y += r.s(44)
		}
	}
	hint := "Tap anywhere to begin"
	r.text(r.body, hint, (r.w-r.width(r.body, hint))/2, r.h-r.s(78), amber)
}
