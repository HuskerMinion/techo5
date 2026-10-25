//go:build !panel

package setup

import "github.com/HuskerMinion/techo5/echod/internal/android/dns"

// resolver points Go's own resolver at the nameservers an Echo's Android reports: echod is built
// without cgo there, so it has no other.
var resolver = []Action{{
	Name:   "point the resolver at the nameservers the platform knows",
	Reason: "there is no /etc/resolv.conf, so Go's resolver has nowhere to look",
	Do:     android(func() error { dns.Use(); return nil }),
}}
