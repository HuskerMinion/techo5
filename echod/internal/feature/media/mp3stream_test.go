package media

import (
	"bufio"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// sampleMP3 is one of the public-domain samples go-mp3 ships with, from the module cache; the test
// that needs it skips when it is not there, rather than the repository carrying audio.
func sampleMP3(t *testing.T) []byte {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/hajimehoshi/go-mp3").Output()
	if err != nil {
		t.Skip("go-mp3's module is not at hand:", err)
	}
	b, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "example", "mpeg2.mp3"))
	if err != nil {
		t.Skip("go-mp3's sample is not at hand:", err)
	}
	return b
}

// served is body as a station serves it, read back as the player reads it.
func served(t *testing.T, body []byte, contentType string) (io.Reader, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return pcmSource(bufio.NewReaderSize(resp.Body, chunk), resp.Header.Get("Content-Type"))
}

// An MP3 station plays: decoded, at the speaker's rate, and not silence.
func TestAnMP3StreamIsDecodedAtTheSpeakersRate(t *testing.T) {
	src, err := served(t, sampleMP3(t), "audio/mpeg")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src.(*mp3Samples); !ok {
		t.Fatalf("an MP3 was read as %T", src)
	}
	pcm, err := io.ReadAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm)%(speaker.Channels*2) != 0 {
		t.Errorf("%d bytes is not whole frames", len(pcm))
	}
	frames := len(pcm) / (speaker.Channels * 2)
	// The sample is MPEG-2 at a lower rate; what comes out is at the speaker's, so a second of it is
	// speaker.Rate frames whatever the file's own rate.
	if frames < speaker.Rate {
		t.Fatalf("%d frames: less than a second of the sample came out", frames)
	}
	loud := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		if v := int16(binary.LittleEndian.Uint16(pcm[i:])); v > 1000 || v < -1000 {
			loud++
		}
	}
	if loud < frames/100 {
		t.Errorf("only %d loud samples in %d frames: that is silence", loud, frames)
	}
}

// A station sending what the device cannot decode is refused by name, not taken as playing.
func TestAStreamThatIsNotMP3OrWAVIsRefused(t *testing.T) {
	_, err := served(t, []byte{0xFF, 0xF1, 0x50, 0x80, 0, 0, 0, 0}, "audio/aacp")
	if err == nil {
		t.Fatal("an AAC stream was taken")
	}
	if !strings.Contains(err.Error(), "audio/aacp") {
		t.Errorf("the refusal %q does not say what the stream was", err)
	}
}

// What Home Assistant sends, a WAV, still plays as it always has.
func TestAWAVStreamStillPlays(t *testing.T) {
	samples := make([]byte, 4*100)
	src, err := served(t, wave(fmtChunk(2, speaker.Rate, 16), samples), "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	pcm, _ := io.ReadAll(src)
	if len(pcm) != len(samples) {
		t.Errorf("%d bytes of samples came out, want %d", len(pcm), len(samples))
	}
}

// 44.1 kHz comes out at 48 kHz: the right length, fed in any size of chunk, and smooth across the joins.
func TestTheResamplerKeepsTimeAndJoinsSmoothly(t *testing.T) {
	const from, to = 44100, 48000
	rs := resampler{from: from, to: to}
	var out []int16
	pos := 0
	for _, n := range []int{1, 7, 441, 4410, 100, 39141} { // 44100 frames in all
		in := make([]int16, n*2)
		for i := range n {
			v := int16(8000 * math.Sin(2*math.Pi*440*float64(pos+i)/from))
			in[i*2], in[i*2+1] = v, -v
		}
		pos += n
		out = append(out, rs.run(in)...)
	}
	frames := len(out) / 2
	if frames < to-2 || frames > to+2 {
		t.Errorf("a second at %d came out as %d frames at %d", from, frames, to)
	}
	// A 440 Hz tone at this level moves at most about 460 a sample at 48 kHz; a bad join jumps more.
	for i := 2; i < len(out); i += 2 {
		if d := math.Abs(float64(out[i]) - float64(out[i-2])); d > 600 {
			t.Fatalf("a jump of %.0f at frame %d", d, i/2)
		}
	}
}

// A station whose ID3 tag claims hundreds of megabytes is refused before anything is allocated for it; a
// small tag is stepped over and the MP3 after it plays.
func TestAnID3TagIsSkippedWithinReason(t *testing.T) {
	huge := append([]byte("ID3\x04\x00\x00\x7f\x7f\x7f\x7f"), make([]byte, 64)...)
	if _, err := served(t, huge, "audio/mpeg"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a quarter-gigabyte tag gave %v", err)
	}
	tag := append([]byte("ID3\x04\x00\x00\x00\x00\x01\x00"), make([]byte, 128)...) // 128 bytes of tag
	src, err := served(t, append(tag, sampleMP3(t)...), "audio/mpeg")
	if err != nil {
		t.Fatal(err)
	}
	if pcm, _ := io.ReadAll(src); len(pcm) < speaker.Rate*4 {
		t.Errorf("after a small tag only %d bytes came out", len(pcm))
	}
}

// Something else called MP3 is given up on after a while, not scanned forever.
func TestGarbageCalledMP3EndsInsteadOfScanningForever(t *testing.T) {
	junk := make([]byte, syncMost+4096) // zeros: never a frame header
	if _, err := served(t, junk, "audio/mpeg"); err == nil {
		t.Error("a quarter megabyte of zeros was taken for MP3")
	}
}

// A stretch of broken bytes in the middle of a stream costs that stretch, not the station.
func TestAStreamCarriesOnPastABadStretch(t *testing.T) {
	mp3 := sampleMP3(t)
	whole, err := served(t, mp3, "audio/mpeg")
	if err != nil {
		t.Fatal(err)
	}
	full, _ := io.ReadAll(whole)
	broken := append([]byte{}, mp3...)
	mid := len(broken) / 2
	for i := mid; i < mid+2000 && i < len(broken); i++ {
		broken[i] = 0xFF // a run of false sync words
	}
	src, err := served(t, broken, "audio/mpeg")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(src)
	if len(got) < len(full)*3/4 {
		t.Errorf("a broken stretch in the middle cut the stream to %d of %d bytes", len(got), len(full))
	}
}
