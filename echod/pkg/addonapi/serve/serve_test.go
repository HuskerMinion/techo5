package serve

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// fakeDevice records what the API asked of it.
type fakeDevice struct {
	mu        sync.Mutex
	connected bool
	quiet     bool
	said      []string
	sayWait   chan struct{} // when set, Say waits on it
	sounds    []string
	timers    map[string][]addonapi.Timer
	next      int
	awake     []bool
	fired     []string
	entities  map[string]any
	media     []string
	settings  map[string]any
	status    addonapi.MediaState
	plays     int

	failEntity error
	timerDelay time.Duration
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{connected: true, timers: map[string][]addonapi.Timer{}, entities: map[string]any{},
		settings: map[string]any{"token": "s3cret"}}
}

func (d *fakeDevice) Info() addonapi.Device {
	return addonapi.Device{Family: "show", Board: "cronos", Width: 960, Height: 480, Language: "en"}
}
func (d *fakeDevice) Settings(string) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.settings
}
func (d *fakeDevice) Connected() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.connected }
func (d *fakeDevice) Quiet() bool     { d.mu.Lock(); defer d.mu.Unlock(); return d.quiet }
func (d *fakeDevice) Say(ctx context.Context, id, text string) error {
	d.mu.Lock()
	wait := d.sayWait
	d.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	d.said = append(d.said, text)
	d.mu.Unlock()
	return nil
}
func (d *fakeDevice) PlaySound(id, name string, w *addonapi.WAV) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w != nil {
		name = fmt.Sprintf("wav:%d", len(w.Samples))
	}
	d.sounds = append(d.sounds, name)
	return nil
}
func (d *fakeDevice) StartTimer(id, name string, dur time.Duration) (string, error) {
	d.mu.Lock()
	wait := d.timerDelay
	d.mu.Unlock()
	time.Sleep(wait) // a device that takes a moment, so two connections overlap
	d.mu.Lock()
	defer d.mu.Unlock()
	d.next++
	tid := fmt.Sprintf("local:%d", d.next)
	d.timers[id] = append(d.timers[id], addonapi.Timer{ID: tid, Name: name, Left: dur.Seconds(), Total: dur.Seconds(), Running: true})
	return tid, nil
}
func (d *fakeDevice) CancelTimer(id, tid string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, t := range d.timers[id] {
		if t.ID == tid {
			d.timers[id] = append(d.timers[id][:i], d.timers[id][i+1:]...)
			return true
		}
	}
	return false
}
func (d *fakeDevice) Timers(id string) []addonapi.Timer {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]addonapi.Timer(nil), d.timers[id]...)
}
func (d *fakeDevice) SetAwake(held bool) { d.mu.Lock(); d.awake = append(d.awake, held); d.mu.Unlock() }
func (d *fakeDevice) Fire(id, name string, data map[string]string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fired = append(d.fired, name)
	return nil
}
func (d *fakeDevice) SetEntity(id string, e addonapi.Entity, state any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failEntity != nil {
		return d.failEntity
	}
	d.entities[e.ID] = state
	return nil
}
func (d *fakeDevice) Media(id, op string, p addonapi.MediaPlay) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.media = append(d.media, op+" "+p.Kind+" "+p.URL)
	if op != addonapi.OpMediaPlay {
		return "", nil
	}
	d.plays++
	media := fmt.Sprint(d.plays)
	d.status = addonapi.MediaState{Media: media, Kind: p.Kind, State: addonapi.MediaBuffering}
	return media, nil
}
func (d *fakeDevice) MediaStatus(string) addonapi.MediaState {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status.State == "" {
		return addonapi.MediaState{State: addonapi.MediaIdle}
	}
	return d.status
}

func (d *fakeDevice) awakeNow() (bool, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.awake) == 0 {
		return false, 0
	}
	return d.awake[len(d.awake)-1], len(d.awake)
}

