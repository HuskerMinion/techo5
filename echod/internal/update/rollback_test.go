package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// store builds a slot store in a temporary directory: each slot's state file, when not "", and its
// etc/techo5-release, when not "".
func store(t *testing.T, slots map[string][2]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, s := range slots {
		state, release := s[0], s[1]
		if state != "" {
			if err := os.MkdirAll(filepath.Join(dir, "slots"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "slots", name+".state"), []byte(state+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if release != "" {
			etc := filepath.Join(dir, "slots", name, "etc")
			if err := os.MkdirAll(etc, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(etc, "techo5-release"), []byte(release+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

const release40 = "techo5 rootfs v1.6.40 built 2026-11-10T09:12Z, daemon techo5 v1.6.40"

// A rollback goes ahead only to a slot that committed itself, and says which release that is.
func TestRollBackToAGoodSlot(t *testing.T) {
	dir := store(t, map[string][2]string{
		"a": {"good", "techo5 rootfs v1.6.41 built 2026-11-15T10:00Z, daemon techo5 v1.6.41"},
		"b": {"good", release40},
	})
	other := otherSlotIn(dir, "a")
	if other != (Slot{Name: "b", State: "good", Version: "v1.6.40"}) {
		t.Fatalf("other slot = %+v", other)
	}
	if err := canRollBack("a", other); err != nil {
		t.Fatalf("rolling back to a good slot refused: %v", err)
	}
	if got := other.String(); got != "v1.6.40 (good)" {
		t.Errorf("sensor = %q", got)
	}
	// And the other way round.
	if other := otherSlotIn(dir, "b"); other.Name != "a" || other.Version != "v1.6.41" {
		t.Errorf("other slot from b = %+v", other)
	}
}

// Every case where the other slot is not good is refused, with a reason that says so, before
// slotctl is run: a stray press must not leave the device without a system that works.
func TestRollBackRefusesUnlessTheOtherSlotIsGood(t *testing.T) {
	cases := []struct {
		why    string
		booted string
		slots  map[string][2]string
		want   string
	}{
		{"the other slot was never installed", "a",
			map[string][2]string{"a": {"good", release40}}, "never been installed"},
		{"the other slot is empty but has a directory", "a",
			map[string][2]string{"a": {"good", release40}, "b": {"", release40}}, "never been installed"},
		{"the other slot is bad", "a",
			map[string][2]string{"a": {"good", release40}, "b": {"bad", release40}}, "marked bad"},
		{"the other slot is on trial", "b",
			map[string][2]string{"a": {"trial 2", release40}, "b": {"good", release40}}, "on trial"},
		{"the other slot ran out of tries", "b",
			map[string][2]string{"a": {"trial 0", release40}, "b": {"good", release40}}, "on trial"},
		{"the other slot's state is not one slotctl writes", "a",
			map[string][2]string{"a": {"good", release40}, "b": {"weird", release40}}, "not good"},
		{"not booted from a slot", "",
			map[string][2]string{"a": {"good", release40}, "b": {"good", release40}}, "did not boot from a slot"},
		{"a booted slot name that is not a or b", "c",
			map[string][2]string{"a": {"good", release40}, "b": {"good", release40}}, "did not boot from a slot"},
	}
	for _, c := range cases {
		dir := store(t, c.slots)
		err := canRollBack(c.booted, otherSlotIn(dir, c.booted))
		if err == nil {
			t.Errorf("%s: rollback allowed", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want it to say %q", c.why, err, c.want)
		}
	}
}

func TestOtherSlotSensor(t *testing.T) {
	cases := []struct {
		slot Slot
		want string
	}{
		{Slot{Name: "b", State: "none"}, "empty"},
		{Slot{Name: "b", State: "bad", Version: "v1.6.39"}, "v1.6.39 (bad)"},
		{Slot{Name: "b", State: "trial 2", Version: "v1.6.42"}, "v1.6.42 (trial 2)"},
		{Slot{Name: "b", State: "good"}, "unknown version (good)"},
	}
	for _, c := range cases {
		if got := c.slot.String(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.slot, got, c.want)
		}
	}
	// A release file in another form is shown as it is.
	if got := releaseVersion("built by hand\nsecond line"); got != "built by hand" {
		t.Errorf("releaseVersion = %q", got)
	}
}

// The release rolled back from, and anything no newer, is held back from installing by itself; a
// newer one is not.
func TestHeldBack(t *testing.T) {
	cases := []struct {
		offered, held string
		want          bool
		why           string
	}{
		{"v1.6.41", "v1.6.41", true, "the release rolled back from"},
		{"v1.6.40", "v1.6.41", true, "an older one"},
		{"v1.6.42", "v1.6.41", false, "a newer one is out"},
		{"v1.7.0-beta.1", "v1.6.41", false, "a newer prerelease"},
		{"v1.6.41", "", false, "nothing rolled back"},
		{"", "v1.6.41", false, "nothing offered"},
		{"dev-build", "dev-build", true, "an unrankable build, the same one"},
		{"dev-build-2", "dev-build", false, "an unrankable build, another one"},
	}
	for _, c := range cases {
		if got := HeldBack(c.offered, c.held); got != c.want {
			t.Errorf("%s: HeldBack(%q, %q) = %v, want %v", c.why, c.offered, c.held, got, c.want)
		}
	}
}
