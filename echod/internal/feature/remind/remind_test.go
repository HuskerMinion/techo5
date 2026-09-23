package remind

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

type sent struct {
	to []string
	m  announce.Message
}

// fake is a Feature whose sound, speech and network are recorders.
func fake(t *testing.T) (*Feature, chan string, chan sent) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	said, out := make(chan string, 4), make(chan sent, 4)
	f := &Feature{
		keep:  true,
		chime: func() {},
		say:   func(label string) { said <- label },
		send:  func(to []string, m announce.Message) { out <- sent{to, m} },
	}
	return f, said, out
}

func next[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("nothing arrived")
		panic("unreachable")
	}
}

func none[T any](t *testing.T, ch chan T) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("did not expect %+v", v)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAReminderForHereIsShownAndSaidAndGoesNowhereElse(t *testing.T) {
	f, said, out := fake(t)
	f.Fire("Take the medication", nil)

	if r, ok := f.Showing(); !ok || r.Label != "Take the medication" {
		t.Fatalf("showing %+v, %v", r, ok)
	}
	if got := next(t, said); got != "Take the medication" {
		t.Errorf("said %q", got)
	}
	none(t, out)

	if !f.Stop() {
		t.Fatal("Stop found nothing to stop")
	}
	if _, ok := f.Showing(); ok {
		t.Error("still showing after Stop")
	}
	none(t, out)
	if f.Stop() {
		t.Error("a second Stop found something to stop")
	}
}

func TestAReminderGoesToTheDevicesNamedAndIsStoppedThere(t *testing.T) {
	f, _, out := fake(t)
	self := config.Get().Device.Name
	f.Fire("Pasta", []string{" Kitchen", "Office", self, ""})

	got := next(t, out)
	if !slices.Equal(got.to, []string{"Kitchen", "Office"}) || got.m.Kind != announce.KindReminder || got.m.Text != "Pasta" || got.m.ID == "" {
		t.Fatalf("sent %+v", got)
	}
	id := got.m.ID

	f.Stop()
	stopped := next(t, out)
	if !slices.Equal(stopped.to, []string{"Kitchen", "Office"}) || stopped.m.Kind != announce.KindReminderStopped || stopped.m.ID != id {
		t.Errorf("stop sent %+v", stopped)
	}
}

// Everywhere is every device, which SendTo takes as nil. It is not the same as nowhere, which was
// easy to get wrong: both are "no names".
func TestEverywhereIsStoppedEverywhere(t *testing.T) {
	f, _, out := fake(t)
	f.Fire("Dinner", []string{"Office", "Everywhere"})

	if got := next(t, out); got.to != nil {
		t.Fatalf("sent to %v, want every device", got.to)
	}
	f.Stop()
	if got := next(t, out); got.to != nil || got.m.Kind != announce.KindReminderStopped {
		t.Errorf("stop sent %+v, want every device", got)
	}
}

// One that came from another device does not know who else it went to, so stopping it tells every
// device, and those not showing it ignore it.
func TestStoppingAReminderFromElsewhereTellsTheHouse(t *testing.T) {
	f, said, out := fake(t)
	f.show(Reminder{ID: "kitchen-1", Label: "Pasta", From: "Kitchen"}, dest{})
	next(t, said)

	f.Stop()
	if got := next(t, out); got.to != nil || got.m.ID != "kitchen-1" {
		t.Errorf("stop sent %+v", got)
	}
}

// On a device with no screen a reminder is its chime and its words: nothing stays up afterwards, so
// there is nothing to put away, and it still goes to the other devices.
func TestWithNoScreenAReminderIsSaidAndGone(t *testing.T) {
	f, said, out := fake(t)
	f.keep = false
	f.Fire("Pasta", []string{"Kitchen"})

	if got := next(t, said); got != "Pasta" {
		t.Errorf("said %q", got)
	}
	if got := next(t, out); got.m.Kind != announce.KindReminder {
		t.Errorf("sent %+v", got)
	}
	if _, ok := f.Showing(); ok {
		t.Error("a reminder stayed up on a device with no screen")
	}
	if f.Stop() {
		t.Error("Stop found something to stop")
	}
}

func TestTheBuildSaysWhetherAReminderStays(t *testing.T) {
	if Get().keep != keeps {
		t.Errorf("keep is %v on a build whose keeps is %v", Get().keep, keeps)
	}
}

func TestAStopFromElsewhereTakesDownOnlyThatReminder(t *testing.T) {
	f, _, out := fake(t)
	f.show(Reminder{ID: "kitchen-1", Label: "Pasta", From: "Kitchen"}, dest{})

	f.dismiss("kitchen-2")
	if _, ok := f.Showing(); !ok {
		t.Fatal("a stop for another reminder took this one down")
	}
	f.dismiss("kitchen-1")
	if _, ok := f.Showing(); ok {
		t.Fatal("the stop for this reminder did not take it down")
	}
	none(t, out) // and it is not passed on
}
