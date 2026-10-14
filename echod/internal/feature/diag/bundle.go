package diag

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
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

// wakeTail is how many of the log's wake word lines go in, from the whole of the log file: a device
// that wakes by mistake did it hours ago as often as just now, and the last few hundred lines are long
// past it. The file keeps earlier runs (it is only started again when it is over 5 MB as the daemon
// starts), so the times in it can go back to zero partway through.
const wakeTail = 100

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
	// Bluetooth devices and the phones playing here are often named after whoever owns them, and the
	// log names them.
	for _, name := range btaudio.Get().PairedNames() {
		r.Known("bluetooth", name)
	}
	if from, ok := phonePlaying(media.Get().Receiving()); ok {
		r.Known("bluetooth", from)
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
	last, wakes := readLog(layout.LogPath, logTail, wakeTail)
	section("daemon log (last "+fmt.Sprint(logTail)+" lines)", last)
	section("wake words (last "+fmt.Sprint(wakeTail)+" detections and near misses)", wakes)
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
	add("speaker gain: %d dB", c.Speaker.GainDB)
	add("microphone muted: %t", c.Microphone.Muted)
	out = append(out, screenSettings(c)...)
	add("alarms: %d set, %d followed, sunrise=%d min", len(c.Alarms.List), len(c.Alarms.Follow), c.Alarms.SunriseMinutes)
	add("radio: source=%q own=%d favorites wired=%t", c.Home.RadioSource, len(c.Home.Radio.Own), c.Home.Radio.Configured())
	add("security: ssh=%t camera_web=%t screen_web=%t talk_back=%t settings_lock=%t", c.Security.SSH, c.Security.Camera, c.Security.Screen, c.Security.TalkBack, c.Security.LockPIN != "")
	add("updates: channel=%q", c.Update.Channel)
	add("deck: set=%t pages=%d obs=%t obs_password=%t", c.Deck.Set(), len(c.Deck.Pages), c.Deck.OBS.Addr != "", c.Deck.OBS.Password != "")
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

// readLog goes through the log at path once, line by line, so that a log grown large over weeks is
// never held whole: it keeps its last n lines, and its last wakeN wake word detections and near misses
// with their scores. Only the wake engine's own lines count, as it writes them - not a song title, an
// announcement or anything else in the log that happens to say the same words.
func readLog(path string, n, wakeN int) (last, wakes string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	lines, found := newRing(n), newRing(wakeN)
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			lines.add(line)
			if wakeLine.MatchString(line) {
				found.add(line)
			}
		}
		if err != nil {
			break
		}
	}
	return lines.String(), found.String()
}

// wakeLine is one of the wake engine's own lines: the message comes straight after the level and the
// time, where nothing from outside the daemon can be put, and the slot is its first field.
var wakeLine = regexp.MustCompile(`^[A-Z] \[ *[0-9.]+\] wake (detected|near miss) slot=`)

// ring keeps the last lines added to it, as many as it was made for.
type ring struct {
	lines []string
	next  int
	full  bool
}

func newRing(n int) *ring { return &ring{lines: make([]string, max(n, 0))} }

func (r *ring) add(line string) {
	if len(r.lines) == 0 {
		return
	}
	r.lines[r.next] = line
	r.next = (r.next + 1) % len(r.lines)
	r.full = r.full || r.next == 0
}

// String is the lines kept, oldest first, one to a line.
func (r *ring) String() string {
	if !r.full {
		return strings.Join(r.lines[:r.next], "\n")
	}
	return strings.Join(append(append([]string(nil), r.lines[r.next:]...), r.lines[:r.next]...), "\n")
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

// phonePlaying is the name of the phone playing over Bluetooth, from the name of what is being
// received: not AirPlay's, Spotify's or the like, which are the apps' names and would be replaced
// wherever the bundle says them.
func phonePlaying(receiving string) (string, bool) {
	return strings.CutPrefix(receiving, media.BluetoothPrefix)
}
