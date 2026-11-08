package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"golang.org/x/sys/unix"
)

var (
	soloistRuntime  = "/data/techo5-soloist/runtime"
	soloistdPath    = "/data/techo5-soloist/soloistd"
	soloistExec     = "/data/techo5-soloist/exec"
	soloistRevision atomic.Uint64
	soloistConfigMu sync.Mutex
	soloistState    = struct {
		sync.RWMutex
		text string
	}{text: "Not started"}
)

// Separate from exported settings and the old librespot cache. Disabled does not erase pairing.
var soloistHome = func() string { return filepath.Join(filepath.Dir(layout.StateDir), "techo5-soloist") }

type soloistConfig struct {
	DeviceName    string `json:"device_name"`
	APIKey        string `json:"api_key"`
	DataDir       string `json:"data_dir"`
	CacheDir      string `json:"cache_dir"`
	CacheSizeMB   int    `json:"cache_size_mb"`
	WebSocket     string `json:"websocket"`
	InitialVolume int    `json:"initial_volume"`
}

func soloistConfigPath() string {
	return filepath.Join(soloistHome(), "config", "soloistd", "config.json")
}
func soloistData() string { return filepath.Join(soloistHome(), "data", "soloist") }

func soloistLibraries() (string, string) {
	if _, err := os.Stat(filepath.Join(soloistRuntime, "usr/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1")); err == nil {
		return filepath.Join(soloistRuntime, "usr/lib/aarch64-linux-gnu"), "ld-linux-aarch64.so.1"
	}
	return filepath.Join(soloistRuntime, "usr/lib/arm-linux-gnueabihf"), "ld-linux-armhf.so.3"
}

