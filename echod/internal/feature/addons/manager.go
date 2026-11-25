package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safename"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// Root is where add-ons live: one folder each, on userdata, so they outlive updates and nothing of
// them is in the firmware image.
//
//	<id>/                0755 root: searchable, as the add-on's user has to reach bin/ and state/
//	<id>/bin/<program>   the release's binary, 0755 root, not writable by the add-on
//	<id>/bin/<file>      each extra file its entry pins, 0644 root, under the name addon.json gives it
//	<id>/addon.json      its manifest, as the catalog pinned it
//	<id>/installed.json  what was installed and what the owner agreed to
//	<id>/uid             the user id it runs as, kept with a state folder that outlives a removal
//	<id>/state/          0700, the add-on's own user's: kept across updates, its home, {state} in its arguments
//	<id>/log, log.1      what it writes, capped and rotated, root's and 0640
//
// The extra files are in bin/ with the program so that everything an update replaces is two names,
// bin and addon.json: staging, the switch, putting the old version back and recover move bin/ whole,
// and the files come and go with the program they were pinned with, never one without the other.
//
// It is a folder of its own on /data, not under /data/techo5-linux: the Dot's boot keeps that folder
// 0700 (it holds the Wi-Fi's password and the SSH host keys), and a user other than root could not get
// through it to its add-on.
var Root = "/data/techo5-addons"

// Restarting: soon after an add-on that ran a while stops, then backing off to a minute for one that
// keeps stopping at once, and giving up after giveUpAfter of those in a row; Start tries again.
var (
	restartFirst = 2 * time.Second
	restartMost  = time.Minute
	ranLong      = 30 * time.Second
	giveUpAfter  = 5

	// trialFor is how long an updated add-on has to stay up before the version it replaced is
	// deleted. One that stops sooner is put back.
	trialFor = time.Minute
)

// States an add-on is in, as the card and Home Assistant say them.
const (
	StateRunning    = "running"
	StateStarting   = "starting"
	StateRestarting = "restarting"
	StateStopped    = "stopped"
	StateGaveUp     = "gave up"
	StateNoMemory   = "not enough memory"
	StateFailed     = "could not start"
	StateTooBig     = "stopped, using too much storage"
)

// Errors a caller can tell apart.
var (
	// ErrBusy is an install, update or removal asked for while another is running, or a change to an
	// add-on that one of them is working on.
	ErrBusy = errors.New("addons: another add-on is being installed, updated or removed; try again when it is done")
	// ErrNotAgreed is an install, or an update asking for more, without the box ticked.
	ErrNotAgreed = errors.New("addons: tick the box to say you understand the risk")
	// ErrAsksMore is an update from Home Assistant that asks for more than the owner agreed to: it is
	// installed only from the setup page, where the warning is shown.
	ErrAsksMore = errors.New("addons: this update asks for more than the version installed; install it from the setup page")
	// ErrNotRunning is a change asked for before the supervisor has started.
	ErrNotRunning = errors.New("addons: the add-on supervisor is not running")
	// ErrChanged is an install or update whose listing changed between the page and the press: what the
	// box agreed to is not what would be installed. The page shows the new listing.
	ErrChanged = errors.New("addons: the catalog changed since the page was shown; look at it again before you install")
	// ErrExpired is an install or update from a catalog past its expiry.
	ErrExpired = errors.New("addons: the catalog this device has is out of date, so nothing is installed from it until a fresh one is fetched")
)

// record is installed.json.
type record struct {
	ID          string       `json:"id"`
	Version     string       `json:"version"`
	Name        string       `json:"name"`
	Author      string       `json:"author"`
	Arch        string       `json:"arch"`
	UID         uint32       `json:"uid"`
	Enabled     bool         `json:"enabled"`
	Agreed      catalog.Asks `json:"agreed"`
	AgreedAt    time.Time    `json:"agreed_at"`
	InstalledAt time.Time    `json:"installed_at"`
	// DiskMB is the storage its listing says it uses, which its state folder is held to (storageMB).
	DiskMB int `json:"disk_mb,omitempty"`
	// RolledBack is the version an update to it went back from, until the next update.
	RolledBack string `json:"rolled_back,omitempty"`
}

// addon is one installed add-on as the supervisor holds it.
type addon struct {
	rec record
	man catalog.Manifest

	state, problem string
	firewall       string // why its ports may not be open, when they may not

	cancel context.CancelFunc
	done   chan struct{} // closed when its supervising loop has ended
	exits  chan struct{} // a token each time it exits, for an update's trial
}

// job is the install, update or removal running now.
type job struct {
	ID, Version, What string
	At                float32
}

// Manager installs, runs, updates and removes add-ons. There is one (Get); tests make their own.
type Manager struct {
	root     string
	clock    clock
	fw       *firewall
	cat      *catalogStore
	device   func() catalog.Device
	download func(ctx context.Context, b update.Binary, to string, progress func(float32)) error

	// changed is told when what Home Assistant shows may have changed: an add-on installed or removed
	// (its entity comes or goes), or its version or the catalog's.
	changed func(entitiesChanged bool)

	op sync.Mutex // held for the whole of an install, update or removal

	mu      sync.Mutex
	ctx     context.Context // the supervisor's, while Run runs
	addons  map[string]*addon
	busy    *job
	lastJob string // how the last install, update or removal ended, when it did not go through
}

func newManager(root string, c clock, run func(string, ...string) error) *Manager {
	return &Manager{
		root:     root,
		clock:    c,
		fw:       newFirewall(run),
		cat:      &catalogStore{dir: root, clock: c},
		device:   thisDevice,
		download: update.Download,
		changed:  func(bool) {},
		addons:   map[string]*addon{},
	}
}

// thisDevice is what catalog entries are checked against.
func thisDevice() catalog.Device {
	return catalog.Device{Family: family, Board: layout.Board, Running: layout.Version, Arch: runtime.GOARCH}
}

func (m *Manager) dir(id string) string { return filepath.Join(m.root, id) }

