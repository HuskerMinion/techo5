//go:build !dot && !spot

package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/deck"
)

// The deck over the API (#731): read the whole deck, and set, add or remove one page, for an app that
// edits the deck away from the setup page. The setup page's forms need a press on the device and answer
// in HTML; this is the same deck, signed like every other request here.
//
// The API may set less than the setup page. A key in a script is not a person at the setup page, so it
// may make OBS buttons and buttons that open an app the paired computer's agent lists, and nothing that
// presses keys, types or runs a script in somebody's signed-in session. Those buttons stay the setup
// page's: one already on a page has to be sent back unchanged in its place, so the API can neither make,
// change, move nor remove one, and a page holding one cannot be removed through it.

// deckComputers is what was last heard from each paired computer; the deck's, replaced in tests.
var deckComputers = func() map[string]deck.ComputerState { return deck.Get().Computers() }

// deckReload has the deck pick up a saved page now rather than on its next look; replaced in tests.
var deckReload = func() { deck.Get().Reload() }

// deckWrites keeps two API writes from reading the same page and saving over each other.
var deckWrites sync.Mutex

// apiAction is whether the API may make or change a button that does a.
func apiAction(a config.DeckAction) bool {
	return a == config.DeckNone || !a.OnComputer() || a == config.DeckPCOpen
}

// setupOnly is whether a button belongs to the setup page alone: it acts in a signed-in session.
func setupOnly(b config.DeckButton) bool {
	return b.Action == config.DeckPCKeys || b.Action == config.DeckPCType || b.Action == config.DeckPCRun
}

type deckAction struct {
	ID    config.DeckAction `json:"id"`
	Label string            `json:"label"`
	API   bool              `json:"api"` // whether a button doing it may be made or changed here
}

type deckComputer struct {
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Apps      []string `json:"apps"`
}

type deckLimits struct {
	ColsMin  int `json:"cols_min"`
	ColsMax  int `json:"cols_max"`
	RowsMin  int `json:"rows_min"`
	RowsMax  int `json:"rows_max"`
	PagesMax int `json:"pages_max"`
	TextMax  int `json:"text_max"`
	ValueMax int `json:"value_max"`
}

type deckPage struct {
	Buttons []config.DeckButton `json:"buttons"`
}

type deckView struct {
	Cols      int            `json:"cols"`
	Rows      int            `json:"rows"`
	Pages     []deckPage     `json:"pages"`
	Computers []deckComputer `json:"computers"`
	OBS       struct {
		Addr        string `json:"addr"`
		PasswordSet bool   `json:"password_set"`
	} `json:"obs"`
	Actions []deckAction `json:"actions"`
	Colors  []string     `json:"colors"`
	Limits  deckLimits   `json:"limits"`
}

// deckNow is the deck as the API shows it. Never a computer's address or pairing key, nor OBS's
// password: an app editing buttons needs none of them, and the API travels the network unencrypted.
func deckNow() deckView {
	d := config.Get().Deck
	var v deckView
	v.Cols, v.Rows = d.Grid()
	v.Pages = make([]deckPage, 0, len(d.Pages))
	for _, p := range d.Pages {
		v.Pages = append(v.Pages, deckPage{Buttons: append([]config.DeckButton{}, p.Buttons...)})
	}
	states := deckComputers()
	v.Computers = make([]deckComputer, 0, len(d.Computers))
	for _, c := range d.Computers {
		x := deckComputer{Name: c.Name, Apps: []string{}}
		if st, ok := states[c.Name]; ok && st.Hello != nil {
			x.Connected = true
			x.Apps = append(x.Apps, st.Hello.Apps...)
		}
		v.Computers = append(v.Computers, x)
	}
	v.OBS.Addr, v.OBS.PasswordSet = d.OBS.Addr, d.OBS.Password != ""
	for _, a := range config.DeckActions() {
		v.Actions = append(v.Actions, deckAction{ID: a, Label: a.Label(), API: apiAction(a)})
	}
	v.Colors = config.DeckColors
	v.Limits = deckLimits{config.DeckColsMin, config.DeckColsMax, config.DeckRowsMin, config.DeckRowsMax,
		config.DeckPagesMax, config.DeckTextMax, config.DeckValueMax}
	return v
}

func (f *Remote) deckGet(w http.ResponseWriter) { writeJSON(w, http.StatusOK, deckNow()) }

// deckPage sets (PUT) or removes (DELETE) page n, and answers the deck as it is then.
func (f *Remote) deckPage(w http.ResponseWriter, method, n string, body []byte) {
	p, err := strconv.Atoi(n)
	if err != nil || p < 0 || strconv.Itoa(p) != n {
		http.Error(w, "no such page", http.StatusNotFound)
		return
	}
	deckWrites.Lock()
	defer deckWrites.Unlock()
	d := config.Get().Deck
	if method == http.MethodDelete {
		f.deckRemove(w, d, p)
		return
	}
	switch {
	case p > len(d.Pages):
		http.Error(w, fmt.Sprintf("no such page: the deck has %d, and a new one is page %d", len(d.Pages), len(d.Pages)), http.StatusNotFound)
		return
	case p >= config.DeckPagesMax:
		http.Error(w, fmt.Sprintf("the deck has its %d pages already", config.DeckPagesMax), http.StatusBadRequest)
		return
	}
	var in []config.DeckButton
	dec := json.NewDecoder(bytes.NewReader(body))
	// A misspelled field is said, rather than quietly saving a button without it.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "that is not a JSON array of buttons: "+err.Error(), http.StatusBadRequest)
		return
	}
	var old []config.DeckButton
	if p < len(d.Pages) {
		old = d.Pages[p].Buttons
	}
	buttons, problem := deckCheck(d, old, in)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	// One write to the settings file, adding the page when it is the next one.
	if err := config.Set().Deck().Page(p, buttons); err != nil {
		http.Error(w, "could not save it: "+err.Error(), http.StatusInternalServerError)
		return
	}
	deckReload()
	writeJSON(w, http.StatusOK, deckNow())
}

