package btaudio

import "testing"

// A speaker is handed a step only once it takes its volume from here, and only a step that changed:
// the write loop asks every period, and the latest step waiting replaces an older one.
func TestTheSpeakerIsHandedTheLatestChangedStep(t *testing.T) {
	l := &levelStream{to: make(chan int, 1)}
	l.last.Store(-1)
	if l.Level(10) {
		t.Fatal("a speaker not yet known to take a volume took one")
	}
	l.on.Store(true)
	if !l.Level(10) {
		t.Fatal("a speaker that takes a volume did not")
	}
	l.Level(10)
	if got := <-l.to; got != 10 {
		t.Errorf("handed %d, want 10", got)
	}
	select {
	case got := <-l.to:
		t.Errorf("the same step was handed again: %d", got)
	default:
	}
	l.Level(11)
	l.Level(14)
	if got := <-l.to; got != 14 {
		t.Errorf("handed %d, want the latest, 14", got)
	}
}
