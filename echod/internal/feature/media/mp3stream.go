package media

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/hajimehoshi/go-mp3"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// A stream the device fetches itself - a station kept on it, one the voice assistant found, one typed
// on the setup page - arrives as the station sends it, not converted by Home Assistant first. Most
// internet radio is MP3, which is decoded here; AAC and the rest are refused by name, rather than the
// player reporting a station it cannot make a sound from.

// pcmSource is body as the speaker's own samples: a WAV (what Home Assistant's conversion sends)
// after its header, or an MP3 decoded and brought to the speaker's rate.
func pcmSource(body *bufio.Reader, contentType string) (io.Reader, error) {
	head, err := body.Peek(4)
	if err != nil {
		return nil, fmt.Errorf("reading the stream: %w", err)
	}
	switch {
	case string(head) == "RIFF":
		if err := header(body); err != nil {
			return nil, err
		}
		return body, nil
	case string(head) == "fLaC":
		return newFLACSamples(body)
	case streamIsMP3(head, contentType) || unlabeledMP3(body, contentType):
		if err := skipID3(body); err != nil {
			return nil, err
		}
		return newMP3Samples(body)
	}
	ct := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	if ct == "" {
		ct = "an unknown format"
	}
	return nil, unplayable{ct}
}

// DecodeStream is body as the speaker's own samples (48 kHz stereo S16_LE): WAV, MP3 or FLAC, by its
// first bytes and contentType. For a source that fetches its own stream and plays it as a received
// track (feature/dlna).
func DecodeStream(body *bufio.Reader, contentType string) (io.Reader, error) {
	return pcmSource(body, contentType)
}

// unplayable is a stream in a format the device does not decode: AAC, HLS, Ogg and the rest. It is
// its own kind of failure because it is the one a music library can still play (library.go).
type unplayable struct{ format string }

func (u unplayable) Error() string {
	return fmt.Sprintf("the stream is %s, which this device cannot play", u.format)
}

// IsUnplayable is whether err is a stream the device cannot decode, as against one that is down.
func IsUnplayable(err error) bool {
	var u unplayable
	return errors.As(err, &u)
}

// mp3Types are what servers call MP3.
var mp3Types = []string{"audio/mpeg", "audio/mp3", "audio/x-mpeg", "audio/x-mp3", "audio/mpeg3", "audio/x-mpeg-3"}

// streamIsMP3 is an MP3 by its first bytes (an ID3 tag, or a frame's sync word) or by what the server
// calls it. AAC's own frames (ADTS) start with the same sync bits; what tells them apart is the layer,
// which an MPEG audio frame never leaves at zero and ADTS always does. A server that says AAC is
// believed. One that says nothing, or only that it is bytes, and starts part way into a frame, is
// looked into: a stream is MP3 when a Layer III frame header turns up in its first few kilobytes.
func streamIsMP3(head []byte, contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if strings.Contains(ct, "aac") {
		return false
	}
	sync := head[0] == 0xFF && head[1]&0xE0 == 0xE0 && (head[1]>>1)&0x03 != 0
	if string(head[:3]) == "ID3" || sync || slices.Contains(mp3Types, ct) {
		return true
	}
	return false
}

// unlabeledMP3 is whether a stream its server does not name (no type, or only "bytes") is MP3 all the
// same: a Layer III frame header in its first few kilobytes. The decoder finds the first one itself, and
// something else that happened to hold those bits fails there, within syncMost.
func unlabeledMP3(body *bufio.Reader, contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if ct != "" && ct != "application/octet-stream" {
		return false
	}
	b, _ := body.Peek(4096)
	for i := 0; i+1 < len(b); i++ {
		if b[i] == 0xFF && b[i+1]&0xE0 == 0xE0 && (b[i+1]>>1)&0x03 == 0x01 { // sync, Layer III
			return true
		}
	}
	return false
}

// id3Most is the largest ID3 tag skipped at a stream's start. A station's tag is a title and maybe a
// small picture; the size field can claim a quarter of a gigabyte, which the decoder's own skipping
// would allocate whole, on a device with one gigabyte.
const id3Most = 1 << 20

// skipID3 steps over an ID3v2 tag at the start of body, refusing one larger than id3Most.
func skipID3(body *bufio.Reader) error {
	h, err := body.Peek(10)
	if err != nil || string(h[:3]) != "ID3" {
		return nil
	}
	if h[6]|h[7]|h[8]|h[9] >= 0x80 {
		return errors.New("the stream's ID3 tag is malformed")
	}
	size := int(h[6])<<21 | int(h[7])<<14 | int(h[8])<<7 | int(h[9])
	if h[5]&0x10 != 0 {
		size += 10 // a footer
	}
	if size > id3Most {
		return fmt.Errorf("the stream starts with an ID3 tag of %d bytes, too large to skip", size)
	}
	_, err = body.Discard(10 + size)
	return err
}

