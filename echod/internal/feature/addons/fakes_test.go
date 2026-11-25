package addons

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The fakes the supervisor is tested with: a clock moved on by hand, programs that start and exit when
// told, an iptables that keeps its rules in memory, and release assets served from a test server.

// fakeClock fires an After only when Advance moves past it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
}

// pending is how many Afters are waiting to fire within d.
func (c *fakeClock) pending(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, w := range c.waiters {
		if !w.at.After(c.now.Add(d)) {
			n++
		}
	}
	return n
}

// waitPending waits, in real time, until n Afters due within d are waiting.
func (c *fakeClock) waitPending(t *testing.T, n int, d time.Duration) {
	t.Helper()
	eventually(t, "a wait on the clock", func() bool { return c.pending(d) >= n })
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited too long for %s", what)
}

// fakeProc is a started program that runs until exit or Stop.
type fakeProc struct {
	spec spec
	once sync.Once
	done chan struct{}
	err  error
}

func (p *fakeProc) Wait() error { <-p.done; return p.err }
func (p *fakeProc) Stop()       { p.exit(errors.New("signal: terminated")) }
func (p *fakeProc) exit(err error) {
	p.once.Do(func() { p.err = err; close(p.done) })
}

type fakeSpawner struct {
	mu    sync.Mutex
	procs []*fakeProc
	fail  error
	// exitAtOnce makes every program exit as soon as it starts.
	exitAtOnce bool
}

func (s *fakeSpawner) spawn(sp spec) (process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	p := &fakeProc{spec: sp, done: make(chan struct{})}
	s.procs = append(s.procs, p)
	if s.exitAtOnce {
		p.exit(errors.New("exit status 1"))
	}
	return p, nil
}

func (s *fakeSpawner) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.procs)
}

func (s *fakeSpawner) last() *fakeProc {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.procs) == 0 {
		return nil
	}
	return s.procs[len(s.procs)-1]
}

// fakeTables is iptables with its chains in memory, per tool: enough of -N, -F, -C, -A, -I and -D for
// what the firewall does.
type fakeTables struct {
	mu     sync.Mutex
	chains map[string]map[string][]string // tool, chain, rules
	broken map[string]bool                // tools that fail every command
	slow   time.Duration                  // how long each command takes, as on a loaded device
}

func newFakeTables() *fakeTables {
	t := &fakeTables{chains: map[string]map[string][]string{}, broken: map[string]bool{}}
	for _, tool := range tools {
		t.chains[tool] = map[string][]string{"INPUT": {"-i wlan0 -j TECHO5-IN"}}
	}
	return t
}

func (f *fakeTables) run(tool string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.slow > 0 {
		time.Sleep(f.slow)
	}
	if f.broken[tool] {
		return errors.New(tool + ": not found")
	}
	cs := f.chains[tool]
	if len(args) < 2 {
		return errors.New("bad command")
	}
	op, chain, rest := args[0], args[1], args[2:]
	if op == "-I" && len(rest) > 0 && rest[0] == "1" {
		rest = rest[1:]
	}
	r := strings.Join(rest, " ")
	rules, exists := cs[chain]
	switch op {
	case "-N":
		if exists {
			return errors.New("chain exists")
		}
		cs[chain] = nil
	case "-F":
		if !exists {
			return errors.New("no chain")
		}
		cs[chain] = nil
	case "-C":
		if !slices.Contains(rules, r) {
			return errors.New("no such rule")
		}
	case "-A":
		if !exists {
			return errors.New("no chain")
		}
		cs[chain] = append(rules, r)
	case "-I":
		if !exists {
			return errors.New("no chain")
		}
		cs[chain] = append([]string{r}, rules...)
	case "-D":
		i := slices.Index(rules, r)
		if i < 0 {
			return errors.New("no such rule")
		}
		cs[chain] = slices.Delete(rules, i, i+1)
	default:
		return errors.New("unknown " + op)
	}
	return nil
}

func (f *fakeTables) rules(tool, chain string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.chains[tool][chain])
}

// flushInput is what the Dot's firewall does when it is run again.
func (f *fakeTables) flushInput() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, tool := range tools {
		f.chains[tool]["INPUT"] = nil
	}
}

// assets serves release files, by path, over plain http: catalog entries name https links, which the
// download rewrites to the server.
type assets struct {
	mu    sync.Mutex
	files map[string][]byte
	srv   *httptest.Server
}

