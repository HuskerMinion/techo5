package setup

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/api"
)

// The three things on the page that cost the most to do by mistake are asked for twice: a box to tick
// on the page, and the same box looked for when the form arrives, so a form sent without it is refused
// and nothing is changed.

// Forgetting the network the device is on takes it off the network, and the page with it.
func TestForgettingTheNetworkInUseNeedsTheBox(t *testing.T) {
	f, c := in(t)
	was := onNetwork
	t.Cleanup(func() { onNetwork = was })
	onNetwork = func(context.Context) string { return "Home network" }

	to := post(t, f, c, url.Values{"what": {"forget"}, "ssid": {"Home network"}})
	if p := to.Query().Get("problem"); !strings.Contains(p, "tick the box") || !strings.Contains(p, "Home network") {
		t.Errorf("forgetting the network in use without the box: %q", p)
	}
	if to.Query().Get("at") != "wifi" {
		t.Errorf("the refusal came back to %s, not to the Wi-Fi section", to)
	}
	// With the box it is let through to the forgetting, and so is a network the device is not on
	// without it. Neither is refused for the box, whatever a test machine with no Wi-Fi says next.
	for _, v := range []url.Values{
		{"what": {"forget"}, "ssid": {"Home network"}, "understood": {"yes"}},
		{"what": {"forget"}, "ssid": {"Somewhere else"}},
	} {
		if p := post(t, f, c, v).Query().Get("problem"); strings.Contains(p, "tick the box") {
			t.Errorf("%v was refused for the box: %q", v, p)
		}
	}
}

// Letting a Home Assistant add the device drops the key the one that has it now uses.
func TestOpeningToAHomeAssistantNeedsTheBox(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"adopt"}})
	if p := to.Query().Get("problem"); !strings.Contains(p, "tick the box") {
		t.Errorf("opening the device without the box: %q", p)
	}
	if !api.Get().AdoptionOpenUntil().IsZero() {
		t.Error("the device was opened to a Home Assistant without the box")
	}
	body := get(f, "/setup?tab=general", c).Body.String()
	if !strings.Contains(body, `name="understood"`) ||
		!strings.Contains(body, `class="danger">Let a Home Assistant add this device`) {
		t.Error("the page does not offer the box, or the button does not look like what it does")
	}
}

// Turning the settings lock off leaves the settings open to anyone at the screen.
func TestTurningTheLockOffNeedsTheBox(t *testing.T) {
	f, c := in(t)
	if p := post(t, f, c, url.Values{"what": {"lock"}, "do": {"set"}, "pin": {"2468"}}).Query().Get("problem"); p != "" {
		t.Fatalf("setting a PIN: %s", p)
	}
	to := post(t, f, c, url.Values{"what": {"lock"}, "do": {"clear"}})
	if p := to.Query().Get("problem"); !strings.Contains(p, "tick the box") {
		t.Errorf("turning the lock off without the box: %q", p)
	}
	if config.Get().Security.LockPIN == "" {
		t.Fatal("the lock was turned off without the box")
	}
	if hasScreen {
		body := get(f, "/setup?tab=privacy", c).Body.String()
		if !strings.Contains(body, `class="danger">Turn the lock off`) || !strings.Contains(body, `name="understood"`) {
			t.Error("the page does not offer the box with Turn the lock off")
		}
	}
	if p := post(t, f, c, url.Values{"what": {"lock"}, "do": {"clear"}, "understood": {"yes"}}).Query().Get("problem"); p != "" {
		t.Fatalf("turning the lock off with the box: %s", p)
	}
	if config.Get().Security.LockPIN != "" {
		t.Error("the lock is still on after it was turned off with the box")
	}
}

// headCSS is the page's head as it is sent, for the tests that look for a style rule.
func headCSS() string {
	w := httptest.NewRecorder()
	head(w)
	return w.Body.String()
}

// What cannot be undone does not look like Save.
func TestWhatCannotBeUndoneLooksIt(t *testing.T) {
	if !strings.Contains(headCSS(), `button.danger{background:transparent;color:var(--bad);border:1px solid var(--bad)}`) {
		t.Error("the page has no style for a button that removes something")
	}
}
