package cast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

type recSink struct {
	mu     sync.Mutex
	hello  Hello
	refuse error
	frames []color.Color
	audio  int
	ended  chan struct{}
	begins int // how many times a phone got as far as being asked about
}

func (s *recSink) Begin(h Hello) (Welcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hello = h
	s.begins++
	if s.refuse != nil {
		return Welcome{}, s.refuse
	}
	return Welcome{OK: true, W: 64, H: 32, Rate: 48000, Channel: 2, LatencyMs: 40}, nil
}

func (s *recSink) Frame(img image.Image) {
	s.mu.Lock()
	s.frames = append(s.frames, img.At(1, 1))
	s.mu.Unlock()
}

func (s *recSink) Audio([]byte, time.Time) {
	s.mu.Lock()
	s.audio++
	s.mu.Unlock()
}

func (s *recSink) End() { close(s.ended) }

func (s *recSink) count() (frames, audio int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames), s.audio
}

func start(t *testing.T, sink *recSink) string {
	t.Helper()
	return startWith(t, sink, "pairing-key")
}

// startWith is start with the device's key set to key.
func startWith(t *testing.T, sink *recSink, key string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &Receiver{Key: func() string { return key }, Sink: sink}
	go r.Serve(ctx, ln)
	return ln.Addr().String()
}

func solid(w, h int, shade uint8) []byte {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	return b.Bytes()
}

func connect(t *testing.T, addr, key string, h Hello) (*secure, Welcome) {
	t.Helper()
	return connectFrom(t, "", addr, key, h)
}

// from dials from a loopback address of its own (127.0.0.2 and on), so the receiver sees another
// phone: it counts connections by where they come from. Empty is any address.
func dialFrom(from, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: time.Second}
	if from != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(from)}
	}
	return d.Dial("tcp", addr)
}

