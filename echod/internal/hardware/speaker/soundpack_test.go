package speaker

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	zw.Close()
	return buf.Bytes()
}

const goodManifest = `{"format":"techo5-soundpack","version":1,"sounds":[]}`

// The quick sounds take their own recording, at two seconds at most, and go back to the notes without.
func TestQuickSoundsOwnAndFallBack(t *testing.T) {
	ownSounds(t)
	for name, notes := range map[string]func() []Note{
		"volume_change": VolumeSound, "button_press": ButtonSound,
		"bluetooth_connected": BTConnectSound, "drop_in": DropInSound, "listening_ended": ListenEndSound,
	} {
		before := notes()
		if err := SaveOwn(name, bytes.NewReader(wavFile(t, Rate, 1, 3000))); err == nil || !strings.Contains(err.Error(), "2 seconds") {
			t.Errorf("%s: three seconds was kept: %v", name, err)
		}
		if err := SaveOwn(name, bytes.NewReader(wavFile(t, 44100, 1, 100))); err == nil {
			t.Errorf("%s: wrong rate was kept", name)
		}
		if err := SaveOwn(name, bytes.NewReader(wavFile(t, Rate, 1, 1500))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		got := notes()
		if len(got) != 1 || got[0].Clip == nil || got[0].Ms != 1500 {
			t.Errorf("%s: plays %v", name, got)
		}
		if err := RemoveOwn(name); err != nil {
			t.Fatal(err)
		}
		if after := notes(); len(after) != len(before) || (len(after) > 0 && after[0].Clip != nil) {
			t.Errorf("%s: after removing, plays %v, was %v", name, after, before)
		}
	}
	// The long ones keep their ten seconds.
	if err := SaveOwn("alarm", bytes.NewReader(wavFile(t, Rate, 1, 5000))); err != nil {
		t.Errorf("alarm at five seconds: %v", err)
	}
}

func TestSoundPackRoundTrip(t *testing.T) {
	dir := ownSounds(t)
	var empty bytes.Buffer
	if err := PackSounds(&empty); err == nil {
		t.Error("a pack with no sounds was made")
	}
	a, b := wavFile(t, Rate, 2, 300), wavFile(t, Rate, 1, 900)
	SaveOwn("volume_change", bytes.NewReader(a))
	SaveOwn("alarm", bytes.NewReader(b))
	var pack bytes.Buffer
	if err := PackSounds(&pack); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"volume_change", "alarm"} {
		RemoveOwn(n)
	}
	names, err := LoadPack(pack.Bytes())
	if err != nil || len(names) != 2 {
		t.Fatalf("load: %v %v", names, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "volume_change.wav")); !bytes.Equal(got, a) {
		t.Error("volume_change did not come back as it went")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "alarm.wav")); !bytes.Equal(got, b) {
		t.Error("alarm did not come back as it went")
	}
}

func TestSoundPackRefusals(t *testing.T) {
	dir := ownSounds(t)
	ok := wavFile(t, Rate, 1, 200)
	m := []byte(goodManifest)
	for what, files := range map[string]map[string][]byte{
		"path out":      {"manifest.json": m, "../alarm.wav": ok},
		"nested":        {"manifest.json": m, "sub/alarm.wav": ok},
		"absolute":      {"manifest.json": m, "/etc/alarm.wav": ok},
		"wrong type":    {"manifest.json": m, "alarm.wav": ok, "settings.json": []byte("{}")},
		"unknown sound": {"manifest.json": m, "doorbell.wav": ok},
		"no manifest":   {"alarm.wav": ok},
		"bad manifest":  {"manifest.json": []byte(`{"format":"other","version":1}`), "alarm.wav": ok},
		"too long":      {"manifest.json": m, "alarm.wav": ok, "volume_change.wav": wavFile(t, Rate, 1, 3000)},
		"not a sound":   {"manifest.json": m, "alarm.wav": []byte("hello")},
		"too big":       {"manifest.json": m, "alarm.wav": append(wavFile(t, Rate, 1, 100), make([]byte, MaxOwnUpload)...)},
		"no sounds":     {"manifest.json": m},
	} {
		if names, err := LoadPack(packOf(t, files)); err == nil {
			t.Errorf("%s: loaded %v", what, names)
		}
	}
	if _, err := LoadPack([]byte("not a zip")); err == nil {
		t.Error("garbage loaded")
	}
	if _, err := LoadPack(make([]byte, MaxPack+1)); err == nil {
		t.Error("an oversize pack loaded")
	}
	// Nothing from a refused pack stays, even the sounds ahead of the bad one.
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused pack left %d files", len(entries))
	}
}