// everything is a grant of every capability, with a command, an entity, settings and a sound file.
func everything(dir string) Grant {
	return Grant{ID: "meals", UID: uint32(os.Getuid()), Caps: addonapi.Capabilities,
		Commands: []addonapi.Command{{Name: "next"}, {Name: "step", Args: map[string]addonapi.Arg{"to": {Type: addonapi.TypeString, Max: 4, Required: true}}}},
		Entities: []addonapi.Entity{{Domain: addonapi.DomainSensor, ID: "step", Name: "Step", Text: true}, {Domain: addonapi.DomainButton, ID: "go", Name: "Go"}},
		Settings: &addonapi.Settings{Test: true, Fields: []addonapi.Setting{{Key: "token", Label: "Token", Type: addonapi.TypeSecret}}},
		Files:    []string{"ding.wav", "notes.txt"}, BinDir: dir}
}

type rig struct {
	t    *testing.T
	s    *Server
	d    *fakeDevice
	path string
	dir  string

	// The clock and the peer, which a test changes while the server reads them.
	mu      sync.Mutex
	frozen  time.Time
	peer    func(net.Conn) (uint32, error)
	allowed bool
}

func (r *rig) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen.IsZero() {
		return time.Now()
	}
	return r.frozen
}

// freeze stops the server's clock at a moment, and advance moves it on.
func (r *rig) freeze() {
	r.mu.Lock()
	r.frozen = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	r.mu.Unlock()
}

func (r *rig) advance(d time.Duration) {
	r.mu.Lock()
	r.frozen = r.frozen.Add(d)
	r.mu.Unlock()
}

func (r *rig) setPeer(f func(net.Conn) (uint32, error)) { r.mu.Lock(); r.peer = f; r.mu.Unlock() }
func (r *rig) setAllowed(ok bool)                       { r.mu.Lock(); r.allowed = ok; r.mu.Unlock() }

func newRig(t *testing.T, g func(dir string) Grant) *rig {
	t.Helper()
	// A short folder: a Unix socket's path is held to 108 bytes.
	dir, err := os.MkdirTemp("", "aapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := newFakeDevice()
	r := &rig{t: t, d: d, dir: dir, peer: peerUID, allowed: true}
	s := New(filepath.Join(dir, "run"), d, func(uint32) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.allowed })
	s.Chown = func(string, int) error { return nil }
	s.Now = r.now
	s.PeerUID = func(c net.Conn) (uint32, error) { r.mu.Lock(); f := r.peer; r.mu.Unlock(); return f(c) }
	r.s = s
	path, err := s.Open(g(dir))
	if err != nil {
		t.Fatal(err)
	}
	r.path = path
	t.Cleanup(func() { s.Close("meals") })
	return r
}