// connectFrom is connect from the address from.
func connectFrom(t *testing.T, from, addr, key string, h Hello) (*secure, Welcome) {
	t.Helper()
	raw, err := dialFrom(from, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	c, err := dial(raw, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(h)
	if err := Write(c, KindHello, b); err != nil {
		t.Fatal(err)
	}
	kind, payload, err := Read(c)
	if err != nil {
		t.Fatal(err)
	}
	if kind != KindWelcome {
		t.Fatalf("kind %d, want a welcome", kind)
	}
	var w Welcome
	json.Unmarshal(payload, &w)
	return c, w
}

func TestFramesArriveInOrderAndAudioFollows(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	c, w := connect(t, addr, "pairing-key", Hello{Name: "Pixel\x07 9", Video: true, Audio: true})
	if !w.OK || w.W != 64 {
		t.Fatalf("welcome %+v", w)
	}
	t0 := time.Now()
	us := func() int64 { return time.Since(t0).Microseconds() }
	Write(c, KindClock, Stamp(us()))
	for i := 0; i < 5; i++ {
		Write(c, KindVideo, Stamp(us()), solid(64, 32, uint8(40*i+20)))
		Write(c, KindAudio, Stamp(us()), make([]byte, 4*480))
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	Write(c, KindBye)
	<-sink.ended

	frames, audio := sink.count()
	if frames < 4 || audio != 5 {
		t.Fatalf("%d frames and %d audio chunks, want at least 4 and 5", frames, audio)
	}
	last := -1
	for _, f := range sink.frames {
		g, _, _, _ := f.RGBA()
		if int(g) < last {
			t.Fatal("frames out of order")
		}
		last = int(g)
	}
	if sink.hello.Name != "Pixel 9" {
		t.Fatalf("name %q, control characters should be gone", sink.hello.Name)
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := dial(raw, "another-key"); err == nil {
		// The initiator only learns of a bad key when the reply fails to check, or the socket closes.
		if _, _, err := Read(&secure{Conn: raw}); err == nil {
			t.Fatal("a wrong key was let through")
		}
	}
}

func TestSinkRefusalReachesThePhone(t *testing.T) {
	sink := &recSink{ended: make(chan struct{}), refuse: errors.New("in a call")}
	addr := start(t, sink)
	_, w := connect(t, addr, "pairing-key", Hello{Name: "x", Video: true})
	if w.OK || w.Reason != "in a call" {
		t.Fatalf("welcome %+v", w)
	}
}

func TestMediaBeforeTheClockAndOversizeFramesAreDropped(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	c, _ := connect(t, addr, "pairing-key", Hello{Name: "x", Video: true, Audio: true})
	Write(c, KindVideo, Stamp(0), solid(64, 32, 100)) // no clock yet
	Write(c, KindAudio, Stamp(0), make([]byte, 400))
	Write(c, KindClock, Stamp(0))
	Write(c, KindVideo, Stamp(0), solid(640, 320, 100)) // larger than the screen
	Write(c, KindAudio, Stamp(0), make([]byte, 401))    // not whole frames
	time.Sleep(200 * time.Millisecond)
	Write(c, KindBye)
	<-sink.ended
	if f, a := sink.count(); f != 0 || a != 0 {
		t.Fatalf("%d frames and %d audio chunks shown, want none", f, a)
	}
}

// A second phone with the key, while one casts, is told the Show is busy rather than closed on as if
// its key were wrong, and nobody is asked about it.
func TestSecondPhoneIsToldTheShowIsBusy(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	c, _ := connect(t, addr, "pairing-key", Hello{Name: "first", Video: true})
	_, w := connect(t, addr, "pairing-key", Hello{Name: "second", Video: true})
	if w.OK || w.Reason != reasonBusy {
		t.Fatalf("welcome %+v, want busy", w)
	}
	sink.mu.Lock()
	begins := sink.begins
	sink.mu.Unlock()
	if begins != 1 {
		t.Fatalf("%d phones asked about, want only the first", begins)
	}
	Write(c, KindBye)
}

// closedAtOnce is whether the receiver closes c without a word, well inside handshakeFor.
func closedAtOnce(c net.Conn) bool {
	c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

// The bug this guards against: a stranger without the key reconnecting every few milliseconds held the
// one cast's slot, so the owner's phone was closed on and told its key did not match.
func TestAReconnectFloodCannotKeepAPhoneWithTheKeyOut(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var held []net.Conn
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Silent: it never sends a byte. Some are held open, some closed at once.
			if c, err := dialFrom("127.0.0.2", addr); err == nil {
				if len(held)%2 == 0 {
					held = append(held, c)
				} else {
					c.Close()
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	time.Sleep(100 * time.Millisecond)

	c, w := connectFrom(t, "127.0.0.3", addr, "pairing-key", Hello{Name: "owner", Video: true})
	if !w.OK {
		t.Fatalf("the owner's phone was refused during a flood: %+v", w)
	}
	Write(c, KindBye)
}

func TestOneAddressHasOnePlaceInTheHandshake(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	first, err := dialFrom("127.0.0.2", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	time.Sleep(50 * time.Millisecond) // the receiver has taken it
	second, err := dialFrom("127.0.0.2", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !closedAtOnce(second) {
		t.Fatal("a second connection from one address was let into the handshake")
	}
	if closedAtOnce(first) {
		t.Fatal("the first connection from the address was closed at once")
	}
	// Another address still has a place, up to pendingMax in all.
	var others []net.Conn
	defer func() {
		for _, c := range others {
			c.Close()
		}
	}()
	for i := 3; i < 3+pendingMax-1; i++ {
		c, err := dialFrom("127.0.0."+strconv.Itoa(i), addr)
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, c)
	}
	time.Sleep(50 * time.Millisecond)
	for _, c := range others {
		if closedAtOnce(c) {
			t.Fatal("a connection within pendingMax was closed at once")
		}
	}
	over, err := dialFrom("127.0.0.20", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer over.Close()
	if !closedAtOnce(over) {
		t.Fatalf("a connection past the %d in the handshake was let in", pendingMax)
	}
}

func TestADeclinedAddressIsTurnedAwayWithoutAsking(t *testing.T) {
	was := declineFor
	declineFor = 300 * time.Millisecond
	t.Cleanup(func() { declineFor = was })
	sink := &recSink{ended: make(chan struct{}), refuse: Declined("declined")}
	addr := start(t, sink)
	begins := func() int {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return sink.begins
	}

	if _, w := connectFrom(t, "127.0.0.2", addr, "pairing-key", Hello{Name: "guest"}); w.OK || w.Reason != "declined" {
		t.Fatalf("welcome %+v, want declined", w)
	}
	if _, w := connectFrom(t, "127.0.0.2", addr, "pairing-key", Hello{Name: "guest"}); w.OK || w.Reason != reasonDeclined {
		t.Fatalf("welcome %+v, want the address turned away", w)
	}
	if begins() != 1 {
		t.Fatalf("asked %d times, want once: a declined phone must not put the question up again", begins())
	}
	// Another address is asked as usual, and so is this one once the time is up.
	connectFrom(t, "127.0.0.3", addr, "pairing-key", Hello{Name: "other"})
	if begins() != 2 {
		t.Fatalf("asked %d times, want another phone asked", begins())
	}
	time.Sleep(declineFor)
	connectFrom(t, "127.0.0.2", addr, "pairing-key", Hello{Name: "guest"})
	if begins() != 3 {
		t.Fatalf("asked %d times, want the address asked again after declineFor", begins())
	}
}

// A refusal that is not the person's (the screen not ready, in a call) is no reason to turn the phone
// away when it tries again.
func TestOtherRefusalsDoNotTurnTheAddressAway(t *testing.T) {
	sink := &recSink{ended: make(chan struct{}), refuse: errors.New("in a call")}
	addr := start(t, sink)
	connectFrom(t, "127.0.0.2", addr, "pairing-key", Hello{Name: "x"})
	if _, w := connectFrom(t, "127.0.0.2", addr, "pairing-key", Hello{Name: "x"}); w.Reason != "in a call" {
		t.Fatalf("welcome %+v, want asked again", w)
	}
}

// A key of 8 characters, which cast_key took before it wanted 16, still pairs.
func TestAShortKeySavedEarlierStillWorks(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := startWith(t, sink, "8charkey")
	c, w := connect(t, addr, "8charkey", Hello{Name: "x", Video: true})
	if !w.OK {
		t.Fatalf("welcome %+v", w)
	}
	Write(c, KindBye)
}

func TestStopTellsThePhoneAndEndsTheCast(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &Receiver{Key: func() string { return "pairing-key" }, Sink: sink}
	go r.Serve(ctx, ln)

	c, _ := connect(t, ln.Addr().String(), "pairing-key", Hello{Name: "x", Video: true})
	r.Stop("stopped on the device")
	kind, payload, err := Read(c)
	if err != nil || kind != KindStop || string(payload) != "stopped on the device" {
		t.Fatalf("got kind %d %q err %v, want a stop with its reason", kind, payload, err)
	}
	select {
	case <-sink.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the sink was not told the cast ended")
	}
}

func TestHalfScaleFramesAreDoubledAndBoundedByHalfTheScreen(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	c, _ := connect(t, addr, "pairing-key", Hello{Name: "x", Video: true, Scale: 2})
	t0 := time.Now()
	Write(c, KindClock, Stamp(0))
	Write(c, KindVideo, Stamp(time.Since(t0).Microseconds()), solid(32, 16, 100)) // half of 64x32: fine
	Write(c, KindVideo, Stamp(time.Since(t0).Microseconds()), solid(64, 32, 100)) // full size: too big now
	time.Sleep(300 * time.Millisecond)
	Write(c, KindBye)
	<-sink.ended
	if f, _ := sink.count(); f != 1 {
		t.Fatalf("%d frames shown, want the half-size one only", f)
	}
}

func TestStatsComeOnceASecondAndCountFrames(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	c, w := connect(t, addr, "pairing-key", Hello{Name: "Pixel", Video: true})
	if !w.OK {
		t.Fatalf("welcome %+v", w)
	}
	t0 := time.Now()
	us := func() int64 { return time.Since(t0).Microseconds() }
	Write(c, KindClock, Stamp(us()))
	go func() {
		for i := 0; i < 50; i++ {
			Write(c, KindVideo, Stamp(us()), solid(64, 32, 90))
			time.Sleep(40 * time.Millisecond)
		}
	}()

	c.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		kind, payload, err := Read(c)
		if err != nil {
			t.Fatalf("no stats within 4 s: %v", err)
		}
		if kind != KindStats {
			continue
		}
		var st Stats
		if err := json.Unmarshal(payload, &st); err != nil {
			t.Fatal(err)
		}
		if st.Shown == 0 {
			t.Fatalf("stats %+v after a second of frames, want some shown", st)
		}
		return
	}
}

func TestTitleIsCleanedAndPassedOn(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	addr := start(t, sink)
	connect(t, addr, "pairing-key", Hello{Name: "Pixel", Video: true, Title: "  Big Buck\x07 Bunny  "})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.hello.Title != "Big Buck Bunny" {
		t.Fatalf("title %q", sink.hello.Title)
	}
}

// A paired phone cannot fill the device's memory with frames: a big one is refused, a stamp from far
// ahead is refused, and what waits in the queue is held to a byte budget, the oldest going first.
func TestVideoMemoryIsBounded(t *testing.T) {
	p := &play{frames: make(chan frame, queued), latency: 0}
	p.clockSync.start = time.Now()
	p.clockSync.observe(p.clockSync.now()) // the clock message that lets frames be placed
	stamp := func() int64 { return p.clockSync.now() }
	send := func(us int64, size int) { p.video(append(Stamp(us), make([]byte, size)...)) }

	send(stamp(), videoMax+1)
	if p.bad.Load() != 1 || len(p.frames) != 0 {
		t.Fatalf("an oversize frame was taken: bad=%d queued=%d", p.bad.Load(), len(p.frames))
	}
	send(stamp()+int64(farMax/time.Microsecond)+500_000, 100)
	if p.bad.Load() != 2 || len(p.frames) != 0 {
		t.Fatalf("a stamp far ahead was taken: bad=%d queued=%d", p.bad.Load(), len(p.frames))
	}
	for i := 0; i < 40; i++ {
		send(stamp(), videoMax)
	}
	if got := p.queuedB.Load(); got > queueBytes {
		t.Fatalf("%d bytes queued, budget %d", got, queueBytes)
	}
	if p.overflow.Load() == 0 {
		t.Fatal("nothing was pushed out of a full queue")
	}
}

// Frames still queued when a cast ends are not decoded and painted after it.
func TestQueuedFramesAreNotShownAfterTheCastEnds(t *testing.T) {
	sink := &recSink{ended: make(chan struct{})}
	p := &play{sink: sink, frames: make(chan frame, queued), w: Welcome{W: 64, H: 32}, h: Hello{Scale: 1}}
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
	for i := 0; i < 10; i++ {
		p.frames <- frame{due: time.Now(), jpg: jpg.Bytes()}
	}
	p.over.Store(true)
	close(p.frames)
	p.show()
	if n, _ := sink.count(); n != 0 {
		t.Fatalf("%d frames were shown after the cast ended", n)
	}
}

// watchGone says the connection has closed without taking what the phone sent from the session.
func TestWatchGoneSeesAClosedConnectionAndLeavesDataAlone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() { c, _ := ln.Accept(); accepted <- c }()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer server.Close()

	gone, stop := watchGone(server)
	defer stop()
	_, _ = client.Write([]byte("x"))
	select {
	case <-gone:
		t.Fatal("a phone that sent data was taken for gone")
	case <-time.After(500 * time.Millisecond):
	}
	buf := make([]byte, 1)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	if n, _ := server.Read(buf); n != 1 || buf[0] != 'x' {
		t.Fatal("watching took the phone's data")
	}
	client.Close()
	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("a closed connection was not noticed")
	}
}
