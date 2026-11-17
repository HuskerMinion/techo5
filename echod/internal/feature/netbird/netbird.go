//go:build !dot

// Package netbird puts the device on its owner's NetBird network (docs/netbird.md): the other way, beside
// Tailscale, for a Home Assistant elsewhere to run it, and for its setup page to be reached from afar
// while Remote setup is on. Everything the device lets in over Tailscale it lets in over NetBird, and
// nothing more: the firewall's tailnet chain is on NetBird's interface too, and the web port, the API and
// the rest take a connection over it as from away (web.Away).
//
// NetBird's client is not in the image. It is downloaded when NetBird is first turned on, checked
// against the size and SHA-256 the daemon is built with (pinned.go), and kept in the state directory.
// It runs as its own daemon, spoken to with its own command line over a unix socket. It keeps the
// device's identity on the network in its state directory, so a device joined once rejoins by itself;
// what is kept here is whether it runs, and the management server it was joined to.
//
// It is started to take nothing from the network: no DNS, no routes, no SSH server. Its own firewall is
// on, so the owner's access policies decide which ports each peer reaches, under ours: the firewall's
// tailnet chain stays the ceiling (firewallOn). WireGuard runs in the client rather than the kernel, and
// routing the old way, which every one of these kernels has.
package netbird

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tailscale"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/service"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func init() {
	component.Register(component.Network, Get(), component.Order(56),
		component.Supervise(service.Restart(5*time.Second, 5*time.Minute)))
	// A diagnostics bundle names neither this device's NetBird name nor its address, as for Tailscale.
	names := diag.TailnetNames
	diag.TailnetNames = func() []string {
		s := Get().State()
		return append(names(), s.Name, s.IP)
	}
	web.NetbirdUp = func() bool { s := Get().State(); return s.Running && s.State == connected }
}

// DefaultManagement is NetBird's own management server, for a device joined without one of its own.
const DefaultManagement = "https://api.netbird.io:443"

// Where the client is kept and keeps its state, and the socket its daemon answers on.
var (
	stateDir   = filepath.Join(layout.StateDir, "netbird")
	sockPath   = "/run/techo5-netbird.sock"
	daemonAddr = "--daemon-addr=unix://" + sockPath
)

// pollEvery is how often the status is read while it runs, which is what the sensor goes by.
const pollEvery = 15 * time.Second

// NetBird's own words for where its daemon stands.
const (
	connected  = "Connected"
	needsLogin = "NeedsLogin"
)

// Status is where the device stands.
type Status struct {
	On          bool    // meant to run
	Downloading bool    // fetching the client
	Got         float32 // how much of it, 0 to 1
	Running     bool    // the daemon is running
	State       string  // the daemon's own: NeedsLogin, Connecting, Connected, and so on
	Name        string  // this device's name on the network
	IP          string  // its address there
	Problem     string  // why it is not on the network, when it is not
	Joining     bool    // a setup key is held in memory, to join when the download is done
	JoinProblem string  // why a join that waited for the download did not happen, to give the key again
}

type Feature struct {
	sw     *esphome.Switch
	status *esphome.TextSensor

	// Changed fires when the Status does; listeners must not block.
	Changed hook.Hook[Status]

	mu     sync.Mutex
	pub    sync.Mutex // held while a change is made and published
	st     Status
	reload chan struct{}

	// A setup key given during the download, held here only: never on disk, in a log or a status. One
	// goroutine (waitParked) joins with it when the client answers, and clears it on every way out.
	pk      sync.Mutex
	parked  *parkedJoin
	waiting bool
}

type parkedJoin struct{ key, mgmt string }

// Parked is what Login answers when the client is still downloading: the key is held and the join is
// made by itself when the download is done. It is not a failure.
type Parked struct{ Got float32 }

func (p *Parked) Error() string {
	return fmt.Sprintf("Downloading NetBird (%d%%). It will join by itself when the download is done.", int(p.Got*100))
}

