package dlna

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Moving through a song (Seek) is fetching it again from the byte where the time asked for is, with an
// HTTP Range request, and decoding from there. Where that byte is depends on the format, and is found
// out once a song has started (mapSong), from its first bytes fetched by a Range request of their own,
// which is also how a server that does not send parts of a file is told:
//
//   - WAV: exactly, from the data chunk's place and the speaker's 192,000 bytes a second (the only WAV
//     the device plays is in its own format).
//   - MP3: from the table of contents a VBR file's first frame carries (Xing, or Fraunhofer's VBRI),
//     else in proportion to the file's length, which is exact for a constant bit rate; the fetch then
//     starts at the first frame from that byte, found as two frame headers a frame apart.
//   - FLAC: from the file's seek table, to the point at or before the time, and the samples between it
//     and the time decoded and dropped. A FLAC without one is not moved through: finding a frame
//     without a table is guesswork this does not do.
//
// Anything else (no length, a server that answers a Range request with the whole file, a live stream)
// cannot be moved through, and the controller is told so (710).

// headBytes is how much of a song's start is fetched to find out how to move through it: the headers
// and the first frame of all but an MP3 with a large picture in its tag, whose first frame is fetched
// by itself.
const headBytes = 64 << 10

// seekMap is how to move through one song.
type seekMap struct {
	uri    string
	kind   string // wav, mp3 or flac
	length int64
	dur    time.Duration

	// WAV: where the samples are, and a frame's bytes (its block align).
	dataStart, dataEnd int64
	align              int64

	// MP3: where the frames start, and the table of contents if there is one: Xing's hundred places
	// (in 256ths of tocBytes), or VBRI's byte offsets from audioStart, one every vbriEvery.
	audioStart int64
	toc        []byte
	tocBytes   int64
	vbri       []int64
	vbriEvery  time.Duration

	// FLAC: a header that stands for the file's own (fLaC and its STREAMINFO alone), its rate, where its
	// first frame is, and its seek points.
	flacHead   []byte
	rate       int
	firstFrame int64
	points     []flacPoint
}

type flacPoint struct{ sample, off uint64 }

// fetchRange asks for bytes from to to of uri (to < 0 for the rest), and takes only a server that
// sends that part (206, from the byte asked for).
func fetchRange(ctx context.Context, client *http.Client, uri string, from, to int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	want := "bytes=" + strconv.FormatInt(from, 10) + "-"
	if to >= 0 {
		want += strconv.FormatInt(to, 10)
	}
	req.Header.Set("Range", want)
	resp, err := client.Do(req)
	if err != nil {
		return nil, quietErr(err) // it names the address, token and all
	}
	if resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("the server answered a request for part of the song with %s", resp.Status)
	}
	if cr := resp.Header.Get("Content-Range"); !strings.HasPrefix(cr, "bytes "+strconv.FormatInt(from, 10)+"-") {
		resp.Body.Close()
		return nil, fmt.Errorf("the server sent another part of the song (%q)", cr)
	}
	return resp, nil
}

// ranger reads parts of a song: from its first bytes where they cover the part, else by a Range
// request for it.
type ranger struct {
	ctx    context.Context
	client *http.Client
	uri    string
	length int64
	head   []byte
}

func (g *ranger) at(off int64, n int) ([]byte, error) {
	if off < 0 || n < 0 || off >= g.length {
		return nil, errors.New("past the end of the song")
	}
	n = int(min(int64(n), g.length-off))
	if off+int64(n) <= int64(len(g.head)) {
		return g.head[off : off+int64(n)], nil
	}
	resp, err := fetchRange(g.ctx, g.client, g.uri, off, off+int64(n)-1)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(n)))
	if err != nil {
		return nil, err
	}
	if len(b) < n {
		return nil, errors.New("the server sent less of the song than asked for")
	}
	return b, nil
}

// mapSong finds out how to move through the song at uri, length bytes long; hint is the length in time
// its description gave, for an MP3 that says none itself.
func mapSong(ctx context.Context, client *http.Client, uri string, length int64, hint time.Duration) (*seekMap, error) {
	if length <= 0 {
		return nil, errors.New("the server gives no length for the song")
	}
	resp, err := fetchRange(ctx, client, uri, 0, min(length, headBytes)-1)
	if err != nil {
		return nil, err
	}
	head, err := io.ReadAll(io.LimitReader(resp.Body, headBytes))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(head) < 12 {
		return nil, errors.New("the song is too short to move through")
	}
	g := &ranger{ctx: ctx, client: client, uri: uri, length: length, head: head}
	m := &seekMap{uri: uri, length: length}
	switch {
	case string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		err = m.mapWAV(g)
	case string(head[:4]) == "fLaC":
		err = m.mapFLAC(g, hint)
	default:
		err = m.mapMP3(g, hint)
	}
	if err != nil {
		return nil, err
	}
	if m.dur <= 0 {
		return nil, errors.New("the song's length in time is not known")
	}
	return m, nil
}

