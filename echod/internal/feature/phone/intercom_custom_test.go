package phone

import (
	"context"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestAutoAnswerEnabled(t *testing.T) {
	tests := []struct {
		nom     string
		dropIn  bool
		delai   int
		attendu bool
	}{
		{"Desactive", false, 0, false},
		{"NoDropIn", false, 5, false},
		{"AuthorizedDropIn", true, 5, true},
	}

	for _, test := range tests {
		resultat := autoAnswerEnabled(test.dropIn, test.delai)

		if resultat != test.attendu {
			t.Errorf("%s : obtenu %v, attendu %v",
				test.nom, resultat, test.attendu)
		}
	}
}

func TestIntercomWithoutAutoAnswer(t *testing.T) {
	p, peer := house(t)

	// A zero delay preserves the default behavior.
	if err := config.Set().Home().IntercomAutoAnswerDelay(0); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	if got := <-l.control; got != msgRinging {
		t.Fatalf("Expected ringing, got : %q", got)
	}

	select {
	case got := <-l.control:
		t.Errorf("Unexpected automatic answer : %q", got)
	case <-time.After(200 * time.Millisecond):
		// The call must continue ringing.
	}

	p.Hangup()
}

func TestIntercomDelayedAutoAnswer(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })

	// Enable delayed Drop In answering after two seconds.
	if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	// Verify that the call starts by ringing.
	if got := <-l.control; got != msgRinging {
		t.Fatalf("Expected ringing, got : %q", got)
	}

	// Wait for automatic answering.
	select {
	case got := <-l.control:
		if got != msgAnswer {
			t.Errorf("Expected answer, got : %q", got)
		}
	case <-time.After(4 * time.Second):
		t.Error("No automatic answer after the delay")
	}

	p.Hangup()
}

func TestIntercomHangupBeforeAutoAnswer(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })

	if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	if got := <-l.control; got != msgRinging {
		t.Fatalf("Expected ringing, got : %q", got)
	}

	// The caller hangs up before the delay expires.
	time.Sleep(200 * time.Millisecond)
	l.close()

	// Verify that the device returns to idle.
	waitFor(t, "return to idle", func() bool {
		return p.State().Phase == Idle
	})

	// Wait beyond the original delay.
	time.Sleep(2200 * time.Millisecond)

	if p.State().Phase != Idle {
		t.Error("The intercom did not remain idle")
	}
}

func TestIntercomNoAnswerAfterHangup(t *testing.T) {
	p, peer := house(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	if got := <-l.control; got != msgRinging {
		t.Fatalf("Expected ringing, got : %q", got)
	}

	waitFor(t, "ringing", func() bool {
		return p.State().Phase == Ringing
	})

	// Keep the answer channel for this call.
	p.mu.Lock()
	answered := p.answered
	p.mu.Unlock()

	// Cancel the call.
	p.Hangup()

	// Wait until the device is idle.
	waitFor(t, "return to idle", func() bool {
		return p.State().Phase == Idle
	})

	// Simulate a late automatic answer attempt.
	p.answerIf(answered)

	// Verify that the call was not answered.
	select {
	case <-answered:
		t.Fatal("A canceled call was answered")
	default:
		// No automatic answer is expected.
	}
}

func TestIntercomHangupDuringWarning(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })

	// Activer le decrochage automatique apres 2 secondes.
	if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	if got := <-l.control; got != msgRinging {
		t.Fatalf("Expected ringing, got : %q", got)
	}

	// Attendre que l'avertissement prenne le haut-parleur.
	waitFor(t, "warning started", func() bool {
		return speaker.Sound().Holder() == "drop in"
	})

	// Memoriser le canal de reponse de cet appel.
	p.mu.Lock()
	answered := p.answered
	p.mu.Unlock()

	if answered == nil {
		t.Fatal("Le canal de reponse a deja ete supprime")
	}

	// The caller hangs up during the warning.
	l.close()

	// The device must return to idle.
	waitFor(t, "return to idle", func() bool {
		return p.State().Phase == Idle
	})

	// Verifier qu'aucun decrochage n'a ete declenche.
	select {
	case <-answered:
		t.Fatal("The call was answered after hangup")
	default:
		// Aucun decrochage : resultat attendu.
	}

	if p.State().Phase != Idle {
		t.Fatal("The intercom should be idle")
	}
}
func TestIntercomNewCallAfterHangup(t *testing.T) {
	p, peer := house(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First call.
	premier, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}

	if got := <-premier.control; got != msgRinging {
		t.Fatalf("Premier appel : obtenu %q", got)
	}

	// The first caller hangs up.
	premier.close()

	waitFor(t, "idle after first call", func() bool {
		return p.State().Phase == Idle
	})

	// Second call.
	second, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()

	select {
	case got := <-second.control:
		if got != msgRinging {
			t.Fatalf("Deuxieme appel : attendu ringing, obtenu %q", got)
		}
	case <-ctx.Done():
		t.Fatal("The second call did not ring")
	}

	p.Hangup()
}

func TestIntercomManualAnswerDuringChime(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })

	if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
		t.Fatal(err)
	}

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

	waitFor(t, "warning chime", func() bool {
		return speaker.Sound().Holder() == "drop in"
	})

	// Answer manually while the automatic warning is playing.
	p.Answer()

	select {
	case got := <-l.control:
		if got != msgAnswer {
			t.Fatalf("expected answer, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual answer did not complete")
	}

	waitFor(t, "chime stopped", func() bool {
		return speaker.Sound().Holder() != "drop in"
	})

	p.Hangup()
}

// An interrupted warning must never count as a completed warning.
func TestIntercomChimeInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan bool, 1)

	go func() {
		result <- chime(ctx)
	}()

	waitFor(t, "warning chime", func() bool {
		return speaker.Sound().Holder() == "drop in"
	})

	// Interrupt the warning before it finishes.
	speaker.Sound().Silence()

	select {
	case played := <-result:
		if played {
			t.Fatal("an interrupted chime was reported as completed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the interrupted chime did not stop")
	}
}

// An interrupted auto-answer warning resumes ringing without answering.
func TestIntercomNoAnswerAfterInterruptedChime(t *testing.T) {
	p, peer := house(t)
	if err := config.Set().Home().DropIn(true); err != nil {
		t.Fatal(err)
	}
	known := knownDevice
	knownDevice = func(name, from string) bool { return from == peer.Address }
	t.Cleanup(func() { knownDevice = known })

	if err := config.Set().Home().IntercomAutoAnswerDelay(2); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	if got := <-l.control; got != msgRinging {
		t.Fatalf("expected ringing, got %q", got)
	}

	// Wait until the warning takes the speaker.
	waitFor(t, "warning chime", func() bool {
		return speaker.Sound().Holder() == "drop in"
	})

	// Interrupt the warning.
	speaker.Sound().Silence()

	// An interrupted warning must resume ringing, without automatically answering.
	waitFor(t, "ringing resumed", func() bool {
		return speaker.Sound().Holder() == "ringing"
	})

	// The interrupted warning must not answer the call.
	select {
	case got := <-l.control:
		t.Fatalf("unexpected automatic answer: %q", got)
	case <-time.After(300 * time.Millisecond):
	}

	p.Hangup()

	waitFor(t, "idle after hangup", func() bool {
		return p.State().Phase == Idle
	})
}
