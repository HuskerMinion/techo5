package streaming

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// spotifyEvent is the program librespot runs on each player event: it writes one line, tab-separated,
// to a pipe the daemon reads: the event, the song, its artists and album, its cover (the first librespot
// lists, which is the largest) and the volume the app set. Tabs and newlines in what it writes are made
// spaces, so a song's name cannot end the line early, and each field is cut short: short enough that a
// whole line stays under the pipe's atomic write, so two events running at once cannot interleave their
// lines, even in text whose characters take four bytes each. The pipe is opened
// for reading as well as writing (1<>), which never waits: a pipe nobody reads any more, as when the
// receivers are stopped, cannot leave the program stuck.
const spotifyEvent = `#!/bin/sh
case "$PLAYER_EVENT" in
track_changed|stopped|session_connected|session_disconnected|volume_changed) ;;
*) exit 0 ;;
esac
clean() { printf '%s' "$1" | tr '\t\n' '  ' | cut -c1-200; }
artists() { printf '%s' "$ARTISTS" | awk 'NR > 1 { printf ", " } { printf "%s", $0 }' | tr '\t' ' ' | cut -c1-200; }
cover() { printf '%s' "$COVERS" | awk 'NR == 1' | tr -d '\t\r' | cut -c1-200; }
printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$PLAYER_EVENT" "$(clean "$NAME")" "$(artists)" "$(clean "$ALBUM")" "$(cover)" "$(clean "$VOLUME")" 1<>"$TECHO5_SPOTIFY_EVENTS"
`

// The Spotify app's volume slider and the device's volume are one. The librespot build-librespot.sh
// makes takes volumes from outside (--volume-fifo, librespot-volume-fifo.patch) and passes the audio on
// unscaled: the device's volume is handed to it as a phone connects and whenever it changes, by the
// buttons, Home Assistant or night volume alike, so the app's slider shows it, and a move of the slider
// sets the device's volume to where the slider is (spotifySync).
//
// An older librespot, from an image made before that, takes no volume from outside and scales what it
// sends by the slider whatever it is told (its "fixed" control in 0.8 still scales). It is told to scale
// linearly, which the pump undoes exactly (spotifyLevel), and each move of the slider moves the device
// by as many steps as it moved (slider). By as many, not to the same place: its slider does not follow
// the device's buttons, and a slider that says 100% over a device at 8 would otherwise send it to
// nearly full at the first nudge down.
//
// librespot also says its volume as a phone connects, which is only where it was left, and may say it
// more than once: everything it says in the moments after a connect only marks where the slider is.

// spotifyFull is librespot's top volume.
const spotifyFull = 65535

// spotifyLevel undoes librespot's scaling on what it sends. librespot says its volume as a phone
// connects, before any audio, so the unity it starts at is never what a song is undone by.
var spotifyLevel = newLevel()

// spotifyPipe is the read end of librespot's audio while it runs, for spotifyQueued.
var spotifyPipe atomic.Pointer[os.File]

// spotifyGain is the gain that undoes librespot's scaling at the app's volume v, so what reaches the
// speaker is the song at the level the device's volume sets. Silence is left alone: there is nothing
// to undo.
func spotifyGain(v int) float64 {
	if v <= 0 || v >= spotifyFull {
		return 1
	}
	return spotifyFull / float64(v)
}

// spotifyQueued is how much audio is waiting in librespot's pipe, scaled at whatever volume it was
// scaled at when it was written.
func spotifyQueued() int {
	f := spotifyPipe.Load()
	if f == nil {
		return 0
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return 0
	}
	n := 0
	_ = rc.Control(func(fd uintptr) { n, _ = unix.IoctlGetInt(int(fd), unix.TIOCINQ) })
	return n
}

// spotifyStep is the device's volume step for the app's level.
func spotifyStep(v int) int { return (v*media.VolumeSteps + spotifyFull/2) / spotifyFull }

// spotifyValue is the app's level for the device's volume step: the one spotifyStep takes back to it.
func spotifyValue(step int) int { return (step*spotifyFull + media.VolumeSteps/2) / media.VolumeSteps }

// connectQuiet is how long after a phone connects a volume is only librespot saying where it was.
const connectQuiet = 3 * time.Second

// slider follows the app's volume slider. last is where it was, -1 before anything was heard since
// librespot started or a phone connected or went.
type slider struct {
	last int

	// by and at are the last move taken, and when: one heard just before a phone's connect line was
	// the connect's (the programs that say them finish in no set order), and is taken back (session).
	by int
	at time.Time
}

