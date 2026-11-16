package setup

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
)

// A save comes back to the section it was made in. Every section with a form is a fieldset with a name
// of its own, the redirect after a save carries that name (at=) and ends at the fieldset (#sec-name),
// and what happened is said inside it. It used to be said once, at the top of the tab: on a long tab
// that was as many as sixteen sections above the row just used, and the browser was left at the top
// with it.

// sectionFor is the section a form is drawn in, where that is not one of the form's own name: the
// forms that share a fieldset with others.
var sectionFor = map[string]string{
	"forget":       "wifi",
	"timer_cancel": "alarm_edit", "snoozes_cancel": "alarm_edit",
	"doorbell-chime":  "doorbell",
	"calendar-remove": "calendar",
	"theme-colors":    "themes",
	"font-remove":     "fonts",
	"deckobs":         "deck", "deckgrid": "deck", "deckpage": "deck", "deckpc": "deck",
	"immich-album": "immich", "immich-show": "immich", "immich-forget": "immich",
	"photos-remove": "photos",
	"babylink":      "baby", "babylight": "baby",
	"update-install": "updates", "update-check": "updates", "update-auto": "updates",
	"sound-play": "sounds", "sound-remove": "sounds",
	"wakeword-remove": "wakewords",
}

// sectionOf is the section a form's answer is shown in.
func sectionOf(what string) string {
	if s, ok := sectionFor[what]; ok {
		return s
	}
	return what
}

// sectionNamed is at when it is a name the page knows, and nothing when it is anything else. The name
// arrives in the address, which anybody can write: only a name from the page's own list is ever
// compared with, and none is written into the page from the address.
func sectionNamed(at string) string {
	if _, ok := tabFor[at]; ok {
		return at
	}
	return ""
}

// sectionWriter is the page being written with what the last save came back with. It is handed to the
// sections as the page itself, so that a section can say what happened without every section being
// given three more arguments. What they write is kept, not sent: whether any of them said it is only
// known once all of them are drawn, and when none did it is said above them, as it used to be.
type sectionWriter struct {
	http.ResponseWriter
	body               bytes.Buffer
	saved, problem, at string
	said               bool
}

func (s *sectionWriter) Write(p []byte) (int, error) { return s.body.Write(p) }

// cameBackTo is whether the save the page came back from was made in this section.
func cameBackTo(w io.Writer, section string) bool {
	s, ok := w.(*sectionWriter)
	return ok && !s.said && s.at == section && (s.saved != "" || s.problem != "")
}

// sayHere says what the last save did, when it was made in this section. Once: a section drawn twice
// would not say it twice.
func sayHere(w io.Writer, section string) {
	if !cameBackTo(w, section) {
		return
	}
	s := w.(*sectionWriter)
	s.said = true
	banner(w, s.saved, s.problem)
}

// banner is what a save did, or what was wrong with it.
func banner(w io.Writer, saved, problem string) {
	if saved != "" {
		fmt.Fprintf(w, `<div class="banner ok" role="status">%s</div>`, html.EscapeString(doneWords(saved)))
	}
	if problem != "" {
		fmt.Fprintf(w, `<div class="banner bad" role="alert">%s</div>`, html.EscapeString(problem))
	}
}

// fieldsetOpen starts a section: its fieldset, named so a save can come back to it, its legend, and
// what the last save did when it was made here. The legend is written as it is given, so it is only
// ever words from this package.
func fieldsetOpen(w io.Writer, section, legend string) {
	fmt.Fprintf(w, `<fieldset id="sec-%s"><legend>%s</legend>`, section, legend)
	sayHere(w, section)
}