// Seams for tests.
var (
	parkPoll    = time.Second
	parkTimeout = 10 * time.Minute
	joinTimeout = 2 * time.Minute
	// problemGrace is how long a download Problem must last before the held key is dropped for it.
	problemGrace = 30 * time.Second
)

// pendingMark is a file with no key in it, saying a join was waiting on the download, so a restart can
// say so.
func pendingMark() string { return filepath.Join(stateDir, "join-pending") }

const restartedMsg = "the device restarted before NetBird finished downloading; give the key again"

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{
			sw: &esphome.Switch{Base: esphome.Base{ObjectID: "netbird", Name: "NetBird",
				Icon: "mdi:vpn", Category: esphome.CategoryConfig}},
			status: &esphome.TextSensor{Base: esphome.Base{ObjectID: "netbird_status", Name: "NetBird status",
				Icon: "mdi:vpn", Category: esphome.CategoryDiagnostic}},
			reload: make(chan struct{}, 1),
		}
		f.sw.OnCommand = func(on bool) {
			if err := f.SetOn(on); err != nil {
				slog.Warn("netbird: switch", "err", err)
			}
		}
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "netbird" }

// Entities are the switch and the status, where NetBird can run (Available).
func (f *Feature) Entities() []esphome.Entity {
	if !Available() {
		return nil
	}
	return []esphome.Entity{f.sw, f.status}
}

// Available is whether NetBird can run here: wherever Tailscale can, an image that carries it on a
// 64-bit kernel, since the client is the same kind of static arm64 program. A variable, for tests.
var Available = func() bool { return tailscale.Available() }

func (f *Feature) Restore(c config.Config) {
	f.set(func(s *Status) { s.On = c.Netbird.On })
	if _, err := os.Stat(pendingMark()); err == nil {
		_ = os.Remove(pendingMark())
		f.set(func(s *Status) { s.JoinProblem = restartedMsg })
	}
}

// State is a copy of where the device stands.
func (f *Feature) State() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

var errOffAgain = errors.New("netbird: NetBird was switched off before the key could be used")

var errUnkeyed = errors.New("netbird: set an API encryption key first; over NetBird the device's Home Assistant link would otherwise be open in the clear")

// SetOn runs NetBird or stops it. Stopping keeps the device's identity, so on again rejoins by itself.
// On the first time, it downloads the client.
//
// On needs Home Assistant's link to have an encryption key of its own, as Tailscale does, and for the
// same reason: over the network its port is open to every peer allowed to reach the device.
func (f *Feature) SetOn(on bool) error {
	if on && !Available() {
		return errors.New("netbird: not on this device (it can't run Tailscale either)")
	}
	if on && !encrypted() {
		f.sw.Set(f.State().On)
		return errUnkeyed
	}
	if err := config.Set().Netbird().On(on); err != nil {
		return err
	}
	if !on {
		f.dropParked("")
	}
	f.set(func(s *Status) { s.On = on })
	slog.Info("netbird: switched", "on", on)
	f.poke()
	return nil
}

// Login joins a NetBird network with a setup key, at a management server (NetBird's own when empty),
// turning NetBird on first if it was off. The key is handed to the client and not kept anywhere: what
// the client keeps is the device's own identity.
func (f *Feature) Login(ctx context.Context, setupKey, management string) error {
	return f.login(ctx, setupKey, management, false)
}

