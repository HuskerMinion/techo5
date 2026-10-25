package phone

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A call placed with a name shows it with the number, as a caller the network names is shown; a name
// that is empty, the number itself, or nothing printable shows the number alone, and a long one is cut
// to what an intercom caller's is.
func TestCalledPeer(t *testing.T) {
	long := strings.Repeat("x", nameMost+10)
	for _, tc := range []struct{ name, number, want string }{
		{"Alex Mobile", "015112345678", "Alex Mobile (015112345678)"},
		{"  Küche  ", "**612", "Küche (**612)"},
		{"", "**612", "**612"},
		{"**612", "**612", "**612"},
		{"\x00\x07", "106", "106"},
		{long, "106", strings.Repeat("x", nameMost) + " (106)"},
	} {
		if got := calledPeer(tc.name, tc.number); got != tc.want {
			t.Errorf("calledPeer(%q, %q) = %q, want %q", tc.name, tc.number, got, tc.want)
		}
	}
}

// A named call is shown by its name from the moment it takes the line, with the number it dials.
func TestANamedCallShowsItsName(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := Get()
	p.mu.Lock()
	p.line = &line{} // never dialed on: the wait is canceled first
	p.mu.Unlock()
	p.set(func(s *State) { s.Configured, s.Registered = true, true })
	t.Cleanup(func() {
		p.mu.Lock()
		p.line = nil
		p.mu.Unlock()
		p.set(func(s *State) { s.Phase, s.Configured, s.Registered = Idle, false, false })
	})

	waiting := make(chan struct{})
	err := p.CallAfter("+49 151 1234-5678", "Alex Mobile", func(ctx context.Context) bool {
		close(waiting)
		<-ctx.Done()
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	<-waiting
	if st := p.State(); st.Peer != "Alex Mobile (4915112345678)" {
		t.Errorf("peer %q, want the name with the number dialed", st.Peer)
	}
	p.Hangup()
	deadline := time.Now().Add(time.Second)
	for p.State().Phase != Idle && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

// The log names nobody: a contact's or a caller's name comes off, and the number stays.
func TestTheLogKeepsOnlyTheNumber(t *testing.T) {
	for in, want := range map[string]string{
		"Mom (15551234567)":       "15551234567",
		"Dr. Lee (Clinic) (5550)": "5550",
		"15551234567":             "15551234567",
		"":                        "",
		"Unknown":                 "Unknown",
		"Mom":                     "a named caller",
		"*86":                     "*86",
	} {
		if got := loggedPeer(in); got != want {
			t.Errorf("loggedPeer(%q) = %q, want %q", in, got, want)
		}
	}
}
