// Package camera serves the Show's own camera over HTTP, for Home Assistant and anything else
// on the network: a JPEG snapshot and an MJPEG stream. Home Assistant's "Generic Camera" takes the
// snapshot URL, "MJPEG IP Camera" the stream; either makes the Show a camera entity there.
//
// Both pages are off unless switched on (feature/security), and take the camera stream's login or a
// browser the setup page has let in (feature/camstream webpages.go). While either sends the camera, the
// screen shows it is being watched, as it does for the stream.
//
// The sensor runs only while a request holds it, and stops a few seconds after the last one, so
// a device nobody is watching has its camera off — and the mute button keeps it off entirely.
package camera

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func init() {
	component.Register(component.Network, Get(), component.Order(70))
	Get().register()
}

const (
	// quality is the JPEG quality for both, on every board but the 2nd gen Show 5 on a mainline kernel.
	quality = 85
	// qualityFull is the 2nd gen Show 5's on a mainline kernel (fullPages): every picture sent is the
	// sensor's full size, developed and encoded across the cores (encodeFrame), so the stream is as
	// sharp as a still.
	qualityFull = 90

	// snapshotWait bounds a snapshot request: sensor start plus a frame or two.
	snapshotWait = 8 * time.Second
)

type Feature struct{}

var shared = &Feature{}

func Get() *Feature { return shared }

func (f *Feature) Name() string { return "camera" }

// register puts the camera's pages on the device's web port (feature/web), which is open while any
// of its pages is switched on and shut when none is. A device with no camera registers nothing here
// and still has the port for the pages it does have.
func (f *Feature) register() {
	web.Handle("/camera.jpg", "Camera", cameraOpen, guarded(f.snapshot))
	web.Handle("/camera.mjpeg", "", cameraOpen, guarded(f.stream))
	f.registerScreen()
}

// cameraOpen is the switch and the hardware together, asked at the moment of the request: the device
// nodes are looked for then rather than when the daemon starts, since this runs before the rest of
// the daemon has touched anything.
func cameraOpen() bool { return config.Get().Security.Camera && camera.Available() }

// guarded asks for the camera's login. The handlers mark the camera watched themselves, once there is
// a picture to send: a request refused for the mute or the lens cover lights nothing.
func guarded(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The generation before the login: a login saved while this one is checked ends it too.
		gen := camstream.PageGen()
		if !camstream.PageAllowed(w, r) {
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), pageGenKey{}, gen)))
	}
}

// pageGenKey carries the page generation guarded read into the handler.
type pageGenKey struct{}

// fullPages is whether the camera pages send full-size pictures at qualityFull: the 2nd gen Show 5 on
// a mainline kernel, where it was tried and its cores keep up. Every other board sends the half-size
// picture and the full-size still at quality, as before; the 2nd gen Show 8 too, whose 2112x1568
// frame would be about four times the work on the cores per viewer.
var fullPages = layout.CronosMainline()

// encode is a streamed frame: at the sensor's full size where fullPages, where viewers of the same
// frame share one encode (the last one is kept, and a second viewer asking for it while it is being
// made waits for it); elsewhere the half-size picture.
func encode(f *camera.Frame) ([]byte, error) {
	if raw, ok := any(f).(interface{ RawJPEG() []byte }); ok && len(raw.RawJPEG()) > 0 {
		return raw.RawJPEG(), nil
	}
	if !fullPages {
		return encodeHalf(f)
	}
	encoded.mu.Lock()
	defer encoded.mu.Unlock()
	if encoded.frame == f {
		return encoded.jpg, nil
	}
	b, err := encodeFrame(f)
	if err != nil {
		return nil, err
	}
	encoded.frame, encoded.jpg = f, b
	return b, nil
}

// encodeHalf is a streamed frame's half-size picture at quality, as every board but the 2nd gen Show 5
// sends it.
func encodeHalf(f interface{ Image() *image.RGBA }) ([]byte, error) {
	return encodeWhole(f.Image(), quality)
}

// stillFrame is a frame for a still: the next frame.
func stillFrame(ctx context.Context) (*camera.Frame, error) {
	return camera.Get().Snapshot(ctx)
}

// encodeStill is a still's JPEG: by encodeFrame where fullPages, else developed at full size (Full)
// and encoded at quality.
func encodeStill(f *camera.Frame) ([]byte, error) {
	if raw, ok := any(f).(interface{ RawJPEG() []byte }); ok && len(raw.RawJPEG()) > 0 {
		return raw.RawJPEG(), nil
	}
	if fullPages {
		return encodeFrame(f)
	}
	return encodeFull(f)
}

// encodeFull is a still at the sensor's own size, at quality.
func encodeFull(f interface{ Full() *image.RGBA }) ([]byte, error) {
	return encodeWhole(f.Full(), quality)
}

var encoded struct {
	mu    sync.Mutex
	frame *camera.Frame
	jpg   []byte
}

// encodeFrame is a frame, streamed or still, as a JPEG where fullPages: developed on the cores at
// full size (Full) and encoded in strips across them (encodeStripes).
func encodeFrame(f *camera.Frame) ([]byte, error) {
	return encodeStripes(f.Full(), qualityFull)
}

// snapshot answers with the next frame.
func (f *Feature) snapshot(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), snapshotWait)
	defer cancel()
	fr, err := stillFrame(ctx)
	if err != nil {
		slog.Warn("camera snapshot", "err", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer camstream.PageWatching()() // only a picture that goes out lights the dot
	b, err := encodeStill(fr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// stream sends frames as they come until the client hangs up.
func (f *Feature) stream(w http.ResponseWriter, r *http.Request) {
	release, err := camera.Get().Acquire()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer release()
	defer camstream.PageWatching()()
	// A new login or the lens cover ends the stream (camstream.PageGen), counted from before the login.
	gen, ok := r.Context().Value(pageGenKey{}).(uint64)
	if !ok {
		gen = camstream.PageGen()
	}
	const boundary = "techo5frame"
	frames := make(chan *camera.Frame, 1)
	// The client is slower than the sensor: skip, never queue, but end on the newest frame.
	cancel := camera.Get().ListenLatest(frames)
	defer cancel()
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	check := time.NewTicker(time.Second)
	defer check.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-check.C:
			if camstream.PageGen() != gen {
				return
			}
		case fr := <-frames:
			if camstream.PageGen() != gen {
				return
			}
			b, err := encode(fr)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(b)); err != nil {
				return
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, "\r\n"); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			// The sensor stopped (the mute): its black frame is shown, and the page is done, so
			// the dot goes out; a reconnect is refused while it stays off.
			if fr.Blank() {
				return
			}
		}
	}
}