// login is Login; again is a join made from the held key, which is not held a second time.
func (f *Feature) login(ctx context.Context, setupKey, management string, again bool) error {
	setupKey = strings.TrimSpace(setupKey)
	if setupKey == "" || strings.ContainsAny(setupKey, " \t\r\n") {
		return errors.New("netbird: that is not a setup key")
	}
	mgmt, err := managementURL(management)
	if err != nil {
		return err
	}
	if !encrypted() {
		return errUnkeyed
	}
	if !f.State().On {
		if again {
			return errOffAgain // switched off while the key waited: not turned on behind the owner's back
		}
		if err := f.SetOn(true); err != nil {
			return err
		}
	}
	// The client may be downloading, or its daemon starting: wait for it to answer. A download is not
	// waited out: Home Assistant's connection waits on an action, so the key is held in memory and the
	// join is made when the download is done (park).
	for {
		if _, err := status(ctx); err == nil {
			break
		}
		if st := f.State(); st.Downloading && !again {
			f.park(setupKey, mgmt)
			return &Parked{Got: st.Got}
		}
		select {
		case <-ctx.Done():
			if st := f.State(); st.Downloading && !again {
				f.park(setupKey, mgmt)
				return &Parked{Got: st.Got}
			}
			return errors.New("netbird: the client is not running yet; the NetBird status says why")
		case <-time.After(time.Second):
		}
	}
	if again && !f.State().On {
		return errOffAgain
	}
	name := tailscale.Hostname(config.Get().Device.Name)
	out, err := cli(ctx, []string{"NB_SETUP_KEY=" + setupKey}, upArgs(mgmt, name)...)
	if err != nil {
		return fmt.Errorf("netbird: joining: %s", cmpOr(lastLine(scrub(out, setupKey)), err.Error()))
	}
	// The server's address only: its path or query could carry a token.
	shown := mgmt
	if u, err := url.Parse(mgmt); err == nil {
		shown = u.Scheme + "://" + u.Host
	}
	slog.Info("netbird: joining", "name", name, "management", shown)
	if err := config.Set().Netbird().Joined(mgmtSaved(mgmt)); err != nil {
		return err
	}
	f.set(func(s *Status) { s.JoinProblem = "" })
	f.poke()
	upAt := time.Now()
	for {
		st, err := status(ctx)
		if err == nil && st.DaemonStatus == connected && st.IP != "" {
			return nil
		}
		// Still asking for a key a few seconds after taking one: the management server turned it down.
		if err == nil && needsKey(st.DaemonStatus) && time.Since(upAt) > 5*time.Second {
			return errors.New("netbird: the management server did not take the key; it may be used up or expired: make a new one")
		}
		select {
		case <-ctx.Done():
			if err == nil && needsKey(st.DaemonStatus) {
				return errors.New("netbird: the management server did not take the key; it may be used up or expired: make a new one")
			}
			return fmt.Errorf("netbird: the key was handed over, but the device has not connected yet (%s)", cmpOr(st.DaemonStatus, "not answering"))
		case <-time.After(time.Second):
		}
	}
}

// park holds the key for the join that waits on the download; a newer key replaces an older one.
func (f *Feature) park(key, mgmt string) {
	f.pk.Lock()
	f.parked = &parkedJoin{key, mgmt}
	start := !f.waiting
	f.waiting = true
	f.pk.Unlock()
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.WriteFile(pendingMark(), nil, 0o600)
	f.set(func(s *Status) { s.Joining, s.JoinProblem = true, "" })
	slog.Info("netbird: a join waits for the download")
	if start {
		go f.waitParked()
	}
}

// dropParked forgets the held key; why, when not empty, is what the page says about it.
func (f *Feature) dropParked(why string) {
	f.pk.Lock()
	f.parked = nil
	f.pk.Unlock()
	_ = os.Remove(pendingMark())
	f.set(func(s *Status) {
		if s.Joining || why != "" {
			s.Joining, s.JoinProblem = false, why
		}
	})
}

// dropParkedIfNone ends the join like dropParked, but only when, under the same lock, no key is held: a
// key parked meanwhile keeps its mark and its Joining.
func (f *Feature) dropParkedIfNone(why string) {
	f.pk.Lock()
	if f.parked != nil {
		f.pk.Unlock()
		return
	}
	_ = os.Remove(pendingMark())
	f.set(func(s *Status) {
		if s.Joining || why != "" {
			s.Joining, s.JoinProblem = false, why
		}
	})
	f.pk.Unlock()
}

// takeParked hands over the held key and forgets it.
func (f *Feature) takeParked() *parkedJoin {
	f.pk.Lock()
	defer f.pk.Unlock()
	p := f.parked
	f.parked = nil
	return p
}

