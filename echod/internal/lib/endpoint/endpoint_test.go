package endpoint

import (
	"encoding/binary"
	"fmt"
	"math"
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
