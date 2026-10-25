package tessera

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// The layout in force is kept on disk, so a restart does not leave the screen empty until the add-on
// notices: the add-on starts a session again after one, finds the screen holds the revision it was going
// to send, and sends only the tiles' states. It is written when a layout comes into force and when its
// titles change, never for a state: states change all day, and the add-on sends every one again after a
// restart anyway.

// storeVersion is the shape of the file. A file of another version is not read.
const storeVersion = 1

type stored struct {
	Version int     `json:"version"`
	Layout  *Layout `json:"layout"`
}

// Save writes l to path, through a temporary file so a reader never finds half of it. The file is
// flushed before the rename and the folder after it, as the settings are (config/store.go): otherwise a
// power cut soon after can leave the new name with nothing in it.
func Save(path string, l *Layout) error {
	if l == nil {
		return errors.New("no layout")
	}
	b, err := json.Marshal(stored{Version: storeVersion, Layout: l})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil // folders cannot be synced there
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Load reads the layout saved at path: nil and no error when there is none.
func Load(path string) (*Layout, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s stored
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Version != storeVersion {
		return nil, fmt.Errorf("layout file version %d, want %d", s.Version, storeVersion)
	}
	return s.Layout, nil
}