func (m *seekMap) mapWAV(g *ranger) error {
	pos, format := int64(12), false
	for range 32 {
		h, err := g.at(pos, 8)
		if err != nil {
			return err
		}
		id, size := string(h[:4]), int64(binary.LittleEndian.Uint32(h[4:8]))
		switch id {
		case "fmt ":
			b, err := g.at(pos+8, 16)
			if err != nil {
				return err
			}
			if ch, rate, bits := binary.LittleEndian.Uint16(b[2:]), binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint16(b[14:]); ch != 2 || rate != speaker.Rate || bits != 16 {
				return fmt.Errorf("a WAV the device does not play (%d Hz, %d channels, %d bits)", rate, ch, bits)
			}
			// Two channels of 16 bits are four bytes a frame; a WAV that says otherwise is not one to
			// count frames in.
			if m.align = int64(binary.LittleEndian.Uint16(b[12:])); m.align != 4 {
				return fmt.Errorf("a WAV whose frames are %d bytes", m.align)
			}
			format = true
		case "data":
			if !format {
				return errors.New("a WAV with its samples before its format")
			}
			m.kind, m.dataStart = "wav", pos+8
			// A WAV written as it was made says no size, or the largest there is: the file's end is.
			m.dataEnd = m.dataStart + size
			if size == 0 || size == 0xFFFFFFFF || m.dataEnd > m.length {
				m.dataEnd = m.length
			}
			m.dur = time.Duration((m.dataEnd - m.dataStart) / m.align * int64(time.Second) / speaker.Rate)
			return nil
		}
		pos += 8 + size + size%2
	}
	return errors.New("no samples in the WAV's first chunks")
}

// flacMostSkip bounds how far past a FLAC's seek point the place moved to may be: what lies between is
// decoded and dropped, which for a minute is a moment, and for an hour would be minutes of the
// device's time with nothing heard.
const flacMostSkip = time.Minute

// flacMostPoints bounds the seek points taken from a FLAC's table, and with them what is fetched of it
// and kept for the song: far more than any encoder writes (one every ten seconds is usual).
const flacMostPoints = 1 << 16

func (m *seekMap) mapFLAC(g *ranger, hint time.Duration) error {
	pos := int64(4)
	var info []byte
	table := false
	for range 64 {
		h, err := g.at(pos, 4)
		if err != nil {
			return err
		}
		last, typ, n := h[0]&0x80 != 0, h[0]&0x7f, int64(h[1])<<16|int64(h[2])<<8|int64(h[3])
		switch {
		case typ == 0 && n == 34:
			if info, err = g.at(pos+4, 34); err != nil {
				return err
			}
		case typ == 3 && !table:
			// The first table only: a stream has one, and a file with more is not believed for them.
			table = true
			b, err := g.at(pos+4, int(min(n, 18*flacMostPoints)))
			if err != nil {
				return err
			}
			for i := 0; i+18 <= len(b); i += 18 {
				p := flacPoint{sample: binary.BigEndian.Uint64(b[i:]), off: binary.BigEndian.Uint64(b[i+8:])}
				if p.sample != ^uint64(0) { // a placeholder
					m.points = append(m.points, p)
				}
			}
		}
		pos += 4 + n
		if last {
			m.firstFrame = pos
			break
		}
	}
	if info == nil || m.firstFrame == 0 {
		return errors.New("a FLAC whose headers could not be read")
	}
	m.rate = int(info[10])<<12 | int(info[11])<<4 | int(info[12])>>4
	total := uint64(info[13]&0x0f)<<32 | uint64(binary.BigEndian.Uint32(info[14:18]))
	if m.rate <= 0 {
		return errors.New("a FLAC with no rate")
	}
	// A point past the samples or past the file is not a place to fetch from: dropped, which also
	// keeps the first frame and an offset from adding up to a byte that is not there.
	room := uint64(max(m.length-m.firstFrame, 0))
	kept := m.points[:0]
	for _, p := range m.points {
		if p.off < room && (total == 0 || p.sample < total) {
			kept = append(kept, p)
		}
	}
	m.points = kept
	moves := false
	for _, p := range m.points {
		moves = moves || p.sample > 0
	}
	if !moves {
		return errors.New("a FLAC with no seek table")
	}
	m.kind = "flac"
	m.flacHead = append([]byte("fLaC\x80\x00\x00\x22"), info...) // STREAMINFO, the last block
	m.dur = hint
	if total > 0 {
		m.dur = samplesTime(total, m.rate)
	}
	return nil
}

