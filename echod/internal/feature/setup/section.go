package setup

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
)

// A save comes back to the section it was made in. Every section with a form is a card with a name of
// its own, the redirect after a save carries that name (at=) and ends at the card (#sec-name), and
// what happened is said inside it, the card drawn open. It used to be said once, at the top of the
// tab: on a long tab that was as many as sixteen sections above the row just used, and the browser was
// left at the top with it.
//
// A card is a <details>: shut, it is its title and a line about it, so a long tab reads as a list of
// what is on it. It is drawn open when the page comes back to it from a save, when the address names it
// (open=, as the rail's links do), or when the address asks for every one (open=all); and a list that
// is the point of its tab (alwaysOpen) is always drawn open. With the page's small script, the cards a
// browser left open on a tab are opened again the next time (cardScript); without it, all of this
// still works.

// sectionFor is the section a form is drawn in, where that is not one of the form's own name: the
// forms that share a card with others.
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
	"screenkey":      "screenpage",
	"update-install": "updates", "update-check": "updates", "update-auto": "updates",
	"sound-play": "sounds", "sound-remove": "sounds",
	"wakeword-remove": "wakewords",
	"wakeclips-test":  "wakeclips", "wakeclips-trust": "wakeclips", "wakeclips-keep": "wakeclips",
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
//
// It also keeps the cards as they are drawn, for the rail's list of the tab's sections (cards.go): the
// list is made from what the tab drew, so it cannot name a section the tab left out. open is which
// cards the address asks to be drawn open (openOf).
type sectionWriter struct {
	http.ResponseWriter
	body               bytes.Buffer
	saved, problem, at string
	open               string
	said               bool
	cards              []card
}

// card is a section as the rail lists it: its name and its title.
type card struct{ name, title string }

// alwaysOpen are the sections that are drawn open whatever the address says: lists that are what their
// tab is for, not settings. Alarms & Timers is opened to see what goes off next, and a shut card there
// would hide the one thing the tab is opened for.
var alwaysOpen = map[string]bool{"alarm_edit": true}

// openOf is which cards an address's open= asks for: all of them ("all"), none and forget the ones
// this browser keeps open ("none"), one section the page knows, or nothing. Anything else is nothing:
// the value is only compared with names of the page's own, and never written into the page.
func openOf(v string) string {
	if v == "all" || v == "none" {
		return v
	}
	return sectionNamed(v)
}

// drawnOpen is whether a section's card is drawn open. A section drawn somewhere other than a tab of
// the page has nobody to open it, and is drawn open.
func drawnOpen(w io.Writer, section string) bool {
	if alwaysOpen[section] || cameBackTo(w, section) {
		return true
	}
	s, ok := w.(*sectionWriter)
	return !ok || s.open == "all" || s.open == section
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

// sectionOpen starts a section: its card, named so a save can come back to it, its title, a line that
// says where it stands for when it is shut, and what the last save did when it was made here. The
// title is written as it is given, so it is only ever words from this package; the line is escaped,
// and may say what a setting is set to. Every sectionOpen is ended by a sectionClose.
func sectionOpen(w io.Writer, section, title string, summary ...string) {
	open, keep := "", ""
	if drawnOpen(w, section) {
		open = " open"
	}
	if alwaysOpen[section] {
		keep = " data-keep"
	}
	fmt.Fprintf(w, `<details class="card" id="sec-%s"%s%s><summary><span class="ttl">%s</span><span class="sum">%s</span></summary><div class="in">`,
		section, open, keep, title, html.EscapeString(strings.Join(summary, " · ")))
	if s, ok := w.(*sectionWriter); ok {
		s.cards = append(s.cards, card{section, title})
	}
	sayHere(w, section)
}

// sectionClose ends what sectionOpen started.
func sectionClose(w io.Writer) {
	fmt.Fprint(w, `</div></details>`)
}
