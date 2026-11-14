package camstream

import (
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// fakeAU is an encoder's picture: one packet carrying n.
type fakeAU struct {
	n     byte
	key   bool
	taken time.Time
}

func (p fakeAU) Packets(seq *uint16, ts, ssrc uint32, mtu int) [][]byte {
	b := make([]byte, 13)
	b[0], b[1] = 0x80, 0x80|96
	binary.BigEndian.PutUint16(b[2:], *seq)
	binary.BigEndian.PutUint32(b[4:], ts)
	binary.BigEndian.PutUint32(b[8:], ssrc)
	b[12] = p.n
	*seq++
	return [][]byte{b}
}
func (p fakeAU) keyframe() bool { return p.key }
func (p fakeAU) at() time.Time  { return p.taken }

func withEncoders(t *testing.T, es ...*streamEncoder) {
	t.Helper()
	saved := encoders
	encoders = es
	t.Cleanup(func() { encoders = saved })
}

// The stream is the first registered encoder whose hardware is there, else JPEGs.
func TestTheFirstEncoderThereMakesTheStream(t *testing.T) {
	absent := &streamEncoder{name: "absent", available: func() bool { return false }}
	there := &streamEncoder{name: "there", available: func() bool { return true },
		describe: func() Described { return Described{"H264", 1920, 1088, 60, 21_000_000} }}
	later := &streamEncoder{name: "later", available: func() bool { return true }}
	withEncoders(t, absent, there, later)
	if e := activeEncoder(); e != there {
		t.Fatalf("active %v, want the first that is there", e)
	}
	if st := Stream(); st.Codec != "H264" || st.W != 1920 || st.Bitrate != 21_000_000 {
		t.Fatalf("Stream %+v", st)
	}
	withEncoders(t, absent)
	if activeEncoder() != nil || Stream().Codec != "JPEG" {
		t.Fatal("with no encoder there, the stream is not JPEG")
	}
}

// An encoder's own settings join the switches, and are restored with them.
func TestAnEncodersSettings(t *testing.T) {
	sw := &esphome.Switch{Base: esphome.Base{ObjectID: "fake_setting"}}
	restored := false
	e := &streamEncoder{name: "fake", available: func() bool { return true },
		entities: func() []esphome.Entity { return []esphome.Entity{sw} },
		restore:  func(config.Config) { restored = true }}
	withEncoders(t, e)
	f := build()
	f.available = func() bool { return true }
	if got := f.Entities(); len(got) != 4 || got[3] != sw {
		t.Fatalf("entities %v", got)
	}
	f.Restore(config.Config{})
	if !restored {
		t.Fatal("not restored")
	}
}

// Over RTSP an encoder's stream is described by it, each viewer is started at a keyframe it asks for,
// and its pictures are timed from that keyframe, when the camera took them.
func TestAnEncodersStreamOverRTSP(t *testing.T) {
	f, addr, _, released := server(t)
	savedAcquire := acquireCamera
	acquireCamera = func() (func(), error) { return func() { released <- true }, nil }
	t.Cleanup(func() { acquireCamera = savedAcquire })
	var mu sync.Mutex
	keys := 0
	running := make(chan *Feature, 1)
	stopped := make(chan struct{})
	e := &streamEncoder{name: "fake", available: func() bool { return true }, queue: 3,
		sdp: func() string { return "m=video 0 RTP/AVP 96\r\na=rtpmap:96 H264/90000\r\n" },
		run: func(f *Feature, stop <-chan struct{}, release func()) {
			defer release()
			running <- f
			<-stop
			close(stopped)
		},
		keyframe: func() { mu.Lock(); keys++; mu.Unlock() }}
	f.encoder = func() *streamEncoder { return e }

	c := dial(t, addr, "viewer", "secret")
	r := c.do("DESCRIBE", c.url, "Accept: application/sdp")
	if r.status != 200 || !strings.Contains(r.body, "a=rtpmap:96 H264/90000\r\na=control:track1") {
		t.Fatalf("DESCRIBE: %d %q", r.status, r.body)
	}
	r = c.do("SETUP", r.headers["content-base"]+"track1", "Transport: RTP/AVP/TCP;unicast;interleaved=0-1")
	id, _, _ := strings.Cut(r.headers["session"], ";")
	if r = c.do("PLAY", c.url, "Session: "+id); r.status != 200 {
		t.Fatalf("PLAY: %d", r.status)
	}
	pump := <-running
	at := time.Now()
	pump.hand(fakeAU{n: 1, taken: at})                                   // not one to start at
	pump.hand(fakeAU{n: 2, key: true, taken: at.Add(time.Second)})       // the start
	pump.hand(fakeAU{n: 3, taken: at.Add(time.Second + time.Second/30)}) // a frame on
	var got []byte
	var stamps []uint32
	for len(got) < 2 {
		_, p := c.packet()
		got = append(got, p[12])
		stamps = append(stamps, binary.BigEndian.Uint32(p[4:]))
	}
	if !slices.Equal(got, []byte{2, 3}) || stamps[0] != 0 || stamps[1] < 2999 || stamps[1] > 3000 {
		t.Fatalf("pictures %v at %v, want the keyframe at 0 and the next a 30th of a second on", got, stamps)
	}
	mu.Lock()
	if keys != 1 {
		t.Errorf("%d keyframes asked for one viewer", keys)
	}
	mu.Unlock()
	if r = c.do("TEARDOWN", c.url, "Session: "+id); r.status != 200 {
		t.Fatalf("TEARDOWN: %d", r.status)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the encoder was not stopped after TEARDOWN")
	}
	<-released
}
