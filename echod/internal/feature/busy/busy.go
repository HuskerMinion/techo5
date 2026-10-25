// Package busy keeps a marker saying the device is in the middle of something a reboot would cut off,
// for the boot script's loops that reboot on their own: the kernel update of the boards on TECHO5's own
// kernel (the 2nd gen Show 8, the Skylight), which reboots into a new kernel, and its trial, which
// reboots to use up a try (etc/techo5/boot.sh).
//
// The marker is Path, written while the device is busy and removed when it is not. Its first lines
// are the daemon's pid and the uptime it was written at, "pid <n>" and "at <seconds>"; it is written
// again every few seconds while it stands, so a marker left by a daemon that died or hung is seen to
// be stale (boot.sh takes one more than a minute old, or whose pid is not the daemon's, as none).
// Then one reason a line:
//
//	ring    an alarm or a timer is sounding
//	soon    an alarm or a timer rings within RingAhead: a reboot now would land on it
//	voice   a voice request is being heard or answered
//	call    a call or a talk through a camera
//	sound   an announcement or another sound has the speaker
//	media   music or a station is playing
//	light   the wake light is coming up before an alarm, or held at full by one that rang
//	alarm   an alarm (on the device, a followed Home Assistant helper, or a snooze) is due within
//	        AlarmAhead: a reboot then could still be in progress, or fail, when it should ring
//
// The script waits while the marker is there, but not forever: what can last for hours (media, the
// light, alarms one after another) holds it off for two hours at most, and after that only ring and
// soon do, so a unit that is always busy, or a kernel that keeps something from ever ending, still
// takes its kernel.
package busy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Path is the marker.
const Path = "/run/techo5/busy"

// AlarmAhead is how long before an alarm is due the device counts as busy.
const AlarmAhead = 30 * time.Minute

// RingAhead is how long before anything rings (an alarm, a timer) a reboot is held off even past the
// script's limit.
const RingAhead = time.Minute

// every is how often the marker is brought up to date.
const every = 5 * time.Second

func init() {
	component.Register(component.Device, Get(), component.Order(90))
}

// Snapshot is what the device is doing, as far as a reboot is concerned.
type Snapshot struct {
	Ring, Voice, Call, Sound, Media, Light bool
	// NextAlarm is when the next alarm rings, and NextTimer when the next running timer does; zero
	// for none. Alarms and timers are kept apart (alarm's View has no timers).
	NextAlarm, NextTimer time.Time
}

// Reasons are the marker's lines for s at now, in a fixed order; none when a reboot would cut nothing
// off. A clock not yet set says nothing about alarms.
func Reasons(s Snapshot, now time.Time) []string {
	var r []string
	add := func(on bool, why string) {
		if on {
			r = append(r, why)
		}
	}
	set := now.Year() >= 2020
	within := func(at time.Time, d time.Duration) bool { return !at.IsZero() && !at.After(now.Add(d)) }
	add(s.Ring, "ring")
	// A timer counts down from now, so it needs no set clock.
	add((set && within(s.NextAlarm, RingAhead)) || within(s.NextTimer, RingAhead), "soon")
	add(s.Voice, "voice")
	add(s.Call, "call")
	add(s.Sound, "sound")
	add(s.Media, "media")
	add(s.Light, "light")
	add(set && within(s.NextAlarm, AlarmAhead), "alarm")
	return r
}

// Marker keeps the file at its path.
type Marker struct {
	path   string
	pid    int
	uptime func() (int64, error)
	mu     sync.Mutex
}

