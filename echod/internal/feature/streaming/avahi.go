package streaming

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// avahiConf announces what the receivers register and nothing of its own: no workstation record and no
// host details. The daemon announces itself (ESPHome, Sendspin) on its own, beside avahi. The host name
// is the device's own with -media after it: every image is called techo5, which would have a house of
// them renaming each other, and the device's own name is what the daemon already answers for.
func avahiConf(host string) string {
	return `[server]
host-name=` + host + `
use-ipv4=yes
use-ipv6=yes
ratelimit-interval-usec=1000000
ratelimit-burst=1000
[wide-area]
enable-wide-area=no
[publish]
publish-hinfo=no
publish-workstation=no
[reflector]
enable-reflector=no
[rlimits]
`
}

// runAvahiOnce runs avahi-daemon for the receivers to register with, until it stops or ctx ends.
func runAvahiOnce(ctx context.Context) {
	conf := filepath.Join(runDir, "avahi-daemon.conf")
	host := layout.Slug(config.Get().Device.Name)
	if host == "" {
		host = "techo5"
	}
	// A DNS label is at most 63 characters, -media included.
	host = strings.TrimRight(host[:min(len(host), 57)], "-")
	if err := os.WriteFile(conf, []byte(avahiConf(host+"-media")), 0o644); err != nil {
		slog.Error("streaming: writing avahi's configuration failed", "err", err)
		return
	}
	// Where avahi keeps its pid; /run is cleared at boot.
	_ = os.MkdirAll("/run/avahi-daemon", 0o755)
	ensureBus()
	// avahi drops root for its own user by itself.
	err := runProgram(ctx, program{name: "avahi", path: avahiPath, args: []string{"-f", conf, "--no-rlimits"}})
	if ctx.Err() == nil {
		slog.Warn("streaming: avahi stopped", "err", err)
	}
}

// avahiRunning is whether avahi says, on the system bus, that it is up and its name is settled.
func avahiRunning(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "dbus-send", "--system", "--print-reply", "--dest=org.freedesktop.Avahi",
		"/", "org.freedesktop.Avahi.Server.GetState").Output()
	// AVAHI_SERVER_RUNNING is 2.
	return err == nil && strings.Contains(string(out), "int32 2")
}

// busSocket is the system bus avahi registers on; dbusPath starts one.
var (
	busSocket = "/run/dbus/system_bus_socket"
	dbusPath  = "/usr/bin/dbus-daemon"
)

// busStarting is held while the system bus is being started, by whichever of this and Bluetooth's start
// (techo5-lib.sh on the Show, techo5-bt on the Dot) gets there first, so the two never start two.
const busStarting = "/run/techo5-dbus-starting"

// ensureBus starts the system bus when nothing has yet. Bluetooth's start brings it up too, but only
// once the radio is there, and avahi will not run without it. Started detached, as Bluetooth's start
// does, so it outlives the daemon; that start sees it running and leaves it be.
func ensureBus() {
	if busUp() {
		return
	}
	if err := os.Mkdir(busStarting, 0o755); err != nil {
		// Bluetooth's start has it in hand.
		if waitForBus(5 * time.Second) {
			return
		}
		// Or had, and died holding it: the lock is taken over rather than waited on forever.
		_ = os.Remove(busStarting)
		if err := os.Mkdir(busStarting, 0o755); err != nil {
			return
		}
	}
	defer os.Remove(busStarting)
	if busUp() {
		return
	}
	_ = os.MkdirAll(filepath.Dir(busSocket), 0o755)
	_ = exec.Command("dbus-uuidgen", "--ensure").Run()
	if err := exec.Command(dbusPath, "--system", "--fork", "--nopidfile").Run(); err != nil {
		slog.Warn("streaming: starting the system bus failed; avahi needs it", "err", err)
		return
	}
	waitForBus(3 * time.Second)
}

// busUp is whether there is a system bus, or one on its way.
func busUp() bool {
	if _, err := os.Stat(busSocket); err == nil {
		return true
	}
	out, err := exec.Command("pidof", "dbus-daemon").Output()
	return err == nil && len(out) > 0
}

// waitForBus is whether the system bus is there within the time given.
func waitForBus(within time.Duration) bool {
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(busSocket); err == nil {
			return true
		}
	}
	return false
}

// killLeftovers ends any avahi or receiver still running from before: the daemon starts them, and stops
// them on its way out, but one that crashed or was killed leaves them behind, and a second avahi will
// not start beside the first. Called with none of this daemon's own running.
func killLeftovers() {
	ours := map[string]bool{avahiPath: true, shairportPath: true, librespotPath: true}
	isOurs := func(pid int) bool {
		exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		return err == nil && ours[exe]
	}
	dirs, _ := os.ReadDir("/proc")
	var found []int
	for _, d := range dirs {
		if pid, err := strconv.Atoi(d.Name()); err == nil && pid != os.Getpid() && isOurs(pid) {
			found = append(found, pid)
		}
	}
	if len(found) == 0 {
		return
	}
	slog.Warn("streaming: ending what an earlier run left behind", "pids", found)
	for _, pid := range found {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(time.Second)
	for _, pid := range found {
		// Only what is still one of them: a number freed in that second may be something else's now.
		if isOurs(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