func newAssets(t *testing.T) *assets {
	a := &assets{files: map[string][]byte{}}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		b, ok := a.files[r.URL.Path]
		a.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(string(b)))
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// put serves b at path and is the pin a catalog entry would give it.
func (a *assets) put(path string, b []byte) update.Binary {
	a.mu.Lock()
	a.files[path] = b
	a.mu.Unlock()
	sum := sha256.Sum256(b)
	return update.Binary{URL: "https://example.com" + path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}
}

// download is update.Download, from the test server.
func (a *assets) download(ctx context.Context, b update.Binary, to string, progress func(float32)) error {
	b.URL = a.srv.URL + strings.TrimPrefix(b.URL, "https://example.com")
	return update.Download(ctx, b, to, progress)
}

// rig is a Manager with every fake in place, and what it was given.
type rig struct {
	t      *testing.T
	m      *Manager
	clock  *fakeClock
	procs  *fakeSpawner
	tables *fakeTables
	assets *assets
	seed   string
	pub    ed25519.PublicKey
	served []byte // the format 1 catalog fetchSigned serves
	sig    string
	// servedV2 is the format 2 catalog fetchSigned serves; without one, it is not published
	// (update.ErrNotFound), and the format 1 one is taken.
	servedV2 []byte
	sigV2    string
	asked    []string // the catalogs fetchSigned was asked for, by url
	cancel   context.CancelFunc
	ran      chan struct{}
	owned    map[string]uint32 // what chown handed to whom
	mu       sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := &rig{t: t, clock: newFakeClock(), procs: &fakeSpawner{}, tables: newFakeTables(), assets: newAssets(t),
		seed: base64.StdEncoding.EncodeToString(priv.Seed()), pub: pub, owned: map[string]uint32{}}
	r.m = newManager(t.TempDir(), r.clock, r.tables.run)
	r.m.download = r.assets.download
	r.m.device = func() catalog.Device {
		return catalog.Device{Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "arm"}
	}

	restore := []func(){}
	swap := func(f func()) { restore = append(restore, f) }
	oldFetch, oldVerify, oldSpawn, oldDrop, oldChown := fetchSigned, verifySigned, spawn, canDrop, chown
	oldMem, oldFree, oldFirst, oldMost, oldLong, oldTrial, oldCheck := memAvailableMB, freeMB, restartFirst, restartMost, ranLong, trialFor, checkEvery
	oldProc, oldTmp, oldKill, oldDu := procDir, tmpDirs, killPID, duMB
	swap(func() {
		fetchSigned, verifySigned, spawn, canDrop, chown = oldFetch, oldVerify, oldSpawn, oldDrop, oldChown
		memAvailableMB, freeMB, restartFirst, restartMost, ranLong, trialFor, checkEvery = oldMem, oldFree, oldFirst, oldMost, oldLong, oldTrial, oldCheck
		procDir, tmpDirs, killPID, duMB = oldProc, oldTmp, oldKill, oldDu
	})
	// No real process is looked at, or killed, and no real temporary folder swept.
	procDir, tmpDirs = filepath.Join(t.TempDir(), "proc"), []string{t.TempDir()}
	killPID = func(int) error { return nil }
	t.Cleanup(func() {
		r.stopRun()
		for _, f := range restore {
			f()
		}
	})
	verifySigned = func(format int, body, sig []byte) error {
		domain := update.CatalogDomainV2
		if format == catalog.FormatV1 {
			domain = update.CatalogDomain
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
		if err != nil || !ed25519.Verify(r.pub, append([]byte(domain), body...), raw) {
			return errors.New("not signed by the test key")
		}
		return nil
	}
	fetchSigned = func(ctx context.Context, format int, url string, max int64) ([]byte, []byte, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.asked = append(r.asked, url)
		body, sig := r.served, r.sig
		if format != catalog.FormatV1 {
			if url != catalogV2URL {
				return nil, nil, errors.New("format 2 asked for at " + url)
			}
			if r.servedV2 == nil {
				return nil, nil, fmt.Errorf("fetching %s: %w", url, update.ErrNotFound)
			}
			body, sig = r.servedV2, r.sigV2
		} else if url != catalogURL {
			return nil, nil, errors.New("format 1 asked for at " + url)
		}
		if body == nil {
			return nil, nil, errors.New("nothing served")
		}
		if err := verifySigned(format, body, []byte(sig)); err != nil {
			return nil, nil, err
		}
		return body, []byte(sig), nil
	}
	spawn = r.procs.spawn
	canDrop = func() bool { return true }
	chown = func(path string, c *cred) {
		r.mu.Lock()
		r.owned[path] = c.UID
		r.mu.Unlock()
	}
	memAvailableMB = func() (int, error) { return 445, nil }
	freeMB = func(string) (int64, error) { return 1000, nil }
	restartFirst, restartMost, ranLong, trialFor = 2*time.Second, time.Minute, 30*time.Second, time.Minute
	checkEvery = 1000 * time.Hour // the Run loop's own wait stays out of the way
	return r
}

// serve signs and serves a catalog, stamped to expire a month after the fake clock's now unless it
// says otherwise, as the file of its format: catalog.json for format 1, catalog-v2.json for 2.
func (r *rig) serve(c catalog.Catalog) {
	if c.Expires.IsZero() {
		c.Expires = r.clock.Now().Add(30 * 24 * time.Hour)
	}
	b, err := json.Marshal(c)
	if err != nil {
		r.t.Fatal(err)
	}
	sign := update.SignCatalogV2
	if c.Format == catalog.FormatV1 {
		sign = update.SignCatalog
	}
	sig, err := sign(b, r.seed)
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	if c.Format == catalog.FormatV1 {
		r.served, r.sig = b, sig
	} else {
		r.servedV2, r.sigV2 = b, sig
	}
	r.mu.Unlock()
}

// unpublishV2 takes the format 2 catalog away, as before it was first published.
func (r *rig) unpublishV2() {
	r.mu.Lock()
	r.servedV2, r.sigV2 = nil, ""
	r.mu.Unlock()
}

// shown is what the card shows for a version in the catalog the supervisor has, as the install form
// carries it back.
func (r *rig) shown(id, version string) string {
	c, _ := r.m.cat.get()
	a, _ := c.Find(id)
	v, _ := a.Version(version)
	return catalog.Shown(a, v)
}

// offered is what the card shows for an installed add-on's update.
func (r *rig) offered(id string) string {
	if s := r.state(id); s.Update != nil {
		return s.Update.Shown
	}
	return ""
}

// run starts the supervisor and waits until it has fetched the catalog.
func (r *rig) run() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.ran = cancel, make(chan struct{})
	go func() { _ = r.m.Run(ctx); close(r.ran) }()
	eventually(r.t, "the supervisor to start", func() bool {
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		return r.m.ctx != nil
	})
	r.clock.waitPending(r.t, 1, checkEvery)
}

