//go:build !dot && !spot

package display

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The keyboard over a streamed dashboard (#493). The page is a picture here, so a text field tapped on
// it is something the dashcast server says (dashboard.Field), and the screen's own keyboard comes up
// over the page, as it does for the Wi-Fi passphrase and the library's search. Each key goes to the
// field as it is pressed, so the page's own search follows the typing; the field at the top shows the
// typing here, starting from what was in the page's field. The last key is Enter (in a text area, where
// Enter is a new line, only Done) and puts the keyboard away; Cancel puts it away. Either lets go of
// the page's field, so a tap on it brings the keyboard back. The keyboard fills the screen and takes
// every tap, so one left with no key pressed for dashKeyboardIdle puts itself away as Cancel does.

// dashKeyboard is the keyboard over the streamed page. Under Display.mu.
type dashKeyboard struct {
	up               bool
	seq              uint64 // the server's last word on the field that was acted on (dashboard.Field.Seq)
	kind, label      string
	text             string
	shifted, symbols bool
	touched          time.Time // when it came up or a key was last pressed
}

// dashKeyboardMax is as many characters as the field here shows, a long search or a note.
const dashKeyboardMax = 500

// dashKeyboardIdle is how long the keyboard stays up with no key pressed.
var dashKeyboardIdle = 2 * time.Minute

// dashKeyboardScene follows what the server said about the page's field: a field getting focus brings
// the keyboard up with its text, and losing it puts the keyboard away. Each word is acted on once, so a
// keyboard put away here is not brought back by the same word.
func (d *Display) dashKeyboardScene(s *scene, streamed bool) {
	fl := s.dash.Field
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case !streamed, d.dashKbd.up && fl.Seq == d.dashKbd.seq && time.Since(d.dashKbd.touched) > dashKeyboardIdle:
		// The page put by with the keyboard up (a sheet over it, the clock back), or the keyboard left
		// alone: the page's field is let go of, so a tap on it brings the keyboard up again.
		if d.dashKbd.up {
			safe.Go("dashboard keyboard away", func() { dashboard.Get().Key("done") })
		}
		d.dashKbd = dashKeyboard{seq: d.dashKbd.seq}
	case fl.Seq != d.dashKbd.seq:
		if fl.Up {
			d.dashKbd = dashKeyboard{up: true, seq: fl.Seq, kind: fl.Kind, label: fl.Label, text: fl.Value}
			d.dashKeyboardTouched()
		} else {
			d.dashKbd = dashKeyboard{seq: fl.Seq}
		}
	}
	s.dashKbd = d.dashKbd
}

// dashKeyboardTouched notes the keyboard used now, and has the screen looked at again once it has
// been left for dashKeyboardIdle. Under d.mu.
func (d *Display) dashKeyboardTouched() {
	d.dashKbd.touched = time.Now()
	after := dashKeyboardIdle + 50*time.Millisecond
	if d.dashKbdWake == nil {
		d.dashKbdWake = time.AfterFunc(after, d.wake)
	} else {
		d.dashKbdWake.Reset(after)
	}
}

// dashKeyboardUp is whether the keyboard is over the streamed page, taking its taps.
func (d *Display) dashKeyboardUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dashKbd.up
}

// dashKeyboardSymbols is whether the keyboard over the streamed page shows its symbols.
func (d *Display) dashKeyboardSymbols() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dashKbd.symbols
}

// dashKey is a key tapped on the keyboard over the streamed page (keyAt's names).
func (d *Display) dashKey(k string) {
	f := dashboard.Get()
	var typed, key string
	d.mu.Lock()
	n := &d.dashKbd
	if n.up {
		d.dashKeyboardTouched()
	}
	switch k {
	case "":
	case "shift":
		n.shifted = !n.shifted
	case "symbols":
		n.symbols = !n.symbols
	case "backspace":
		// Only what shows here is taken away: a password starts empty here though not on the page.
		if _, size := utf8.DecodeLastRuneInString(n.text); size > 0 {
			n.text = n.text[:len(n.text)-size]
			key = "Backspace"
		}
	case "cancel":
		n.up = false
		key = "done"
	case "join":
		n.up = false
		key = "Enter"
		if n.kind == "textarea" {
			key = "done" // Enter there is a new line, not the end of the typing
		}
	default:
		if k == "space" {
			k = " "
		}
		if n.shifted && !n.symbols {
			k = strings.ToUpper(k)
			n.shifted = false
		}
		if utf8.RuneCountInString(n.text) < dashKeyboardMax {
			n.text += k
			typed = k
		}
	}
	d.mu.Unlock()
	f.Type(typed)
	if key != "" {
		f.Key(key)
	}
	if key == "Enter" {
		f.Key("done") // put away here, so the page's field is let go of too
	}
	d.wake()
}

// dashKeyboardPage draws the keyboard over the whole panel: the field's label, what is typed (a
// password as stars), and the keys, the last one Done.
func (r *renderer) dashKeyboardPage(k dashKeyboard) {
	fillCanvas(r.dst, walnut)
	shown := k.text
	if k.kind == "password" {
		shown = strings.Repeat("*", utf8.RuneCountInString(k.text))
	}
	r.keyboard(k.label, shown, false, k.shifted, k.symbols, tr("done"))
}
