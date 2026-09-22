// Package redact takes the things nobody should paste into a public issue out of text: addresses,
// names, serial numbers, keys.
//
// It is used on anything the device offers to hand over — the diagnostics bundle above all — so that
// somebody asking for help does not have to read their own log line by line first, and so that what
// they send is safe by construction rather than by their remembering.
//
// It replaces rather than deletes, because a log with <mac> in it still reads, and a reader can see
// that something was there. Where the same value appears twice it gets the same placeholder, so a
// pair of addresses talking to each other still makes sense.
package redact

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// The shapes worth taking out wherever they appear. Order matters: the longer, more specific ones
// go first so a MAC address is not eaten by the shorter patterns inside it.
var patterns = []struct {
	name string
	re   *regexp.Regexp

	// inner says the value is the first group rather than the whole match, for a pattern that has to
	// see what is either side of a thing in order to know what it is.
	inner bool
}{
	{"key", regexp.MustCompile(`(?i)\b(?:psk|token|secret|password|passphrase|api[_-]?key)\s*[:=]\s*"?([^\s",}]{6,})"?`), false},
	{"mac", regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`), false},
	// An address is matched loosely and then confirmed with the parser, because the shapes Go actually
	// prints are not the eight written-out groups: it compresses the longest run of zeros, so a global
	// address reads 2601:abc:def::1 and a link-local fe80::1%wlan0, and a delegated prefix reads
	// 2601:abc:def::/56. A pattern strict enough to describe all of that is unreadable, and a pattern
	// loose enough to catch it takes in timestamps and hex dumps along with it. So the shape only says
	// where to look, and net/netip says whether it is really an address; anything it refuses is left
	// exactly as it was. The match takes in the character before the value because an address may begin
	// with a colon, which is no word boundary; only the value goes.
	{"ipv6", regexp.MustCompile(`(?:^|[^0-9A-Fa-f:.])((?:[0-9A-Fa-f]{0,4}:){2,7}(?:[0-9A-Fa-f]{1,4}\b)?(?:%[0-9A-Za-z_.-]+)?(?:/\d{1,3})?)`), true},
	{"ip", regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), false},
	{"email", regexp.MustCompile(`\b[\w.+-]+@[\w-]+\.[\w.-]+\b`), false},
	// A telephone number, and not the tail of a number that merely holds ten digits: a rate of
	// 49.98765432109876 has a run of digits after the point that this used to take for a phone, which
	// left a log reading "per_second=49.<phone-130>" line after line and nothing in it worth reading.
	// The match takes in what is either side of the number so it can tell; only the number goes.
	{"phone", regexp.MustCompile(`(?:^|[^\d.+-])(\+?\d{10,15})(?:$|[^\d.])`), true},
	{"url", regexp.MustCompile(`\b(?:https?)://[^\s"'<>]+`), false},
}

// Redactor takes text and gives it back with the private parts replaced. Values it is told about —
// the device's name, its Wi-Fi, its serial — are taken out wherever they appear, whatever shape they
// are in; the patterns above catch the rest.
type Redactor struct {
	// known are exact strings to replace, longest first so "Guest's Desk" goes before "Guest".
	known []struct{ value, with string }

	// seen keeps one placeholder per value, so the same address reads the same twice.
	seen  map[string]string
	count map[string]int
}

func New() *Redactor {
	return &Redactor{seen: map[string]string{}, count: map[string]int{}}
}

// Known names a value to take out: the device's name, an SSID, a serial number. Short values are
// ignored, since replacing every "a" helps nobody.
func (r *Redactor) Known(kind, value string) {
	value = strings.TrimSpace(value)
	if len(value) < 3 {
		return
	}
	r.known = append(r.known, struct{ value, with string }{value, "<" + kind + ">"})
	sort.SliceStable(r.known, func(i, j int) bool { return len(r.known[i].value) > len(r.known[j].value) })
}

// Text is the redacted version.
func (r *Redactor) Text(s string) string {
	for _, k := range r.known {
		s = strings.ReplaceAll(s, k.value, k.with)
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllStringFunc(s, func(match string) string {
			// A key is "psk=abc": the name stays so the line still reads, the value goes.
			if p.name == "key" {
				if i := strings.IndexAny(match, ":="); i >= 0 {
					return match[:i+1] + " <key>"
				}
			}
			// A pattern that had to take in what surrounds a value to recognize it gives back what
			// surrounds it untouched.
			before, value, after := "", match, ""
			if p.inner {
				at := p.re.FindStringSubmatchIndex(match)
				if len(at) < 4 || at[2] < 0 {
					return match
				}
				before, value, after = match[:at[2]], match[at[2]:at[3]], match[at[3]:]
			}
			// Localhost and the unspecified address say nothing about anybody.
			switch value {
			case "127.0.0.1", "0.0.0.0", "255.255.255.255", "::1":
				return match
			}
			// The loose address shape also fits a timestamp, a duration and a line of hex, so what is
			// not an address is put back the way it came.
			if p.name == "ipv6" && !isIPv6(value) {
				return match
			}
			return before + r.placeholder(p.name, value) + after
		})
	}
	return s
}

// isIPv6 says whether the text really is an address, or a prefix: the parser is the only honest
// answer to a question a regular expression cannot ask. A prefix counts because a delegated /56 names
// the household as surely as an address inside it does.
func isIPv6(s string) bool {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return err == nil && p.Addr().Is6()
	}
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is6()
}

// placeholder is the same stand-in every time for the same value, numbered so that two different
// addresses in one log are still two different things.
func (r *Redactor) placeholder(kind, value string) string {
	if got, ok := r.seen[value]; ok {
		return got
	}
	r.count[kind]++
	out := fmt.Sprintf("<%s-%d>", kind, r.count[kind])
	r.seen[value] = out
	return out
}
