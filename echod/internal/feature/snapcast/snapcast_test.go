package snapcast

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	ssync "github.com/Sendspin/sendspin-go/pkg/sync"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestMessageRoundTrip(t *testing.T) {
	sent := int64(1_700_000_123_456_789)
	b := encode(msgTime, 7, sent, timePayload())
	m, p, err := readMessage(bytes.NewReader(b), func() int64 { return 42 })
	if err != nil {
		t.Fatal(err)
	}
	if m.typ != msgTime || m.id != 7 || m.sent != sent || m.received != 42 || len(p) != 8 {
		t.Errorf("read back %+v with %d bytes", m, len(p))
	}
}

func TestSplitNegative(t *testing.T) {
	for _, us := range []int64{-1, -1_500_000, 0, 999_999, 1_000_000} {
		sec, usec := split(us)
		if usec < 0 || usec >= 1e6 || micros(sec, usec) != us {
			t.Errorf("%d split to %d s %d us", us, sec, usec)
		}
	}
}

func TestRefusesHugeMessage(t *testing.T) {
	b := encode(msgWireChunk, 1, 0, nil)
	binary.LittleEndian.PutUint32(b[22:], maxMessage+1)
	if _, _, err := readMessage(bytes.NewReader(b), func() int64 { return 0 }); err == nil {
		t.Error("a message past maxMessage was read")
	}
}

func TestShortFieldsRefused(t *testing.T) {
	if _, _, err := parseCodecHeader([]byte{9, 0, 0, 0, 'f'}); err == nil {
		t.Error("a codec name longer than the message was read")
	}
	if _, _, err := parseWireChunk([]byte{1, 2, 3}); err == nil {
		t.Error("a chunk without a timestamp was read")
	}
}

// wave is a RIFF WAVE header as Snapserver sends for pcm.
func wave(rate, channels, bits int) []byte {
	h := make([]byte, 44)
	copy(h, "RIFF")
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint16(h[34:], uint16(bits))
	copy(h[36:], "data")
	return h
}

func TestFormats(t *testing.T) {
	// fLaC, a STREAMINFO block header, then STREAMINFO for 48 kHz, 2 channels, 16 bits.
	flac := []byte("fLaC")
	flac = append(flac, 0x80, 0, 0, 34)
	si := make([]byte, 34)
	rate := 48000
	si[10] = byte(rate >> 12)
	si[11] = byte(rate >> 4)
	si[12] = byte(rate<<4) | byte(2-1)<<1 | byte((16-1)>>4)
	si[13] = byte((16-1)&0xF) << 4
	flac = append(flac, si...)

	opus := make([]byte, 12)
	binary.LittleEndian.PutUint32(opus, 0x4F505553)
	binary.LittleEndian.PutUint32(opus[4:], 48000)
	binary.LittleEndian.PutUint16(opus[8:], 16)
	binary.LittleEndian.PutUint16(opus[10:], 2)

	want := format{rate: 48000, channels: 2, bits: 16}
	for codec, h := range map[string][]byte{"flac": flac, "pcm": wave(48000, 2, 16), "opus": opus} {
		f, err := formatOf(codec, h)
		want.codec = codec
		if err != nil || f != want {
			t.Errorf("%s: %+v, %v", codec, f, err)
		}
	}
	if _, err := formatOf("ogg", nil); err == nil {
		t.Error("ogg was taken")
	}
}

func TestLevel(t *testing.T) {
	if level(100, false) != 1 || level(0, false) != 0 || level(100, true) != 0 {
		t.Error("ends wrong")
	}
	if l := level(50, false); l < 0.12 || l > 0.13 {
		t.Errorf("half is %v, want 0.125", l)
	}
}

