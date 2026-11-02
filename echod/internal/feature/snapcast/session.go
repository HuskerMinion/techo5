package snapcast

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	ssync "github.com/Sendspin/sendspin-go/pkg/sync"

	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Timing.
//
// syncFirst and syncEvery are how often the clock is asked: quickly at first, so the filter has
// something to go on before the first chunk is due, then once a second, well inside the five seconds
// after which ClockSync calls itself lost.
//
// idleAfter is how long the stream may be silent before the room gives the speaker back. A stream that
// has stopped can arrive as chunks of zeros or as no chunks at all, so it is the sound that counts, not
// the wire; a pause between two tracks is shorter than this.
//
// readTick is how often the session looks for silence when nothing arrives at all.
var (
	syncFirst  = 50 * time.Millisecond
	syncBurst  = 20
	syncEvery  = time.Second
	idleAfter  = 3 * time.Second
	readTick   = 500 * time.Millisecond
	dialTimout = 5 * time.Second
)

// quiet is the largest sample a silent chunk holds: dither and a resampler's noise are not music.
const quiet = 8

// output is what the session plays through, sendspin.Output on the device and a recorder in a test.
type output interface {
	Use(clock *ssync.ClockSync)
	Open(sampleRate, channels, bitDepth int) error
	Close()
	Write(at int64, samples []int16)
	Flush()
	SetLevel(level float32)
	Producer() speaker.Producer
}

// backgrounds is the speaker's arbiter: the room takes the speaker while the stream is audible.
type backgrounds interface {
	Took(p speaker.Producer)
	Gave(p speaker.Producer)
}

// settings is Snapserver's ServerSettings: the buffer every client plays behind the server, this
// client's latency, and its volume.
type settings struct {
	BufferMs int  `json:"bufferMs"`
	Latency  int  `json:"latency"`
	Muted    bool `json:"muted"`
	Volume   int  `json:"volume"`
}

// hello introduces the client. ID is what the server keeps the client's group, name, volume and latency
// under, so it has to be the same every time: the MAC, as snapclient's is.
type hello struct {
	Arch                      string
	ClientName                string
	HostName                  string
	ID                        string
	Instance                  int
	MAC                       string
	OS                        string
	SnapStreamProtocolVersion int
	Version                   string
}

// session is one connection to a server, from dial to close.
type session struct {
	addr   string
	name   string
	mac    string
	out    output
	bg     backgrounds
	report func(state string)

	clock *ssync.ClockSync
	now   func() int64 // the client's clock, Unix microseconds

	conn net.Conn
	wmu  sync.Mutex
	tmu  sync.Mutex
	asks map[uint16]int64 // a Time request's id, and when it went
	next uint16

	// The read loop's own; nothing else touches them.
	set      settings
	dec      sendspin.Decoder
	fmt      format
	open     bool // out is attached and the room has the speaker
	audible  time.Time
	unplayed bool // a stream the speaker cannot take, said once
}

func newSession(addr, name, mac string, out output, bg backgrounds, report func(string)) *session {
	return &session{
		addr: addr, name: name, mac: mac, out: out, bg: bg, report: report,
		clock: ssync.NewClockSync(),
		now:   func() int64 { return time.Now().UnixMicro() },
		asks:  map[uint16]int64{},
		set:   settings{BufferMs: 1000, Volume: 100},
	}
}

// run connects and plays until ctx ends or the connection does. It always leaves the speaker as it
// found it.
func (s *session) run(ctx context.Context) error {
	d := net.Dialer{Timeout: dialTimout}
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return err
	}
	s.conn = conn
	defer conn.Close()
	defer s.stop()

	// The read loop below blocks in Read; closing the connection is what gets it out when ctx ends.
	stopClose := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopClose()

	s.out.Use(s.clock)
	if err := s.sendHello(); err != nil {
		return err
	}
	s.report(stateConnected)
	slog.Info("snapcast connected", "server", s.addr, "id", s.mac)

	syncCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	safe.Go("snapcast time sync", func() { s.syncLoop(syncCtx) })

	return s.readLoop(ctx)
}

func (s *session) send(typ uint16, payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.next++
	_, err := s.conn.Write(encode(typ, s.next, s.now(), payload))
	return err
}

func (s *session) sendHello() error {
	host, _ := os.Hostname()
	if s.name != "" {
		host = s.name
	}
	b, _ := json.Marshal(hello{
		Arch:                      runtime.GOARCH,
		ClientName:                "Snapclient",
		HostName:                  host,
		ID:                        s.mac,
		Instance:                  1,
		MAC:                       s.mac,
		OS:                        "TECHO5",
		SnapStreamProtocolVersion: 2,
		Version:                   "0.34.0",
	})
	return s.send(msgHello, sizedPayload(b))
}

