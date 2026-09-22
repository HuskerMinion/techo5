package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// ask is a browser asking to be let in: it posts to /setup/wait and keeps the cookie it is given.
func ask(t *testing.T, f *Feature) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("asking to be let in: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("no cookie handed out to the browser that asked")
	return nil
}

func get(f *Feature, path string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w
}

// Nothing is readable until the button on the device has been pressed.
func TestNobodyIsLetInWithoutAPress(t *testing.T) {
	f := build()
	f.Open()

	if body := get(f, "/setup", nil).Body.String(); !strings.Contains(body, "press the") &&
		!strings.Contains(body, "Ask to be let in") {
		t.Errorf("a browser that has not asked is not told to press the button: %q", first(body))
	}

	c := ask(t, f)
	if body := get(f, "/setup", c).Body.String(); !strings.Contains(body, "Press the action button") {
		t.Errorf("a browser waiting for the press is not told to make it: %q", first(body))
	}
	if strings.Contains(get(f, "/setup", c).Body.String(), "Time zone") {
		t.Error("the settings are readable before anyone pressed anything")
	}

	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if body := get(f, "/setup", c).Body.String(); !strings.Contains(body, "Time zone") {
		t.Errorf("the press did not let the browser in: %q", first(body))
	}
}

// A press answers the browser that was already waiting. A second browser has to ask again, so that
// whoever presses knows which one they are letting in.
func TestOnlyOneBrowserWaitsAtATime(t *testing.T) {
	f := build()
	f.Open()
	first := ask(t, f)

	w := httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	if w.Code != http.StatusConflict {
		t.Errorf("a second browser asking got %d, want a refusal while another is waiting", w.Code)
	}

	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !f.letIn(first.Value) {
		t.Error("the press did not let in the browser that was waiting")
	}
}

// A press with nobody waiting lets nobody in, and cannot be saved up for the next browser to ask.
func TestAPressIsNotSavedUp(t *testing.T) {
	f := build()
	f.Open()
	if f.press() {
		t.Error("a press with nobody waiting let somebody in")
	}
	c := ask(t, f)
	if f.letIn(c.Value) {
		t.Error("the browser that asked afterwards was let in by a press made before it asked")
	}
}

// Closing the page forgets every session: coming back means pressing again.
func TestClosingForgetsWhoWasLetIn(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !f.letIn(c.Value) {
		t.Fatal("not let in to begin with")
	}
	f.Close()
	if f.letIn(c.Value) {
		t.Error("a session outlived the page being closed")
	}
	if f.On() {
		t.Error("still on after being closed")
	}
}

// The page closes itself when it is left alone, and asking for something keeps it open.
func TestItClosesWhenLeftAloneAndTypingKeepsItOpen(t *testing.T) {
	f := build()
	f.Open()

	f.mu.Lock()
	f.openUntil = time.Now().Add(-time.Second) // as if nothing had been asked of it for the idle time
	f.mu.Unlock()
	if f.On() {
		t.Error("still open after being left alone")
	}

	f.Open()
	f.mu.Lock()
	f.openUntil = time.Now().Add(time.Second)
	f.mu.Unlock()
	f.used()
	f.mu.Lock()
	left := time.Until(f.openUntil)
	f.mu.Unlock()
	if left < idle-time.Minute {
		t.Errorf("using the page left %v on it, want the idle time back at the start", left.Round(time.Second))
	}
}

// A save has to carry the token from the page it came from, so another site cannot post here with a
// browser's cookie.
func TestASaveNeedsThePagesOwnToken(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	post := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		f.serve(w, r)
		return w.Code
	}
	if code := post("what=timezone&zone=UTC"); code != http.StatusForbidden {
		t.Errorf("a save with no token got %d, want it refused", code)
	}
	if code := post("token=wrong&what=timezone&zone=UTC"); code != http.StatusForbidden {
		t.Errorf("a save with somebody else's token got %d, want it refused", code)
	}
}

