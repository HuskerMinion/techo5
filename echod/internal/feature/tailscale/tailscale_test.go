//go:build !dot

package tailscale

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// fakeTailscaled answers the local API on a unix socket the way tailscaled does, keeping what it was sent.
type fakeTailscaled struct {
	started     map[string]any
	status      string
	loginAsked  bool
	loginBefore bool // whether login-interactive came before start, which it must not
}

func (f *fakeTailscaled) serve(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "ts.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "local-tailscaled.sock" {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/localapi/v0/status":
			_, _ = w.Write([]byte(f.status))
		case "/localapi/v0/start":
			_ = json.NewDecoder(r.Body).Decode(&f.started)
			w.WriteHeader(http.StatusNoContent)
		case "/localapi/v0/login-interactive":
			f.loginAsked, f.loginBefore = true, f.started == nil
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// The status is read as tailscaled writes it: the device's own name and its IPv4 address there, and
// nothing of the other nodes on the tailnet.
func TestTheStatusIsRead(t *testing.T) {
	f := &fakeTailscaled{status: `{"BackendState":"Running",
		"Self":{"HostName":"guest-bedroom","DNSName":"guest-bedroom.example-tail.ts.net.","TailscaleIPs":["100.64.0.5","fd7a:115c::5"],"Online":true},
		"Peer":{"b":{"HostName":"laptop","DNSName":"laptop.example-tail.ts.net.","TailscaleIPs":["100.64.0.2"],"Online":true}}}`}
	api := newLocalAPI(f.serve(t))
	st, err := api.status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.BackendState != "Running" || firstIPv4(st.Self.TailscaleIPs) != "100.64.0.5" || st.Self.DNSName != "guest-bedroom.example-tail.ts.net." {
		t.Fatalf("status %+v", st)
	}
}

// Joining sends the key and a device that only joins: its own name, running, and no DNS or routes
// taken from the tailnet. A device new to the tailnet is then told to log in, after the key is given.
func TestLoginAsksOnlyForWhatItNeeds(t *testing.T) {
	f := &fakeTailscaled{status: `{"BackendState":"NeedsLogin"}`}
	api := newLocalAPI(f.serve(t))
	if err := api.login(context.Background(), "tskey-auth-test", "guest-bedroom"); err != nil {
		t.Fatal(err)
	}
	prefs, _ := f.started["UpdatePrefs"].(map[string]any)
	if f.started["AuthKey"] != "tskey-auth-test" || prefs["Hostname"] != "guest-bedroom" ||
		prefs["WantRunning"] != true || prefs["CorpDNS"] != false || prefs["RouteAll"] != false {
		t.Errorf("start sent %+v", f.started)
	}
	if !f.loginAsked || f.loginBefore {
		t.Errorf("a new device was not told to log in after start (asked %v, before start %v)", f.loginAsked, f.loginBefore)
	}
}

// A device that has joined before only gets the new key and settings, as "tailscale up" does.
func TestLoginAgainDoesNotStartOver(t *testing.T) {
	f := &fakeTailscaled{status: `{"BackendState":"Running","HaveNodeKey":true}`}
	if err := newLocalAPI(f.serve(t)).login(context.Background(), "tskey-auth-test", "kitchen"); err != nil {
		t.Fatal(err)
	}
	if f.started == nil || f.loginAsked {
		t.Errorf("start %v, login asked %v", f.started != nil, f.loginAsked)
	}
}

// A key that is not a key is refused before anything is started.
func TestLoginRefusesWhatIsNotAKey(t *testing.T) {
	if err := Get().Login(context.Background(), "hunter2"); err == nil || !strings.Contains(err.Error(), "not an auth key") {
		t.Errorf("a non-key was taken: %v", err)
	}
}

// A device's name becomes its tailnet name: lower case, hyphens between words, an apostrophe dropped.
func TestHostnames(t *testing.T) {
	for in, want := range map[string]string{
		"Guest Bedroom": "guest-bedroom",
		"Kid's Room":    "kids-room",
		"  Kitchen 2 ":  "kitchen-2",
		"!!!":           "techo5",
	} {
		if got := hostname(in); got != want {
			t.Errorf("hostname(%q) = %q, want %q", in, got, want)
		}
	}
}

// The sensor says what matters: off, waiting for a key, or connected under which name.
func TestStatusText(t *testing.T) {
	for _, c := range []struct {
		s    Status
		want string
	}{
		{Status{}, "Off"},
		{Status{On: true, State: "NoState"}, "Starting"},
		{Status{On: true, State: "NeedsLogin", Problem: "needs a key"}, "Needs a key"},
		{Status{On: true, State: "Running", Name: "guest-bedroom", IP: "100.64.0.5"}, "Connected as guest-bedroom (100.64.0.5)"},
		{Status{On: true, Problem: "tailscale is not in this image"}, "Trouble: tailscale is not in this image"},
	} {
		if got := statusText(c.s); got != c.want {
			t.Errorf("statusText(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

// An image without Tailscale (the Panel's) offers no switch for it; one with it, the switch and its status.
func TestEntitiesOnlyWhereTheImageCarriesIt(t *testing.T) {
	was := Available
	t.Cleanup(func() { Available = was })
	Available = func() bool { return false }
	if n := len(Get().Entities()); n != 0 {
		t.Errorf("%d entities with no Tailscale in the image", n)
	}
	Available = func() bool { return true }
	if n := len(Get().Entities()); n != 2 {
		t.Errorf("%d entities, want the switch and the status", n)
	}
}

// Joining or turning it on needs Home Assistant's link to have its own key: none, and nothing changes.
func TestOnNeedsAnEncryptedLink(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was, wasAvailable := encrypted, Available
	t.Cleanup(func() { encrypted, Available = was, wasAvailable })
	encrypted = func() bool { return false }
	Available = func() bool { return true }
	if err := Get().SetOn(true); err == nil || !strings.Contains(err.Error(), "encryption key") || config.Get().Tailscale.On {
		t.Errorf("turned on with no key on the link: %v", err)
	}
	if err := Get().Login(context.Background(), "tskey-auth-test"); err == nil || !strings.Contains(err.Error(), "encryption key") {
		t.Errorf("joined with no key on the link: %v", err)
	}
	if err := Get().SetOn(false); err != nil {
		t.Errorf("turning it off needs no key: %v", err)
	}
}
