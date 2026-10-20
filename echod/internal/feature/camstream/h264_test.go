//go:build !dot

package camstream

import (
	"testing"
	"time"

	"github.com/pion/rtp"
)

// An access unit goes out as RTP with the marker on its last packet only, a NAL larger than a packet
// split (FU-A), and the sequence counted on.
func TestH264Packets(t *testing.T) {
	big := make([]byte, 3000)
	big[0] = 0x65 // IDR slice
	au := append([]byte{0, 0, 0, 1, 0x67, 1, 2, 3, 0, 0, 0, 1, 0x68, 4, 0, 0, 1}, big...)
	seq := uint16(100)
	pkts := h264Picture{au: au, key: true}.Packets(&seq, 9000, 7, 1400)
	if len(pkts) < 3 {
		t.Fatalf("%d packets for a 3 kB slice", len(pkts))
	}
	for i, b := range pkts {
		var p rtp.Packet
		if err := p.Unmarshal(b); err != nil {
			t.Fatal(err)
		}
		if p.Marker != (i == len(pkts)-1) || p.Timestamp != 9000 || p.SSRC != 7 || p.PayloadType != h264Type {
			t.Fatalf("packet %d: %+v", i, p.Header)
		}
		if len(b) > 1400 {
			t.Fatalf("packet %d is %d bytes", i, len(b))
		}
	}
	if seq != 100+uint16(len(pkts)) {
		t.Fatalf("sequence at %d after %d packets", seq, len(pkts))
	}
}

// A viewer a whole queue behind loses what is queued and starts again at the next keyframe; one that
// keeps up gets every picture.
func TestH264Behind(t *testing.T) {
	f := build()
	slow, fast := make(chan sendable, 3), make(chan sendable, 3)
	f.subs[slow], f.subs[fast] = struct{}{}, struct{}{}
	pic := func(key bool) h264Picture { return h264Picture{key: key, taken: time.Now()} }
	for _, key := range []bool{true, false, false} {
		f.hand(pic(key))
		<-fast
	}
	f.hand(pic(false)) // the slow one is full: it loses its three and waits
	<-fast
	if len(slow) != 0 || !f.lost[slow] {
		t.Fatalf("slow viewer: %d queued, lost %v", len(slow), f.lost[slow])
	}
	f.hand(pic(false))
	<-fast
	if len(slow) != 0 {
		t.Fatal("a P-frame queued for a viewer waiting for a keyframe")
	}
	f.hand(pic(true))
	<-fast
	if p := (<-slow).(h264Picture); !p.key || f.lost[slow] {
		t.Fatal("the keyframe did not start the slow viewer again")
	}
}