// Holding the action button opens the page, which is the only way in on a device with no screen.
func TestHoldingTheActionButtonOpensIt(t *testing.T) {
	f := build()
	if f.On() {
		t.Fatal("open before anyone asked")
	}
	f.button(buttons.Event{Name: buttons.Mute, Kind: buttons.Hold})
	if f.On() {
		t.Error("another button opened it")
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Hold})
	if !f.On() {
		t.Error("holding the action button did not open it")
	}
}

// On the Dot the same press goes on to be Bluetooth pairing, and somebody reaching for pairing has
// not asked for a page on the network.
func TestAPressThatBecomesSomethingElseTakesThePageBack(t *testing.T) {
	f := build()
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Hold})
	if !f.On() {
		t.Fatal("the hold did not open it")
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.LongHold})
	if f.On() {
		t.Error("the page stayed open after the press turned out to be the longer one")
	}

	// A page opened any other way is not somebody's press to take back.
	f.Open()
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.LongHold})
	if !f.On() {
		t.Error("a long hold closed a page it did not open")
	}
}

// The press that lets a browser in is the page's and nobody else's. On a Dot it was also handled as
// an ordinary tap, which started a voice turn nobody asked for and sent fifteen seconds of the room
// to Home Assistant because somebody opened a settings page. What the conversation asks before it
// treats a tap as a question is this, and it has to answer the same whichever listener runs first:
// the button's listeners are in no fixed order, so the asking may come either side of the press
// being taken.
func TestThePressThatLetsABrowserInIsNotAlsoAQuestion(t *testing.T) {
	f := build()
	f.Open()
	if f.TookPress() {
		t.Error("the page claimed a press with no browser waiting: the button would stop talking")
	}

	ask(t, f)
	if !f.TookPress() {
		t.Error("a press with a browser waiting was not the page's")
	}

	// The press itself, as the conversation's listener would see it if it ran second.
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !f.TookPress() {
		t.Error("the press was not the page's once the page had already taken it")
	}

	// And once the moment has passed the button is the assistant's again.
	f.mu.Lock()
	f.took = time.Now().Add(-pressGrace - time.Millisecond)
	f.mu.Unlock()
	if f.TookPress() {
		t.Error("the page went on claiming presses after the one it was answered by")
	}
}

// A page being used is a page somebody meant to open, however the press ended.
func TestUsingThePageKeepsItThroughTheLongerHold(t *testing.T) {
	f := build()
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Hold})
	f.used()
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.LongHold})
	if !f.On() {
		t.Error("a page somebody was using was taken down")
	}
}

