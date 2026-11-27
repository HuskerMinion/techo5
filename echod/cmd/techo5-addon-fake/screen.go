package main

import (
	"strings"

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

func (f fakeScreen) changed(id, on string) {
	sc, ok := f.d.srv.Scene(id, on)
	if !ok {
		f.d.say("the %s is gone", on)
		return
	}
	b, _ := addonapi.Marshal(sc.Root)
	f.d.say("the %s is now %d blocks, %d bytes (type scene %s to see it)", on, countBlocks(sc.Root), len(b), on)
	// TODO(stage 2 part B): draw the scene to a PNG with the device's own layout code, at the family's
	// screen size, so an author sees the page and the card without a device (-png FOLDER).
	renderScene(id, on, sc)
}

// renderScene is where the PNG of a scene will be made; it does nothing until the display's layout
// code for scenes exists.
var renderScene = func(id, on string, sc serve.Scene) {}

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
	case "touch":
		on := arg(1, addonapi.OnPage)
		if len(args) == 0 || !d.srv.Touch(d.id, on, args[0]) {
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
