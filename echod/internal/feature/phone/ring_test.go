package phone

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestTheOldPhoneRingIsTwoSecondsOfBellThenFourQuiet(t *testing.T) {
	tone := bellRing()
	const rate = speaker.VoiceRate
	if len(tone) != 6*rate {
		t.Fatalf("one ring is %d samples, want %d (six seconds)", len(tone), 6*rate)
	}
	loudest := func(from, to int) int {
		m := 0
		for _, v := range tone[from:to] {
			if a := int(v); a > m {
				m = a
			} else if -a > m {
				m = -a
			}
		}
		return m
	}
	if m := loudest(0, 2*rate); m < 8000 || m > 9000 {
		t.Errorf("the bell peaks at %d, want close to 9000", m)
	}
	if m := loudest(rate/2, 3*rate/2); m < 2000 {
		t.Errorf("the bell is only %d loud in the middle of the ring; it should keep sounding", m)
	}
	if m := loudest(3*rate, 6*rate); m > 50 {
		t.Errorf("the quiet part reaches %d; the bell should have died away", m)
	}
}

func TestTheCallRingIsTheOneChosen(t *testing.T) {
	p, _ := house(t)
	if len(ringTone()) != len(bellRing()) {
		t.Fatal("a device that never chose rings with something other than the old phone")
	}
	p.SetRingSound("Chime")
	if config.Get().Home.RingSound != "Chime" || len(ringTone()) != len(chimeRing()) {
		t.Fatal("choosing the chime did not make calls ring with it")
	}
	p.SetRingSound("Doorbell")
	if config.Get().Home.RingSound != "Chime" {
		t.Fatal("a sound that is not offered was saved")
	}
	if err := config.Set().Home().RingSound("Gone"); err != nil {
		t.Fatal(err)
	}
	if RingSoundIndex() != 0 {
		t.Fatal("a saved ring no longer offered should read as the old phone")
	}
}
