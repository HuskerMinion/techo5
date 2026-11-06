package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

func init() { saves["wakecall"] = saveWakeCall }

// The phone book and the choice, here so tests can stand in for them.
var (
	wakeCallBook = func() []phone.Contact { return phone.Get().Contacts() }
	setWakeCall  = voice.SetWakeCall
)

// wakeCallSection is which contact, if any, each wake word calls in place of the assistant
// (config.WakeWord.CallContact). Like realtime voice, it sends the room's sound somewhere, so it is
// chosen here, on the device's own page, and a settings file never chooses it.
func wakeCallSection(w http.ResponseWriter, token string) {
	c := config.Get()
	book := wakeCallBook()
	fmt.Fprint(w, `<fieldset><legend>Call on a wake word</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "wakecall", "sound")
	fmt.Fprint(w, `<p class="note"><strong>A wake word chosen here calls that contact as soon as it is heard,
	 in place of the assistant, and the room is heard in the call from the moment it is answered.</strong>
	 Only while the phone is free: during a call the wake word does what it always does. The button that
	 starts the assistant by hand still starts it. Off until a contact is chosen, and a wake word for
	 realtime voice cannot call.</p>`)
	for _, i := range realtimeChoices(c) {
		word := c.Wake.Slot(i)
		name := fmt.Sprintf("Wake word %d", i+1)
		if word.ID != "" {
			name = fmt.Sprintf("Wake word %d (%s)", i+1, word.ID)
		}
		disabled := ""
		if word.Realtime || word.ID == "" {
			disabled = " disabled"
		}
		fmt.Fprintf(w, `<label for="call%d">%s</label><select id="call%d" name="call%d"%s><option value="">Off</option>`,
			i, html.EscapeString(name), i, i, disabled)
		listed := word.CallContact == ""
		for _, ct := range book {
			on := ct.Name == word.CallContact
			listed = listed || on
			fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, html.EscapeString(ct.Name), selected(on),
				html.EscapeString(ct.Name))
		}
		if !listed {
			// Chosen, and gone from the book since: shown, so that saving does not change it unseen.
			fmt.Fprintf(w, `<option value="%s" selected>%s (not in the phone book)</option>`,
				html.EscapeString(word.CallContact), html.EscapeString(word.CallContact))
		}
		fmt.Fprintf(w, `</select><input type="hidden" name="was_call%d" value="%s">`, i,
			html.EscapeString(word.CallContact))
		if word.Realtime {
			fmt.Fprint(w, `<p class="note">Answered by realtime voice.</p>`)
		}
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

// saveWakeCall changes the slots whose choice was changed on the page.
func saveWakeCall(r *http.Request) string {
	_ = r.ParseForm()
	for _, i := range realtimeChoices(config.Get()) {
		was, ok := r.PostForm[fmt.Sprintf("was_call%d", i)]
		if !ok || len(was) == 0 {
			continue
		}
		// A select that was disabled sends nothing: it is left as it was.
		now, sent := r.PostForm[fmt.Sprintf("call%d", i)]
		if !sent || len(now) == 0 || strings.TrimSpace(now[0]) == strings.TrimSpace(was[0]) {
			continue
		}
		if err := setWakeCall(i+1, now[0]); err != nil {
			return strings.TrimPrefix(err.Error(), "wake call: ")
		}
		slog.Info("setup page: wake call saved", "slot", i+1, "on", strings.TrimSpace(now[0]) != "")
	}
	return ""
}
