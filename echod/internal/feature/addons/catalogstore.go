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
//
// catalogV2URL is the same catalog in format 2, beside it, which this build asks for first. catalog.json
// stays format 1 for the devices that read only that, with what they can read of it; the two are
// written from one source with one serial (cmd/mkcatalog). Until catalog-v2.json is published, this
// build takes catalog.json.
const (
	catalogURL   = "https://github.com/HuskerMinion/techo5/releases/download/addons/catalog.json"
	catalogV2URL = "https://github.com/HuskerMinion/techo5/releases/download/addons/catalog-v2.json"
)

// maxCatalog is a sanity bound on the file: an entry is a kilobyte or two.
const maxCatalog = 256 << 10

// fetchSigned and verifySigned are the update package's, for the format given, each format being
// signed under a domain of its own: variables so a test can serve a catalog signed with a key of its own.
var (
	fetchSigned = func(ctx context.Context, format int, url string, max int64) ([]byte, []byte, error) {
		if format == catalog.FormatV1 {
			return update.FetchSignedCatalog(ctx, url, max)
		}
		return update.FetchSignedCatalogV2(ctx, url, max)
	}
	verifySigned = func(format int, body, sig []byte) error {
		if format == catalog.FormatV1 {
			return update.VerifyCatalog(body, sig)
		}
		return update.VerifyCatalogV2(body, sig)
	}
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
// otherwise set the bar back to its own serial. Both formats share it: they are one catalog, written
// with one serial, so a format 1 copy below a format 2 one believed is older all the same.
//
// The format kept is the one fetched last: catalog-v2.json, or catalog.json until that is published.
// Only one is kept, under the name it is published as, and checked under that format's domain when it is
// read back. A device taken back to a build that reads only catalog.json finds none kept, and fetches it.
type catalogStore struct {
	dir   string
	clock clock

	mu      sync.Mutex
	cat     catalog.Catalog
	body    []byte
	format  int // the format of the catalog believed
	have    bool
	highest int64
	v2      bool      // a format 2 catalog has been believed, ever (catalog-format2)
	fetched time.Time // when the last fetch succeeded
	problem string    // why the last fetch did not, for the card
	logged  string    // the failure last logged, so the same one is logged once
	next    time.Time // when to fetch again
	wait    time.Duration
}

// formats are the catalog formats this build fetches and keeps, the one it prefers first.
var formats = []int{catalog.Format, catalog.FormatV1}

// paths are where the catalog of a format is kept, and the highest serial believed.
func (s *catalogStore) paths(format int) (body, sig, serial string) {
	name := "catalog.json"
	if format != catalog.FormatV1 {
		name = "catalog-v2.json"
	}
	return filepath.Join(s.dir, name), filepath.Join(s.dir, name+".sig"), filepath.Join(s.dir, "catalog-serial")
}

// v2Path is the record that this device has believed a format 2 catalog. It is kept apart from the
// catalog, as the serial is, so a kept catalog that goes missing or is swapped does not take it along.
func (s *catalogStore) v2Path() string { return filepath.Join(s.dir, "catalog-format2") }

// load reads the highest serial seen and the kept catalog, if there is one, it still verifies, and it
// is not older than that serial. Once a format 2 catalog has been believed, a kept format 1 copy is not.
func (s *catalogStore) load() {
	_, _, np := s.paths(catalog.Format)
	if b, err := os.ReadFile(np); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			s.mu.Lock()
			s.highest = n
			s.mu.Unlock()
		}
	}
	if _, err := os.Stat(s.v2Path()); err == nil {
		s.mu.Lock()
		s.v2 = true
		s.mu.Unlock()
	}
	for _, format := range formats {
		if format == catalog.FormatV1 && s.v2Believed() {
			continue
		}
		bp, sp, _ := s.paths(format)
		body, err1 := os.ReadFile(bp)
		sig, err2 := os.ReadFile(sp)
		if err1 != nil || err2 != nil {
			continue
		}
		c, err := s.check(format, body, sig)
		if err == nil && c.Serial < s.highestSeen() {
			err = fmt.Errorf("%w (serial %d, have seen %d)", errOlder, c.Serial, s.highestSeen())
		}
		if err != nil {
			slog.Warn("addons: the kept catalog is not believed; waiting for a fresh one", "file", filepath.Base(bp), "err", err)
			continue
		}
		s.mu.Lock()
		s.cat, s.body, s.format, s.have = c, body, format, true
		s.mu.Unlock()
		s.raise(c.Serial)
		if format != catalog.FormatV1 {
			s.markV2()
		}
		return
	}
}