func (r *rig) dial() *addonapi.Client {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	return c
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func code(err error) string {
	if err == nil {
		return ""
	}
	return addonapi.CodeOf(err)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited too long for %s", what)
}

func TestSocketFolder(t *testing.T) {
	r := newRig(t, everything)
	fi, err := os.Stat(filepath.Dir(r.path))
	// The folder is root's, searchable and nothing more: the add-on can reach its socket and put
	// nothing beside it.
	if err != nil || fi.Mode().Perm() != 0o711 {
		t.Fatalf("the folder: %v %v", fi.Mode(), err)
	}
	fi, err = os.Stat(r.path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the socket: %v %v", fi.Mode(), err)
	}
	r.s.Close("meals")
	if _, err := os.Stat(filepath.Dir(r.path)); !os.IsNotExist(err) {
		t.Errorf("the folder outlived Close: %v", err)
	}
	if _, err := r.s.Open(Grant{ID: "rooted", UID: 0}); !errors.Is(err, ErrRoot) {
		t.Errorf("a root add-on was given a socket: %v", err)
	}
}

func TestHello(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	w := c.Welcome()
	if w.API != addonapi.Version || w.Addon != "meals" || w.Device.Width != 960 || w.Settings["token"] != "s3cret" ||
		len(w.Capabilities) != len(addonapi.Capabilities) {
		t.Errorf("welcome %+v", w)
	}
	if n := r.s.Connected("meals"); n != 1 {
		t.Errorf("connected %d", n)
	}
}

// raw is a connection that speaks frames by hand, for what the client would never send.
type raw struct {
	t  *testing.T
	nc net.Conn
}

func (r *rig) raw() *raw {
	nc, err := net.Dial("unix", r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { nc.Close() })
	return &raw{t: r.t, nc: nc}
}

// send writes a message; a daemon that has hung up shows in what read returns.
func (w *raw) send(s string) { _ = addonapi.WriteFrame(w.nc, []byte(s)) }

// read is the next message, or "" once the daemon has hung up.
func (w *raw) read() string {
	w.t.Helper()
	_ = w.nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, _, err := addonapi.ReadFrame(w.nc, addonapi.MaxMessage)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestFirstIsHello(t *testing.T) {
	r := newRig(t, everything)
	w := r.raw()
	w.send(`{"id":1,"op":"timer.list"}`)
	if got := w.read(); !strings.Contains(got, `"bad_request"`) || !strings.Contains(got, "hello") {
		t.Errorf("a request before hello: %s", got)
	}
	w.send(`{"id":2,"op":"hello","args":{"api":0}}`)
	if got := w.read(); !strings.Contains(got, `"bad_request"`) {
		t.Errorf("hello at level 0: %s", got)
	}
	w.send(`{"id":3,"op":"hello","args":{"api":1}}`)
	if got := w.read(); !strings.Contains(got, `"ok":true`) {
		t.Errorf("hello: %s", got)
	}
	w.send(`{"id":4,"op":"nope"}`)
	if got := w.read(); !strings.Contains(got, `"unknown_op"`) {
		t.Errorf("an unknown op: %s", got)
	}
	w.send(`{"id":5,"op":"speak","args":{"text":"hi","loud":true}}`)
	if got := w.read(); !strings.Contains(got, `"bad_request"`) {
		t.Errorf("an unknown argument: %s", got)
	}
	w.send(`{"id":6,"op":"hello","Op":"x"}`)
	if got := w.read(); !strings.Contains(got, `"bad_request"`) || !strings.Contains(got, `"id":0`) {
		t.Errorf("a key in capitals: %s", got)
	}
}

// Who connects is the kernel's word: another user, root, or a user that is no add-on switched on is
// closed at once.
func TestPeerRefused(t *testing.T) {
	for name, set := range map[string]func(r *rig){
		"wrong uid": func(r *rig) {
			r.setPeer(func(net.Conn) (uint32, error) { return uint32(os.Getuid()) + 1, nil })
		},
		"root":        func(r *rig) { r.setPeer(func(net.Conn) (uint32, error) { return 0, nil }) },
		"not allowed": func(r *rig) { r.setAllowed(false) },
		"unreadable": func(r *rig) {
			r.setPeer(func(net.Conn) (uint32, error) { return 0, errors.New("no") })
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, everything)
			set(r)
			w := r.raw()
			w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
			if got := w.read(); got != "" {
				t.Errorf("answered: %s", got)
			}
		})
	}
}

func TestConnectionCap(t *testing.T) {
	r := newRig(t, everything)
	for range addonapi.MaxConns {
		r.dial()
	}
	w := r.raw()
	w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
	if got := w.read(); got != "" {
		t.Errorf("a fifth connection was answered: %s", got)
	}
}

func TestCapabilityDenied(t *testing.T) {
	r := newRig(t, func(dir string) Grant {
		g := everything(dir)
		g.Caps = []string{addonapi.CapTimers}
		return g
	})
	c := r.dial()
	for op, a := range map[string]any{
		addonapi.OpSpeak:        addonapi.Speak{Text: "hi"},
		addonapi.OpSoundPlay:    addonapi.Sound{Name: "chime"},
		addonapi.OpAwakeHold:    addonapi.AwakeHold{Seconds: 10},
		addonapi.OpSettingsGet:  addonapi.None{},
		addonapi.OpEntitySet:    addonapi.EntitySet{ID: "step", State: "x"},
		addonapi.OpEventFire:    addonapi.Fire{Name: "x"},
		addonapi.OpMediaPlay:    addonapi.MediaPlay{URL: "http://x/a.mp4", Kind: "video"},
		addonapi.OpMediaStatus:  addonapi.None{},
		addonapi.OpCommandReply: addonapi.CommandReply{Call: 1},
	} {
		if err := c.Call(ctx(t), op, a, nil); code(err) != addonapi.CodeDenied {
			t.Errorf("%s without its capability: %v", op, err)
		}
	}
	if c.Welcome().Settings != nil {
		t.Error("hello gave settings to an add-on without the settings capability")
	}
	if _, err := c.StartTimer(ctx(t), "eggs", time.Minute); err != nil {
		t.Errorf("a granted capability: %v", err)
	}
	if _, err := r.s.Command(ctx(t), "meals", "next", nil, "button", false); code(err) != addonapi.CodeDenied {
		t.Errorf("a command to an add-on without commands: %v", err)
	}
}

