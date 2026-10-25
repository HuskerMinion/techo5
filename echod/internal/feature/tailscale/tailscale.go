//go:build !dot

// Package tailscale puts the device on its owner's tailnet (docs/tailscale.md), so that a Home
// Assistant elsewhere can run it, and its setup page can be reached from afar while Remote setup is on.
//
// It runs Tailscale's own daemon, tailscaled, shipped in the image, and speaks to it over the local
// API tailscaled serves on a unix socket. Tailscale keeps the device's identity on the tailnet in its
// state directory, so a device joined once rejoins by itself after every restart; the only thing
// kept here is whether it runs. A device that has never been given a key runs nothing.
package tailscale

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Network, Get(), component.Order(55),
		component.Supervise(service.Restart(5*time.Second, 5*time.Minute)))
	// A diagnostics bundle names neither this device's tailnet name nor its address there.
	diag.TailnetNames = func() []string {
		s := Get().State()
		return []string{s.Name, s.IP}
	}
	// Connected, a tailnet address is from away whatever the interface is called (web.Away).
	web.TailscaleUp = func() bool { s := Get().State(); return s.Running && s.State == "Running" }
}

// Where tailscaled lives and keeps its state. The binary is the image's; a copy in the state
// directory is used when the image has none, which is how a device is tried before an image carries it.
var (
	stateDir = filepath.Join(layout.StateDir, "tailscale")
	sockPath = "/run/techo5-tailscaled.sock"
	binaries = []string{"/usr/local/bin/tailscaled", filepath.Join(stateDir, "tailscaled")}
)

// pollEvery is how often the status is read while it runs, which is what the sensor goes by.
const pollEvery = 15 * time.Second

// Status is where the device stands.
type Status struct {
	On      bool   // meant to run
	Running bool   // tailscaled is running
	State   string // tailscaled's own: NeedsLogin, Running, and so on
	Name    string // this device's name on the tailnet
	IP      string // its tailnet IPv4 address
	Problem string // why it is not on the tailnet, when it is not
}

type Feature struct {
	sw     *esphome.Switch
	status *esphome.TextSensor

	// Changed fires when the Status does; listeners must not block.
	Changed hook.Hook[Status]

	mu     sync.Mutex
	pub    sync.Mutex // held while a change is made and published
	st     Status
	api    *localAPI
	reload chan struct{}
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		f := &Feature{
			sw: &esphome.Switch{Base: esphome.Base{ObjectID: "tailscale", Name: "Tailscale",
				Icon: "mdi:vpn", Category: esphome.CategoryConfig}},
			status: &esphome.TextSensor{Base: esphome.Base{ObjectID: "tailscale_status", Name: "Tailscale status",
				Icon: "mdi:vpn", Category: esphome.CategoryDiagnostic}},
			api:    newLocalAPI(sockPath),
			reload: make(chan struct{}, 1),
		}
		f.sw.OnCommand = func(on bool) {
			if err := f.SetOn(on); err != nil {
				slog.Warn("tailscale: switch", "err", err)
			}
		}
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "tailscale" }

// Entities are the switch and the status, on an image that carries Tailscale: none on one without it,
// such as the Panel's.
func (f *Feature) Entities() []esphome.Entity {
	if !Available() {
		return nil
	}
	return []esphome.Entity{f.sw, f.status}
}

// Available is whether Tailscale can run here: the image carries it, and the kernel is 64-bit. The
// binary is a static arm64 one, so a Show 8 2nd gen on Amazon's 32-bit kernel has the file and could
// never run it (a 64-bit ARM kernel says aarch64 to the 32-bit processes on it too; a 32-bit one never
// does). Decided once; a variable, for tests.
var Available = sync.OnceValue(func() bool {
	var u unix.Utsname
	return findBinary() != "" && unix.Uname(&u) == nil && unix.ByteSliceToString(u.Machine[:]) == "aarch64"
})

func (f *Feature) Restore(c config.Config) {
	f.set(func(s *Status) { s.On = c.Tailscale.On })
}

// State is a copy of where the device stands.
func (f *Feature) State() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

// SetOn runs Tailscale or stops it. Stopping keeps the device's identity, so on again rejoins by
// itself; a device never given a key has nothing to rejoin with and says so.
//
// On needs Home Assistant's link to have an encryption key of its own, however it is asked for: over
// the tailnet its port is open to every node allowed to reach the device, and with no key it would
// take commands from any of them, in the clear.
func (f *Feature) SetOn(on bool) error {
	if on && !Available() {
		return errors.New("tailscale: not on this device (its image has none, or its kernel is 32-bit)")
	}
	if on && !encrypted() {
		f.sw.Set(f.State().On)
		return errors.New("tailscale: set an API encryption key first; over the tailnet the device's Home Assistant link would otherwise be open in the clear")
	}
	if err := config.Set().Tailscale().On(on); err != nil {
		return err
	}
	f.set(func(s *Status) { s.On = on })
	slog.Info("tailscale: switched", "on", on)
	f.poke()
	return nil
}

