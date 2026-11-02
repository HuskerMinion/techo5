package speaker

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// A sound pack is every recording of the owner's own in one file, to keep or to put on another device:
// a zip of the sounds' .wav files and a small manifest. It holds sounds and nothing else - no settings,
// no names, nothing private - and loading one accepts nothing else either.

const (
	// PackExt is the extension a pack is saved with.
	PackExt = ".soundpack"
	// MaxPack is the most a pack may be, on the wire and unpacked: every sound at its longest, and
	// room besides. It is held in memory whole to be checked.
	MaxPack = 12 << 20

	packFormat   = "techo5-soundpack"
	packVersion  = 1
	packManifest = "manifest.json"
)

type packInfo struct {
	Format  string   `json:"format"`
	Version int      `json:"version"`
	Sounds  []string `json:"sounds"`
}

// PackSounds writes the owner's recordings as a pack. It errors when there are none to save.
func PackSounds(w io.Writer) error {
	zw := zip.NewWriter(w)
	info := packInfo{Format: packFormat, Version: packVersion, Sounds: []string{}}
	files := map[string][]byte{}
	for _, c := range ownable {
		b, err := os.ReadFile(c.ownPath())
		if err != nil || len(b) > MaxOwnUpload || CheckOwn(c.file, b) != nil {
			continue // not there, or one the device would not play anyway
		}
		info.Sounds = append(info.Sounds, c.file)
		files[c.file] = b
	}
	if len(info.Sounds) == 0 {
		return errors.New("there are no sounds of your own to save")
	}
	m, _ := json.Marshal(info)
	for _, f := range append([]string{packManifest}, info.Sounds...) {
		name, b := f, m
		if f != packManifest {
			name, b = f+".wav", files[f]
		}
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := fw.Write(b); err != nil {
			return err
		}
	}
	return zw.Close()
}

// LoadPack installs the sounds in a pack and reports which. Every entry is checked before any is kept:
// a pack holds the manifest and sounds on the list, each a file the device would play for that sound,
// and nothing else - no folders, no other names, no paths. Sounds not in the pack stay as they are.
func LoadPack(b []byte) ([]string, error) {
	if len(b) > MaxPack {
		return nil, fmt.Errorf("that file is too big for a sound pack: the most is %d MB", MaxPack>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, errors.New("that is not a sound pack")
	}
	var total uint64
	got := map[*Clip][]byte{}
	var order []*Clip
	sawManifest := false
	for _, e := range zr.File {
		total += e.UncompressedSize64
		if total > MaxPack || e.UncompressedSize64 > MaxOwnUpload {
			return nil, errors.New("the pack holds more than a sound pack can")
		}
		if e.Name == packManifest {
			data, err := readEntry(e)
			if err != nil {
				return nil, err
			}
			var info packInfo
			if json.Unmarshal(data, &info) != nil || info.Format != packFormat || info.Version != packVersion {
				return nil, errors.New("that is not a sound pack this device reads")
			}
			sawManifest = true
			continue
		}
		name, ok := strings.CutSuffix(e.Name, ".wav")
		c := ownClip(name)
		if !ok || c == nil || e.FileInfo().IsDir() {
			return nil, fmt.Errorf("the pack holds %q, which is not one of the sounds", printable(e.Name))
		}
		if got[c] != nil {
			return nil, fmt.Errorf("the pack holds %s twice", name)
		}
		data, err := readEntry(e)
		if err != nil {
			return nil, err
		}
		if err := CheckOwn(name, data); err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		got[c] = data
		order = append(order, c)
	}
	if !sawManifest {
		return nil, errors.New("that is not a sound pack: it has no manifest")
	}
	if len(order) == 0 {
		return nil, errors.New("the pack holds no sounds")
	}
	if err := os.MkdirAll(SoundsDir, 0o755); err != nil {
		return nil, err
	}
	var names []string
	for _, c := range order {
		if err := keepOwn(c, got[c]); err != nil {
			return names, err
		}
		names = append(names, c.file)
	}
	return names, nil
}

// readEntry reads one entry, never past the most a sound may be whatever its header claims.
func readEntry(e *zip.File) ([]byte, error) {
	rc, err := e.Open()
	if err != nil {
		return nil, errors.New("the pack is damaged")
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, MaxOwnUpload+1))
	if err != nil || len(b) > MaxOwnUpload {
		return nil, errors.New("the pack is damaged or holds a file bigger than a sound can be")
	}
	return b, nil
}

// printable is a name from a pack cut short and cleaned of anything that would not show.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return '?'
		}
		return r
	}, s)
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return s
}