// samplesTime is how long n samples at rate last, without the product overflowing for a long file (a
// FLAC counts its samples in 36 bits), and at most the longest time there is.
func samplesTime(n uint64, rate int) time.Duration {
	r := uint64(rate)
	secs := n / r
	if secs >= uint64(math.MaxInt64/int64(time.Second)) {
		return math.MaxInt64
	}
	return time.Duration(secs)*time.Second + time.Duration(n%r*uint64(time.Second)/r)
}

// timeSamples is the sample of a FLAC at rate that to falls in, without the product overflowing.
func timeSamples(to time.Duration, rate int) uint64 {
	to = max(to, 0)
	return uint64(to/time.Second)*uint64(rate) + uint64(to%time.Second)*uint64(rate)/uint64(time.Second)
}

// mp3Frame is what an MPEG audio frame header says.
type mp3Frame struct {
	mpeg1         bool
	rate, bitrate int // samples a second, bits a second
	size          int // the frame's bytes
	samples       int // a frame's samples
	mono          bool
}

var (
	mp3Bitrates1 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3Bitrates2 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3Rates     = [3]int{44100, 48000, 32000}
)

// mp3Header reads a Layer III frame header, MPEG-1 or 2 (what the decoder plays), at the start of b.
func mp3Header(b []byte) (mp3Frame, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return mp3Frame{}, false
	}
	version, layer := (b[1]>>3)&3, (b[1]>>1)&3
	br, sr := b[2]>>4, (b[2]>>2)&3
	if (version != 3 && version != 2) || layer != 1 || br == 0 || br == 15 || sr == 3 {
		return mp3Frame{}, false
	}
	f := mp3Frame{mpeg1: version == 3, rate: mp3Rates[sr], mono: b[3]>>6 == 3}
	pad := int(b[2]>>1) & 1
	if f.mpeg1 {
		f.bitrate, f.samples = mp3Bitrates1[br]*1000, 1152
		f.size = 144*f.bitrate/f.rate + pad
	} else {
		f.rate /= 2
		f.bitrate, f.samples = mp3Bitrates2[br]*1000, 576
		f.size = 72*f.bitrate/f.rate + pad
	}
	return f, true
}

// mp3Sync is where in b the first frame is: a header with another like it a frame later, which a
// stray 0xFF in the middle of a frame almost never has.
func mp3Sync(b []byte) (int, bool) {
	for i := 0; i+4 <= len(b); i++ {
		f, ok := mp3Header(b[i:])
		if !ok {
			continue
		}
		next, ok := mp3Header(b[min(i+f.size, len(b)):])
		if ok && next.mpeg1 == f.mpeg1 && next.rate == f.rate {
			return i, true
		}
	}
	return 0, false
}