// Login joins the tailnet with an auth key, turning Tailscale on first if it was off. The key is
// handed to tailscaled and not kept anywhere here: what tailscaled keeps is the device's own identity.
func (f *Feature) Login(ctx context.Context, authKey string) error {
	authKey = strings.TrimSpace(authKey)
	if !strings.HasPrefix(authKey, "tskey-") {
		return errors.New("tailscale: that is not an auth key (they start tskey-auth-)")
	}
	if !encrypted() {
		return errors.New("tailscale: set an API encryption key first; over the tailnet the device's Home Assistant link would otherwise be open in the clear")
	}
	if !f.State().On {
		if err := f.SetOn(true); err != nil {
			return err
		}
	}
	// tailscaled may be starting: give it a moment to answer before the key is sent.
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := f.api.status(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tailscale: not running yet: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err := f.api.login(ctx, authKey, hostname(config.Get().Device.Name)); err != nil {
		return err
	}
	slog.Info("tailscale: joining the tailnet", "name", hostname(config.Get().Device.Name))
	f.poke()
	// tailscaled takes the key without saying whether the tailnet will: wait to see it connect, so a key
	// that is used up or expired is an answer here rather than a device that quietly never joins.
	for {
		st, err := f.api.status(ctx)
		if err == nil && st.BackendState == "Running" {
			return nil
		}
		select {
		case <-ctx.Done():
			if err == nil && st.BackendState == "NeedsLogin" {
				return errors.New("tailscale: the tailnet did not take the key; it may be used up or expired: make a new one")
			}
			return fmt.Errorf("tailscale: the key was handed over, but the device has not connected yet (%s)", cmpOr(st.BackendState, "not answering"))
		case <-time.After(time.Second):
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Actions: tailscale_login joins the tailnet with a key. Only over an encrypted Home Assistant link,
// since the key would otherwise cross the network in the clear.
// None where Tailscale can't run (Available).
func (f *Feature) Actions() []*esphome.Action {
	if !Available() {
		return nil
	}
	return []*esphome.Action{{
		Name: "tailscale_login",
		Args: []esphome.Arg{{Name: "auth_key", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if !encrypted() {
				return nil, errors.New("tailscale: set an API encryption key first; the auth key would otherwise cross the network in the clear")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return nil, f.Login(ctx, c.String("auth_key"))
		},
	}}
}

func (f *Feature) poke() {
	select {
	case f.reload <- struct{}{}:
	default:
	}
}

// Run keeps tailscaled running while it is meant to, and its status read.
func (f *Feature) Run(ctx context.Context) error {
	if !Available() {
		// Nothing to run here, ever: not an error to restart on.
		<-ctx.Done()
		return nil
	}
	for {
		if !f.State().On {
			f.set(func(s *Status) { s.Running, s.State, s.IP, s.Problem = false, "", "", "" })
			select {
			case <-ctx.Done():
				return nil
			case <-f.reload:
				continue
			}
		}
		if err := f.runOnce(ctx); err != nil {
			// Nothing tailscaled said still holds, not even the name.
			f.set(func(s *Status) { s.Running, s.State, s.Name, s.IP, s.Problem = false, "", "", "", err.Error() })
			return err // the supervisor waits longer each time
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// runOnce runs tailscaled until it is turned off, ctx ends, or tailscaled exits.
func (f *Feature) runOnce(ctx context.Context) error {
	bin := findBinary()
	if bin == "" {
		return errors.New("tailscale is not in this image")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	// A tailscaled left running by an echod that died without stopping it holds the tunnel, and this
	// one could never start beside it: it goes first.
	stopLeftovers()
	_ = os.Remove(sockPath)

	// tailscaled is told to stop when the thread that started it ends - and that thread is kept for
	// as long as it runs - so an echod that crashes or is killed does not leave it behind.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	rctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(rctx, bin,
		"--state="+filepath.Join(stateDir, "tailscaled.state"),
		"--statedir="+stateDir,
		"--socket="+sockPath,
		"--tun="+web.TunName,
		"--no-logs-no-support")
	cmd.Stdout, cmd.Stderr = logWriter{}, logWriter{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tailscale: starting: %w", err)
	}
	slog.Info("tailscale: started", "binary", bin)
	f.set(func(s *Status) { s.Running, s.Problem = true, "" })
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// tailscaled takes a moment to open its socket and connect: ask every second until it has, for a while,
	// saying "Starting" rather than trouble, so the status is right as soon as there is one.
	f.set(func(s *Status) { s.State, s.Problem = "", "" })
	for deadline := time.Now().Add(30 * time.Second); !f.settled(ctx) && time.Now().Before(deadline); {
		select {
		case err := <-exited:
			if ctx.Err() != nil || !f.State().On {
				return nil
			}
			return fmt.Errorf("tailscale: tailscaled exited: %v", err)
		case <-ctx.Done():
			stop()
			<-exited
			return nil
		case <-f.reload:
			if !f.State().On {
				stop()
				<-exited
				slog.Info("tailscale: stopped")
				return nil
			}
		case <-time.After(time.Second):
		}
	}
	f.poll(ctx)
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			if ctx.Err() != nil || !f.State().On {
				return nil
			}
			return fmt.Errorf("tailscale: tailscaled exited: %v", err)
		case <-ctx.Done():
			stop()
			<-exited
			return nil
		case <-f.reload:
			if !f.State().On {
				stop()
				<-exited
				slog.Info("tailscale: stopped")
				return nil
			}
			f.poll(ctx)
		case <-tick.C:
			f.poll(ctx)
		}
	}
}

// settled is whether tailscaled is answering and has got past starting up.
func (f *Feature) settled(ctx context.Context) bool {
	actx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	st, err := f.api.status(actx)
	return err == nil && st.BackendState != "NoState" && st.BackendState != "Starting"
}

// poll reads the status and says what it found.
func (f *Feature) poll(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := f.api.status(pctx)
	if err != nil {
		f.set(func(s *Status) {
			s.State, s.Name, s.IP, s.Problem = "", "", "", "tailscaled is not answering"
		})
		return
	}
	f.set(func(s *Status) {
		s.State, s.Name, s.IP, s.Problem = st.BackendState, "", "", ""
		if st.Self != nil {
			s.Name = strings.TrimSuffix(strings.SplitN(st.Self.DNSName, ".", 2)[0], ".")
			s.IP = firstIPv4(st.Self.TailscaleIPs)
		}
		if st.BackendState == "NeedsLogin" {
			s.Problem = "needs a key: the tailscale_login action, or the setup page"
		}
	})
}

func firstIPv4(ips []string) string {
	for _, ip := range ips {
		if !strings.Contains(ip, ":") {
			return ip
		}
	}
	return ""
}

func (f *Feature) set(fn func(s *Status)) {
	// One change is published before the next is made, so Home Assistant and the screen never end
	// on an older one than the last.
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
		slog.Info("tailscale: " + now)
	}
	f.status.Set(now)
	f.Changed.Emit(st)
}

// statusText is the sensor: what somebody looking at Home Assistant wants to know about it.
func statusText(s Status) string {
	switch {
	case !s.On:
		return "Off"
	case s.Problem != "" && s.State != "NeedsLogin":
		return "Trouble: " + s.Problem
	case s.State == "NeedsLogin":
		return "Needs a key"
	case s.State == "Running" && s.Name != "":
		return fmt.Sprintf("Connected as %s (%s)", s.Name, s.IP)
	case s.State != "" && s.State != "NoState" && s.State != "Starting":
		return s.State
	}
	return "Starting"
}

// hostname is the device's name as a tailnet name: lower case, letters and digits, hyphens between.
func hostname(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '\'':
			// "Kid's Room" is kids-room, not kid-s-room
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	h := strings.TrimSuffix(b.String(), "-")
	if h == "" {
		return "techo5"
	}
	return h
}

func findBinary() string {
	for _, b := range binaries {
		if info, err := os.Stat(b); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return b
		}
	}
	return ""
}

// encrypted reports whether the Home Assistant link has a key of its own rather than the reserved
// all-zeros one an unprovisioned device answers with. A variable, for tests.
var encrypted = func() bool {
	b, err := os.ReadFile(layout.KeyPath)
	if err != nil {
		return false
	}
	k, err := esphome.ParsePSK(strings.TrimSpace(string(b)))
	return err == nil && !k.IsZero()
}

// logWriter puts tailscaled's own output in the daemon's log at Debug: it is chatty, and what matters
// of it reaches the status sensor.
type logWriter struct{}

func (logWriter) Write(b []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.Contains(line, "tskey-") {
			slog.Debug("tailscaled", "said", line)
		}
	}
	return len(b), nil
}

// stopLeftovers ends any tailscaled still running on this device's socket - one that outlived the
// echod that started it - and waits a little for it to let go of the tunnel.
func stopLeftovers() {
	want := []byte("--socket=" + sockPath)
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || !bytes.Contains(cmdline, want) {
			continue
		}
		slog.Warn("tailscale: stopping a tailscaled left running", "pid", pid)
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