func HasSoloist() bool {
	libs, loader := soloistLibraries()
	for _, p := range []string{soloistdPath, soloistExec, filepath.Join(libs, loader),
		filepath.Join(soloistRuntime, "usr/bin/patchelf"), filepath.Join(soloistRuntime, "usr/bin/pulseaudio")} {
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func readSoloistConfig() (soloistConfig, error) {
	var c soloistConfig
	b, err := os.ReadFile(soloistConfigPath())
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

func SoloistKeyConfigured() bool { c, err := readSoloistConfig(); return err == nil && c.APIKey != "" }
func SoloistStatus() string {
	soloistState.RLock()
	defer soloistState.RUnlock()
	return soloistState.text
}
func setSoloistStatus(s string) { soloistState.Lock(); soloistState.text = s; soloistState.Unlock() }

func privateSoloistDir(path string) error {
	return soloistOwnedDir(path, 0o700, receiverCred())
}

// Pin the directory with O_NOFOLLOW before changing permissions/ownership. A network receiver
// owns its data directories and must not be able to redirect privileged chmod/chown through links.
func soloistOwnedDir(path string, mode uint32, cred *syscall.Credential) error {
	if err := os.Mkdir(path, os.FileMode(mode)); err != nil && !os.IsExist(err) {
		return err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	uid, gid := os.Geteuid(), os.Getegid()
	if cred != nil {
		uid, gid = int(cred.Uid), int(cred.Gid)
	}
	if err = unix.Fchown(fd, uid, gid); err != nil {
		return err
	}
	return unix.Fchmod(fd, mode)
}

func writeSoloistConfig(c soloistConfig) error {
	dir := filepath.Dir(soloistConfigPath())
	// The service can read the config but cannot replace its directory entries with symlinks.
	group := os.Getegid()
	if c := receiverCred(); c != nil {
		group = int(c.Gid)
	}
	protected := &syscall.Credential{Uid: uint32(os.Geteuid()), Gid: uint32(group)}
	for _, p := range []string{soloistHome(), filepath.Join(soloistHome(), "config"), dir} {
		if err := soloistOwnedDir(p, 0o750, protected); err != nil {
			return err
		}
	}
	for _, p := range []string{filepath.Join(soloistHome(), "data"), soloistData(), filepath.Join(soloistHome(), "cache"), filepath.Join(soloistHome(), "pulse")} {
		if err := privateSoloistDir(p); err != nil {
			return err
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".key-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	handTo(receiverCred(), f.Name())
	return os.Rename(f.Name(), soloistConfigPath())
}

// SaveSoloistKey never returns or logs a credential. Blank input preserves it; Remove is explicit.
func SaveSoloistKey(key string, remove bool) error {
	soloistConfigMu.Lock()
	defer soloistConfigMu.Unlock()
	key = strings.TrimSpace(key)
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n\t\x00 ") {
		return errors.New("Enter the API key without spaces or line breaks")
	}
	if remove {
		if key != "" {
			return errors.New("Choose either replace or remove")
		}
		// stop/restart is requested below; removing config prevents future launches.
		if err := os.Remove(soloistConfigPath()); err != nil && !os.IsNotExist(err) {
			return errors.New("Could not remove the API key")
		}
	} else if key != "" {
		c := soloistConfig{APIKey: key, DeviceName: config.Get().Device.Name, DataDir: soloistData(),
			CacheDir: filepath.Join(soloistHome(), "cache"), CacheSizeMB: 100, WebSocket: "127.0.0.1:0", InitialVolume: 100}
		if err := writeSoloistConfig(c); err != nil {
			return errors.New("Could not securely save the API key")
		}
	} else {
		return nil
	}
	restartSoloist()
	return nil
}

func restartSoloist() {
	soloistRevision.Add(1)
	select {
	case Get().wake <- struct{}{}:
	default:
	}
}

func SetSoloist(on bool) error {
	if on && !HasSoloist() {
		return errors.New("Soloist experiment is not installed in this image")
	}
	if err := config.Set().Streaming().SpotifySoloist(on); err != nil {
		return err
	}
	restartSoloist()
	return nil
}

func soloistEnvironment(run string) []string {
	h := soloistHome()
	libs, _ := soloistLibraries()
	return []string{"HOME=" + h, "XDG_CONFIG_HOME=" + filepath.Join(h, "config"), "XDG_DATA_HOME=" + filepath.Join(h, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(h, "cache"), "XDG_RUNTIME_DIR=" + run, "PULSE_RUNTIME_PATH=" + run,
		"PULSE_STATE_PATH=" + filepath.Join(h, "pulse"), "PULSE_SERVER=unix:" + filepath.Join(run, "native"),
		"PULSE_SINK=techo5", "TECHO5_SOLOIST_RUNTIME=" + soloistRuntime, "SOLOIST_EXEC=" + soloistExec,
		"LD_LIBRARY_PATH=" + libs + ":" + filepath.Join(libs, "pulseaudio")}
}

// Child output is deliberately not forwarded to logs: it can contain keys/account details. Only
// fixed, nonsecret status messages are extracted. The UI never mistakes a process for a paired session.
type soloistMessages struct {
	mu      sync.Mutex
	partial string
}

func (w *soloistMessages) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial += string(b)
	for strings.Contains(w.partial, "\n") {
		line, rest, _ := strings.Cut(w.partial, "\n")
		w.partial = rest
		lower := strings.ToLower(line)
		keyLine := strings.NewReplacer("-", " ", "_", " ").Replace(lower)
		switch {
		case (strings.Contains(keyLine, "api key") || strings.Contains(keyLine, "apikey")) && (strings.Contains(lower, "invalid") || strings.Contains(lower, "reject") || strings.Contains(lower, "unauthor")):
			setSoloistStatus("Spotify rejected the API key. Replace it and save again.")
		case strings.Contains(lower, "downloading"):
			setSoloistStatus("Downloading current Soloist build")
		case strings.Contains(lower, "update check failed"):
			setSoloistStatus("Update check failed; retrying automatically")
		}
	}
	if len(w.partial) > 8192 {
		w.partial = ""
	}
	return len(b), nil
}

func runSoloistChild(ctx context.Context, path string, args, env []string) error {
	return runSoloistChildOutput(ctx, path, args, env, nil)
}

func runSoloistChildOutput(ctx context.Context, path string, args, env []string, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: receiverCred(), Setpgid: true}
	output := &soloistMessages{}
	cmd.Stdout, cmd.Stderr = output, output
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 12 * time.Second // soloistd gives Soloist ten seconds to shut down.
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return err
}

func runSoloistReceiver(ctx context.Context, name string) {
	if !HasSoloist() {
		setSoloistStatus("Private runtime or soloistd is missing")
		return
	}
	if !SoloistKeyConfigured() {
		setSoloistStatus("API key required: open setup → Sound & Voice")
		return
	}
	soloistConfigMu.Lock()
	c, err := readSoloistConfig()
	if err != nil {
		soloistConfigMu.Unlock()
		setSoloistStatus("Could not read private configuration")
		return
	}
	c.DeviceName = name
	if err := writeSoloistConfig(c); err != nil {
		soloistConfigMu.Unlock()
		setSoloistStatus("Could not save private configuration")
		return
	}
	soloistConfigMu.Unlock()
	wait := restartFirst
	for ctx.Err() == nil {
		start := time.Now()
		runSoloistSession(ctx)
		if ctx.Err() != nil {
			break
		}
		setSoloistStatus("Receiver stopped; retrying automatically. Check key and network.")
		if time.Since(start) > ranLong {
			wait = restartFirst
		} else {
			wait = min(wait*2, restartMost)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	setSoloistStatus("Stopped")
}

func runSoloistSession(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	run := filepath.Join(runDir, "soloist")
	if err := privateSoloistDir(run); err != nil {
		return
	}
	_ = os.Remove(filepath.Join(run, "native"))
	r, rawW, err := os.Pipe()
	if err != nil {
		return
	}
	defer r.Close()
	defer rawW.Close()
	pcmR, pcmW, err := os.Pipe()
	if err != nil {
		return
	}
	defer pcmR.Close()
	defer pcmW.Close()
	stop := context.AfterFunc(ctx, func() { r.Close(); rawW.Close(); pcmR.Close(); pcmW.Close() })
	defer stop()
	var playing atomic.Bool
	unhealthy := make(chan struct{}, 1)
	starts := &startCount{}
	volumeLevel := newLevel()
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); pump(ctx, home.SpotifyName, pcmR, nil, starts) }()
	go func() { defer wg.Done(); forwardSoloistFloatPCM(ctx, r, pcmW, &playing, volumeLevel) }()
	go func() { defer wg.Done(); followSoloist(ctx, &playing, starts, unhealthy, volumeLevel, r) }()
	go func() { defer wg.Done(); watchSoloistDoor(ctx) }()
	defer func() { cancel(); wg.Wait(); media.Get().ReceiverGone(home.SpotifyName) }()
	conf := filepath.Join(filepath.Dir(soloistConfigPath()), "pulse.pa")
	body := fmt.Sprintf("load-module module-native-protocol-unix socket=%s auth-anonymous=1\nload-module module-null-sink sink_name=techo5 format=float32le rate=44100 channels=2\nset-default-sink techo5\n", filepath.Join(run, "native"))
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		return
	}
	handTo(receiverCred(), conf)
	modules, _ := filepath.Glob(filepath.Join(soloistRuntime, "usr/lib/pulse-*/modules"))
	if len(modules) != 1 {
		return
	}
	env := soloistEnvironment(run)
	pulseDone := make(chan error, 1)
	go func() {
		pulseDone <- runSoloistChild(ctx, soloistExec, []string{filepath.Join(soloistRuntime, "usr/bin/pulseaudio"), "-n", "--file=" + conf, "--daemonize=no", "--use-pid-file=no", "--exit-idle-time=-1", "--disable-shm=yes", "--dl-search-path=" + modules[0], "--log-target=stderr"}, env)
	}()
	pulseFinished := false
	defer func() {
		cancel()
		if !pulseFinished {
			<-pulseDone
		}
	}()
	readyDeadline := time.NewTimer(15 * time.Second)
	defer readyDeadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(run, "native")); err == nil {
			break
		}
		select {
		case <-readyDeadline.C:
			setSoloistStatus("Private audio server did not start; retrying")
			return
		case <-ctx.Done():
			return
		case <-pulseDone:
			pulseFinished = true
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	setSoloistStatus("Starting Soloist; select this device in Spotify")
	captureDone := make(chan error, 1)
	go func() {
		captureDone <- runSoloistChildOutput(ctx, soloistExec, []string{filepath.Join(soloistRuntime, "usr/bin/parec"), "--raw", "--format=float32le", "--rate=44100", "--channels=2", "--device=techo5.monitor", "--latency-msec=60"}, env, rawW)
	}()
	captureFinished := false
	defer func() {
		cancel()
		if !captureFinished {
			<-captureDone
		}
	}()
	done := make(chan error, 1)
	go func() { done <- runSoloistChild(ctx, soloistdPath, []string{"run"}, env) }()
	select {
	case <-ctx.Done():
		cancel()
		<-done
	case <-done:
		cancel()
	case <-captureDone:
		captureFinished = true
		cancel()
		<-done
	case <-unhealthy:
		setSoloistStatus("Soloist stopped responding; restarting automatically")
		cancel()
		<-done
	case <-pulseDone:
		pulseFinished = true
		cancel()
		<-done
	}
}
