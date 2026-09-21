package detect

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
)

// The announce word starts off.
//
// It is a device that acts on something said near it, and the phrase is short enough to turn up
// inside ordinary sentences — "house announcement" begins with the whole of it. Nobody should
// discover that by having a microphone open in their kitchen.
func TestTheAnnounceWordStartsOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))

	if config.Get().Wake.Announce.Listening() {
		t.Error("listening on a fresh device, want off until somebody asks")
	}
}

// The top of the range is off, which is what makes the one control a switch as well.
func TestTurningTheAnnounceWordOnAndOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))

	if err := config.Set().Announce().Threshold(config.DefaultAnnounceThreshold); err != nil {
		t.Fatal(err)
	}
	if !config.Get().Wake.Announce.Listening() {
		t.Error("not listening after being turned on")
	}

	if err := config.Set().Announce().Threshold(config.AnnounceOff); err != nil {
		t.Fatal(err)
	}
	if config.Get().Wake.Announce.Listening() {
		t.Error("still listening at the top of the range, which is the off position")
	}
}

// The two reserved slots are not each other, and both sit above the slots Home Assistant hands out.
// A collision here would be one word answering for another, silently.
func TestTheReservedSlotsAreDistinct(t *testing.T) {
	if AnnounceSlot == StopSlot {
		t.Fatal("the announce word and the stop word share a slot")
	}
	// Home Assistant's own slots are 0 and 1; anything it might grow into must stay below these.
	if StopSlot < 10 || AnnounceSlot < 10 {
		t.Errorf("reserved slots %d and %d are too near Home Assistant's", StopSlot, AnnounceSlot)
	}
}

// The model is embedded and written out where nothing scans for wake words, so the picker cannot
// offer it as an assistant to talk to. It is a feature of the device, not a choice of voice.
func TestTheModelIsThereToLoad(t *testing.T) {
	dir := t.TempDir()

	path, err := assets.HouseAnnounce(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Dir(path); got != dir {
		t.Errorf("written to %s, want %s", got, dir)
	}

	// Again, to check an unchanged model is not rewritten every boot: the store is flash.
	again, err := assets.HouseAnnounce(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != path {
		t.Errorf("second call gave %s, want %s", again, path)
	}
}

// The wake word cannot start announcements back to back.
//
// This is a circuit breaker, not a considered interval. Devices in earshot feed each other, and the
// reasoning that said they could not has been wrong on real hardware twice: once when they answered
// their own playback, and again after the hold-off that was supposed to stop it. A house cannot talk
// to itself faster than this whichever argument turns out to be wrong next.
func TestTheWakeWordCannotRepeatStraightAway(t *testing.T) {
	if minGap < 5*time.Second {
		t.Errorf("minGap is %v, which is not long enough to break a loop", minGap)
	}
	// Longer than a recording can run, so it never cuts off something somebody meant.
	if minGap < longestRecording {
		t.Errorf("minGap %v is shorter than a recording, so one could start inside another", minGap)
	}
}

// longestRecording mirrors announce's ceiling; a copy, because importing it here for one number
// would be a dependency for a test.
const longestRecording = 15 * time.Second