func TestOversized(t *testing.T) {
	r := newRig(t, everything)
	w := r.raw()
	w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
	w.read()
	big := make([]byte, 4+addonapi.MaxMessage+10)
	binary.BigEndian.PutUint32(big, uint32(addonapi.MaxMessage+10))
	if _, err := w.nc.Write(big); err != nil {
		t.Fatal(err)
	}
	if got := w.read(); !strings.Contains(got, `"too_large"`) {
		t.Fatalf("a message past the limit: %s", got)
	}
	// It stays in step: the next request is answered.
	w.send(`{"id":2,"op":"timer.list"}`)
	if got := w.read(); !strings.Contains(got, `"id":2`) || !strings.Contains(got, `"ok":true`) {
		t.Errorf("after a message past the limit: %s", got)
	}
	// One claiming far more is hung up on, unread.
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], 64<<20)
	_, _ = w.nc.Write(h[:])
	if got := w.read(); got != "" {
		t.Errorf("a message claiming 64 MB was answered: %s", got)
	}
}

func TestSpeakTooLong(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	if _, err := c.Speak(ctx(t), strings.Repeat("a", addonapi.MaxSpeak+1)); code(err) != addonapi.CodeTooLarge {
		t.Errorf("401 characters: %v", err)
	}
	if err := c.Fire(ctx(t), "x", map[string]string{"a": strings.Repeat("b", addonapi.MaxEventData)}); code(err) != addonapi.CodeTooLarge {
		t.Errorf("an event past 1 KB: %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	c := r.dial()
	limited := 0
	for range addonapi.Burst + 5 {
		if _, err := c.Timers(ctx(t)); code(err) == addonapi.CodeRateLimited {
			limited++
		}
	}
	if limited != 6 { // hello took one of the burst
		t.Errorf("past the burst of %d, %d were limited", addonapi.Burst, limited)
	}
	r.advance(time.Second)
	if _, err := c.Timers(ctx(t)); err != nil {
		t.Errorf("after a second: %v", err)
	}
}

// An add-on that keeps going past its limits is disconnected for a minute, and its card says so.
func TestBan(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	c := r.dial()
	for range strikeLimit.Load() {
		_ = c.Fire(ctx(t), "x", nil) // 2 a second: the rest are strikes
		_ = c.Fire(ctx(t), "x", nil)
		_ = c.Fire(ctx(t), "x", nil)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not disconnected")
	}
	if _, banned := r.s.Banned("meals"); !banned {
		t.Error("not banned")
	}
	w := r.raw()
	w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
	if got := w.read(); got != "" {
		t.Errorf("answered while banned: %s", got)
	}
	r.advance(banFor)
	r.dial()
}

func TestSpeak(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	r.d.sayWait = make(chan struct{})
	n, err := c.Speak(ctx(t), "Step one")
	if err != nil || n != 1 {
		t.Fatalf("speak: %d %v", n, err)
	}
	eventually(t, "the first to be taken", func() bool {
		r.s.mu.Lock()
		defer r.s.mu.Unlock()
		return len(r.s.addons["meals"].speech) == 0
	})
	for i := range addonapi.MaxSpeakQueue {
		if _, err := c.Speak(ctx(t), fmt.Sprint("more ", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Speak(ctx(t), "one too many"); code(err) != addonapi.CodeBusy {
		t.Errorf("a fourth in the queue: %v", err)
	}
	if n, err := c.CancelSpeech(ctx(t)); err != nil || n != addonapi.MaxSpeakQueue {
		t.Errorf("cancel: %d %v", n, err)
	}
	close(r.d.sayWait)
	eventually(t, "the one being said", func() bool {
		r.d.mu.Lock()
		defer r.d.mu.Unlock()
		return len(r.d.said) == 1
	})
	r.d.mu.Lock()
	r.d.quiet = true
	r.d.mu.Unlock()
	if _, err := c.Speak(ctx(t), "at night"); code(err) != addonapi.CodeBusy {
		t.Errorf("speak in quiet hours: %v", err)
	}
	r.d.mu.Lock()
	r.d.quiet, r.d.connected = false, false
	r.d.mu.Unlock()
	if _, err := c.Speak(ctx(t), "no HA"); code(err) != addonapi.CodeUnavailable {
		t.Errorf("speak without Home Assistant: %v", err)
	}
}

func TestSound(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	advance := func() { r.advance(time.Second) }
	c := r.dial()
	if err := c.PlaySound(ctx(t), "chime"); err != nil {
		t.Fatal(err)
	}
	if err := c.PlaySound(ctx(t), "done"); code(err) != addonapi.CodeRateLimited {
		t.Errorf("two sounds in a second: %v", err)
	}
	advance()
	if err := c.PlaySound(ctx(t), "fanfare"); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a sound not in the set: %v", err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "ding.wav"), wavBytes(16000, 8000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.PlayFile(ctx(t), "ding.wav"); err != nil {
		t.Errorf("its own WAV: %v", err)
	}
	advance()
	for _, f := range []string{"notes.txt", "other.wav", "../ding.wav"} {
		if err := c.PlayFile(ctx(t), f); code(err) != addonapi.CodeBadRequest {
			t.Errorf("%s played: %v", f, err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.dir, "ding.wav"), wavBytes(8000, 8000*6), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.PlayFile(ctx(t), "ding.wav"); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a six-second WAV: %v", err)
	}
	r.d.mu.Lock()
	r.d.quiet = true
	r.d.mu.Unlock()
	advance()
	if err := c.PlaySound(ctx(t), "tick"); code(err) != addonapi.CodeBusy {
		t.Errorf("a sound in quiet hours: %v", err)
	}
	if got := strings.Join(r.d.sounds, ","); got != "chime,wav:8000" {
		t.Errorf("played %s", got)
	}
}

func wavBytes(rate, samples int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+2*samples))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(2*samples))
	b.Write(make([]byte, 2*samples))
	return b.Bytes()
}

func TestTimers(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	c := r.dial()
	for range addonapi.MaxTimers {
		if _, err := c.StartTimer(ctx(t), "eggs", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// Past the burst, a start or a cancel waits for its token.
	if _, err := c.StartTimer(ctx(t), "one more", time.Minute); code(err) != addonapi.CodeRateLimited {
		t.Errorf("a ninth start at once: %v", err)
	}
	r.advance(time.Hour)
	if _, err := c.StartTimer(ctx(t), "one more", time.Minute); code(err) != addonapi.CodeBusy {
		t.Errorf("a ninth timer: %v", err)
	}
	list, err := c.Timers(ctx(t))
	if err != nil || len(list) != addonapi.MaxTimers {
		t.Fatalf("list: %d %v", len(list), err)
	}
	if err := c.CancelTimer(ctx(t), list[0].ID); err != nil {
		t.Error(err)
	}
	r.advance(addonapi.TimerOpEvery)
	if err := c.CancelTimer(ctx(t), "local:somebody-else"); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a timer not its own: %v", err)
	}
	r.advance(time.Hour)
	for _, d := range []time.Duration{0, -time.Second, addonapi.MaxTimer + time.Second} {
		if _, err := c.StartTimer(ctx(t), "x", d); code(err) != addonapi.CodeBadRequest {
			t.Errorf("a timer of %v: %v", d, err)
		}
	}
	if _, err := c.StartTimer(ctx(t), "a\nb", time.Minute); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a name with a newline: %v", err)
	}
}

func TestKeepAwake(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	a, err := c.HoldAwake(ctx(t), time.Minute)
	if err != nil || a.Handle == "" {
		t.Fatalf("hold: %+v %v", a, err)
	}
	if held, _ := r.d.awakeNow(); !held || !r.s.Awake() {
		t.Error("not held")
	}
	if _, err := c.HoldAwake(ctx(t), addonapi.MaxAwake+time.Second); code(err) != addonapi.CodeBadRequest {
		t.Errorf("past 30 minutes: %v", err)
	}
	if _, err := c.RenewAwake(ctx(t), a.Handle, 10*time.Minute); err != nil {
		t.Errorf("renew: %v", err)
	}
	if _, err := c.RenewAwake(ctx(t), "nope", time.Minute); code(err) != addonapi.CodeBadRequest {
		t.Errorf("renewing a handle not its own: %v", err)
	}
	for range addonapi.MaxAwakeHolds - 1 {
		if _, err := c.HoldAwake(ctx(t), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.HoldAwake(ctx(t), time.Minute); code(err) != addonapi.CodeBusy {
		t.Errorf("a fifth hold: %v", err)
	}
	if err := c.ReleaseAwake(ctx(t), a.Handle); err != nil {
		t.Error(err)
	}
	if held, _ := r.d.awakeNow(); !held {
		t.Error("released with holds left")
	}
	// A connection that drops leaves nothing held.
	c.Close()
	eventually(t, "the holds to end with the connection", func() bool { held, _ := r.d.awakeNow(); return !held && !r.s.Awake() })
}

func TestKeepAwakeExpires(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	if _, err := c.HoldAwake(ctx(t), 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the hold to run out", func() bool { held, _ := r.d.awakeNow(); return !held })
	// And Close ends them too, as the add-on stopping does.
	if _, err := c.HoldAwake(ctx(t), time.Minute); err != nil {
		t.Fatal(err)
	}
	r.s.Close("meals")
	eventually(t, "the hold to end with the add-on", func() bool { held, _ := r.d.awakeNow(); return !held })
}

func TestEntities(t *testing.T) {
	r := newRig(t, everything)
	r.freeze()
	c := r.dial()
	if err := c.SetEntity(ctx(t), "step", "Chop the onions"); err != nil {
		t.Fatal(err)
	}
	if r.d.entities["step"] != "Chop the onions" {
		t.Errorf("state %v", r.d.entities["step"])
	}
	for id, state := range map[string]any{"step": 3, "go": true, "nope": "x"} {
		if err := c.SetEntity(ctx(t), id, state); code(err) != addonapi.CodeBadRequest {
			t.Errorf("%s set to %v: %v", id, state, err)
		}
	}
	limited := 0
	for i := range addonapi.EntitySetsPerSecond + 2 {
		if code(c.SetEntity(ctx(t), "step", fmt.Sprint("step ", i))) == addonapi.CodeRateLimited {
			limited++
		}
	}
	if limited != 3 { // the first set took one
		t.Errorf("%d limited", limited)
	}
	// A state it has already costs nothing, and is not sent on.
	r.advance(time.Second)
	if err := c.SetEntity(ctx(t), "step", "step 1"); err != nil {
		t.Fatal(err)
	}
	r.d.mu.Lock()
	r.d.entities["step"] = "untouched"
	r.d.mu.Unlock()
	for range 10 {
		if err := c.SetEntity(ctx(t), "step", "step 1"); err != nil {
			t.Fatalf("the same state again: %v", err)
		}
	}
	r.d.mu.Lock()
	got := r.d.entities["step"]
	r.d.mu.Unlock()
	if got != "untouched" {
		t.Errorf("the same state was sent on: %v", got)
	}
}

func TestMedia(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	if err := c.PlayMedia(ctx(t), addonapi.MediaVideo, "https://mealie.local/step.mp4", "Step 3"); err != nil {
		t.Fatal(err)
	}
	if err := c.PauseMedia(ctx(t)); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]addonapi.MediaPlay{
		"file url": {URL: "file:///etc/passwd", Kind: "video"},
		"kind":     {URL: "http://x/a", Kind: "image"},
		"no host":  {URL: "http:///a", Kind: "audio"},
		"long":     {URL: "http://x/" + strings.Repeat("a", addonapi.MaxURL), Kind: "audio"},
	} {
		if err := c.Call(ctx(t), addonapi.OpMediaPlay, m, nil); code(err) != addonapi.CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := strings.Join(r.d.media, "|"); got != "media.play video https://mealie.local/step.mp4|media.pause  " {
		t.Errorf("media %q", got)
	}
}

func TestCommand(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	go func() {
		for ev := range c.Events() {
			if ev.Event != addonapi.EventCommand {
				continue
			}
			var ce addonapi.CommandEvent
			_ = ev.Decode(&ce)
			if ce.Call == 0 {
				continue
			}
			switch ce.Command {
			case "next":
				_ = c.ReplyCommand(context.Background(), ce.Call, true, "Step two: stir.")
			case "step":
				_ = c.ReplyCommand(context.Background(), ce.Call, false, "there is no step "+ce.Args["to"].(string))
			}
		}
	}()
	got, err := r.s.Command(ctx(t), "meals", "next", nil, "action", true)
	if err != nil || got != "Step two: stir." {
		t.Errorf("next: %q %v", got, err)
	}
	if _, err := r.s.Command(ctx(t), "meals", "step", map[string]any{"to": "9"}, "action", true); err == nil || !strings.Contains(err.Error(), "no step 9") {
		t.Errorf("a command that failed: %v", err)
	}
	if _, err := r.s.Command(ctx(t), "meals", "step", map[string]any{"to": "12345"}, "action", true); code(err) != addonapi.CodeBadRequest {
		t.Errorf("an argument past its length: %v", err)
	}
	if _, err := r.s.Command(ctx(t), "meals", "dance", nil, "action", true); code(err) != addonapi.CodeBadRequest {
		t.Errorf("an undeclared command: %v", err)
	}
	if _, err := r.s.Command(ctx(t), "other", "next", nil, "action", true); code(err) != addonapi.CodeUnavailable {
		t.Errorf("an add-on not running: %v", err)
	}
	// An answer past the limit is refused to the add-on, and the command times out.
	if err := c.ReplyCommand(ctx(t), 99, true, "x"); code(err) != addonapi.CodeBadRequest {
		t.Errorf("a reply to no call: %v", err)
	}
}

func TestCommandTimesOut(t *testing.T) {
	r := newRig(t, everything)
	r.dial() // says hello, then never answers
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.s.Command(short, "meals", "next", nil, "action", true); code(err) != addonapi.CodeUnavailable {
		t.Errorf("an add-on that does not answer: %v", err)
	}
}

func TestSettingsTest(t *testing.T) {
	r := newRig(t, everything)
	c := r.dial()
	go func() {
		for ev := range c.Events() {
			if ev.Event == addonapi.EventSettingsTest {
				var st addonapi.SettingsTest
				_ = ev.Decode(&st)
				if st.Values["token"] == "good" {
					_ = c.ReplyTest(context.Background(), st.Call, true, "")
				} else {
					_ = c.ReplyTest(context.Background(), st.Call, false, "Mealie says the token is wrong")
				}
			}
		}
	}()
	// Dial returns once the add-on has said hello; the server files the connection just after.
	eventually(t, "the connection to be filed", func() bool { return r.s.Connected("meals") == 1 })
	if ok, why, err := r.s.Test(ctx(t), "meals", map[string]any{"token": "good"}); !ok || err != nil {
		t.Errorf("a good token: %v %q %v", ok, why, err)
	}
	if ok, why, err := r.s.Test(ctx(t), "meals", map[string]any{"token": "bad"}); ok || why != "Mealie says the token is wrong" || err != nil {
		t.Errorf("a bad token: %v %q %v", ok, why, err)
	}
	if err := c.ReplyTest(ctx(t), 1, false, strings.Repeat("x", addonapi.MaxTestAnswer+1)); code(err) != addonapi.CodeTooLarge {
		t.Errorf("a test answer past 200 characters: %v", err)
	}
}

func TestEmit(t *testing.T) {
	r := newRig(t, everything)
	a, b := r.dial(), r.dial()
	r.s.Emit("meals", addonapi.EventTimer, addonapi.TimerEvent{ID: "local:1", Name: "eggs", State: addonapi.TimerRinging})
	for _, c := range []*addonapi.Client{a, b} {
		select {
		case ev := <-c.Events():
			var te addonapi.TimerEvent
			if ev.Event != addonapi.EventTimer || ev.Decode(&te) != nil || te.State != addonapi.TimerRinging {
				t.Errorf("event %+v", ev)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
	}
}

// A slow reader holds nothing up: it is disconnected once its queue is full.
func TestSlowReader(t *testing.T) {
	r := newRig(t, everything)
	w := r.raw()
	w.send(`{"id":1,"op":"hello","args":{"api":1}}`)
	w.read()
	big := map[string]string{"pad": strings.Repeat("x", 60<<10)}
	done := make(chan struct{})
	go func() {
		for range outQueue * 4 {
			r.s.Emit("meals", "settings", big)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Emit was held up by an add-on that does not read")
	}
	eventually(t, "the slow reader to be dropped", func() bool { return r.s.Connected("meals") == 0 })
}
