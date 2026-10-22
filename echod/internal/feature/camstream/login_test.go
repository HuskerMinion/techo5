package camstream

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A camera login is held to the setup page's rules wherever it comes from.
func TestCheckLogin(t *testing.T) {
	for _, c := range []struct {
		user, pass string
		ok         bool
	}{
		{"camera", "longenough12", true},
		{"camera", "", true}, // keeps the password set
		{"camera", "short", false},
		{"cam:era", "longenough12", false},
		{"camera", "long\"enough12", false},
		{"camera", "long\nenough12", false},
	} {
		if err := CheckLogin(c.user, c.pass); (err == nil) != c.ok {
			t.Errorf("CheckLogin(%q, %q) = %v, want ok %v", c.user, c.pass, err, c.ok)
		}
	}
}

// Home Assistant cannot set the login until the device's own switch lets it, which starts off.
func TestCredentialsNeedTheSwitch(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if config.Get().CamStream.LoginFromHA {
		t.Fatal("login from Home Assistant is on in a fresh configuration")
	}
	err := setCredentials("viewer", "longenough12")
	if err == nil || !strings.Contains(err.Error(), "Camera login from Home Assistant") {
		t.Fatalf("with the switch off: %v", err)
	}
	if _, pass := config.Get().CamStream.Login(); pass != "" {
		t.Fatal("a refused login was saved")
	}
}
