package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/useragent"
)

// voiceTimeout bounds a reply fetched while it is still being written. Home Assistant sends it as
// the text to speech engine makes it, so the body takes as long as the agent takes to answer, not
// as long as a download.
const voiceTimeout = 2 * time.Minute

// voiceChunk is how much is read at a time: 32 ms of 16 kHz mono, the size Home Assistant streams
// a reply over the API in.
const voiceChunk = 1024

// speechStall is how long a streamed announcement may go without a byte, once its body has begun,
// before it is given up on (#559). An engine that speaks sentence by sentence pauses between them,
// but not for this long; one that has gone quiet for good would otherwise hold the speaker until
// voiceTimeout. A variable so the tests need not wait for it.
var speechStall = 20 * time.Second

// errStalled is a body that stopped arriving part way.
var errStalled = errors.New("the audio stopped arriving")

// FetchVoice fetches speech Home Assistant is still generating and hands it on as it arrives, as
// 16-bit mono samples at speaker.VoiceRate in little-endian bytes: the form a reply streamed over
// the API takes. A WAVE body plays as it comes. Anything else (MP3) is read whole and decoded as
// Fetch would, then handed on at once.
func FetchVoice(ctx context.Context, url string, each func([]byte)) error {
	return fetchVoice(ctx, url, 0, each)
}

// fetchVoice is FetchVoice, given up on once the body has gone stall without a byte; zero is never.
// A reply fetched early is not given one: it starts before the agent has written anything, and a
// slow agent is not a broken stream.
func fetchVoice(ctx context.Context, url string, stall time.Duration, each func([]byte)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: voiceTimeout, Transport: useragent.Transport(nil)} // named as Fetch's are (#730)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	var in io.Reader = resp.Body
	if stall > 0 {
		w := &stallReader{r: resp.Body, stall: stall, cancel: cancel}
		defer w.stop()
		in = w
	}

	body := bufio.NewReaderSize(io.LimitReader(in, mostAudio+1), 4096)
	head, err := body.Peek(12)
	if err != nil || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		whole, err := io.ReadAll(body)
		if err != nil {
			return stalled(ctx, err, url)
		}
		if len(whole) > mostAudio {
			return fmt.Errorf("%s: more than %d bytes of audio for one announcement", url, mostAudio)
		}
		samples, err := decode(whole, resp.Header.Get("Content-Type"), url)
		if err != nil {
			return err
		}
		each(pcmBytes(samples))
		return nil
	}
	if err := streamWAV(body, each); err != nil {
		return stalled(ctx, err, url)
	}
	return nil
}

// stalled is err as the stall it was, when the read failed because the body stopped arriving.
func stalled(ctx context.Context, err error, url string) error {
	if errors.Is(context.Cause(ctx), errStalled) {
		return fmt.Errorf("%w (from %s)", errStalled, url)
	}
	return fmt.Errorf("%w (from %s)", err, url)
}

// stallReader cancels the fetch once its body has gone stall without a byte. The clock starts once
// audio has begun (stallAfter bytes in), not with the first byte: a server can send the WAV header at
// once and the first sentence only when the engine has made it, and that wait is the engine starting,
// which voiceTimeout bounds.
type stallReader struct {
	r      io.Reader
	stall  time.Duration
	cancel context.CancelCauseFunc
	timer  *time.Timer
	read   int
}

// stallAfter is how far into the body the stall clock starts: past any WAV header, into the audio.
const stallAfter = 1024

func (s *stallReader) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	s.read += n
	if n > 0 && s.read > stallAfter {
		if s.timer == nil {
			s.timer = time.AfterFunc(s.stall, func() { s.cancel(errStalled) })
		} else {
			s.timer.Reset(s.stall)
		}
	}
	return n, err
}

func (s *stallReader) stop() {
	if s.timer != nil {
		s.timer.Stop()
	}
}

// streamWAV reads a WAVE body chunk by chunk to its data, then hands the data on as it arrives. The
// data chunk's size is not trusted: a body still being written says 0 or the most it could be, so
// the data runs to the end of the body.
func streamWAV(r io.Reader, each func([]byte)) error {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return err
	}

	var channels, rate, bits int
	for {
		var h [8]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return fmt.Errorf("no data chunk: %w", err)
		}
		id, size := string(h[0:4]), int64(binary.LittleEndian.Uint32(h[4:8]))
		if id == "data" {
			break
		}
		if id != "fmt " {
			if _, err := io.CopyN(io.Discard, r, size+size%2); err != nil {
				return err
			}
			continue
		}
		// A fmt chunk is 16 to 40 bytes; a size far past that is a broken header, not a buffer to make.
		if size > 64 {
			return errors.New("fmt chunk too large")
		}
		fmtChunk := make([]byte, size+size%2)
		if _, err := io.ReadFull(r, fmtChunk); err != nil {
			return err
		}
		if size < 16 {
			return errors.New("short fmt chunk")
		}
		format := binary.LittleEndian.Uint16(fmtChunk[0:])
		if format == 0xFFFE && size >= 26 {
			format = binary.LittleEndian.Uint16(fmtChunk[24:])
		}
		channels = int(binary.LittleEndian.Uint16(fmtChunk[2:]))
		rate = int(binary.LittleEndian.Uint32(fmtChunk[4:]))
		bits = int(binary.LittleEndian.Uint16(fmtChunk[14:]))
		if format != 1 || bits != 16 || channels < 1 {
			return fmt.Errorf("unsupported WAVE: format %#x, %d-bit, %d channels", format, bits, channels)
		}
	}
	if channels == 0 {
		return errors.New("data before fmt")
	}

	frame := 2 * channels
	buf := make([]byte, voiceChunk*channels)
	var held []byte
	for {
		n, err := r.Read(buf)
		held = append(held, buf[:n]...)
		if whole := len(held) / frame * frame; whole > 0 {
			each(voiceBytes(held[:whole], channels, rate))
			held = append(held[:0], held[whole:]...)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// voiceBytes is interleaved 16-bit frames as mono at speaker.VoiceRate. Home Assistant is asked for
// that already, so this is nearly always a copy.
func voiceBytes(pcm []byte, channels, rate int) []byte {
	if channels == 1 && rate == speaker.VoiceRate {
		return append([]byte(nil), pcm...)
	}
	frames := len(pcm) / (2 * channels)
	mono := make([]int16, frames)
	for i := range mono {
		var sum int
		for c := range channels {
			sum += int(int16(binary.LittleEndian.Uint16(pcm[(i*channels+c)*2:])))
		}
		mono[i] = int16(sum / channels)
	}
	return pcmBytes(toVoiceRate(mono, rate))
}

func pcmBytes(samples []int16) []byte {
	out := make([]byte, 2*len(samples))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}
