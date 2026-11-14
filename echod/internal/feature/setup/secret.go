package setup

import (
	"fmt"
	"html"
)

// Passwords, keys and tokens, all drawn one way. They were drawn four: a hint in the placeholder in
// two wordings, a note under the field, a placeholder that said "Kept"; some let a password manager
// offer the wrong login and some did not; three could be removed and the rest could not; and none could
// be looked at while it was typed, on a page used mostly from a phone.
//
// What is the same for all of them, and was already: a secret that is saved is never written into the
// page. The field is always empty, and the page says only whether there is one.

// What a field for a secret that is kept says under it.
const (
	secretSaved = "One is saved. Leave this empty to keep it."
	secretNone  = "None is saved."
)

// secretField is the field for a password, key or token the device keeps: its label, the field, a Show
// box, and whether one is saved. id is the field's id on the page and name what its form posts it as.
// remove names a tick-box that removes the saved one, for a saver that can; "" where there is nothing
// to take away on its own, and the box is only drawn when there is one to remove.
//
// new-password keeps a password manager from filling in a login it has for some other page of this
// device: the field is for typing a secret in, never for signing in with one.
func secretField(id, label, name string, saved bool, remove string) string {
	said := secretNone
	if saved {
		said = secretSaved
	}
	out := typedSecret(id, label, name, "") + `<p class="note secret">` + said + `</p>`
	if saved && remove != "" {
		out += fmt.Sprintf(`<p class="secret"><label><input type="checkbox" name="%s" value="yes"> Remove the saved one</label></p>`,
			html.EscapeString(remove))
	}
	return out
}

// onceField is the field for a secret that is used once and not kept: a key that joins a network, a
// passphrase handed to the Wi-Fi. There is no saved one to speak of, so it is the field and its Show
// box. attrs are further attributes for the field, as HTML.
func onceField(id, label, name, attrs string) string {
	return typedSecret(id, label, name, attrs)
}

// typedSecret is the label, the field and its Show box. The box is drawn hidden and has no name: the
// page's script brings it out and gives it its use (script.go), and without script there is no box
// that does nothing. The field's value is always empty.
func typedSecret(id, label, name, attrs string) string {
	if attrs != "" {
		attrs = " " + attrs
	}
	return fmt.Sprintf(`<label for="%[1]s">%[2]s</label>
	 <input id="%[1]s" name="%[3]s" type="password" value="" autocomplete="new-password"%[4]s>
	 <label class="show" hidden><input type="checkbox" data-show="%[1]s"> Show</label>`,
		html.EscapeString(id), html.EscapeString(label), html.EscapeString(name), attrs)
}
