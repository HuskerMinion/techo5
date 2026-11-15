package redact

import (
	"regexp"
	"strings"
)

// URLSecrets is s with the login, query and fragment taken off every address in it: where an address
// keeps a camera's password or a token. A colon an error puts after an address stays, so the rest of
// the text still reads. For a log line that has to say what went wrong, which an error naming the
// address it went wrong at does.
func URLSecrets(s string) string {
	return addressRE.ReplaceAllStringFunc(s, func(a string) string {
		tail := ""
		if strings.HasSuffix(a, ":") {
			a, tail = strings.TrimSuffix(a, ":"), ":"
		}
		scheme, rest, _ := strings.Cut(a, "://")
		// The login first. auth is the address up to its path, query or fragment; a login in it ends at
		// its last @. Where auth reads user:pass but holds no @, the password itself has a / ? or #
		// in it, and its @ is further on: the login is cut at the last @ of the whole address, and with
		// none (a space or a quote in the password ended the match first) nothing after the scheme is
		// kept; what follows the space is not an address to this, and an address cannot hold a raw space. An @ in an ordinary path or query is left where it is.
		auth := rest
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			auth = rest[:i]
		}
		switch i, j := strings.LastIndex(auth, "@"), strings.LastIndex(rest, "@"); {
		case i >= 0:
			rest = rest[i+1:]
		case loginCut(auth) && j >= 0:
			rest = rest[j+1:]
		case loginCut(auth):
			return scheme + "://…" + tail
		}
		if i := strings.IndexAny(rest, "?#"); i >= 0 {
			rest = rest[:i]
		}
		return scheme + "://" + rest + tail
	})
}

// loginCut is whether host, the part of an address before its path, reads as the start of a login
// (user:pass) rather than a host and a port: a colon followed by anything but digits.
func loginCut(host string) bool {
	i := strings.LastIndex(host, ":")
	if i < 0 || strings.HasSuffix(host, "]") { // no colon, or a bare IPv6 address
		return false
	}
	port := host[i+1:]
	if port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return true
		}
	}
	return false
}

// addressRE is an address with its scheme, as text repeats it: to the first space or quote.
var addressRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']*`)