// load reads the installed add-ons from their folders. A folder whose record or manifest does not read
// is left alone and said in the log: it is not started, and it is not deleted either. What an install
// the device went down in left behind is deleted, and every folder is given the modes it should have.
func (m *Manager) load() {
	if err := os.MkdirAll(m.root, 0o755); err == nil {
		_ = os.Chmod(m.root, 0o755)
	}
	m.cat.load()
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".incoming-") {
			_ = os.RemoveAll(filepath.Join(m.root, e.Name()))
			continue
		}
		if !e.IsDir() || !catalog.ValidID(e.Name()) {
			continue
		}
		m.recover(e.Name())
		a, err := m.read(e.Name())
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("addons: an add-on's folder does not read; it is left as it is", "id", e.Name(), "err", err)
			}
			continue
		}
		m.modes(a.rec, a.man)
		m.mu.Lock()
		m.addons[a.rec.ID] = a
		m.mu.Unlock()
	}
}

// modes gives an add-on's folders what they need for its user to run it and nothing more: the folder
// and bin/ searchable by anyone and root's, its extra files readable and not writable, state/ its
// user's alone. Go changes user before it changes
// to the state folder and runs the program, so a folder on the way that others cannot search stops
// the add-on before it starts. Set at every start, so a folder made by an older build is put right.
func (m *Manager) modes(rec record, man catalog.Manifest) {
	dir := m.dir(rec.ID)
	for _, d := range []string{dir, filepath.Join(dir, "bin")} {
		if err := os.Chmod(d, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("addons: setting a folder's mode", "path", d, "err", err)
		}
	}
	for _, f := range man.Files {
		// The names were checked when the manifest was read (catalog.ValidFileName), so each is a
		// file in bin/ and nothing else.
		p := filepath.Join(dir, "bin", f)
		if err := os.Chmod(p, 0o644); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("addons: setting an extra file's mode", "path", p, "err", err)
		}
	}
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		slog.Warn("addons: making the state folder", "path", state, "err", err)
		return
	}
	_ = os.Chmod(state, 0o700)
	if !RunsAsRoot(man.Root) {
		chown(state, credFor(rec.UID))
	}
}

// recover finishes or undoes an update the device went down in the middle of. The record is written
// with the new version only once the update has been kept, so a record that names the version in .old
// is an update still on trial, or one half switched: what is in .old goes back. A record that names the
// version in place is an update kept whose .old was not deleted yet.
//
// The switch moves two names, bin and addon.json, one at a time, so a power cut can fall between them:
// each is put back on its own, from .old, when its place is empty or holds the new version's; nothing
// is deleted from the add-on's folder unless there is something in .old to put in its place; and .old
// goes only once both names are back where they belong.
func (m *Manager) recover(id string) {
	dir := m.dir(id)
	old := filepath.Join(dir, ".old")
	defer os.RemoveAll(filepath.Join(dir, ".new"))
	if _, err := os.Stat(old); err != nil {
		return
	}
	version := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		man, err := catalog.ParseManifest(b)
		if err != nil {
			return ""
		}
		return man.Version
	}
	var rec record
	if b, err := os.ReadFile(filepath.Join(dir, "installed.json")); err == nil {
		_ = json.Unmarshal(b, &rec)
	}
	inPlace := version(filepath.Join(dir, "addon.json"))
	for _, name := range []string{"bin", "addon.json"} {
		from, to := filepath.Join(old, name), filepath.Join(dir, name)
		if !exists(from) {
			continue
		}
		switch {
		case !exists(to):
			// Its place is empty: the switch moved it aside and went down before the new one came in,
			// or the new one never did.
		case rec.Version == "" || rec.Version == inPlace:
			continue // the version the record names is in place; this is the one it replaced
		default:
			_ = os.RemoveAll(to) // the new version's, on trial or half moved in
		}
		if err := os.Rename(from, to); err != nil {
			slog.Error("addons: putting the previous version back", "id", id, "name", name, "err", err)
		}
	}
	if exists(filepath.Join(dir, "bin")) && version(filepath.Join(dir, "addon.json")) == rec.Version {
		if inPlace != rec.Version {
			slog.Warn("addons: an update was not finished when the device went down; the version before it is back", "id", id, "version", rec.Version)
		}
		_ = os.RemoveAll(old)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// read is one installed add-on from its folder.
func (m *Manager) read(id string) (*addon, error) {
	b, err := os.ReadFile(filepath.Join(m.dir(id), "installed.json"))
	if err != nil {
		return nil, err
	}
	var rec record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	if rec.ID != id {
		return nil, fmt.Errorf("its record is %q's", rec.ID)
	}
	mb, err := os.ReadFile(filepath.Join(m.dir(id), "addon.json"))
	if err != nil {
		return nil, err
	}
	man, err := catalog.ParseManifest(mb)
	if err != nil {
		return nil, err
	}
	return &addon{rec: rec, man: man, state: StateStopped}, nil
}

func (m *Manager) writeRecord(dir string, rec record) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeWhole(filepath.Join(dir, "installed.json"), b, 0o644)
}

// Run is the supervisor: it starts every installed add-on that is switched on, keeps the firewall's
// rules for them in place, holds their storage to what they said, fetches the catalog now and then,
// and stops them all when ctx ends.
func (m *Manager) Run(ctx context.Context) error {
	m.load()
	// Whatever a daemon that went down without closing it left open, whether or not anything is
	// installed now: an add-on removed by hand would otherwise keep its ports. And whatever it left
	// running: nothing of an add-on's user's should be, before this starts one.
	m.fw.reset()
	if canDrop() {
		killStrays()
	}
	m.mu.Lock()
	m.ctx = ctx
	any := len(m.addons) > 0
	for _, a := range m.addons {
		if a.rec.Enabled {
			m.startLocked(a)
		}
	}
	m.mu.Unlock()
	if any {
		// Home Assistant may have read the entity list before the add-ons were loaded.
		m.changed(true)
	}

	ticks := 0
	for {
		if m.cat.due() {
			if changed, _ := m.cat.refresh(ctx); changed {
				m.changed(false)
			}
		}
		select {
		case <-ctx.Done():
			m.stopAll()
			return nil
		case <-m.clock.After(checkEvery):
		}
		if repaired, broken := m.fw.check(); repaired {
			slog.Warn("addons: the firewall's rules for the add-ons were missing (the image's firewall ran again?); put back")
		} else if broken != "" {
			slog.Warn("addons: the firewall's rules for the add-ons cannot all be put in place; their ports may not be reachable", "missing", broken)
		}
		if ticks++; ticks%storageEvery == 0 {
			m.checkStorage()
		}
	}
}

