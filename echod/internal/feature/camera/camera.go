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
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/jpegenc"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func init() {
	component.Register(component.Network, Get(), component.Order(70))
	Get().register()
}

const (
	// quality is the JPEG quality for both, on every board but those that send full-size pages.
	quality = 85
	// qualityFull is the full-size pages' (fullPages: the 2nd gen Show 5 on a mainline kernel, and the
	// 2nd gen Show 8 with its JPEG encoder) where the cores encode them: every picture sent is the
	// sensor's full size (encodeFrame), so the stream is as sharp as a still.
	qualityFull = 90

	// qualityEncoder and encoderFPS are the full-size pages' quality and most pictures a second where the
	// JPEG encoder makes them (hwEncode). The encoder frees the cores of the encode, not of developing
	// the frame, so uncapped a viewer took 14 to 23 pictures a second (a dark room, a lit one), 60 to
	// 140 Mbit/s and two and a half to nearly four cores. At these a viewer of the armv7 daemon takes
	// about one and a half times the CPU of the half-size page on the cores (a little more than it on
	// arm64), at twice the resolution and some 10 Mbit/s in a lit room.
	qualityEncoder = 80
	encoderFPS     = 7

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

// fullPages is whether the camera pages send full-size pictures (encodeFrame): the 2nd gen Show 5 on
// a mainline kernel, where it was tried and its cores keep up, and the 2nd gen Show 8 where its kernel
// has the JPEG encoder (hwEncode). Every other board sends the half-size picture and the full-size still
// at quality, as before; a Show 8 without the encoder too, whose 2112x1568 frame would be about four
// times the work on the cores per viewer.
var fullPages = fullPagesOn(layout.CronosMainline(), hwEncode)

func fullPagesOn(cronosMainline, hwEncode bool) bool { return cronosMainline || hwEncode }

// hwEncode is whether the pages' JPEGs are encoded on the 2nd gen Show 8's JPEG encoder: its mainline
// kernel, with the encoder there (mtk-jpeg-enc). No other board looks for it.
var hwEncode = layout.AthenaMainline() && jpegenc.Available()

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

// encodeFrame is a frame, streamed or still, as a JPEG where fullPages. On the 2nd gen Show 8 with its
// JPEG encoder (hwEncode), the cores develop it at full size straight into the encoder's YUYV buffer
// (FullYUYV) and the encoder makes the JPEG at qualityEncoder: a fraction of the time, and of the
// power, the cores take to encode it. Elsewhere, or if the encoder fails a picture, it is developed on
// the cores (Full) and encoded in strips across them (encodeStripes) at qualityFull.
//
// The black frame of a stopped sensor has no size for the encoder, and goes the cores' way quietly. A
// failing encoder is logged once a minute at most, and left alone a minute by jpegenc (ErrBackingOff).
func encodeFrame(f *camera.Frame) ([]byte, error) {
	if hwEncode && !f.Blank() {
		b, err := encodeOnEncoder(f)
		if err == nil {
			return b, nil
		}
		hwFailure(err, time.Now())
	}
	return encodeStripes(f.Full(), qualityFull)
}

// hwWarned is when a failing encoder was last logged.
var hwWarned atomic.Int64

// hwFailure logs a picture the encoder failed, once a minute at most, and not the pictures refused while
// jpegenc backs off after it; it reports whether it logged.
func hwFailure(err error, now time.Time) bool {
	if errors.Is(err, jpegenc.ErrBackingOff) {
		return false
	}
	last := hwWarned.Load()
	if now.UnixNano()-last < int64(time.Minute) || !hwWarned.CompareAndSwap(last, now.UnixNano()) {
		return false
	}
	slog.Warn("camera: hardware jpeg failed, encoding on the cores", "err", err)
	return true
}

// yuyvFrame is a frame the cores can develop straight into YUYV: the Show's.
type yuyvFrame interface {
	Dims() (w, h int)
	FullYUYV(dst []byte) bool
}

// encodeOnEncoder is a frame as a JPEG at qualityEncoder made by the JPEG encoder.
func encodeOnEncoder(f *camera.Frame) ([]byte, error) {
	y, ok := any(f).(yuyvFrame)
	if !ok {
		return jpegenc.Encode(f.Full(), qualityEncoder)
	}
	w, h := y.Dims()
	developed := false
	b, err := jpegenc.EncodeYUYV(w, h, qualityEncoder, func(dst []byte) { developed = y.FullYUYV(dst) })
	if err == nil && !developed {
		return nil, errors.New("the frame has no picture to develop")
	}
	return b, err
}

// pageInterval is the least time between two pictures of a page stream: 1/encoderFPS where the JPEG
// encoder makes them, none elsewhere, where the stream goes as fast as the encode and the viewer let it.
func pageInterval(hwEncode bool) time.Duration {
	if hwEncode {
		return time.Second / encoderFPS
	}
	return 0
}

// pageDue is whether a frame goes out on a page stream: once the interval has passed since the last
// one, and always the black frame of a sensor that stopped, which ends the stream.
func pageDue(last, now time.Time, interval time.Duration, blank bool) bool {
	return blank || now.Sub(last) >= interval
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
	interval := pageInterval(hwEncode)
	var last time.Time
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
			// Too soon: skipped, never held, as the sensor sends the next soon enough.
			if !pageDue(last, time.Now(), interval, fr.Blank()) {
				continue
			}
			last = time.Now()
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
