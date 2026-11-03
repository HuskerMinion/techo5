package layout

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMAC(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0A1B2C3D4E5F", "0a:1b:2c:3d:4e:5f"},
		{"0A1B2C3D4E5F\n", "0a:1b:2c:3d:4e:5f"},
		{"0a:1b:2c:3d:4e:5f", "0a:1b:2c:3d:4e:5f"},
		{"000000000000", ""},
		{"00:00:00:00:00:00", ""},
		{"0A1B2C3D4E", ""},
		{"0A1B2C3D4E5F60", ""},
		{"not an address", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := MAC(tt.in); got != tt.want {
			t.Errorf("MAC(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestColor(t *testing.T) {
	// Real serial format, fabricated tails: black units read G090LF…, white G090L9…, and the tail is
	// the part that identifies a unit, so it is zeroed rather than real.
	const (
		blackSerial = "G090LF0000000000"
		whiteSerial = "G090L90000000000"
	)

	tests := []struct {
		name                     string
		productID2, serial, want string
	}{
		{"both say black", "0", blackSerial, "black"},
		{"both say white", "20", whiteSerial, "white"},
		{"white high code", "2020", whiteSerial, "white"},
		{"trailing space", "20 ", whiteSerial, "white"},

		{"fields disagree", "0", whiteSerial, "unknown"},
		{"nothing to read", "", "", "unknown"},
		{"productid2 only", "0", "", "unknown"},
		{"serial only", "", whiteSerial, "white"},
		{"serial too short", "", "G090L", "unknown"},
		{"another model, productid2 0", "0", "G0000600000000000", "unknown"},
		{"another model, productid2 set", "2020", "G0000300000000000", "unknown"},
	}
	for _, tt := range tests {
		if got := Color(tt.productID2, tt.serial); got != tt.want {
			t.Errorf("%s: Color(%q, %q) = %q, want %q", tt.name, tt.productID2, tt.serial, got, tt.want)
		}
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Kitchen", "kitchen"},
		{"Living Room", "living-room"},
		{"kitchen", "kitchen"},
		{"  Front   Porch  ", "front-porch"},
		{"Kid's Room", "kid-s-room"},
		{"Echo Dot A1B2C3", "echo-dot-a1b2c3"},
		{"café", "caf"},
		{"---", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Slug(tt.in); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A name cut to fit a DNS label is cut between characters, never inside one, and a name that fits is
// left as it is.
func TestCutName(t *testing.T) {
	if got := CutName("Kitchen", LabelMax); got != "Kitchen" {
		t.Errorf("a short name came back %q", got)
	}
	for _, name := range []string{strings.Repeat("ü", 31), strings.Repeat("台", 31), strings.Repeat("a", 31) + strings.Repeat("😀", 20)} {
		got := CutName(name, LabelMax)
		if len(got) > LabelMax || !utf8.ValidString(got) || !strings.HasPrefix(name, got) || len(got) < LabelMax-3 {
			t.Errorf("CutName(%q) = %q (%d bytes)", name, got, len(got))
		}
	}
	if got := CutName("ab cd", 3); got != "ab" {
		t.Errorf("a cut before a space came back %q", got)
	}
}

// A name with nothing a slug keeps falls back to the default name's slug with the end of the MAC
// address; any other name is its own slug.
func TestSlugOr(t *testing.T) {
	if got := SlugOr("Living Room", "00:11:22:aa:bb:cc"); got != "living-room" {
		t.Errorf("SlugOr = %q, want living-room", got)
	}
	if got, want := SlugOr("Кухня", "00:11:22:aa:bb:cc"), Slug(NameFromMAC("00:11:22:aa:bb:cc")); got != want || !strings.HasSuffix(got, "-aabbcc") {
		t.Errorf("SlugOr of an all-Cyrillic name = %q, want %q", got, want)
	}
}