func (m *seekMap) mapMP3(g *ranger, hint time.Duration) error {
	start := int64(0)
	if h := g.head; len(h) >= 10 && string(h[:3]) == "ID3" {
		if h[6]|h[7]|h[8]|h[9] >= 0x80 {
			return errors.New("an MP3 with a malformed tag")
		}
		start = 10 + (int64(h[6])<<21 | int64(h[7])<<14 | int64(h[8])<<7 | int64(h[9]))
		if h[5]&0x10 != 0 {
			start += 10
		}
	}
	b, err := g.at(start, 8192)
	if err != nil {
		return err
	}
	i, ok := mp3Sync(b)
	if !ok {
		return errors.New("not an MP3 this device moves through")
	}
	b = b[i:]
	f, _ := mp3Header(b)
	m.kind, m.audioStart = "mp3", start+int64(i)
	frames := 0
	// Xing (or Info, its name in a constant bit rate file) after the side information.
	side := 32
	switch {
	case f.mpeg1 && f.mono:
		side = 17
	case !f.mpeg1 && !f.mono:
		side = 17
	case !f.mpeg1:
		side = 9
	}
	if x := 4 + side; len(b) >= x+8 && (string(b[x:x+4]) == "Xing" || string(b[x:x+4]) == "Info") {
		flags, p := binary.BigEndian.Uint32(b[x+4:]), x+8
		if flags&1 != 0 && len(b) >= p+4 {
			frames, p = int(binary.BigEndian.Uint32(b[p:])), p+4
		}
		if flags&2 != 0 && len(b) >= p+4 {
			m.tocBytes, p = int64(binary.BigEndian.Uint32(b[p:])), p+4
		}
		if flags&4 != 0 && len(b) >= p+100 {
			m.toc = append([]byte(nil), b[p:p+100]...)
		}
	} else if v := 4 + 32; len(b) >= v+26 && string(b[v:v+4]) == "VBRI" {
		frames = int(binary.BigEndian.Uint32(b[v+14:]))
		entries, scale := int(binary.BigEndian.Uint16(b[v+18:])), int64(binary.BigEndian.Uint16(b[v+20:]))
		size, every := int(binary.BigEndian.Uint16(b[v+22:])), int(binary.BigEndian.Uint16(b[v+24:]))
		if entries > 0 && size >= 1 && size <= 4 && every > 0 && len(b) >= v+26+entries*size {
			at := int64(0)
			for e := range entries {
				m.vbri = append(m.vbri, at)
				var n int64
				for _, c := range b[v+26+e*size : v+26+(e+1)*size] {
					n = n<<8 | int64(c)
				}
				at += n * scale
			}
			// In 64 bits: an int is 32 on the device, and a second's nanoseconds alone are most of that.
			m.vbriEvery = time.Duration(int64(every) * int64(f.samples) * int64(time.Second) / int64(f.rate))
		}
	}
	if m.tocBytes <= 0 || m.tocBytes > m.length-m.audioStart {
		m.tocBytes = m.length - m.audioStart
	}
	switch {
	case frames > 0:
		m.dur = time.Duration(int64(frames) * int64(f.samples) * int64(time.Second) / int64(f.rate))
	case hint > 0:
		m.dur = hint
	default:
		// A constant bit rate: the frames' bytes at the first one's rate.
		m.dur = time.Duration((m.length - m.audioStart) * 8 * int64(time.Second) / int64(f.bitrate))
	}
	return nil
}

// place is the byte to fetch from for to, and the time that byte starts at where that is known
// exactly (WAV, a FLAC seek point); an MP3's is taken to be to.
func (m *seekMap) place(to time.Duration) (off int64, at time.Duration) {
	to = max(0, min(to, m.dur))
	switch m.kind {
	case "wav":
		// No further than the last whole frame, counted from where the samples start: a seek to the very
		// end fetches that frame and ends, where a byte past the file would be refused (416).
		frame := int64(to) * speaker.Rate / int64(time.Second)
		last := m.dataStart + max((m.dataEnd-m.dataStart)/m.align-1, 0)*m.align
		off = min(m.dataStart+frame*m.align, last)
		return off, time.Duration((off - m.dataStart) / m.align * int64(time.Second) / speaker.Rate)
	case "flac":
		want := timeSamples(to, m.rate)
		p := flacPoint{}
		for _, q := range m.points {
			if q.sample <= want && q.sample >= p.sample {
				p = q
			}
		}
		return m.firstFrame + int64(p.off), samplesTime(p.sample, m.rate)
	}
	frac := float64(to) / float64(m.dur)
	audio := float64(m.tocBytes)
	switch {
	case len(m.toc) == 100:
		pct := min(frac*100, 99.999)
		i := int(pct)
		a, b := float64(m.toc[i]), 256.0
		if i < 99 {
			b = float64(m.toc[i+1])
		}
		off = m.audioStart + int64(math.Round((a+(b-a)*(pct-float64(i)))/256*audio))
	case len(m.vbri) > 0 && m.vbriEvery > 0:
		i := int(to / m.vbriEvery)
		if i >= len(m.vbri) {
			off = m.audioStart + int64(audio)
			break
		}
		next := int64(audio)
		if i+1 < len(m.vbri) {
			next = m.vbri[i+1]
		}
		part := float64(to-time.Duration(i)*m.vbriEvery) / float64(m.vbriEvery)
		off = m.audioStart + m.vbri[i] + int64(float64(next-m.vbri[i])*part)
	default:
		off = m.audioStart + int64(frac*float64(m.length-m.audioStart))
	}
	return min(off, m.length-1), to
}

