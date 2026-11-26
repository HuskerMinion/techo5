package led

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// classRing makes the Echo Dot 5th gen's twelve is31fl3246 modules under a temporary directory, as the
// kernel lists them: color, brightness, max_brightness and trigger each.
func classRing(t *testing.T) (string, *Ring) {
	t.Helper()
	// The kernel's names have a colon (is31fl3246:ledmod_N), which not every test host allows in a path.
	prefix := filepath.Join(t.TempDir(), "is31fl3246-ledmod_")
	for n := range Segments {
		dir := prefix + strconv.Itoa(n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, v := range map[string]string{"color": "0 0 0\n", "brightness": "0\n", "max_brightness": "255\n",
			"trigger": "none [boot-anim]\n"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, ok := newClassRing(prefix)
	if !ok {
		t.Fatal("no class ring")
	}
	return prefix, r
}

func readAttr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A frame goes to the modules as "R G B", segment n to ledmod_n, with their brightness put at full first;
// a segment that has not changed is not written again.
func TestTheDot5RingTakesEachSegmentsColor(t *testing.T) {
	prefix, r := classRing(t)
	if !r.Present() || !r.Class() || r.Single() {
		t.Fatal("the ring is not taken for twelve class devices")
	}

	frame := make([]byte, Channels)
	frame[0], frame[1], frame[2] = 10, 20, 30
	frame[33], frame[34], frame[35] = 1, 2, 3
	if err := r.SetFrame(frame); err != nil {
		t.Fatal(err)
	}
	if got := readAttr(t, prefix+"0/color"); got != "10 20 30" {
		t.Errorf("ledmod_0 color %q", got)
	}
	if got := readAttr(t, prefix+"11/color"); got != "1 2 3" {
		t.Errorf("ledmod_11 color %q", got)
	}
	if got := readAttr(t, prefix+"5/brightness"); got != "255" {
		t.Errorf("ledmod_5 brightness %q, not its max_brightness", got)
	}

	// Removing a module's color makes a second write detectable.
	if err := os.Remove(prefix + "11/color"); err != nil {
		t.Fatal(err)
	}
	frame[0] = 99
	if err := r.SetFrame(frame); err != nil {
		t.Fatal(err)
	}
	if got := readAttr(t, prefix+"0/color"); got != "99 20 30" {
		t.Errorf("ledmod_0 color %q after a change", got)
	}
	if _, err := os.Stat(prefix + "11/color"); !os.IsNotExist(err) {
		t.Error("an unchanged segment was written again")
	}

	got, err := r.Frame()
	if err != nil || got[0] != 99 || got[35] != 3 {
		t.Errorf("Frame() = %v, %v", got, err)
	}
	if r.hasCurrent() {
		t.Error("the class ring reported the is31fl3236's one current setting")
	}
}

// Stopping the kernel's boot animation takes every module's trigger off and puts the brightness the
// kernel then zeroes back at full, and the next frame is written whole.
func TestTheDot5RingsBootAnimationIsStopped(t *testing.T) {
	prefix, r := classRing(t)
	if err := r.SetFrame(make([]byte, Channels)); err != nil {
		t.Fatal(err)
	}
	for n := range Segments {
		if err := os.WriteFile(prefix+strconv.Itoa(n)+"/brightness", []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetBootAnimation(false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0", "11"} {
		if got := readAttr(t, prefix+n+"/trigger"); got != "none" {
			t.Errorf("ledmod_%s trigger %q", n, got)
		}
		if got := readAttr(t, prefix+n+"/brightness"); got != "255" {
			t.Errorf("ledmod_%s brightness %q", n, got)
		}
	}
	if err := os.Remove(prefix + "7/color"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetFrame(make([]byte, Channels)); err != nil {
		t.Fatal(err)
	}
	if got := readAttr(t, prefix+"7/color"); got != "0 0 0" {
		t.Errorf("the frame after the animation was not written whole: ledmod_7 %q", got)
	}
}

// Eleven modules are not the ring.
func TestAnIncompleteClassRingIsNotTaken(t *testing.T) {
	prefix, _ := classRing(t)
	if err := os.Remove(prefix + "11/color"); err != nil {
		t.Fatal(err)
	}
	if _, ok := newClassRing(prefix); ok {
		t.Error("a ring with a module missing was taken")
	}
}