func TestWithPort(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20":      "192.168.1.20:1704",
		"192.168.1.20:1800": "192.168.1.20:1800",
		"snap.local":        "snap.local:1704",
		"fd00::1":           "[fd00::1]:1704",
		"[fd00::1]:1704":    "[fd00::1]:1704",
	} {
		if got := withPort(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// recorder stands in for the speaker's output and its arbiter.
type recorder struct {
	mu     sync.Mutex
	opened int
	closed int
	took   int
	gave   int
	level  float32
	writes []write
	clock  *ssync.ClockSync
}

type write struct {
	at int64
	n  int
}

func (r *recorder) Use(c *ssync.ClockSync) { r.mu.Lock(); r.clock = c; r.mu.Unlock() }
func (r *recorder) Open(rate, ch, bits int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opened++
	return nil
}
func (r *recorder) Close() { r.mu.Lock(); r.closed++; r.mu.Unlock() }
func (r *recorder) Write(at int64, s []int16) {
	r.mu.Lock()
	r.writes = append(r.writes, write{at, len(s)})
	r.mu.Unlock()
}
func (r *recorder) Flush()                     {}
func (r *recorder) SetLevel(l float32)         { r.mu.Lock(); r.level = l; r.mu.Unlock() }
func (r *recorder) Producer() speaker.Producer { return r }
func (r *recorder) Suspend()                   {}
func (r *recorder) Resume()                    {}
func (r *recorder) Duck(int)                   {}
func (r *recorder) Requeue()                   {}
func (r *recorder) Took(speaker.Producer)      { r.mu.Lock(); r.took++; r.mu.Unlock() }
func (r *recorder) Gave(speaker.Producer)      { r.mu.Lock(); r.gave++; r.mu.Unlock() }

func (r *recorder) snapshot() recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recorder{opened: r.opened, closed: r.closed, took: r.took, gave: r.gave, level: r.level,
		writes: append([]write(nil), r.writes...)}
}

// fakeServer is a Snapserver whose clock runs offset ahead of the client's.
type fakeServer struct {
	t      *testing.T
	ln     net.Listener
	offset int64
	hello  chan hello
	// deaf stops answering Time after the stream has started, as a server that vanished without closing
	// the connection.
	deaf bool
}

func (f *fakeServer) now() int64 { return time.Now().UnixMicro() + f.offset }

func (f *fakeServer) serve(conn net.Conn, chunks func(send func(typ uint16, p []byte))) {
	defer conn.Close()
	var wmu sync.Mutex
	send := func(typ uint16, refersTo uint16, p []byte) {
		b := encode(typ, 0, f.now(), p)
		binary.LittleEndian.PutUint16(b[4:], refersTo)
		wmu.Lock()
		conn.Write(b)
		wmu.Unlock()
	}

	started := false
	for {
		m, p, err := readMessage(conn, f.now)
		if err != nil {
			return
		}
		switch m.typ {
		case msgHello:
			j, _, _ := sized(p)
			var h hello
			json.Unmarshal(j, &h)
			f.hello <- h
			set, _ := json.Marshal(settings{BufferMs: 1000, Latency: 100, Volume: 50})
			send(msgServerSettings, 0, sizedPayload(set))
			ch := append(sizedPayload([]byte("pcm")), sizedPayload(wave(48000, 2, 16))...)
			send(msgCodecHeader, 0, ch)
		case msgTime:
			if started && f.deaf {
				continue
			}
			// latency is the server's receipt less the client's sent, as Snapserver answers.
			lat := make([]byte, 8)
			sec, usec := split(m.received - m.sent)
			binary.LittleEndian.PutUint32(lat, uint32(sec))
			binary.LittleEndian.PutUint32(lat[4:], uint32(usec))
			send(msgTime, m.id, lat)
			if !started {
				started = true
				go chunks(func(typ uint16, p []byte) { send(typ, 0, p) })
			}
		}
	}
}

func chunkAt(at int64, samples []int16) []byte {
	p := make([]byte, 8)
	sec, usec := split(at)
	binary.LittleEndian.PutUint32(p, uint32(sec))
	binary.LittleEndian.PutUint32(p[4:], uint32(usec))
	audio := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(audio[2*i:], uint16(s))
	}
	return append(p, sizedPayload(audio)...)
}

// fast is a session's timing shortened for a test.
func fast(s *session) *session {
	s.timing.idleAfter = 300 * time.Millisecond
	s.timing.readTick = 20 * time.Millisecond
	s.timing.syncFirst = 5 * time.Millisecond
	return s
}