// checkEvery is how often the firewall's rules are checked, and storageEvery how many of those go by
// between looks at how much the add-ons keep, which walks their folders.
var (
	checkEvery   = time.Minute
	storageEvery = 10
)

// storageMB is how much an add-on that says it uses diskMB may keep in its state folder before it is
// stopped: a few times what it said, so one that grows a little is left alone, and one that fills the
// storage the device's own settings and updates need is not.
func storageMB(diskMB int) int64 { return int64(max(diskMB*4, 64)) }

// checkStorage stops an add-on whose state folder holds more than it may.
func (m *Manager) checkStorage() {
	m.mu.Lock()
	var running []*addon
	for _, a := range m.addons {
		if a.cancel != nil {
			running = append(running, a)
		}
	}
	m.mu.Unlock()
	for _, a := range running {
		m.mu.Lock()
		rec := a.rec
		trying := m.busy != nil && m.busy.ID == rec.ID
		m.mu.Unlock()
		if trying {
			continue // an update owns it; stopping it here would read as the update failing its trial
		}
		used, limit := duMB(filepath.Join(m.dir(rec.ID), "state")), storageMB(rec.DiskMB)
		if used <= limit {
			continue
		}
		why := fmt.Sprintf("it keeps %d MB, and its listing says it uses about %d MB, so it was stopped at %d MB; "+
			"Start runs it again, and Remove can delete what it keeps", used, rec.DiskMB, limit)
		slog.Warn("addons: an add-on was stopped for the storage it uses", "id", rec.ID, "mb", used, "limit", limit)
		m.stop(a)
		m.set(a, StateTooBig, why)
	}
}

// Refresh fetches the catalog now, for the card's Check now and Home Assistant's check.
func (m *Manager) Refresh(ctx context.Context) error {
	changed, err := m.cat.refresh(ctx)
	if changed {
		m.changed(false)
	}
	return err
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	var list []*addon
	for _, a := range m.addons {
		if a.cancel != nil {
			a.cancel()
			list = append(list, a)
		}
	}
	m.ctx = nil
	m.mu.Unlock()
	for _, a := range list {
		m.mu.Lock()
		d := a.done
		m.mu.Unlock()
		if d != nil {
			<-d
		}
	}
}

