//go:build linux

package btaudio

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
)

// refusingAlsa is a bluez-alsa that will not leave a stream's audio unscaled.
type refusingAlsa struct{ fakeAlsa }

func (a *refusingAlsa) SetSoftVolume(bluealsa.PCM, bool) error { return errors.New("not supported") }

const speakerPath = "/org/bluealsa/hci0/dev_Y/a2dpsrc/sink"

// speakerPCM is the stream to a speaker at level on both channels, scaled by bluez-alsa or not (soft).
func speakerPCM(level int, soft bool) bluealsa.PCM {
	return bluealsa.PCM{Path: speakerPath, Volume: uint16(level)<<8 | uint16(level), SoftVolume: soft}
}

// handed is what the speaker package was told about the sink's own volume: set for each hand, nil for
// each time it was taken back.
type handed struct{ sets []func(int) }

func (h *handed) last() func(int) { return h.sets[len(h.sets)-1] }

// speakerFeature is a Feature with the device's volume at step 12, what the speaker package is handed
// recorded rather than done, and a speaker's buttons setting the device's volume counted, and taken
// unless held (night volume holding a raise).
func speakerFeature(t *testing.T) (*Feature, *handed, *[]int, *bool) {
	t.Helper()
	vol, held := 12, false
	was := volumeNow
	volumeNow = func() int { return vol }
	t.Cleanup(func() { volumeNow = was })
	h := &handed{}
	wasLevel := sinkLevel
	sinkLevel = func(_ speaker.Sink, set func(int)) { h.sets = append(h.sets, set) }
	t.Cleanup(func() { sinkLevel = wasLevel })
	set := []int{}
	wasTo := speakerTo
	speakerTo = func(level int) {
		set = append(set, level)
		if !held {
			vol = stepFor(level)
		}
	}
	t.Cleanup(func() { speakerTo = wasTo })
	return &Feature{}, h, &set, &held
}

func yes() bool { return true }
func no() bool  { return false }

// The Echo's step goes to the speaker as its share of the range, as a phone hands over its slider: the
// speaker puts its own loudness curve on that. Off is 0, the top step the speaker's top, every step a
// level of its own, and a speaker's level comes back as the step it is nearest.
func TestTheSpeakersLevelIsTheStepsShare(t *testing.T) {
	if levelFor(0) != 0 || levelFor(media.VolumeSteps) != bluealsa.VolumeMax {
		t.Fatalf("ends at %d and %d", levelFor(0), levelFor(media.VolumeSteps))
	}
	for step := 1; step <= media.VolumeSteps; step++ {
		if levelFor(step) <= levelFor(step-1) {
			t.Errorf("step %d at %d, no louder than step %d", step, levelFor(step), step-1)
		}
		if stepFor(levelFor(step)) != step {
			t.Errorf("step %d came back as %d", step, stepFor(levelFor(step)))
		}
	}
}

// A speaker that does not take absolute volume, or a bluez-alsa that will not leave its audio unscaled,
// keeps the volume as it was: nothing set on the stream, and the audio scaled here (#550).
func TestNoAbsoluteVolumeKeepsTheVolumeInSoftware(t *testing.T) {
	f, h, set, _ := speakerFeature(t)
	al := &fakeAlsa{}
	s := &bluealsa.Stream{PCM: speakerPCM(bluealsa.VolumeMax, true)}
	f.speakerLook(al, s.PCM, s, no, true)
	f.speakerLook(al, speakerPCM(40, true), s, no, false) // its level moving is not taken either
	if soft, levels := al.got(); len(soft) != 0 || len(levels) != 0 || len(h.sets) != 0 || len(*set) != 0 {
		t.Errorf("soft %v, levels %v, handed %d, set %v; want nothing", soft, levels, len(h.sets), *set)
	}

	r := &refusingAlsa{}
	f.speakerLook(r, s.PCM, s, yes, true)
	if _, levels := r.got(); len(levels) != 0 || len(h.sets) != 0 || f.spk.abs {
		t.Errorf("refused: levels %v, handed %d, abs %v", levels, len(h.sets), f.spk.abs)
	}
}

// A speaker that takes absolute volume gets the device's level, and the audio goes to it unscaled; a
// level it lost switching off and on is put back as it reconnects.
func TestTheSpeakerIsHandedTheDevicesVolume(t *testing.T) {
	f, h, set, _ := speakerFeature(t)
	al := &fakeAlsa{}
	s := &bluealsa.Stream{PCM: speakerPCM(10, true)}
	f.speakerLook(al, s.PCM, s, yes, true)
	if soft, _ := al.got(); !slices.Equal(soft, []bool{false}) || len(h.sets) != 1 || h.last() == nil {
		t.Fatalf("soft %v, handed %d", soft, len(h.sets))
	}
	h.last()(12) // the speaker package tells the step, as SetSinkLevel does
	if _, levels := al.got(); !slices.Equal(levels, []int{levelFor(12)}) {
		t.Errorf("levels %v, want [%d]", levels, levelFor(12))
	}
	// The same step told again is not handed again; another is.
	h.last()(12)
	h.last()(20)
	if _, levels := al.got(); !slices.Equal(levels, []int{levelFor(12), levelFor(20)}) {
		t.Errorf("levels %v, want [%d %d]", levels, levelFor(12), levelFor(20))
	}
	if len(*set) != 0 {
		t.Errorf("handing the level set the volume: %v", *set)
	}

	// The re-apply a few seconds on hands the level again, as the first time.
	f.spk.again = true
	f.speakerLook(al, speakerPCM(levelFor(20), false), s, yes, false)
	if len(h.sets) != 2 {
		t.Fatalf("handed %d times after the re-apply, want 2", len(h.sets))
	}
	h.last()(12)
	if _, levels := al.got(); levels[len(levels)-1] != levelFor(12) {
		t.Errorf("the re-apply did not put the level back: %v", levels)
	}
}