// moved takes the slider's new level v, heard at now, and says how many steps it moved, 0 for a level
// that only says where it is: one heard as a phone connects, or the first heard since librespot
// started or a phone connected, which has nothing to move from.
func (s *slider) moved(v int, connecting bool, now time.Time) int {
	prev := s.last
	s.last = v
	if connecting || prev < 0 {
		return 0
	}
	by := spotifyStep(v) - spotifyStep(prev)
	if by != 0 {
		s.by, s.at = by, now
	}
	return by
}

// session is a phone connecting or going at now, or librespot starting (connected false): the slider
// is somewhere new, and has not moved from anywhere. A move heard in the moments before a connect is
// said back, to be taken back: it was the connect's.
func (s *slider) session(connected bool, now time.Time) (undo int) {
	if connected && !s.at.IsZero() && now.Sub(s.at) <= connectQuiet {
		undo = -s.by
	}
	*s = slider{last: -1}
	return undo
}

// echoWait is how long a level handed to librespot may take to come back as the app's.
const echoWait = 3 * time.Second

// spotifySync keeps the app's slider and the device's volume as one, with a librespot that takes
// volumes from outside.
//
// librespot says every volume it is given back as the app's, so a level heard that was handed to it
// moments ago is its own coming back, and is let by: taken as the app's it could pull the device back
// to where it was a moment ago, since the programs that say the levels finish in no set order. A level
// the device took from the slider is not handed back, which would fight a slider still being dragged,
// unless the device ended somewhere else (night volume stopped a raise), so the slider shows that.
type spotifySync struct {
	write  func(v int) bool // hands librespot a level; false when it was not taken
	device func() int       // the device's volume step
	set    func(step int)   // sets the device's volume step
	most   func(to int) int // the most a raise may take the device to (media.CapRaise)
	now    func() time.Time

	// after runs f after d, and says how to stop it: time.AfterFunc's, a variable for the tests.
	after func(d time.Duration, f func()) (stop func() bool)

	mu     sync.Mutex
	active bool              // a phone is connected: librespot takes a volume only then
	shown  int               // the step the app's slider is at, as far as is known; -1 for not known
	sent   map[int]time.Time // levels handed to librespot and not yet heard back, and when
	said   int               // the level librespot said last, 0..spotifyFull; -1 for none yet
	moved  spotifyMove       // the last move of the device the slider made
	settle func() bool       // stops the settle to come (settleLocked), nil for none
}

// spotifyMove is a move of the device the app's slider made: from and to steps, and when.
type spotifyMove struct {
	from, to int
	at       time.Time
}

// settleAfter is how long the app's slider is to be still before the device's level is handed to
// librespot once more (settle).
const settleAfter = time.Second

func newSpotifySync() *spotifySync {
	return &spotifySync{shown: -1, said: -1, sent: map[int]time.Time{}, now: time.Now,
		after: func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }}
}

// started is librespot starting, or starting again after it stopped: no phone is connected to it,
// whatever was last heard. A librespot that crashed never says its phone went, and should one connect
// to the new one without that, a volume heard before its connect line would be taken as the slider's.
func (s *spotifySync) started() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active, s.shown, s.said, s.moved = false, -1, -1, spotifyMove{}
	clear(s.sent)
	if s.settle != nil {
		s.settle()
		s.settle = nil
	}
}

// session is a phone connecting, or going: one that connects is told the device's volume. A move the
// slider made in the moments before a connect line was the connect's (librespot says the level it was
// left at as a phone connects, and the programs that say the two finish in no set order: librespot
// skips saying a phone went when telling Spotify fails, so the device can still think one connected)
// and is taken back, unless the device has moved on since.
func (s *spotifySync) session(connected bool) {
	s.mu.Lock()
	undo := -1
	if m := s.moved; connected && !m.at.IsZero() && s.now().Sub(m.at) <= connectQuiet && s.device() == m.to {
		undo = m.from
	}
	s.active, s.shown, s.said, s.moved = connected, -1, -1, spotifyMove{}
	clear(s.sent)
	s.mu.Unlock()
	if undo >= 0 {
		slog.Info("streaming: a Spotify volume heard as a phone connected is taken back", "volume", undo)
		s.set(undo)
	}
	s.levelChanged(s.device())
}

