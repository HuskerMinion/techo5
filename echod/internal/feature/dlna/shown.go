package dlna

import (
	"net"
	"net/url"
	"strings"
)

// shownURI is the address of s as the renderer reports it to the controller at host, in its answers
// and its events. The controller that set it gets it whole. Any other gets it with the values of
// query parameters that look like credentials blanked: an address often carries the token of the
// server it comes from (a Plex server's X-Plex-Token), and any device on the network may ask what is
// playing.
func shownURI(s song, host string) string {
	if s.uri == "" || sameHost(s.from, host) {
		return s.uri
	}
	return withoutSecrets(s.uri)
}

// sameHost is whether two hosts are the same address (an IPv4 address and its IPv6-mapped form are).
func sameHost(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	return ia != nil && ib != nil && ia.Equal(ib)
}

// secretParam is whether a query parameter's name looks like a credential.
func secretParam(name string) bool {
	n := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	for _, s := range []string{"token", "apikey", "password", "passwd", "secret", "auth", "sig", "signature", "key"} {
		if n == s || strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// withoutSecrets is u with the values of its credential-like query parameters blanked, and any user
// and password in it dropped. An address that cannot be read is not shown at all.
func withoutSecrets(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	p.User = nil
	if p.RawQuery == "" {
		return p.String()
	}
	parts := strings.Split(p.RawQuery, "&")
	for i, kv := range parts {
		name, _, _ := strings.Cut(kv, "=")
		if dec, err := url.QueryUnescape(name); err == nil {
			name = dec
		}
		if secretParam(name) {
			parts[i] = strings.SplitN(kv, "=", 2)[0] + "="
		}
	}
	p.RawQuery = strings.Join(parts, "&")
	return p.String()
}
