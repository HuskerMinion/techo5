package phone

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Phone without encryption is kept, and signs the account in again at once, the new way.
func TestPlainSIPIsKeptAndSignsInAgain(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := Get()
	drain := func() bool {
		select {
		case <-p.reload:
			return true
		default:
			return false
		}
	}
	drain()

	p.SetPlainSIP(true)
	if !config.Get().Home.PhonePlain {
		t.Error("Phone without encryption was not kept")
	}
	if !drain() {
		t.Error("turning it on did not sign the account in again")
	}
	p.SetPlainSIP(false)
	if config.Get().Home.PhonePlain {
		t.Error("Phone without encryption was not turned off")
	}
	if !drain() {
		t.Error("turning it off did not sign the account in again")
	}
}

// Without encryption the line is plain UDP to port 5060, as a FRITZ!Box's registrar takes it, and its
// addresses carry no transport; with it, TLS to 5061.
func TestPlainLineAddresses(t *testing.T) {
	for _, tc := range []struct {
		l    line
		want string
	}{
		{line{acct: Account{Server: "fritz.box"}, tran: "udp", port: 5060}, "sip:**620@fritz.box:5060"},
		{line{acct: Account{Server: "toronto1.voip.ms"}, tran: "tls", port: 5061}, "sip:**620@toronto1.voip.ms:5061;transport=tls"},
	} {
		u, err := tc.l.uri("**620")
		if err != nil {
			t.Fatal(err)
		}
		if got := u.String(); got != tc.want {
			t.Errorf("%s line: %s, want %s", tc.l.tran, got, tc.want)
		}
	}
}
