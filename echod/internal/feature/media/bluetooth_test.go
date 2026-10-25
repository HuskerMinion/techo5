package media

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

func pcm(frames, channels int, sample func(f, ch int) int16) []byte {
	b := make([]byte, frames*channels*2)
	for f := 0; f < frames; f++ {
		for ch := 0; ch < channels; ch++ {
			binary.LittleEndian.PutUint16(b[(f*channels+ch)*2:], uint16(sample(f, ch)))
		}
	}
	return b
}

// 48 kHz stereo is the speaker's own format: passed through untouched.
func TestReceivedAtTheSpeakerRatePassesThrough(t *testing.T) {
	in := pcm(100, 2, func(f, ch int) int16 { return int16(f*10 + ch) })
	out := newToSpeaker(speaker.Rate, 2).run(in)
	if len(out) != 200 {
		t.Fatalf("got %d samples, want 200", len(out))
	}
	for f := 0; f < 100; f++ {
		if out[f*2] != int16(f*10) || out[f*2+1] != int16(f*10+1) {
			t.Fatalf("frame %d changed: %d %d", f, out[f*2], out[f*2+1])
		}
	}
}

// 44.1 kHz, read in uneven pieces, comes out at 48 kHz with the right length and no discontinuity.
func TestReceivedAt44100IsResampledContinuously(t *testing.T) {
	const frames = 44100 // one second
	tone := func(f, ch int) int16 { return int16(8000 * math.Sin(2*math.Pi*440*float64(f)/44100)) }
	in := pcm(frames, 2, tone)

	c := newToSpeaker(44100, 2)
	var out []int16
	for off, piece := 0, 0; off < len(in); piece++ {
		n := (1234 + piece*977) % 5000 * 4
		if n == 0 {
			n = 4
		}
		if off+n > len(in) {
			n = len(in) - off
		}
		out = append(out, c.run(in[off:off+n])...)
		off += n
	}

	got := len(out) / 2
	if got < speaker.Rate-10 || got > speaker.Rate+10 {
		t.Errorf("one second came out as %d frames, want about %d", got, speaker.Rate)
	}
	// A 440 Hz tone at 8000 changes by at most about 2*pi*440/48000*8000 ≈ 461 per output frame.
	for i := 1; i < got; i++ {
		if d := int(out[i*2]) - int(out[(i-1)*2]); d > 520 || d < -520 {
			t.Fatalf("jump of %d at output frame %d: the pieces did not join", d, i)
		}
	}
}

// Mono goes to both channels.
func TestReceivedMonoFillsBothChannels(t *testing.T) {
	in := pcm(480, 1, func(f, _ int) int16 { return int16(f) })
	out := newToSpeaker(speaker.Rate, 1).run(in)
	for i := 0; i+1 < len(out); i += 2 {
		if out[i] != out[i+1] {
			t.Fatalf("channels differ at %d: %d %d", i/2, out[i], out[i+1])
		}
	}
}

// A phone plays once it has sent sendingAgain of audible audio without a break: not for silence,
// however long, nor for a sound of a second, nor for audio just under audibleFloor. It stops after
// receiveQuiet with nothing audible, idle reads included, not for a shorter quiet moment.
func TestPhonePlays(t *testing.T) {
	at := time.Unix(1000, 0)
	var p PhonePlays
	read := func(level int16, d time.Duration) (turned bool) {
		for end := at.Add(d); at.Before(end); at = at.Add(20 * time.Millisecond) {
			if p.Hear(pcm(882, 2, func(int, int) int16 { return level }), at) {
				turned = true
			}
		}
		return turned
	}
	idle := func(d time.Duration) (turned bool) {
		for end := at.Add(d); at.Before(end); at = at.Add(time.Second) {
			if p.Hear(nil, at) {
				turned = true
			}
		}
		return turned
	}
	silence := read(0, 30*time.Second)
	aSound, after := read(4000, time.Second), read(0, time.Second)
	if silence || aSound || after || read(audibleFloor, 3*time.Second) {
		t.Fatal("heard playing without music")
	}
	start, heard := at, time.Time{}
	for ; at.Before(start.Add(2 * time.Second)); at = at.Add(20 * time.Millisecond) {
		if p.Hear(pcm(882, 2, func(int, int) int16 { return 3000 }), at) && heard.IsZero() {
			heard = at
		}
	}
	if heard.IsZero() || !p.Playing() {
		t.Fatal("music was not heard")
	}
	if got := heard.Sub(start); got < sendingAgain || got > sendingAgain+50*time.Millisecond {
		t.Fatalf("heard after %v, want %v", got, sendingAgain)
	}
	if read(0, 3*time.Second) || read(3000, time.Second) || !p.Playing() {
		t.Fatal("a quiet moment stopped it")
	}
	if !idle(receiveQuiet+time.Second) || p.Playing() {
		t.Error("a phone gone idle was still heard playing")
	}

	// Asked to pause, a phone that stops is heard stopping at once, and playing again after it.
	read(3000, 2*time.Second)
	p.Asked(true)
	if read(3000, 3*time.Second) || !p.Playing() {
		t.Fatal("a phone asked to pause and playing on was heard stopping")
	}
	if !read(0, time.Second) || p.Playing() {
		t.Fatal("a phone asked to pause that stopped was still heard playing")
	}
	if !read(3000, 2*time.Second) || !p.Playing() {
		t.Fatal("a phone played again after it paused was not heard starting")
	}

	// One that plays on through the seconds after the ask ignored it: the gap between two of its
	// songs is not it stopping.
	p.Asked(true)
	if read(3000, receiveQuiet+time.Second) || read(0, time.Second) || read(3000, 3*time.Second) ||
		!p.Playing() {
		t.Error("a phone that ignored the pause was heard stopping between two songs")
	}
}

// AirPlay's 44.1 kHz and a phone's goes through the band-limited converter, as a station does, mono
// or stereo: straight lines left a 10 kHz tone a ghost at 13.9 kHz only 21 dB under it.
func TestReceivedAt44100GoesThroughTheConverter(t *testing.T) {
	for _, channels := range []int{1, 2} {
		tone := func(f, _ int) int16 { return int16(16000 * math.Sin(2*math.Pi*10000*float64(f)/44100)) }
		in := pcm(4410, channels, tone)
		got := newToSpeaker(44100, channels).run(in)

		samples := make([]int16, 4410*channels)
		for i := range samples {
			samples[i] = int16(binary.LittleEndian.Uint16(in[i*2:]))
		}
		want := audio.NewConverter(44100, speaker.Rate, channels).Run(samples, nil)
		if len(got) != len(want)*speaker.Channels/channels {
			t.Fatalf("%d channels: %d samples, want %d", channels, len(got), len(want)*speaker.Channels/channels)
		}
		for f := range len(want) / channels {
			l, r := want[f*channels], want[f*channels+channels-1]
			if got[f*2] != l || got[f*2+1] != r {
				t.Fatalf("%d channels, frame %d: %d %d, want %d %d", channels, f, got[f*2], got[f*2+1], l, r)
			}
		}
	}
}
