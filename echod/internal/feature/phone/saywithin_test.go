package phone

import (
	"testing"
	"time"
)

// SayWithin waits for room in the call's queue rather than dropping a piece, as Say does, and gives
// up only after the wait; with no call up it takes nothing.
func TestSayWithinWaitsForRoom(t *testing.T) {
	p := &Phone{say: make(chan []int16, 1)}
	if p.SayWithin([]int16{1}, 50*time.Millisecond) {
		t.Fatal("a piece was taken with no call up")
	}
	p.state.Phase = Talking
	if !p.SayWithin([]int16{1}, 50*time.Millisecond) {
		t.Fatal("the first piece was not taken")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		<-p.say // the call sends it
	}()
	if !p.SayWithin([]int16{2}, time.Second) {
		t.Fatal("a piece was dropped although room came within the wait")
	}
	start := time.Now()
	if p.SayWithin([]int16{3}, 50*time.Millisecond) {
		t.Fatal("a piece was taken into a full queue")
	}
	if took := time.Since(start); took < 40*time.Millisecond {
		t.Fatalf("gave up after %v, before the wait", took)
	}
}
