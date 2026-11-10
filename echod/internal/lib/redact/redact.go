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

// haDomains are the Home Assistant entity-id domains worth recognizing: a value "climate.den" or
// "media_player.guest_room" is an entity id, and an object id is often a room or a person. The domain
// is one of a known set so the shape cannot eat an ordinary dotted token (a Go pkg.Func, a config
// key), and the object id that follows is checked against file extensions so a stack trace's
// timer.go or a source=... attribute stays. A domain with an underscore after it (climate_sheet.go)
// never starts the shape at all, because the dot must come right after the domain.
var haDomains = []string{
	"alarm_control_panel", "air_quality", "assist_satellite", "automation", "binary_sensor", "button",
	"calendar", "camera", "climate", "conversation", "counter", "cover", "date", "datetime",
	"device_tracker", "event", "fan", "geo_location", "group", "humidifier", "image", "input_boolean",
	"input_button", "input_datetime", "input_number", "input_select", "input_text", "lawn_mower",
	"light", "lock", "media_player", "notify", "number", "person", "remote", "scene", "schedule",
	"script", "select", "sensor", "siren", "stt", "sun", "switch", "text", "time", "timer", "todo",
	"tts", "update", "vacuum", "valve", "wake_word", "water_heater", "weather", "zone",
}

// entityRE matches a known domain, a dot, and an object id. The domains go in longest first so
// binary_sensor is tried before sensor and input_number before number.
var entityRE = buildEntityRE(haDomains)

func buildEntityRE(domains []string) *regexp.Regexp {
	d := append([]string(nil), domains...)
	sort.Slice(d, func(i, j int) bool { return len(d[i]) > len(d[j]) })
	return regexp.MustCompile(`\b(?:` + strings.Join(d, "|") + `)\.[a-z0-9_]+\b`)
}

// notEntityTail are object-id tails that mean the dotted token is a file name, not an entity id: a
// log's stack trace and source=... attributes name timer.go, person.go, update.go and the like,
// whose base is also a domain. These are put back untouched.
var notEntityTail = map[string]bool{
	"go": true, "py": true, "sh": true, "ps1": true, "c": true, "h": true, "cc": true, "cpp": true,
	"rs": true, "js": true, "ts": true, "mod": true, "sum": true, "md": true, "txt": true, "log": true,
	"json": true, "yaml": true, "yml": true, "toml": true, "ini": true, "cfg": true, "conf": true,
	"xml": true, "html": true, "htm": true, "css": true, "csv": true, "sql": true, "proto": true,
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "svg": true, "ico": true,
	"ttf": true, "otf": true, "bin": true, "pcm": true, "tflite": true, "pb": true, "gz": true,
	"tar": true, "zip": true, "so": true, "dll": true, "exe": true, "out": true, "tmp": true,
	"bak": true, "patch": true, "diff": true, "lock": true,
}

// isEntity is whether id is a Home Assistant entity id rather than a file name: its object-id tail
// (after the one dot) is not a known file extension.
func isEntity(id string) bool {
	_, object, ok := strings.Cut(id, ".")
	return ok && !notEntityTail[object]
}

