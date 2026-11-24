package addons

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// catalogURL is where the signed catalog is published on its own (tools/sign-catalog.ps1): a release
// of the project's that is not a firmware release, so a listing does not wait for one. The signature
// is beside it, catalog.json.sig. Compiled in, as the update channels are: nothing on the network can
// point a device at another list.
const catalogURL = "https://github.com/HuskerMinion/techo5/releases/download/addons/catalog.json"

// maxCatalog is a sanity bound on the file: an entry is a kilobyte or two.
const maxCatalog = 256 << 10

// fetchSigned and verifySigned are the update package's, variables so a test can serve a catalog
// signed with a key of its own.
var (
	fetchSigned  = update.FetchSignedCatalog
	verifySigned = update.VerifyCatalog
)

// Fetching again: catalogEvery after one that worked; after one that did not, a minute, then twice as
// long each time up to catalogEvery. Until the catalog is first published, and on a device that is
// offline, every fetch fails, and every minute for ever would be a request and a warning a minute.
var (
	catalogEvery = 6 * time.Hour
	catalogRetry = time.Minute
)

// catalogStore is the catalog this device last believed, kept beside the add-ons with its signature
// so the card has something to show before the first fetch, or with no internet. What is kept is
// checked again when it is read.
//
// The highest serial ever believed is kept in a file of its own (catalog-serial), apart from the
// catalog: a kept catalog replaced on the storage by an older one, signed as well as today's, would
// otherwise set the bar back to its own serial.
type catalogStore struct {
	dir   string
	clock clock

	mu      sync.Mutex
	cat     catalog.Catalog
	body    []byte
	have    bool
	highest int64
	fetched time.Time // when the last fetch succeeded
	problem string    // why the last fetch did not, for the card
	logged  string    // the failure last logged, so the same one is logged once
	next    time.Time // when to fetch again
	wait    time.Duration
}

func (s *catalogStore) paths() (body, sig, serial string) {
	return filepath.Join(s.dir, "catalog.json"), filepath.Join(s.dir, "catalog.json.sig"), filepath.Join(s.dir, "catalog-serial")
}

// load reads the highest serial seen and the kept catalog, if there is one, it still verifies, and it
// is not older than that serial.
func (s *catalogStore) load() {
	bp, sp, np := s.paths()
	if b, err := os.ReadFile(np); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			s.mu.Lock()
			s.highest = n
			s.mu.Unlock()
		}
	}
	body, err1 := os.ReadFile(bp)
	sig, err2 := os.ReadFile(sp)
	if err1 != nil || err2 != nil {
		return
	}
	c, err := s.check(body, sig)
	if err == nil && c.Serial < s.highestSeen() {
		err = fmt.Errorf("%w (serial %d, have seen %d)", errOlder, c.Serial, s.highestSeen())
	}
	if err != nil {
		slog.Warn("addons: the kept catalog is not believed; waiting for a fresh one", "err", err)
		return
	}
	s.mu.Lock()
	s.cat, s.body, s.have = c, body, true
	s.mu.Unlock()
	s.raise(c.Serial)
}

func (s *catalogStore) highestSeen() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.highest
}

// raise keeps n as the highest serial seen, when it is.
func (s *catalogStore) raise(n int64) {
	s.mu.Lock()
	if n <= s.highest {
		s.mu.Unlock()
		return
	}
	s.highest = n
	s.mu.Unlock()
	_, _, np := s.paths()
	if err := os.MkdirAll(s.dir, 0o755); err == nil {
		if err := writeWhole(np, []byte(strconv.FormatInt(n, 10)+"\n"), 0o644); err != nil {
			slog.Warn("addons: keeping the catalog's serial", "err", err)
		}
	}
}

// check is a catalog's signature and its contents.
func (s *catalogStore) check(body, sig []byte) (catalog.Catalog, error) {
	if err := verifySigned(body, sig); err != nil {
		return catalog.Catalog{}, err
	}
	return catalog.Parse(body)
}

// errOlder is a catalog older than one already believed.
var errOlder = errors.New("addons: the catalog served is older than one this device has believed")

// due is whether it is time to fetch again.
func (s *catalogStore) due() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.clock.Now().Before(s.next)
}

// refresh fetches the catalog, and keeps it when it is signed, reads as a catalog, and is no older than
// the highest serial believed: an old catalog is signed as well as today's, and taken again it would
// bring back a version withdrawn since. It says whether anything the card or Home Assistant shows
// changed: the catalog, or why the last fetch failed.
func (s *catalogStore) refresh(ctx context.Context) (changed bool, err error) {
	body, sig, err := fetchSigned(ctx, catalogURL, maxCatalog)
	var c catalog.Catalog
	if err == nil {
		if c, err = catalog.Parse(body); err == nil && c.Serial < s.highestSeen() {
			err = fmt.Errorf("%w (serial %d, have seen %d)", errOlder, c.Serial, s.highestSeen())
		}
	}
	now := s.clock.Now()
	if err != nil {
		if errors.Is(err, update.ErrClock) {
			// Not a failure: the next minute's check tries again, once the clock is set.
			s.mu.Lock()
			s.next = now.Add(catalogRetry)
			s.mu.Unlock()
			return false, err
		}
		s.mu.Lock()
		s.wait = min(max(s.wait*2, catalogRetry), catalogEvery)
		s.next = now.Add(s.wait)
		why := err.Error()
		changed = why != s.problem
		s.problem = why
		logIt := why != s.logged
		s.logged = why
		s.mu.Unlock()
		if logIt {
			if errors.Is(err, update.ErrNotFound) {
				slog.Info("addons: no add-on catalog is published yet; trying again later", "url", catalogURL)
			} else {
				slog.Warn("addons: fetching the catalog; trying again later", "err", err)
			}
		}
		return changed, err
	}
	s.mu.Lock()
	changed = !bytes.Equal(body, s.body) || s.problem != ""
	s.cat, s.body, s.have, s.fetched, s.problem, s.logged = c, body, true, now, "", ""
	s.wait, s.next = 0, now.Add(catalogEvery)
	s.mu.Unlock()
	if changed {
		s.keep(body, sig)
	}
	s.raise(c.Serial)
	return changed, nil
}

// keep writes the catalog and its signature where load finds them, each whole or not at all.
func (s *catalogStore) keep(body, sig []byte) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Warn("addons: keeping the catalog", "err", err)
		return
	}
	bp, sp, _ := s.paths()
	for _, f := range []struct {
		path string
		b    []byte
	}{{bp, body}, {sp, sig}} {
		if err := writeWhole(f.path, f.b, 0o644); err != nil {
			slog.Warn("addons: keeping the catalog", "file", f.path, "err", err)
		}
	}
}

// get is the catalog believed now, and whether there is one.
func (s *catalogStore) get() (catalog.Catalog, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cat, s.have
}

// expired is whether the catalog believed is past its expiry, and so offers no installs or updates.
func (s *catalogStore) expired() bool {
	c, ok := s.get()
	return ok && c.Expired(s.clock.Now())
}

// state is when the catalog was last fetched and why the last fetch failed, if it did.
func (s *catalogStore) state() (time.Time, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetched, s.problem
}

// writeWhole writes a file beside itself and renames it into place, so a power cut leaves the old one or
// the new one, never half of either. The data is synced before the rename and the folder after it: an
// ext4 that puts the rename on the storage before the file's blocks leaves an empty file at the name,
// which for a record or the next user id is worse than the old one. These are written rarely (an
// install, a catalog that changed), so the syncs cost the flash next to nothing.
func writeWhole(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}
