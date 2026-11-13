package phone

import (
	"context"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// A known device can use delayed Drop In, but never before the warning chime.
func TestDelayedDropInAnswers(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	if got := <-l.control; got != msgRinging {
		t.Fatalf("expected ringing, got %q", got)
	}
	select {
	case got := <-l.control:
		t.Fatalf("answered before the delay: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case got := <-l.control:
		if got != msgAnswer {
			t.Fatalf("expected answer after warning, got %q", got)
		}
	case <-ctx.Done():
		t.Fatal("delayed Drop In was never answered")
	}
	p.Hangup()
}

// Revoking Drop In or enabling Do Not Disturb during the delay must prevent automatic answering:
// Drop In turned off leaves an ordinary call ringing, no longer shown as a Drop In; Do Not Disturb
// turns the call away, as it would have from the start.
func TestDelayedDropInRechecksPermissions(t *testing.T) {
	cases := []struct {
		name     string
		revoke   func(*testing.T)
		declined bool
	}{
		{"drop in disabled", func(t *testing.T) {
			if err := config.Set().Home().DropIn(false); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"do not disturb enabled", func(t *testing.T) {
			if err := config.Set().Home().DoNotDisturb(true); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, peer := house(t)
			if err := config.Set().Home().DropIn(true); err != nil {
				t.Fatal(err)
			}
			if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
				t.Fatal(err)
			}
			known := knownDevice
			knownDevice = func(name, from string) bool { return from == peer.Address }
			t.Cleanup(func() { knownDevice = known })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			l, err := dialDevice(ctx, peer, intercomKey())
			if err != nil {
				t.Fatal(err)
			}
			defer l.close()
			if got := <-l.control; got != msgRinging {
				t.Fatalf("expected ringing, got %q", got)
			}
			tc.revoke(t)
			if tc.declined {
				select {
				case got := <-l.control:
					if got != msgDecline {
						t.Fatalf("under Do Not Disturb the call said %q, want it declined", got)
					}
				case <-time.After(4 * time.Second):
					t.Fatal("under Do Not Disturb the call was not turned away")
				}
				return
			}
			select {
			case got := <-l.control:
				t.Fatalf("answered after permission changed: %q", got)
			case <-time.After(2700 * time.Millisecond):
			}
			if st := p.State(); st.Phase != Ringing || st.DropIn {
				t.Fatalf("expected an ordinary call ringing, got %v drop in %v", st.Phase, st.DropIn)
			}
			p.Hangup()
		})
	}
}

// A caller with an unrecognized device name or address must ring, even with a delay selected.
func TestDelayedDropInRejectsUnknownPeer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		known func(string, string) bool
	}{
		{"unknown device", func(name, from string) bool { return false }},
		{"wrong address", func(name, from string) bool { return from == "192.0.2.77" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, peer := house(t)
			if err := config.Set().Home().DropIn(true); err != nil {
				t.Fatal(err)
			}
			if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
				t.Fatal(err)
			}
			known := knownDevice
			knownDevice = tc.known
			t.Cleanup(func() { knownDevice = known })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			l, err := dialDevice(ctx, peer, intercomKey())
			if err != nil {
				t.Fatal(err)
			}
			defer l.close()
			if got := <-l.control; got != msgRinging {
				t.Fatalf("expected ringing, got %q", got)
			}
			if p.State().DropIn {
				t.Fatal("unknown peer was allowed to Drop In")
			}
			select {
			case got := <-l.control:
				t.Fatalf("unknown peer was answered: %q", got)
			case <-time.After(2700 * time.Millisecond):
			}
			p.Hangup()
		})
	}
}

// Canceling a delayed call or interrupting its warning must never open the microphones.
func TestDelayedDropInCancellation(t *testing.T) {
	t.Run("hangup before delay", func(t *testing.T) {
		p, peer := house(t)
		if err := config.Set().Home().DropIn(true); err != nil {
			t.Fatal(err)
		}
		if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
			t.Fatal(err)
		}
		known := knownDevice
		knownDevice = func(name, from string) bool { return from == peer.Address }
		t.Cleanup(func() { knownDevice = known })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		l, err := dialDevice(ctx, peer, intercomKey())
		if err != nil {
			t.Fatal(err)
		}
		if got := <-l.control; got != msgRinging {
			t.Fatalf("expected ringing, got %q", got)
		}
		l.close()
		waitFor(t, "return to idle", func() bool { return p.State().Phase == Idle })
		time.Sleep(2200 * time.Millisecond)
		if p.State().Phase != Idle {
			t.Fatal("call was reactivated after hangup")
		}
	})

	t.Run("interrupted warning", func(t *testing.T) {
		p, peer := house(t)
		if err := config.Set().Home().DropIn(true); err != nil {
			t.Fatal(err)
		}
		if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
			t.Fatal(err)
		}
		known := knownDevice
		knownDevice = func(name, from string) bool { return from == peer.Address }
		t.Cleanup(func() { knownDevice = known })
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		l, err := dialDevice(ctx, peer, intercomKey())
		if err != nil {
			t.Fatal(err)
		}
		defer l.close()
		if got := <-l.control; got != msgRinging {
			t.Fatalf("expected ringing, got %q", got)
		}
		waitFor(t, "warning chime", func() bool { return speaker.Sound().Holder() == "drop in" })
		speaker.Sound().Silence()
		waitFor(t, "ringing resumed", func() bool { return speaker.Sound().Holder() == "ringing" })
		select {
		case got := <-l.control:
			t.Fatalf("interrupted warning answered call: %q", got)
		case <-time.After(300 * time.Millisecond):
		}
		p.Hangup()
	})
}
