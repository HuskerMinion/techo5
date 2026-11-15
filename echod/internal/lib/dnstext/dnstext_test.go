package dnstext

import (
	"testing"

	"github.com/miekg/dns"
)

func TestUnescape(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"Kitchen":                      "Kitchen",
		`Echo Show 8 K\195\188che`:     "Echo Show 8 Küche",
		`Stra\195\159e`:                "Straße",
		`Echo\ Show\ 8\ K\195\188che`:  "Echo Show 8 Küche",
		`a\.b`:                         "a.b",
		`back\\slash`:                  `back\slash`,
		`say \"hi\"`:                   `say "hi"`,
		`\0659`:                        "A9",
		`trailing\`:                    `trailing\`,
		`\12`:                          "12",
		`\999`:                         "999",
		`\255`:                         "�",
		`Wohnzimmer \226\128\147 Sofa`: "Wohnzimmer – Sofa",
	} {
		if got := Unescape(in); got != want {
			t.Errorf("Unescape(%q) = %q, want %q", in, got, want)
		}
	}
}

// What the multicast DNS library makes of a record on the wire comes back as what was sent: a TXT
// string and an instance name, each with letters outside ASCII and characters it escapes. They are
// put in the records the way the advertiser does, as plain strings; a dot inside a label is the one
// thing the sender has to escape itself.
func TestThroughTheWire(t *testing.T) {
	const name = `Echo Show 8 Küche "Süd" v1.0`
	txt := &dns.TXT{
		Hdr: dns.RR_Header{Name: "x.local.", Rrtype: dns.TypeTXT, Class: dns.ClassINET},
		Txt: []string{"name=" + name},
	}
	ptr := &dns.PTR{
		Hdr: dns.RR_Header{Name: "_techo5._tcp.local.", Rrtype: dns.TypePTR, Class: dns.ClassINET},
		Ptr: `Echo Show 8 Küche "Süd" v1\.0._techo5._tcp.local.`,
	}
	m := new(dns.Msg)
	m.Answer = []dns.RR{txt, ptr}
	wire, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	back := new(dns.Msg)
	if err := back.Unpack(wire); err != nil {
		t.Fatal(err)
	}
	if got := Unescape(back.Answer[0].(*dns.TXT).Txt[0]); got != "name="+name {
		t.Errorf("TXT %q, want %q", got, "name="+name)
	}
	label, _, _ := cutLabel(back.Answer[1].(*dns.PTR).Ptr)
	if got := Unescape(label); got != name {
		t.Errorf("instance %q, want %q", got, name)
	}
}

// cutLabel is the first label of an escaped name, up to the first dot that is not escaped.
func cutLabel(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '.':
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// A decoded name keeps its letters but not control or format characters: a line break, an escape, a
// right-to-left override or a zero-width space.
func TestUnescapeLeavesOutWhatMisleads(t *testing.T) {
	for in, want := range map[string]string{
		`Kitchen\010Fake`:     "KitchenFake",
		`Den\027[31m`:         "Den[31m",
		`K\195\188che`:        "Küche",
		"Office\u202Etxt.exe": "Officetxt.exe",
		"Hall\u200Bway":       "Hallway",
		`Garage\009Two`:       "GarageTwo",
	} {
		if got := Unescape(in); got != want {
			t.Errorf("Unescape(%q) = %q, want %q", in, got, want)
		}
	}
}
