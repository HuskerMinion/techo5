package led

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetFrameSkipsIdenticalWrites(t *testing.T) {
	dir := t.TempDir()
	r := &Ring{Path: dir}
	frame := filepath.Join(dir, "frame")

	first := make([]byte, Channels)
	first[0] = 0x10
	if err := r.SetFrame(first); err != nil {
		t.Fatal(err)
	}

	// Removing the file makes a second write detectable: if SetFrame wrote again it would recreate it.
	if err := os.Remove(frame); err != nil {
		t.Fatal(err)
	}
	if err := r.SetFrame(first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(frame); !os.IsNotExist(err) {
		t.Fatal("an identical frame was written again")
	}

	changed := make([]byte, Channels)
	changed[0] = 0x11
	if err := r.SetFrame(changed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(frame); err != nil {
		t.Fatal("a changed frame was not written:", err)
	}

	// The caller's slice must not be aliased: reusing a buffer is exactly what an effect does.
	changed[0] = 0x12
	if err := os.Remove(frame); err != nil {
		t.Fatal(err)
	}
	if err := r.SetFrame(changed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(frame); err != nil {
		t.Fatal("a frame differing only from a reused buffer was skipped:", err)
	}
}

func TestForgetForcesTheNextWrite(t *testing.T) {
	dir := t.TempDir()
	r := &Ring{Path: dir}
	frame := filepath.Join(dir, "frame")

	vals := make([]byte, Channels)
	if err := r.SetFrame(vals); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(frame); err != nil {
		t.Fatal(err)
	}

	r.Forget()
	if err := r.SetFrame(vals); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(frame); err != nil {
		t.Fatal("Forget did not force the next write:", err)
	}
}

// singleLED makes an LP5562 as the Echo Input's kernel serves it: R, G and B, each with brightness and
// max_brightness.
func singleLED(t *testing.T, max string) string {
	t.Helper()
	dir := t.TempDir()
	for _, ch := range singleChannels {
		if err := os.MkdirAll(filepath.Join(dir, ch), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, v := range map[string]string{"brightness": "0", "max_brightness": max} {
			if err := os.WriteFile(filepath.Join(dir, ch, name), []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func readBrightness(t *testing.T, dir string) [3]string {
	t.Helper()
	var got [3]string
	for i, ch := range singleChannels {
		b, err := os.ReadFile(filepath.Join(dir, ch, "brightness"))
		if err != nil {
			t.Fatal(err)
		}
		got[i] = string(b)
	}
	return got
}

// On the Echo Input the twelve segments show as one LED, each channel at the brightest segment's, scaled
// to the channel's max_brightness; the frame read back is the twelve drawn.
func TestOneLEDShowsTheRingMixed(t *testing.T) {
	dir := singleLED(t, "255")
	r, ok := newSingle(dir)
	if !ok || !r.Single() || !r.Present() {
		t.Fatal("the LP5562's channels were not taken for a single LED")
	}
	segs := make([]Color, Segments)
	segs[3] = Color{R: 0, G: 40, B: 200}
	segs[7] = Color{R: 10, G: 180, B: 90}
	if err := r.SetSegments(segs); err != nil {
		t.Fatal(err)
	}
	if got, want := readBrightness(t, dir), [3]string{"10", "180", "200"}; got != want {
		t.Errorf("brightness %v, want %v", got, want)
	}
	frame, err := r.Frame()
	if err != nil || len(frame) != Channels || frame[3*3+2] != 200 {
		t.Errorf("Frame() = %v, %v; want the twelve segments drawn", frame, err)
	}
	// Neither of the ring driver's other files exists on an LED class device: nothing is written to them.
	if err := r.SetCurrent(100); err != nil {
		t.Errorf("SetCurrent: %v", err)
	}
	if err := r.SetBootAnimation(true); err != nil {
		t.Errorf("SetBootAnimation: %v", err)
	}
}

func TestOneLEDScalesToItsMaximum(t *testing.T) {
	dir := singleLED(t, "100")
	r, ok := newSingle(dir)
	if !ok {
		t.Fatal("no single LED")
	}
	if err := r.SetAll(Color{R: 255, G: 51, B: 0}); err != nil {
		t.Fatal(err)
	}
	if got, want := readBrightness(t, dir), [3]string{"100", "20", "0"}; got != want {
		t.Errorf("brightness %v, want %v", got, want)
	}
}

// A directory without all three channels is not the Echo Input's LED.
func TestNoSingleLEDWithoutItsChannels(t *testing.T) {
	dir := singleLED(t, "255")
	if err := os.RemoveAll(filepath.Join(dir, "B")); err != nil {
		t.Fatal(err)
	}
	if _, ok := newSingle(dir); ok {
		t.Error("two channels were taken for the Echo Input's LED")
	}
}
