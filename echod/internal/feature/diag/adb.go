//go:build !panel

package diag

// adbHere: an Echo under Android can open adbd to the network, through its firewall.
const adbHere = true
