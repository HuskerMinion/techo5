//go:build panel

package speaker

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/loopback"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

// dump records what is played while files/dump-echo exists: echo-played.raw holds the 48 kHz stereo
// S16LE stream, and echo-played.txt the loopback index its first frame became (every three frames
// are one loopback sample). A debug build only (lib/loopback's Dump).
var dump = loopback.Dump{FlagPath: layout.StateDir + "/dump-echo"}

// speakerSocket is the abstract socket the app plays from: 48 kHz stereo S16LE, written to an
// AudioTrack as it arrives. The app's blocking writes are what pace the write loop here.
const speakerSocket = "@techo5.spk"

const dialWait = 10 * time.Second

// sendBuffer is eight periods of 48 kHz stereo S16LE, 160 ms: enough that a slow tablet stalling the
// daemon for a moment does not run the track dry, and small enough to keep the writes close to what
// is heard. The delay between the two is measured (mic/capture_panel.go), so it only has to be steady.
const sendBuffer = 8 * period * Channels * Bits / 8

// openDevice connects to the app. There is no mixer and nothing to hold: Android owns the hardware.
func openDevice() (playback, *alsa.Mixer, *os.File, bool, error) {
	deadline := time.Now().Add(dialWait)
	for {
		conn, err := panelsock.Dial(speakerSocket)
		if err == nil {
			// A send buffer of a few periods, so a write blocks once the app is that far behind. With the
			// default, the socket alone holds a second or more, the writes run that far ahead of what
			// is heard in bursts, and the echo reference (which counts what was written) jumps with them.
			if uc, ok := conn.(*net.UnixConn); ok {
				_ = uc.SetWriteBuffer(sendBuffer)
			}
			return &socketPlayback{conn: conn}, nil, nil, false, nil
		}
		if time.Now().After(deadline) {
			return nil, nil, nil, false, fmt.Errorf("speaker: connecting to the app: %w", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// socketPlayback sends the stream to the app and keeps what it sent, at 16 kHz mono, as the
// reference the microphone's echo canceller reads (lib/loopback). Silence is kept too: the history is
// a clock as well as a recording, and the microphone lines up against it.
type socketPlayback struct {
	conn net.Conn

	// The downmix runs across writes: a write can end part way through a frame, and every third frame
	// makes one reference sample.
	partial []byte
	acc     int32
	n       int
	out     []int16

	dumpFile *os.File
}

func (p *socketPlayback) Write(b []byte) (int, error) {
	n, err := p.conn.Write(b)
	if n > 0 {
		p.record(b[:n])
		p.keep(b[:n])
	}
	return n, err
}

func (p *socketPlayback) record(b []byte) {
	if !dump.On() {
		if p.dumpFile != nil {
			_ = p.dumpFile.Close()
			p.dumpFile = nil
		}
		return
	}
	if p.dumpFile == nil {
		f, err := os.Create(layout.StateDir + "/echo-played.raw")
		if err != nil {
			return
		}
		p.dumpFile = f
		// The index the next loopback sample will get, and how far into a sample the downmix is.
		_ = os.WriteFile(layout.StateDir+"/echo-played.txt",
			[]byte(fmt.Sprintf("%d %d %d\n", loopback.Played.Written(), p.n, len(p.partial))), 0o600)
	}
	_, _ = p.dumpFile.Write(b)
}

// keep averages each frame's two channels and every three frames, 48 kHz to 16 kHz. A box filter is
// enough for a reference: the canceller learns the room's response, and the speech it plays was
// upsampled from 16 kHz in the first place.
func (p *socketPlayback) keep(b []byte) {
	if len(p.partial) > 0 {
		b = append(p.partial, b...)
		p.partial = nil
	}
	p.out = p.out[:0]
	for len(b) >= 4 {
		l := int32(int16(uint16(b[0]) | uint16(b[1])<<8))
		r := int32(int16(uint16(b[2]) | uint16(b[3])<<8))
		b = b[4:]
		p.acc += l + r
		p.n++
		if p.n == 3 {
			p.out = append(p.out, int16(p.acc/6))
			p.acc, p.n = 0, 0
		}
	}
	if len(b) > 0 {
		p.partial = append([]byte(nil), b...)
	}
	loopback.Played.Write(p.out)
}

func (p *socketPlayback) Close() error { return p.conn.Close() }

// errNoDelay is Delay's answer: what the socket and the app's AudioTrack still hold is not known
// here, so Latency is DefaultLatency.
var errNoDelay = errors.New("speaker: the app does not say how much it holds")

func (p *socketPlayback) Delay() (int, error) { return 0, errNoDelay }
