package media

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	esphome "github.com/ygelfand/go-esphome-device"
)

func TestHASpeakerGainRejectsNonIntegerAndNonFiniteValues(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	writes := 0
	p := &Player{gain: &esphome.Number{}, gainApply: func(int) error { writes++; return nil }}
	for _, v := range []float32{-1, 7, 10, 1.5, float32(math.NaN()), float32(math.Inf(1))} {
		p.commandSpeakerGain(v)
	}
	if writes != 0 || p.SpeakerGain() != 0 {
		t.Fatal("invalid HA value reached the amplifier")
	}
	p.commandSpeakerGain(5)
	if writes != 1 || p.SpeakerGain() != 5 || config.Get().Speaker.GainDB != 5 {
		t.Fatal("valid HA setting was not applied and saved")
	}
}

func TestSpeakerGainSavesAndRestoresDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	config.Use(path)
	var applied int
	p := &Player{gain: &esphome.Number{}, gainApply: func(v int) error { applied = v; return nil }}
	for _, db := range []int{5, 6, 0} {
		if err := p.SetSpeakerGain(db); err != nil {
			t.Fatal(err)
		}
		st, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Get().Speaker.GainDB != db || p.SpeakerGain() != db || applied != db {
			t.Fatalf("gain did not agree after reload for %d dB", db)
		}
	}
	for _, db := range []int{-1, 7, 10} {
		if err := p.SetSpeakerGain(db); err == nil {
			t.Fatalf("accepted %d", db)
		}
	}
	if applied != 0 {
		t.Fatal("invalid input reached the amplifier")
	}
	if err := (&Player{}).SetSpeakerGain(5); err == nil {
		t.Fatal("unsupported player accepted gain")
	}
}

func TestSpeakerGainDoesNotPublishOrSaveFailedWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	config.Use(path)
	p := &Player{gain: &esphome.Number{}, gainApply: func(int) error { return errors.New("amplifier failed") }}
	if err := p.SetSpeakerGain(5); err == nil {
		t.Fatal("write failure ignored")
	}
	if p.SpeakerGain() != 0 || config.Get().Speaker.GainDB != 0 {
		t.Fatal("failed gain was published or saved")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed amplifier write changed saved state")
	}
}

func TestSpeakerGainUndoesWriteWhenSaveFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	config.Use(path)
	var applied []int
	p := &Player{gain: &esphome.Number{}, gainApply: func(v int) error { applied = append(applied, v); return nil }}
	if err := p.SetSpeakerGain(5); err == nil {
		t.Fatal("save failure ignored")
	}
	if len(applied) != 2 || applied[0] != 5 || applied[1] != 0 || p.SpeakerGain() != 0 {
		t.Fatalf("failed save did not restore amplifier: %v", applied)
	}
}