func (r *rig) stopRun() {
	if r.cancel != nil {
		r.cancel()
		<-r.ran
		r.cancel = nil
	}
}

// manifestFor is the addon.json a version would ship.
func manifestFor(id string, v catalog.Version) []byte {
	b, _ := json.Marshal(catalog.Manifest{ID: id, Version: v.Version, Command: []string{id}, Args: []string{"-data", "{state}"},
		Ports: v.Ports, Root: v.Root, Page: "http://{device}:32501/", MemoryMB: v.MemoryMB})
	return b
}

// version is a version of the test add-on, its files served, its manifest as given (nil: the one it
// should ship).
func (r *rig) version(id, ver string, manifest []byte) catalog.Version {
	v := catalog.Version{
		Version:   ver,
		MinTECHO5: map[string]string{"show": "v1.6.52"},
		Ports: []catalog.Port{
			{Proto: "tcp", Port: 32500, Why: "control"},
			{Proto: "tcp", Port: 32501, Why: "page"},
			{Proto: "udp", From: 32410, To: 32414, Why: "discovery"},
		},
		MemoryMB: 40,
		DiskMB:   12,
	}
	v.Binaries = map[string]update.Binary{"arm": r.assets.put("/"+id+"/"+ver+"/arm", []byte("program "+id+" "+ver))}
	if manifest == nil {
		manifest = manifestFor(id, v)
	}
	m := r.assets.put("/"+id+"/"+ver+"/addon.json", manifest)
	v.Manifest = &m
	return v
}

func entry(id string, versions ...catalog.Version) catalog.Addon {
	return catalog.Addon{ID: id, Name: "Test " + id, Author: "An Author", Summary: "Does a thing.",
		Repo: "https://github.com/example/" + id, Versions: versions}
}

func (r *rig) state(id string) Status {
	for _, s := range r.m.Installed() {
		if s.ID == id {
			return s
		}
	}
	return Status{}
}

func (r *rig) waitState(id, state string) {
	r.t.Helper()
	eventually(r.t, id+" to be "+state, func() bool { return r.state(id).State == state })
}

func (r *rig) dir(id string) string { return filepath.Join(r.m.root, id) }
