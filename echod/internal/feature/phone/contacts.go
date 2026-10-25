package phone

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Contacts are the phone book: the people and places a screen offers to call, and the names a call
// asked for by voice is matched against. It is set from Home Assistant (the phone_contacts action) or
// the setup page. Phone numbers are personal, so they are kept like the login: their own owner-only
// file, not state.json, and never in a log or a Home Assistant entity.
var contactsPath = filepath.Join(layout.StateDir, "phone-contacts.json")

// Contact is one entry: a name to show, what to dial, and other names voice may use for it ("Mother"
// for "Mom"), which are never shown.
type Contact struct {
	Name   string   `json:"name"`
	Number string   `json:"number"`
	Also   []string `json:"also,omitempty"`
}

// Names is the contact's shown name followed by its other names.
func (c Contact) Names() []string { return append([]string{c.Name}, c.Also...) }

// MaxContacts is as many as a screen can list without scrolling.
const MaxContacts = 12

// Contacts is the list, in the order it was given.
func (p *Phone) Contacts() []Contact {
	b, err := os.ReadFile(contactsPath)
	if err != nil {
		return nil
	}
	var out []Contact
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// SetContacts replaces the phone book with one written as parseContacts reads it, and reports how many
// contacts it now has. A list that does not read saves nothing. The log says how many, never who or
// what number.
func (p *Phone) SetContacts(s string) (int, error) {
	list, err := parseContacts(s)
	if err != nil {
		return 0, err
	}
	if err := saveContacts(list); err != nil {
		return 0, err
	}
	slog.Info("phone: contacts replaced", "count", len(list))
	p.Changed.Emit(p.State())
	return len(list), nil
}

// parseContacts reads "Name=number, Name=number": entries apart by commas, semicolons or new lines.
// A name can be followed by other names for the same contact, each after a |, as in
// "Mom|Mother=15551234567": the first is the one shown. An empty string is an empty list.
//
// A name may be used once in the whole book, other names included, and whatever its capitals: "call
// Mom" has to mean one number. The errors name the entry by its place, not by what it says, since what
// it says can be a number and errors are logged.
func parseContacts(s string) ([]Contact, error) {
	var out []Contact
	seen := map[string]bool{}
	n := 0
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n++
		names, number, ok := strings.Cut(part, "=")
		number = dialable(number)
		if !ok || number == "" {
			return nil, fmt.Errorf("phone: contact %d is not name=number", n)
		}
		var c Contact
		for i, name := range strings.Split(names, "|") {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("phone: contact %d has an empty name", n)
			}
			key := nameKey(name)
			if seen[key] {
				return nil, fmt.Errorf("phone: contact %d has a name that is in the phone book twice", n)
			}
			seen[key] = true
			if i == 0 {
				c.Name = name
			} else {
				c.Also = append(c.Also, name)
			}
		}
		c.Number = number
		out = append(out, c)
	}
	if len(out) > MaxContacts {
		return nil, fmt.Errorf("phone: %d contacts, at most %d fit on a screen", len(out), MaxContacts)
	}
	return out, nil
}

// nameKey is what two names are compared by: their words, in lower case, so "Mom", "mom" and "Mom."
// are the same name, as they are to voice. A name with no words in it is compared whole.
func nameKey(name string) string {
	if words := NameWords(name); len(words) > 0 {
		return strings.Join(words, " ")
	}
	return strings.ToLower(name)
}

// NameWords are the words of a name or a sentence, as voice compares them: lower case, letters, digits
// and apostrophes, everything else a space.
func NameWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
}

// FormatContacts writes a list the way parseContacts reads it, one contact to a line: "Mom|Mother =
// 15551234567".
func FormatContacts(list []Contact) string {
	lines := make([]string, 0, len(list))
	for _, c := range list {
		lines = append(lines, strings.Join(c.Names(), "|")+" = "+c.Number)
	}
	return strings.Join(lines, "\n")
}

func saveContacts(list []Contact) error {
	if len(list) == 0 {
		err := os.Remove(contactsPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	tmp := contactsPath + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, contactsPath)
}