func first(s string) string {
	if i := strings.Index(s, "<h1>"); i >= 0 {
		s = s[i:]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// Asking over and over with nobody pressing anything stops being allowed for a while.
func TestAskingForeverIsRefusedForAWhile(t *testing.T) {
	f := build()
	f.Open()
	for i := 0; i < tries; i++ {
		f.mu.Lock()
		f.waitingEnd = time.Now().Add(-time.Second) // as if that one had run out unanswered
		f.mu.Unlock()
		f.await()
	}
	if !f.ShutOut() {
		t.Fatal("still letting a browser ask after five tries with no press")
	}
	if _, ok := f.await(); ok {
		t.Error("a browser was allowed to ask while shut out")
	}

	w := httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("asking while shut out got %d, want too many requests", w.Code)
	}
}

// A press clears the run, so somebody who mistypes their way through a few attempts and then presses
// is not locked out afterwards.
func TestAPressClearsTheRun(t *testing.T) {
	f := build()
	f.Open()
	for i := 0; i < tries-2; i++ {
		f.mu.Lock()
		f.waitingEnd = time.Now().Add(-time.Second)
		f.mu.Unlock()
		f.await()
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	f.mu.Lock()
	run := f.unanswered
	f.mu.Unlock()
	if run != 0 {
		t.Errorf("%d tries still counted against the browser after a press", run)
	}
}

// Renaming is refused until the box saying what it does to Home Assistant has been ticked, and a
// name that is no good is refused whatever the box says.
func TestRenamingNeedsTheBoxTicked(t *testing.T) {
	if got := rename("Study", false); got == "" {
		t.Error("renamed without the box ticked")
	} else if !strings.Contains(got, "tick the box") {
		t.Errorf("refused with %q, want it to say the box has to be ticked", got)
	}
	for _, bad := range []string{"", "   ", "a name that is very much too long to fit on the screen"} {
		if got := rename(bad, true); got == "" {
			t.Errorf("accepted %q as a name", bad)
		}
	}
	if got := rename("Two\nLines", true); got == "" {
		t.Error("accepted a name with a newline in it")
	}
}

// The diagnostics are behind the press like everything else: a browser that has not been let in
// cannot have the device's log by asking for it.
func TestDiagnosticsNeedThePressToo(t *testing.T) {
	f := build()
	f.Open()
	if code := get(f, "/setup/diagnostics.txt", nil).Code; code != http.StatusForbidden {
		t.Errorf("diagnostics with no session got %d, want them refused", code)
	}
	c := ask(t, f)
	if code := get(f, "/setup/diagnostics.txt", c).Code; code != http.StatusForbidden {
		t.Errorf("diagnostics while only waiting got %d, want them refused", code)
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	w := get(f, "/setup/diagnostics.txt", c)
	if w.Code != http.StatusOK {
		t.Fatalf("diagnostics after the press got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "TECHO5 diagnostics") {
		t.Errorf("that does not look like the bundle: %q", first(w.Body.String()))
	}
}

// The idle time is there to close the page when whoever opened it has walked away. Only a browser
// that has been let in counts as somebody being there: anything else on the network could otherwise
// hold the page open by asking for it, which is the opposite of what the timer is for.
func TestOnlyALetInBrowserKeepsThePageOpen(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	// Wind the page back to a minute from closing, and see who can put it off.
	soon := time.Now().Add(time.Minute)
	f.mu.Lock()
	f.openUntil = soon
	f.mu.Unlock()

	for _, path := range []string{"/setup", "/setup/state", "/setup/diagnostics.txt"} {
		get(f, path, nil)
	}
	f.mu.Lock()
	held := f.openUntil
	f.mu.Unlock()
	if !held.Equal(soon) {
		t.Errorf("a request with no session put the closing time off to %v", held.Sub(soon))
	}

	get(f, "/setup", c)
	f.mu.Lock()
	held = f.openUntil
	f.mu.Unlock()
	if !held.After(soon) {
		t.Error("the browser that was let in did not keep the page open")
	}
}

// The cookie has to reach the whole port, not only this page: the screenshot page asks the same
// question about the options that work the screen.
func TestTheSessionCookieCoversThePort(t *testing.T) {
	f := build()
	f.Open()
	if c := ask(t, f); c.Path != "/" {
		t.Errorf("the session cookie is scoped to %q, want the whole port", c.Path)
	}
}

// The "other network" field takes whatever somebody types, and it becomes a quoted value in
// wpa_supplicant.conf. A newline in it would end that line and turn the rest into configuration of
// its own, so it is refused before anything is written.
func TestANetworkNameCannotCarryALineEnding(t *testing.T) {
	dir := t.TempDir()
	old := wifi.Conf
	wifi.Conf = dir + "/wpa_supplicant.conf"
	t.Cleanup(func() { wifi.Conf = old })

	injected := "Home\nnetwork={\n\tssid=\"Theirs\"\n\tkey_mgmt=NONE\n}"

	problem := joinWifi(context.Background(), injected, "hunter2hunter")
	if problem == "" {
		t.Fatal("a network name carrying a line ending was accepted")
	}
	if _, err := os.Stat(wifi.Conf); !os.IsNotExist(err) {
		t.Errorf("the configuration was written for a name that was refused: %v", err)
	}
}
