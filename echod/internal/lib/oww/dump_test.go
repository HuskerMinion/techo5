package oww

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// TestDumpEmbeddings writes the embeddings this engine produces for a fixed, synthetic input, so
// that the training pipeline outside this repo can be checked against the exact front end the device
// runs. A classifier trained on embeddings that differ from these is one the device will score
// differently from however it scored in training, and nothing downstream would say so.
//
// Set DUMP_AUDIO and DUMP_FEATS to run it.
func TestDumpEmbeddings(t *testing.T) {
	audioPath, featPath := os.Getenv("DUMP_AUDIO"), os.Getenv("DUMP_FEATS")
	if audioPath == "" || featPath == "" {
		t.Skip("set DUMP_AUDIO and DUMP_FEATS")
	}

	// Two seconds of something with structure at speech frequencies, deterministic so both sides
	// see identical samples: a chirp plus a couple of tones, at speech-like amplitudes.
	const n = SampleRate * 2
	pcm := make([]int16, n)
	for i := range pcm {
		f := float64(i) / SampleRate
		v := 3000*math.Sin(2*math.Pi*(200+300*f)*f) +
			1200*math.Sin(2*math.Pi*1100*f) +
			400*math.Sin(2*math.Pi*2700*f)
		pcm[i] = int16(v)
	}

	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// One classifier's worth of history is not enough; keep everything.
	e.maxFrames = 1 << 20

	if _, err := e.Process(pcm); err != nil {
		t.Fatal(err)
	}

	af, err := os.Create(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	defer af.Close()
	if err := binary.Write(af, binary.LittleEndian, pcm); err != nil {
		t.Fatal(err)
	}

	ff, err := os.Create(featPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ff.Close()
	if err := binary.Write(ff, binary.LittleEndian, e.feats); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d samples and %d embeddings (%d floats)", len(pcm), len(e.feats)/embedDims, len(e.feats))
}
