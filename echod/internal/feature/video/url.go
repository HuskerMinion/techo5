package video

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// reNumeric is a host made of numbers alone, decimal or hex, between dots.
var reNumeric = regexp.MustCompile(`^(0x[0-9a-f]*|[0-9]+)(\.(0x[0-9a-f]*|[0-9]+))*\.?$`)

// mostURL bounds an address: anything longer is not one a person or a server meant.
const mostURL = 4096

// CheckURL accepts what the player may fetch: an http or https address naming a host other than the
// device itself. Nothing else: no file, data, rtsp or pipe address, no local path, no ffmpeg protocol
// prefix (concat:, subfile:, crypto+…). The decoder is also run with only the network protocols
// allowed and has no file protocol built in at all, so this is the first of three fences, not the
// only one.
func CheckURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("no address")
	}
	if len(raw) > mostURL {
		return nil, errors.New("the address is too long")
	}
	for _, r := range raw {
		// Control characters and spaces have no place in an address, and a newline in one is how a
		// header or a second command gets smuggled into whatever reads it next.
		if r <= ' ' || r == 0x7f {
			return nil, errors.New("the address has spaces or control characters in it")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("not an address")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "":
		return nil, errors.New("not an address: a path or a name alone is not fetched")
	default:
		return nil, errors.New("only http and https addresses are played, not " + u.Scheme)
	}
	// ffmpeg reads the scheme itself, case and all: what is handed on is lower case, so what it sees is
	// what was checked.
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Opaque != "" || u.Host == "" {
		return nil, errors.New("the address names no host")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, errors.New("the address names no host")
	}
	if strings.Contains(host, "%") {
		// An IPv6 address with a zone (fe80::1%wlan0) is a link on the device itself: never a media
		// server's address, and not one Go and the C library read alike.
		return nil, errors.New("not an address this device fetches from: " + host)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, errors.New("the device does not fetch from itself")
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return nil, errors.New("the device does not fetch from " + host)
	}
	// The other ways of writing an address the C library takes and Go does not (127.1, 2130706433,
	// 0x7f000001): refused rather than told apart, since nobody writes a real one so.
	if ip == nil && reNumeric.MatchString(host) {
		return nil, errors.New("not an address this device fetches from: " + host)
	}
	return u, nil
}

// Host is the address's host, for the screen and Home Assistant: never the path or a password in it.
func Host(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Hostname()
}

// Redacted is the address for the log: no password, and no query, which is where media servers put
// their tokens (Plex's X-Plex-Token, Jellyfin's api_key).
func Redacted(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.User = nil
	if c.RawQuery != "" {
		c.RawQuery = "…"
	}
	c.Fragment = ""
	return c.String()
}
