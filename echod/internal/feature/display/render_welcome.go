//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"strings"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// hasMicrophone is whether this screen has anyone to speak to: every Show but the Skylight Calendar,
// which was built without microphones. A variable so a preview can draw both.
var hasMicrophone = func() bool { return !layout.Skylight() }

// welcome is the first-run card: what to say, where the settings are, and that a tap begins.
// First run (firstrun.go) begins with it and ends with it, with another line at its foot.
func (r *renderer) welcome(s scene) { r.welcomeCard(tr("firstrun_tap_to_begin")) }

// welcomeCard is the welcome card with hint at its foot.
func (r *renderer) welcomeCard(hint string) {
	r.useFont()
	// Every position is in the Show 5's pixels, scaled (r.s) like the fonts are: unscaled, the lines
	// of a wider panel's larger type ran into each other.
	r.bevel(image.Rect(r.margin, r.s(40), r.w-r.margin, r.h-r.s(40)), shift(walnut, 10), true)
	r.text(r.title, tr("firstrun_hello"), r.margin+r.s(40), r.s(118), amber)
	for _, l := range r.welcomeText(welcomeLines()) {
		r.text(l.face, l.text, r.margin+r.s(40), l.y, cream)
	}
	r.text(r.body, hint, (r.w-r.width(r.body, hint))/2, r.h-r.s(78), amber)
}

// welcomeLines are the welcome card's paragraphs: what to say, where the settings are, and how to move
// the screen; on a Show without microphones, only where things are.
func welcomeLines() []string {
	name := config.Get().Device.Name
	if name == "" {
		name = "TECHO5"
	}
	if !hasMicrophone() {
		return []string{
			fmt.Sprintf(tr("firstrun_this_is_s"), name),
			tr("firstrun_welcome_settings"),
			tr("firstrun_welcome_home"),
		}
	}
	word := strings.ReplaceAll(config.Get().Wake.Slot(0).ID, "_", " ")
	if word == "" {
		word = tr("firstrun_the_wake_word")
	} else {
		word = upper(word[:1]) + word[1:]
	}
	return []string{
		fmt.Sprintf(tr("firstrun_welcome_say"), name, word),
		tr("firstrun_welcome_settings"),
		tr("firstrun_welcome_move"),
	}
}

// welcomeText lays the welcome card's paragraphs out from s(190): in the usual size, or smaller when
// that would run into the hint at the card's foot, as a longer language's words or a long name can.
func (r *renderer) welcomeText(paras []string) []placedLine {
	lay := func(face font.Face, pitch int) []placedLine {
		var out []placedLine
		y := r.s(190)
		for _, para := range paras {
			for _, line := range r.wrapLines(face, para, r.w-2*r.margin-r.s(80)) {
				out = append(out, placedLine{face, line, y, cream})
				y += pitch
			}
		}
		return out
	}
	out := lay(r.small, r.s(44))
	if len(out) > 0 && out[len(out)-1].y > r.welcomeFloor() {
		out = lay(r.tiny, r.s(34))
	}
	return out
}

// welcomeFloor is the lowest baseline the welcome card's lines may have, clear of the hint under them.
func (r *renderer) welcomeFloor() int { return r.h - r.s(78) - r.s(36) }