// waitParked waits for the download to finish and the client to answer, joins once with the held key,
// and lets go of it on every way out: joined, failed, the download failed, ten minutes, NetBird off. It
// ends only when, under the lock that park takes, no key is held: a Join while it was deciding to stop,
// or while it was joining, is never left without a waiter.
func (f *Feature) waitParked() {
	for f.joinParked() {
	}
}

// joinParked waits for the key held now and joins with it; false, with the waiter marked done, when none
// is held.
func (f *Feature) joinParked() bool {
	deadline := time.Now().Add(parkTimeout)
	var problemSince time.Time
	for {
		f.pk.Lock()
		if f.parked == nil {
			f.waiting = false
			f.pk.Unlock()
			return false // switched off, or taken
		}
		f.pk.Unlock()
		st := f.State()
		if !st.On {
			f.dropParked("")
			return true
		}
		if !st.Downloading {
			if _, err := status(context.Background()); err == nil {
				break
			}
			// The supervisor retries a failed download: a Problem is only believed when it lasts.
			if st.Problem != "" {
				if problemSince.IsZero() {
					problemSince = time.Now()
				}
				if time.Since(problemSince) >= problemGrace {
					f.dropParked("NetBird did not download: " + st.Problem + "; give the key again")
					return true
				}
			} else {
				problemSince = time.Time{}
			}
		} else {
			problemSince = time.Time{}
		}
		if time.Now().After(deadline) {
			f.dropParked("NetBird did not finish downloading in ten minutes; give the key again")
			return true
		}
		time.Sleep(parkPoll)
	}
	p := f.takeParked()
	if p == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	defer cancel()
	err := f.login(ctx, p.key, p.mgmt, true)
	p.key = ""
	why := ""
	if err != nil {
		slog.Warn("netbird: the join that waited for the download failed", "err", err.Error())
		why = err.Error() + "; give the key again"
	}
	// A newer key held while this one joined stays, for the next turn; only with none is the join over.
	f.dropParkedIfNone(why)
	return true
}

// upArgs joins the device: everything the network could give it is refused, and the client's own
// firewall kept on for the access policies (firewallOn). Each is said outright: the client keeps only
// what it is told.
func upArgs(mgmt, name string) []string {
	return []string{"up",
		"--management-url=" + mgmt, "--hostname=" + name, "--interface-name=" + web.NetbirdTun,
		"--disable-dns", "--disable-client-routes", "--disable-server-routes", "--disable-firewall=false", "--disable-ipv6",
		"--allow-server-ssh=false", "--enable-rosenpass=false", "--disable-auto-connect=false"}
}

// managementURL is a management server's URL as the client takes it: NetBird's own for none, and only
// http or https to a host.
func managementURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultManagement, nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", errors.New("netbird: the management server is a URL, such as https://netbird.example.com")
	}
	return u.String(), nil
}

// mgmtSaved is what config keeps: nothing for NetBird's own.
func mgmtSaved(mgmt string) string {
	if mgmt == DefaultManagement {
		return ""
	}
	return mgmt
}

