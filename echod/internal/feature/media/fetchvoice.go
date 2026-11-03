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
)

// voiceTimeout bounds a reply fetched while it is still being written. Home Assistant sends it as
// the text to speech engine makes it, so the body takes as long as the agent takes to answer, not
// as long as a download.
const voiceTimeout = 2 * time.Minute

// voiceChunk is how much is read at a time: 32 ms of 16 kHz mono, the size Home Assistant streams
// a reply over the API in.
const voiceChunk = 1024

// FetchVoice fetches speech Home Assistant is still generating and hands it on as it arrives, as
// 16-bit mono samples at speaker.VoiceRate in little-endian bytes: the form a reply streamed over
// the API takes. A WAVE body plays as it comes. Anything else (MP3) is read whole and decoded as
// Fetch would, then handed on at once.
func FetchVoice(ctx context.Context, url string, each func([]byte)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: voiceTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	body := bufio.NewReaderSize(io.LimitReader(resp.Body, mostAudio+1), 4096)
	head, err := body.Peek(12)
	if err != nil || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		whole, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		samples, err := decode(whole, resp.Header.Get("Content-Type"), url)
		if err != nil {
			return err
		}
		each(pcmBytes(samples))
		return nil
	}
	if err := streamWAV(body, each); err != nil {
		return fmt.Errorf("%w (from %s)", err, url)
	}
	return nil
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
