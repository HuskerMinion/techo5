//go:build dot

package setup

import "net/http"

// hasTailnet is whether this device can be on the tailnet at all: the Dot has no Tailscale, so it has
// no Remote setup to put up either (remote.go).
func hasTailnet() bool { return false }

func tailscaleSection(http.ResponseWriter, string) {}

func saveTailscale(*http.Request) string { return "this device has no Tailscale" }
