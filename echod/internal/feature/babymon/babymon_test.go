package babymon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// fake stands in for the rest of the device: a switch, a mute, a key and a microphone the test speaks
// into.
type fake struct {
	on, muted atomic.Bool
	key       atomic.Value // string
	opened    atomic.Int32 // listeners the microphones were asked for
	closed    atomic.Int32
	mu        sync.Mutex
	mics      []chan []int16
}

func newFake() *fake {
	f := &fake{}
	f.key.Store("secret-key")
	return f
}

func (f *fake) device() device {
	return device{
		listen: func(string) (<-chan []int16, func()) {
			ch := make(chan []int16, 64)
			f.mu.Lock()
			f.mics = append(f.mics, ch)
			f.mu.Unlock()
			f.opened.Add(1)
			return ch, func() { f.closed.Add(1) }
		},
		switchedOn: f.on.Load,
		muted:      f.muted.Load,
		key:        func() string { return f.key.Load().(string) },
	}
}

// speak sends n frames of one sample value to every listener.
func (f *fake) speak(n int, v int16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.mics {
		for i := 0; i < n; i++ {
			fr := make([]int16, mic.FrameSamples)
			for j := range fr {
				fr[j] = v
			}
			select {
			case ch <- fr:
			default:
			}
		}
	}
}

