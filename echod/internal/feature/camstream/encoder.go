package camstream

import (
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rtpjpeg"
)

// Video encoders. Where a board has a video encoder, a file of its own registers it (registerEncoder,
// in an init) and the stream is the encoder's (H.264, say) instead of the JPEGs of camstream.go: the
// first registered whose hardware is there makes it. Nothing here knows any board: with none
// registered, or none there, the stream is JPEGs as ever.

// streamEncoder makes the stream on a board's encoder.
type streamEncoder struct {
	name      string
	available func() bool // its hardware is there
	// entities and restore are its own settings, where it has any.
	entities func() []esphome.Entity
	restore  func(c config.Config)
	// describe is the stream it sends: for ONVIF and the camera test.
	describe func() Described
	// sdp is the RTSP description's media lines: m=, a=rtpmap and a=fmtp, each ending "\r\n".
	sdp func() string
	// queue is how many pictures a viewer may be behind before it starts again at a keyframe.
	queue int
	// run makes the pictures from the camera (held: release lets it go) and hands each to the viewers
	// (Feature.hand) until stop; when the camera stops by itself, it lets them all go (letAllGo).
	run func(f *Feature, stop <-chan struct{}, release func())
	// keyframe asks for a keyframe soon, for a viewer that joins; nothing while none runs.
	keyframe func()
}

var encoders []*streamEncoder

// registerEncoder adds a board's encoder, from an init in the board's own file. No board in this tree
// registers one, so the stream is JPEGs as ever.
//
//lint:ignore U1000 the extension point boards register their encoders through; unused in this tree by design.
func registerEncoder(e *streamEncoder) { encoders = append(encoders, e) }

// activeEncoder is the encoder that makes the stream on this board, nil for JPEGs.
func activeEncoder() *streamEncoder {
	for _, e := range encoders {
		if e.available() {
			return e
		}
	}
	return nil
}

// Described is the stream as it is sent: its codec, size, rate and bitrate, for ONVIF and the screen.
type Described struct {
	Codec              string // "H264", or "JPEG"
	W, H, FPS, Bitrate int    // Bitrate 0: not limited
}

// Stream is the stream this board sends.
func Stream() Described {
	if e := activeEncoder(); e != nil {
		return e.describe()
	}
	w, h := size()
	return Described{"JPEG", w, h, streamFPS(), 0}
}

// sendable is one picture as the stream sends it: an RTP/JPEG frame, or an encoder's.
type sendable interface {
	Packets(seq *uint16, ts, ssrc uint32, mtu int) [][]byte
}

// keyed is a picture a viewer can start at, or not: a video encoder's keyframes. A picture that does
// not say is one to start at. at is when the camera took it.
type keyed interface {
	keyframe() bool
	at() time.Time
}

// encoderEntities are the encoder's own settings, on a board that has one.
func (f *Feature) encoderEntities() []esphome.Entity {
	if e := f.encoder(); e != nil && e.entities != nil {
		return e.entities()
	}
	return nil
}

func (f *Feature) restoreEncoder(c config.Config) {
	for _, e := range encoders {
		if e.restore != nil {
			e.restore(c)
		}
	}
}

// startEncoder starts the encoder's pictures for the first viewer.
func (f *Feature) startEncoder(e *streamEncoder) error {
	release, err := acquireCamera()
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	var once sync.Once
	f.mu.Lock()
	f.stopPic = func() { once.Do(func() { close(stop) }) }
	f.mu.Unlock()
	go e.run(f, stop, release)
	return nil
}

// acquireCamera holds the camera for an encoder; replaced in tests.
var acquireCamera = func() (func(), error) { return camera.Get().Acquire() }

// queueFor is how many pictures a viewer may be behind: one JPEG, the newest; an encoder's more.
func (f *Feature) queueFor(e *streamEncoder) int {
	if e != nil {
		return e.queue
	}
	return 1
}

// hand gives an encoder's picture to every viewer: one that is a whole queue behind loses what is
// queued and starts again at the next keyframe.
func (f *Feature) hand(pic sendable) {
	k, isKeyed := pic.(keyed)
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		if isKeyed {
			if len(ch) == cap(ch) {
				f.lost[ch] = true
				for len(ch) > 0 {
					<-ch
				}
			}
			if f.lost[ch] {
				if !k.keyframe() {
					continue
				}
				delete(f.lost, ch)
			}
			ch <- pic
			continue
		}
		select {
		case <-ch: // behind: the older picture goes
		default:
		}
		ch <- pic
	}
}

// letAllGo lets every viewer go, the camera having stopped.
func (f *Feature) letAllGo() {
	f.mu.Lock()
	subs := make([]chan sendable, 0, len(f.subs))
	for ch := range f.subs {
		subs = append(subs, ch)
	}
	f.mu.Unlock()
	for _, ch := range subs {
		f.unsubscribe(ch)
	}
}

// keyClock times a viewer's keyed pictures from the first keyframe it is sent: ok is false for a
// picture before it, which the viewer cannot start at. Other pictures keep the time they were given.
type keyClock struct{ first time.Time }

func (c *keyClock) stamp(pic sendable, ts uint32) (uint32, bool) {
	k, ok := pic.(keyed)
	if !ok {
		return ts, true
	}
	if c.first.IsZero() {
		if !k.keyframe() {
			return 0, false
		}
		c.first = k.at()
	}
	return uint32(k.at().Sub(c.first).Seconds() * rtpjpeg.ClockRate), true
}
