package firstrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// fresh is a test's own config and state directory.
func fresh(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	was, wasHass := stateDir, hass.Path
	stateDir, hass.Path = dir, filepath.Join(dir, "hass.json")
	t.Cleanup(func() { stateDir, hass.Path = was, wasHass })
	return dir
}

// A device just installed has first run to do; one already in use (told Home Assistant a version, or
// a Show whose welcome card was put away) is done, and never sees it.
func TestSettleTellsANewDeviceFromOneInUse(t *testing.T) {
	cases := []struct {
		name    string
		set     func()
		pending bool
	}{
		{"new", func() {}, true},
		{"in Home Assistant", func() { _ = config.Set().Update().LastVersion("v1.6.29") }, false},
		{"welcomed Show", func() { _ = config.Set().Screen().Welcomed(true) }, false},
	}
	for _, c := range cases {
		fresh(t)
		if Pending() {
			t.Fatalf("%s: pending before the look", c.name)
		}
		c.set()
		Settle()
		if Pending() != c.pending {
			t.Errorf("%s: pending %v, want %v", c.name, Pending(), c.pending)
		}
		// The look is once: Home Assistant arriving later does not end first run by itself.
		_ = config.Set().Update().LastVersion("v1.6.30")
		Settle()
		if Pending() != c.pending {
			t.Errorf("%s: a second look changed it", c.name)
		}
	}
}

