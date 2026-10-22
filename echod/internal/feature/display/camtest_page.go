//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The camera test: a page opened from the Privacy & Security card that holds the camera while it is
// up and says how it is doing. A live picture, the frame rate the sensor keeps and the one the page
// keeps, the frame's size and leveling, how this board turns the sensor's frames into the stream and
// the pages (camtest_path.go), and what of it is open to the network. It takes every tap, and Back
// goes back to the card. It lets the camera go by itself after camTestFor, so a page left open never
// keeps the sensor on.

var (
	// camTestFor is how long the page stays up untouched before it closes and lets the camera go.
	camTestFor = 2 * time.Minute

	// camTestTick is how often the page is redrawn while it is up, for its rates and its countdown,
	// with no new picture to bring it.
	camTestTick = 500 * time.Millisecond
)

const (
	// camTestPreviewEvery is how often a frame is developed for the picture. Developing is the cores'
	// work, so the page shows three or so a second and counts every frame the sensor sends.
	camTestPreviewEvery = 300 * time.Millisecond

	// camTestWindow is how far back the rates are measured.
	camTestWindow = 2 * time.Second
)

// What the page holds the camera through. They are variables so that a test can stand in a camera
// on a machine with none.
var (
	camTestAvailable = camera.Available
	camTestAcquire   = func() (func(), error) { return camera.Get().Acquire() }
	camTestListen    = func(fn func(*camera.Frame)) func() { return camera.Get().Frames.Listen(fn) }
)

// camTest is one opening of the page. The frames arrive on the camera's goroutine and the pictures
// are developed on one of their own, so what they write is under mu, not the display's lock.
type camTest struct {
	opened time.Time
	done   chan struct{}
	once   sync.Once

	mu         sync.Mutex
	started    bool        // a frame has come
	err        string      // why the camera would not start
	frames     []time.Time // the frames of the last camTestWindow, empty ones included
	shown      []time.Time // the pictures developed in that time
	developing bool        // a picture is being developed
	devAt      time.Time   // when the last one was started
	empty      bool        // the last frame had nothing in it: the camera is held off
	w, h       int         // the sensor's frame
	preview    *image.RGBA
	path       []string // how the board makes its pictures, a paragraph a line
}

// camTestView is the page as one frame draws it.
type camTestView struct {
	left       time.Duration // until it closes by itself
	started    bool
	err        string
	empty      bool
	sensorFPS  float64
	shownFPS   float64
	w, h       int
	preview    *image.RGBA
	board      string
	path       []string
	network    bool   // the camera is streamed (Camera streaming on, with a password)
	pages      bool   // Camera on the network is on: the camera's web pages
	user       string // the camera login's user name, never its password; empty for no login
	discovery  bool   // Camera discovery (ONVIF) is on
	address    string
	privacyOff bool // the privacy mute is on
}

// stop ends the opening: its goroutine lets the camera go.
func (t *camTest) stop() { t.once.Do(func() { close(t.done) }) }