// heard takes a level librespot said, 0..spotifyFull; connecting when it came in the moments after a
// phone connected.
func (s *spotifySync) heard(v int, connecting bool) {
	s.mu.Lock()
	s.said = v
	if at, ok := s.sent[v]; ok && s.now().Sub(at) <= echoWait {
		// Heard back once: librespot says each level it is handed once, so the same level heard
		// again is the slider's.
		delete(s.sent, v)
		s.mu.Unlock()
		return
	}
	// Once the slider is still, the device's level is handed to librespot again, should the lines
	// have come in another order than librespot said them and left the two apart (settleLocked).
	s.settleLocked()
	// librespot says a volume only while a phone is connected: one heard before the connect's own line
	// (the programs that say them finish in no set order) is the connect's too, and like the connect's
	// is only where librespot was left. The device's level stands, and the app is told it.
	if connecting || !s.active {
		s.active = true
		s.tellLocked(s.device())
		s.mu.Unlock()
		return
	}
	step := spotifyStep(v)
	s.shown = step
	from, to := s.device(), step
	if to > from {
		to = s.most(to)
	}
	if to != from {
		s.moved = spotifyMove{from: from, to: to, at: s.now()}
	}
	s.mu.Unlock()
	switch {
	case to != from:
		s.set(to) // levelChanged tells the app if it ended somewhere other than the slider
	case to != step:
		s.tell(to)
	}
}

// levelChanged is the device's volume changing, for whatever reason: the app's slider is moved to it
// unless it is there already.
func (s *spotifySync) levelChanged(step int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if step != s.shown {
		s.tellLocked(step)
	}
}

// settleLocked has the device's level handed to librespot settleAfter from now, unless the slider
// moves again first. librespot says each level as it takes it, but the programs that say them finish
// in no set order: a level the device handed it and a move of the slider can come back the other way
// round, the device at the slider's and the app at the device's last. Called with mu held.
//
// Handing librespot the level it has already changes nothing and is not said back (librespot 0.8 says
// a volume only when it changes), so the settle's level is counted as handed to it only when it is not
// the level librespot said last. Counted all the same, it stayed in sent for echoWait, and the slider
// dragged back to that level in the meantime (an end of the slider, often) was taken for the echo
// and left the device where it was. Should the lines have crossed and librespot say the level after
// all, it is the device's own and moves nothing.
func (s *spotifySync) settleLocked() {
	if s.settle != nil {
		s.settle()
	}
	s.settle = s.after(settleAfter, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.settle = nil
		step := s.device()
		s.handLocked(step, spotifyValue(step) != s.said)
	})
}

func (s *spotifySync) tell(step int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tellLocked(step)
}

// tellLocked hands librespot the device's step while a phone is connected. Called with mu held: write
// never waits.
func (s *spotifySync) tellLocked(step int) { s.handLocked(step, true) }

// handLocked is tellLocked, with the level counted in sent only when librespot is to say it back
// (echoed).
func (s *spotifySync) handLocked(step int, echoed bool) {
	if !s.active {
		return
	}
	now := s.now()
	for v, at := range s.sent {
		if now.Sub(at) > echoWait {
			delete(s.sent, v)
		}
	}
	v := spotifyValue(step)
	if !s.write(v) {
		return
	}
	if echoed {
		s.sent[v] = now
	}
	s.shown = step
}

// writeSpotifyVolume hands librespot a level through its volume pipe, and says whether it took it.
// Nothing waits: with librespot not running there is nobody to open the pipe to and the level is
// dropped, as it is when the pipe is full, which a librespot that stopped reading would leave it.
// A line is far shorter than the pipe's atomic write, so it goes whole or not at all.
func writeSpotifyVolume(path string, v int) bool {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	line := []byte(strconv.Itoa(v) + "\n")
	n, err := unix.Write(fd, line)
	return err == nil && n == len(line)
}

// librespotTakesVolume is whether the image's librespot takes volumes from outside (--volume-fifo):
// one from an image made before build-librespot.sh patched it in does not, and would refuse to start
// if it were given one. A variable for the tests.
var librespotTakesVolume = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, librespotPath, "--help").Output()
	return bytes.Contains(out, []byte("--volume-fifo"))
}

// spotifyPort is where librespot listens for the Spotify app handing it a login: fixed, so a firewall
// can let it through (the Dot's does).
const spotifyPort = "4070"

// spotifyCache is where the login a phone hands over is kept, so the device is still the account's
// after a restart: on userdata beside the device's own state, not in it (that is root's alone, and
// librespot runs as the receivers' user), readable by that user alone. Turning Spotify Connect off
// forgets it (forgetSpotify).
func spotifyCache() string { return filepath.Join(filepath.Dir(layout.StateDir), "techo5-spotify") }

// forgetSpotify removes the kept login: a device switched off from Spotify Connect, or given away, is
// no longer the account's.
func forgetSpotify() {
	if _, err := os.Stat(spotifyCache()); err != nil {
		return
	}
	if err := os.RemoveAll(spotifyCache()); err != nil {
		slog.Warn("streaming: forgetting the Spotify login failed", "err", err)
		return
	}
	slog.Info("streaming: Spotify login forgotten")
}

