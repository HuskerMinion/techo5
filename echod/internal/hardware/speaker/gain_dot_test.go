//go:build dot && !donut

package speaker

import (
	"errors"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// driverLevel is the level a sequence writes to the codec's driver gain, or -1 when it writes none.
func driverLevel(t *testing.T, seq []kctl) int32 {
	t.Helper()
	level := int32(-1)
	for _, k := range seq {
		if k.name == driverGain {
			if level >= 0 {
				t.Fatalf("the driver gain is written twice in %v", seq)
			}
			level = k.level
		}
	}
	return level
}

// The Speaker gain raises the speaker's driver gain 1 level per dB over the stock 6 (0 dB), on both
// boards, and never touches the jack's 11. Out-of-range values are held to 0..+6 dB.
func TestSpeakerGainSetsTheDriverPerSettingAndRoute(t *testing.T) {
	boards := map[string]map[Output][]kctl{"dot": dotPaths, "radar": radarPaths(unityEQ(), unityEQ())}
	for board, paths := range boards {
		if got := driverLevel(t, paths[OutputSpeaker]); got != 6 {
			t.Fatalf("%s: the stock speaker path writes %d, want 6", board, got)
		}
		for db := 0; db <= config.SpeakerGainMax; db++ {
			if got, want := driverLevel(t, withSpeakerGain(paths[OutputSpeaker], OutputSpeaker, db)), int32(6+db); got != want {
				t.Errorf("%s speaker at +%d dB: level %d, want %d", board, db, got, want)
			}
			if got := driverLevel(t, withSpeakerGain(paths[OutputHeadphone], OutputHeadphone, db)); got != 11 {
				t.Errorf("%s jack at +%d dB: level %d, want 11", board, db, got)
			}
		}
		for db, want := range map[int]int32{-1: 6, -40: 6, 7: 12, 29: 12} {
			if got := driverLevel(t, withSpeakerGain(paths[OutputSpeaker], OutputSpeaker, db)); got != want {
				t.Errorf("%s speaker at %d dB: level %d, want %d", board, db, got, want)
			}
		}
		// Everything but the driver gain is the path as it was, in the same order.
		gained := withSpeakerGain(paths[OutputSpeaker], OutputSpeaker, 4)
		if len(gained) != len(paths[OutputSpeaker]) {
			t.Fatalf("%s: the gained path has %d writes, want %d", board, len(gained), len(paths[OutputSpeaker]))
		}
		for i, k := range gained {
			if k.name != paths[OutputSpeaker][i].name || (k.name != driverGain && k.level != paths[OutputSpeaker][i].level) {
				t.Errorf("%s: write %d is %v, was %v", board, i, k, paths[OutputSpeaker][i])
			}
		}
		// The stock paths are shared by every player: the gain is never written into them.
		if got := driverLevel(t, paths[OutputSpeaker]); got != 6 {
			t.Errorf("%s: a gained path changed the stock one to %d", board, got)
		}
	}
	if got := driverLevel(t, speakerGainControls(5)); got != 11 || len(speakerGainControls(5)) != 1 {
		t.Errorf("a live change writes %v, want the driver gain alone at 11", speakerGainControls(5))
	}
}

// Every path the player writes comes from Player.path, which carries the saved gain on the speaker
// only: at a start (route), a route change and a plug or unplug (setOutput), and a path written again.
func TestPlayerPathCarriesTheSpeakerGain(t *testing.T) {
	p := &Player{}
	for db := 0; db <= config.SpeakerGainMax; db++ {
		p.gainDB = db
		if got := driverLevel(t, p.path(OutputSpeaker)); got != int32(6+db) {
			t.Errorf("speaker at +%d dB: level %d", db, got)
		}
		if got := driverLevel(t, p.path(OutputHeadphone)); got != 11 {
			t.Errorf("jack with +%d dB set: level %d", db, got)
		}
	}
}

type pathGainWrites struct {
	names  []string
	levels []uint32
	fail   error
}

func (m *pathGainWrites) SetInt(name string, v uint32) error {
	if m.fail != nil {
		return m.fail
	}
	m.names = append(m.names, name)
	m.levels = append(m.levels, v)
	return nil
}

// A change is written at once while the speaker plays, and kept for later on the jack, before Start,
// or after a write that failed.
func TestSpeakerGainOnTheDotIsWrittenWithThePath(t *testing.T) {
	if !GainAvailable() || !GainPossible() {
		t.Fatal("the Dot's codec gain is always there")
	}
	m := &pathGainWrites{}
	held := true
	was := heldPathMixer
	heldPathMixer = func(*Player) pathGainMixer {
		if held {
			return m
		}
		return nil
	}
	t.Cleanup(func() { heldPathMixer = was })

	p := &Player{out: OutputSpeaker}
	if err := p.SetSpeakerGain(3); err != nil {
		t.Fatal(err)
	}
	if len(m.levels) != 1 || m.names[0] != driverGain || m.levels[0] != 9 || p.gainDB != 3 {
		t.Fatalf("+3 dB on the speaker: wrote %v %v, kept %d", m.names, m.levels, p.gainDB)
	}

	p.out = OutputHeadphone
	if err := p.SetSpeakerGain(4); err != nil {
		t.Fatal(err)
	}
	if len(m.levels) != 1 || p.gainDB != 4 {
		t.Fatalf("on the jack: wrote %v, kept %d", m.levels, p.gainDB)
	}
	if got := driverLevel(t, p.path(OutputSpeaker)); got != 10 {
		t.Fatalf("back on the speaker: level %d, want 10", got)
	}

	held = false
	p.out = OutputSpeaker
	if err := p.SetSpeakerGain(0); err != nil || len(m.levels) != 1 || p.gainDB != 0 {
		t.Fatalf("before Start: err %v, wrote %v, kept %d", err, m.levels, p.gainDB)
	}

	// A write that fails keeps the gain all the same, for the speaker's next path.
	held = true
	m.fail = errors.New("write failed")
	if err := p.SetSpeakerGain(6); err != nil || p.gainDB != 6 {
		t.Fatalf("a failed write: err %v, kept %d", err, p.gainDB)
	}
	if got := driverLevel(t, p.path(OutputSpeaker)); got != 12 {
		t.Fatalf("the next path after a failed write: level %d, want 12", got)
	}
	m.fail = nil
	if err := p.SetSpeakerGain(7); err == nil || len(m.levels) != 1 {
		t.Fatalf("+7 dB was taken: err %v, wrote %v", err, m.levels)
	}
}
