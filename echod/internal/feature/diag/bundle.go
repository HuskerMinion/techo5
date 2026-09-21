package diag

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/redact"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The diagnostics bundle: everything worth having when something is wrong, with the private parts
// already taken out.
//
// Asking somebody for a log means asking them to read it first and remove their own address, their
// Wi-Fi, their serial number and whatever else is in there. Most people will not, and the ones who
// do will get it wrong once. So the device does it: lib/redact runs over everything here before it
// is written, and the values it knows about — this device's name, its network, its serial — are
// named to it rather than left to a pattern.
//
// It is a plain text file, because somebody has to read it in an issue.

// logTail is how much of the daemon's log goes in: enough to hold what just happened.
const logTail = 400

// Bundle is the whole thing as text, ready to be sent to somebody.
func Bundle() string {
	r := redact.New()
	c := config.Get()
	r.Known("device", c.Device.Name)
	r.Known("serial", serial())
	for _, ssid := range wifi.Saved() {
		r.Known("wifi", ssid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if st := wifi.Current(ctx); st.SSID != "" {
		r.Known("wifi", st.SSID)
	}

	var b strings.Builder
	section := func(title string, body string) {
		fmt.Fprintf(&b, "\n=== %s ===\n", title)
		body = strings.TrimRight(body, "\n")
		if body == "" {
			body = "(nothing)"
		}
		b.WriteString(body + "\n")
	}

	fmt.Fprintf(&b, "TECHO5 diagnostics, %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Addresses, names, keys and serial numbers have been replaced.\n")

	section("device", strings.Join([]string{
		"model: " + layout.Model,
		"board: " + layout.Board,
		"version: " + layout.VersionString(),
		"slot: " + readTrim("/run/techo5/slot"),
		"uptime: " + uptime(),
		"go: " + runtime.Version(),
	}, "\n"))

	section("settings", settingsSummary(c))
	section("network", networkSummary(ctx))
	// The log is where this device's own start script puts it, which is not the same file on all of
	// them, and not under StateDir on any: an empty log section is the one thing a bundle cannot be
	// missing, since it is what somebody asked for the bundle to see.
	section("daemon log (last "+fmt.Sprint(logTail)+" lines)", tail(layout.LogPath, logTail))
	if layout.BootLog != "" {
		section("boot log", tail(layout.BootLog, 120))
	}
	section("kernel crash records", crashList())
	section("kernel messages", tail("", 0))

	return r.Text(b.String())
}

// settingsSummary is what the device is set to, without the settings that are secrets in themselves.
func settingsSummary(c config.Config) string {
	var out []string
	add := func(f string, v ...any) { out = append(out, fmt.Sprintf(f, v...)) }
	add("wake words: %d configured", len(c.Wake.Words))
	add("volume: %d", c.Speaker.Volume)
	add("microphone muted: %t", c.Microphone.Muted)
	out = append(out, screenSettings(c)...)
	add("alarms: %d set, %d followed, sunrise=%d min", len(c.Alarms.List), len(c.Alarms.Follow), c.Alarms.SunriseMinutes)
	add("radio: source=%q own=%d favorites wired=%t", c.Home.RadioSource, len(c.Home.Radio.Own), c.Home.Radio.Configured())
	add("security: ssh=%t camera_web=%t screen_web=%t", c.Security.SSH, c.Security.Camera, c.Security.Screen)
	add("updates: channel=%q", c.Update.Channel)
	return strings.Join(out, "\n")
}

// networkSummary is the connection as the device sees it. The addresses in here are replaced like
// everything else; what is worth knowing is whether there is one at all.
func networkSummary(ctx context.Context) string {
	st := wifi.Current(ctx)
	var out []string
	out = append(out, fmt.Sprintf("wifi: connected=%t ssid=%q state=%q address=%q", st.Connected, st.SSID, st.State, st.Address))
	out = append(out, fmt.Sprintf("saved networks: %d", len(wifi.Saved())))
	return strings.Join(out, "\n")
}

// crashList is what the boot scripts kept from the kernel's own record of a crash, by name and size
// — the records themselves are too big and too raw for a bundle.
func crashList() string {
	dir := layout.StateDir + "/crash"
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		names = append(names, fmt.Sprintf("%s (%d bytes, %s)", e.Name(), info.Size(), info.ModTime().Format(time.RFC3339)))
	}
	sort.Strings(names)
	return strings.Join(names, "\n") + "\n\nThese are in " + dir + " on the device."
}

// tail is the last n lines of a file. With no path it is the kernel's ring buffer instead.
func tail(path string, n int) string {
	var body []byte
	if path == "" {
		out, err := exec.Command("dmesg").Output()
		if err != nil {
			return ""
		}
		body, n = out, 80
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		body = b
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(b))
}

func uptime() string {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "?"
	}
	var secs float64
	if _, err := fmt.Sscanf(string(b), "%f", &secs); err != nil {
		return "?"
	}
	return (time.Duration(secs) * time.Second).Round(time.Minute).String()
}

// serial is this unit's, for the redactor to take out wherever it appears.
func serial() string {
	for _, p := range []string{"/proc/idme/serial", filepath.Join(layout.StateDir, "serial")} {
		if s := readTrim(p); s != "" && s != "?" {
			return s
		}
	}
	return ""
}