// runSpotifyReceiver runs librespot under the device's name until ctx ends, playing what it sends and
// showing what it says is playing.
func runSpotifyReceiver(ctx context.Context, name string) {
	r, w, err := os.Pipe()
	if err != nil {
		slog.Error("streaming: Spotify's audio pipe failed", "err", err)
		return
	}
	defer r.Close()
	defer w.Close()
	spotifyPipe.Store(r)
	defer spotifyPipe.Store(nil)
	cred := receiverCred()
	volume := spotifyVolumePipe(runDir, cred)
	var one *spotifySync
	if volume != "" {
		one = newSpotifySync()
		one.write = func(v int) bool { return writeSpotifyVolume(volume, v) }
		one.device = media.Get().Volume
		one.set = media.Get().Set
		one.most = media.Get().CapRaise
	}
	app := &spotifyFallback{slider: slider{last: -1}}
	events := filepath.Join(runDir, "spotify-events")
	script := filepath.Join(runDir, "spotify-event")
	_ = os.Remove(events)
	if err := syscall.Mkfifo(events, 0o600); err != nil {
		slog.Warn("streaming: Spotify's event pipe failed; no song names", "err", err)
	} else if err := os.WriteFile(script, []byte(spotifyEvent), 0o755); err != nil {
		slog.Warn("streaming: writing Spotify's event program failed; no song names", "err", err)
	} else {
		handTo(cred, events)
		safe.Go("spotify events", func() { readSpotifyEvents(ctx, events, one, app) })
	}
	cache := spotifyCache()
	_ = os.MkdirAll(cache, 0o700)
	_ = os.Chmod(cache, 0o700)
	handTo(cred, cache)
	args, level := spotifyArgs(name, cache, script, volume)
	safe.Go("spotify audio", func() { pump(ctx, home.SpotifyName, r, level, nil) })
	supervise(ctx, program{
		name:   "librespot",
		path:   librespotPath,
		args:   args,
		stdout: w,
		env:    []string{"TECHO5_SPOTIFY_EVENTS=" + events},
		cred:   cred,
		keep:   librespotWorthLogging,
		started: func() {
			// A new librespot has no phone connected, whatever the last one said.
			if one != nil {
				one.started()
			}
			app.started()
		},
	})
}

// spotifyVolumePipe makes the pipe the device's volume goes to librespot through, in dir, and is its
// path; "" when librespot cannot take volumes from one (an older image's), or the pipe could not be
// made, so its slider moves the device by steps instead.
func spotifyVolumePipe(dir string, cred *syscall.Credential) string {
	if !librespotTakesVolume() {
		return ""
	}
	path := filepath.Join(dir, "spotify-volume")
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		slog.Warn("streaming: Spotify's volume pipe failed; its slider moves the device by steps", "err", err)
		return ""
	}
	handTo(cred, path)
	return path
}

// spotifyArgs are librespot's arguments, and the level the pump undoes its scaling by (nil for none).
// With a volume pipe librespot takes the device's volume from it and sends the audio unscaled, so
// there is nothing to undo; without one, it starts at full and scales linearly, which the pump undoes.
func spotifyArgs(name, cache, script, volume string) ([]string, *level) {
	args := []string{
		"--name", name,
		"--backend", "pipe",
		"--format", "S16",
		"--bitrate", "160",
		"--device-type", "speaker",
		"--disable-audio-cache",
		"--cache", cache,
		"--zeroconf-port", spotifyPort,
		"--onevent", script,
	}
	if volume != "" {
		return append(args, "--volume-fifo", volume), nil
	}
	return append(args, "--initial-volume", "100", "--volume-ctrl", "linear"), spotifyLevel
}