func (s *catalogStore) v2Believed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v2
}

// markV2 records that a format 2 catalog has been believed, once, synced as the serial is: from then on
// the format 1 catalog is never fallen back to. It lists less (no version that requires anything, nor
// its withdrawal), so taken in place of a format 2 one at the same serial it would quietly drop what
// the device was shown, and keeping it would delete the format 2 copy.
func (s *catalogStore) markV2() {
	s.mu.Lock()
	if s.v2 {
		s.mu.Unlock()
		return
	}
	s.v2 = true
	s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err == nil {
		if err := writeWhole(s.v2Path(), []byte("2\n"), 0o644); err != nil {
			slog.Warn("addons: keeping that a format 2 catalog was believed", "err", err)
		}
	}
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
	_, _, np := s.paths(catalog.Format)
	if err := os.MkdirAll(s.dir, 0o755); err == nil {
		if err := writeWhole(np, []byte(strconv.FormatInt(n, 10)+"\n"), 0o644); err != nil {
			slog.Warn("addons: keeping the catalog's serial", "err", err)
		}
	}
}

// check is a catalog's signature, under its format's domain, and its contents, which have to be of the
// format it was published as.
func (s *catalogStore) check(format int, body, sig []byte) (catalog.Catalog, error) {
	if err := verifySigned(format, body, sig); err != nil {
		return catalog.Catalog{}, err
	}
	c, err := catalog.Parse(body)
	if err == nil && c.Format != format {
		err = fmt.Errorf("addons: the catalog published as format %d is format %d", format, c.Format)
	}
	return c, err
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
//
// The format 2 catalog is asked for first. Until it is published there is none (update.ErrNotFound,
// for the catalog itself; a missing signature is not that), and the format 1 one is taken instead,
// quietly: that is how things stand until it is, not a failure. Anything else wrong with the format 2
// one is a failure, not a reason to take the other; and once a format 2 catalog has been believed, its
// absence is a failure too (markV2).
func (s *catalogStore) refresh(ctx context.Context) (changed bool, err error) {
	format := catalog.Format
	body, sig, err := fetchSigned(ctx, format, catalogV2URL, maxCatalog)
	if errors.Is(err, update.ErrNotFound) {
		if s.v2Believed() {
			// Not wrapped: this is not "nothing published yet", which is logged quietly.
			err = fmt.Errorf("addons: the format 2 catalog is not there, though this device has believed one; not taking the format 1 catalog in its place: %v", err)
		} else {
			format = catalog.FormatV1
			body, sig, err = fetchSigned(ctx, format, catalogURL, maxCatalog)
		}
	}
	var c catalog.Catalog
	if err == nil {
		if c, err = s.check(format, body, sig); err == nil && c.Serial < s.highestSeen() {
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
	changed = !bytes.Equal(body, s.body) || format != s.format || s.problem != ""
	s.cat, s.body, s.format, s.have, s.fetched, s.problem, s.logged = c, body, format, true, now, "", ""
	s.wait, s.next = 0, now.Add(catalogEvery)
	s.mu.Unlock()
	if changed {
		s.keep(format, body, sig)
	}
	s.raise(c.Serial)
	if format != catalog.FormatV1 {
		s.markV2()
	}
	return changed, nil
}

// keep writes the catalog and its signature where load finds them, each whole or not at all, and takes
// away a kept copy of the other format, so what load finds is the catalog last believed.
func (s *catalogStore) keep(format int, body, sig []byte) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Warn("addons: keeping the catalog", "err", err)
		return
	}
	bp, sp, _ := s.paths(format)
	for _, f := range []struct {
		path string
		b    []byte
	}{{bp, body}, {sp, sig}} {
		if err := writeWhole(f.path, f.b, 0o644); err != nil {
			slog.Warn("addons: keeping the catalog", "file", f.path, "err", err)
			return
		}
	}
	// The other format's copy goes once this one is in place, so a power cut between leaves one of the
	// two, never neither; load prefers format 2 should both be there.
	for _, other := range formats {
		if other == format {
			continue
		}
		obp, osp, _ := s.paths(other)
		for _, p := range []string{obp, osp} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				slog.Warn("addons: removing the catalog kept before", "file", p, "err", err)
			}
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
