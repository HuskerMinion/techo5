package led

import (
	"os"
	"path/filepath"
	"testing"
)

// The Echo Input's kernel animates its LED from boot until it is told to stop, through the LP5562's
// bootanimation beside the LED class devices (#641). Turning it off writes 0 there; a kernel without
// the attribute is left alone, and nothing is made in its place.
func TestTheEchoInputsBootAnimationIsStopped(t *testing.T) {
	chip := t.TempDir()
	leds := filepath.Join(chip, "leds")
	for _, ch := range singleChannels {
		if err := os.MkdirAll(filepath.Join(leds, ch), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(leds, ch, "brightness"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, ok := newSingle(leds)
	if !ok {
		t.Fatal("no single LED")
	}
	attr := filepath.Join(chip, "bootanimation")

	if err := r.SetBootAnimation(false); err != nil {
		t.Fatal("with no bootanimation:", err)
	}
	if _, err := os.Stat(attr); err == nil {
		t.Fatal("a bootanimation was made where the kernel had none")
	}

	if err := os.WriteFile(attr, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.SetBootAnimation(false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(attr); string(b) != "0" {
		t.Errorf("bootanimation is %q, not 0", b)
	}
}
