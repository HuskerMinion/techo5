package plexcast

import "errors"

// Status is what the device's two interfaces say about the Plex account, and the only thing either of
// them needs to know about a PIN: the screen and the setup page draw the same words from it, and
// neither has to know how a link is made.
//
// It is also what the setup page's poll answers, so its fields have JSON names rather than none.
type Status struct {
	// Linked is whether an account is linked.
	Linked bool `json:"linked"`

	// User is the Plex account's name, empty when plex.tv did not give one: a linked device with no
	// name says "Linked", which is still the truth.
	User string `json:"user,omitempty"`

	// Code is the four characters to type at plex.tv/link while a link is being made, empty otherwise.
	Code string `json:"code,omitempty"`

	// Waiting is a PIN out and being polled.
	Waiting bool `json:"waiting,omitempty"`

	// Err is why the last link did not happen, for the row's own line. It is said in the device's
	// words, never plex.tv's.
	Err string `json:"err,omitempty"`
}

// ErrNotHere is a device with no screen asking for a Plex player: the Dot (none.go). The settings
// screen and the setup page are built for it too, so they need an answer rather than a compile error.
var ErrNotHere = errors.New("plex: this device has no screen to cast to")