// startLocked starts an add-on's supervising loop, with m.mu held.
func (m *Manager) startLocked(a *addon) {
	if a.cancel != nil || m.ctx == nil {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	a.cancel, a.done = cancel, make(chan struct{})
	if a.exits == nil {
		a.exits = make(chan struct{}, 8)
	}
	a.state, a.problem, a.firewall = StateStarting, "", ""
	go m.keep(ctx, a, a.done)
}

// stop ends an add-on's supervising loop and waits for its program, and everything its user runs, to
// be gone.
func (m *Manager) stop(a *addon) {
	m.mu.Lock()
	cancel, done := a.cancel, a.done
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// set is a change of state, said in Home Assistant.
func (m *Manager) set(a *addon, state, problem string) {
	m.mu.Lock()
	a.state, a.problem = state, problem
	m.mu.Unlock()
	m.changed(false)
}

// RunsAsRoot is whether an add-on asking for root, or not, would run as root here: when it asks, and
// when this daemon could not run it as another user. The warning says so in bold either way.
func RunsAsRoot(asks bool) bool { return asks || !canDrop() }

// cleanUp is what follows every exit of an add-on that does not run as root: everything else its user
// still runs is killed (a program can leave its process group, and would otherwise outlive a stop or a
// removal, and be there for the next start), and what it left in the shared temporary folders goes.
func cleanUp(c *cred, id string) {
	if c == nil {
		return
	}
	if err := killUID(c.UID); err != nil {
		slog.Error("addons: an add-on's processes would not die", "id", id, "err", err)
	}
	sweepUID(c.UID)
}

// keep runs an add-on until ctx ends, starting it again when it stops, with a backoff, and giving up
// after repeated fast crashes.
func (m *Manager) keep(ctx context.Context, a *addon, done chan struct{}) {
	defer func() {
		m.mu.Lock()
		if a.done == done {
			a.cancel, a.done = nil, nil
		}
		m.mu.Unlock()
		close(done)
	}()

	m.mu.Lock()
	rec, man := a.rec, a.man
	m.mu.Unlock()
	dir := m.dir(rec.ID)
	state := filepath.Join(dir, "state")
	m.modes(rec, man)
	// Whatever its user still runs from before (a daemon that went down without stopping it, a child
	// that left its group) goes before it starts: two copies would fight over its ports and its state.
	if !RunsAsRoot(man.Root) {
		cleanUp(credFor(rec.UID), rec.ID)
	}
	lf, err := openLog(filepath.Join(dir, "log"))
	if err != nil {
		m.set(a, StateFailed, "its log cannot be written: "+err.Error())
		exited(a)
		return
	}
	defer lf.Close()

	var c *cred
	if !RunsAsRoot(man.Root) {
		c = credFor(rec.UID)
	}
	sp := spec{
		Path: filepath.Join(dir, "bin", man.Command[0]),
		Args: man.Expand(state),
		Dir:  state,
		// TECHO5_ADDON_BIN is where its program and its extra files are, so it finds them by name
		// whatever the folder add-ons live in.
		Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + state, "TECHO5_ADDON=" + rec.ID,
			"TECHO5_ADDON_STATE=" + state, "TECHO5_ADDON_BIN=" + filepath.Join(dir, "bin")},
		Cred: c,
		Out:  lf,
	}
	if c != nil {
		sp.NProc, sp.AddressMB = nprocLimit, addressMB(man.MemoryMB, rec.Arch)
	}

	wait, quick := restartFirst, 0
	for {
		// A device short of memory now may not be in a minute: this is a failure to start like any
		// other, tried again after the backoff and given up on with the rest.
		if ok, why := fits(man.MemoryMB); !ok {
			lf.Note("not started: " + why)
			m.set(a, StateNoMemory, why)
			exited(a)
			quick++
		} else if p, err := spawn(sp); err != nil {
			lf.Note("could not start: " + err.Error())
			slog.Warn("addons: an add-on could not start", "id", rec.ID, "err", err)
			m.set(a, StateFailed, err.Error())
			exited(a)
			quick++
		} else {
			who := "as root"
			if c != nil {
				who = "as user " + strconv.Itoa(int(c.UID))
			}
			lf.Note("started " + rec.Version + " " + who)
			fwProblem := ""
			if err := m.fw.openFor(rec.ID, man.Ports); err != nil {
				fwProblem = err.Error()
				lf.Note(fwProblem)
				slog.Warn("addons: opening an add-on's ports", "id", rec.ID, "err", err)
			}
			m.mu.Lock()
			a.firewall = fwProblem
			m.mu.Unlock()
			m.set(a, StateRunning, "")
			start := m.clock.Now()
			gone := make(chan error, 1)
			go func() { gone <- p.Wait() }()
			select {
			case <-ctx.Done():
				p.Stop()
				<-gone
				m.fw.closeFor(rec.ID)
				cleanUp(c, rec.ID)
				lf.Note("stopped")
				m.set(a, StateStopped, "")
				return
			case err = <-gone:
			}
			m.fw.closeFor(rec.ID)
			cleanUp(c, rec.ID)
			ran := m.clock.Now().Sub(start)
			why := "exited"
			if err != nil {
				why = err.Error()
			}
			lf.Note(fmt.Sprintf("stopped by itself after %v: %s", ran.Round(time.Second), why))
			slog.Warn("addons: an add-on stopped", "id", rec.ID, "after", ran.Round(time.Second), "err", err)
			exited(a)
			if ran >= ranLong {
				wait, quick = restartFirst, 0
			} else {
				quick++
			}
		}
		if quick >= giveUpAfter {
			m.mu.Lock()
			last := a.state
			m.mu.Unlock()
			why := fmt.Sprintf("it stopped %d times in a row soon after starting, so it is not started again until Start is pressed", quick)
			if last == StateNoMemory {
				why = fmt.Sprintf("there was not enough memory for it %d times in a row, so it is not tried again until Start is pressed", quick)
			}
			lf.Note("given up: " + why)
			m.set(a, StateGaveUp, why)
			return
		}
		// One waiting for memory keeps saying so through the wait, which is the reason for it.
		m.mu.Lock()
		short := a.state == StateNoMemory
		m.mu.Unlock()
		if !short {
			m.set(a, StateRestarting, "")
		}
		select {
		case <-ctx.Done():
			m.set(a, StateStopped, "")
			return
		case <-m.clock.After(wait):
		}
		if quick > 0 {
			wait = min(wait*2, restartMost)
		}
	}
}

// exited tells an update on trial that the add-on is not up: it stopped, or never started.
func exited(a *addon) {
	select {
	case a.exits <- struct{}{}:
	default:
	}
}

// Start switches an add-on on and starts it, which is also how one that was given up on is tried again.
func (m *Manager) Start(id string) error { return m.enable(id, true) }

// Stop stops an add-on and leaves it off, across restarts, until Start.
func (m *Manager) Stop(id string) error { return m.enable(id, false) }

func (m *Manager) enable(id string, on bool) error {
	m.mu.Lock()
	a, ok := m.addons[id]
	switch {
	case !ok:
		m.mu.Unlock()
		return fmt.Errorf("addons: %s is not installed", id)
	case m.ctx == nil:
		m.mu.Unlock()
		return ErrNotRunning
	case m.busy != nil && m.busy.ID == id:
		// An update on trial, or a removal, owns the add-on until it is done: a Stop now would be
		// undone by the trial's end, and a Start would race the removal.
		m.mu.Unlock()
		return ErrBusy
	}
	a.rec.Enabled = on
	rec := a.rec
	m.mu.Unlock()
	if err := m.writeRecord(m.dir(id), rec); err != nil {
		return err
	}
	if on {
		m.mu.Lock()
		m.startLocked(a)
		m.mu.Unlock()
	} else {
		m.stop(a)
	}
	m.changed(false)
	return nil
}

// begin takes the one install, update or removal allowed at a time.
func (m *Manager) begin(id, version, what string) (func(failed error), error) {
	if !m.op.TryLock() {
		return nil, ErrBusy
	}
	m.mu.Lock()
	m.busy = &job{ID: id, Version: version, What: what}
	m.lastJob = ""
	m.mu.Unlock()
	m.changed(false)
	return func(failed error) {
		m.mu.Lock()
		m.busy = nil
		if failed != nil {
			m.lastJob = failed.Error()
		}
		m.mu.Unlock()
		m.op.Unlock()
		m.changed(false)
	}, nil
}

// refused is what the card says for an install or update turned down before it began, as begin's end
// says it for one turned down after.
func (m *Manager) refused(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.lastJob = err.Error()
	m.mu.Unlock()
	m.changed(false)
}

// progress is how far a download has got, said in Home Assistant at each whole percent.
func (m *Manager) progress(at float32) {
	m.mu.Lock()
	moved := false
	if m.busy != nil {
		moved = int(m.busy.At*100) != int(at*100)
		m.busy.At = at
	}
	m.mu.Unlock()
	if moved {
		m.changed(false)
	}
}

func (m *Manager) phase(what string) {
	m.mu.Lock()
	if m.busy != nil {
		m.busy.What = what
	}
	m.mu.Unlock()
	m.changed(false)
}

// offered is the catalog's entry for an add-on and the version of it this device would install.
func (m *Manager) offered(id, version string) (catalog.Addon, catalog.Version, error) {
	cat, ok := m.cat.get()
	if !ok {
		return catalog.Addon{}, catalog.Version{}, errors.New("addons: the catalog has not been fetched yet; try again in a minute")
	}
	if m.cat.expired() {
		return catalog.Addon{}, catalog.Version{}, ErrExpired
	}
	a, ok := cat.Find(id)
	if !ok {
		return a, catalog.Version{}, fmt.Errorf("addons: %s is not in the catalog", id)
	}
	v, ok := a.Version(version)
	if !ok {
		return a, v, fmt.Errorf("addons: the catalog has no %s %s", id, version)
	}
	if !a.Listed(v) {
		return a, v, fmt.Errorf("addons: %s %s is no longer listed", id, version)
	}
	if ok, why := v.RunsOn(m.device()); !ok {
		return a, v, fmt.Errorf("addons: %s %s %s", a.Name, version, why)
	}
	return a, v, nil
}

// clash is another installed add-on already asking for one of these ports.
func (m *Manager) clash(id string, ports []catalog.Port) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for other, a := range m.addons {
		if other == id {
			continue
		}
		for _, p := range ports {
			for _, q := range a.rec.Agreed.Ports {
				if p.Overlaps(q) {
					return fmt.Errorf("addons: port %s is %s's already", p, a.rec.Name)
				}
			}
		}
	}
	return nil
}

