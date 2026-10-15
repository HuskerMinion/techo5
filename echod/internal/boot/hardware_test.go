package boot

import "testing"

func TestReadableName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Kitchen\n", "Kitchen\n"},
		{"Küche Echo\n", "Küche Echo\n"},
		{"K\xfcche Echo\n", "Küche Echo\n"},
		{"Caf\xe9", "Café"},
		{"\x8aibenik \x9cuvre", "Šibenik œuvre"},
		{"A\x81B\xff", "A\u0081Bÿ"},
	} {
		if got := readableName([]byte(c.in)); got != c.want {
			t.Errorf("readableName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
