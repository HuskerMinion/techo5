package security

import (
	"strings"
	"testing"
)

// A camera login is what the rtsp://user:password@ form and the digests can carry: no spaces, colons,
// @, / or quotes, and a password of 12 or more.
func TestCheckLogin(t *testing.T) {
	for _, c := range []struct {
		user, pass string
		ok         bool
	}{
		{"camera", "longenough12", true},
		{"camera", "short", false},
		{"", "longenough12", false},
		{"cam era", "longenough12", false},
		{"camera", "long:enough12", false},
		{"camera", "long@enough12", false},
		{"camera", "long/enough12", false},
		{"camera", `long"enough12`, false},
		{"camera", strings.Repeat("x", 11), false},
	} {
		if err := CheckLogin(c.user, c.pass); (err == nil) != c.ok {
			t.Errorf("CheckLogin(%q, %q) = %v, want ok %v", c.user, c.pass, err, c.ok)
		}
	}
}
