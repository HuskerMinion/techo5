package asp

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// AFE.cfg names the music volume table music_volTab on the 2nd gen Show 8 and only AVL_music_volTab on
// the 2nd gen Show 5; comments and the rest of the file are ignored, and a file without one gives nil.
func TestReadVolumeTable(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  string
		want []float64
	}{
		"athena": {`{ "Playback": { // the vendor's chain
			"music_volTab" : [ -200, -70, -2, 0 ], "AVL_music_volTab" : [ -1 ] } }`, []float64{-200, -70, -2, 0}},
		"cronos": {`{ "AVL": { "AVL_music_volTab" : [ -200, /* quiet */ -13.5, 0 ] } }`, []float64{-200, -13.5, 0}},
		"none":   {`{ "Playback": {} }`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "AFE.cfg")
			if err := os.WriteFile(path, []byte(tc.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readVolumeTable(path); !slices.Equal(got, tc.want) {
				t.Errorf("readVolumeTable = %v, want %v", got, tc.want)
			}
		})
	}
	if got := readVolumeTable(filepath.Join(t.TempDir(), "missing.cfg")); got != nil {
		t.Errorf("missing file: %v", got)
	}
}
