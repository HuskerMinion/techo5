package detect

import "testing"

// The environment decides the front end either way, and the board's own stands where it says nothing.
func TestTheEnvironmentPicksTheFrontEndOverTheBoard(t *testing.T) {
	for _, c := range []struct {
		env       string
		byDefault bool
		want      bool
	}{
		{"", false, false},
		{"", true, true},
		{"exact", false, true},
		{"approx", true, false},
		{"something else", true, true},
		{"something else", false, false},
	} {
		if got := exactFrontEnd(c.env, c.byDefault); got != c.want {
			t.Errorf("env %q, board default %v: exact = %v, want %v", c.env, c.byDefault, got, c.want)
		}
	}
}
