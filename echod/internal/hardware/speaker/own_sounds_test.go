package speaker

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// ownSounds points SoundsDir at a folder of the test's own for as long as the test runs.
func ownSounds(t *testing.T) string {
	t.Helper()
	was := SoundsDir
	SoundsDir = t.TempDir()
	t.Cleanup(func() { SoundsDir = was })
	return SoundsDir
}

// writeWAV writes ms of a constant level as a 16-bit WAVE file at rate, with channels channels.
func writeWAV(t *testing.T, path string, rate, channels, ms int) {
	t.Helper()
	frames := rate * ms / 1000
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+frames*channels*2))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(frames*channels*2))
	for range frames * channels {
		_ = binary.Write(&b, binary.LittleEndian, int16(8000))
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A recording of your own named for a sound plays in its place, at its own length, and taking it
// away brings the stock sound back without a restart.
func TestARecordingOfYourOwnReplacesTheSound(t *testing.T) {
	dir := ownSounds(t)
	c := &Clip{file: "mute_switch_on"}
	stock := c.Ms()

	own := filepath.Join(dir, "mute_switch_on.wav")
	writeWAV(t, own, Rate, 2, 250)
	if c.Ms() != 250 || !c.Own() {
		t.Fatalf("with a recording of its own it lasts %d ms, own %v", c.Ms(), c.Own())
	}
	if n := len(tone(c.Note(), toneLevel)); n != Rate/4*Channels {
		t.Errorf("rendered %d samples, want %d", n, Rate/4*Channels)
	}
	if got := Length([]Note{c.Note()}); got.Milliseconds() != 250 {
		t.Errorf("its length is %v", got)
	}

	if err := os.Remove(own); err != nil {
		t.Fatal(err)
	}
	if c.Ms() != stock || c.Own() {
		t.Errorf("with the recording gone it lasts %d ms, want the stock %d", c.Ms(), stock)
	}
}

// A file the device cannot play as it is leaves the stock sound playing: a recording at the wrong rate
// played anyway is the right sound at the wrong speed.
func TestARecordingAtTheWrongRateIsNotPlayed(t *testing.T) {
	dir := ownSounds(t)
	c := &Clip{file: "mute_switch_off"}
	stock := c.Ms()
	writeWAV(t, filepath.Join(dir, "mute_switch_off.wav"), 44100, 2, 250)
	if c.Own() || c.Ms() != stock {
		t.Errorf("a 44.1 kHz recording was taken: own %v, %d ms against the stock %d", c.Own(), c.Ms(), stock)
	}
}

// Failure and cancel have no recording of their own to begin with, so they keep their notes until
// somebody gives them one.
func TestFailureAndCancelTakeARecording(t *testing.T) {
	dir := ownSounds(t)
	if len(FailureSound()) != len(ToneTrouble) || len(CancelSound()) != len(ToneCancel) {
		t.Fatal("with no recordings, failure and cancel are not their notes")
	}
	writeWAV(t, filepath.Join(dir, "failure.wav"), Rate, 1, 400)
	writeWAV(t, filepath.Join(dir, "canceled.wav"), Rate, 1, 300)
	if got := Length(FailureSound()); got.Milliseconds() != 400 {
		t.Errorf("failure lasts %v with a recording of its own", got)
	}
	if got := Length(CancelSound()); got.Milliseconds() != 300 {
		t.Errorf("cancel lasts %v with a recording of its own", got)
	}
}
