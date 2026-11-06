package voice

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	esphome "github.com/ygelfand/go-esphome-device"
)

var errTest = errors.New("test")

// wakeCallStandIns replaces the phone, the mute and the event for a test, and gives back what they saw.
type wakeCallStandIns struct {
	state     phone.State
	book      []phone.Contact
	muted     bool
	dialed    []string
	notPlaced []string
}

func standIn(t *testing.T) *wakeCallStandIns {
	t.Helper()
	oldPhone, oldDial, oldMute, oldNot := wakeCallPhone, wakeCallDial, wakeCallMuted, wakeCallNotPlaced
	t.Cleanup(func() {
		wakeCallPhone, wakeCallDial, wakeCallMuted, wakeCallNotPlaced = oldPhone, oldDial, oldMute, oldNot
	})
	s := &wakeCallStandIns{
		state: phone.State{Configured: true, Registered: true, Phase: phone.Idle},
		book:  []phone.Contact{{Name: "Chat service", Number: "100", Also: []string{"Assistant"}}},
	}
	wakeCallPhone = func() (phone.State, []phone.Contact) { return s.state, s.book }
	wakeCallMuted = func() bool { return s.muted }
	wakeCallDial = func(c phone.Contact) error {
		s.dialed = append(s.dialed, c.Number)
		s.state.Phase = phone.Dialing
		return nil
	}
	wakeCallNotPlaced = func(name, reason string) { s.notPlaced = append(s.notPlaced, reason) }
	return s
}

func wakeCallVoice() *Voice {
	vs := &esphome.VoiceSatellite{}
	return &Voice{vs: vs, turn: newConversation(vs)}
}

func TestWakeCallRoutesOnlyTheDetectedSlotAndUsesPhoneAdmission(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	s := standIn(t)
	v := wakeCallVoice()
	if err := SetWakeCall(1, "assistant"); err != nil {
		t.Fatal(err)
	}
	if config.Get().Wake.Slot(0).CallContact != "Chat service" {
		t.Fatal("alias not resolved to the contact's first name")
	}
	if !v.CanAnswer(0) {
		t.Fatal("a call that can be placed depends on Home Assistant")
	}
	v.WokeAt(0, time.Now())
	if len(s.dialed) != 1 || s.dialed[0] != "100" || len(v.turn.events) != 0 {
		t.Fatalf("wake did not call directly: dialed %v, events %d", s.dialed, len(v.turn.events))
	}
	if len(s.notPlaced) != 0 {
		t.Fatalf("a placed call was reported as not placed: %v", s.notPlaced)
	}

	// The manual Wake button keeps the usual assistant.
	s.state.Phase = phone.Idle
	v.Start(0)
	if len(s.dialed) != 1 || len(v.turn.events) != 1 {
		t.Fatal("manual Wake button lost the usual assistant")
	}
}

func TestWakeCallNotPlacedSaysWhy(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	s := standIn(t)
	if err := SetWakeCall(1, "Chat service"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		set    func()
		reason string
	}{
		{"no phone account", func() { s.state.Configured = false }, notPlacedNoPhone},
		{"not signed in", func() { s.state.Registered = false }, notPlacedNotSignedIn},
		{"muted", func() { s.muted = true }, notPlacedMuted},
		{"contact gone from the book", func() { s.book = nil }, notPlacedNoContact},
	} {
		t.Run(c.name, func(t *testing.T) {
			s.state = phone.State{Configured: true, Registered: true, Phase: phone.Idle}
			s.book = []phone.Contact{{Name: "Chat service", Number: "100"}}
			s.muted, s.dialed, s.notPlaced = false, nil, nil
			c.set()
			v := wakeCallVoice()
			if v.CanAnswer(0) {
				t.Error("claims a wake it cannot call for")
			}
			v.WokeAt(0, time.Now())
			if len(s.dialed) != 0 {
				t.Error("dialed")
			}
			if len(v.turn.events) != 0 {
				t.Error("fell back to a turn")
			}
			if len(s.notPlaced) != 1 || s.notPlaced[0] != c.reason {
				t.Errorf("not_placed %v, want %s", s.notPlaced, c.reason)
			}
		})
	}
}

func TestWakeCallDialFailureIsReported(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	s := standIn(t)
	_ = SetWakeCall(1, "Chat service")
	wakeCallDial = func(phone.Contact) error { return errTest }
	v := wakeCallVoice()
	v.WokeAt(0, time.Now())
	if len(s.notPlaced) != 1 || s.notPlaced[0] != notPlacedDialFailed {
		t.Fatalf("not_placed %v", s.notPlaced)
	}
}

// During a call the slot is an ordinary one: a wake let through (Wake word during calls) opens the
// usual turn, so the call can be hung up by voice, and nothing is dialed.
func TestWakeCallDuringACallIsAnOrdinaryWake(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	s := standIn(t)
	_ = SetWakeCall(1, "Chat service")
	for _, phase := range []phone.Phase{phone.Ringing, phone.Talking} {
		s.state.Phase = phase
		if wakeCallApplies(0) {
			t.Errorf("%v: still a call slot", phase)
		}
		v := wakeCallVoice()
		v.WokeAt(0, time.Now())
		if len(s.dialed) != 0 || len(s.notPlaced) != 0 {
			t.Errorf("%v: dialed %v, not placed %v", phase, s.dialed, s.notPlaced)
		}
		if len(v.turn.events) != 1 {
			t.Errorf("%v: no ordinary turn", phase)
		}
	}
}

// Home Assistant's own wake word engine never listens for a slot chosen to call, as for realtime voice.
func TestWakeCallSlotIsNotHomeAssistants(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	standIn(t)
	if !haAnswers(0) {
		t.Fatal("an ordinary slot is not Home Assistant's")
	}
	_ = SetWakeCall(1, "Chat service")
	if haAnswers(0) {
		t.Fatal("Home Assistant still listens for a call slot")
	}
	_ = SetWakeCall(1, "")
	if !haAnswers(0) {
		t.Fatal("the slot stays off Home Assistant after the call was cleared")
	}
}

func TestWakeCallConfigurationValidatesTheChoice(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).ID("test_word")
	standIn(t)
	for _, slot := range []int{-1, 0, 3, 999999} {
		if err := SetWakeCall(slot, "Chat service"); err == nil {
			t.Error("invalid slot accepted")
		}
	}
	for _, name := range []string{"unknown", strings.Repeat("x", MaxCallContactLen+1), "Chat\nservice"} {
		if err := SetWakeCall(1, name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
	if err := SetWakeCall(2, "Chat service"); err == nil {
		t.Error("a slot with no word accepted")
	}
	if err := config.Set().Wake(0).Realtime(true); err != nil {
		t.Fatal(err)
	}
	if err := SetWakeCall(1, "Chat service"); err == nil {
		t.Fatal("realtime slot accepted")
	}
	if err := SetWakeCall(1, ""); err != nil {
		t.Fatal("could not explicitly turn the call off")
	}
}
