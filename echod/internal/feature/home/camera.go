package home

import (
	"context"
	"image"
	_ "image/jpeg" // Home Assistant serves camera snapshots as JPEG
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
	"github.com/HuskerMinion/techo5/echod/internal/lib/triggers"
)

// The cameras page: "show the front door" puts a camera's live view up for a while, a tap takes it
// down. A camera Home Assistant can stream is shown from its live stream, decoded by the video
// decoder, when Live camera video is on (camera_stream.go). Any other, and one whose stream fails, is
// shown from Home Assistant's snapshots through the token, fetched one after another while the view is
// up, decoded and scaled here: a few a second, enough to see who is there.

const (
	// cameraShow is how long a camera stays up when asked for by voice.
	cameraShow = 30 * time.Second
)

// cameraRetry is how long a camera waits after a frame fails before asking again. A variable only so
// that a test does not have to wait it out when it takes a view down.
var cameraRetry = 2 * time.Second

// spawn runs fn in the background and counts it, so that a test can wait for every goroutine a view
// started to be gone (Feature.loops) before the next test swaps the config and Home Assistant under it.
func (f *Feature) spawn(fn func()) {
	f.loops.Add(1)
	go func() {
		defer f.loops.Done()
		fn()
	}()
}

// CameraView is what the screen shows.
type CameraView struct {
	Entity string
	Name   string
	Until  time.Time
	Frame  *image.RGBA // the latest frame, scaled to fit; nil until the first arrives
	Error  string      // why there is no frame, when there is none
	// Caption is a notification's words over the view (feature/notify); nil for a plain camera.
	Caption *Caption
	// Grid is the cameras of the camera grid, while the grid is up (Entity is GridEntity); nil otherwise.
	Grid []GridCell
	// OverPlain is a notification's picture put over a camera somebody was already watching (a
	// doorbell's): the screen does not hold it back at night (display's gateCamera), since the camera
	// was up anyway.
	OverPlain bool
	// View numbers the view, so that a tap can close the view it was read from and not whichever is up
	// when it gets around to it (HideView). Until cannot say which view it is: it moves when the first
	// frame arrives. A camera asked for again while its view is up keeps its number, and a different
	// notification's picture over it gets a new one.
	View uint64

	span time.Duration // how long it was asked for; Until is restarted from the first frame
	gen  uint64        // which camera view this is: frames fetched for an earlier one are not shown in it
}

// frameMu keeps a streamed camera's picture still while the screen draws it: a stream's frames are
// copied into one picture for the whole stream (showStream) rather than each made anew, which at 24
// frames a second of 896×672 is over fifty megabytes a second for the collector. A frame from anywhere
// else is never written to once it is shown.
var frameMu sync.RWMutex

// ReadFrame runs draw with a CameraView's Frame kept still: the screen draws a frame inside it.
func ReadFrame(draw func()) {
	frameMu.RLock()
	defer frameMu.RUnlock()
	draw()
}

// LocalCamera is the entity name of the device's own camera on the list; it is not a Home
// Assistant entity, its frames come from the sensor behind the screen.
const LocalCamera = "local"

// Cameras is the configured list, with the device's own camera first where it has one.
func (f *Feature) Cameras() []config.Camera {
	h := config.Get().Home
	cams := slices.Clone(h.Cameras)
	if h.AllHACameras() {
		// No list chosen (the home_cameras action or the setup page): every camera Home Assistant has,
		// by its own name. None chosen leaves cams empty.
		cams = listHACameras(f)
	}
	cams = append(cams, h.Reolink.Cameras...)
	cams = append(cams, h.Go2RTC.Cameras...)
	if camera.Available() && !h.OwnCameraLeftOut {
		cams = append([]config.Camera{{Entity: LocalCamera, Name: localCameraName}}, cams...)
	}
	return ordered(cams, h.CameraOrder)
}

