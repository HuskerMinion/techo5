package announce

import (
	"testing"
	"time"
)

func reset() {
	peers.Lock()
	peers.by = map[string]known{}
	peers.Unlock()
}

func heard(name string, ago time.Duration) {
	peers.Lock()
	if peers.by == nil {
		peers.by = map[string]known{}
	}
	peers.by[name] = known{Peer: Peer{Name: name, Address: "192.168.1.9", Port: 8181}, at: time.Now().Add(-ago)}
	peers.Unlock()
}

// A device that missed one look is still in the house.
//
// It was not. Each look replaced the list with whatever answered in its three seconds, so a device
// that was busy — or whose answer was lost, which is a thing multicast does — stopped being one of
// the others until the next look two minutes later. The count flapped between one and two on a real
// house, and an announcement sent in the gap quietly did not reach it.
func TestADeviceThatMissedOneLookIsStillHere(t *testing.T) {
	reset()
	heard("Kitchen", browseEvery+time.Second)

	if got := len(Peers()); got != 1 {
		t.Errorf("%d devices, want the one that missed a look kept", got)
	}
}

// A device that has been quiet for several looks running has gone.
func TestADeviceThatWentAwayIsForgotten(t *testing.T) {
	reset()
	heard("Kitchen", forgetAfter+time.Minute)
	heard("Laundry Room", 0)

	// Forgetting happens on a look; nothing has looked yet.
	if got := len(Peers()); got != 2 {
		t.Fatalf("%d devices before a look, want 2", got)
	}

	peers.Lock()
	now := time.Now()
	for name, k := range peers.by {
		if now.Sub(k.at) > forgetAfter {
			delete(peers.by, name)
		}
	}
	peers.Unlock()

	got := Peers()
	if len(got) != 1 || got[0].Name != "Laundry Room" {
		t.Errorf("kept %v, want only the one still answering", got)
	}
}

// The house is reported in a settled order, so a screen showing it does not reshuffle every couple
// of minutes for no reason anybody watching could explain.
func TestTheHouseIsInAStableOrder(t *testing.T) {
	reset()
	heard("Office", 0)
	heard("Kitchen", 0)
	heard("Laundry Room", 0)

	first := Peers()
	for range 5 {
		got := Peers()
		for i := range got {
			if got[i].Name != first[i].Name {
				t.Fatalf("order changed: %v then %v", first, got)
			}
		}
	}
	if first[0].Name != "Kitchen" {
		t.Errorf("first is %q, want them sorted by name", first[0].Name)
	}
}
