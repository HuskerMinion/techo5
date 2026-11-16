package config

import "slices"

// Sending wake clips to a trainer on the home network (feature/detect, clipsend.go). Off unless chosen:
// it is the one setting that sends what the microphones heard off the device, so it is set on the setup
// page or by the select, and the remote API may only read it.

// The places clips can go.
const (
	SendOff       = ""
	SendTater     = "tater"
	SendWakesmith = "wakesmith"
	SendCustom    = "custom"
)

// SendTargets are the choices, in the order they are listed.
var SendTargets = []string{SendOff, SendTater, SendWakesmith, SendCustom}

// SendLabel is what a target is called on the page and in Home Assistant.
func SendLabel(t string) string {
	switch t {
	case SendTater:
		return "Tater Trainer"
	case SendWakesmith:
		return "wakesmith"
	case SendCustom:
		return "Custom"
	}
	return "Off"
}

// SendTargetOf is the target a label names, and whether it names one.
func SendTargetOf(label string) (string, bool) {
	for _, t := range SendTargets {
		if SendLabel(t) == label {
			return t, true
		}
	}
	return "", false
}

// SendKinds are the kinds of clip that can be sent, as the clip folders name them.
var SendKinds = []string{"wake", "nearmiss", "mistake", "noise"}

// WakeSend is where kept wake clips are uploaded to, and which of them.
type WakeSend struct {
	// Target is one of the Send constants; empty is off.
	Target string `json:"target,omitempty"`

	// URL is the trainer's address: for Tater Trainer its base, for the others the base the API paths
	// are added to.
	URL string `json:"url,omitempty"`

	// Token is the bearer token wakesmith and Custom receivers ask for. A secret: never shown back, never
	// in a diagnostic bundle or an exported settings file.
	Token string `json:"token,omitempty"`

	// Skip is the kinds that are not sent. Every kind is sent unless it is named here.
	Skip []string `json:"skip,omitempty"`

	// Pin is the SHA-256 of the trainer's certificate (hex), trusted by the owner when it does not verify
	// normally, as a self-signed one does. It is for the address saved with it: a new address clears it.
	Pin string `json:"pin,omitempty"`

	// Since is when the target was set (UTC, RFC 3339): only clips kept after it are sent, so turning
	// this on never uploads what was kept before anybody said where it may go.
	Since string `json:"since,omitempty"`
}

// On is whether clips are to be sent anywhere.
func (s WakeSend) On() bool { return s.Target != SendOff && s.URL != "" }

// Sends is whether clips of a kind are sent.
func (s WakeSend) Sends(kind string) bool { return !slices.Contains(s.Skip, kind) }

// SendUpdate changes where wake clips go, in one save. f gets the setting as it is.
func (w Writer) SendUpdate(f func(*WakeSend)) error {
	return w.st.Update(func(c *Config) { f(&c.Wake.Send) })
}