// ordered puts the cameras named in order first, in that order, and the rest after them as they were.
// A camera in order that is not on the list is passed over.
func ordered(cams []config.Camera, order []string) []config.Camera {
	if len(order) == 0 {
		return cams
	}
	out := make([]config.Camera, 0, len(cams))
	used := make([]bool, len(cams))
	for _, e := range order {
		for i, c := range cams {
			if !used[i] && c.Entity == e {
				out, used[i] = append(out, c), true
				break
			}
		}
	}
	for i, c := range cams {
		if !used[i] {
			out = append(out, c)
		}
	}
	return out
}

// listHACameras is Home Assistant's own camera list; a test puts a list of its own in.
var listHACameras = (*Feature).homeAssistantCameras

// CamerasNone is the word that chooses no Home Assistant camera, in home_cameras and on the setup page.
const CamerasNone = "none"

// ParseCameras reads a camera list as home_cameras takes it: "camera.x=Front door,camera.y=Deck", the
// name left off for the entity's own. none is the word CamerasNone (any case); an empty list is All.
func ParseCameras(s string) (cams []config.Camera, none bool) {
	if strings.EqualFold(strings.TrimSpace(s), CamerasNone) {
		return nil, true
	}
	for _, item := range strings.Split(s, ",") {
		entity, name, _ := strings.Cut(strings.TrimSpace(item), "=")
		entity, name = strings.TrimSpace(entity), strings.TrimSpace(name)
		if entity == "" {
			continue
		}
		if name == "" {
			name = strings.TrimPrefix(entity, "camera.")
		}
		cams = append(cams, config.Camera{Entity: entity, Name: name})
	}
	return cams, false
}

// SetCameraList saves whether the device's own camera is left off the camera list, and the list's
// order (entities, first to last), and redraws what shows it.
func SetCameraList(ownLeftOut bool, order []string) error {
	if err := config.Set().Home().CameraList(ownLeftOut, order); err != nil {
		return err
	}
	Get().Changed.Emit(struct{}{})
	return nil
}

// SetGo2RTCNames saves the names shown and said for go2rtc's streams, by entity, and redraws what
// shows them.
func SetGo2RTCNames(names map[string]string) error {
	if err := config.Set().Home().Go2RTCNames(names); err != nil {
		return err
	}
	Get().Changed.Emit(struct{}{})
	return nil
}

// SetCameras saves which of Home Assistant's cameras the device lists: none, the list, or every one
// when the list is empty, and redraws what shows them.
func SetCameras(cams []config.Camera, none bool) error { return Get().setCameras(cams, none) }

func (f *Feature) setCameras(cams []config.Camera, none bool) error {
	var err error
	if none {
		err = config.Set().Home().NoCameras()
	} else {
		err = config.Set().Home().Cameras(cams)
	}
	if err != nil {
		return err
	}
	slog.Info("home: cameras set", "none", none, "count", len(cams))
	f.Changed.Emit(struct{}{})
	return nil
}

// haCamerasEvery is how often Home Assistant's own camera list is looked at again.
const haCamerasEvery = 10 * time.Minute

