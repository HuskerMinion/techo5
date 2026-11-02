package media

import (
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func laneTestPlayer(t *testing.T) *Player {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	return &Player{
		mp:       &esphome.MediaPlayer{},
		voiceVol: newLaneOffset("voice_volume", "Voice volume", "mdi:account-voice"),
		callVol:  newLaneOffset("call_volume", "Call volume", "mdi:phone-in-talk"),
	}
}

// The offsets are saved, clamped to the range of steps, and show in their entities; a device that never
// set them has none saved.
func TestVoiceAndCallVolumesAreSavedAndRestored(t *testing.T) {
	p := laneTestPlayer(t)
	if s := config.Get().Speaker; s.VoiceOffset != 0 || s.CallOffset != 0 {
		t.Fatalf("defaults are %d and %d, want 0 and 0", s.VoiceOffset, s.CallOffset)
	}
	p.SetVoiceOffset(-7)
	p.SetCallOffset(99)
	if s := config.Get().Speaker; s.VoiceOffset != -7 || s.CallOffset != config.VolumeSteps {
		t.Fatalf("saved %d and %d, want -7 and %d", s.VoiceOffset, s.CallOffset, config.VolumeSteps)
	}
	if p.voiceVol.Get() != -7 || p.callVol.Get() != float32(config.VolumeSteps) {
		t.Errorf("entities read %v and %v", p.voiceVol.Get(), p.callVol.Get())
	}

	// A restart: new entities, read back from what was saved.
	q := laneTestPlayer2(t, config.Get())
	if q.voiceVol.Get() != -7 || q.callVol.Get() != float32(config.VolumeSteps) {
		t.Errorf("restored entities read %v and %v", q.voiceVol.Get(), q.callVol.Get())
	}
}

// laneTestPlayer2 restores the two entities from a saved config the way Restore does.
func laneTestPlayer2(t *testing.T, c config.Config) *Player {
	t.Helper()
	q := &Player{
		voiceVol: newLaneOffset("voice_volume", "Voice volume", "mdi:account-voice"),
		callVol:  newLaneOffset("call_volume", "Call volume", "mdi:phone-in-talk"),
	}
	q.voiceVol.Set(float32(c.Speaker.VoiceOffset))
	q.callVol.Set(float32(c.Speaker.CallOffset))
	q.pushLanes()
	return q
}

// The entities are in the config category and take a number either side of zero.
func TestVoiceAndCallVolumeEntities(t *testing.T) {
	p := laneTestPlayer(t)
	for _, n := range []*esphome.Number{p.voiceVol, p.callVol} {
		if n.Category != esphome.CategoryConfig || n.Min != -config.VolumeSteps || n.Max != config.VolumeSteps {
			t.Errorf("%s: category %v, range %v to %v", n.ObjectID, n.Category, n.Min, n.Max)
		}
	}
	p.voiceVol.OnCommand = func(v float32) { p.SetVoiceOffset(int(v)) }
	p.voiceVol.OnCommand(-3)
	if config.Get().Speaker.VoiceOffset != -3 {
		t.Error("the command did not save")
	}
}
