//go:build !panel

package firewall

// managed: an Echo keeps its own chains in the vendor firewall.
const managed = true