// homeAssistantCameras is Home Assistant's cameras as last fetched, starting a fetch in the background
// when that is stale; it never waits, since screens ask for it while drawing.
func (f *Feature) homeAssistantCameras() []config.Camera {
	if !hass.Get().Ready() {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(f.haCamerasAt) > haCamerasEvery && !f.haCamerasBusy {
		f.haCamerasBusy = true
		f.spawn(func() {
			list, err := hass.Get().Entities("camera")
			var cams []config.Camera
			for _, e := range list {
				name := e.Name
				if name == "" {
					name = e.ID
				}
				cams = append(cams, config.Camera{Entity: e.ID, Name: name})
			}
			f.mu.Lock()
			f.haCamerasBusy = false
			if err != nil {
				slog.Warn("home: listing Home Assistant's cameras", "err", err)
				f.haCamerasAt = time.Now().Add(time.Minute - haCamerasEvery) // try again in a minute
			} else {
				f.haCameras, f.haCamerasAt = cams, time.Now()
			}
			f.mu.Unlock()
			if err == nil {
				f.Changed.Emit(struct{}{})
			}
		})
	}
	return f.haCameras
}

// Camera is the view in progress, if any.
func (f *Feature) Camera() (CameraView, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cam.Entity == "" || time.Now().After(f.cam.Until) {
		return CameraView{}, false
	}
	v := f.cam
	v.Grid = slices.Clone(f.cam.Grid) // the grid's fetch writes its cells while the screen reads this
	return v, true
}

// ShowCamera puts a camera up for d, with its sound if the device's own setting asks for it.
func (f *Feature) ShowCamera(entity string, d time.Duration) {
	f.showCamera(entity, d, CameraSound())
}

// showCamera is ShowCamera with the sound decided by the caller, which is what the action does: an
// automation for a doorbell wants that one camera heard whether or not the device's setting says so.
func (f *Feature) showCamera(entity string, d time.Duration, sound bool) {
	f.showCameraCaptioned(entity, d, sound, nil)
}

// showCameraCaptioned puts a camera up with a notification's words over it, or none (nil): a camera
// asked for any other way is a plain camera, so a caption never outlasts its notification.
func (f *Feature) showCameraCaptioned(entity string, d time.Duration, sound bool, c *Caption) {
	if entity == GridEntity {
		return // the grid's name, not a camera's: the grid goes up with ShowGrid
	}
	name, listed := entity, false
	if entity == LocalCamera {
		name = localCameraName // left off the list, an automation can still put it up
	}
	for _, cam := range f.Cameras() {
		if cam.Entity == entity {
			name, listed = cam.Name, true
		}
	}
	f.mu.Lock()
	now := time.Now()
	fresh := f.cam.Entity != entity || now.After(f.cam.Until)
	if !listed && !fresh {
		name = f.cam.Name // the same camera asked for again keeps the name it was given, looked up or not
	}
	view := f.cam.View
	if fresh || !sameCaption(f.cam.Caption, c) {
		// Asked for again with the same words (or none) it is the same thing on screen and keeps its
		// number, so a tap read before the re-show still closes it; anything else is a new view.
		view = f.newView()
	}
	up := f.cam.Entity != "" && now.Before(f.cam.Until)
	overPlain := c != nil && up && (f.cam.Caption == nil || f.cam.OverPlain)
	if !sameCaption(f.cam.Caption, c) {
		f.endCaption("replaced")
	}
	until := now.Add(d)
	if !fresh && f.cam.Until.After(until) {
		// The same camera asked for again for less time than it has left: a notification's picture of
		// the doorbell's own camera does not cut the doorbell's view short.
		until = f.cam.Until
	}
	f.cancelStill() // a camera in place of a still: the still's picture is not wanted
	if c != nil {
		f.dropWaiting("replaced") // a newer notification's picture: one still waiting was replaced by it
	}
	f.cam = CameraView{Entity: entity, Name: name, Until: until, Frame: f.cam.Frame, Caption: c, OverPlain: overPlain, span: d, View: view, gen: f.cam.gen}
	var ctx context.Context
	if fresh {
		f.cam.Frame = nil
		f.camMuted = false // a fresh view starts audible if its sound was asked for
		// A view of its own, ending what fetched for the one before: a camera closed while its stream
		// was starting and opened again must not end up with two decoders.
		ctx = f.newCameraFetch()
	}
	gen := f.cam.gen
	f.mu.Unlock()
	live := CameraLive() // read here, not by the fetch: a test swaps the config once this returns
	slog.Info("camera up", "entity", entity, "for", d, "sound", fresh && sound)
	if fresh {
		if entity == LocalCamera {
			f.spawn(func() { f.localFrames() })
		} else {
			f.spawn(func() { f.fetchFrames(ctx, entity, gen, live) })
		}
		if sound && !isReolink(entity) && !isGo2RTC(entity) {
			// The sound is asked of Home Assistant and taken off the speaker when this view ends; a
			// view already up keeps the sound it was started with (camera_sound.go).
			f.spawn(func() { f.startCameraSound(entity, thisDevice()) })
		}
	}
	if !listed && name == entity && entity != LocalCamera && !isReolink(entity) && !isGo2RTC(entity) {
		// Not on the device's list, or on Home Assistant's list before it has been fetched: Home
		// Assistant is asked for this one camera's name rather than showing its entity id.
		f.spawn(func() { f.lookUpName(entity, entity) })
	}
	f.Changed.Emit(struct{}{})
}

// lookUpName asks Home Assistant what it calls entity and puts that on the view in place of
// placeholder, if the view is still entity's and still says placeholder. Nothing changes when Home
// Assistant cannot be asked or has no name for it.
func (f *Feature) lookUpName(entity, placeholder string) {
	s, err := hass.Get().State(entity)
	if err != nil {
		slog.Debug("camera name", "entity", entity, "err", err)
		return
	}
	name, _ := s.Attributes["friendly_name"].(string)
	if name = strings.TrimSpace(name); name == "" {
		return
	}
	f.mu.Lock()
	ours := f.cam.Entity == entity && f.cam.Name == placeholder
	if ours {
		f.cam.Name = name
	}
	f.mu.Unlock()
	if ours {
		f.Changed.Emit(struct{}{})
	}
}

// HoldCamera keeps the view of entity up for at least d more, if it is the one up: a camera being
// talked through (feature/talkback) does not time out mid-sentence.
func (f *Feature) HoldCamera(entity string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if f.cam.Entity != entity || now.After(f.cam.Until) {
		return
	}
	if until := now.Add(d); until.After(f.cam.Until) {
		f.cam.Until = until
	}
}

// endCaption notes why the notification's view up now is going, if one is, for notify to report
// (PictureEnded). The caller holds f.mu.
func (f *Feature) endCaption(why string) {
	if c := f.cam.Caption; c != nil && c.ID != "" && f.cam.Entity != "" && time.Now().Before(f.cam.Until) {
		f.endedID, f.endedWhy = c.ID, why
	}
}

// dropWaiting forgets the still waiting for the camera view, if there is one, and wakes what waits with
// it (showWaiting). why is how it went, as endCaption, noted unless its time had run out; "" for a still
// that is coming up, or that nobody is to hear about. The caller holds f.mu.
func (f *Feature) dropWaiting(why string) {
	w := f.waiting
	if w == nil {
		return
	}
	if why != "" && w.c.ID != "" && time.Now().Before(w.until) {
		f.endedID, f.endedWhy = w.c.ID, why
	}
	close(w.gone)
	f.waiting = nil
}

// PictureEnded is why notification id's view went other than by running out: "replaced" by another
// camera or picture, "dismissed" by a tap or the camera page closing; "" when it ran out, or is up.
func (f *Feature) PictureEnded(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" || f.endedID != id {
		return ""
	}
	return f.endedWhy
}

