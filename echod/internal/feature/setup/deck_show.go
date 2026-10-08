//go:build !dot && !spot

package setup

import (
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/deck"
	"github.com/HuskerMinion/techo5/echod/internal/lib/obsws"
)

// deckSection is TECHO5 Deck on the Screen & Photos tab: where OBS is, the grid, and every page's
// buttons, one form a page. The scene and input boxes suggest OBS's own names while it's connected,
// so nobody has to type them exactly.
func deckSection(w http.ResponseWriter, token string) {
	d := config.Get().Deck
	st := deck.Get().OBS()
	fmt.Fprint(w, `<fieldset><legend>Deck</legend>
	 <p class="note" style="margin-top:0">Buttons for OBS Studio on this screen. Swipe up from the bottom
	  edge of the clock to open it, swipe down to put it away; swipe left and right for the pages.</p>`)

	// OBS.
	status := "Not set up."
	switch {
	case d.OBS.Addr == "":
	case st.Connected:
		status = fmt.Sprintf("Connected: %d scenes, live scene %s.", len(st.Scenes), st.Scene)
	case st.Problem != "":
		status = st.Problem + "."
	default:
		status = "Connecting…"
	}
	pwNote := "No password saved."
	if d.OBS.Password != "" {
		pwNote = "A password is saved. Leave this empty to keep it."
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "deckobs", "photos")
	fmt.Fprintf(w, `<label for="obsaddr">OBS address</label>
	 <input id="obsaddr" name="address" value="%s" placeholder="192.168.1.20" autocomplete="off">
	 <label for="obspw">OBS WebSocket password</label>
	 <input id="obspw" name="password" type="password" autocomplete="off">
	 <p class="note">%s <label><input type="checkbox" name="nopassword" value="yes" style="width:auto"> OBS has
	  authentication turned off</label></p>
	 <p class="note"><strong>%s</strong> In OBS: Tools → WebSocket Server Settings → Enable WebSocket server;
	  the port is 4455 unless you changed it (then add it: 192.168.1.20:4456). OBS's WebSocket isn't
	  encrypted: the password is never sent, but scene names are readable on your network.</p>
	 <p><button type="submit">Save</button></p></form>`,
		html.EscapeString(d.OBS.Addr), html.EscapeString(pwNote), html.EscapeString(status))

	// The grid.
	cols, rows := d.Grid()
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "deckgrid", "photos")
	fmt.Fprint(w, `<label for="deckcols">Buttons across</label><select id="deckcols" name="cols">`)
	for n := config.DeckColsMin; n <= config.DeckColsMax; n++ {
		fmt.Fprintf(w, `<option%s>%d</option>`, selected(n == cols), n)
	}
	fmt.Fprint(w, `</select><label for="deckrows">Rows</label><select id="deckrows" name="rows">`)
	for n := config.DeckRowsMin; n <= config.DeckRowsMax; n++ {
		fmt.Fprintf(w, `<option%s>%d</option>`, selected(n == rows), n)
	}
	fmt.Fprint(w, `</select><p><button type="submit">Save</button></p></form>`)

	deckComputersPart(w, token, d)

	// OBS's names and the computers' apps and scripts, for the boxes to suggest.
	deckNames(w, "deck-scenes", st.Scenes)
	deckNames(w, "deck-inputs", st.Inputs)
	var pcNames []string
	for _, s := range deck.Get().Computers() {
		if s.Hello != nil {
			pcNames = append(pcNames, s.Hello.Scripts...)
			pcNames = append(pcNames, s.Hello.Apps...)
		}
	}
	deckNames(w, "deck-pc", pcNames)

	pages := max(len(d.Pages), 1)
	for p := range pages {
		open := ""
		if p == 0 {
			open = " open"
		}
		fmt.Fprintf(w, `<details%s><summary>Page %d</summary><form method="post" action="/setup/save?big=deck">`, open, p+1)
		hidden(w, token, "deckpage", "photos")
		fmt.Fprintf(w, `<input type="hidden" name="page" value="%d">`, p)
		for i := range cols * rows {
			deckButtonRow(w, i, cols, d.Button(p, i), d.Computers)
		}
		fmt.Fprint(w, `<p><button type="submit">Save page</button>`)
		if p == pages-1 && pages < config.DeckPagesMax {
			fmt.Fprint(w, ` <button type="submit" name="op" value="add">Add a page</button>`)
		}
		if pages > 1 {
			fmt.Fprint(w, ` <button type="submit" name="op" value="remove">Remove this page</button>`)
		}
		fmt.Fprint(w, `</p></form></details>`)
	}
	fmt.Fprint(w, `<p class="note"><strong>Scene or input</strong> is the scene to switch to, the input to mute, or
	 the scene a source is in. <strong>Icon</strong> is a Material Design icon name (pictogrammers.com/library/mdi),
	 like <code>microphone-off</code>; empty uses the button's own. Home Assistant can press a button with the
	 <code>deck_press</code> action.</p></fieldset>`)
}

