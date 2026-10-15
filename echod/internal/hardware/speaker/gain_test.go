package speaker

import (
	"context"
	"errors"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

type testGainMixer struct {
	control alsa.Control
	missing bool
	fail    error
	writes  []uint32
}

func (m *testGainMixer) Find(name string) (alsa.Control, error) {
	if m.missing || name != gainControl {
		return alsa.Control{}, errors.New("control absent")
	}
	return m.control, nil
}

func (m *testGainMixer) Set(_ alsa.Control, v uint32) error {
	if m.fail != nil {
		return m.fail
	}
	m.writes = append(m.writes, v)
	return nil
}

func gainTestMixer() *testGainMixer {
	return &testGainMixer{control: alsa.Control{Name: gainControl, Type: alsa.TypeInteger,
		Count: 1, Min: 0, Max: 158, Step: 1}}
}

func TestSpeakerGainWritesDBAndCanReset(t *testing.T) {
	m := gainTestMixer()
	for _, db := range []int{0, 5, 6, 0} {
		if err := writeSpeakerGain(m, db); err != nil {
			t.Fatal(err)
		}
		if got := m.writes[len(m.writes)-1]; got != uint32(110+db) {
			t.Fatalf("%d dB wrote %d", db, got)
		}
	}
	for _, db := range []int{-1, 7, 10, 158} {
		if err := writeSpeakerGain(m, db); err == nil {
			t.Fatalf("accepted %d dB", db)
		}
	}
	if len(m.writes) != 4 {
		t.Fatal("invalid gain reached hardware")
	}
}

func TestSpeakerGainRejectsUnsupportedControls(t *testing.T) {
	for _, change := range []func(*testGainMixer){
		func(m *testGainMixer) { m.missing = true },
		func(m *testGainMixer) { m.control.Type = alsa.TypeEnumerated },
		func(m *testGainMixer) { m.control.Count = 2 },
		func(m *testGainMixer) { m.control.Max = 115 },
		func(m *testGainMixer) { m.control.Min = 111 },
		func(m *testGainMixer) { m.control.Step = 2 },
	} {
		m := gainTestMixer()
		change(m)
		if err := writeSpeakerGain(m, 5); err == nil {
			t.Fatal("unsupported control accepted")
		}
		if len(m.writes) != 0 {
			t.Fatal("unsupported control written")
		}
	}
	m := gainTestMixer()
	m.fail = errors.New("write failed")
	if err := writeSpeakerGain(m, 5); !errors.Is(err, m.fail) {
		t.Fatalf("lost write failure: %v", err)
	}
}

func TestSpeakerGainIsQueuedBeforeStart(t *testing.T) {
	was := GainAvailable
	GainAvailable = func() bool { return true }
	t.Cleanup(func() { GainAvailable = was })
	p := &Player{}
	if err := p.SetSpeakerGain(5); err != nil {
		t.Fatal(err)
	}
	if p.gainDB != 5 {
		t.Fatal("gain was not remembered before the mixer opens")
	}
	if err := p.SetSpeakerGain(7); err == nil || p.gainDB != 5 {
		t.Fatal("bad gain changed the queued value")
	}
}

// A gain restore failure must reach the normal playback loop rather than fail the
// service and repeatedly restart it. Cancel at the first wait after mixer setup;
// an unopened mixer forces the same error branch as a failed amplifier write.
func TestSpeakerRunContinuesAfterGainRestoreFails(t *testing.T) {
	was := GainAvailable
	GainAvailable = func() bool { return true }
	t.Cleanup(func() { GainAvailable = was })
	for _, db := range []int{0, 5} {
		p := New()
		p.pb = &alsa.Playback{}
		p.gainDB = db
		if err := p.applySpeakerGain(); err == nil {
			t.Fatal("test did not force a gain restore failure")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := p.Run(ctx); err != nil {
			t.Fatalf("gain restore at %d dB stopped playback: %v", db, err)
		}
	}
}
