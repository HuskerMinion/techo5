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
	"regexp"
	"sort"
	"strings"
)

// The shapes worth taking out wherever they appear. Order matters: the longer, more specific ones
// go first so a MAC address is not eaten by the shorter patterns inside it.
var patterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"key", regexp.MustCompile(`(?i)\b(?:psk|token|secret|password|passphrase|api[_-]?key)\s*[:=]\s*"?([^\s",}]{6,})"?`)},
	{"mac", regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`)},
	{"ipv6", regexp.MustCompile(`\b(?:[0-9A-Fa-f]{1,4}:){4,7}[0-9A-Fa-f]{1,4}\b`)},
	{"ip", regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)},
	{"email", regexp.MustCompile(`\b[\w.+-]+@[\w-]+\.[\w.-]+\b`)},
	{"phone", regexp.MustCompile(`\b\+?\d{10,15}\b`)},
	{"url", regexp.MustCompile(`\b(?:https?)://[^\s"'<>]+`)},
}

// Redactor takes text and gives it back with the private parts replaced. Values it is told about —
// the device's name, its Wi-Fi, its serial — are taken out wherever they appear, whatever shape they
// are in; the patterns above catch the rest.
type Redactor struct {
	// known are exact strings to replace, longest first so "Terry's Desk" goes before "Terry".
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
			// Localhost and the unspecified address say nothing about anybody.
			switch match {
			case "127.0.0.1", "0.0.0.0", "255.255.255.255", "::1":
				return match
			}
			return r.placeholder(p.name, match)
		})
	}
	return s
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
