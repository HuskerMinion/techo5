package cast

import (
	"encoding/binary"
	"errors"
	"io"
)

// Kinds. Sender to device are below 0x10, device to sender from 0x10.
const (
	KindHello = 1 // JSON Hello: the first message, once
	KindVideo = 2 // 8-byte presentation time (µs), then a JPEG of the whole frame
	KindAudio = 3 // 8-byte presentation time (µs), then interleaved S16LE PCM
	KindClock = 4 // 8-byte sender clock (µs), sent about once a second
	KindBye   = 5 // the sender is done

	KindWelcome = 0x10 // JSON Welcome: answers the hello
	KindStop    = 0x11 // the device ended the cast; the text says why
	KindStats   = 0x12 // JSON Stats: how the cast is going, about once a second
)

// MessageMax is the largest message either side accepts: a full-screen JPEG is well under 1 MB.
const MessageMax = 4 << 20

// Hello opens a cast. Audio is fixed at what the speaker plays, so a sender that cannot make it says
// so here rather than the device playing it at the wrong speed.
type Hello struct {
	Name     string `json:"name"`  // the phone, for the device to show
	Video    bool   `json:"video"` // frames will follow
	Audio    bool   `json:"audio"` // audio will follow
	Rate     int    `json:"rate,omitempty"`
	Channels int    `json:"channels,omitempty"`

	// Scale is 1 (frames are screen size, the default) or 2 (frames are half the screen's width and
	// height and are drawn doubled). A device that is short of decoding speed does a quarter of the
	// work at 2, and the picture is softer.
	Scale int `json:"scale,omitempty"`

	// Title is what is playing, for the device to say on its screen for a moment. Optional.
	Title string `json:"title,omitempty"`
}

// Stats is the device's report on how the cast is going, sent about once a second. The counts are
// totals since the cast began, so a sender takes differences and a lost report costs nothing.
type Stats struct {
	Shown        int `json:"shown"`         // frames put on the screen
	Dropped      int `json:"dropped"`       // frames thrown away: late, skipped behind a newer one, queue overflow, undecodable
	AudioLate    int `json:"audio_late"`    // audio chunks that arrived too late to play on time
	AudioDropped int `json:"audio_dropped"` // audio chunks thrown away
}

// Welcome answers a Hello. W and H are the screen: frames larger than that are refused.
type Welcome struct {
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`
	W       int    `json:"w,omitempty"`
	H       int    `json:"h,omitempty"`
	Rate    int    `json:"rate,omitempty"`     // 48000
	Channel int    `json:"channels,omitempty"` // 2
	// LatencyMs is how long after its presentation time a frame is shown: what the device buffers to
	// smooth the network. A sender may send this far ahead of real time and no further.
	LatencyMs int `json:"latency_ms,omitempty"`
}

// Write sends one message: a 4-byte big-endian length, the kind, then the parts.
func Write(w io.Writer, kind byte, parts ...[]byte) error {
	n := 1
	for _, p := range parts {
		n += len(p)
	}
	if n > MessageMax {
		return errors.New("wire: a message too large to send")
	}
	msg := make([]byte, 4, 4+n)
	binary.BigEndian.PutUint32(msg, uint32(n))
	msg = append(msg, kind)
	for _, p := range parts {
		msg = append(msg, p...)
	}
	_, err := w.Write(msg)
	return err
}

// Read reads one message.
func Read(r io.Reader) (kind byte, payload []byte, err error) {
	var hdr [4]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MessageMax {
		return 0, nil, errors.New("wire: a message of an impossible size")
	}
	msg := make([]byte, n)
	if _, err = io.ReadFull(r, msg); err != nil {
		return
	}
	return msg[0], msg[1:], nil
}

// Stamp is the 8-byte microsecond time that leads a video, audio or clock payload.
func Stamp(us int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(us))
	return b[:]
}

// Split takes the stamp off the front of a payload.
func Split(p []byte) (us int64, rest []byte, ok bool) {
	if len(p) < 8 {
		return 0, nil, false
	}
	return int64(binary.BigEndian.Uint64(p)), p[8:], true
}
