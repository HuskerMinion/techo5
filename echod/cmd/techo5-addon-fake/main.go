// Command techo5-addon-fake is the add-on API without a device, for add-on authors (docs/addon-api.md):
// it serves an add-on's socket with the daemon's own server (pkg/addonapi/serve), so the framing, the
// peer check, the limits and every capability's refusal are the device's, and logs what each request
// would have done instead of doing it. From the keyboard it sends the add-on what a device would: a
// command from Home Assistant, a button press, a timer ringing, new settings, a settings test, what
// the media it started is doing (media.state), and taps, swipes and the back gesture on its page and
// card. Its scenes are checked as the device checks them, and said in the log.
//
//	techo5-addon-fake -manifest addon.json [-settings values.json] [-socket path] [-- program args...]
//
// With a program after --, it runs it with TECHO5_ADDON_API set, as the device does; without, it
// prints the export line to run the add-on by hand. Type help for the keyboard's commands.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/feature/display"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi/serve"
)

func main() {
	manifest := flag.String("manifest", "addon.json", "the add-on's addon.json")
	values := flag.String("settings", "", "a JSON file of the add-on's settings, by key")
	socket := flag.String("socket", "", "where to make the socket's folder (default: a temporary folder)")
	family := flag.String("family", "show", "the device family hello tells: show, spot or dot")
	pngTo := flag.String("png", "", "draw the add-on's page to this PNG, and its card beside it (page-card.png), each time they change")
	size := flag.String("size", "", "the screen the PNG is drawn for: show5, show8 or spot (default: show8, or spot with -family spot)")
	flag.Parse()
	if err := run(*manifest, *values, *socket, *family, flag.Args(), os.Stdin, os.Stdout, drawing{to: *pngTo, size: *size}); err != nil {
		fmt.Fprintln(os.Stderr, "techo5-addon-fake:", err)
		os.Exit(1)
	}
}

// screens are each family's screen, as hello says them.
var screens = map[string][2]int{"show": {960, 480}, "spot": {480, 480}, "dot": {0, 0}}

func run(manifest, values, socket, family string, program []string, in io.Reader, out io.Writer, draw drawing) error {
	b, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	// Read as the device reads it: one it would refuse is refused here first.
	m, err := catalog.ParseManifest(b)
	if err != nil {
		return err
	}
	if m.API == 0 {
		return errors.New("the manifest does not speak the API: give it \"api\": 1 and its capabilities")
	}
	if os.Getuid() == 0 {
		return errors.New("run it as yourself, not as root: the API refuses root, as the device does")
	}
	if draw.size == "" {
		draw.size = "show8"
		if family == "spot" {
			draw.size = "spot"
		}
	}
	if _, ok := display.AddonScreens[draw.size]; draw.to != "" && !ok {
		return fmt.Errorf("-size %q: the PNG is drawn for show5, show8 or spot", draw.size)
	}
	d := &fake{out: out, family: family, timers: map[string]*fakeTimer{}, settings: map[string]any{}, draw: draw}
	if values != "" {
		vb, err := os.ReadFile(values)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(vb, &d.settings); err != nil {
			return fmt.Errorf("%s: %w", values, err)
		}
	}
	if socket == "" {
		dir, err := os.MkdirTemp("", "techo5-addon")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		socket = dir
	}
	uid := uint32(os.Getuid())
	s := serve.New(socket, d, func(u uint32) bool { return u == uid })
	s.Chown = func(string, int) error { return nil } // it is the author's own user already
	s.Trace = func(id string, req addonapi.Request, err error) {
		result := "ok"
		if err != nil {
			result = err.Error()
		}
		d.say("request %s %s: %s", req.Op, string(req.Args), result)
	}
	d.srv = s
	// A dot has no screen, and refuses the screen's ops as a device does.
	if family != "dot" {
		s.SetScreen(fakeScreen{d: d})
	}
	path, err := s.Open(serve.Grant{ID: m.ID, UID: uid, Caps: m.Capabilities, Commands: m.Commands, Entities: m.Entities,
		Settings: m.Settings, Files: m.Files, BinDir: filepath.Dir(manifest)})
	if err != nil {
		return err
	}
	defer s.Close(m.ID)
	// The name bar says the name the add-on's catalog entry gives it; the fake has only its id.
	d.id, d.name = m.ID, m.ID
	fmt.Fprintf(out, "serving %s on %s\n", m.ID, path)
	if len(program) > 0 {
		cmd := exec.Command(program[0], program[1:]...)
		cmd.Env = append(os.Environ(), addonapi.EnvSocket+"="+path)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			return err
		}
		defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	} else {
		fmt.Fprintf(out, "run the add-on with:\n  export %s=%s\n", addonapi.EnvSocket, path)
	}
	return d.keyboard(in, m)
}

