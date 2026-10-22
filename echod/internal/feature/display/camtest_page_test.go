//go:build !dot && !spot

package display

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// fakeCamera stands in for the camera behind the camera test: it counts who holds it and who listens.
type fakeCamera struct {
	mu       sync.Mutex
	listen   func(*camera.Frame)
	released chan struct{}
	canceled bool
}

func standInCamera(t *testing.T) *fakeCamera {
	t.Helper()
	c := &fakeCamera{released: make(chan struct{})}
	avail, acquire, listen := camTestAvailable, camTestAcquire, camTestListen
	camTestAvailable = func() bool { return true }
	camTestAcquire = func() (func(), error) {
		var once sync.Once
		return func() { once.Do(func() { close(c.released) }) }, nil
	}
	camTestListen = func(fn func(*camera.Frame)) func() {
		c.mu.Lock()
		c.listen = fn
		c.mu.Unlock()
		return func() {
			c.mu.Lock()
			c.canceled = true
			c.mu.Unlock()
		}
	}
	t.Cleanup(func() { camTestAvailable, camTestAcquire, camTestListen = avail, acquire, listen })
	return c
}

// send waits for the page to be listening for frames, and hands it one.
func (c *fakeCamera) send(t *testing.T, fr *camera.Frame) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		c.mu.Lock()
		fn := c.listen
		c.mu.Unlock()
		if fn != nil {
			fn(fr)
			return
		}
	}
	t.Fatal("the camera test never listened for frames")
}

func (c *fakeCamera) waitReleased(t *testing.T) {
	t.Helper()
	select {
	case <-c.released:
	case <-time.After(2 * time.Second):
		t.Fatal("the camera was never let go")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.canceled {
		t.Error("let go of the camera but still listening for its frames")
	}
}

// The Privacy & Security card has the camera test where there is a camera, and none where there is not.
func TestTheCameraTestRowIsWhereThereIsACamera(t *testing.T) {
	has := func() bool {
		for _, r := range securityRows(sheetView{}) {
			if r.id == "camtest" {
				return r.kind == ctlButton
			}
		}
		return false
	}
	avail := camTestAvailable
	t.Cleanup(func() { camTestAvailable = avail })
	camTestAvailable = func() bool { return false }
	if has() {
		t.Error("a camera test on a device with no camera")
	}
	camTestAvailable = func() bool { return true }
	if !has() {
		t.Error("no camera test on a device with a camera")
	}
}

// The row opens the page and holds the camera; Back goes back to the card and lets the camera go.
// A frame with nothing in it reads as the privacy mute, not a black picture.
func TestTheCameraTestOpensAndBackLetsTheCameraGo(t *testing.T) {
	c := standInCamera(t)
	d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480))), poke: make(chan struct{}, 1), sheet: true}
	d.cat = catSecurity
	if !d.deviceRowTap("camtest", partMain, 0) {
		t.Fatal("the row was not the Show's")
	}
	if !d.cameraTestUp() || d.sheetUp() {
		t.Fatalf("after the row: page up %v, sheet up %v", d.cameraTestUp(), d.sheetUp())
	}
	c.send(t, &camera.Frame{Seq: 1, At: time.Now()})
	if v := d.cameraTestView(time.Now()); v == nil || !v.started || !v.empty {
		t.Errorf("an empty frame reads as %+v", v)
	}

	// A tap away from Back does nothing.
	d.cameraTestTap(d.r.w-10, d.r.h/2)
	if !d.cameraTestUp() {
		t.Fatal("a tap off Back closed the page")
	}
	b := d.r.camTestBackRect()
	d.cameraTestTap(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2)
	if d.cameraTestUp() {
		t.Fatal("Back left the page up")
	}
	d.mu.Lock()
	sheet, cat := d.sheet, d.cat
	d.mu.Unlock()
	if !sheet || cat != catSecurity {
		t.Errorf("Back went to sheet %v on card %d, not the Privacy & Security card", sheet, cat)
	}
	c.waitReleased(t)
}

// Left open, the page closes by itself and lets the camera go, without bringing the settings back.
func TestTheCameraTestClosesByItself(t *testing.T) {
	c := standInCamera(t)
	forTime, tick := camTestFor, camTestTick
	camTestFor, camTestTick = 30*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { camTestFor, camTestTick = forTime, tick })
	d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480))), poke: make(chan struct{}, 1)}
	d.openCameraTest()
	c.waitReleased(t)
	for end := time.Now().Add(time.Second); d.cameraTestUp() && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if d.cameraTestUp() || d.sheetUp() {
		t.Errorf("after its time: page up %v, sheet up %v", d.cameraTestUp(), d.sheetUp())
	}
}

// The rates are frames a second over the window, and none once they stop coming.
func TestTheCameraTestRate(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	var times []time.Time
	for i := 0; i < 29; i++ {
		times = append(times, now.Add(time.Duration(i)*time.Second/14))
	}
	last := times[len(times)-1]
	if got := rate(times, last); got < 13.9 || got > 14.1 {
		t.Errorf("14 a second reads as %.2f", got)
	}
	if got := rate(times, last.Add(3*time.Second)); got != 0 {
		t.Errorf("frames that stopped read as %.2f a second", got)
	}
	if got := since(times, last.Add(-time.Second)); len(got) != 15 {
		t.Errorf("a second's worth is %d frames", len(got))
	}
}

// The page's words fit beside the picture and above the foot of the screen at the Show 5's size and
// the Show 8's, with the longest account of a board (the 2nd gen Show 8's), and Back is on the screen.
func TestTheCameraTestPageFits(t *testing.T) {
	path := []string{
		"OV13855, RAW10 2112x1568, 30 fps → developed on the cores",
		"Pages: JPEGs developed and encoded on the cores",
	}
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		v := &camTestView{left: 90 * time.Second, started: true, sensorFPS: 29.8, shownFPS: 3.1, w: 2112, h: 1568,
			preview: image.NewRGBA(image.Rect(0, 0, 1056, 784)),
			board:   "Echo Show 8 2nd gen (athena)", path: path, user: "frontdoor", address: "192.0.2.50", network: true}
		bottom := r.cameraTestPage(scene{now: time.Date(2026, 10, 18, 9, 41, 0, 0, time.UTC), camtest: v})
		if bottom > size.Y-r.s(4) {
			t.Errorf("%v: the words run to %d, off the foot of the screen", size, bottom)
		}
		if b := r.camTestBackRect(); !b.In(image.Rect(0, 0, size.X, size.Y)) {
			t.Errorf("%v: Back is off the screen at %v", size, b)
		}
		if pic := r.camTestPicBox(); pic.Max.Y > r.camTestBackRect().Min.Y {
			t.Errorf("%v: the picture runs into Back", size)
		}
	}
}
