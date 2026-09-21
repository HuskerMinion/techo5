package announce

import (
	"context"
	"testing"
	"time"
)

// A look around ends by itself and leaves the feature standing.
//
// It did not. The library closes the results channel when the look ends and this closed it a second
// time, which panics, and the panic landed before the list of other devices was assigned — so the
// list stayed empty for the life of the process and every device announced only to itself. The
// supervisor caught the panic and restarted the feature every few seconds, so from outside it looked
// like a house where nobody could hear anybody.
//
// This needs no network: the browse ends on its own timeout whether or not anything answers, and the
// second close happened on that path too.
func TestALookAroundEndsWithoutPanicking(t *testing.T) {
	f := &Feature{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.browse(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(browseFor + 10*time.Second):
		t.Fatal("the look never finished")
	}
}

// Two of them in a row: the second one is where a channel reused or a server left registered would
// show up, and a feature that browses every couple of minutes does this for as long as it runs.
func TestLookingAroundTwice(t *testing.T) {
	f := &Feature{}
	for i := range 2 {
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.browse(context.Background())
		}()
		select {
		case <-done:
		case <-time.After(browseFor + 10*time.Second):
			t.Fatalf("look %d never finished", i+1)
		}
	}
}
