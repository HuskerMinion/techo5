//go:build !dot

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// fakeButtonRuns has runCameraButton hand what it is asked to run to a channel, answering heard, for the
// length of the test.
func fakeButtonRuns(t *testing.T, heard bool) chan config.CameraButton {
	t.Helper()
	ran := make(chan config.CameraButton, 4)
	old := runCameraButton
	runCameraButton = func(_ string, b config.CameraButton) bool { ran <- b; return heard }
	t.Cleanup(func() { runCameraButton = old })
	return ran
}

// noRun fails the test when anything ran.
func noRun(t *testing.T, ran chan config.CameraButton, what string) {
	t.Helper()
	select {
	case b := <-ran:
		t.Errorf("%s ran %+v", what, b)
	case <-time.After(50 * time.Millisecond):
	}
}

// ranOne waits for one run and gives it.
func ranOne(t *testing.T, ran chan config.CameraButton, what string) config.CameraButton {
	t.Helper()
	select {
	case b := <-ran:
		return b
	case <-time.After(2 * time.Second):
		t.Fatalf("%s ran nothing", what)
	}
	return config.CameraButton{}
}

// A button runs only on a second tap: the first arms it and it says so, a second within buttonArmed runs
// it, and a second that comes too late, or lands on another button, only arms again.
func TestACameraButtonAsksForASecondTap(t *testing.T) {
	ran := fakeButtonRuns(t, true)
	gate := config.CameraButton{Label: "Open gate", Service: "script.open_gate"}
	door := config.CameraButton{Label: "Unlock", Service: "lock.unlock", Entity: "lock.front_door"}
	buttons := []config.CameraButton{gate, door}
	const cam = "camera.gate"
	at := time.Now()
	var c cameraButtons

	if c.tap(cam, 0, buttons, at) {
		t.Fatal("the first tap ran the button")
	}
	noRun(t, ran, "the first tap")
	f := c.faces(cam, buttons, at.Add(time.Second))
	if !f[0].armed || f[0].label != "Tap again: Open gate" || f[1].armed || f[1].label != "Unlock" {
		t.Errorf("faces %+v after the first tap, want the gate's armed and saying so", f)
	}
	if f := c.faces("camera.deck", buttons, at.Add(time.Second)); f[0].armed {
		t.Error("another camera's view shows the button armed")
	}
	if !c.tap(cam, 0, buttons, at.Add(3*time.Second)) {
		t.Fatal("the second tap did not run the button")
	}
	if b := ranOne(t, ran, "the second tap"); b != gate {
		t.Errorf("ran %+v, want the gate's", b)
	}
	if c.tap(cam, 0, buttons, at.Add(3500*time.Millisecond)) {
		t.Error("a third tap ran it again without being armed")
	}
	noRun(t, ran, "a tap after the run")

	// Too late: the button waits buttonArmed, and a tap after that arms it afresh.
	at = at.Add(time.Minute)
	c.tap(cam, 1, buttons, at)
	if f := c.faces(cam, buttons, at.Add(buttonArmed)); f[1].armed {
		t.Error("the button is still armed after buttonArmed")
	}
	if c.tap(cam, 1, buttons, at.Add(buttonArmed)) {
		t.Error("a tap after buttonArmed ran the button")
	}
	noRun(t, ran, "a late tap")

	// Another button: the arm moves to it, and neither runs.
	at = at.Add(time.Minute)
	c.tap(cam, 0, buttons, at)
	if c.tap(cam, 1, buttons, at.Add(time.Second)) {
		t.Error("a tap on the other button ran it")
	}
	if f := c.faces(cam, buttons, at.Add(time.Second)); f[0].armed || !f[1].armed {
		t.Errorf("faces %+v, want the arm moved to the second button", f)
	}
	noRun(t, ran, "a tap on another button")

	// The set changed under the finger: the button now in that place is not the one armed.
	changed := []config.CameraButton{gate, {Label: "Unlock", Service: "lock.unlock", Entity: "lock.back_door"}}
	if c.tap(cam, 1, changed, at.Add(2*time.Second)) {
		t.Error("a button that replaced the armed one ran on one tap")
	}
	noRun(t, ran, "a replaced button")

	// Out of range is nothing.
	if c.tap(cam, 5, buttons, at) || c.tap(cam, -1, buttons, at) {
		t.Error("a tap on no button ran something")
	}
}

// A button says it ran for a moment, or why it could not, in its own place.
func TestACameraButtonSaysHowItWent(t *testing.T) {
	buttons := []config.CameraButton{{Label: "Open gate", Service: "script.open_gate"}}
	const cam = "camera.gate"
	wait := func(c *cameraButtons, want string) camButtonFace {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if f := c.faces(cam, buttons, time.Now()); f[0].label == want {
				return f[0]
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("the button never said %q", want)
		return camButtonFace{}
	}

	ran := fakeButtonRuns(t, true)
	var ok cameraButtons
	now := time.Now()
	ok.tap(cam, 0, buttons, now)
	ok.tap(cam, 0, buttons, now)
	ranOne(t, ran, "the second tap")
	if f := wait(&ok, "Sent"); f.failed || f.armed {
		t.Errorf("the run said %+v, want a plain Sent", f)
	}

	ran = fakeButtonRuns(t, false)
	var unheard cameraButtons
	now = time.Now()
	unheard.tap(cam, 0, buttons, now)
	unheard.tap(cam, 0, buttons, now)
	ranOne(t, ran, "the second tap")
	if f := wait(&unheard, "No Home Assistant"); !f.failed {
		t.Errorf("the failed run said %+v, want it marked failed", f)
	}
	if f := unheard.faces(cam, buttons, time.Now().Add(buttonFailed+time.Second)); f[0].label != "Open gate" || f[0].failed {
		t.Errorf("after buttonFailed the button says %+v, want its label back", f[0])
	}
}

// The armed words: the label lowered into the sentence, unless it starts with a word in capitals.
func TestArmedLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Open gate": "Tap again: Open gate",
		"Unlock":    "Tap again: Unlock",
		"TV off":    "Tap again: TV off",
		"Ö":         "Tap again: Ö",
		"":          "Tap again",
	} {
		if got := armedLabel(in); got != want {
			t.Errorf("armedLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