// Actions: netbird_login joins with a setup key, at a management server of the owner's own if one is
// given. Only over an encrypted Home Assistant link, since the key would otherwise cross the network in
// the clear. None where NetBird can't run (Available).
func (f *Feature) Actions() []*esphome.Action {
	if !Available() {
		return nil
	}
	return []*esphome.Action{{
		Name: "netbird_login",
		Args: []esphome.Arg{{Name: "setup_key", Type: esphome.ArgString}, {Name: "management_url", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if !encrypted() {
				return nil, errors.New("netbird: set an API encryption key first; the setup key would otherwise cross the network in the clear")
			}
			// Home Assistant's connection waits on an action, so it is kept short, as Tailscale's is; a
			// download is not waited for (Login).
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := f.Login(ctx, c.String("setup_key"), c.String("management_url"))
			var p *Parked
			if errors.As(err, &p) {
				// Not a failure: the key is held and the join follows the download.
				slog.Info("netbird: " + p.Error())
				return nil, nil
			}
			return nil, err
		},
	}}
}

func (f *Feature) poke() {
	select {
	case f.reload <- struct{}{}:
	default:
	}
}

// Run keeps the client's daemon running while it is meant to, and its status read.
func (f *Feature) Run(ctx context.Context) error {
	if !Available() {
		<-ctx.Done()
		return nil
	}
	for {
		if !f.State().On {
			f.set(func(s *Status) { s.Running, s.Downloading, s.State, s.IP, s.Problem = false, false, "", "", "" })
			select {
			case <-ctx.Done():
				return nil
			case <-f.reload:
				continue
			}
		}
		err := f.fetchClient(ctx)
		if err == nil {
			err = f.runOnce(ctx)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			f.set(func(s *Status) {
				s.Running, s.Downloading, s.State, s.Name, s.IP, s.Problem = false, false, "", "", "", err.Error()
			})
			return err // the supervisor waits longer each time
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// binPath is where the pinned client is kept, by its version, so a daemon that pins a newer one fetches it.
func binPath() string { return filepath.Join(stateDir, "netbird-"+Version) }

// room is what the download needs free: the archive and the client unpacked beside it, with some to
// spare.
const room = 3 * 40 << 20

// fetchClient makes sure the pinned client is here: downloaded, checked and unpacked, unless it already
// is. Older versions go.
func (f *Feature) fetchClient(ctx context.Context) error {
	bin := binPath()
	if info, err := os.Stat(bin); err == nil && info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	var fs unix.Statfs_t
	if unix.Statfs(stateDir, &fs) == nil && int64(fs.Bavail)*int64(fs.Bsize) < room {
		return fmt.Errorf("not enough room to download NetBird (it needs %d MB)", room>>20)
	}
	slog.Info("netbird: downloading the client", "version", Version)
	f.set(func(s *Status) { s.Downloading, s.Got, s.Problem = true, 0, "" })
	archive := filepath.Join(stateDir, "netbird-"+Version+".tar.gz")
	last := float32(-1)
	err := download(ctx, client, archive, func(p float32) {
		// The sensor moves in tenths, not every packet.
		if p-last >= 0.1 || p == 1 {
			last = p
			f.set(func(s *Status) { s.Got = p })
		}
	})
	if err != nil {
		return fmt.Errorf("downloading NetBird: %w", err)
	}
	defer os.Remove(archive)
	if err := unpack(archive, bin); err != nil {
		return fmt.Errorf("unpacking NetBird: %w", err)
	}
	f.set(func(s *Status) { s.Downloading = false })
	old, _ := filepath.Glob(filepath.Join(stateDir, "netbird-*"))
	for _, o := range old {
		if o != bin && o != archive {
			_ = os.Remove(o)
		}
	}
	slog.Info("netbird: client ready", "version", Version)
	return nil
}

// download is update.Download; a variable, for tests.
var download = update.Download

// unpack puts the archive's client at bin and its license beside it: each written whole and then renamed,
// so a device stopped halfway has no half a client.
func unpack(archive, bin string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	got := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		var to string
		var mode os.FileMode
		switch h.Name {
		case "netbird":
			to, mode = bin, 0o755
		case "LICENSE":
			to, mode = filepath.Join(stateDir, "LICENSE"), 0o644
		default:
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a file", h.Name)
		}
		if err := writeWhole(to, mode, tr); err != nil {
			return err
		}
		got = got || h.Name == "netbird"
	}
	if !got {
		return errors.New("no client in it")
	}
	return nil
}

func writeWhole(to string, mode os.FileMode, r io.Reader) error {
	tmp := to + ".part"
	w, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		os.Remove(tmp)
		return err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, to)
}

// env is what the client is run with: its state in ours, and the choices that suit these kernels.
func env(extra ...string) []string {
	return append(append(os.Environ(),
		"NB_STATE_DIR="+filepath.Join(stateDir, "state"),
		// The command line keeps its own few files under the user's config: ours, not root's home.
		"HOME="+stateDir, "XDG_CONFIG_HOME="+filepath.Join(stateDir, "cli"),
		// WireGuard in the client: most of these kernels have no WireGuard module.
		"NB_WG_KERNEL_DISABLED=true",
		// Routing the old way, and no eBPF: these kernels may have neither policy routing nor BPF to spare.
		"NB_USE_LEGACY_ROUTING=true", "NB_DISABLE_EBPF_WG_PROXY=true",
		// Nothing sent to NetBird but what joining and connecting take.
		"NB_METRICS_PUSH_ENABLED=false",
	), extra...)
}

// cli runs the client's command line against its daemon, and answers what it said.
// A variable, for tests.
var cli = func(ctx context.Context, extraEnv []string, args ...string) ([]byte, error) {
	cmd := command(ctx, extraEnv, args...)
	return cmd.CombinedOutput()
}

func command(ctx context.Context, extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binPath(), append(args, daemonAddr, "--log-file=console")...)
	cmd.Env = env(extraEnv...)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// daemonStatus is the part of the client's status --json read here.
type daemonStatus struct {
	DaemonStatus string `json:"daemonStatus"`
	IP           string `json:"netbirdIp"`
	FQDN         string `json:"fqdn"`
	Management   struct {
		Connected bool   `json:"connected"`
		Error     string `json:"error"`
	} `json:"management"`
}

// status asks the daemon where it stands. A variable, for tests.
var status = func(ctx context.Context) (daemonStatus, error) {
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The answer only: the command line logs to stderr, and a line of that is not JSON.
	cmd := command(sctx, nil, "status", "--json")
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if err != nil {
		return daemonStatus{}, fmt.Errorf("%v: %s", err, lastLine(errOut.Bytes()))
	}
	return parseStatus(out)
}

func parseStatus(out []byte) (daemonStatus, error) {
	var st daemonStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return st, err
	}
	// The address comes with its network's prefix length.
	st.IP, _, _ = strings.Cut(st.IP, "/")
	return st, nil
}

func needsKey(s string) bool {
	return s == needsLogin || s == "LoginFailed" || s == "SessionExpired"
}

// runOnce runs the daemon until it is turned off, ctx ends, or it exits.
func (f *Feature) runOnce(ctx context.Context) error {
	if err := tailscale.Fenced(web.NetbirdTun); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "state"), 0o700); err != nil {
		return err
	}
	// A daemon left running by an echod that died without stopping it holds the interface and the
	// socket: it goes first.
	stopLeftovers()
	_ = os.Remove(sockPath)
	if err := firewallOn(filepath.Join(stateDir, "state", "default.json")); err != nil {
		slog.Warn("netbird: turning its firewall on", "err", err)
	}

	// The daemon is told to stop when the thread that started it ends, and that thread is kept for as
	// long as it runs, so an echod that crashes or is killed does not leave it behind.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	rctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(rctx, binPath(), "service", "run", daemonAddr,
		"--log-file=console", "--log-level=info")
	cmd.Env = env()
	cmd.Stdout, cmd.Stderr = logWriter{}, logWriter{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("netbird: starting: %w", err)
	}
	slog.Info("netbird: started", "version", Version)
	f.set(func(s *Status) { s.Running, s.State, s.Problem = true, "", "" })
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	f.poll(ctx)
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	// Quicker at first: the daemon takes a moment to open its socket and connect.
	quick := time.NewTicker(2 * time.Second)
	defer quick.Stop()
	quickUntil := time.Now().Add(time.Minute)
	for {
		select {
		case err := <-exited:
			if ctx.Err() != nil || !f.State().On {
				return nil
			}
			return fmt.Errorf("netbird: the client exited: %v", err)
		case <-ctx.Done():
			stop()
			<-exited
			return nil
		case <-f.reload:
			if !f.State().On {
				stop()
				<-exited
				slog.Info("netbird: stopped")
				return nil
			}
			f.poll(ctx)
		case <-quick.C:
			if time.Now().Before(quickUntil) && f.State().State != connected {
				f.poll(ctx)
			}
		case <-tick.C:
			f.poll(ctx)
		}
	}
}

