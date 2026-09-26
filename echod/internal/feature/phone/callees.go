package phone

import (
	"errors"

	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// Callee is someone a screen offers to call: another device in the house, over the intercom, or one of
// the phone's contacts, over the phone line.
type Callee struct {
	Name   string
	Number string // a contact's number; empty for a device
	Device bool
}

// Callees is who a screen's Call list shows: the other devices in the house first, by name, while this
// one has a house word, then the contacts, while the phone is signed in. A device answers only to its house
// word and a contact only over a signed-in line, so neither is offered where it could not be reached.
func (p *Phone) Callees() []Callee {
	var out []Callee
	if intercomOpen() {
		for _, pe := range announce.Peers() {
			out = append(out, Callee{Name: pe.Name, Device: true})
		}
	}
	if p.State().Registered {
		for _, c := range p.Contacts() {
			out = append(out, Callee{Name: c.Name, Number: c.Number})
		}
	}
	return out
}

// CallCallee calls someone from a Call list, whichever line reaches them.
func (p *Phone) CallCallee(c Callee) error {
	if c.Device {
		return p.CallDevice(c.Name)
	}
	if c.Number == "" {
		return errors.New("phone: no number to call")
	}
	return p.Call(c.Number)
}
