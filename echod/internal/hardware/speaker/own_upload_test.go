package speaker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wavFile is a WAVE file's bytes, as writeWAV makes one.
func wavFile(t *testing.T, rate, channels, ms int) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.wav")
	writeWAV(t, path, rate, channels, ms)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A recording sent from the setup page is kept only when the player would play it, under the name of
// the sound it is for and nowhere else, and taking it away brings the stock sound back.
func TestARecordingFromTheSetupPage(t *testing.T) {
	dir := ownSounds(t)

	good := wavFile(t, Rate, 2, 300)
	if err := SaveOwn("mute_switch_on", bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	if has, problem := OwnState("mute_switch_on"); !has || problem != "" {
		t.Errorf("after saving: has %v, problem %q", has, problem)
	}
	if !ClipMuteOn.Own() || ClipMuteOn.Ms() != 300 {
		t.Errorf("the clip plays %d ms, own %v", ClipMuteOn.Ms(), ClipMuteOn.Own())
	}

	// What the player would refuse is refused here, with a reason, and the file before it stays.
	for what, b := range map[string][]byte{
		"another rate":   wavFile(t, 44100, 2, 300),
		"too long":       wavFile(t, Rate, 1, 10500),
		"no sound":       wavFile(t, Rate, 1, 0),
		"not a WAV file": []byte("ID3 this is an mp3, or says it is"),
		"too big":        append(wavFile(t, Rate, 2, 100), make([]byte, MaxOwnUpload)...),
	} {
		err := SaveOwn("mute_switch_on", bytes.NewReader(b))
		if err == nil {
			t.Errorf("%s was kept", what)
			continue
		}
		if !strings.Contains(err.Error(), "48 kHz") && !strings.Contains(err.Error(), "bigger") {
			t.Errorf("%s: the reason does not say what is needed: %v", what, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mute_switch_on.wav")); !bytes.Equal(b, good) {
		t.Error("a refused file replaced the one before it")
	}

	// A name is one of the sounds or nothing: no path is made from what was sent.
	for _, name := range []string{"", "../mute_switch_on", "mute_switch_on/../../x", "/etc/passwd", "alarm_clock", "mute_switch_on.wav"} {
		if err := SaveOwn(name, bytes.NewReader(good)); err == nil {
			t.Errorf("%q was taken as a sound", name)
		}
		if err := RemoveOwn(name); err == nil {
			t.Errorf("%q was removed as a sound", name)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the folder holds %d files, want only the one recording", len(entries))
	}

	if err := RemoveOwn("mute_switch_on"); err != nil {
		t.Fatal(err)
	}
	if has, _ := OwnState("mute_switch_on"); has || ClipMuteOn.Own() {
		t.Error("the recording is still there after removing it")
	}
	if err := RemoveOwn("mute_switch_on"); err != nil {
		t.Errorf("removing one already gone: %v", err)
	}
}

// A file copied on by hand that cannot play is listed as there, with the reason, rather than as
// playing.
func TestARecordingThatCannotPlaySaysWhy(t *testing.T) {
	dir := ownSounds(t)
	writeWAV(t, filepath.Join(dir, "failure.wav"), 22050, 1, 200)
	has, problem := OwnState("failure")
	if !has || !strings.Contains(problem, "22050") {
		t.Errorf("has %v, problem %q", has, problem)
	}
	if len(OwnSounds()) != 7 || OwnSounds()[0] != "wake_word_triggered" {
		t.Errorf("sounds %v", OwnSounds())
	}
}

// What an upload a power cut stopped left in the folder is deleted at the next start, and nothing else
// there is touched.
func TestSweepUploadsDeletesOnlyUnfinishedUploads(t *testing.T) {
	dir := ownSounds(t)
	for _, name := range []string{".upload-123", ".upload-x", "failure.wav", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	SweepUploads()
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, " ") != "failure.wav notes.txt" {
		t.Errorf("left %v", left)
	}
}