// librespotWorthLogging keeps librespot's warnings and errors, and what it says as it fails to start or
// crashes, and drops the rest, which names the account that logged in.
func librespotWorthLogging(line string) bool {
	for _, s := range []string{" WARN ", " ERROR ", "panicked", "error:", "Usage:"} {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// readSpotifyEvents follows the event pipe, telling the media player the song and the page its cover,
// and taking the app's volume as the device's: kept as one through the volume pipe by one, or, with
// none, moved by steps (app). Opened for writing as well, so it neither waits for librespot nor sees
// an end between events.
func readSpotifyEvents(ctx context.Context, path string, one *spotifySync, app *spotifyFallback) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		slog.Warn("streaming: opening Spotify's event pipe failed", "err", err)
		return
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	defer f.Close()
	track := func(title, artist, album, cover string) {
		media.Get().SetReceivedTrack(home.SpotifyName, title, artist, album)
		home.ReceivedArt(home.SpotifyName, cover)
	}
	if one != nil {
		defer media.Get().OnLevel.Listen(one.levelChanged)()
		followSpotifyEvents(f, spotifyEvents{
			track: track,
			volume: func(v int, connecting bool) {
				if connecting {
					slog.Info("streaming: Spotify connected", "volume", media.Get().Volume())
				}
				one.heard(v, connecting)
			},
			session: one.session,
			now:     time.Now,
		})
		return
	}
	followSpotifyEvents(f, spotifyEvents{
		track: track,
		volume: func(v int, connecting bool) {
			spotifyLevel.set(spotifyGain(v), spotifyQueued())
			if connecting {
				slog.Info("streaming: Spotify connected", "volume", spotifyStep(v))
			}
			app.heard(v, connecting, media.Get().Volume, media.Get().Set)
		},
		session: func(connected bool) { app.session(connected, media.Get().Volume, media.Get().Set) },
		now:     time.Now,
	})
}

// spotifyFallback is the app's slider moving the device by steps, for a librespot that takes no
// volume from outside, kept as librespot restarts and phones come and go.
type spotifyFallback struct {
	mu     sync.Mutex
	slider slider
}

// heard takes the app's level v, moving the device's volume (got from device, set by set) by as many
// steps as the slider moved.
func (a *spotifyFallback) heard(v int, connecting bool, device func() int, set func(int)) {
	a.mu.Lock()
	by := a.slider.moved(v, connecting, time.Now())
	a.mu.Unlock()
	// The app says each level more than once as the slider moves; those move nothing.
	if by != 0 {
		set(device() + by)
	}
}

// session is a phone connecting or going; a move heard just before a connect is taken back.
func (a *spotifyFallback) session(connected bool, device func() int, set func(int)) {
	a.mu.Lock()
	undo := a.slider.session(connected, time.Now())
	a.mu.Unlock()
	if undo != 0 {
		slog.Info("streaming: a Spotify volume heard as a phone connected is taken back")
		set(device() + undo)
	}
}

// started is librespot starting again: the slider is wherever the next phone has it.
func (a *spotifyFallback) started() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.slider.session(false, time.Now())
}

// spotifyEvents is what the event lines are turned into.
type spotifyEvents struct {
	// track is a new song, or nothing when playing stops.
	track func(title, artist, album, cover string)
	// volume is the app's volume, 0..spotifyFull; connecting when it came in the moments after a phone
	// connected, and so only says where librespot was left.
	volume func(v int, connecting bool)
	// session, when there is one, is a phone connecting, or going.
	session func(connected bool)
	now     func() time.Time
}

// followSpotifyEvents reads event lines from r until it ends. A line too long to be one of the
// program's is skipped, not taken as the end.
func followSpotifyEvents(r io.Reader, on spotifyEvents) {
	br := bufio.NewReaderSize(r, 4096)
	var connected time.Time
	for {
		line, err := readLine(br, 16<<10)
		if err != nil {
			return
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			continue
		}
		// The program cuts each field at a byte count, which can fall inside a character.
		for i := range f {
			f[i] = strings.ToValidUTF8(f[i], "")
		}
		switch f[0] {
		case "track_changed":
			on.track(f[1], f[2], f[3], f[4])
		case "stopped":
			on.track("", "", "", "")
		case "session_disconnected":
			on.track("", "", "", "")
			if on.session != nil {
				on.session(false)
			}
		case "session_connected":
			connected = on.now()
			if on.session != nil {
				on.session(true)
			}
		case "volume_changed":
			v, err := strconv.Atoi(strings.TrimSpace(f[5]))
			if err != nil || v < 0 || v > spotifyFull {
				continue
			}
			// Not only the first after a connect: librespot can say its level more than once as it
			// starts, and in whatever order the event programs finish.
			connecting := !connected.IsZero() && on.now().Sub(connected) < connectQuiet
			on.volume(v, connecting)
		}
	}
}

// readLine is the next line from br without its newline, or "" for one longer than most, which is read
// through and dropped.
func readLine(br *bufio.Reader, most int) (string, error) {
	var line []byte
	over := false
	for {
		part, isPrefix, err := br.ReadLine()
		if err != nil {
			return "", err
		}
		if !over {
			line = append(line, part...)
			over = len(line) > most
		}
		if !isPrefix {
			break
		}
	}
	if over {
		return "", nil
	}
	return string(line), nil
}