// firewallOn turns the client's own firewall on in a profile joined with it off (--disable-firewall, as
// every device was joined at first), so such a device gets its access policies too. The daemon reads the profile
// when it starts, which is after this. Nothing to do without a profile, or with the firewall on already.
//
// The client's firewall can only narrow what ours lets in. On the userspace interface it filters what
// it decrypts before the kernel sees it. A host firewall it could use instead is only ever nftables: the
// image's iptables is the nftables one (and the athena kernel has no nftables), while ours is
// iptables-legacy, separate tables a packet has to pass as well. So it never gets ahead of the tailnet
// chain (tailscale.Fenced) that NetBird does not start without.
func firewallOn(profile string) error {
	b, err := os.ReadFile(profile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var c map[string]json.RawMessage
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("%s: %w", profile, err)
	}
	if string(c["DisableFirewall"]) != "true" {
		return nil
	}
	c["DisableFirewall"] = json.RawMessage("false")
	out, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return err
	}
	slog.Info("netbird: its firewall turned on, for the access policies")
	return writeWhole(profile, 0o600, bytes.NewReader(out))
}

// poll reads the status and says what it found.
func (f *Feature) poll(ctx context.Context) {
	st, err := status(ctx)
	if err != nil {
		f.set(func(s *Status) { s.State, s.Name, s.IP, s.Problem = "", "", "", "" })
		return
	}
	f.set(func(s *Status) {
		s.State, s.Name, s.IP, s.Problem = st.DaemonStatus, "", "", ""
		if st.DaemonStatus == connected {
			s.Name = strings.TrimSuffix(strings.SplitN(st.FQDN, ".", 2)[0], ".")
			s.IP = st.IP
		}
		switch {
		case needsKey(st.DaemonStatus):
			s.Problem = "needs a setup key: the netbird_login action, or the setup page"
		case st.DaemonStatus != connected && st.Management.Error != "":
			s.Problem = "the management server: " + st.Management.Error
		}
	})
}