// fake is a device that says what it would do.
type fake struct {
	srv    *serve.Server
	id     string
	name   string
	family string
	draw   drawing

	mu       sync.Mutex
	out      io.Writer
	timers   map[string]*fakeTimer
	next     int
	settings map[string]any

	// The add-on's latest media, as the fake plays it: its state, and its position as of posAt, which
	// moves on while it plays, as a device's does.
	mediaSeq int
	media    addonapi.MediaState
	pos      time.Duration
	posAt    time.Time
}

type fakeTimer struct {
	name    string
	total   time.Duration
	ends    time.Time
	t       *time.Timer
	ringing bool
}

func (d *fake) say(format string, a ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.out, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func (d *fake) Info() addonapi.Device {
	s := screens[d.family]
	return addonapi.Device{Family: d.family, Width: s[0], Height: s[1], Language: "en", Version: "fake"}
}

func (d *fake) Settings(string) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]any{}
	for k, v := range d.settings {
		out[k] = v
	}
	return out
}

func (d *fake) Connected() bool { return true }
func (d *fake) Quiet() bool     { return false }

func (d *fake) Say(ctx context.Context, id, text string) error {
	d.say("says: %q", text)
	// As long as the words would take, roughly, so a queue of them is seen to wait its turn.
	select {
	case <-time.After(time.Duration(len(text)) * 60 * time.Millisecond):
	case <-ctx.Done():
	}
	return nil
}

func (d *fake) PlaySound(id, name string, w *addonapi.WAV) error {
	if w != nil {
		d.say("plays its own sound: %v at %d Hz", w.Length().Round(time.Millisecond), w.Rate)
		return nil
	}
	d.say("plays the sound %s", name)
	return nil
}

func (d *fake) StartTimer(id, name string, dur time.Duration) (string, error) {
	d.mu.Lock()
	d.next++
	tid := fmt.Sprintf("local:fake%d", d.next)
	ft := &fakeTimer{name: name, total: dur, ends: time.Now().Add(dur)}
	ft.t = time.AfterFunc(dur, func() { d.ring(tid) })
	d.timers[tid] = ft
	d.mu.Unlock()
	d.say("sets the timer %s (%s) for %v", tid, name, dur)
	return tid, nil
}

func (d *fake) ring(tid string) {
	d.mu.Lock()
	ft, ok := d.timers[tid]
	if ok {
		ft.ringing = true
	}
	d.mu.Unlock()
	if !ok {
		return
	}
	d.say("the timer %s (%s) rings; type stop to stop it", tid, ft.name)
	d.srv.Emit(d.id, addonapi.EventTimer, addonapi.TimerEvent{ID: tid, Name: ft.name, State: addonapi.TimerRinging})
}

func (d *fake) CancelTimer(id, tid string) bool {
	d.mu.Lock()
	ft, ok := d.timers[tid]
	if ok {
		ft.t.Stop()
		delete(d.timers, tid)
	}
	d.mu.Unlock()
	if ok {
		d.say("cancels the timer %s", tid)
	}
	return ok
}

func (d *fake) Timers(id string) []addonapi.Timer {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []addonapi.Timer
	for tid, ft := range d.timers {
		if ft.ringing {
			continue
		}
		out = append(out, addonapi.Timer{ID: tid, Name: ft.name, Left: max(time.Until(ft.ends), 0).Seconds(),
			Total: ft.total.Seconds(), Running: true})
	}
	return out
}

func (d *fake) SetAwake(held bool) {
	if held {
		d.say("keeps the screen awake")
	} else {
		d.say("lets the screen sleep")
	}
}

func (d *fake) Fire(id, name string, data map[string]string) error {
	d.say("puts esphome.techo5_addon on Home Assistant's bus: %s %v", name, data)
	return nil
}

func (d *fake) SetEntity(id string, e addonapi.Entity, state any) error {
	d.say("sets %s.addon_%s_%s to %v", e.Domain, strings.ReplaceAll(id, "-", "_"), e.ID, state)
	return nil
}

// fakeBuffers is how long the fake's media takes to start playing.
var fakeBuffers = time.Second