// room checks the disk for a version, with what the build for arch downloads on top: its binary and
// its extra files.
func (m *Manager) room(v catalog.Version, arch string) error {
	free, err := freeMB(m.root)
	if err != nil {
		return nil // not knowing is not a reason to refuse
	}
	need, ok := neededMB(v, arch)
	if !ok {
		return errors.New("addons: its listing gives sizes that do not add up to an amount of storage")
	}
	if free < need {
		return fmt.Errorf("addons: it needs about %d MB of room on the device's storage, and %d MB is free", need, free)
	}
	return nil
}

// maxPartMB bounds each size room adds up, a petabyte: far past any device, and small enough that a few
// of them cannot overflow the sum.
const maxPartMB = 1 << 30

// neededMB is the room a version takes on the device, in whole megabytes, and whether its sizes make
// one. The catalog checks them (catalog.Version.valid), but this is the last word before writing to the
// storage, so a size that is negative, or so large the sum would wrap, refuses rather than passing as
// little or no room.
func neededMB(v catalog.Version, arch string) (int64, bool) {
	parts := []int64{int64(v.DiskMB), v.Binaries[arch].Size >> 20, 1}
	for _, f := range v.FilesFor(arch) {
		if f.Size < 0 || f.Size > maxPartMB<<20 {
			return 0, false
		}
		parts = append(parts, (f.Size+1<<20-1)>>20)
	}
	var need int64
	for _, p := range parts {
		if p < 0 || p > maxPartMB {
			return 0, false
		}
		need += p
	}
	return need, true
}

// fetchVersion downloads a version's manifest, binary and extra files into dir, checking each against
// the catalog. Any one that does not check out fails the whole: the caller deletes dir, and nothing of
// the version is installed.
func (m *Manager) fetchVersion(ctx context.Context, id string, v catalog.Version, dir string) (catalog.Manifest, error) {
	d := m.device()
	b := v.Binaries[d.Arch]
	files := v.FilesFor(d.Arch)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		return catalog.Manifest{}, err
	}
	mpath := filepath.Join(dir, "addon.json")
	if err := m.download(ctx, *v.Manifest, mpath, nil); err != nil {
		return catalog.Manifest{}, fmt.Errorf("addons: downloading its manifest: %w", err)
	}
	mb, err := os.ReadFile(mpath)
	if err != nil {
		return catalog.Manifest{}, err
	}
	man, err := catalog.ParseManifest(mb)
	if err != nil {
		return man, err
	}
	if err := man.Matches(id, v, d.Arch); err != nil {
		return man, fmt.Errorf("addons: refused: %w", err)
	}
	if err := os.Chmod(mpath, 0o644); err != nil {
		return man, err
	}
	m.phase("downloading")
	// One progress over everything downloaded, by size, so the card's percentage does not start over
	// for each file.
	total, done := b.Size, int64(0)
	for _, f := range files {
		total += f.Size
	}
	share := func(size int64) func(float32) {
		from := done
		done += size
		return func(at float32) {
			if total > 0 {
				m.progress(float32(float64(from)+float64(at)*float64(size)) / float32(total))
			}
		}
	}
	prog := filepath.Join(dir, "bin", man.Command[0])
	if err := m.download(ctx, b, prog, share(b.Size)); err != nil {
		return man, fmt.Errorf("addons: downloading it: %w", err)
	}
	if err := os.Chmod(prog, 0o755); err != nil {
		return man, err
	}
	// Under the catalog's names, which Matches has just found are the manifest's, each checked by its
	// size and hash as the binary is. Root's, as the daemon downloads them, and read-only to the add-on:
	// they are what was pinned, which the add-on reads and unpacks into its state, never changes.
	for _, f := range files {
		p, err := safename.Join(filepath.Join(dir, "bin"), f.Name)
		if err != nil {
			return man, err
		}
		if err := m.download(ctx, f.Binary, p, share(f.Size)); err != nil {
			return man, fmt.Errorf("addons: downloading its extra file %s: %w", f.Name, err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			return man, err
		}
	}
	return man, nil
}

// allocUID is the user id an add-on is installed as: the one a state folder kept by its last removal
// is owned by, or else the next never given out, which is kept (next-uid) before it is used, so it is
// never given out twice, whatever happens after.
func (m *Manager) allocUID(id string) (uint32, error) {
	parse := func(b []byte) (uint32, bool) {
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 32)
		return uint32(n), err == nil && n >= firstUID && n <= lastUID+1
	}
	if b, err := os.ReadFile(filepath.Join(m.dir(id), "uid")); err == nil {
		if n, ok := parse(b); ok && n <= lastUID {
			return n, nil
		}
	}
	next := uint32(firstUID)
	if b, err := os.ReadFile(filepath.Join(m.root, "next-uid")); err == nil {
		if n, ok := parse(b); ok {
			next = n
		}
	}
	// An older build gave out ids without the mark: none of those may be given again either.
	entries, _ := os.ReadDir(m.root)
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join(m.root, e.Name(), "uid")); err == nil {
			if n, ok := parse(b); ok && n >= next {
				next = n + 1
			}
		}
	}
	if next > lastUID {
		return 0, errNoUID
	}
	if err := writeWhole(filepath.Join(m.root, "next-uid"), []byte(strconv.Itoa(int(next+1))+"\n"), 0o644); err != nil {
		return 0, err
	}
	return next, nil
}