// listen starts a listener and returns what it was sent and how it ended.
func listen(m *Feature) (got chan []byte, end chan error, stop func()) {
	got, end, done := make(chan []byte, 64), make(chan error, 1), make(chan struct{})
	go func() {
		end <- m.Serve(done, func(b []byte) error { got <- b; return nil })
	}()
	var once sync.Once
	return got, end, func() { once.Do(func() { close(done) }) }
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestOffRefusesAndNeverOpensTheMicrophones(t *testing.T) {
	d := newFake()
	m := build(d.device())
	if err := m.Serve(make(chan struct{}), func([]byte) error { return nil }); err != ErrOff {
		t.Fatalf("Serve with the switch off = %v, want ErrOff", err)
	}
	if d.opened.Load() != 0 || m.Count() != 0 {
		t.Errorf("the microphones were opened (%d) or someone counted (%d) with the switch off", d.opened.Load(), m.Count())
	}
}

func TestPacketFraming(t *testing.T) {
	got := packet([]int16{1, -2, 0x1234, -32768})
	want := []byte{0x01, 0x00, 0xFE, 0xFF, 0x34, 0x12, 0x00, 0x80}
	if string(got) != string(want) {
		t.Errorf("packet = % x, want % x", got, want)
	}
}

func TestSendsWholePacketsOfTheRoom(t *testing.T) {
	d := newFake()
	d.on.Store(true)
	m := build(d.device())
	got, end, stop := listen(m)
	waitFor(t, "the listener", func() bool { return d.opened.Load() == 1 })

	d.speak(batch-1, 7)
	select {
	case b := <-got:
		t.Fatalf("a packet of %d bytes came before a batch was complete", len(b))
	case <-time.After(50 * time.Millisecond):
	}
	d.speak(1, 7)
	select {
	case b := <-got:
		if len(b) != 2*batch*mic.FrameSamples || b[0] != 7 || b[1] != 0 {
			t.Errorf("packet is %d bytes starting % x", len(b), b[:2])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no packet")
	}
	stop()
	if err := <-end; err != nil {
		t.Errorf("a listener that left ended with %v", err)
	}
	if d.closed.Load() != 1 {
		t.Errorf("the microphone listener was not let go")
	}
}

func TestIndicatorFollowsListeners(t *testing.T) {
	d := newFake()
	d.on.Store(true)
	m := build(d.device())
	var changes atomic.Int32
	m.Changed.Listen(func(struct{}) { changes.Add(1) })
	if m.Count() != 0 || m.sensor.Get() {
		t.Fatal("listening before anyone does")
	}
	_, _, stop1 := listen(m)
	_, end2, stop2 := listen(m)
	waitFor(t, "two listeners", func() bool { return m.Count() == 2 })
	if !m.sensor.Get() {
		t.Error("the sensor is off while two listen")
	}
	stop1()
	waitFor(t, "one listener", func() bool { return m.Count() == 1 })
	if !m.sensor.Get() {
		t.Error("the sensor went off with one still listening")
	}
	stop2()
	<-end2
	waitFor(t, "none", func() bool { return m.Count() == 0 })
	if m.sensor.Get() {
		t.Error("the sensor stays on after the last listener left")
	}
	if changes.Load() < 4 {
		t.Errorf("the screen was told %d times, want at least 4", changes.Load())
	}
}

func TestAtMostMaxListeners(t *testing.T) {
	d := newFake()
	d.on.Store(true)
	m := build(d.device())
	for i := 0; i < Max; i++ {
		listen(m)
	}
	waitFor(t, "all of them", func() bool { return m.Count() == Max })
	if err := m.Serve(make(chan struct{}), func([]byte) error { return nil }); err != ErrFull {
		t.Errorf("one more = %v, want ErrFull", err)
	}
}

func TestMuteStopsTheAudioAtOnce(t *testing.T) {
	d := newFake()
	d.on.Store(true)
	m := build(d.device())
	got, end, _ := listen(m)
	waitFor(t, "the listener", func() bool { return d.opened.Load() == 1 })
	d.speak(batch, 3)
	<-got

	d.muted.Store(true)
	d.speak(batch*3, 3) // a full batch is waiting when the mute is pressed: none of it may go out
	if err := <-end; err != ErrMuted {
		t.Fatalf("listener ended with %v, want ErrMuted", err)
	}
	select {
	case b := <-got:
		t.Errorf("a packet of %d bytes was sent after the mute", len(b))
	default:
	}
	if err := m.Serve(make(chan struct{}), func([]byte) error { return nil }); err != ErrMuted {
		t.Errorf("a new listener while muted = %v, want ErrMuted", err)
	}
	if m.Count() != 0 {
		t.Errorf("%d still counted after the mute", m.Count())
	}
}

func TestSwitchOffAndANewLinkEndListeners(t *testing.T) {
	d := newFake()
	d.on.Store(true)
	m := build(d.device())
	_, end, _ := listen(m)
	waitFor(t, "the listener", func() bool { return m.Count() == 1 })
	d.key.Store("another")
	if err := <-end; err != ErrNewKey {
		t.Errorf("after a new link = %v, want ErrNewKey", err)
	}

	d.key.Store("secret-key")
	_, end, _ = listen(m)
	waitFor(t, "the listener", func() bool { return m.Count() == 1 })
	d.on.Store(false)
	if err := <-end; err != ErrOff {
		t.Errorf("after the switch went off = %v, want ErrOff", err)
	}
}

func TestPagesAnswerNotFoundUnlessOnWithTheKey(t *testing.T) {
	d := newFake()
	m := build(d.device())
	for _, tc := range []struct {
		name, url string
		on        bool
		want      int
	}{
		{"switch off, right key", PagePath + "?k=secret-key", false, 404},
		{"switch off, audio", AudioPath + "?k=secret-key", false, 404},
		{"no key", PagePath, true, 404},
		{"wrong key", PagePath + "?k=nope", true, 404},
		{"wrong key, audio", AudioPath + "?k=nope", true, 404},
		{"right key", PagePath + "?k=secret-key", true, 200},
	} {
		d.on.Store(tc.on)
		h := m.page
		if strings.HasPrefix(tc.url, AudioPath) {
			h = m.audio
		}
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	// A device that has never had the switch on has no key, and an empty one must not match it.
	d.key.Store("")
	d.on.Store(true)
	rec := httptest.NewRecorder()
	m.page(rec, httptest.NewRequest(http.MethodGet, PagePath+"?k=", nil))
	if rec.Code != 404 {
		t.Errorf("an empty key opened the page: %d", rec.Code)
	}
}

func TestPageHasNoSecretAndKeepsTheRate(t *testing.T) {
	if strings.Contains(pageHTML, "secret") || !strings.Contains(pageHTML, "RATE=16000") || mic.Rate != 16000 {
		t.Error("the page holds a key, or plays at another rate than the microphones")
	}
}

func TestAudioOverAWebSocketAndMuteClosesIt(t *testing.T) {
	d := newFake()
	d.on.Store(true)
	m := build(d.device())
	srv := httptest.NewServer(http.HandlerFunc(m.audio))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + AudioPath
	if _, resp, err := websocket.DefaultDialer.Dial(url+"?k=nope", nil); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("a wrong key got in: %v", err)
	}
	c, _, err := websocket.DefaultDialer.Dial(url+"?k=secret-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	waitFor(t, "the listener", func() bool { return d.opened.Load() == 1 && m.Count() == 1 })
	d.speak(batch, 5)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	typ, b, err := c.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage || len(b) != 2*batch*mic.FrameSamples {
		t.Fatalf("first message: type %d, %d bytes, %v", typ, len(b), err)
	}
	d.muted.Store(true)
	for {
		if _, _, err = c.ReadMessage(); err != nil {
			break
		}
	}
	if ce, ok := err.(*websocket.CloseError); !ok || ce.Text != ErrMuted.Error() {
		t.Errorf("ended with %v, want a close that says muted", err)
	}
	waitFor(t, "none counted", func() bool { return m.Count() == 0 })
}
