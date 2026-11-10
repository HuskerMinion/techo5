package speaker

import (
	"path/filepath"
	"testing"
)

func TestIntercomRingSamplesAreCopied(t *testing.T) {
	clip := &Clip{made: true}

	clip.samples = []int16{100, 200, 300}
	clip.own = true

	samples := clip.OwnSamples()

	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}

	samples[0] = 999

	if clip.samples[0] != 100 {
		t.Fatal("OwnSamples modified the original samples")
	}
}

func TestIntercomRingPreviewWithoutWAV(t *testing.T) {
	ownSounds(t)

	if err := PreviewOwn("intercom_ring"); err == nil {
		t.Fatal("expected an error when no custom ringtone exists")
	}
}

func TestIntercomRingPreviewWithWAV(t *testing.T) {
	dir := ownSounds(t)

	writeWAV(t,
		filepath.Join(dir, "intercom_ring.wav"),
		Rate, 1, 300,
	)

	if !ClipIntercomRing.Own() {
		t.Fatal("custom intercom ringtone was not loaded")
	}

	if ClipIntercomRing.Ms() != 300 {
		t.Fatalf("expected 300 ms, got %d", ClipIntercomRing.Ms())
	}

	samples := ClipIntercomRing.render(previewLevel)

	if len(samples) != Rate*300/1000*Channels {
		t.Fatalf("unexpected preview sample count: %d", len(samples))
	}

	if samples[len(samples)/2] == 0 {
		t.Fatal("preview audio is silent")
	}
}