// The marker a gift install leaves means first run, even on a device that was in use; finishing
// removes it.
func TestTheGiftMarkerMeansFirstRun(t *testing.T) {
	dir := fresh(t)
	// What a gift install leaves in place: an earlier first run finished, welcomed, a room, a step.
	_ = config.Set().Update().LastVersion("v1.6.29")
	_ = config.Set().FirstRun().Settle(true)
	_ = config.Set().FirstRun().Step(Sound)
	_ = config.Set().Screen().Welcomed(true)
	_ = config.Set().Home().Area("Kitchen")
	// And the giver's own files, which the first start wipes as a reset would.
	for _, f := range []string{"hass.json", "phone.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "firstrun"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	Settle()
	if !Pending() || Step() != Hello || config.Get().Home.Area != "" {
		t.Fatalf("with the marker: pending %v, step %q, room %q", Pending(), Step(), config.Get().Home.Area)
	}
	for _, f := range []string{"hass.json", "phone.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("a gift install kept the giver's %s", f)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "phone.json"), []byte("{}"), 0o600) // set up during first run
	Go(Name)
	Settle() // a restart partway through keeps its step, and wipes nothing again
	if Step() != Name {
		t.Fatalf("a restart moved first run to %q", Step())
	}
	if _, err := os.Stat(filepath.Join(dir, "phone.json")); err != nil {
		t.Error("a restart during first run wiped again")
	}
	Finish()
	if Pending() {
		t.Fatal("finishing left first run pending")
	}
	if _, err := os.Stat(filepath.Join(dir, "firstrun")); !os.IsNotExist(err) {
		t.Fatalf("finishing left the marker: %v", err)
	}
}

func TestStepsInOrder(t *testing.T) {
	fresh(t)
	got := []string{}
	for s := Hello; ; s = After(s) {
		got = append(got, s)
		if s == Done {
			break
		}
	}
	if len(got) != len(Steps()) {
		t.Fatalf("walked %v, steps %v", got, Steps())
	}
	if n, of := Number(Steps()[1]); n != 1 || of != len(Steps())-2 {
		t.Errorf("the first counted step is %d of %d", n, of)
	}
	if n, _ := Number(Hello); n != 0 {
		t.Errorf("hello is counted: %d", n)
	}
}

// Reset clears the old home's things, keeps the device's own, and leaves first run to do.
func TestResetClearsForSomeoneElse(t *testing.T) {
	dir := fresh(t)
	restarted := make(chan struct{}, 1)
	was := restart
	restart = func() { restarted <- struct{}{} }
	t.Cleanup(func() { restart = was })

	for _, f := range []string{"psk", "name", "phone.json", "phone-contacts.json", "hass.json", "photos/a.jpg", "ssh/authorized_keys", "models/okay_nabu.tflite", "mac"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = config.Set().Home().Area("Kitchen")
	_ = config.Set().Home().HouseWord("pancake")
	_ = config.Set().Update().AutoInstall(true)
	_ = config.Set().FirstRun().Finish()

	if err := Reset(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("it did not restart")
	}
	// Nothing is wiped before the restart, and a feature writing its state on the way down is too
	// late: the next start wipes it all before anything runs.
	if _, err := os.Stat(filepath.Join(dir, "psk")); err != nil {
		t.Fatal("the key went before the restart")
	}
	_ = config.Set().Home().HouseWord("written on the way down")
	Settle()
	if _, err := os.Stat(filepath.Join(dir, "reset")); !os.IsNotExist(err) {
		t.Errorf("the reset marker is still there: %v", err)
	}
	for _, gone := range []string{"psk", "name", "phone.json", "phone-contacts.json", "hass.json", "photos", "ssh"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{"models/okay_nabu.tflite", "mac", "firstrun"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s went: %v", kept, err)
		}
	}
	c := config.Get()
	if c.Home.Area != "" || c.Home.HouseWord != "" || c.Update.AutoInstall || !Pending() {
		t.Errorf("settings after the reset: area %q, word %q, auto %v, pending %v", c.Home.Area, c.Home.HouseWord, c.Update.AutoInstall, Pending())
	}
	if Step() != Hello {
		t.Errorf("first run after the reset is on %q", Step())
	}
}

// A device with no screen, installed the usual way or for someone else, is set up once Home Assistant
// has it with a key; a screen keeps its own first run.
func TestHomeAssistantFinishesFirstRunWithNoScreen(t *testing.T) {
	dir := fresh(t)
	was, hc, k := screenless, HomeConnected, Key
	t.Cleanup(func() { screenless, HomeConnected, Key = was, hc, k })
	home, key := false, "a key"
	HomeConnected = func() bool { return home }
	Key = func() string { return key }
	Settle()

	screenless = false
	home = true
	_ = os.WriteFile(filepath.Join(dir, "name"), []byte("Kitchen Dot\n"), 0o600)
	if doneByHome() || !Pending() {
		t.Fatal("a screen's first run was finished by Home Assistant")
	}
	screenless = true
	home = false
	if doneByHome() {
		t.Fatal("finished with nothing connected")
	}
	home, key = true, ""
	if doneByHome() {
		t.Fatal("finished on a Home Assistant with no key")
	}
	key = "a key"
	_ = os.Remove(filepath.Join(dir, "name")) // a gift install: the name from its MAC
	if !doneByHome() || Pending() {
		t.Fatal("Home Assistant with a key did not finish it")
	}
}

// Both buttons down for ten seconds is the reset; one alone, or both for less, is not.
func TestTheButtonsHeldTogether(t *testing.T) {
	now := time.Now()
	down := map[buttons.Name]time.Time{}
	was := pressed
	pressed = func(n buttons.Name) (bool, time.Time) { at, ok := down[n]; return ok, at }
	t.Cleanup(func() { pressed = was })

	down[buttons.Action] = now.Add(-20 * time.Second)
	if comboHeld(now) {
		t.Error("the action button alone reset it")
	}
	down[buttons.VolumeDown] = now.Add(-5 * time.Second)
	if comboHeld(now) {
		t.Error("five seconds of both reset it")
	}
	if !comboHeld(now.Add(5 * time.Second)) {
		t.Error("ten seconds of both did not")
	}
}

// A device with no screen can be reset for someone else too: Improv gives it its new network.
func TestResetWithNoScreen(t *testing.T) {
	fresh(t)
	was := screenless
	t.Cleanup(func() { screenless = was })
	screenless = true
	if len(Get().Actions()) != 1 {
		t.Fatal("a device with no screen, which Improv gives a network, offered no reset")
	}
}

// A board Improv has not been tried on yet offers no reset, and refuses one asked for.
func TestNoResetWhereImprovIsUntried(t *testing.T) {
	fresh(t)
	was := improvUntried
	t.Cleanup(func() { improvUntried = was })
	improvUntried = func() bool { return true }
	if CanReset() || Reset() == nil || len(Get().Actions()) != 0 {
		t.Fatal("a board Improv is untried on offered a reset")
	}
	if _, err := os.Stat(resetPath()); err == nil {
		t.Fatal("a refused reset left its marker")
	}
}