func (f *Feature) set(fn func(s *Status)) {
	// One change is published before the next is made, so Home Assistant and the screen never end on an
	// older one than the last.
	f.pub.Lock()
	defer f.pub.Unlock()
	f.mu.Lock()
	was := statusText(f.st)
	fn(&f.st)
	st := f.st
	f.mu.Unlock()
	f.sw.Set(st.On)
	now := statusText(st)
	if now != was {
		slog.Info("netbird: " + now)
	}
	f.status.Set(now)
	f.Changed.Emit(st)
}

// statusText is the sensor: what somebody looking at Home Assistant wants to know about it.
func statusText(s Status) string {
	switch {
	case !s.On:
		return "Off"
	case s.Downloading:
		return fmt.Sprintf("Downloading NetBird (%d%%)", int(s.Got*100))
	case needsKey(s.State):
		return "Needs a setup key"
	case s.Problem != "":
		return "Trouble: " + s.Problem
	case s.State == connected && s.Name != "":
		return fmt.Sprintf("Connected as %s (%s)", s.Name, s.IP)
	case s.State != "":
		return s.State
	}
	return "Starting"
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// lastLine is the last thing a command said, which is where it says what went wrong.
func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func scrub(b []byte, secret string) []byte {
	if secret == "" {
		return b
	}
	return bytes.ReplaceAll(b, []byte(secret), []byte("<setup key>"))
}

// encrypted is tailscale.LinkKeyed; a variable, for tests.
var encrypted = tailscale.LinkKeyed

// logWriter puts the daemon's own output in our log at Debug: it is chatty, and what matters of it
// reaches the status sensor.
type logWriter struct{}

func (logWriter) Write(b []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			slog.Debug("netbird", "said", line)
		}
	}
	return len(b), nil
}

// stopLeftovers ends any daemon still running on this device's socket, one that outlived the echod that
// started it, and waits a little for it to let go of the interface.
func stopLeftovers() {
	want := []byte(daemonAddr)
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || !bytes.Contains(cmdline, want) || !bytes.Contains(cmdline, []byte("service")) {
			continue
		}
		slog.Warn("netbird: stopping a daemon left running", "pid", pid)
		_ = syscall.Kill(pid, syscall.SIGTERM)
		pids = append(pids, pid)
	}
	for _, pid := range pids {
		for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
