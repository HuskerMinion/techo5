// Package dnstext turns the text of a DNS record back into what its sender wrote.
//
// The multicast DNS library hands a TXT string or an instance name over in zone-file form, not as
// the bytes that came off the network: every byte outside printable ASCII is written as a decimal
// escape (\DDD), and a backslash, a quote, and in a name a dot or a space, get a backslash in front
// (RFC 1035 section 5.1, the form RFC 6763 instance names are shown in). A device named "Echo Show 8
// Küche" arrives as "Echo Show 8 K\195\188che", and that is what the Call list showed.
package dnstext

import (
	"strings"
	"unicode"
)

// Unescape undoes the zone-file escaping: \DDD is the byte with that decimal value and \X is X. A
// backslash with nothing valid after it is kept as it is. Bytes that do not make UTF-8 once decoded
// (a sender on another character set) become the replacement character, so a screen never draws
// half a letter.
func Unescape(s string) string {
	return printable(unescape(s))
}

// printable leaves out what a name has no use for and could be used to mislead with: control
// characters (a line break in a log line, an escape on a terminal) and format characters (a
// right-to-left override that makes a name read as another, a zero-width space). Any device on the
// network can advertise a name, and once decoded these are real characters, not \DDD text.
func printable(s string) string {
	if strings.IndexFunc(s, hidden) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if hidden(r) {
			return -1
		}
		return r
	}, s)
}

func hidden(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		if v, ok := decimal(s[i+1:]); ok {
			b.WriteByte(v)
			i += 3
			continue
		}
		b.WriteByte(s[i+1])
		i++
	}
	return strings.ToValidUTF8(b.String(), "�")
}

// decimal reads the three digits of a \DDD escape, which name one byte (000 to 255).
func decimal(s string) (byte, bool) {
	if len(s) < 3 {
		return 0, false
	}
	n := 0
	for _, d := range []byte(s[:3]) {
		if d < '0' || d > '9' {
			return 0, false
		}
		n = n*10 + int(d-'0')
	}
	if n > 255 {
		return 0, false
	}
	return byte(n), true
}
