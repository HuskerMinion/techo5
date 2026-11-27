package subband

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Dot 2's Fire OS 6 file runs beam, microphone, band, tap. A file of that shape whose numbers
// carry their own coordinates is read back here, so a reading that puts any coefficient in the wrong
// place fails without the vendor's file.
func TestDotV2LayoutIsBeamMicBandTap(t *testing.T) {
	g := known[1]
	dir := t.TempDir()

	var win, w strings.Builder
	for n := range g.WindowLen {
		fmt.Fprintf(&win, "%g,\n", math.Sin(math.Pi*(float64(n)+0.5)/float64(g.WindowLen)))
	}
	for beam := range g.Beams {
		for mic := range g.Inputs() {
			for band := range g.Bands {
				for tap := range g.Taps {
					fmt.Fprintf(&w, "%d,\n%d,\n", beam*4+mic, band*4+tap)
				}
			}
		}
	}
	for name, text := range map[string]string{g.windowFile: win.String(), g.weightsFile: w.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for beam := range g.Beams {
		for band := range g.Bands {
			for tap := range g.Taps {
				for mic, got := range loaded.weight(band, beam, tap) {
					if want := complex(float32(beam*4+mic), float32(band*4+tap)); got != want {
						t.Fatalf("beam %d mic %d band %d tap %d read as %v, want %v", beam, mic, band, tap, got, want)
					}
				}
			}
		}
	}
}
