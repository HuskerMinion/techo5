package setup

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// remoteTest is a page with the link keyed, this house on 192.0.2.0/24 with Tailscale up, and the
// notices of a browser let in kept.
func remoteTest(t *testing.T) (*Feature, *[]string) {
	t.Helper()
	wasKeyed, wasNotice, wasNets := keyed, letInNotice, web.LocalNets
	t.Cleanup(func() { keyed, letInNotice, web.LocalNets = wasKeyed, wasNotice, wasNets })
	keyed = func() bool { return true }
	web.LocalNets = func() (bool, []netip.Prefix) {
		return true, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	}
	var notices []string
	letInNotice = func(from string) { notices = append(notices, from) }
	return build(), &notices
}

// askFrom is ask, from an address.
func askFrom(t *testing.T, f *Feature, from string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/setup/wait", nil)
	r.RemoteAddr = from
	w := httptest.NewRecorder()
	f.serve(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatalf("asking from %s: %d %s", from, w.Code, strings.TrimSpace(w.Body.String()))
	return nil
}

func letIn(f *Feature, c *http.Cookie) bool {
	return strings.Contains(get(f, "/setup", c).Body.String(), `class="rail"`)
}

// Without a key of its own on the link, anybody on the network could turn Remote setup on: it stays off,
// and the page stays closed.
func TestRemoteSetupNeedsAKeyedLink(t *testing.T) {
	f, _ := remoteTest(t)
	keyed = func() bool { return false }
	f.setRemote(true)
	if f.Remote() || f.On() {
		t.Fatalf("remote setup on an unkeyed link: remote %v, page open %v", f.Remote(), f.On())
	}
}

// Home Assistant lets in the browser asking over the tailnet, only while Remote setup is on, as a press
// would; the sensor and the screen say where it asked from.
func TestHomeAssistantLetsInABrowserFromAnotherHome(t *testing.T) {
	f, notices := remoteTest(t)
	f.Open()
	c := askFrom(t, f, "100.64.0.2:5000")
	if f.letInFromHA() || letIn(f, c) {
		t.Fatal("let in from Home Assistant with Remote setup off")
	}

	f.setRemote(true) // the browser is still asking
	if got := f.remote.asking.Get(); got != "100.64.0.2" {
		t.Errorf("Setup page asking = %q, want the address asking", got)
	}
	if !f.letInFromHA() || !letIn(f, c) {
		t.Fatal("Home Assistant did not let the browser from away in")
	}
	if len(*notices) != 1 || (*notices)[0] != "100.64.0.2" {
		t.Errorf("the screen said %v, want once, with the address", *notices)
	}
	if got := f.remote.asking.Get(); got != "" {
		t.Errorf("Setup page asking = %q once nobody is asking", got)
	}
	// Nobody asking: nothing to let in, and nothing saved up for the next browser to ask.
	if f.letInFromHA() {
		t.Error("let in with nobody asking")
	}
	if c2 := askFrom(t, f, "100.64.0.3:5000"); letIn(f, c2) {
		t.Error("a browser that asked after the press is in")
	}
}

// A browser in the device's own home is let in at the device, never from Home Assistant.
func TestHomeAssistantDoesNotLetInABrowserInTheDevicesHome(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	c := askFrom(t, f, "192.0.2.20:5000")
	if got := f.remote.asking.Get(); !strings.Contains(got, "in the device's home") {
		t.Errorf("Setup page asking = %q, want it to say the browser is in the device's home", got)
	}
	if f.letInFromHA() || letIn(f, c) {
		t.Fatal("Home Assistant let in a browser in the device's own home")
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !letIn(f, c) {
		t.Fatal("the press on the device did not let it in")
	}
}

// Turning Remote setup off lets go of the browsers it let in over the tailnet, and only those.
func TestRemoteSetupOffLetsGoOfItsBrowsers(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	here := askFrom(t, f, "192.0.2.20:5000")
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	away := askFrom(t, f, "100.64.0.2:5000")
	f.letInFromHA()
	if !letIn(f, here) || !letIn(f, away) {
		t.Fatal("both browsers should be in")
	}
	f.setRemote(false)
	if letIn(f, away) {
		t.Error("the browser from away is still in with Remote setup off")
	}
	if !letIn(f, here) {
		t.Error("the browser in the device's home was let go too")
	}
}

// Turning Remote setup on while it is already on (a second turn_on from Home Assistant) keeps the
// browsers it let in as its own, so turning it off still lets go of them.
func TestRemoteSetupOnTwiceKeepsItsBrowsers(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	away := askFrom(t, f, "100.64.0.2:5000")
	if !f.letInFromHA() || !letIn(f, away) {
		t.Fatal("the browser from away should be in")
	}
	f.setRemote(true)
	if !f.Remote() || !letIn(f, away) {
		t.Fatal("a second on turned Remote setup off, or let go of the browser")
	}
	f.setRemote(false)
	if letIn(f, away) {
		t.Error("the browser let in before the second on outlived Remote setup")
	}
}

// A key taken off the link while Remote setup is on ends it at the next let in.
func TestALinkThatLostItsKeyEndsRemoteSetup(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	c := askFrom(t, f, "100.64.0.2:5000")
	keyed = func() bool { return false }
	if f.letInFromHA() || letIn(f, c) || f.Remote() {
		t.Fatal("let in, or still on, with the link's key gone")
	}
}

// Remote setup holds for the opening of the page it was turned on in, however that ends, and opening the
// page again does not bring it back.
func TestRemoteSetupGoesWithThePage(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	f.Close()
	f.Open()
	if f.Remote() {
		t.Error("Remote setup came back with the page")
	}

	f.setRemote(true)
	f.mu.Lock()
	f.openUntil = time.Now().Add(-time.Second) // left alone for the idle time
	f.mu.Unlock()
	if f.Remote() {
		t.Error("Remote setup outlived the page closing for want of use")
	}
	f.Open()
	if f.Remote() {
		t.Error("Remote setup came back when the page was opened again")
	}

	// Closed for want of use and opened again at the device, with nothing asking in between.
	f.setRemote(true)
	f.mu.Lock()
	f.openUntil = time.Now().Add(-time.Second)
	f.mu.Unlock()
	f.Open()
	if f.Remote() {
		t.Error("Remote setup carried over to a page opened again at the device")
	}

	// Another opening, whichever way it came about, is not the one it was turned on in.
	f.setRemote(true)
	f.mu.Lock()
	f.openedAt = f.openedAt.Add(time.Second)
	f.mu.Unlock()
	if f.Remote() {
		t.Error("Remote setup held for an opening it was not turned on in")
	}

	f.setRemote(true)
	f.setRemote(false)
	if f.Remote() || !f.On() {
		t.Errorf("turned off: remote %v, page open %v (want the page left as it was)", f.Remote(), f.On())
	}
}

// Remote setup going off on the way to a let in from Home Assistant lets nobody in.
func TestALetInFromHomeAssistantNeedsRemoteSetupToTheEnd(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	c := askFrom(t, f, "100.64.0.2:5000")
	f.remote.on.Store(false) // as a switch-off between letInFromHA's look and admit
	if f.admit(f.Asking(), byHA) || letIn(f, c) {
		t.Fatal("let in from Home Assistant with Remote setup gone off")
	}
}

// Where a browser asked from is noted with the asking itself, so a press landing at once still knows it
// for one from away, to let go of with Remote setup.
func TestABrowserLetInAtOnceIsStillRemoteSetups(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	c := askFrom(t, f, "100.64.0.2:5000")
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	f.setRemote(false)
	if letIn(f, c) {
		t.Fatal("a browser from away let in by a press outlived Remote setup")
	}
}

// Turning Remote setup off drops a browser still asking over the tailnet, which nobody can let in any
// more, and the sensor and the screen stop saying it asks; one asking in the device's home stays.
func TestRemoteSetupOffDropsTheBrowserAskingFromAnotherHome(t *testing.T) {
	f, _ := remoteTest(t)
	f.setRemote(true)
	askFrom(t, f, "100.64.0.2:5000")
	if f.Asking() == "" || f.remote.asking.Get() != "100.64.0.2" {
		t.Fatalf("asking %q, sensor %q: want the browser from away", f.Asking(), f.remote.asking.Get())
	}
	changed := 0
	stop := f.Changed.Listen(func(struct{}) { changed++ })
	f.setRemote(false)
	stop()
	if f.Asking() != "" || f.remote.asking.Get() != "" {
		t.Errorf("asking %q, sensor %q after Remote setup off: want none", f.Asking(), f.remote.asking.Get())
	}
	if changed == 0 {
		t.Error("the screen was not told the browser stopped asking")
	}

	f.setRemote(true)
	askFrom(t, f, "192.0.2.20:5000")
	f.setRemote(false)
	if f.Asking() == "" {
		t.Error("a browser asking in the device's home was dropped with Remote setup")
	}
}