// syncMost is how far into a stream a frame header is looked for, before or after a bad frame: the
// decoder scans byte by byte, and a stream of something else called MP3 would keep it scanning forever.
const syncMost = 256 << 10

// scanLimit is the stream as the decoder reads it, failing once the decoder has read syncMost bytes
// without the count being reset: reset once a frame has been decoded.
type scanLimit struct {
	r    io.Reader
	read int
}

func (s *scanLimit) Read(p []byte) (int, error) {
	if s.read >= syncMost {
		return 0, errors.New("no MP3 frame found in the stream")
	}
	n, err := s.r.Read(p[:min(len(p), syncMost-s.read)])
	s.read += n
	return n, err
}

// mp3Samples reads an MP3 stream as 16-bit stereo at speaker.Rate: what the WAV path hands on.
type mp3Samples struct {
	src     *scanLimit
	d       *mp3.Decoder
	rs      resampler
	in      []byte
	out     []byte
	err     error
	resyncs int
}

// mostResyncs is how many bad frames a stream may have before it is given up on. Live radio loses a
// byte now and then; a stream that keeps failing is not one to keep trying.
const mostResyncs = 20

func newMP3Samples(r io.Reader) (*mp3Samples, error) {
	src := &scanLimit{r: r}
	d, err := newDecoder(src)
	if err != nil {
		return nil, err
	}
	// go-mp3 always gives 16-bit stereo; only the rate can differ from the speaker's.
	return &mp3Samples{src: src, d: d, rs: resampler{from: d.SampleRate(), to: speaker.Rate}, in: make([]byte, 4608)}, nil
}

func newDecoder(src *scanLimit) (*mp3.Decoder, error) {
	d, err := mp3.NewDecoder(src)
	if err != nil {
		return nil, fmt.Errorf("mp3: %w", err)
	}
	if d.SampleRate() <= 0 {
		return nil, errors.New("mp3: no sample rate")
	}
	return d, nil
}

func (m *mp3Samples) Read(p []byte) (int, error) {
	for len(m.out) == 0 {
		if m.err != nil {
			return 0, m.err
		}
		n, err := m.d.Read(m.in)
		n -= n % 4
		if n > 0 {
			m.src.read = 0 // a frame came out: the stream is MP3, and the scan starts over
			samples := make([]int16, n/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(m.in[i*2:]))
			}
			samples = m.rs.run(samples)
			m.out = make([]byte, len(samples)*2)
			for i, s := range samples {
				binary.LittleEndian.PutUint16(m.out[i*2:], uint16(s))
			}
		}
		if err != nil {
			m.err = m.resync(err)
		}
	}
	n := copy(p, m.out)
	m.out = m.out[n:]
	return n, nil
}

// resync is what an error from the decoder comes to: the end of the stream is the end, but a bad frame
// in live radio is a byte lost on the way, and a new decoder finds the next frame, up to mostResyncs
// times. nil when it carried on.
func (m *mp3Samples) resync(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || m.resyncs >= mostResyncs {
		return err
	}
	// go-mp3's own "ran out" error is a type of its own, in a package outside reach: known by its words.
	if strings.Contains(err.Error(), "unexpected EOF") {
		return err
	}
	m.resyncs++
	d, derr := newDecoder(m.src)
	if derr != nil {
		return derr
	}
	if d.SampleRate() != m.rs.from {
		m.rs = resampler{from: d.SampleRate(), to: speaker.Rate}
	}
	m.d = d
	return nil
}

// resampler brings interleaved stereo from one rate to another as it streams past, by straight lines
// between neighboring samples: a station's 44.1 kHz to the speaker's 48. The last frame of each chunk
// is carried into the next, so the joins are as smooth as the middles.
type resampler struct {
	from, to int
	pos      float64 // where the next output falls, in input frames from the start of the chunk
	prev     [2]int16
	primed   bool
}

func (r *resampler) run(in []int16) []int16 {
	if r.from == r.to || r.from <= 0 || r.to <= 0 {
		return in
	}
	n := len(in) / 2
	if n == 0 {
		return nil
	}
	at := func(i, ch int) float64 {
		if i < 0 {
			return float64(r.prev[ch])
		}
		return float64(in[i*2+ch])
	}
	if !r.primed {
		r.prev = [2]int16{in[0], in[1]}
		r.primed = true
	}
	step := float64(r.from) / float64(r.to)
	out := make([]int16, 0, int(float64(n)/step)*2+4)
	// Frame -1 is the last of the previous chunk, so the first output can fall between it and frame 0.
	for r.pos < float64(n-1) {
		i := int(r.pos+1) - 1 // floor, for pos down to -1
		f := r.pos - float64(i)
		for ch := range 2 {
			a, b := at(i, ch), at(i+1, ch)
			out = append(out, int16(a+(b-a)*f))
		}
		r.pos += step
	}
	r.pos -= float64(n)
	r.prev = [2]int16{in[(n-1)*2], in[(n-1)*2+1]}
	return out
}
