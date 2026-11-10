package phone

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestDownsampleIntercom(t *testing.T) {
	// Générer une seconde de son à 48 kHz.
	input := make([]int16, speaker.Rate)

	// Signal sinusoïdal à 1 000 Hz.
	for i := range input {
		phase := 2 * math.Pi * 1000 * float64(i) / speaker.Rate
		input[i] = int16(10000 * math.Sin(phase))
	}

	output := downsampleIntercom(input)

	// Une seconde à 16 kHz contient 16 000 échantillons.
	if len(output) != speaker.VoiceRate {
		t.Fatalf("expected %d samples, got %d",
			speaker.VoiceRate, len(output))
	}

	// Vérifier que le signal n'est pas devenu silencieux.
	var peak int16
	for _, sample := range output {
		if sample > peak {
			peak = sample
		}
	}

	if peak < 8000 {
		t.Fatalf("signal too weak after conversion: peak=%d", peak)
	}
}

func TestDownsampleIntercomFiltersHighFrequencies(t *testing.T) {
	// Générer un signal à une fréquence donnée.
	generate := func(hz float64) []int16 {
		samples := make([]int16, speaker.Rate)

		for i := range samples {
			phase := 2 * math.Pi * hz * float64(i) / speaker.Rate
			samples[i] = int16(10000 * math.Sin(phase))
		}

		return samples
	}

	// Mesurer l'amplitude maximale en ignorant le début du signal.
	peak := func(samples []int16) int {
		var maximum int

		for _, sample := range samples[100:] {
			value := int(sample)
			if value < 0 {
				value = -value
			}
			if value > maximum {
				maximum = value
			}
		}

		return maximum
	}

	low := downsampleIntercom(generate(1000))
	high := downsampleIntercom(generate(12000))

	lowPeak := peak(low)
	highPeak := peak(high)

	if lowPeak < 8000 {
		t.Fatalf("low frequency attenuated too much: %d", lowPeak)
	}

	if highPeak > 500 {
		t.Fatalf("high frequency insufficiently filtered: %d", highPeak)
	}
}

func TestIntercomRingToneFallback(t *testing.T) {
	if speaker.ClipIntercomRing.Own() {
		t.Skip("custom ringtone already installed")
	}

	got := intercomRingTone()
	want := ringTone()

	if !slices.Equal(got, want) {
		t.Fatal("intercom ringtone did not fall back to standard ring")
	}
}

func TestIntercomRingToneCustomWAV(t *testing.T) {
	// Isoler les fichiers WAV de ce test.
	oldDir := speaker.SoundsDir
	speaker.SoundsDir = t.TempDir()
	t.Cleanup(func() {
		speaker.SoundsDir = oldDir
		speaker.ClipIntercomRing.Own()
	})

	// Créer un WAV mono, 16 bits, 48 kHz, de 200 ms.
	const durationMs = 200
	frames := speaker.Rate * durationMs / 1000

	var wav bytes.Buffer

	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+frames*2))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint32(speaker.Rate))
	binary.Write(&wav, binary.LittleEndian, uint32(speaker.Rate*2))
	binary.Write(&wav, binary.LittleEndian, uint16(2))
	binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(frames*2))

	for i := 0; i < frames; i++ {
		binary.Write(&wav, binary.LittleEndian, int16(8000))
	}

	// Enregistrer le WAV avec le mécanisme de TECHO5.
	if err := speaker.SaveOwn("intercom_ring", bytes.NewReader(wav.Bytes())); err != nil {
		t.Fatal(err)
	}

	// Vérifier que le WAV personnalisé est reconnu.
	if !speaker.ClipIntercomRing.Own() {
		t.Fatal("custom intercom ringtone was not loaded")
	}

	// Récupérer la sonnerie convertie à 16 kHz.
	tone := intercomRingTone()

	expected := speaker.VoiceRate * durationMs / 1000

	if len(tone) != expected {
		t.Fatalf("expected %d samples, got %d", expected, len(tone))
	}

	// Vérifier que la sonnerie contient du son.
	if tone[len(tone)/2] < 7000 {
		t.Fatalf("custom ringtone too quiet: %d", tone[len(tone)/2])
	}
}
