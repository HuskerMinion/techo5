package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

func init() { saves["phonebook"] = savePhoneBook }

// phoneBookSection is the phone's contacts, as the phone_contacts action sets them, and whether "call
// Mom" said to the device calls them. Only on a device that can hear: one without microphones never
// signs in to place a call (phone.Run).
//
// The book carries what the page showed beside it, as the speaker's fields do, so a save that leaves it
// alone does not put back an older list over one Home Assistant has sent since. The numbers are on this
// page only, which is the device's own and open only to somebody who pressed its button: never in the
// log, and never in Home Assistant.
func phoneBookSection(w http.ResponseWriter, token string) {
	book := phone.FormatContacts(phone.Get().Contacts())
	byVoice := phone.CallByVoice()
	fmt.Fprint(w, `<fieldset><legend>Phone book</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "phonebook", "sound")
	fmt.Fprintf(w, `<label for="contacts">Contacts, one to a line</label>
	 <textarea id="contacts" name="contacts" rows="6" autocomplete="off" spellcheck="false" placeholder="Mom|Mother = 15551234567">%s</textarea>
	 <input type="hidden" name="was_contacts" value="%s">
	 <p class="note">A name, an equals sign and the number: <code>Alex = 15551234567</code>. Other names for
	  the same person go after the first, each after a <code>|</code>: <code>Mom|Mother = 15551234567</code>.
	  The first name is the one the screen shows; voice can use any of them. Up to %d contacts, and a
	  name only once. Home Assistant's <code>phone_contacts</code> action sets the same list.</p>
	 <p><label><input type="checkbox" name="byvoice" value="yes" style="width:auto"%s> Call contacts by voice:
	  "call Mom" said to this device calls Mom's number, after saying whom it is calling. The stop word or
	  Hang up within a few seconds calls it off.</label><input type="hidden" name="was_byvoice" value="%t"></p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(book), html.EscapeString(book), phone.MaxContacts, checkedIf(byVoice), byVoice)
}

// savePhoneBook keeps the book when it was changed on the page, and the switch when it was.
func savePhoneBook(r *http.Request) string {
	_ = r.ParseForm()
	if _, ok := r.PostForm["contacts"]; ok {
		// A browser sends a text box's lines apart by \r\n, whatever the page gave it.
		book := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("contacts"), "\r\n", "\n"))
		was := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("was_contacts"), "\r\n", "\n"))
		if book != was {
			if _, err := phone.Get().SetContacts(book); err != nil {
				return strings.TrimPrefix(err.Error(), "phone: ")
			}
		}
	}
	// A checkbox left clear sends nothing, so this is read from whether it came.
	if was := r.PostFormValue("was_byvoice"); was != "" {
		if on := r.PostFormValue("byvoice") == "yes"; strconv.FormatBool(on) != was {
			phone.Get().SetCallByVoice(on)
		}
	}
	slog.Info("setup page: phone book saved")
	return ""
}