// The shapes worth taking out wherever they appear. Order matters: the longer, more specific ones
// go first so a MAC address is not eaten by the shorter patterns inside it.
var patterns = []struct {
	name string
	re   *regexp.Regexp

	// inner says the value is the first group rather than the whole match, for a pattern that has to
	// see what is either side of a thing in order to know what it is.
	inner bool
}{
	// A login written into an address of any kind (rtsp://user:pass@camera, http://admin:pw@go2rtc):
	// the login goes, the address is left for the patterns after.
	{"login", regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://([^\s/@"'<>]+)@`), true},
	{"key", regexp.MustCompile(`(?i)\b(?:psk|token|secret|password|passphrase|api[_-]?key)\s*[:=]\s*"?([^\s",}]{6,})"?`), false},
	{"mac", regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`), false},
	// An address is matched loosely and then confirmed with the parser, because the shapes Go actually
	// prints are not the eight written-out groups: it compresses the longest run of zeros, so a global
	// address reads 2601:abc:def::1 and a link-local fe80::1%wlan0, and a delegated prefix reads
	// 2601:abc:def::/56. A pattern strict enough to describe all of that is unreadable, and a pattern
	// loose enough to catch it takes in timestamps and hex dumps along with it. So the shape only says
	// where to look, and net/netip says whether it is really an address; anything it refuses is left
	// exactly as it was. The match takes in the character before the value because an address may begin
	// with a colon, which is no word boundary; only the value goes. That character may not be a letter,
	// a digit or anything else a name is made of, because otherwise the shape starts in the middle of a
	// word: std::string ends in a "d::" that the parser is happy to call an address, and a bundle full
	// of chewed-up C++ is no help to anybody.
	{"ipv6", regexp.MustCompile(`(?:^|[^0-9A-Za-z:.%_])((?:[0-9A-Fa-f]{0,4}:){2,7}(?:[0-9A-Fa-f]{1,4}\b)?(?:%[0-9A-Za-z_.-]+)?(?:/\d{1,3})?)`), true},
	// Four numbers with dots between them are confirmed by the parser too, for the same reason:
	// 999.999.999.999 is not an address and neither is a version number. The shape runs past four parts
	// on purpose so that 1.2.3.4.5 is offered whole and refused whole, rather than having its first four
	// parts taken for an address; and the character before may not be a letter, so v1.2.3.4 stays put.
	{"ip", regexp.MustCompile(`(?:^|[^0-9A-Za-z.])((?:\d{1,3}\.){3,}\d{1,3})`), true},
	{"email", regexp.MustCompile(`\b[\w.+-]+@[\w-]+\.[\w.-]+\b`), false},
	// A telephone number is any run of digits, and phones decides which runs are one: what a number is
	// depends on what is either side of it, and a pattern that takes in its neighbors to look at them
	// also swallows the next number's neighbor, so of two numbers a character apart only the first went.
	{"phone", regexp.MustCompile(`\d+`), false},
	{"url", regexp.MustCompile(`\b(?:https?)://[^\s"'<>]+`), false},
	// A Home Assistant entity id, after the URLs so one inside an address goes with the address. The
	// shape is confirmed in Text, which puts back a file name that only looks like one.
	{"entity", entityRE, false},
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
		if p.name == "phone" {
			s = r.phones(p.re, s)
			continue
		}
		s = p.re.ReplaceAllStringFunc(s, func(match string) string {
			// A key is "psk=abc": the name stays so the line still reads, the value goes.
			if p.name == "key" {
				if i := strings.IndexAny(match, ":="); i >= 0 {
					return match[:i+1] + " <key>"
				}
			}
			// A login is the same wherever it is, and nothing to number.
			if p.name == "login" {
				at := p.re.FindStringSubmatchIndex(match)
				return match[:at[2]] + "<login>" + match[at[3]:]
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
			case "127.0.0.1", "0.0.0.0", "255.255.255.255", "::1", "::":
				return match
			}
			// The loose address shape also fits a timestamp, a duration, a version number and a line of
			// hex, so what is not an address is put back the way it came.
			if (p.name == "ipv6" && !isIP(value, 6)) || (p.name == "ip" && !isIP(value, 4)) {
				return match
			}
			// A dotted token that starts with a domain is a file name, not an entity id, when its tail
			// is a file extension; it stays so a stack trace still reads.
			if p.name == "entity" && !isEntity(value) {
				return match
			}
			return before + r.placeholder(p.name, value) + after
		})
	}
	return s
}

// phones puts the placeholders in for the runs of digits in s that are telephone numbers: ten to
// fifteen digits, with the + in front of them if there is one. Only the number goes; the letters run
// on to it stay, as in caller=Bob<phone-1> or <phone-1>x12.
func (r *Redactor) phones(re *regexp.Regexp, s string) string {
	var out strings.Builder
	last := 0
	for _, at := range re.FindAllStringIndex(s, -1) {
		from, digits, end := at[0], at[0], at[1]
		if n := end - digits; n < 10 || n > 15 {
			continue
		}
		if from > 0 && s[from-1] == '+' {
			from--
		}
		if !isPhone(s, from, digits, end) {
			continue
		}
		out.WriteString(s[last:from])
		out.WriteString(r.placeholder("phone", s[from:end]))
		last = end
	}
	out.WriteString(s[last:])
	return out.String()
}

// isPhone judges the number at s[from:end], whose digits start at s[digits], by what is around it in
// s. A number with a point either side of it is part of a decimal, a version or a timestamp: a rate
// of 49.98765432109876 once read "per_second=49.<phone-130>" line after line. One after a minus sign
// that follows no digit is a negative number. And a number inside a word that is hex all through is
// an address or a hash, not a phone: a kernel stack trace's [<0000000012e3d9f7>] came out as
// [<<phone-1>e3d9f7>]. The word is the letters and digits run on to the number either side, less a
// URL escape in front (tel%3A5551234567 is a phone after "tel:").
func isPhone(s string, from, digits, end int) bool {
	if from > 0 {
		switch s[from-1] {
		case '.':
			return false
		case '-':
			// Only between two phone numbers: a dash after a shorter run is a UUID's, a date's or a
			// version's.
			n := 0
			for i := from - 2; i >= 0 && isDigit(s[i]); i-- {
				n++
			}
			if n < 10 || n > 15 {
				return false
			}
		}
	}
	if end+1 < len(s) && s[end] == '.' && isDigit(s[end+1]) {
		return false
	}
	start, stop := digits, end
	for start > 0 && isWord(s[start-1]) {
		start--
	}
	for stop < len(s) && isWord(s[stop]) {
		stop++
	}
	if start > 0 && s[start-1] == '%' && start+2 <= digits && isHex(s[start:start+2]) {
		start += 2
	}
	word := s[start:stop]
	if len(word) > 2 && word[0] == '0' && (word[1] == 'x' || word[1] == 'X') {
		return false
	}
	// A hex word is a pointer or a hash when it is long, or when its letters sit among digits other than
	// the run itself; a name of hex letters glued to a number ("Ada5551234567") is not.
	if isHex(word) && (len(word) > 15 || strings.ContainsAny(word, "abcdefABCDEF") &&
		strings.ContainsAny(s[start:digits]+s[end:stop], "0123456789")) {
		return false
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWord(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isHex is whether s is hex digits all through.
func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// isIP says whether the text really is an address of the given family, or a prefix in it: the parser
// is the only honest answer to a question a regular expression cannot ask. A prefix counts because a
// delegated /56 names the household as surely as an address inside it does.
func isIP(s string, family int) bool {
	is := func(a netip.Addr) bool {
		if family == 4 {
			return a.Is4()
		}
		return a.Is6()
	}
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return err == nil && is(p.Addr())
	}
	a, err := netip.ParseAddr(s)
	return err == nil && is(a)
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
