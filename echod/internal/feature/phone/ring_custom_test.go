package phone

import (
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestCustomRingUsesExistingCallRingOption(t *testing.T) {
	if !slices.Contains(RingSounds, "Your own") {
		t.Fatal("custom ringtone unavailable in Call ring")
	}
	config.Use(t.TempDir() + "/ring.json")
	if err := config.Set().Home().RingSound("Your own"); err != nil {
		t.Fatal(err)
	}
	if RingSounds[RingSoundIndex()] != "Your own" {
		t.Fatal("saved custom ringtone not selected")
	}
}

// A short custom WAV repeats after one second; the automatic answer timer is independent.
func TestCustomRingCycleHasOneSecondPause(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    time.Duration
	}{
		{1, 2 * time.Second},
		{2, 3 * time.Second},
		{5, 6 * time.Second},
		{10, 11 * time.Second},
	} {
		pcm := make([]int16, tc.seconds*speaker.Rate*speaker.Channels)
		if got := customRingCycleDuration(pcm); got != tc.want {
			t.Errorf("%d-second clip: cycle %v, want %v", tc.seconds, got, tc.want)
		}
	}
}