func TestSessionPlaysThenGoesIdle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &fakeServer{t: t, ln: ln, offset: 1000 * 1e6, hello: make(chan hello, 1)}

	tones := make([]int16, 2*960) // 20 ms of stereo
	for i := range tones {
		tones[i] = 3000
	}
	var (
		stamps  []int64
		stampMu sync.Mutex
	)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		srv.serve(conn, func(send func(uint16, []byte)) {
			// Let the clock settle before the music, as a real stream starts some time after joining.
			time.Sleep(300 * time.Millisecond)
			for range 10 {
				at := srv.now()
				stampMu.Lock()
				stamps = append(stamps, at)
				stampMu.Unlock()
				send(msgWireChunk, chunkAt(at, tones))
				time.Sleep(20 * time.Millisecond)
			}
			// Then silence, as a stopped stream sends.
			for range 20 {
				send(msgWireChunk, chunkAt(srv.now(), make([]int16, 2*960)))
				time.Sleep(20 * time.Millisecond)
			}
		})
	}()

	rec := &recorder{}
	var states []string
	var smu sync.Mutex
	s := fast(newSession(ln.Addr().String(), "Kitchen", "02:00:00:00:00:01", rec, rec, func(st string) {
		smu.Lock()
		states = append(states, st)
		smu.Unlock()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	ran := make(chan struct{})
	defer func() { cancel(); <-ran }()
	go func() { s.run(ctx); close(ran) }()

	h := <-srv.hello
	if h.ID != "02:00:00:00:00:01" || h.HostName != "Kitchen" || h.SnapStreamProtocolVersion != 2 {
		t.Errorf("hello %+v", h)
	}

	time.Sleep(1500 * time.Millisecond)
	got := rec.snapshot()
	if got.opened != 1 || got.took != 1 {
		t.Fatalf("opened %d took %d, want the speaker taken once", got.opened, got.took)
	}
	if got.gave != 1 || got.closed != 1 {
		t.Errorf("gave %d closed %d, want the speaker back once the stream went silent", got.gave, got.closed)
	}
	if got.level < 0.12 || got.level > 0.13 {
		t.Errorf("level %v for volume 50", got.level)
	}
	if len(got.writes) < 10 {
		t.Fatalf("%d chunks written, want the 10 tones and the silence after", len(got.writes))
	}
	// The first tone lands at its timestamp plus the buffer, less the client's latency.
	stampMu.Lock()
	first := stamps[0]
	stampMu.Unlock()
	if want := first + (1000-100)*1000; got.writes[0].at != want {
		t.Errorf("first chunk at %d, want %d", got.writes[0].at, want)
	}
	if got.writes[0].n != len(tones) {
		t.Errorf("first chunk %d samples, want %d", got.writes[0].n, len(tones))
	}

	// The filter has the server's clock 1000 s ahead, to within the loopback's jitter.
	if off := s.clock.ServerMicrosNow() - time.Now().UnixMicro(); off < 1000*1e6-5000 || off > 1000*1e6+5000 {
		t.Errorf("server clock read as %d us ahead, want 1e9", off)
	}

	smu.Lock()
	defer smu.Unlock()
	if len(states) < 3 || states[0] != stateConnected || states[1] != statePlaying {
		t.Errorf("states %v", states)
	}
}

// rawChunk is a chunk of whatever bytes, decodable or not.
func rawChunk(at int64, audio []byte) []byte {
	p := make([]byte, 8)
	sec, usec := split(at)
	binary.LittleEndian.PutUint32(p, uint32(sec))
	binary.LittleEndian.PutUint32(p[4:], uint32(usec))
	return append(p, sizedPayload(audio)...)
}

// serveOnce runs srv on the first connection to a new listener, and is the listener's address.
func serveOnce(t *testing.T, srv *fakeServer, chunks func(send func(uint16, []byte))) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		srv.serve(conn, chunks)
	}()
	return ln.Addr().String()
}

// A server that stops answering without closing the connection is given up on, rather than leaving
// the state on connected for as long as TCP takes to notice.
func TestSessionGivesUpOnASilentServer(t *testing.T) {
	srv := &fakeServer{t: t, hello: make(chan hello, 1), deaf: true}
	addr := serveOnce(t, srv, func(func(uint16, []byte)) {})

	rec := &recorder{}
	s := fast(newSession(addr, "Kitchen", "02:00:00:00:00:01", rec, rec, func(string) {}))
	s.timing.deadAfter = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	err := s.run(ctx)
	if err == nil || !strings.Contains(err.Error(), "nothing from the server") {
		t.Fatalf("run ended with %v, want it to give up on the server", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("gave up after %s, want about deadAfter", took)
	}
}

// One chunk that does not decode costs that chunk; the next plays on a fresh decoder. A run of them
// ends the connection.
func TestSessionGetsOverABadChunk(t *testing.T) {
	tone := make([]byte, 4*960)
	for i := 0; i < len(tone); i += 2 {
		binary.LittleEndian.PutUint16(tone[i:], 3000)
	}
	odd := []byte{1, 2, 3} // not whole 16-bit samples

	srv := &fakeServer{t: t, hello: make(chan hello, 1)}
	addr := serveOnce(t, srv, func(send func(uint16, []byte)) {
		time.Sleep(300 * time.Millisecond)
		for _, audio := range [][]byte{tone, odd, tone, odd, odd, odd, tone} {
			send(msgWireChunk, rawChunk(srv.now(), audio))
			time.Sleep(20 * time.Millisecond)
		}
	})

	rec := &recorder{}
	s := fast(newSession(addr, "Kitchen", "02:00:00:00:00:01", rec, rec, func(string) {}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := s.run(ctx)
	if err == nil || !strings.Contains(err.Error(), "did not decode") {
		t.Fatalf("run ended with %v, want three bad chunks in a row to end it", err)
	}
	// The tone before the bad chunk and the one after it; the last never arrives.
	if n := len(rec.snapshot().writes); n != 2 {
		t.Errorf("%d chunks played, want 2", n)
	}
}

func TestSilent(t *testing.T) {
	if !silent([]int16{0, 8, -8, 3}) {
		t.Error("dither counted as sound")
	}
	if silent([]int16{0, 9}) {
		t.Error("sound counted as silence")
	}
}
