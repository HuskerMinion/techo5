package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The process-wide store, at layout.StatePath. There is one device and one file, so callers use the
// package functions rather than being handed a Store. Atomic because a test's Use swaps it while
// goroutines another test started may still be reading it.
var (
	once   sync.Once
	shared atomic.Pointer[loaded]
)

// loaded is the store and whatever its load reported, for callers that want to log it.
type loaded struct {
	s   *Store
	err error
}

func current() *loaded {
	once.Do(func() {
		s, err := Load(layout.StatePath)
		shared.Store(&loaded{s, err})
	})
	return shared.Load()
}

func store() *Store { return current().s }

// Get is what the device is set to.
func Get() Config { return store().Get() }

// Set names something to change. Each leaf takes the lock, applies the change and writes the file:
//
//	config.Set().Speaker().Volume(8)
func Set() Writer { return store().Set() }

// LoadError reports a problem reading the file. Config is still usable: it starts from defaults and
// the file is overwritten on the next change.
func LoadError() error { return current().err }

// Started records what echod was told at start-up.
func Started(d Device) { store().started(d) }

// Use points the process-wide store at another path. For tests.
func Use(path string) {
	once.Do(func() {})
	s, err := Load(path)
	shared.Store(&loaded{s, err})
}

// Store reads and writes one file.
type Store struct {
	path string

	mu sync.Mutex
	c  Config

	// readable is whether what is on disk was understood. It gates writing, because everything here is
	// written as one whole document: a store that fell back to defaults would replace a file it could
	// not read with a file that has nothing in it, turning a bad read into permanent loss.
	//
	// A missing file is readable. There is nothing to lose and the first change creates it.
	readable bool

	// was is the file as it was read, kept for the settings this build has no field for (unknown.go).
	was map[string]any
}

// Load reads the file over the defaults, so a key the file does not mention keeps the value it was
// built with.
//
// A missing file is not an error: echod runs with defaults and writes it on the first change. An
// unreadable or unparseable one is — the caller may carry on with defaults, but nothing will be
// saved over it until someone has looked.
func Load(path string) (*Store, error) {
	st := &Store{path: path, c: Defaults()}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			st.c.Speaker.SoundsMoved = true // a new device starts on the new default: nothing to move
			st.c.rescaleNightLight()        // nor a night clock level to put on the new scale
			st.readable = true
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st.c); err != nil {
		return st, fmt.Errorf("config: %s: %w", path, err)
	}
	_ = json.Unmarshal(b, &st.was)
	st.c.moveSounds()
	st.c.rescaleNightLight()

	st.readable = true
	return st, nil
}

// Get returns a copy. The wake words and alarm lists are copied too, or a caller holding the snapshot would be
// holding the store's own slice.
func (st *Store) Get() Config {
	st.mu.Lock()
	defer st.mu.Unlock()

	c := st.c
	c.Wake.Words = slices.Clone(st.c.Wake.Words)
	c.Alarms.List = slices.Clone(st.c.Alarms.List)
	for i := range c.Alarms.List {
		c.Alarms.List[i].RingOn = slices.Clone(c.Alarms.List[i].RingOn)
	}
	c.Alarms.Follow = slices.Clone(st.c.Alarms.Follow)
	c.Alarms.Snoozed = slices.Clone(st.c.Alarms.Snoozed)
	c.Alarms.Skipped = slices.Clone(st.c.Alarms.Skipped)
	if h := st.c.Alarms.Held; h != nil {
		held := *h
		c.Alarms.Held = &held
	}
	c.Timers.Local = slices.Clone(st.c.Timers.Local)
	c.Screen.WorldClocks = slices.Clone(st.c.Screen.WorldClocks)
	c.Deck = st.c.Deck.clone()
	c.Video = st.c.Video.clone()
	c.Share = st.c.Share.clone()
	return c
}

// Set names something to change in this store.
func (st *Store) Set() Writer { return Writer{st: st} }

func (st *Store) started(d Device) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.c.Device = d
}

// Update applies a change and writes the file. A change the file could not take still holds in
// memory, and goes to disk with the next change that is saved: what callers count on for a value they
// make up once and must keep (the DLNA renderer's id), whether or not it could be saved.
func (st *Store) Update(f func(*Config)) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	f(&st.c)
	return st.write()
}

// UpdateOrUndo is Update for a setting that must not hold unless it was saved: one the file could not
// take is taken back whole, so the next change saved cannot write it behind everyone's back. Keep wake
// clips is one: turned on while the flash was failing, the recorder stays off, and the setting must
// too. A file that was never readable is the exception, since nothing is written over it all run: the
// change holds until the next start, as Update's do.
func (st *Store) UpdateOrUndo(f func(*Config)) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	before := deepCopy(st.c)
	f(&st.c)
	err := st.write()
	var renamed afterRename
	switch {
	case err == nil, !st.readable:
	case errors.As(err, &renamed):
		// The new file is in place, only not yet flushed with its folder: the disk has the change, and
		// memory keeps it to match. Saved, as far as the caller goes, so what it does for the setting
		// (the recorder Keep wake clips turns on) matches both.
		slog.Warn("config: saved, but the folder could not be flushed after", "path", st.path, "err", err)
		return nil
	default:
		st.c = before
	}
	return err
}

// UpdateChecked applies a change and writes the file only when f accepts it. A change f refuses, or
// one that could not be saved, is taken back whole — even when the file was never readable. Unlike
// UpdateOrUndo there is no run-long exception for an unreadable file, because this is for something
// the caller reports as done only once it is on disk, and memory alone must not be able to say so.
//
// f may change st.c before returning an error; the change is taken back either way.
func (st *Store) UpdateChecked(f func(*Config) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	before := deepCopy(st.c)
	if err := f(&st.c); err != nil {
		st.c = before
		return err
	}
	err := st.write()
	var renamed afterRename
	switch {
	case err == nil:
		return nil
	case errors.As(err, &renamed):
		// As UpdateOrUndo: the new file is in place, only not yet flushed with its folder. Memory
		// keeps the change to match what is on disk, and the caller is told it was saved.
		slog.Warn("config: saved, but the folder could not be flushed after", "path", st.path, "err", err)
		return nil
	default:
		st.c = before
		return err
	}
}

// afterRename is write failing after the new file took the old one's place: what is on disk is the
// change, though a power cut could still lose it.
type afterRename struct{ err error }

func (e afterRename) Error() string { return e.err.Error() }
func (e afterRename) Unwrap() error { return e.err }

// write replaces the file through a temporary one, flushed at every step, because the thing this has
// to survive is a reboot rather than a crash.
//
// A rename is atomic, which is enough for being killed: either the old file or the new one is there.
// It is not enough for losing power. The rename can reach the disk while the data behind it has not,
// and what comes back is the new name with nothing in it — which is how a device that had settings
// comes up with none. So the contents are flushed before the rename and the directory after it.
func (st *Store) write() error {
	if !st.readable {
		return fmt.Errorf("config: %s was not readable, refusing to write over it", st.path)
	}

	b, err := withUnknown(st.c, st.was)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := st.path + ".tmp"
	// Owner-only: the file holds passwords and tokens (the Reolink recorder's, Music Assistant's, the
	// cameras' for talking through them, a go2rtc server's). Set on the file itself too, since one left from an older
	// build keeps the mode it was made with.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
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

	if err := os.Rename(tmp, st.path); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		return nil // directories cannot be synced there, and the rename is already durable enough
	}
	d, err := os.Open(dir)
	if err != nil {
		return afterRename{err}
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return afterRename{err}
	}
	return nil
}
