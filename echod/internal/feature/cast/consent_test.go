//go:build !dot && !spot

package cast

import (
	"context"
	"testing"
	"time"
)

// open is a feature with its port open, as settle leaves it; close shuts it.
func open(t *testing.T) *Feature {
	t.Helper()
	f := build()
	ctx, cancel := context.WithCancel(context.Background())
	f.mu.Lock()
	f.running, f.runCtx = cancel, ctx
	f.mu.Unlock()
	return f
}

// answer waits for the phone to be asked about, then answers it.
func answer(t *testing.T, f *Feature, name string, accept bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for f.Asking() != name {
		if time.Now().After(deadline) {
			t.Fatalf("the device never asked about %q", name)
		}
		time.Sleep(time.Millisecond)
	}
	f.Decide(accept)
}

func TestConsentAcceptThenTheSamePhoneIsAskedAgain(t *testing.T) {
	f := open(t)
	done := make(chan error, 1)
	go func() { done <- f.consent("Pixel 9") }()
	answer(t, f, "Pixel 9", true)
	if err := <-done; err != nil {
		t.Fatalf("accepted phone refused: %v", err)
	}
	if f.Asking() != "" {
		t.Fatal("still asking after the answer")
	}
	// A name proves nothing (the phone picks it), so a reconnect is asked about too.
	go func() { done <- f.consent("Pixel 9") }()
	answer(t, f, "Pixel 9", false)
	if err := <-done; err == nil {
		t.Fatal("a phone accepted before got in without being asked")
	}
}

func TestConsentDeclineRefuses(t *testing.T) {
	f := open(t)
	done := make(chan error, 1)
	go func() { done <- f.consent("Stranger") }()
	answer(t, f, "Stranger", false)
	if err := <-done; err == nil || err.Error() != "declined" {
		t.Fatalf("got %v, want declined", err)
	}
	// Declined is not remembered: the next try asks again.
	go func() { done <- f.consent("Stranger") }()
	answer(t, f, "Stranger", true)
	if err := <-done; err != nil {
		t.Fatalf("second try: %v", err)
	}
}

func TestConsentOtherPhoneIsAsked(t *testing.T) {
	f := open(t)
	done := make(chan error, 1)
	go func() { done <- f.consent("A") }()
	answer(t, f, "A", true)
	<-done
	go func() { done <- f.consent("B") }()
	answer(t, f, "B", false)
	if err := <-done; err == nil {
		t.Fatal("a phone that was never accepted got in on another's acceptance")
	}
}

func TestConsentOnAClosedPortIsRefusedWithoutAsking(t *testing.T) {
	f := build() // never opened, as after close()
	if err := f.consent("Late"); err == nil {
		t.Fatal("a hello that arrived after Cast was turned off was asked about")
	}
	if f.Asking() != "" {
		t.Fatal("a question was put up with Cast off")
	}
	if got := f.state.Get(); got != "off" {
		t.Fatalf("state %q with Cast off, want off", got)
	}
}

func TestTurningCastOffEndsAQuestionAtOnce(t *testing.T) {
	f := open(t)
	done := make(chan error, 1)
	go func() { done <- f.consent("Pixel") }()
	deadline := time.Now().Add(time.Second)
	for f.Asking() != "Pixel" {
		if time.Now().After(deadline) {
			t.Fatal("never asked")
		}
		time.Sleep(time.Millisecond)
	}
	f.close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a question was accepted after Cast was turned off")
		}
	case <-time.After(time.Second):
		t.Fatal("the question outlived the port")
	}
	if f.Asking() != "" {
		t.Fatal("still asking with Cast off")
	}
	if got := f.state.Get(); got != "off" {
		t.Fatalf("state %q, want off", got)
	}
}

// Each question has its own id, so the screen can tell the one it drew from one that came up since (a
// tap answers only the drawn, settled question), and it is 0 when nothing is asked.
func TestEachQuestionHasItsOwnID(t *testing.T) {
	f := open(t)
	if f.AskID() != 0 {
		t.Fatal("an id with nothing asked")
	}
	done := make(chan error, 1)
	var ids []uint64
	for _, name := range []string{"A", "A"} {
		go func() { done <- f.consent(name) }()
		answer(t, f, name, false)
		<-done
		ids = append(ids, f.askSeq)
	}
	if ids[0] == ids[1] {
		t.Fatalf("two questions shared the id %d", ids[0])
	}
	if f.AskID() != 0 {
		t.Fatal("an id left over after the answer")
	}
}
