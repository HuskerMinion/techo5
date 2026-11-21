//go:build !dot && !spot

package cast

import (
	"testing"
	"time"
)

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
	f := build()
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
	f := build()
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
	f := build()
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