// newView is the number for a view being put up. The caller holds f.mu.
func (f *Feature) newView() uint64 {
	f.viewSeq++
	return f.viewSeq
}

// newCameraFetch gives the camera view being put up a number of its own (CameraView.gen) and the
// context its frames are fetched with, ending the one before's. The caller holds f.mu.
func (f *Feature) newCameraFetch() context.Context {
	f.stopCameraFetch()
	f.camGen++
	f.cam.gen = f.camGen
	ctx, cancel := context.WithCancel(context.Background())
	f.camCancel = cancel
	return ctx
}

// stopCameraFetch ends what the camera view up fetches its frames with, a stream's decoder among them,
// now rather than at its next look at the clock. The caller holds f.mu.
func (f *Feature) stopCameraFetch() {
	if f.camCancel != nil {
		f.camCancel()
		f.camCancel = nil
	}
}

// sameCaption is whether two captions are the same notification's (or both none).
func sameCaption(a, b *Caption) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID
}

// HideView takes the view down if it is still the view numbered view, and says whether it did. The
// compare and the hide are one step under the lock, so a tap that was read before a newer view replaced
// its own cannot take the newer one down.
func (f *Feature) HideView(view uint64) bool {
	f.mu.Lock()
	ours := f.cam.View == view && f.cam.Entity != "" && time.Now().Before(f.cam.Until)
	if ours {
		f.endCaption("dismissed")
		f.cancelStill()
		f.stopCameraFetch()
		f.cam.Until = time.Time{}
		f.cam.Caption = nil
	}
	f.mu.Unlock()
	if ours {
		f.Changed.Emit(struct{}{})
	}
	return ours
}