func deckNames(w http.ResponseWriter, id string, names []string) {
	fmt.Fprintf(w, `<datalist id="%s">`, id)
	for _, n := range names {
		fmt.Fprintf(w, `<option value="%s">`, html.EscapeString(n))
	}
	fmt.Fprint(w, `</datalist>`)
}

// deckButtonRow is one button's boxes. The scene-or-input box holds the input for a mute button and
// the scene for the others, so a row has one box for either.
func deckButtonRow(w http.ResponseWriter, i, cols int, b config.DeckButton, pcs []config.DeckComputer) {
	target := b.Scene
	list := "deck-scenes"
	if b.Action == config.DeckOBSMute {
		target, list = b.Input, "deck-inputs"
	}
	n := strconv.Itoa(i)
	fmt.Fprintf(w, `<div class="deckrow"><p class="note" style="margin:.6em 0 .2em"><strong>Row %d, button %d</strong></p>`, i/cols+1, i%cols+1)
	fmt.Fprintf(w, `<input name="label%s" value="%s" maxlength="60" placeholder="Label" aria-label="Label" autocomplete="off">`, n, html.EscapeString(b.Label))
	fmt.Fprintf(w, `<select name="action%s" aria-label="Action">`, n)
	for _, a := range config.DeckActions() {
		switch a {
		case config.DeckOBSScene:
			fmt.Fprint(w, `<optgroup label="OBS">`)
		case config.DeckPCKeys:
			fmt.Fprint(w, `</optgroup><optgroup label="Computer">`)
		}
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, a, selected(a == b.Action), html.EscapeString(a.Label()))
	}
	fmt.Fprint(w, `</optgroup></select>`)
	fmt.Fprintf(w, `<input name="target%s" value="%s" maxlength="200" placeholder="Scene or input" aria-label="Scene or input" list="%s" autocomplete="off">`,
		n, html.EscapeString(target), list)
	fmt.Fprintf(w, `<input name="source%s" value="%s" maxlength="200" placeholder="Source (show/hide only)" aria-label="Source" autocomplete="off">`,
		n, html.EscapeString(b.Source))
	if len(pcs) > 0 || b.Computer != "" {
		fmt.Fprintf(w, `<select name="computer%s" aria-label="Computer"><option value="">Computer…</option>`, n)
		for _, c := range pcs {
			fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(c.Name), selected(c.Name == b.Computer), html.EscapeString(c.Name))
		}
		if _, ok := (config.Deck{Computers: pcs}).Computer(b.Computer); !ok && b.Computer != "" {
			fmt.Fprintf(w, `<option value="%s" selected>%s (not paired)</option>`, html.EscapeString(b.Computer), html.EscapeString(b.Computer))
		}
		fmt.Fprint(w, `</select>`)
		fmt.Fprintf(w, `<input name="value%s" value="%s" maxlength="1000" placeholder="Keys, text, app, website or script" aria-label="Keys, text, app, website or script" list="deck-pc" autocomplete="off">`,
			n, html.EscapeString(b.Value))
	}
	fmt.Fprintf(w, `<select name="color%s" aria-label="Color">`, n)
	for _, c := range config.DeckColors {
		label := "Theme color"
		if c != "" {
			label = strings.ToUpper(c[:1]) + c[1:]
		}
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, c, selected(c == b.Color), html.EscapeString(label))
	}
	fmt.Fprint(w, `</select>`)
	fmt.Fprintf(w, `<input name="icon%s" value="%s" maxlength="60" placeholder="Icon (optional)" aria-label="Icon" autocomplete="off"></div>`,
		n, html.EscapeString(b.Icon))
}

