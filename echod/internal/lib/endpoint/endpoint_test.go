package endpoint

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tone is n windows of a 300 Hz tone at an RMS level, standing in for a voice at that loudness.
func tone(windows int, rms float64) []int16 {
	out := make([]int16, windows*Window)
	amp := rms * math.Sqrt2
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*300*float64(i)/16000))
	}
	return out
}

func feed(d *Detector, parts ...[]int16) bool {
	var ended bool
	for _, p := range parts {
		ended = d.Feed(p)
	}
	return ended
}

// Speech and then quiet ends the turn, at the end of the speech.
func TestSpeechThenQuietEnds(t *testing.T) {
	d := New(Default)
	if !feed(d, tone(15, 1500), tone(10, 30)) {
		t.Fatal("speech followed by a second of quiet did not end the turn")
	}
	if got := d.EndedAt(); got != 15*Window {
		t.Errorf("ended at %d samples, want the end of the speech at %d", got, 15*Window)
	}
}

// A pause between words is not the end.
func TestAPauseBetweenWordsDoesNotEnd(t *testing.T) {
	d := New(Default)
	if feed(d, tone(8, 1500), tone(4, 30), tone(8, 1500)) {
		t.Fatal("a 0.4 s pause between words ended the turn")
	}
}

// A television across the room, well below the speaker, is where the speaker stopped.
func TestTheRoomTalkingAfterTheSpeakerEnds(t *testing.T) {
	d := New(Default)
	room := tone(30, 400) // about a quarter of the speaker's level, as in the recordings
	if !feed(d, tone(15, 1500), room) {
		t.Fatal("the room talking at a quarter of the speaker's level kept the turn open")
	}
	if got := d.EndedAt(); got != 15*Window {
		t.Errorf("ended at %d samples, want %d", got, 15*Window)
	}
}

// Nothing ends before there has been speech: the quiet before someone starts is not a request.
func TestQuietAloneNeverEnds(t *testing.T) {
	d := New(Default)
	if feed(d, tone(40, 30)) {
		t.Fatal("four seconds of quiet with nobody talking ended the turn")
	}
}

// softRequest is a loud wake word followed by a request between Off and On (0.4 * peak), with a
// breath between each word: the shape the device logs show when a turn is cut early.
func softRequest() [][]int16 {
	parts := [][]int16{tone(3, 1500)}
	for i := 0; i < 8; i++ {
		parts = append(parts, tone(1, 600), tone(1, 30))
	}
	return parts
}

// Quiet ends a turn only when it is quiet in a row. A window between Off and On is the speaker still
// talking, more softly than their loudest moment: it breaks the run rather than adding to it, or a
// longer sentence collects six quiet windows across its breaths and is cut while they are speaking.
func TestQuietMustBeConsecutive(t *testing.T) {
	cases := []struct {
		name  string
		parts [][]int16
		ended bool
		at    int
	}{
		{
			name:  "a request softer than the wake word, with breaths",
			parts: softRequest(),
			ended: false,
		},
		{
			name:  "a soft window breaks the run",
			parts: [][]int16{tone(3, 1500), tone(3, 30), tone(1, 600), tone(3, 30)},
			ended: false,
		},
		{
			name:  "six quiet windows in a row end it",
			parts: [][]int16{tone(3, 1500), tone(6, 30)},
			ended: true,
			at:    3 * Window,
		},
		{
			name:  "a window at the speaker's level breaks the run",
			parts: [][]int16{tone(3, 1500), tone(4, 30), tone(1, 1500), tone(4, 30)},
			ended: false,
		},
		{
			name:  "speech below the gate does not end it",
			parts: [][]int16{tone(2, 1500), tone(6, 30)},
			ended: false,
		},
		{
			name:  "the last heard window sets EndedAt",
			parts: [][]int16{tone(3, 1500), tone(1, 600), tone(6, 30)},
			ended: true,
			at:    4 * Window,
		},
		{
			// A breath before the last word is not counted towards the run that ends the turn:
			// the soft word breaks it, so the six quiet windows start after it and EndedAt is
			// its end, keeping the word in the audio.
			name:  "a soft final word is not cut off",
			parts: [][]int16{tone(3, 1500), tone(2, 30), tone(1, 600), tone(6, 30)},
			ended: true,
			at:    6 * Window,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := New(Default)
			if got := feed(d, c.parts...); got != c.ended {
				t.Fatalf("ended = %v, want %v", got, c.ended)
			}
			if got := d.EndedAt(); got != c.at {
				t.Errorf("ended at %d samples, want %d", got, c.at)
			}
		})
	}
}

