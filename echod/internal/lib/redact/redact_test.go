package redact

import (
	"strings"
	"testing"
)

// The things the project's own rules say must never reach a public issue: addresses, names, keys,
// serial numbers. This test is the list, and it is the point of the package.
func TestTheThingsThatMustNotGetOut(t *testing.T) {
	r := New()
	r.Known("device", "Terry's Desk")
	r.Known("wifi", "HomeNet-5G")
	r.Known("serial", "G0911B0593450T7T")

	in := strings.Join([]string{
		`I [12.34] wifi joined ssid="HomeNet-5G" addrs=192.168.200.96`,
		`I [12.40] bluetooth up address=A0:D0:DC:06:9D:3E`,
		`I [12.55] home assistant http://192.168.34.155:8123 token=eyJhbGciOiJIUzI1NiJ9.abc`,
		`I [13.02] this device is Terry's Desk (G0911B0593450T7T)`,
		`I [13.10] ipv6 2606:8e80:5066:7e01:aae6:21ff:fe77:3fae`,
		`I [13.20] phone registered peer=+15551234567 psk="s3cretpassphrase"`,
		`I [13.30] serving on 127.0.0.1:8899`,
	}, "\n")

	out := r.Text(in)
	for _, leak := range []string{
		"HomeNet-5G", "192.168.200.96", "A0:D0:DC:06:9D:3E", "192.168.34.155",
		"eyJhbGciOiJIUzI1NiJ9", "Terry's Desk", "G0911B0593450T7T",
		"2606:8e80", "+15551234567", "s3cretpassphrase",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("%q is still in the text:\n%s", leak, out)
		}
	}
	// Localhost says nothing about anybody and is left alone, so a log still reads.
	if !strings.Contains(out, "127.0.0.1") {
		t.Errorf("localhost was replaced, which helps nobody:\n%s", out)
	}
}

// The same address twice reads as the same thing, and two different ones stay different: a log about
// two devices talking has to still make sense afterwards.
func TestTheSameValueReadsTheSameTwice(t *testing.T) {
	r := New()
	out := r.Text("from 192.168.1.10 to 192.168.1.20, then 192.168.1.10 again")
	first := strings.Index(out, "<ip-1>")
	if first < 0 || strings.Count(out, "<ip-1>") != 2 {
		t.Errorf("the repeated address did not come back the same:\n%s", out)
	}
	if !strings.Contains(out, "<ip-2>") {
		t.Errorf("two different addresses became one thing:\n%s", out)
	}
}

// A key's name stays so the line still says what it was, and only the value goes.
func TestAKeyKeepsItsName(t *testing.T) {
	out := New().Text(`api_key="abcdef123456" psk=another-secret-value`)
	if strings.Contains(out, "abcdef123456") || strings.Contains(out, "another-secret-value") {
		t.Errorf("a key survived:\n%s", out)
	}
	if !strings.Contains(out, "api_key") || !strings.Contains(out, "psk") {
		t.Errorf("the line no longer says what was there:\n%s", out)
	}
}

// Something short is not worth replacing everywhere it appears, or a log becomes unreadable.
func TestShortValuesAreLeftAlone(t *testing.T) {
	r := New()
	r.Known("device", "A")
	if out := r.Text("A quick brown fox"); out != "A quick brown fox" {
		t.Errorf("a one-letter name was replaced through the text: %q", out)
	}
}