func (d *fake) Media(id, op string, p addonapi.MediaPlay) (string, error) {
	if op == addonapi.OpMediaPlay {
		d.mu.Lock()
		prev := d.endLocked(addonapi.MediaReplaced)
		d.mediaSeq++
		media := fmt.Sprint(d.mediaSeq)
		d.media, d.pos, d.posAt = addonapi.MediaState{Media: media, Kind: p.Kind, State: addonapi.MediaBuffering}, 0, time.Now()
		d.mu.Unlock()
		if prev != nil {
			d.srv.MediaEnded(d.id, *prev)
		}
		d.say("plays %s %s (%q) as media %s; it buffers for %v, then plays (type help for media)", p.Kind, p.URL, p.Title, media, fakeBuffers)
		time.AfterFunc(fakeBuffers, func() {
			d.mu.Lock()
			now := d.media.Media == media && d.media.State == addonapi.MediaBuffering
			if now {
				d.setLocked(addonapi.MediaPlaying, -1)
			}
			d.mu.Unlock()
			if now {
				d.srv.MediaChanged(d.id)
			}
		})
		return media, nil
	}
	d.mu.Lock()
	on := d.media.Media != "" && d.media.State != addonapi.MediaStopped
	var end *addonapi.MediaState
	if on {
		switch op {
		case addonapi.OpMediaPause:
			d.setLocked(addonapi.MediaPaused, -1)
		case addonapi.OpMediaResume:
			d.setLocked(addonapi.MediaPlaying, -1)
		case addonapi.OpMediaStop:
			end = d.endLocked(addonapi.MediaStoppedByAddon)
		}
	}
	d.mu.Unlock()
	if !on {
		return "", addonapi.Errorf(addonapi.CodeBadRequest, "nothing the add-on started is playing")
	}
	if end != nil {
		d.srv.MediaEnded(d.id, *end)
	}
	d.say("%s", strings.TrimPrefix(op, "media."))
	return "", nil
}

// setLocked moves the media to state, and its position to pos when that is not negative.
func (d *fake) setLocked(state string, pos time.Duration) {
	d.pos = d.positionLocked()
	if pos >= 0 {
		d.pos = pos
	}
	d.posAt = time.Now()
	d.media.State = state
}

// positionLocked is where the media is now: it moves on only while it plays.
func (d *fake) positionLocked() time.Duration {
	if d.media.State == addonapi.MediaPlaying {
		return d.pos + time.Since(d.posAt)
	}
	return d.pos
}

// endLocked ends the media for reason, when one is on, and answers its end to tell the add-on.
func (d *fake) endLocked(reason string) *addonapi.MediaState {
	if d.media.Media == "" || d.media.State == addonapi.MediaStopped {
		return nil
	}
	d.setLocked(addonapi.MediaStopped, -1)
	d.media.Reason = reason
	st := d.statusLocked()
	return &st
}

func (d *fake) statusLocked() addonapi.MediaState {
	if d.media.Media == "" {
		return addonapi.MediaState{State: addonapi.MediaIdle}
	}
	st := d.media
	st.PositionMs = addonapi.Ms(d.positionLocked())
	return st
}

func (d *fake) MediaStatus(string) addonapi.MediaState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.statusLocked()
}

// mediaKeys is the keyboard's media command: what the add-on's media does on the device.
func (d *fake) mediaKeys(rest string) {
	verb, arg, _ := strings.Cut(rest, " ")
	arg = strings.TrimSpace(arg)
	secs := func() (time.Duration, bool) {
		if arg == "" {
			return -1, true
		}
		var f float64
		if _, err := fmt.Sscan(arg, &f); err != nil || f < 0 {
			d.say("media %s takes a number of seconds", verb)
			return 0, false
		}
		return time.Duration(f * float64(time.Second)), true
	}
	d.mu.Lock()
	if d.media.Media == "" || d.media.State == addonapi.MediaStopped {
		d.mu.Unlock()
		d.say("the add-on has no media playing; it starts some with media.play")
		return
	}
	d.mu.Unlock()
	switch verb {
	case addonapi.MediaPlaying, addonapi.MediaPaused, addonapi.MediaBuffering:
		at, ok := secs()
		if !ok {
			return
		}
		d.mu.Lock()
		d.setLocked(verb, at)
		d.mu.Unlock()
		d.srv.MediaChanged(d.id)
	case "duration":
		at, ok := secs()
		if !ok || at < 0 {
			return
		}
		d.mu.Lock()
		d.media.DurationMs = addonapi.Ms(at)
		d.mu.Unlock()
		d.srv.MediaChanged(d.id)
	case addonapi.MediaEnded, addonapi.MediaStoppedOnDevice, addonapi.MediaReplaced, addonapi.MediaError:
		d.mu.Lock()
		end := d.endLocked(verb)
		d.mu.Unlock()
		if end != nil {
			d.srv.MediaEnded(d.id, *end)
		}
	default:
		d.say("media takes playing, paused or buffering [SECONDS], duration SECONDS, or an end: ended, device, replaced, error")
		return
	}
	d.say("the media is now %s", verb)
}

