package home

import (
	"bytes"
	"image"
	"image/jpeg"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/triggers"
)

// The cameras page: "show the front door" puts a camera's live view up for a while, a tap takes it
// down. Frames are Home Assistant's snapshots through the token, fetched one after another while
// the view is up, decoded and scaled here; a few a second is what the panel and the SoC manage,
// and enough to see who is there.

const (
	// cameraShow is how long a camera stays up when asked for by voice.
	cameraShow = 30 * time.Second
)

// CameraView is what the screen shows.
type CameraView struct {
	Entity string
	Name   string
	Until  time.Time
	Frame  *image.RGBA // the latest frame, scaled to fit; nil until the first arrives
	Error  string      // why there is no frame, when there is none

	span time.Duration // how long it was asked for; Until is restarted from the first frame
}

// LocalCamera is the entity name of the device's own camera on the list; it is not a Home
// Assistant entity, its frames come from the sensor behind the screen.
const LocalCamera = "local"

// Cameras is the configured list, with the device's own camera first where it has one.
func (f *Feature) Cameras() []config.Camera {
	cams := config.Get().Home.Cameras
	if len(cams) == 0 {
		// No list chosen (the home_cameras action): every camera Home Assistant has, by its own name.
		cams = f.homeAssistantCameras()
	}
	if camera.Available() {
		return append([]config.Camera{{Entity: LocalCamera, Name: localCameraName}}, cams...)
	}
	return cams
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
		go func() {
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
		}()
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
	return f.cam, true
}

// ShowCamera puts a camera up for d.
func (f *Feature) ShowCamera(entity string, d time.Duration) {
	name := entity
	for _, c := range f.Cameras() {
		if c.Entity == entity {
			name = c.Name
		}
	}
	f.mu.Lock()
	fresh := f.cam.Entity != entity || time.Now().After(f.cam.Until)
	f.cam = CameraView{Entity: entity, Name: name, Until: time.Now().Add(d), Frame: f.cam.Frame, span: d}
	if fresh {
		f.cam.Frame = nil
	}
	f.mu.Unlock()
	slog.Info("camera up", "entity", entity, "for", d)
	if fresh {
		if entity == LocalCamera {
			go f.localFrames()
		} else {
			go f.fetchFrames(entity)
		}
	}
	f.Changed.Emit(struct{}{})
}

// HideCamera takes the view down.
func (f *Feature) HideCamera() {
	f.mu.Lock()
	f.cam.Until = time.Time{}
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// fetchFrames pulls snapshots while the view is up, one after another.
func (f *Feature) fetchFrames(entity string) {
	for {
		f.mu.Lock()
		up := f.cam.Entity == entity && time.Now().Before(f.cam.Until)
		f.mu.Unlock()
		if !up {
			return
		}
		frame, err := f.snapshot(entity)
		f.mu.Lock()
		if f.cam.Entity == entity {
			if err != nil {
				f.cam.Error = err.Error()
			} else {
				// The time on screen counts from the first picture, not from the request: some
				// cameras take a while to start a stream, and a view that closes as it opens
				// is no view at all.
				if f.cam.Frame == nil {
					f.cam.Until = time.Now().Add(f.cam.span)
				}
				f.cam.Frame, f.cam.Error = frame, ""
			}
		}
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		if err != nil {
			slog.Warn("camera frame", "entity", entity, "err", err)
			time.Sleep(2 * time.Second)
		}
	}
}

// localFrames shows the device's own camera while the view is up: every frame the sensor
// produces, scaled to the panel.
func (f *Feature) localFrames() {
	release, err := camera.Get().Acquire()
	if err != nil {
		f.mu.Lock()
		f.cam.Error = err.Error()
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		return
	}
	defer release()
	dst := image.NewRGBA(image.Rect(0, 0, cameraFrameH*camera.Width/camera.Height, cameraFrameH))
	frames := make(chan *camera.Frame, 1)
	cancel := camera.Get().Frames.Listen(func(fr *camera.Frame) {
		select {
		case frames <- fr:
		default:
		}
	})
	defer cancel()
	for {
		f.mu.Lock()
		up := f.cam.Entity == LocalCamera && time.Now().Before(f.cam.Until)
		f.mu.Unlock()
		if !up {
			return
		}
		select {
		case fr := <-frames:
			xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), fr.RGBA, fr.RGBA.Bounds(), xdraw.Src, nil)
			shown := image.NewRGBA(dst.Bounds())
			copy(shown.Pix, dst.Pix)
			f.mu.Lock()
			if f.cam.Entity == LocalCamera {
				if f.cam.Frame == nil {
					f.cam.Until = time.Now().Add(f.cam.span)
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
		if c.Entity == LocalCamera {
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
	b, err := hass.Get().Fetch("/api/camera_proxy/" + entity)
	if err != nil {
		return nil, err
	}
	src, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	w, h := cameraFrameW, sh*cameraFrameW/sw
	if h > cameraFrameH {
		w, h = sw*cameraFrameH/sh, cameraFrameH
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst, nil
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
// the bell rings.
func (f *Feature) cameraActions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name: "home_cameras",
			Args: []esphome.Arg{{Name: "cameras", Type: esphome.ArgString}}, // "camera.x=Front door,camera.y=Deck"
			Run: func(c esphome.Call) (any, error) {
				var cams []config.Camera
				for _, item := range strings.Split(c.String("cameras"), ",") {
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
				if err := config.Set().Home().Cameras(cams); err != nil {
					return nil, err
				}
				slog.Info("home: cameras set", "count", len(cams))
				f.Changed.Emit(struct{}{})
				return nil, nil
			},
		},
		{
			Name: "home_show_camera",
			Args: []esphome.Arg{{Name: "entity", Type: esphome.ArgString}, {Name: "seconds", Type: esphome.ArgInt}},
			Run: func(c esphome.Call) (any, error) {
				d := time.Duration(c.Int("seconds")) * time.Second
				if d <= 0 {
					d = cameraShow
				}
				f.ShowCamera(strings.TrimSpace(c.String("entity")), d)
				return nil, nil
			},
		},
	}
}