func (f *Remote) deckRemove(w http.ResponseWriter, d config.Deck, p int) {
	if p >= len(d.Pages) {
		http.Error(w, "no such page", http.StatusNotFound)
		return
	}
	if i := slices.IndexFunc(d.Pages[p].Buttons, setupOnly); i >= 0 {
		cols, _ := d.Grid()
		http.Error(w, fmt.Sprintf("row %d, button %d presses keys, types or runs a script on a computer: a page with one is removed on the setup page",
			i/cols+1, i%cols+1), http.StatusBadRequest)
		return
	}
	if err := config.Set().Deck().RemovePage(p); err != nil {
		http.Error(w, "could not save it: "+err.Error(), http.StatusInternalServerError)
		return
	}
	deckReload()
	writeJSON(w, http.StatusOK, deckNow())
}

// deckCheck is the page a PUT would save, from the buttons sent and the page's buttons now (old), or
// what is wrong with it. The page is the grid's squares, those not sent empty, followed by any buttons
// past the grid kept from a bigger grid chosen before, as the setup page keeps them.
func deckCheck(d config.Deck, old, in []config.DeckButton) ([]config.DeckButton, string) {
	cols, rows := d.Grid()
	if len(in) > cols*rows {
		return nil, fmt.Sprintf("the grid is %d × %d: a page has at most %d buttons", cols, rows, cols*rows)
	}
	states := deckComputers()
	out := make([]config.DeckButton, cols*rows, max(cols*rows, len(old)))
	for i := range cols * rows {
		var sent, was config.DeckButton
		if i < len(in) {
			sent = in[i]
		}
		if i < len(old) {
			was = old[i]
		}
		where := fmt.Sprintf("row %d, button %d", i/cols+1, i%cols+1)
		// A button sent back as it is stays, whatever it does: an app that edits one button need not
		// understand the rest, and an "open" button whose computer is off for now is not lost. It is
		// compared as sent as well as tidied, so one saved with a field the form would not keep still
		// counts as unchanged.
		b := tidyButton(sent)
		if sent == was || b == was {
			out[i] = was
			continue
		}
		switch {
		case setupOnly(was):
			return nil, where + " presses keys, types or runs a script on a computer: it is changed or removed on the setup page; send it back unchanged"
		case !slices.Contains(config.DeckActions(), b.Action):
			return nil, where + ": " + strconv.Quote(string(b.Action)) + " is not something a deck button does"
		case !apiAction(b.Action):
			return nil, where + ": buttons that press keys, type or run a script are made on the setup page only"
		case !slices.Contains(config.DeckColors, b.Color):
			return nil, where + ": " + strconv.Quote(b.Color) + " is not one of the deck's colors"
		case tooLong(b):
			return nil, fmt.Sprintf("%s: text is at most %d characters, and a value %d", where, config.DeckTextMax, config.DeckValueMax)
		}
		if problem := config.DeckButtonProblem(i, cols, b); problem != "" {
			return nil, problem
		}
		if b.Action == config.DeckPCOpen {
			if _, ok := d.Computer(b.Computer); !ok {
				return nil, where + ": " + strconv.Quote(b.Computer) + " is not a paired computer"
			}
			st, ok := states[b.Computer]
			if !ok || st.Hello == nil || !slices.Contains(st.Hello.Apps, b.Value) {
				return nil, where + ": " + strconv.Quote(b.Value) + " is not one of the apps " + b.Computer + "'s Deck agent lists"
			}
		}
		out[i] = b
	}
	if len(old) > cols*rows {
		out = append(out, old[cols*rows:]...)
	}
	return out, ""
}

// tidyButton keeps the fields the button's action uses, trimmed, as the setup page's form does, so a
// button saved here reads back the same as one saved there.
func tidyButton(b config.DeckButton) config.DeckButton {
	out := config.DeckButton{
		Label:  strings.TrimSpace(b.Label),
		Icon:   strings.TrimPrefix(strings.TrimSpace(b.Icon), "mdi:"),
		Color:  b.Color,
		Action: b.Action,
	}
	switch b.Action {
	case config.DeckOBSMute:
		out.Input = strings.TrimSpace(b.Input)
	case config.DeckOBSScene:
		out.Scene = strings.TrimSpace(b.Scene)
	case config.DeckOBSSource:
		out.Scene, out.Source = strings.TrimSpace(b.Scene), strings.TrimSpace(b.Source)
	}
	if b.Action.OnComputer() {
		out.Computer, out.Value = b.Computer, b.Value
		if b.Action != config.DeckPCType {
			out.Value = strings.TrimSpace(b.Value)
		}
	}
	return out
}

func tooLong(b config.DeckButton) bool {
	for _, s := range []string{b.Label, b.Icon, b.Scene, b.Source, b.Input, b.Computer} {
		if len(s) > config.DeckTextMax {
			return true
		}
	}
	return len(b.Value) > config.DeckValueMax
}