// Install installs an add-on from the catalog and starts it. agreed is the box on the setup page's
// warning, ticked, and shown is what the card showed above it (catalog.Shown): nothing is installed
// without the box, nothing whose listing has changed since the page, and nothing but the setup page
// asks for an install.
func (m *Manager) Install(ctx context.Context, id, version, shown string, agreed bool) (err error) {
	defer func() { m.refused(err) }()
	if !agreed {
		return ErrNotAgreed
	}
	end, err := m.begin(id, version, "checking")
	if err != nil {
		return err
	}
	defer func() { end(err) }()
	m.mu.Lock()
	running := m.ctx != nil
	_, have := m.addons[id]
	m.mu.Unlock()
	switch {
	case !running:
		return ErrNotRunning
	case have:
		return fmt.Errorf("addons: %s is installed already", id)
	}

	entry, v, err := m.offered(id, version)
	if err != nil {
		return err
	}
	if shown != catalog.Shown(entry, v) {
		return ErrChanged
	}
	if ok, why := fits(v.MemoryMB); !ok {
		return errors.New("addons: it " + why)
	}
	if err := m.clash(id, v.Ports); err != nil {
		return err
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return err
	}
	if err := m.room(v, m.device().Arch); err != nil {
		return err
	}

	stage := filepath.Join(m.root, ".incoming-"+id)
	_ = os.RemoveAll(stage)
	defer os.RemoveAll(stage)
	man, err := m.fetchVersion(ctx, id, v, stage)
	if err != nil {
		return err
	}
	uid, err := m.allocUID(id)
	if err != nil {
		return err
	}
	now := m.clock.Now()
	rec := record{ID: id, Version: v.Version, Name: entry.Name, Author: entry.Author, Arch: m.device().Arch, UID: uid,
		Enabled: true, Agreed: v.Asks(m.device().Arch), AgreedAt: now, InstalledAt: now, DiskMB: v.DiskMB}

	// Into place: the add-on's folder may already be there, holding the state a removal kept.
	m.phase("installing")
	dir := m.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(dir, "bin"))
	if err := os.Rename(filepath.Join(stage, "bin"), filepath.Join(dir, "bin")); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(stage, "addon.json"), filepath.Join(dir, "addon.json")); err != nil {
		return err
	}
	if err := writeWhole(filepath.Join(dir, "uid"), []byte(strconv.Itoa(int(uid))+"\n"), 0o644); err != nil {
		return err
	}
	m.modes(rec, man)
	if err := m.writeRecord(dir, rec); err != nil {
		return err
	}
	slog.Info("addons: installed", "id", id, "version", v.Version, "author", entry.Author, "root", RunsAsRoot(man.Root))

	a := &addon{rec: rec, man: man, state: StateStopped}
	m.mu.Lock()
	m.addons[id] = a
	m.startLocked(a)
	m.mu.Unlock()
	m.changed(true)
	return nil
}

// Offer is a newer version of an installed add-on: what it is, what it asks that the owner has not
// agreed to, if anything, and what the card shows for it (catalog.Shown), which its form carries back.
// Problem is why it cannot be installed here, when it cannot.
type Offer struct {
	Version string
	More    []string
	Problem string
	Shown   string
}

// offer is the update for an installed add-on, if the catalog has one and is not out of date.
func (m *Manager) offer(rec record) (catalog.Addon, catalog.Version, *Offer) {
	cat, ok := m.cat.get()
	if !ok {
		return catalog.Addon{}, catalog.Version{}, nil
	}
	entry, ok := cat.Find(rec.ID)
	if !ok || m.cat.expired() {
		return entry, catalog.Version{}, nil
	}
	v, ok := entry.Newest(m.device())
	if !ok || v.Version == rec.Version || !update.Newer(v.Version, rec.Version) {
		return entry, v, nil
	}
	o := &Offer{Version: v.Version, More: v.Asks(m.device().Arch).More(rec.Agreed), Shown: catalog.Shown(entry, v)}
	if ok, why := fits(v.MemoryMB); !ok {
		o.Problem = "it " + why
	}
	return entry, v, o
}

