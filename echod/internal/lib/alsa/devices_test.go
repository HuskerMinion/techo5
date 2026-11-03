package alsa

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindPCMEnumeration(t *testing.T) {
	root := t.TempDir()
	for name, id := range map[string]string{"card0": "other", "card2": "mt8163cronos"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "id"), []byte(id+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	pcm := filepath.Join(root, "pcm")
	const list = "00-06: Microphones wrong-card : : capture 1\n02-07: Microphones tlv-codec : : capture 1\n02-04: MultiMedia1_Playback (*) : : playback 1\n02-09: MicrophonesExtra (*) : : capture 1\n"
	if err := os.WriteFile(pcm, []byte(list), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		capture bool
		want    int
	}{{"Microphones", true, 7}, {"MultiMedia1_Playback", false, 4}} {
		c, d, found, err := findPCM(root, pcm, "mt8163cronos", tc.name, tc.capture)
		if err != nil || !found || c != 2 || d != tc.want {
			t.Fatalf("%s: got %d %d %v %v", tc.name, c, d, found, err)
		}
	}
	_, _, found, err := findPCM(root, pcm, "absent", "Microphones", true)
	if found || err != nil {
		t.Fatalf("absent card: %v %v", found, err)
	}
	_, _, found, err = findPCM(root, pcm, "mt8163cronos", "Microphones", false)
	if !found || err == nil {
		t.Fatalf("wrong direction: %v %v", found, err)
	}
	_, _, found, err = findPCM(root, pcm, "mt8163cronos", "Missing", true)
	if !found || err == nil {
		t.Fatalf("missing stream: %v %v", found, err)
	}
}

// A board whose codec is its second card, after an HDMI output (the Surface RT's WM8962): found by its
// id, whatever its number. No id, or an id no card has, is card 0.
func TestCardByID(t *testing.T) {
	root := t.TempDir()
	for name, id := range map[string]string{"card0": "tegrahda", "card1": "WM8962"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "id"), []byte(id+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]int{"WM8962": 1, "tegrahda": 0, "": 0, "absent": 0} {
		if got := cardByID(root, id); got != want {
			t.Errorf("card %q is %d, want %d", id, got, want)
		}
	}
}