// HideCamera takes the view down, and forgets whose words were on it: a frame still on its way for
// the view must find nothing to land on (viewIsUp), and a tap on a view gone must not be taken for a
// tap on a notification that came after it.
func (f *Feature) HideCamera() {
	f.mu.Lock()
	f.endCaption("dismissed")
	f.cancelStill()
	f.stopCameraFetch()
	f.cam.Until = time.Time{}
	f.cam.Caption = nil
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// HidePicture takes the view down if it is notification id's, and says whether it did. The compare and
// the hide are one step under the lock, so a tap or a replacement landing while a newer notification
// takes the page cannot take the newer one's view down.
func (f *Feature) HidePicture(id string) bool {
	f.mu.Lock()
	if f.waiting != nil && f.waiting.c.ID == id {
		// Its picture never went up: it was waiting for the camera view to end.
		f.dropWaiting("dismissed")
		f.mu.Unlock()
		return true
	}
	ours := f.cam.Caption != nil && f.cam.Caption.ID == id && time.Now().Before(f.cam.Until)
	if ours {
		f.endCaption("dismissed")
		f.cancelStill()
		f.stopCameraFetch()
		f.cam.Until = time.Time{}
		f.cam.Caption = nil
	}
	f.mu.Unlock()
	if ours {
		f.Changed.Emit(struct{}{})
	}
	return ours
}

// viewIsUp is whether entity's view is the one on the screen and has not run out or been taken down.
// A fetch writes to the view only while this holds, with f.mu held across the check and the write:
// otherwise a frame that was on its way when somebody tapped would put the view back up.
func (f *Feature) viewIsUp(entity string) bool {
	return f.cam.Entity == entity && time.Now().Before(f.cam.Until)
}

// fetchFrames shows the camera while its view, gen, is up: its live stream where it has one and live
// video is on (camera_stream.go), else, or once that fails, snapshots one after another.
func (f *Feature) fetchFrames(ctx context.Context, entity string, gen uint64, live bool) {
	if live && f.streamFrames(ctx, entity, gen) {
		return
	}
	for f.viewUp(ctx, entity, gen) {
		frame, err := f.snapshot(entity)
		f.showFrame(entity, gen, frame, err)
		if err != nil {
			slog.Warn("camera frame", "entity", entity, "err", err)
			pause(ctx, cameraRetry)
		}
	}
}

// viewUp is whether camera view gen of entity is still on screen.
func (f *Feature) viewUp(ctx context.Context, entity string, gen uint64) bool {
	if ctx.Err() != nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.viewIsUp(entity) && f.cam.gen == gen
}

// showFrame puts a frame of entity's on screen, or why there is none, if its view gen is still up. The
// check and the write are one step under f.mu (viewIsUp): a frame on its way when somebody tapped must
// not put the view back up.
func (f *Feature) showFrame(entity string, gen uint64, frame *image.RGBA, err error) {
	f.mu.Lock()
	if f.viewIsUp(entity) && f.cam.gen == gen {
		if err != nil {
			f.cam.Error = err.Error()
		} else {
			// The time on screen counts from the first picture, not from the request: some
			// cameras take a while to start a stream, and a view that closes as it opens
			// is no view at all.
			if f.cam.Frame == nil {
				if until := time.Now().Add(f.cam.span); until.After(f.cam.Until) {
					f.cam.Until = until // never shorter than a hold asked for (HoldCamera)
				}
			}
			f.cam.Frame, f.cam.Error = frame, ""
		}
	}
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// localFrames shows the device's own camera while the view is up: every frame the sensor
// produces, scaled to the panel.
func (f *Feature) localFrames() {
	release, err := camera.Get().Acquire()
	if err != nil {
		f.mu.Lock()
		if f.viewIsUp(LocalCamera) {
			f.cam.Error = err.Error()
		}
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		return
	}
	defer release()
	_, fh := cameraFrameSize()
	dst := image.NewRGBA(image.Rect(0, 0, fh*camera.Width/camera.Height, fh))
	frames := make(chan *camera.Frame, 1)
	// The newest frame replaces one still waiting (see ListenLatest): dropping the new one instead
	// kept a stale picture on screen when the sensor stopped for the mute or the lens cover.
	cancel := camera.Get().ListenLatest(frames)
	defer cancel()
	for {
		f.mu.Lock()
		up := f.viewIsUp(LocalCamera)
		f.mu.Unlock()
		if !up {
			return
		}
		select {
		case fr := <-frames:
			src := fr.Image()
			picscale.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src)
			shown := image.NewRGBA(dst.Bounds())
			copy(shown.Pix, dst.Pix)
			f.mu.Lock()
			if f.viewIsUp(LocalCamera) {
				if f.cam.Frame == nil {
					if until := time.Now().Add(f.cam.span); until.After(f.cam.Until) {
						f.cam.Until = until // never shorter than a hold asked for (HoldCamera)
					}
				}
				f.cam.Frame, f.cam.Error = shown, ""
			}
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
		case <-time.After(500 * time.Millisecond):
			// No frame yet (the sensor is starting) — back round to check the view is still up.
		}
	}
}

// Prewarm asks Home Assistant for one frame from every camera and drops it. Some cameras take
// seconds to start a stream on the first request; asking while the list is on screen means the
// one that gets tapped answers at once.
func (f *Feature) Prewarm() {
	for _, c := range f.Cameras() {
		if c.Entity == LocalCamera || isReolink(c.Entity) || isGo2RTC(c.Entity) {
			continue
		}
		go func(entity string) {
			if _, err := hass.Get().Fetch("/api/camera_proxy/" + entity); err != nil {
				slog.Debug("camera prewarm", "entity", entity, "err", err)
			}
		}(c.Entity)
	}
}

// snapshot fetches one frame and scales it to fit the panel.
func (f *Feature) snapshot(entity string) (*image.RGBA, error) {
	var b []byte
	var err error
	switch {
	case isReolink(entity):
		b, err = reolinkSnap(entity)
	case isGo2RTC(entity):
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		fw, _ := cameraFrameSize()
		b, err = go2rtcSnap(ctx, entity, fw)
		cancel()
	default:
		b, err = hass.Get().Fetch("/api/camera_proxy/" + entity)
	}
	if err != nil {
		return nil, err
	}
	src, err := decodeWithin(b, maxFramePixels, "camera "+entity)
	if err != nil {
		return nil, err
	}
	return cameraFrame(src), nil
}

// CameraFit is the Camera picture setting: "" whole and as large as the screen takes it, "fill" the
// whole screen with the rest cut off, "native" at the size the camera sends it, never larger.
func CameraFit() string {
	switch f := config.Get().Screen.CameraFit; f {
	case "fill", "native":
		return f
	}
	return ""
}

// cameraFrame is a camera's picture for its view, as Camera picture says: fitted to the panel, filling
// it, or at its own size where that is smaller than the panel.
func cameraFrame(src image.Image) *image.RGBA {
	switch CameraFit() {
	case "fill":
		return fillFrame(src)
	case "native":
		fw, fh := cameraFrameSize()
		if b := src.Bounds(); b.Dx() <= fw && b.Dy() <= fh {
			dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			xdraw.Copy(dst, image.Point{}, src, b, xdraw.Src, nil)
			return dst
		}
	}
	return fitFrame(src)
}

// fillFrame scales a picture to cover the panel, keeping its shape, cut to it about its middle.
func fillFrame(src image.Image) *image.RGBA {
	fw, fh := cameraFrameSize()
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	part := b
	if sw*fh > sh*fw { // wider than the panel: its sides cut off
		w := sh * fw / fh
		part = image.Rect(b.Min.X+(sw-w)/2, b.Min.Y, b.Min.X+(sw-w)/2+w, b.Max.Y)
	} else {
		h := sw * fh / fw
		part = image.Rect(b.Min.X, b.Min.Y+(sh-h)/2, b.Max.X, b.Min.Y+(sh-h)/2+h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, fw, fh))
	picscale.CatmullRom.Scale(dst, dst.Bounds(), src, part, xdraw.Src)
	return dst
}

// fitFrame scales a picture to fit the panel, keeping its shape.
func fitFrame(src image.Image) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	fw, fh := cameraFrameSize()
	w, h := fw, sh*fw/sw
	if h > fh {
		w, h = sw*fh/sh, fh
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	picscale.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src)
	return dst
}