// Update installs the newest version the catalog has of an add-on, and keeps it only if it stays up for
// trialFor; otherwise the version it replaced is put back. agreed is the setup page's box, which is
// asked only when the update asks for more than was agreed to, and shown what the card showed for the
// update; fromHA is Home Assistant's install, which is refused for such an update (ErrAsksMore), as the
// warning is not seen there, and which shows nothing to compare.
func (m *Manager) Update(ctx context.Context, id, shown string, agreed, fromHA bool) (err error) {
	defer func() { m.refused(err) }()
	m.mu.Lock()
	a, ok := m.addons[id]
	var rec record
	if ok {
		rec = a.rec
	}
	supervising := m.ctx
	m.mu.Unlock()
	switch {
	case !ok:
		return fmt.Errorf("addons: %s is not installed", id)
	case supervising == nil:
		return ErrNotRunning
	}
	// The trial ends early when the caller gives up or the daemon stops, whichever comes first.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopTrial := context.AfterFunc(supervising, cancel)
	defer stopTrial()
	if m.cat.expired() {
		return ErrExpired
	}
	_, v, o := m.offer(rec)
	if o == nil {
		return fmt.Errorf("addons: there is no newer %s to install", rec.Name)
	}
	if o.Problem != "" {
		return errors.New("addons: " + o.Problem)
	}
	if !fromHA && shown != o.Shown {
		return ErrChanged
	}
	if len(o.More) > 0 && (fromHA || !agreed) {
		if fromHA {
			return ErrAsksMore
		}
		return ErrNotAgreed
	}
	end, err := m.begin(id, v.Version, "checking")
	if err != nil {
		return err
	}
	defer func() { end(err) }()
	if _, _, err := m.offered(id, v.Version); err != nil {
		return err
	}
	if err := m.clash(id, v.Ports); err != nil {
		return err
	}
	if err := m.room(v, m.device().Arch); err != nil {
		return err
	}
	m.mu.Lock()
	rec = a.rec // as it is now, under the job: nothing else changes it from here
	m.mu.Unlock()

	dir := m.dir(id)
	stage, old := filepath.Join(dir, ".new"), filepath.Join(dir, ".old")
	_ = os.RemoveAll(stage)
	_ = os.RemoveAll(old)
	defer os.RemoveAll(stage)
	man, err := m.fetchVersion(ctx, id, v, stage)
	if err != nil {
		return err
	}

	// The switch: the old version stopped and moved aside whole, the new one moved in, and started.
	m.phase("switching")
	wasOn := rec.Enabled
	m.stop(a)
	if err := os.MkdirAll(old, 0o755); err != nil {
		m.restart(a, wasOn)
		return err
	}
	swap := func(from, to string) error {
		for _, name := range []string{"bin", "addon.json"} {
			if err := os.Rename(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := swap(dir, old); err != nil {
		_ = swap(old, dir)
		m.restart(a, wasOn)
		return err
	}
	if err := swap(stage, dir); err != nil {
		putBack(dir, old)
		m.restart(a, wasOn)
		return err
	}
	next := rec
	// It stays as on or off as it was: an update installed while it was stopped is not a Start.
	next.Version, next.Enabled, next.RolledBack, next.DiskMB = v.Version, wasOn, "", v.DiskMB
	if len(o.More) > 0 {
		next.Agreed, next.AgreedAt = merged(rec.Agreed, v.Asks(m.device().Arch)), m.clock.Now()
	}
	m.modes(next, man)

	if !wasOn {
		// Stopped, it stays stopped: the new version goes in without a trial, which would mean starting
		// it, and is tried the next time Start is pressed, as any start is.
		m.mu.Lock()
		a.rec, a.man = next, man
		m.mu.Unlock()
		if err := m.writeRecord(dir, next); err != nil {
			return err
		}
		_ = os.RemoveAll(old)
		slog.Info("addons: updated while stopped", "id", id, "from", rec.Version, "to", v.Version)
		return nil
	}

	m.mu.Lock()
	a.rec, a.man = next, man
	for len(a.exits) > 0 {
		<-a.exits
	}
	m.startLocked(a)
	exits := a.exits
	m.mu.Unlock()
	m.phase("trying")

	select {
	case <-exits:
		// It did not stay up: back to the version it replaced.
		slog.Warn("addons: an update did not stay up; going back", "id", id, "tried", v.Version, "back_to", rec.Version)
		m.stop(a)
		putBack(dir, old)
		back, rerr := m.read(id)
		rec.RolledBack = v.Version
		m.mu.Lock()
		if rerr == nil {
			a.man = back.man
		}
		a.rec = rec
		m.mu.Unlock()
		_ = m.writeRecord(dir, rec)
		m.restart(a, wasOn)
		return fmt.Errorf("addons: %s %s did not stay up for %v, so %s is back", rec.Name, v.Version, trialFor, rec.Version)
	case <-m.clock.After(trialFor):
	case <-ctx.Done():
		// The daemon is stopping before the trial was over: the record still names the version in
		// .old, so the next start puts it back (recover) rather than keep one that was never proved.
		return ctx.Err()
	}
	// The record first, as it is now: once it names the new version the update is kept, and an .old
	// left behind by a power cut here is only deleted at the next start.
	m.mu.Lock()
	kept := a.rec
	m.mu.Unlock()
	if err := m.writeRecord(dir, kept); err != nil {
		return err
	}
	_ = os.RemoveAll(old)
	slog.Info("addons: updated", "id", id, "from", rec.Version, "to", v.Version)
	return nil
}

// putBack moves the version in old back over whatever is in dir, name by name, deleting a name in dir
// only when old has one to put in its place.
func putBack(dir, old string) {
	for _, name := range []string{"bin", "addon.json"} {
		from, to := filepath.Join(old, name), filepath.Join(dir, name)
		if !exists(from) {
			continue
		}
		_ = os.RemoveAll(to)
		if err := os.Rename(from, to); err != nil {
			slog.Error("addons: putting the previous version back failed", "name", name, "err", err)
		}
	}
}

// restart starts an add-on again after an update stopped it, if it was on.
func (m *Manager) restart(a *addon, on bool) {
	if !on {
		return
	}
	m.mu.Lock()
	m.startLocked(a)
	m.mu.Unlock()
}

// merged is everything agreed to in either, but for the extra files, which are the ones agreed to now:
// a file is agreed to by its hash, and the ones before are not on the device any more.
func merged(a, b catalog.Asks) catalog.Asks {
	out := catalog.Asks{Ports: slices.Clone(a.Ports), Root: a.Root || b.Root, Files: slices.Clone(b.Files)}
	for _, p := range b.Ports {
		covered := false
		for _, q := range out.Ports {
			if q.Covers(p) {
				covered = true
				break
			}
		}
		if !covered {
			out.Ports = append(out.Ports, p)
		}
	}
	return out
}

// Remove stops an add-on, kills whatever its user still runs, takes its ports out of the firewall and
// deletes its folder. With keepState its state folder stays, and the user id that owns it, for the
// next install of it to carry on with.
func (m *Manager) Remove(id string, keepState bool) (err error) {
	m.mu.Lock()
	a, ok := m.addons[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("addons: %s is not installed", id)
	}
	m.mu.Lock()
	version := a.rec.Version
	m.mu.Unlock()
	end, err := m.begin(id, version, "removing")
	if err != nil {
		return err
	}
	defer func() { end(err) }()
	m.stop(a)
	m.fw.closeFor(id)
	m.mu.Lock()
	rec, man := a.rec, a.man
	m.mu.Unlock()
	if !RunsAsRoot(man.Root) {
		cleanUp(credFor(rec.UID), id)
	}
	dir := m.dir(id)
	if keepState {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Name() != "state" && e.Name() != "uid" {
				if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
					return err
				}
			}
		}
	} else if err := os.RemoveAll(dir); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.addons, id)
	m.mu.Unlock()
	slog.Info("addons: removed", "id", id, "kept_state", keepState)
	m.changed(true)
	return nil
}

