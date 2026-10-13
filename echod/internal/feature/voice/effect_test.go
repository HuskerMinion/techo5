package voice

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// handleNext handles what the turn was posted, failing if nothing was.
func handleNext(t *testing.T, c *conversation) {
	t.Helper()
	select {
	case e := <-c.events:
		c.handle(e)
	case <-time.After(time.Second):
		t.Fatal("nothing was posted to the turn")
	}
}

// An effect asked for during a request shows at once on the step it is in; None clears it, Default
// puts the step's own back, and a name the ring does not have is refused.
func TestAnEffectAskedForShowsOnTheStep(t *testing.T) {
	c := thinkingTurn(t)
	v := &Voice{turn: c}
	own := c.claim.Showing()
	var other string
	for _, name := range led.EffectNames() {
		if name != own.Effect {
			other = name
			break
		}
	}

	if err := v.ShowEffect("Not an effect"); err == nil {
		t.Error("an effect the ring does not have was taken")
	}

	if err := v.ShowEffect(other); err != nil {
		t.Fatal(err)
	}
	handleNext(t, c)
	if got := c.claim.Showing(); got.Effect != other {
		t.Errorf("the thinking request shows %q, want %q", got.Effect, other)
	}

	if err := v.ShowEffect(component.EffectNone); err != nil {
		t.Fatal(err)
	}
	handleNext(t, c)
	if got := c.claim.Showing(); got.Effect != "" {
		t.Errorf("None left %q on the ring", got.Effect)
	}

	if err := v.ShowEffect(component.EffectDefault); err != nil {
		t.Fatal(err)
	}
	handleNext(t, c)
	if got := c.claim.Showing(); got.Effect != own.Effect || got.Reverse != own.Reverse {
		t.Errorf("Default shows %q reversed %v, want the step's own %q reversed %v",
			got.Effect, got.Reverse, own.Effect, own.Reverse)
	}
}

// With no request running, an effect asked for is not an error and changes nothing.
func TestAnEffectWithNoRequestDoesNothing(t *testing.T) {
	c := thinkingTurn(t)
	c.handle(event{kind: evCancel})
	v := &Voice{turn: c}
	for len(c.events) > 0 {
		<-c.events
	}

	if err := v.ShowEffect(led.EffectNames()[0]); err != nil {
		t.Errorf("refused with no request running: %v", err)
	}
	select {
	case e := <-c.events:
		t.Errorf("posted %v with no request running", e.kind)
	case <-time.After(50 * time.Millisecond):
	}
}