// saveDeckOBS keeps where OBS is. An empty password keeps the saved one; the box for an OBS with
// authentication off clears it.
func saveDeckOBS(r *http.Request) string {
	addr := strings.TrimSpace(r.PostFormValue("address"))
	if addr != "" {
		if _, err := obsws.URL(addr); err != nil {
			return "that is not an address OBS can be at"
		}
	}
	w := config.Set().Deck()
	if err := w.OBS(addr, r.PostFormValue("password")); err != nil {
		return "could not save it: " + err.Error()
	}
	if r.PostFormValue("nopassword") == "yes" {
		if err := w.ForgetOBSPassword(); err != nil {
			return "could not save it: " + err.Error()
		}
	}
	deck.Get().Reload()
	return ""
}

func saveDeckGrid(r *http.Request) string {
	cols, err1 := strconv.Atoi(r.PostFormValue("cols"))
	rows, err2 := strconv.Atoi(r.PostFormValue("rows"))
	if err1 != nil || err2 != nil || cols < config.DeckColsMin || cols > config.DeckColsMax ||
		rows < config.DeckRowsMin || rows > config.DeckRowsMax {
		return "that is not a grid the deck can have"
	}
	if err := config.Set().Deck().Grid(cols, rows); err != nil {
		return "could not save it: " + err.Error()
	}
	return ""
}

// saveDeckPage keeps one page's buttons, and adds or removes a page when asked.
func saveDeckPage(r *http.Request) string {
	p, err := strconv.Atoi(r.PostFormValue("page"))
	if err != nil || p < 0 || p >= config.DeckPagesMax {
		return "that is not a page of the deck"
	}
	w := config.Set().Deck()
	if r.PostFormValue("op") == "remove" {
		if err := w.RemovePage(p); err != nil {
			return "could not save it: " + err.Error()
		}
		deck.Get().Reload()
		return ""
	}
	cols, rows := config.Get().Deck.Grid()
	buttons := make([]config.DeckButton, 0, cols*rows)
	for i := range cols * rows {
		n := strconv.Itoa(i)
		a := config.DeckAction(r.PostFormValue("action" + n))
		if !slices.Contains(config.DeckActions(), a) {
			return "that is not something a deck button does"
		}
		b := config.DeckButton{
			Label: strings.TrimSpace(r.PostFormValue("label" + n)),
			Icon:  strings.TrimPrefix(strings.TrimSpace(r.PostFormValue("icon"+n)), "mdi:"),
			Color: r.PostFormValue("color" + n), Action: a,
		}
		target := strings.TrimSpace(r.PostFormValue("target" + n))
		switch a {
		case config.DeckOBSMute:
			b.Input = target
		case config.DeckOBSScene, config.DeckOBSSource:
			b.Scene = target
		}
		if a == config.DeckOBSSource {
			b.Source = strings.TrimSpace(r.PostFormValue("source" + n))
		}
		if a.OnComputer() {
			b.Computer = r.PostFormValue("computer" + n)
			b.Value = r.PostFormValue("value" + n)
			if a != config.DeckPCType {
				b.Value = strings.TrimSpace(b.Value)
			}
		}
		if problem := deckButtonProblem(i, cols, b); problem != "" {
			return problem
		}
		buttons = append(buttons, b)
	}
	// Buttons past the grid, kept from a bigger grid chosen before, stay as they were.
	if old := config.Get().Deck; p < len(old.Pages) && len(old.Pages[p].Buttons) > len(buttons) {
		buttons = append(buttons, old.Pages[p].Buttons[len(buttons):]...)
	}
	if err := w.Page(p, buttons); err != nil {
		return "could not save it: " + err.Error()
	}
	if r.PostFormValue("op") == "add" {
		if err := w.AddPage(); err != nil {
			return "could not save it: " + err.Error()
		}
	}
	deck.Get().Reload()
	return ""
}