// TestQuietIsConsecutive is the invariant the table cases illustrate, over generated sequences of the
// three levels there are: the turn ends at the first run of Quiet windows below Off*peak, and only
// once enough windows at or above On have counted as speech to open it. EndedAt is the end of the
// window before that run - the last one heard, because nothing in the run reached Off.
//
// Every sequence opens with a loud window, so the peak the thresholds are relative to is fixed and
// the levels mean the same thing throughout. Deterministic and small: one seed, a few hundred
// sequences.
func TestQuietIsConsecutive(t *testing.T) {
	const (
		loud   = 1500.0 // at or above On*peak
		voiced = 600.0  // between Off*peak and On*peak, 0.4 of the peak
		quiet  = 30.0   // below Off*peak
	)
	levels := []float64{loud, voiced, quiet}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 400; i++ {
		seq := []float64{loud}
		for n := rng.Intn(31); n > 0; n-- {
			seq = append(seq, levels[rng.Intn(len(levels))])
		}
		parts := make([][]int16, len(seq))
		for j, r := range seq {
			parts[j] = tone(1, r)
		}
		d := New(Default)
		ended := feed(d, parts...)
		wantEnded, wantAt := endsAt(seq, Default)
		if ended != wantEnded || d.EndedAt() != wantAt {
			t.Fatalf("%v: ended %v at %d, want %v at %d", seq, ended, d.EndedAt(), wantEnded, wantAt)
		}
	}
}

// endsAt is the invariant over what was fed, stated without the detector: a window is speech at or
// above On*peak and quiet below Off*peak; the turn ends at Quiet quiet windows in a row once Speech
// has counted; and EndedAt is the end of the window just before that run.
func endsAt(seq []float64, s Settings) (ended bool, at int) {
	peak, speech, run := 0.0, 0, 0
	for i, r := range seq {
		peak = math.Max(peak, r)
		if r >= s.On*peak {
			speech++
		}
		if r < s.Off*peak {
			run++
		} else {
			run = 0
		}
		if run == s.Quiet && speech >= s.Speech {
			return true, (i + 1 - s.Quiet) * Window
		}
	}
	return false, 0
}

// Recordings of real turns, when there are some: where the detector would have ended each one, and
// the audio up to there written out for speech recognition to be run on. Only with ENDPOINT_CLIPS set
// to a folder of 16 kHz mono WAVs; ENDPOINT_OUT is where the cut copies go, and ENDPOINT_SETTINGS
// overrides the thresholds as "on,off,quiet,speech". The recordings are the household talking, so
// they live outside the repository and nothing here keeps them.
func TestOnRecordings(t *testing.T) {
	dir := os.Getenv("ENDPOINT_CLIPS")
	if dir == "" {
		t.Skip("ENDPOINT_CLIPS not set")
	}
	s := Default
	if v := os.Getenv("ENDPOINT_SETTINGS"); v != "" {
		f := strings.Split(v, ",")
		s.On, _ = strconv.ParseFloat(f[0], 64)
		s.Off, _ = strconv.ParseFloat(f[1], 64)
		s.Quiet, _ = strconv.Atoi(f[2])
		s.Speech, _ = strconv.Atoi(f[3])
	}
	out := os.Getenv("ENDPOINT_OUT")
	if out != "" {
		if err := os.MkdirAll(out, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	for _, p := range paths {
		pcm, err := readWAV(p)
		if err != nil {
			t.Logf("%s: %v", filepath.Base(p), err)
			continue
		}
		d := New(s)
		d.Feed(pcm)
		end := d.EndedAt()
		keep := pcm
		if end > 0 {
			// What the pipeline gets: everything sent until the detector decided, which is the quiet
			// run after the last of the speech as well as the speech.
			keep = pcm[:min(len(pcm), end+s.Quiet*Window)]
		}
		fmt.Printf("%s %.1f s -> %s\n", filepath.Base(p), float64(len(pcm))/16000, endText(end))
		if out != "" {
			if err := writeWAV(filepath.Join(out, filepath.Base(p)), keep); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func endText(end int) string {
	if end == 0 {
		return "no end"
	}
	return fmt.Sprintf("ends at %.1f s", float64(end)/16000)
}

func readWAV(path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	i := strings.Index(string(b), "data")
	if i < 0 || i+8 > len(b) {
		return nil, fmt.Errorf("no data chunk")
	}
	body := b[i+8:]
	out := make([]int16, len(body)/2)
	for j := range out {
		out[j] = int16(binary.LittleEndian.Uint16(body[2*j:]))
	}
	return out, nil
}

func writeWAV(path string, pcm []int16) error {
	pad := make([]int16, 16000) // trailing silence, as a pipeline's end of audio leaves
	pcm = append(append([]int16(nil), pcm...), pad...)
	b := make([]byte, 44+2*len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return os.WriteFile(path, b, 0o600)
}
