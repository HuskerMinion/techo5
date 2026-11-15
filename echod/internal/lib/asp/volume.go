package asp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// VolumeTable is the unit's own music volume curve, in dB per step from mute to full, as its AFE.cfg
// gives it to the vendor's chain in front of the EQ and compressor; nil when AFE.cfg has none.
func (t *Tuning) VolumeTable() []float64 { return readVolumeTable(filepath.Join(t.dir, "AFE.cfg")) }

// volumeKeys are the names AFE.cfg gives the music volume table: the 2nd gen Show 8's "Athena" has
// music_volTab (and an identical AVL_music_volTab), the 2nd gen Show 5's "Cronos" only the AVL one.
var volumeKeys = []string{"music_volTab", "AVL_music_volTab"}

// readVolumeTable reads the music volume table out of AFE.cfg: Fire OS turns the music down by it, in
// front of the EQ and compressor, and its last step is 0 dB. A missing file or table is nil.
func readVolumeTable(path string) []float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	b = stripComments(b)
	for _, key := range volumeKeys {
		at := strings.Index(string(b), `"`+key+`"`)
		if at < 0 {
			continue
		}
		rest := string(b[at+len(key)+2:])
		open, end := strings.Index(rest, "["), strings.Index(rest, "]")
		if open < 0 || end < open {
			continue
		}
		var table []float64
		if err := json.Unmarshal([]byte(rest[open:end+1]), &table); err == nil && len(table) > 1 {
			for i, db := range table {
				table[i] = min(db, 0) // a step is the vendor's chain turned down, never up
			}
			return table
		}
	}
	return nil
}
