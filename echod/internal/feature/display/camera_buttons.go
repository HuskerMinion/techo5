//go:build !dot

package display

import (
	"image"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Camera buttons: the buttons along the foot of a camera's view that run a Home Assistant action, "Open
// gate" or "Unlock" (feature/home/camera_buttons.go keeps them). They open gates and doors, so none runs
// on one tap: the first arms it, and it says so ("Tap again: Open gate") in the highlighted button,
// and a second tap on it within buttonArmed runs it. A tap on a button neither closes the view nor
// reaches Talk.

const (
	// buttonArmed is how long a button waits for its second tap.
	buttonArmed = 4 * time.Second

	// buttonSent is how long a button that ran says so; buttonFailed, how long one that could not run
	// says why.
	buttonSent   = 2 * time.Second
	buttonFailed = 4 * time.Second
)

// camButtonFace is one button as a frame draws it: what it says, and whether it is armed (waiting for
// its second tap) or saying it failed.
type camButtonFace struct {
	label  string
	armed  bool
	failed bool
}

// cameraButtons is where the camera page's buttons are between taps: the one armed, and a word one has
// to say after it ran. The zero value is ready, with nothing armed.
type cameraButtons struct {
	mu sync.Mutex

	// The button armed, by its camera, its place and the button itself, so that a set changed under
	// the finger does not run a button the first tap did not arm; zero armedAt is none.
	camera  string
	index   int
	button  config.CameraButton
	armedAt time.Time

	// note is a word button noteIndex on noteCamera says instead of its label until noteUntil: that it
	// ran, or why it did not. noteFailed says which.
	noteCamera string
	noteIndex  int
	note       string
	noteFailed bool
	noteUntil  time.Time
}

// runCameraButton runs a button's action through Home Assistant, as the device's own (which needs "Allow
// the device to perform Home Assistant actions"), and says whether a Home Assistant was connected to
// take it; a variable so that a test can see what a tap runs without one.
var runCameraButton = func(camera string, b config.CameraButton) bool {
	c := component.Call{Service: b.Service, Data: map[string]string{}}
	if b.Entity != "" {
		c.Data["entity_id"] = b.Entity
	}
	heard := true
	c.Unheard = func() { heard = false }
	// The label is the owner's own words; the camera, the action and its entity say what ran.
	slog.Info("camera button", "camera", camera, "service", b.Service, "entity", b.Entity)
	component.CallService.Emit(c)
	return heard
}

// faces is camera's buttons as the frame at now draws them.
func (c *cameraButtons) faces(camera string, buttons []config.CameraButton, now time.Time) []camButtonFace {
	if len(buttons) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]camButtonFace, len(buttons))
	for i, b := range buttons {
		out[i] = camButtonFace{label: b.Label}
		switch {
		case c.armedHere(camera, i, b, now):
			out[i] = camButtonFace{label: armedLabel(b.Label), armed: true}
		case c.noteCamera == camera && c.noteIndex == i && now.Before(c.noteUntil):
			out[i] = camButtonFace{label: c.note, failed: c.noteFailed}
		}
	}
	return out
}

// armedHere is whether button b, at i on camera's view, is the one armed and still waiting at now.
// Under mu.
func (c *cameraButtons) armedHere(camera string, i int, b config.CameraButton, now time.Time) bool {
	return !c.armedAt.IsZero() && c.camera == camera && c.index == i && c.button == b &&
		now.Sub(c.armedAt) < buttonArmed && !now.Before(c.armedAt)
}

// tap is a tap on button i of camera's buttons at now. The first arms it; a second on the same button
// while it waits runs it, off the finger's goroutine, since the call ends in a network write. It says
// whether this tap ran it.
func (c *cameraButtons) tap(camera string, i int, buttons []config.CameraButton, now time.Time) bool {
	if i < 0 || i >= len(buttons) {
		return false
	}
	b := buttons[i]
	c.mu.Lock()
	if !c.armedHere(camera, i, b, now) {
		c.camera, c.index, c.button, c.armedAt = camera, i, b, now
		c.noteUntil = time.Time{} // a word left on another button gives way
		c.mu.Unlock()
		slog.Info("camera button asks to be sure", "camera", camera, "service", b.Service)
		return false
	}
	c.armedAt = time.Time{}
	c.mu.Unlock()
	go func() {
		heard := runCameraButton(camera, b)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.noteCamera, c.noteIndex, c.noteFailed = camera, i, !heard
		if heard {
			c.note, c.noteUntil = "Sent", time.Now().Add(buttonSent)
		} else {
			c.note, c.noteUntil = "No Home Assistant", time.Now().Add(buttonFailed)
			slog.Warn("camera button: Home Assistant is not connected to this device; the action was not run",
				"camera", camera, "service", b.Service)
		}
	}()
	return true
}

// armedLabel is what an armed button says: "Tap again: Open gate". The label stays as written: a
// sentence made of it reads oddly for a noun ("Garage").
func armedLabel(label string) string {
	if label == "" {
		return "Tap again"
	}
	return "Tap again: " + label
}

// cameraButtonsFor is the buttons the camera page shows on view v: none on the grid.
func cameraButtonsFor(v home.CameraView) []config.CameraButton {
	if len(v.Grid) > 0 || strings.TrimSpace(v.Entity) == "" {
		return nil
	}
	return home.CameraButtons(v.Entity)
}

// cameraButton draws one of the camera page's action buttons in b: the quiet button the Talk control
// is, the program's highlighted one while armed, and the warning one while it says why it failed.
func (r *paint) cameraButton(b image.Rectangle, f camButtonFace) {
	switch {
	case f.armed:
		r.labelButton(b, f.label, btnPrimary)
	case f.failed:
		r.labelButton(b, f.label, btnDanger)
	default:
		r.labelButton(b, f.label, btnSecondary)
	}
}
