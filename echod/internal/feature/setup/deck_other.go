//go:build dot || spot

package setup

import "net/http"

// The deck is the Show's alone: on the Spot and the Dot there is no section, and a form posted for
// one is refused.

func deckSection(http.ResponseWriter, string) {}

func saveDeckOBS(*http.Request) string  { return "this device has no deck" }
func saveDeckGrid(*http.Request) string { return "this device has no deck" }
func saveDeckPage(*http.Request) string { return "this device has no deck" }
func saveDeckPC(*http.Request) string   { return "this device has no deck" }