// Set writes the marker for these reasons, or removes it for none. It is written every time, with the
// uptime now, and replaced whole, so the script never reads half of one.
func (m *Marker) Set(reasons []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(reasons) == 0 {
		err := os.Remove(m.path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	up, err := m.uptime()
	if err != nil {
		return err
	}
	body := fmt.Sprintf("pid %d\nat %d\n%s\n", m.pid, up, strings.Join(reasons, "\n"))
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// uptime is the seconds since boot, as the script reads them from /proc/uptime: a clock that NTP does
// not move.
func uptime() (int64, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("/proc/uptime is empty")
	}
	whole, _, _ := strings.Cut(f[0], ".")
	return strconv.ParseInt(whole, 10, 64)
}

// Busy is the device's marker.
type Busy struct {
	marker *Marker
	look   func(now time.Time) Snapshot
}

var (
	once   sync.Once
	shared *Busy
)

func Get() *Busy {
	once.Do(func() { shared = &Busy{marker: &Marker{path: Path, pid: os.Getpid(), uptime: uptime}, look: look} })
	return shared
}

func (b *Busy) Name() string { return "busy" }

// look asks each part of the device what it is doing.
func look(now time.Time) Snapshot {
	playing, _ := media.Get().Playing()
	s := Snapshot{
		Ring:  ring.IsSounding(),
		Voice: voice.Get().Busy(),
		Call:  phone.Get().Busy() || talkback.Get().Busy(),
		Sound: speaker.Sound().Busy(),
		Media: playing || media.Get().ExternalPlaying(),
		Light: alarm.Get().SunriseProgress(now) > 0,
	}
	if next := alarm.Get().View(now).Next; next != nil {
		s.NextAlarm = next.At
	}
	for _, c := range timer.Get().List(now) {
		if c.Active && (s.NextTimer.IsZero() || now.Add(c.Left).Before(s.NextTimer)) {
			s.NextTimer = now.Add(c.Left)
		}
	}
	return s
}

// keepsMarker is whether this board's boot script reads the marker: the ones that update their own
// kernel (device.conf KERNEL_UPDATE, cli/echod's kernel command). A variable for the tests.
var keepsMarker = func() bool { return layout.AthenaMainline() || layout.Skylight() }

// Run keeps the marker, on the devices whose boot script reads it. A marker from before this start
// goes first: whatever it said is over.
func (b *Busy) Run(ctx context.Context) error {
	if !keepsMarker() {
		<-ctx.Done()
		return nil
	}
	b.marker.Set(nil)
	defer b.marker.Set(nil)
	t := time.NewTicker(every)
	defer t.Stop()
	failed := ""
	for {
		now := time.Now()
		// Said once, not every few seconds while it lasts.
		err := b.marker.Set(Reasons(b.look(now), now))
		if err != nil && err.Error() != failed {
			slog.Warn("busy marker", "err", err)
		}
		failed = ""
		if err != nil {
			failed = err.Error()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RingsNow is the boot script's last look, for a reboot that is already on its way (the kernel update
// writes the kernel, then reboots): whether a fresh marker at path says something rings now or within
// the minute. A marker more than staleAfter old, or whose pid is not a live daemon, says nothing.
func RingsNow(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	up, err := uptime()
	if err != nil {
		return false
	}
	return ringsIn(string(b), up, func(pid string) bool {
		// Digits only, as boot.sh checks it: anything else is no process of ours.
		if pid == "" || strings.Trim(pid, "0123456789") != "" {
			return false
		}
		comm, err := os.ReadFile("/proc/" + pid + "/comm")
		return err == nil && strings.TrimSpace(string(comm)) == "techo5"
	})
}

// staleAfter is how old a marker may be and still be believed: the daemon writes it every few seconds.
const staleAfter = 60

func ringsIn(marker string, up int64, alive func(pid string) bool) bool {
	var pid string
	at := int64(-1)
	rings := false
	for _, l := range strings.Split(marker, "\n") {
		switch {
		case strings.HasPrefix(l, "pid "):
			pid = strings.TrimPrefix(l, "pid ")
		case strings.HasPrefix(l, "at "):
			if n, err := strconv.ParseInt(strings.TrimPrefix(l, "at "), 10, 64); err == nil {
				at = n
			}
		case l == "ring" || l == "soon":
			rings = true
		}
	}
	return rings && at >= 0 && up-at <= staleAfter && pid != "" && alive(pid)
}
