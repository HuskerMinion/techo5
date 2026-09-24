package mute

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// fakeLine is a mute line whose hardware may or may not act on the button by itself.
type fakeLine struct {
	muted bool
	acts  bool
	sets  []bool
}

func (f *fakeLine) Get() (bool, error)         { return f.muted, nil }
func (f *fakeLine) Set(muted bool) error       { f.sets = append(f.sets, muted); f.muted = muted; return nil }
func (f *fakeLine) Toggle() (bool, error)      { f.muted = !f.muted; return f.muted, nil }
func (f *fakeLine) HardwareActs(was bool) bool { return f.acts }
func (f *fakeLine) Lag() time.Duration         { return 0 }

// A press that stops a ring leaves the microphones where they were. On a Dot the keypad driver has
// already flipped the mute by the time the press arrives; before keep, a Dot muted before an alarm
// was live after the press that stopped it, with its wake words stopped and its state still muted
// (techo5-dot#4).
func TestKeepAfterARing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		acts      bool
		was, now  bool // the switch before the press, and the line after the hardware's own move
		wantSets  []bool
		wantMuted bool
	}{
		{"a muted Dot that the press unmuted is muted again", true, true, false, []bool{true}, true},
		{"hardware that did not move is left alone", true, true, true, nil, true},
		{"hardware that does not act on the button is never touched", false, true, true, nil, true},
	} {
		line := &fakeLine{muted: tc.now, acts: tc.acts}
		m := &Mute{sw: &esphome.Switch{}, line: line}
		m.sw.Set(tc.was)

		m.keep()

		if len(line.sets) != len(tc.wantSets) || (len(tc.wantSets) > 0 && line.sets[0] != tc.wantSets[0]) {
			t.Errorf("%s: line set %v, want %v", tc.name, line.sets, tc.wantSets)
		}
		if line.muted != tc.wantMuted {
			t.Errorf("%s: line ends muted=%v, want %v", tc.name, line.muted, tc.wantMuted)
		}
		if m.sw.Get() != tc.was {
			t.Errorf("%s: switch says %v, want it unchanged at %v", tc.name, m.sw.Get(), tc.was)
		}
	}
}
