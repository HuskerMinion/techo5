//go:build !dot

package plexcast

import "github.com/HuskerMinion/techo5/echod/internal/android/firewall"

// The ports this feature opens while an account is linked, each in a chain of its own, as every
// feature that listens here does (android/firewall): the device's vendor firewall drops what it was not
// told about, and an empty chain is closed. On the Linux image there is no such firewall and these do
// nothing; tools/linux/rootfs/usr/local/sbin/techo5-firewall opens the same ports at boot.
func firewallOpen(port int) error { return firewall.Open(firewall.Plex, port) }

func firewallClose() error { return firewall.Close(firewall.Plex) }

// firewallOpenUDP is Plex's discovery: searches answered on one port, announcements sent to another.
func firewallOpenUDP(from, to int) error { return firewall.OpenUDP(firewall.PlexGDM, from, to) }

func firewallCloseUDP() error { return firewall.Close(firewall.PlexGDM) }