// frame is one frame from the sensor: counted, and every camTestPreviewEvery developed for the
// picture, on a goroutine of its own, since a listener must not hold the camera up.
func (t *camTest) frame(fr *camera.Frame, wake func()) {
	w, h := fr.Dims()
	now := time.Now()
	at := fr.At
	if at.IsZero() {
		at = now
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = true
	t.frames = since(append(t.frames, at), at.Add(-camTestWindow))
	t.empty = w == 0
	if t.empty {
		return
	}
	t.w, t.h = w, h
	if t.developing || now.Sub(t.devAt) < camTestPreviewEvery {
		return
	}
	t.developing, t.devAt = true, now
	safe.Go("camera test picture", func() {
		img := fr.Image()
		t.mu.Lock()
		t.developing = false
		if img != nil {
			done := time.Now()
			t.preview = img
			t.shown = since(append(t.shown, done), done.Add(-camTestWindow))
		}
		t.mu.Unlock()
		wake()
	})
}

// since is times without those before from; they are in order, so the ones kept are the tail.
func since(times []time.Time, from time.Time) []time.Time {
	i := 0
	for i < len(times) && times[i].Before(from) {
		i++
	}
	return times[i:]
}

// rate is how many a second times are coming, over the window, from the first to the last of them;
// nothing until there are two. Times that stopped coming a while ago are no rate at all.
func rate(times []time.Time, now time.Time) float64 {
	n := len(times)
	if n < 2 || now.Sub(times[n-1]) > camTestWindow {
		return 0
	}
	span := times[n-1].Sub(times[0]).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(n-1) / span
}

// view is the page as it is now.
func (t *camTest) view(now time.Time) camTestView {
	t.mu.Lock()
	v := camTestView{
		left: t.opened.Add(camTestFor).Sub(now), started: t.started, err: t.err, empty: t.empty,
		sensorFPS: rate(t.frames, now), shownFPS: rate(t.shown, now), w: t.w, h: t.h,
		preview: t.preview, path: t.path,
	}
	t.mu.Unlock()
	v.board = layout.Model
	cs := config.Get().CamStream
	v.network = cs.On && cs.Pass != ""
	v.pages = config.Get().Security.Camera
	if user, pass := cs.Login(); pass != "" {
		v.user = user
	}
	v.discovery = cs.Discovery
	v.address = address()
	v.privacyOff, _ = mute.Get().Muted()
	return v
}

// openCameraTest puts the page up and starts the camera for it.
func (d *Display) openCameraTest() {
	t := &camTest{opened: time.Now(), done: make(chan struct{})}
	d.mu.Lock()
	old := d.camtest
	d.camtest = t
	d.mu.Unlock()
	if old != nil {
		old.stop()
	}
	slog.Info("camera test", "open", true)
	safe.Go("camera test", func() { d.runCameraTest(t) })
	d.wake()
}

// runCameraTest holds the camera for the page until it closes, and closes it after camTestFor.
func (d *Display) runCameraTest(t *camTest) {
	path := camTestPath()
	t.mu.Lock()
	t.path = path
	t.mu.Unlock()
	d.wake()

	// Acquire can wait out a stop that is under way, so the page is drawn meanwhile, starting.
	release, err := camTestAcquire()
	if err != nil {
		why := capitalize(err.Error())
		if muted, _ := mute.Get().Muted(); muted {
			why = "The privacy mute holds the camera off: turn the microphones back on to test it."
		} else if w := camera.Get().Wedged(); w != nil {
			why = capitalize(w.Error())
		}
		t.mu.Lock()
		t.err = why
		t.mu.Unlock()
		slog.Info("camera test: no camera", "err", err)
	} else {
		defer release()
		cancel := camTestListen(func(fr *camera.Frame) { t.frame(fr, d.wake) })
		defer cancel()
	}
	d.wake()

	tick := time.NewTicker(camTestTick)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case now := <-tick.C:
			if now.Sub(t.opened) >= camTestFor {
				slog.Info("camera test: closed by itself", "after", camTestFor)
				d.endCameraTest(t, false)
				return
			}
			d.wake()
		}
	}
}

// endCameraTest takes the page down if t is still the one up. back returns to the Privacy & Security
// card it was opened from; the sheet comes back before the page goes, so the settings lock never
// sees neither open.
func (d *Display) endCameraTest(t *camTest, back bool) {
	d.mu.Lock()
	if d.camtest != t {
		d.mu.Unlock()
		return
	}
	if back {
		d.sheet, d.cat, d.picker, d.restartArm = true, catSecurity, "", time.Time{}
		d.draft, d.cardScroll, d.pickScroll = nil, 0, 0
	}
	d.camtest = nil
	d.mu.Unlock()
	t.stop()
	slog.Info("camera test", "open", false)
	d.wake()
}

// cameraTestUp is whether the page is up.
func (d *Display) cameraTestUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.camtest != nil
}

// cameraTestView is the page for the frame being drawn; nil while it is down.
func (d *Display) cameraTestView(now time.Time) *camTestView {
	d.mu.Lock()
	t := d.camtest
	d.mu.Unlock()
	if t == nil {
		return nil
	}
	v := t.view(now)
	return &v
}

// cameraTestTap is a finger on the page: only Back means anything.
func (d *Display) cameraTestTap(x, y int) {
	if !image.Pt(x, y).In(d.r.camTestBackRect()) {
		return
	}
	d.mu.Lock()
	t := d.camtest
	d.mu.Unlock()
	if t != nil {
		d.endCameraTest(t, true)
	}
}

// cameraTestRows is the Privacy & Security card's way to the page, on a device with a camera.
func cameraTestRows() []settingRow {
	if !camTestAvailable() {
		return nil
	}
	return []settingRow{{id: "camtest", label: "Camera test", sub: "Frame rate, picture and how the board makes it", kind: ctlButton, button: "Open"}}
}

// The page's parts, in the Show 5's units: the picture on the left with Back under it, the words on
// the right.
const (
	camTestTopBase    = 72
	camTestPicWBase   = 400
	camTestBackWBase  = 200
	camTestBackHBase  = 56
	camTestBottomBase = 14
	camTestGapBase    = 24
)

// camTestPicBox is the most the picture may take.
func (r *renderer) camTestPicBox() image.Rectangle {
	top := r.s(camTestTopBase)
	bottom := r.h - r.s(camTestBottomBase) - r.s(camTestBackHBase) - r.s(14)
	return image.Rect(r.margin, top, r.margin+r.s(camTestPicWBase), bottom)
}

// camTestBackRect is Back, under the picture.
func (r *renderer) camTestBackRect() image.Rectangle {
	bottom := r.h - r.s(camTestBottomBase)
	return image.Rect(r.margin, bottom-r.s(camTestBackHBase), r.margin+r.s(camTestBackWBase), bottom)
}