// syncLoop asks the server's time, a burst at first and then steadily.
func (s *session) syncLoop(ctx context.Context) {
	for i := 0; ctx.Err() == nil; i++ {
		if err := s.askTime(); err != nil {
			return
		}
		wait := syncEvery
		if i < syncBurst {
			wait = syncFirst
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (s *session) askTime() error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.next++
	id, sent := s.next, s.now()

	s.tmu.Lock()
	s.asks[id] = sent
	// A reply that never came leaves its ask behind; a handful is plenty to match against.
	if len(s.asks) > 32 {
		clear(s.asks)
		s.asks[id] = sent
	}
	s.tmu.Unlock()

	_, err := s.conn.Write(encode(msgTime, id, sent, timePayload()))
	return err
}

// timed takes a Time reply. The request went at t1 by our clock; the server says it arrived latency
// later by its own, so t2 is t1 + latency; the reply left at t3, its sent, and arrived at t4. That is
// NTP's four times, which is what ClockSync filters, and the same offset Snapcast's own client takes:
// (latency_c2s - latency_s2c) / 2.
func (s *session) timed(m base, p []byte) {
	latency, err := parseTime(p)
	if err != nil {
		return
	}
	s.tmu.Lock()
	t1, ok := s.asks[m.refersTo]
	delete(s.asks, m.refersTo)
	s.tmu.Unlock()
	if !ok {
		return
	}
	s.clock.ProcessSyncResponse(t1, t1+latency, m.sent, m.received)
}

// message is one read off the connection, or the error that ended reading.
type message struct {
	m   base
	p   []byte
	err error
}

// readLoop handles what the server sends, and looks for silence between messages. Reading is a
// goroutine of its own rather than a read deadline: a deadline that fires halfway through a message
// leaves the stream out of step for good, and a stream gone idle sends nothing to wake the loop.
func (s *session) readLoop(ctx context.Context) error {
	msgs := make(chan message, 64)
	safe.Go("snapcast read", func() {
		for {
			m, p, err := readMessage(s.conn, s.now)
			select {
			case msgs <- message{m, p, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	})

	tick := time.NewTicker(readTick)
	defer tick.Stop()
	for {
		var msg message
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.idleCheck()
			continue
		case msg = <-msgs:
		}
		if msg.err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return msg.err
		}

		m, p := msg.m, msg.p
		switch m.typ {
		case msgTime:
			s.timed(m, p)
		case msgServerSettings:
			s.settings(p)
		case msgCodecHeader:
			s.codecHeader(p)
		case msgWireChunk:
			s.chunk(p)
		case msgError:
			return fmt.Errorf("snapcast: server says %s", parseError(p))
		}
		s.idleCheck()
	}
}

func (s *session) settings(p []byte) {
	j, _, err := sized(p)
	if err != nil {
		return
	}
	set := s.set
	if err := json.Unmarshal(j, &set); err != nil {
		slog.Warn("snapcast server settings", "err", err)
		return
	}
	s.set = set
	s.out.SetLevel(level(set.Volume, set.Muted))
	slog.Info("snapcast settings", "buffer_ms", set.BufferMs, "latency_ms", set.Latency,
		"volume", set.Volume, "muted", set.Muted)
}

// level is snapclient's software mixer: the volume cubed, which is near enough to how loudness is heard
// that the slider's middle sounds like the middle.
func level(volume int, muted bool) float32 {
	if muted {
		return 0
	}
	v := float64(max(0, min(volume, 100))) / 100
	return float32(math.Pow(v, 3))
}

// codecHeader starts a new stream: a new decoder, and nothing of the old one left to play.
func (s *session) codecHeader(p []byte) {
	codec, header, err := parseCodecHeader(p)
	if err != nil {
		slog.Warn("snapcast codec header", "err", err)
		return
	}
	f, err := formatOf(codec, header)
	if err != nil {
		slog.Error("snapcast cannot play this stream", "codec", codec, "err", err)
		s.closeDecoder()
		return
	}
	dec, err := sendspin.NewDecoder(codec, f.rate, f.channels, f.bits, header)
	if err != nil {
		slog.Error("snapcast decoder", "codec", codec, "err", err)
		s.closeDecoder()
		return
	}
	s.closeDecoder()
	s.dec, s.fmt, s.unplayed = dec, f, false
	s.out.Flush()
	slog.Info("snapcast stream", "codec", f.codec, "rate", f.rate, "ch", f.channels, "bits", f.bits)
}

func (s *session) closeDecoder() {
	if s.dec != nil {
		_ = s.dec.Close()
		s.dec = nil
	}
}

// chunk decodes a span of the stream and places it where the server says it is heard: its timestamp
// plus the buffer every client plays behind, less this client's latency, which the server lets a user
// set for a speaker that is slow to sound. The card's own latency is the output's business.
func (s *session) chunk(p []byte) {
	if s.dec == nil {
		return
	}
	at, audio, err := parseWireChunk(p)
	if err != nil {
		return
	}
	pcm, err := s.dec.Decode(audio)
	if err != nil {
		slog.Warn("snapcast decode", "err", err)
		s.closeDecoder()
		return
	}
	if len(pcm) == 0 {
		return
	}

	if !silent(pcm) {
		s.audible = time.Now()
		if !s.open && !s.start() {
			return
		}
	}
	if !s.open {
		return
	}
	s.out.Write(at+int64(s.set.BufferMs-s.set.Latency)*1000, pcm)
}

// start takes the speaker for an audible stream, once the clock can be believed: a chunk placed by a
// clock that has not synced is placed decades out.
func (s *session) start() bool {
	if s.clock.CheckQuality() == ssync.QualityLost {
		return false
	}
	if err := s.out.Open(s.fmt.rate, s.fmt.channels, speaker.Bits); err != nil {
		if !s.unplayed {
			slog.Error("snapcast", "err", err)
			s.unplayed = true
		}
		return false
	}
	s.open = true
	s.bg.Took(s.out.Producer())
	s.report(statePlaying)
	slog.Info("snapcast playing", "quality", s.clock.CheckQuality())
	return true
}

// idleCheck gives the speaker back once the stream has been silent for idleAfter.
func (s *session) idleCheck() {
	if s.open && time.Since(s.audible) > idleAfter {
		slog.Info("snapcast idle")
		s.stop()
		s.report(stateConnected)
	}
}

// stop lets go of the speaker, if the room has it.
func (s *session) stop() {
	if !s.open {
		return
	}
	s.open = false
	s.out.Close()
	s.bg.Gave(s.out.Producer())
}

func silent(pcm []int16) bool {
	for _, v := range pcm {
		if v > quiet || v < -quiet {
			return false
		}
	}
	return true
}