// MatchCamera finds a camera named in what was heard: "show the front door", "show me the deck
// camera", the German "zeig die Haustür". The sentence has to ask for a camera at all (lib/triggers,
// in the screen's language) and then name one; the names are the owner's own, in whatever language
// they wrote them in Home Assistant. Empty when nothing matches.
func (f *Feature) MatchCamera(heard string) string {
	if !triggers.AboutCamera(heard, config.Get().Screen.Language) {
		return ""
	}
	h := strings.ToLower(heard)
	best, bestLen := "", 0
	for _, c := range f.Cameras() {
		n := strings.ToLower(c.Name)
		if n != "" && strings.Contains(h, n) && len(n) > bestLen {
			best, bestLen = c.Entity, len(n)
		}
	}
	return best
}

// Camera actions: the list, and showing one — for an automation that wants the front door up when
// the bell rings. The sound is a second action rather than an argument on the first, because Home
// Assistant registers an action's arguments as a closed, all-required set: no caller can leave one
// out, so adding one to home_show_camera would break every automation already calling it.
func (f *Feature) cameraActions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name: "home_cameras",
			Args: []esphome.Arg{{Name: "cameras", Type: esphome.ArgString}}, // "camera.x=Front door,camera.y=Deck"
			// "none" for no Home Assistant camera, empty for every one.
			Run: func(c esphome.Call) (any, error) {
				cams, none := ParseCameras(c.String("cameras"))
				return nil, f.setCameras(cams, none)
			},
		},
		{
			Name: "home_show_camera",
			Args: []esphome.Arg{{Name: "entity", Type: esphome.ArgString}, {Name: "seconds", Type: esphome.ArgInt}},
			Run: func(c esphome.Call) (any, error) {
				f.ShowCamera(strings.TrimSpace(c.String("entity")), cameraSeconds(c))
				return nil, nil
			},
		},
		{
			Name: "home_show_camera_sound",
			Args: []esphome.Arg{{Name: "entity", Type: esphome.ArgString}, {Name: "seconds", Type: esphome.ArgInt}, {Name: "sound", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				f.showCamera(strings.TrimSpace(c.String("entity")), cameraSeconds(c), ParseOnOff(c.String("sound"), CameraSound()))
				return nil, nil
			},
		},
	}
}

// cameraSeconds is how long a show-camera action's view lasts: its own seconds, or the default when
// that is zero or less.
func cameraSeconds(c esphome.Call) time.Duration {
	d := time.Duration(c.Int("seconds")) * time.Second
	if d <= 0 {
		d = cameraShow
	}
	return d
}
