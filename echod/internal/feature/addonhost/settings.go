package addonhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's settings are its own, not the device's: kept beside it as settings.json, root's and 0600,
// not in state.json, not in Home Assistant, and not in the device's backup or diagnostics. They are
// written only when Save changes something, and kept with the state folder when the add-on is removed
// with its box unticked (feature/addons, Remove), so installing it again finds them.

// settingsFile is an add-on's settings' name in its folder.
const settingsFile = "settings.json"

// settingsStore reads and writes the files, one at a time.
type settingsStore struct {
	root func() string // the add-ons' folder (addons.Root)
	mu   sync.Mutex
}

func (s *settingsStore) path(id string) (string, error) {
	if !catalog.ValidID(id) {
		return "", fmt.Errorf("addonhost: %q is not an add-on", id)
	}
	return filepath.Join(s.root(), id, settingsFile), nil
}

// load is the add-on's settings as saved, holding only what its schema declares and in the shape each
// field takes: what an older version saved under a key since dropped, or of a type since changed, is
// left out rather than handed on.
func (s *settingsStore) load(id string, schema *addonapi.Settings) map[string]any {
	out := map[string]any{}
	if schema == nil {
		return out
	}
	p, err := s.path(id)
	if err != nil {
		return out
	}
	s.mu.Lock()
	b, err := os.ReadFile(p)
	s.mu.Unlock()
	if err != nil {
		return out
	}
	var saved map[string]any
	if json.Unmarshal(b, &saved) != nil {
		return out
	}
	for _, f := range schema.Fields {
		if v, ok := saved[f.Key]; ok && f.Check(v) == nil {
			out[f.Key] = v
		}
	}
	return out
}

// save writes values when they differ from what is saved, and says whether they did. The file goes in
// whole, through a temporary one renamed over it, so a power cut leaves the old settings or the new.
func (s *settingsStore) save(id string, values map[string]any) (bool, error) {
	p, err := s.path(id)
	if err != nil {
		return false, err
	}
	b, err := json.Marshal(values) // a map's keys are written sorted, so the same values are the same bytes
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, b) {
		return false, nil
	}
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		return false, errors.New("addonhost: the add-on is not installed")
	}
	tmp := p + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	_, werr := f.Write(b)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	// 0600 whatever the umask was, and root's: the add-on is told its settings over the API, and has no
	// business reading another add-on's, or this file behind the daemon's back.
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// Form fields: a setting's input is named set_<key>, a secret's secret_<key> (which the setup page
// never keeps in a refused form, setup's stash), and a secret's Clear box clear_<key>.
const (
	FieldPrefix  = "set_"
	SecretPrefix = "secret_"
	ClearPrefix  = "clear_"
)

// fromForm reads the settings in a form against the schema, over saved: every field of its type and
// within its bounds, a box left unticked as false, and a secret left empty as the one saved (it is
// never shown back, so an empty field means "as it is"), unless its Clear box is ticked.
func fromForm(schema *addonapi.Settings, form func(string) string, saved map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, f := range schema.Fields {
		raw := form(FieldPrefix + f.Key)
		var v any
		switch f.Type {
		case addonapi.TypeSecret:
			raw = form(SecretPrefix + f.Key)
			switch {
			case form(ClearPrefix+f.Key) == "yes":
				v = ""
			case raw == "":
				if old, ok := saved[f.Key]; ok {
					v = old
				} else {
					v = ""
				}
			default:
				v = raw
			}
		case addonapi.TypeBool:
			v = raw == "yes" || raw == "on"
		case addonapi.TypeNumber:
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue // left out, as never set
			}
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("%s is a number", f.Label)
			}
			v = n
		case addonapi.TypeString:
			v = raw
		default: // select, entity
			v = strings.TrimSpace(raw)
			if v == "" {
				continue
			}
		}
		if err := f.Check(v); err != nil {
			return nil, err
		}
		out[f.Key] = v
	}
	return out, nil
}
