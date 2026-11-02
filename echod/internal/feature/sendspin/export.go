package sendspin

import (
	"fmt"
	"strings"

	ssync "github.com/Sendspin/sendspin-go/pkg/sync"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Output is the timestamped output this package plays a group's stream through, for another
// protocol's client (feature/snapcast): it places audio by the server's clock and holds the room to
// it, which is everything a synchronized client needs past its own wire format.
//
// There is one speaker, so one Output is heard at a time: whichever opened last is attached.
type Output struct{ o *out }

func NewOutput() *Output { return &Output{o: newOut(speaker.Get())} }

// Use points the output at the clock the server's timestamps are in.
func (x *Output) Use(clock *ssync.ClockSync) { x.o.use(clock) }

// Open attaches the output to the speaker, or refuses a format the speaker cannot play.
func (x *Output) Open(sampleRate, channels, bitDepth int) error {
	return x.o.open(sampleRate, channels, bitDepth)
}

func (x *Output) Close() { x.o.close() }

// Write places samples at the frame server time at (microseconds) is heard.
func (x *Output) Write(at int64, samples []int16) { x.o.write(at, samples) }

// Flush drops what has not been heard and the anchor with it.
func (x *Output) Flush() { x.o.flush() }

// SetLevel is the stream's own volume, 0 to 1, applied as frames are rendered.
func (x *Output) SetLevel(level float32) {
	x.o.mu.Lock()
	x.o.level = max(0, min(level, 1))
	x.o.mu.Unlock()
}

// Producer is what the speaker's backgrounds take and give back (Arbiter.Took and Gave).
func (x *Output) Producer() speaker.Producer { return x.o }

func (x *Output) Misses() (late, dropped int64) { return x.o.misses() }

func (x *Output) QueuedMs() int { return x.o.queuedMs() }

// Decoder turns a chunk into interleaved 16-bit samples: all of the audio the chunk carries, in order,
// so a protocol whose chunk is a span of the stream places it at the chunk's timestamp.
type Decoder interface {
	Decode(chunk []byte) ([]int16, error)
	Close() error
}

// NewDecoder is a decoder for codec: flac (header is the stream header the frames follow), opus or pcm.
func NewDecoder(codec string, sampleRate, channels, bitDepth int, header []byte) (Decoder, error) {
	switch strings.ToLower(codec) {
	case "flac":
		d, err := newFLACDecoder(header)
		if err != nil {
			return nil, err
		}
		return allFrames{d.(*flacDecoder)}, nil
	case "opus", "pcm":
		d, err := decoderFor(codec, sampleRate, channels, bitDepth, header)
		if err != nil {
			return nil, err
		}
		return oneChunk{d}, nil
	}
	return nil, fmt.Errorf("sendspin: no decoder for %q", codec)
}

type allFrames struct{ d *flacDecoder }

func (a allFrames) Decode(chunk []byte) ([]int16, error) { return a.d.decodeAll(chunk) }
func (a allFrames) Close() error                         { return a.d.close() }

// oneChunk wraps a decoder whose chunk is already whole: an Opus packet, or samples.
type oneChunk struct{ d decoder }

func (o oneChunk) Decode(chunk []byte) ([]int16, error) { return o.d.decode(chunk) }
func (o oneChunk) Close() error                         { return o.d.close() }