// fitIn is the largest w x h rectangle that fits box, centered in it.
func fitIn(box image.Rectangle, w, h int) image.Rectangle {
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	fw, fh := box.Dx(), box.Dx()*h/w
	if fh > box.Dy() {
		fw, fh = box.Dy()*w/h, box.Dy()
	}
	x, y := box.Min.X+(box.Dx()-fw)/2, box.Min.Y
	return image.Rect(x, y, x+fw, y+fh)
}

// cameraTestPage draws the page, and returns how far down its words went.
func (r *renderer) cameraTestPage(s scene) int {
	v := s.camtest
	r.text(r.body, "Camera test", r.margin, r.s(52), amber)
	right := clockHM(s.now)
	if v.left > 0 {
		left := v.left.Round(time.Second)
		right = fmt.Sprintf("closes in %d:%02d  ·  %s", int(left.Minutes()), int(left.Seconds())%60, right)
	}
	r.text(r.small, right, r.w-r.margin-r.width(r.small, right), r.s(52), dim)

	// The picture, or what stands in its place.
	pic := fitIn(r.camTestPicBox(), v.w, v.h)
	if v.preview != nil && !v.empty && v.err == "" {
		xdraw.ApproxBiLinear.Scale(r.dst, pic, v.preview, v.preview.Bounds(), draw.Src, nil)
	} else {
		r.bevel(pic, shift(walnut, 8), false)
		msg := "Starting the camera…"
		switch {
		case v.err != "":
			msg = v.err
		case v.empty:
			msg = "The privacy mute holds the camera off: its frames come empty until the mute is turned off."
		case v.started:
			msg = "Developing the first picture…"
		}
		lines := r.wrap(r.tiny, msg, pic.Dx()-r.s(32))
		lh := r.tiny.Metrics().Height.Ceil()
		y := pic.Min.Y + (pic.Dy()-len(lines)*lh)/2 + r.tiny.Metrics().Ascent.Ceil()
		for _, l := range lines {
			r.text(r.tiny, l, pic.Min.X+(pic.Dx()-r.width(r.tiny, l))/2, y, cream)
			y += lh
		}
	}
	r.key(r.camTestBackRect(), "Back", false)

	// The words, down the right.
	x := pic.Max.X + r.s(camTestGapBase)
	if box := r.camTestPicBox(); box.Max.X+r.s(camTestGapBase) > x {
		x = box.Max.X + r.s(camTestGapBase)
	}
	width := r.w - r.margin - x
	y := r.s(camTestTopBase) + r.tiny.Metrics().Ascent.Ceil()
	line := func(face font.Face, s string, c color.RGBA) {
		for _, l := range r.wrap(face, s, width) {
			r.text(face, l, x, y, c)
			y += face.Metrics().Height.Ceil()
		}
	}
	gap := func() { y += r.s(10) }

	sensor := "Sensor: no frames yet"
	switch {
	case v.empty:
		sensor = fmt.Sprintf("Sensor %.1f fps, empty frames", v.sensorFPS)
	case v.started:
		sensor = fmt.Sprintf("Sensor %.1f fps  ·  shown %.1f fps", v.sensorFPS, v.shownFPS)
	}
	line(r.tiny, sensor, cream)
	if v.w > 0 {
		size := fmt.Sprintf("Frame %dx%d RAW10", v.w, v.h)
		if v.preview != nil {
			b := v.preview.Bounds()
			size += fmt.Sprintf("  ·  picture %dx%d", b.Dx(), b.Dy())
		}
		line(r.tiny, size, cream)
	}
	gap()
	if v.board != "" {
		line(r.micro, v.board, cream)
	}
	for _, p := range v.path {
		line(r.micro, p, dim)
	}
	gap()
	user, addr := v.user, v.address
	if s.demo {
		addr = "192.0.2.50"
		if user != "" {
			user = "camera"
		}
	}
	if v.network {
		found := ""
		if v.discovery {
			found = " (found by discovery)"
		}
		line(r.micro, fmt.Sprintf("Streamed: rtsp://%s:%s/camera, ONVIF on %d%s", addr, camTestRTSPPort, camTestWebPort, found), cream)
	} else {
		line(r.micro, "Not streamed: Camera streaming is off, or has no password yet", cream)
	}
	if v.pages {
		line(r.micro, fmt.Sprintf("Pages: http://%s:%d/camera.jpg and /camera.mjpeg", addr, camTestWebPort), dim)
	}
	if user != "" {
		line(r.micro, "Login: “"+user+"”, for the stream, ONVIF and the pages", dim)
	} else {
		line(r.micro, "No login yet: the pages are open, the stream shut", dim)
	}
	if v.privacyOff && v.err == "" && !v.empty {
		line(r.micro, "The privacy mute is on.", amber)
	}
	return y - r.micro.Metrics().Height.Ceil()
}