// open fetches the song from to, decoded: the speaker's samples, the body to close, and where in the
// song they start.
func (m *seekMap) open(ctx context.Context, client *http.Client, to time.Duration) (io.Reader, io.ReadCloser, time.Duration, error) {
	off, at := m.place(to)
	if m.kind == "flac" && to-at > flacMostSkip {
		// A table with no point near the place (a hostile one, or one with points an hour apart) would
		// have the device decode all that lies between, to drop it: not moved there.
		return nil, nil, 0, fmt.Errorf("the FLAC's seek table has no point within %v of the place", flacMostSkip)
	}
	resp, err := fetchRange(ctx, client, m.uri, off, -1)
	if err != nil {
		return nil, nil, 0, err
	}
	var pcm io.Reader
	switch m.kind {
	case "wav":
		pcm = bufio.NewReaderSize(resp.Body, 64<<10)
	case "mp3":
		br := bufio.NewReaderSize(resp.Body, 64<<10)
		b, _ := br.Peek(16 << 10)
		i, ok := mp3Sync(b)
		if !ok {
			resp.Body.Close()
			return nil, nil, 0, errors.New("no MP3 frame where the song was moved to")
		}
		_, _ = br.Discard(i)
		pcm, err = media.DecodeStream(br, "audio/mpeg")
	case "flac":
		// The file's own header is put back in front (its STREAMINFO is all the decoder needs), and
		// what is decoded between the seek point and the time is dropped.
		br := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(m.flacHead), resp.Body), 64<<10)
		if pcm, err = media.DecodeStream(br, "audio/flac"); err == nil {
			// Counted in samples first, as timeSamples does, so the product cannot overflow.
			skip := int64(timeSamples(to-at, speaker.Rate)) * 4
			pcm, at = &skipping{r: pcm, left: skip}, to
		}
	}
	if err != nil {
		resp.Body.Close()
		return nil, nil, 0, err
	}
	return pcm, resp.Body, at, nil
}

// skipping drops the first left bytes of r.
type skipping struct {
	r    io.Reader
	left int64
}

func (s *skipping) Read(p []byte) (int, error) {
	if s.left > 0 {
		n, err := io.CopyN(io.Discard, s.r, s.left)
		s.left -= n
		if err != nil {
			return 0, err
		}
	}
	return s.r.Read(p)
}

// parseSeekTarget reads a Seek's REL_TIME (or ABS_TIME) target as UPnP writes it, H+:MM:SS with an
// optional fraction, .F+ or .F0/F1; M:SS too, which some controllers send.
func parseSeekTarget(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	var frac time.Duration
	if i := strings.IndexByte(v, '.'); i >= 0 {
		f := v[i+1:]
		v = v[:i]
		if n, d, ok := strings.Cut(f, "/"); ok {
			a, err1 := digits(n)
			b, err2 := digits(d)
			// Nine digits at most, so a second's nanoseconds times the numerator stays in 64 bits.
			if err1 != nil || err2 != nil || len(n) > 9 || len(d) > 9 || b == 0 || a >= b {
				return 0, false
			}
			frac = time.Duration(a * int64(time.Second) / b)
		} else {
			if _, err := digits(f); err != nil {
				return 0, false
			}
			x, _ := strconv.ParseFloat("0."+f, 64)
			frac = time.Duration(x * float64(time.Second))
		}
	}
	parts := strings.Split(v, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	// No field longer than a time needs, so nothing read can overflow a time once multiplied out:
	// the hours are bounded below, and minutes on their own (M:SS) as well.
	for _, p := range parts {
		if len(p) > 6 {
			return 0, false
		}
	}
	var n [3]int64
	for i, p := range parts {
		x, err := digits(p)
		if err != nil {
			return 0, false
		}
		n[3-len(parts)+i] = x
	}
	if n[2] >= 60 || (len(parts) == 3 && n[1] >= 60) || n[1] > seekMostMinutes || n[0] > 1000 {
		return 0, false
	}
	return time.Duration(n[0])*time.Hour + time.Duration(n[1])*time.Minute + time.Duration(n[2])*time.Second + frac, true
}

// seekMostMinutes bounds the minutes of a target written M:SS: a thousand hours, as H:MM:SS is.
const seekMostMinutes = 60000

// digits reads a run of decimal digits, nothing else (no sign, no space).
func digits(s string) (int64, error) {
	if s == "" || len(s) > 12 {
		return 0, errors.New("not a number")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
