//go:build !dot

package tailscale

import (
	"context"
	"strings"
	"testing"
)

// A device on Tailscale's control server that is given a Headscale is logged out of Tailscale's first,
// then joins the Headscale like a new device.
func TestLoginMovesToAnotherControlServer(t *testing.T) {
	f := &fakeTailscaled{status: `{"BackendState":"Running","HaveNodeKey":true}`, control: DefaultControl}
	if err := newLocalAPI(f.serve(t)).login(context.Background(), "hskey-auth-test", "kitchen", "https://headscale.example.com"); err != nil {
		t.Fatal(err)
	}
	prefs, _ := f.started["UpdatePrefs"].(map[string]any)
	if !f.loggedOut || prefs["ControlURL"] != "https://headscale.example.com" || !f.loginAsked || f.loginBefore {
		t.Errorf("logged out %v, start %+v, login asked %v (before start %v)", f.loggedOut, f.started, f.loginAsked, f.loginBefore)
	}
}

// Joining again at the same control server keeps the device's identity: no logout, no new login. A
// tailscaled whose settings name no control server is on Tailscale's.
func TestLoginAtTheSameControlServerStaysJoined(t *testing.T) {
	for _, c := range []struct{ was, now string }{
		{"", DefaultControl},
		{DefaultControl, DefaultControl},
		{"https://headscale.example.com", "https://headscale.example.com"},
	} {
		f := &fakeTailscaled{status: `{"BackendState":"Running","HaveNodeKey":true}`, control: c.was}
		if err := newLocalAPI(f.serve(t)).login(context.Background(), "tskey-auth-test", "kitchen", c.now); err != nil {
			t.Fatal(err)
		}
		if f.loggedOut || f.loginAsked {
			t.Errorf("was %q, now %q: logged out %v, login asked %v", c.was, c.now, f.loggedOut, f.loginAsked)
		}
	}
}

func TestControlURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  DefaultControl,
		" https://headscale.example.com/":   "https://headscale.example.com",
		"http://headscale.lan:8080":         "http://headscale.lan:8080",
		"https://HeadScale.Example.com:443": "https://headscale.example.com",
		"http://headscale.lan:80/":          "http://headscale.lan",
		"headscale.example.com":             "",
		"ftp://headscale.example.com":       "",
		"https://me:pw@headscale.example":   "",
	} {
		got, err := controlURL(in)
		if (want == "") != (err != nil) || got != want {
			t.Errorf("controlURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if controlSaved(DefaultControl) != "" || controlHost("https://headscale.example.com") != "headscale.example.com" || controlHost("") != "" {
		t.Error("Tailscale's own server is saved as nothing, and a Headscale's host is shown")
	}
}

// Tailscale's keys for Tailscale's server; for a Headscale its keys old and new, and Tailscale-style ones.
func TestCheckKey(t *testing.T) {
	hs := "https://headscale.example.com"
	for _, c := range []struct {
		key, control string
		ok           bool
	}{
		{"tskey-auth-abc", DefaultControl, true},
		{"hskey-auth-abc-def", DefaultControl, false},
		{"hskey-auth-abc-def", hs, true},
		{strings.Repeat("0123456789abcdef", 3), hs, true},
		{"hunter2", hs, false},
		{strings.Repeat("z", 48), hs, false},
	} {
		if err := checkKey(c.key, c.control); (err == nil) != c.ok {
			t.Errorf("checkKey(%q, %q) = %v, want ok %v", c.key, c.control, err, c.ok)
		}
	}
}

// A device numbered outside Tailscale's ranges would let tailnet nodes in as if from the house.
func TestInRange(t *testing.T) {
	if !inRange([]string{"100.64.0.5", "fd7a:115c:a1e0::5"}) {
		t.Error("Tailscale's own ranges were refused")
	}
	if inRange([]string{"10.20.0.5", "fd7a:115c:a1e0::5"}) || inRange([]string{"100.64.0.5", "fd00::5"}) {
		t.Error("an address outside the ranges was taken")
	}
}

// A Headscale key from before 0.28 says nothing of what it is: a line carrying it is known by the key.
func TestLogDropsTheKeyBeingJoinedWith(t *testing.T) {
	key := strings.Repeat("0123456789abcdef", 3)
	logKey.Store(key)
	t.Cleanup(func() { logKey.Store("") })
	if !hasLogKey("control: using key "+key) || hasLogKey("control: connected") {
		t.Error("the key's lines are not told apart")
	}
}
