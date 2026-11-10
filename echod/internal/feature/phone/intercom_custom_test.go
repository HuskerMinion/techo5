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
		{"Apres5Secondes", false, 5, true},
		{"DropIn", true, 5, false},
	}

	for _, test := range tests {
		resultat := autoAnswerEnabled(test.dropIn, test.delai)

		if resultat != test.attendu {
			t.Errorf("%s : obtenu %v, attendu %v",
				test.nom, resultat, test.attendu)
		}
	}
}

func TestIntercomSansDecrochageAuto(t *testing.T) {
	p, peer := house(t)

	// Le délai 0 désactive le décrochage automatique.
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
		t.Fatalf("Attendu : sonnerie, obtenu : %q", got)
	}

	select {
	case got := <-l.control:
		t.Errorf("Décrochage inattendu : %q", got)
	case <-time.After(200 * time.Millisecond):
		// L'appel doit continuer à sonner.
	}

	p.Hangup()
}

func TestIntercomDecrochageAutomatique(t *testing.T) {
	p, peer := house(t)

	// Activer le décrochage automatique après 2 secondes.
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

	// Vérifier que l'appel commence par sonner.
	if got := <-l.control; got != msgRinging {
		t.Fatalf("Attendu : sonnerie, obtenu : %q", got)
	}

	// Attendre le décrochage automatique.
	select {
	case got := <-l.control:
		if got != msgAnswer {
			t.Errorf("Attendu : décrochage, obtenu : %q", got)
		}
	case <-time.After(4 * time.Second):
		t.Error("Aucun décrochage automatique après 2 secondes")
	}

	p.Hangup()
}

func TestIntercomRaccrochageAvantDecrochage(t *testing.T) {
	p, peer := house(t)

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
		t.Fatalf("Attendu : sonnerie, obtenu : %q", got)
	}

	// L'appelant raccroche avant les 2 secondes.
	time.Sleep(200 * time.Millisecond)
	l.close()

	// Vérifier que l'appareil revient au repos.
	waitFor(t, "retour au repos", func() bool {
		return p.State().Phase == Idle
	})

	// Attendre au-delà du délai initial.
	time.Sleep(2200 * time.Millisecond)

	if p.State().Phase != Idle {
		t.Error("L'interphone n'est pas resté au repos")
	}
}

func TestIntercomPasDeReponseApresRaccrochage(t *testing.T) {
	p, peer := house(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	l, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()

	if got := <-l.control; got != msgRinging {
		t.Fatalf("Attendu : sonnerie, obtenu : %q", got)
	}

	waitFor(t, "sonnerie", func() bool {
		return p.State().Phase == Ringing
	})

	// Conserver la référence de cet appel.
	p.mu.Lock()
	answered := p.answered
	p.mu.Unlock()

	// Annuler l'appel.
	p.Hangup()

	// Attendre que l'appareil soit réellement au repos.
	waitFor(t, "retour au repos", func() bool {
		return p.State().Phase == Idle
	})

	// Simuler une tentative de décrochage tardive.
	p.answerIf(answered)

	// Vérifier que la demande de réponse n'a pas été activée.
	select {
	case <-answered:
		t.Fatal("Un appel annulé a été décroché")
	default:
		// Aucun décrochage : comportement attendu.
	}
}

func TestIntercomRaccrochagePendantAvertissement(t *testing.T) {
	p, peer := house(t)

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
		t.Fatalf("Attendu : sonnerie, obtenu : %q", got)
	}

	// Attendre que l'avertissement prenne le haut-parleur.
	waitFor(t, "debut de l'avertissement", func() bool {
		return speaker.Sound().Holder() == "drop in"
	})

	// Memoriser le canal de reponse de cet appel.
	p.mu.Lock()
	answered := p.answered
	p.mu.Unlock()

	if answered == nil {
		t.Fatal("Le canal de reponse a deja ete supprime")
	}

	// L'appelant raccroche pendant l'avertissement.
	l.close()

	// L'appareil doit revenir au repos.
	waitFor(t, "retour au repos", func() bool {
		return p.State().Phase == Idle
	})

	// Verifier qu'aucun decrochage n'a ete declenche.
	select {
	case <-answered:
		t.Fatal("L'appel a ete decroche apres le raccrochage")
	default:
		// Aucun decrochage : resultat attendu.
	}

	if p.State().Phase != Idle {
		t.Fatal("L'interphone devrait etre au repos")
	}
}
func TestIntercomNouvelAppelApresRaccrochage(t *testing.T) {
	p, peer := house(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Premier appel.
	premier, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}

	if got := <-premier.control; got != msgRinging {
		t.Fatalf("Premier appel : obtenu %q", got)
	}

	// Le premier appelant raccroche.
	premier.close()

	waitFor(t, "repos apres premier appel", func() bool {
		return p.State().Phase == Idle
	})

	// Deuxieme appel.
	second, err := dialDevice(ctx, peer, intercomKey())
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()

	select {
	case got := <-second.control:
		if got != msgRinging {
			t.Fatalf("Deuxieme appel : attendu sonnerie, obtenu %q", got)
		}
	case <-ctx.Done():
		t.Fatal("Le deuxieme appel ne sonne pas")
	}

	p.Hangup()
}

func TestIntercomAncienAppelNeModifiePasNouvelAppel(t *testing.T) {
	p, _ := house(t)

	ancien := make(chan struct{})
	nouveau := make(chan struct{})

	// Simuler un nouvel appel actif.
	p.mu.Lock()
	p.intercomCall = nouveau
	p.claim(Ringing, "Nouvel appel", true)
	p.state.Intercom = true
	p.mu.Unlock()

	// Simuler le nettoyage d'un ancien appel.
	p.setIf(func(s *State) bool {
		if p.intercomCall != ancien {
			return false
		}

		s.Phase, s.Peer = Idle, ""
		return true
	})

	// Le nouvel appel doit toujours sonner.
	if p.State().Phase != Ringing {
		t.Fatal("Le nouvel appel a ete interrompu")
	}

	if p.State().Peer != "Nouvel appel" {
		t.Fatal("Le nouvel appel a perdu son correspondant")
	}
}

func TestIntercomManualAnswerDuringChime(t *testing.T) {
	p, peer := house(t)

	if err := config.Set().Home().IntercomRingDisabled(false); err != nil {
		t.Fatal(err)
	}
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

	if err := config.Set().Home().IntercomRingDisabled(false); err != nil {
		t.Fatal(err)
	}
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

	// The ringtone must not resume after the warning was interrupted.
	time.Sleep(100 * time.Millisecond)

	if speaker.Sound().Holder() == "ringing" {
		t.Fatal("ringtone resumed after the warning")
	}

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