// The speaker's own buttons set the device's volume; the level handed to it coming back, rounded or
// late, does not, so a change never goes round (#550). Nor does its level while it was raised for a
// ring, or muted.
func TestTheSpeakersButtonsSetTheVolumeWithoutALoop(t *testing.T) {
	for _, c := range []struct {
		level, last int
		known, ours bool
		told, step  int
		want        bool
	}{
		{levelFor(20), levelFor(12), true, false, 12, 12, true},  // a press
		{levelFor(12), levelFor(12), true, false, 12, 12, false}, // nothing moved
		{levelFor(12), 0, false, false, 12, 12, false},           // nothing known before
		{levelFor(20), levelFor(12), true, true, 12, 12, false},  // ours on its way back
		{levelFor(12) - 2, levelFor(12), true, true, 12, 12, false},
		{levelFor(25), levelFor(20), true, false, 20, 12, false}, // raised for a ring
		{levelFor(5), 0, true, false, 0, 12, false},              // muted
	} {
		if got := speakerSets(c.level, c.last, c.known, c.ours, c.told, c.step); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}

	f, h, set, _ := speakerFeature(t)
	al := &fakeAlsa{}
	s := &bluealsa.Stream{PCM: speakerPCM(10, true)}
	f.speakerLook(al, s.PCM, s, yes, true)
	h.last()(12)
	// bluez-alsa says back what was handed, then the speaker its own rounding of it: neither is a press.
	f.speakerLook(al, speakerPCM(levelFor(12), false), s, yes, false)
	f.speakerLook(al, speakerPCM(levelFor(12)+3, false), s, yes, false)
	if len(*set) != 0 {
		t.Fatalf("our own level came back as a press: %v", *set)
	}
	// Later, a press on the speaker.
	f.spk.ours = time.Time{}
	f.speakerLook(al, speakerPCM(levelFor(18), false), s, yes, false)
	if !slices.Equal(*set, []int{levelFor(18)}) {
		t.Fatalf("set %v, want [%d]", *set, levelFor(18))
	}
	// The device's volume then at the speaker's step, it is not handed back to it, rounded.
	_, before := al.got()
	h.last()(18)
	if _, levels := al.got(); len(levels) != len(before) {
		t.Errorf("the press was handed back: %v", levels[len(before):])
	}
}

// A press the device's volume does not follow (night volume holds the raise) puts the speaker back at
// the device's step, since what it plays is no longer scaled here.
func TestAPressHeldBackPutsTheSpeakerBack(t *testing.T) {
	f, h, set, held := speakerFeature(t)
	*held = true
	al := &fakeAlsa{}
	s := &bluealsa.Stream{PCM: speakerPCM(10, true)}
	f.speakerLook(al, s.PCM, s, yes, true)
	h.last()(12)
	f.spk.ours = time.Time{}
	f.speakerLook(al, speakerPCM(levelFor(25), false), s, yes, false) // the device's volume stays at 12
	if _, levels := al.got(); len(*set) != 1 || levels[len(levels)-1] != levelFor(12) {
		t.Errorf("levels %v: the speaker was left above the device's step", levels)
	}
}

// Absolute volume going away (the AVRCP link dropped) takes the volume back into software: the device
// scales the audio again and bluez-alsa leaves it at full level; bluez-alsa losing the setting while
// it stays is put right.
func TestAbsoluteVolumeGoingAwayGoesBackToSoftware(t *testing.T) {
	f, h, _, _ := speakerFeature(t)
	al := &fakeAlsa{}
	s := &bluealsa.Stream{PCM: speakerPCM(10, true)}
	f.speakerLook(al, s.PCM, s, yes, true)
	h.last()(12)

	// Just after, bluez-alsa may still say it scales: not lost yet.
	f.speakerLook(al, speakerPCM(levelFor(12), true), s, yes, false)
	if len(h.sets) != 1 {
		t.Fatalf("handed %d times before bluez-alsa had time to say", len(h.sets))
	}
	f.spk.absAt = time.Now().Add(-modeSettle)
	f.speakerLook(al, speakerPCM(levelFor(12), true), s, yes, false)
	if len(h.sets) != 2 || h.last() == nil {
		t.Fatalf("bluez-alsa lost the setting: handed %d times", len(h.sets))
	}

	f.spk.checked = time.Time{}
	f.speakerLook(al, speakerPCM(levelFor(12), false), s, no, false)
	soft, levels := al.got()
	if len(h.sets) != 3 || h.last() != nil || soft[len(soft)-1] != true || levels[len(levels)-1] != bluealsa.VolumeMax {
		t.Errorf("handed %d, last nil %v, soft %v, levels %v", len(h.sets), h.last() == nil, soft, levels)
	}
	if f.spk.abs {
		t.Error("still absolute")
	}
	// A stream that closed is handed nothing more.
	f.speakerLook(al, s.PCM, s, yes, true)
	set := h.last()
	f.speakerGone()
	_, before := al.got()
	set(20)
	if _, levels := al.got(); len(levels) != len(before) {
		t.Error("a closed stream's speaker was handed a level")
	}
}