// Status is an installed add-on, for the card and Home Assistant.
type Status struct {
	ID, Name, Author, Version string
	Enabled                   bool
	State, Problem            string
	Firewall                  string // why its ports may not be open
	AsRoot                    bool
	Ports                     []catalog.Port
	Page                      string // with {device} in it
	Repo                      string
	Withdrawn                 string // why the catalog no longer lists it, or this version
	Update                    *Offer
	RolledBack                string
	Log                       string // its log file

	// Working is what an install, update or removal of it is doing now ("" for nothing), and At how far
	// its download has got, 0 to 1.
	Working string
	At      float32
}

// Installed is every installed add-on, by name.
func (m *Manager) Installed() []Status {
	m.mu.Lock()
	list := make([]*addon, 0, len(m.addons))
	for _, a := range m.addons {
		list = append(list, a)
	}
	m.mu.Unlock()
	out := make([]Status, 0, len(list))
	for _, a := range list {
		m.mu.Lock()
		s := Status{ID: a.rec.ID, Name: a.rec.Name, Author: a.rec.Author, Version: a.rec.Version, Enabled: a.rec.Enabled,
			State: a.state, Problem: a.problem, Firewall: a.firewall, AsRoot: RunsAsRoot(a.man.Root),
			Ports: a.man.Ports, Page: a.man.Page, RolledBack: a.rec.RolledBack,
			Log: filepath.Join(m.dir(a.rec.ID), "log")}
		if m.busy != nil && m.busy.ID == a.rec.ID {
			s.Working, s.At = m.busy.What, m.busy.At
		}
		rec := a.rec
		m.mu.Unlock()
		entry, _, o := m.offer(rec)
		s.Update, s.Repo = o, entry.Repo
		if entry.ID != "" {
			if entry.Withdrawn != "" {
				s.Withdrawn = entry.Withdrawn
			} else if v, ok := entry.Version(rec.Version); ok && v.Withdrawn != "" {
				s.Withdrawn = v.Withdrawn
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Listing is an add-on in the catalog, for the card's Available list: the newest version that runs
// here, or when none does, the newest listed and why it does not. Shown is what the card shows for it
// (catalog.Shown), which the install form carries back. Files are the extra files this device's build
// of it brings, which the card lists above the box.
type Listing struct {
	Addon     catalog.Addon
	Version   catalog.Version
	Files     []catalog.File
	Runs      bool
	Why       string
	AsRoot    bool
	Installed bool
	Shown     string

	// NeedsNewer is an add-on with no version this build can read (catalog.Addon.NeedsNewer): Version
	// is only the newest one's number, and the card says it needs a newer TECHO5.
	NeedsNewer bool
}

// newestOf is the newest of versions.
func newestOf(versions []string) string {
	best := ""
	for _, v := range versions {
		if best == "" || (v != best && update.Newer(v, best)) {
			best = v
		}
	}
	return best
}

// Available is the catalog's add-ons, listed ones only, by name.
func (m *Manager) Available() []Listing {
	cat, ok := m.cat.get()
	if !ok {
		return nil
	}
	expired := m.cat.expired()
	m.mu.Lock()
	installed := map[string]bool{}
	for id := range m.addons {
		installed[id] = true
	}
	m.mu.Unlock()
	d := m.device()
	var out []Listing
	for _, a := range cat.Addons {
		if a.Withdrawn != "" {
			continue
		}
		l := Listing{Addon: a, Installed: installed[a.ID]}
		if v, ok := a.Newest(d); ok {
			l.Version, l.Runs = v, true
			if ok, why := fits(v.MemoryMB); !ok {
				l.Runs, l.Why = false, why
			}
		} else if v, ok := a.Latest(); ok {
			_, l.Why = v.RunsOn(d)
			l.Version = v
		} else if a.NeedsNewer() {
			// Every version requires something this build does not know: the add-on is listed for a
			// newer TECHO5, which says so rather than leaving it out as though it were not there.
			l.NeedsNewer, l.Why = true, "needs a newer TECHO5"
			l.Version = catalog.Version{Version: newestOf(a.Skipped)}
		} else {
			continue
		}
		if expired && l.Runs {
			l.Runs, l.Why = false, "the catalog this device has is out of date; it offers nothing until a fresh one is fetched"
		}
		l.AsRoot = RunsAsRoot(l.Version.Root)
		l.Files = l.Version.FilesFor(d.Arch)
		l.Shown = catalog.Shown(a, l.Version)
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addon.Name < out[j].Addon.Name })
	return out
}

// Busy is the install, update or removal running now, if one is.
func (m *Manager) Busy() (job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy == nil {
		return job{}, false
	}
	return *m.busy, true
}

// LastProblem is why the last install, update or removal did not go through, if it did not.
func (m *Manager) LastProblem() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastJob
}

// CatalogState is when the catalog was last fetched, why the last fetch failed if it did, whether
// there is a catalog at all, and when the one there is expires.
func (m *Manager) CatalogState() (fetched time.Time, problem string, have bool, expires time.Time) {
	fetched, problem = m.cat.state()
	c, have := m.cat.get()
	return fetched, problem, have, c.Expires
}

// InstalledUID is whether uid is the user an installed add-on that is switched on runs as.
func (m *Manager) InstalledUID(uid uint32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.addons {
		if a.rec.UID == uid && a.rec.Enabled {
			return true
		}
	}
	return false
}

// Tail is the last lines of an installed add-on's log.
func (m *Manager) Tail(id string, n int) []string {
	if !catalog.ValidID(id) {
		return nil
	}
	return tailLog(filepath.Join(m.dir(id), "log"), n)
}
