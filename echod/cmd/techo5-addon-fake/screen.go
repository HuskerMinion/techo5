package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/display"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi/serve"
)

// fakeScreen is the fake's side of the add-on screen (serve.Screen): it says what the add-on's page and
// card now hold, and, having nothing more pressing to show, brings the page up as soon as it is asked
// and shows every card at once. What it is told, the device's display would be told.
type fakeScreen struct{ d *fake }

func (f fakeScreen) PageChanged(id string) { f.changed(id, addonapi.OnPage) }

func (f fakeScreen) CardChanged(id string) {
	f.changed(id, addonapi.OnCard)
	if _, ok := f.d.srv.Scene(id, addonapi.OnCard); ok {
		f.d.srv.Shown(id, addonapi.OnCard, true)
	}
}

func (f fakeScreen) Open(id string) {
	f.d.say("brings the page up")
	f.d.srv.Shown(id, addonapi.OnPage, true)
}

func (f fakeScreen) Close(id string) {
	f.d.say("puts the page away")
	f.d.srv.Shown(id, addonapi.OnPage, false)
}

// Dropped is everything of the add-on's gone with its last connection: the device lets go of its
// pictures; the fake has none to let go.
func (f fakeScreen) Dropped(string) {
	f.d.say("the add-on's scenes and images are gone with its connection")
}

func (f fakeScreen) changed(id, on string) {
	sc, ok := f.d.srv.Scene(id, on)
	if !ok {
		f.d.say("the %s is gone", on)
		return
	}
	b, _ := addonapi.Marshal(sc.Root)
	f.d.say("the %s is now %d blocks, %d bytes (type scene %s to see it)", on, countBlocks(sc.Root), len(b), on)
	f.d.drawScene(on, sc)
}

// drawing is where the fake draws the add-on's page and card as PNGs (-png), and for which screen
// (-size): "" draws nothing.
type drawing struct {
	to, size string
}

// pngFor is where the page (or the card) is drawn: the -png path, and the card beside it as
// NAME-card.png.
func (w drawing) pngFor(on string) string {
	if on == addonapi.OnPage {
		return w.to
	}
	ext := filepath.Ext(w.to)
	return strings.TrimSuffix(w.to, ext) + "-card" + ext
}

// drawScene draws the add-on's page or card to its PNG with the device's own layout code
// (display.RenderAddonScene), at the -size screen's size, its images as the device has them: one by
// address is drawn once it has been fetched, which draws the scene again.
func (d *fake) drawScene(on string, sc serve.Scene) {
	if d.draw.to == "" {
		return
	}
	img, err := display.RenderAddonScene(d.draw.size, d.name, on, sc.Root,
		func(b *addonapi.Block) ([]byte, string, bool) { return d.srv.Image(d.id, b) }, d.timerLeft)
	if err != nil {
		d.say("the %s could not be drawn: %v", on, err)
		return
	}
	path := d.draw.pngFor(on)
	f, err := os.Create(path)
	if err == nil {
		err = png.Encode(f, img)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		d.say("the %s could not be written: %v", on, err)
		return
	}
	d.say("drew the %s, as a %s draws it, to %s", on, d.draw.size, path)
}

// timerLeft is one of the add-on's timers as the fake keeps it: how long is left, of how long.
func (d *fake) timerLeft(id string) (time.Duration, time.Duration, bool) {
	for _, t := range d.Timers(d.id) {
		if t.ID == id {
			return time.Duration(t.Left * float64(time.Second)), time.Duration(t.Total * float64(time.Second)), true
		}
	}
	return 0, 0, false
}

func countBlocks(b addonapi.Block) int {
	n := 1
	for _, c := range b.Children {
		n += countBlocks(c)
	}
	for _, s := range b.Sections {
		for _, it := range s.Items {
			n += countBlocks(it)
		}
	}
	return n
}

// screenKeys is the keyboard's screen commands: what the owner does with the add-on's page and card.
func (d *fake) screenKeys(verb, rest string) {
	if d.family == "dot" {
		d.say("a dot has no screen")
		return
	}
	args := strings.Fields(rest)
	arg := func(i int, def string) string {
		if i < len(args) {
			return args[i]
		}
		return def
	}
	switch verb {
	case "tile":
		// As a device's Add-ons tab: the tap opens the page, and asks something of the add-on, so it
		// may bring its page up itself for a moment after. Opened before the add-on has set one, the
		// page is in view the moment it does, as on a device.
		d.say("a tap on the add-on's tile")
		d.srv.Asked(d.id)
		fakeScreen{d}.Open(d.id)
	case "touch":
		on := arg(1, addonapi.OnPage)
		sc, _ := d.srv.Scene(d.id, on) // the fake's finger is on the scene as it is now
		if len(args) == 0 || !d.srv.Touch(d.id, on, args[0], sc.Rev) {
			d.say("touch takes the id of a button or a chip on the %s in view: touch ID [page|card]", on)
		}
	case "swipe":
		if !d.srv.Swipe(d.id, arg(0, "")) {
			d.say("swipe takes left or right, on the page in view")
		}
	case "back":
		if !d.srv.Back(d.id) {
			d.say("back is for the page in view; the add-on has none up (open it with shown page on)")
		}
	case "shown":
		on, state := arg(0, ""), arg(1, "")
		if !addonapi.ValidTarget(on) || (state != "on" && state != "off") {
			d.say("shown takes page or card, and on or off")
			return
		}
		d.srv.Shown(d.id, on, state == "on")
	case "dismiss":
		d.srv.DismissCard(d.id)
	case "close":
		// The name bar's way out: the page goes, and what an earlier tap allowed is used up.
		d.srv.Left(d.id)
		fakeScreen{d}.Close(d.id)
	case "scene":
		on := arg(0, addonapi.OnPage)
		sc, ok := d.srv.Scene(d.id, on)
		if !ok {
			d.say("the add-on has no %s", on)
			return
		}
		b, _ := addonapi.Marshal(sc.Root)
		d.say("the %s (revision %d): %s", on, sc.Rev, b)
	default:
		d.say("no such screen command %q", verb)
	}
}