const help = `the keyboard's commands:
  command NAME [JSON]   call a command from Home Assistant, as addon_command does, and wait for its answer
  press NAME            press a command's button, which waits for nothing
  entity ID JSON        Home Assistant sets one of the add-on's entities (a switch, select or number)
  push ID               Home Assistant presses one of the add-on's button entities
  settings JSON         the setup page saves these settings
  test [JSON]           the setup page's Test, with these values (or the ones saved)
  stop                  the stop word: stops every timer ringing
  media STATE [SECONDS] the add-on's media is playing, paused or buffering (at SECONDS in), as media.state says
  media duration SECONDS the add-on's media says how long it is
  media REASON          the add-on's media ends: ended, device (the Stop button), replaced (other media), error
  tile                  tap the add-on's tile under the Add-ons tab: its page comes up
  touch ID [page|card]  tap the button, chip or list item of this id on the page (or the card) in view
  swipe left|right      swipe across the page in view
  back                  the back gesture on the page in view
  shown page|card on|off the page or the card comes into view, or goes (a ring covering it, say); the add-on
                        is told of its page, and of its card nothing
  dismiss               swipe the card away (the add-on is told dismissed)
  close                 the name bar's way out: the page goes
  scene [page|card]     print the page's (or the card's) scene as it is now
  quit                  stop serving`

// keyboard reads what to send the add-on, a line at a time.
func (d *fake) keyboard(in io.Reader, m catalog.Manifest) error {
	fmt.Fprintln(d.out, "type help for what the keyboard does")
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		verb, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch verb {
		case "":
		case "help":
			fmt.Fprintln(d.out, help)
		case "quit", "exit":
			return nil
		case "command", "press":
			name, raw, _ := strings.Cut(rest, " ")
			var args map[string]any
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &args); err != nil {
					d.say("the arguments are a JSON object: %v", err)
					continue
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			text, err := d.srv.Command(ctx, d.id, name, args, map[bool]string{true: "action", false: "button"}[verb == "command"], verb == "command")
			cancel()
			switch {
			case err != nil:
				d.say("%s %s: %v", verb, name, err)
			case verb == "command":
				d.say("Home Assistant is answered: %q", text)
			}
		case "entity", "push":
			id, raw, _ := strings.Cut(rest, " ")
			if _, ok := addonapi.FindEntity(m.Entities, id); !ok {
				d.say("the add-on declares no entity %q", id)
				continue
			}
			ev := addonapi.EntityCommand{ID: id, Press: verb == "push"}
			if verb == "entity" {
				if err := json.Unmarshal([]byte(raw), &ev.State); err != nil {
					d.say("the state is JSON: %v", err)
					continue
				}
			}
			d.srv.Emit(d.id, addonapi.EventEntityCommand, ev)
		case "settings":
			var v map[string]any
			if err := json.Unmarshal([]byte(rest), &v); err != nil {
				d.say("settings are a JSON object: %v", err)
				continue
			}
			d.mu.Lock()
			d.settings = v
			d.mu.Unlock()
			d.srv.Emit(d.id, addonapi.EventSettings, addonapi.Values{Values: v})
		case "test":
			v := d.Settings(d.id)
			if rest != "" {
				if err := json.Unmarshal([]byte(rest), &v); err != nil {
					d.say("settings are a JSON object: %v", err)
					continue
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			ok, why, err := d.srv.Test(ctx, d.id, v)
			cancel()
			switch {
			case err != nil:
				d.say("test: %v", err)
			case ok:
				d.say("the add-on says the settings work")
			default:
				d.say("the add-on says the settings do not work: %s", why)
			}
		case "media":
			d.mediaKeys(rest)
		case "tile", "touch", "swipe", "back", "shown", "dismiss", "close", "scene":
			d.screenKeys(verb, rest)
		case "stop":
			d.mu.Lock()
			var rung []addonapi.TimerEvent
			for tid, ft := range d.timers {
				if ft.ringing {
					rung = append(rung, addonapi.TimerEvent{ID: tid, Name: ft.name, State: addonapi.TimerStopped})
					delete(d.timers, tid)
				}
			}
			d.mu.Unlock()
			for _, e := range rung {
				d.srv.Emit(d.id, addonapi.EventTimer, e)
			}
			d.say("stopped %d ringing", len(rung))
		default:
			d.say("no such command %q; type help", verb)
		}
	}
	slog.Debug("the keyboard closed")
	return sc.Err()
}
