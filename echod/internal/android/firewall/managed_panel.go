//go:build panel

package firewall

// managed is false on a panel: an app cannot touch iptables, and Android lets an app's listening
// ports in as they are, so there is nothing to open.
const managed = false
