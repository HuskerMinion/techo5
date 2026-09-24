package phone

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/lib/sealed"
)

// house is a device answering intercom calls on a test port, with the house word set.
func house(t *testing.T) (*Phone, announce.Peer) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().HouseWord("bluebird"); err != nil {
		t.Fatal(err)
	}
	p := Get()
	p.Hangup()
	p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	p.mu.Lock()
	p.declined = nil
	p.mu.Unlock()
	// A call's handler outlives its request (the connection is taken over), so the test waits for
	// each to finish: the next test swaps the config out from under it otherwise.
	var calls sync.WaitGroup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		defer calls.Done()
		p.intercomIn(w, r)
	}))
	t.Cleanup(func() {
		srv.Close()
		calls.Wait()
	})
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	return p, announce.Peer{Name: "Kitchen", Address: host, Port: n}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited for %s", what)
}

// A caller with the wrong house word never gets as far as ringing.
func TestIntercomRefusesTheWrongWord(t *testing.T) {
	p, peer := house(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := dialDevice(ctx, peer, sealed.Key(intercomLabel, "not this house")); err == nil {
		t.Fatal("a call with the wrong word got through")
	}
	if p.State().Phase != Idle {
		t.Fatalf("the device is %v after a refused call", p.State().Phase)
	}
}

// A call rings with the caller's name, and declining it tells the caller.
func TestIntercomRingsAndCanBeDeclined(t *testing.T) {
	p, peer := house(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	caller := shownName([]byte(config.Get().Device.Name))
	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	if got := <-l.control; got != msgRinging {
		t.Fatalf("first answer %q, not ringing", got)
	}
	waitFor(t, "ringing", func() bool { st := p.State(); return st.Phase == Ringing && st.Intercom && st.Peer == caller })
	p.Hangup()
	select {
	case got := <-l.control:
		if got != msgDecline {
			t.Fatalf("declining sent %q", got)
		}
	case <-ctx.Done():
		t.Fatal("the caller never heard it was declined")
	}
	waitFor(t, "idle", func() bool { st := p.State(); return st.Phase == Idle && !st.Intercom })
}

// A device already on a call says so, and the call it is on carries on.
func TestIntercomIsBusyOnACall(t *testing.T) {
	p, peer := house(t)
	p.set(func(s *State) { s.Phase, s.Peer = Talking, "Someone" })
	defer p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	if got := <-l.control; got != msgBusy {
		t.Fatalf("a busy device answered %q", got)
	}
	if st := p.State(); st.Phase != Talking || st.Peer != "Someone" {
		t.Fatalf("the call in progress became %v with %q", st.Phase, st.Peer)
	}
}

// What a caller calls itself is shown as words, never as control characters or a page of text.
func TestShownName(t *testing.T) {
	if got := shownName([]byte("Kitchen\x1b[2J")); got != "Kitchen[2J" {
		t.Errorf("got %q", got)
	}
	if got := shownName(nil); got != "Another room" {
		t.Errorf("empty name shown as %q", got)
	}
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	if got := shownName(long); len(got) != nameMost {
		t.Errorf("long name kept %d characters", len(got))
	}
}

// firstAnswer is what a device says first to a call.
func firstAnswer(t *testing.T, peer announce.Peer) (byte, *link) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.close)
	select {
	case got := <-l.control:
		return got, l
	case <-ctx.Done():
		t.Fatal("no answer at all")
	}
	return 0, nil
}

// Do not disturb turns a call away before anything rings, and says why.
func TestIntercomDoNotDisturb(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DoNotDisturb(true); err != nil {
		t.Fatal(err)
	}
	if got, _ := firstAnswer(t, peer); got != msgNotNow {
		t.Fatalf("with do not disturb on, a call got %q", got)
	}
	if p.State().Phase != Idle {
		t.Fatalf("the device is %v", p.State().Phase)
	}
}

// A device whose call was just declined cannot ring again straight away.
func TestIntercomDeclinedCallerWaits(t *testing.T) {
	p, peer := house(t)
	got, _ := firstAnswer(t, peer)
	if got != msgRinging {
		t.Fatalf("first call got %q", got)
	}
	waitFor(t, "ringing", func() bool { return p.State().Phase == Ringing })
	p.Hangup()
	waitFor(t, "idle", func() bool { return p.State().Phase == Idle })
	if got, _ := firstAnswer(t, peer); got != msgDecline {
		t.Fatalf("calling again at once got %q", got)
	}
	p.mu.Lock()
	for k := range p.declined {
		p.declined[k] = time.Now().Add(-declineHold)
	}
	p.mu.Unlock()
	if got, _ := firstAnswer(t, peer); got != msgRinging {
		t.Fatalf("calling again later got %q", got)
	}
	p.Hangup()
}

// With Drop In allowed, a call is answered by itself after its chime, and says it was a drop in.
func TestIntercomDropInAnswersItself(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })
	got, l := firstAnswer(t, peer)
	if got != msgRinging {
		t.Fatalf("first answer %q", got)
	}
	waitFor(t, "a drop in", func() bool { return p.State().DropIn })
	select {
	case got := <-l.control:
		if got != msgAnswer {
			t.Fatalf("then %q, not answered", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a drop in was never answered")
	}
	p.Hangup()
	waitFor(t, "idle", func() bool { return p.State().Phase == Idle })
}

// Drop In answers by itself only for a device this one knows, calling from that device's address.
// Anyone else who has the house word - it also travels in announcements - rings like any call.
func TestIntercomDropInOnlyForAKnownDevice(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return false }
	t.Cleanup(func() { knownDevice = known })

	got, l := firstAnswer(t, peer)
	if got != msgRinging {
		t.Fatalf("first answer %q", got)
	}
	waitFor(t, "ringing", func() bool { return p.State().Phase == Ringing })
	if p.State().DropIn {
		t.Fatal("an unknown caller was let drop in")
	}
	select {
	case got := <-l.control:
		t.Fatalf("an unknown caller's call was answered by itself (%q)", got)
	case <-time.After(1500 * time.Millisecond):
	}
	p.Hangup()
	waitFor(t, "idle", func() bool { return p.State().Phase == Idle })
}
