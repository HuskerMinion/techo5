//go:build panel

package phone

// checkCallSource: a panel has no firewall of its own, so anything on the home network can reach the
// listener, and a call is rung for only when it came from the provider (provider.admits).
const checkCallSource = true