// deckButtonProblem is what a button is missing, said so the row can be found.
func deckButtonProblem(i, cols int, b config.DeckButton) string {
	where := fmt.Sprintf("row %d, button %d", i/cols+1, i%cols+1)
	switch {
	case b.Action == config.DeckOBSScene && b.Scene == "":
		return where + ": which scene?"
	case b.Action == config.DeckOBSMute && b.Input == "":
		return where + ": which input?"
	case b.Action == config.DeckOBSSource && (b.Scene == "" || b.Source == ""):
		return where + ": which scene and source?"
	case b.Action.OnComputer() && b.Computer == "":
		return where + ": which computer?"
	case b.Action.OnComputer() && b.Value == "":
		return where + ": " + map[config.DeckAction]string{config.DeckPCKeys: "which keys?", config.DeckPCType: "what text?",
			config.DeckPCOpen: "which app or website?", config.DeckPCRun: "which script?"}[b.Action]
	}
	return ""
}

// deckComputersPart is the computers the deck can act on: the paired ones and how each is, a look
// on the network for others, and pairing one by its key.
func deckComputersPart(w http.ResponseWriter, token string, d config.Deck) {
	fmt.Fprint(w, `<h3>Computers</h3><p class="note" style="margin-top:0">For buttons that press keys, type, open apps
	 and websites or run scripts on a computer, run the TECHO5 Deck agent there (see the Deck guide).</p>`)
	states := deck.Get().Computers()
	for _, c := range d.Computers {
		st, ok := states[c.Name]
		status := "Checking…"
		switch {
		case ok && st.Hello != nil:
			status = fmt.Sprintf("Connected: %d apps, %d scripts", len(st.Hello.Apps), len(st.Hello.Scripts))
			if !st.Hello.Keys {
				status += "; can't press keys there yet"
			}
		case ok && st.Err != "":
			status = st.Err
		}
		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, "deckpc", "photos")
		fmt.Fprintf(w, `<input type="hidden" name="name" value="%s"><p style="margin:.4rem 0"><strong>%s</strong> <span class="note">%s · %s</span>
		 <button class="quiet" type="submit" name="op" value="forget">Forget</button></p></form>`,
			html.EscapeString(c.Name), html.EscapeString(c.Name), html.EscapeString(c.Addr), html.EscapeString(status))
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "deckpc", "photos")
	fmt.Fprint(w, `<p><button class="quiet" type="submit" name="op" value="look">Look for computers</button>`)
	if len(d.Computers) > 0 {
		fmt.Fprint(w, ` <button class="quiet" type="submit" name="op" value="refresh">Refresh</button>`)
	}
	fmt.Fprint(w, `</p></form>`)

	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "deckpc", "photos")
	fmt.Fprint(w, `<input type="hidden" name="op" value="pair"><label for="pcaddr">Pair a computer</label>`)
	found := deck.Get().LastFound()
	if len(found) > 0 {
		fmt.Fprint(w, `<select id="pcaddr" name="addr">`)
		for _, f := range found {
			fmt.Fprintf(w, `<option value="%s">%s (%s)</option>`, html.EscapeString(f.Addr), html.EscapeString(f.Name), html.EscapeString(f.Addr))
		}
		fmt.Fprint(w, `</select><input name="other" placeholder="or another address" aria-label="Another address" autocomplete="off">`)
	} else {
		fmt.Fprint(w, `<input id="pcaddr" name="addr" placeholder="The computer's address, like 192.168.1.30" autocomplete="off">`)
	}
	fmt.Fprint(w, `<input name="key" type="password" placeholder="The key the agent shows" aria-label="Key" autocomplete="off">
	 <p><button type="submit">Pair</button></p></form>`)
}

// saveDeckPC pairs, forgets, looks for or refreshes the computers.
func saveDeckPC(r *http.Request) string {
	f := deck.Get()
	switch r.PostFormValue("op") {
	case "look":
		f.Look(r.Context())
	case "refresh":
		f.Recheck()
	case "forget":
		if err := config.Set().Deck().ForgetComputer(r.PostFormValue("name")); err != nil {
			return "could not save it: " + err.Error()
		}
		f.Reload()
	case "pair":
		addr := strings.TrimSpace(r.PostFormValue("other"))
		if addr == "" {
			addr = r.PostFormValue("addr")
		}
		if _, err := f.Pair(addr, r.PostFormValue("key")); err != nil {
			return "couldn't pair it: " + err.Error()
		}
	default:
		return "that is not something this page does"
	}
	return ""
}
