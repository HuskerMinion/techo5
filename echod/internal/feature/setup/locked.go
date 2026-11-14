package setup

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// What a browser that is not in is told: how to be let in, on the device this is, and what happened
// when it was turned away. The checks themselves are where they were (setup.go, page.go); this is only
// what the answers look like. They were lines of plain text, and a dead end: nothing on them said what
// to do next, and nothing led back to the page.

// letInBy and letInNow are how a browser is let in on this device, mid-sentence and as the instruction
// to follow now. A device with a screen answers on it, with Allow on the card it puts up
// (feature/display), and a Show or a Spot has no action button to press; a device with no screen has
// only the button. The page told every one of them to press the action button.
var letInBy, letInNow = letInWords(hasScreen)

func letInWords(screen bool) (by, now string) {
	if screen {
		return "tap <strong>Allow</strong> on the device's screen", "Tap Allow on the device's screen now."
	}
	return "press the action button on the device", "Press the action button on the device now."
}

// onThisDevice is the words for a device with a screen or the words for one without, whichever this
// is: for the places on the page that say what to do at the device, which is not the same thing on
// both.
func onThisDevice(screen, button string) string {
	if hasScreen {
		return screen
	}
	return button
}

// reopenBy is how the page is put up again once it has closed, on this device.
var reopenBy = reopenWords(hasScreen)

func reopenWords(screen bool) string {
	if screen {
		return "from the device's own settings screen, or with its Setup page switch in Home Assistant"
	}
	return "by holding the action button on the device, or with its Setup page switch in Home Assistant"
}

// What the locked page says happened, above the way to ask again.
const (
	saidNoAnswer = "This browser was not let in: nobody answered at the device in time, or the answer was no."
	saidClosed   = "The page is closed. To change something else, ask to be let in again."
)

// lockedSaid is the line for what brought a browser to the locked page, from the address it came by.
// Only these lines are ever shown, whatever the address says, and one about a wait that ended is not
// shown to a browser the device is still asking about.
func (f *Feature) lockedSaid(q url.Values) string {
	switch {
	case q.Get("closed") != "":
		return saidClosed
	case q.Get("waited") != "" && !f.Waiting():
		return saidNoAnswer
	}
	return ""
}

// refusalPage answers a browser that was turned away with a page in the setup page's own style: what
// happened, why, and the way back. The words are the page's own and never the request's.
//
// Every refusal here is made before anything of the request's body has been read, so the read is ended
// and the connection closed with the answer, as web.Refuse does for a line of text.
func refusalPage(w http.ResponseWriter, code int, what, why string) {
	web.EndRead(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p><p>%s</p>
	 <p><a href="/setup">Back to the setup page</a></p></div>`, html.EscapeString(what), html.EscapeString(why))
}

// notLetIn is the refusal for a browser asking for something of the page's without having been let in:
// one that never was, or one whose time on the page ran out while a form was being filled in. For what
// a browser goes to; what a script on the page fetches is still refused in a line of text, which is
// what the script shows.
func notLetIn(w http.ResponseWriter) {
	longest := fmt.Sprintf("%d minutes", int(life/time.Minute))
	if life == time.Hour {
		longest = "an hour"
	}
	refusalPage(w, http.StatusForbidden, "Not let in",
		fmt.Sprintf("This browser is not let in to the setup page, or not any more: the page lets go of a "+
			"browser when it is left alone for %d minutes, and after %s at most. Nothing was changed. Go back "+
			"to the setup page and ask to be let in again.", int(idle/time.Minute), longest))
}

// closedPage is what the web port answers with for the setup page's paths while the page is off
// (web.Closed). A browser that was using the page when it closed itself reloads into this, and was
// told "404 page not found", as if the device had no such page.
//
// Only a browser carrying the page's cookie is told more, and it is told nothing but how the page is
// opened, which takes somebody at the device or in Home Assistant. Anything else gets the port's own
// 404 to the byte, so a page that is off still looks to the network like a page that is not there.
// The cookie is not checked against anything: every session ended when the page closed.
func (f *Feature) closedPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err != nil || c.Value == "" {
		web.Refuse(w, "404 page not found", http.StatusNotFound)
		return
	}
	refusalPage(w, http.StatusNotFound, "The setup page is closed",
		"It closes itself when it is left alone, and an hour after it was opened. Nothing was changed by "+
			"what was just asked of it. Open it again "+reopenBy+", then come back here and ask to be let in.")
}
