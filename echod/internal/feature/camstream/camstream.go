// Package camstream serves the device's own camera over RTSP, so a recorder (Frigate, an NVR, go2rtc) can
// record it like any other camera: rtsp://<device>:8554/camera, with a login. A recorder that adds cameras
// by ONVIF finds the stream through onvif.go.
//
// Where the board has a video encoder (the 2nd gen Show 8 on a mainline kernel), the stream is H.264 made by
// it at the sensor's rate (h264.go); elsewhere it is JPEGs, carried as RTP/JPEG (lib/rtpjpeg), which ffmpeg and
// everything built on it reads. Each picture is encoded once, JPEGs at most maxFPS a second, and sent to
// every viewer. RTP goes over the RTSP connection itself (TCP, interleaved): one port to let through, and
// nothing to lose on Wi-Fi.
//
// Off on a new device, and never on without a password: a camera that anybody on the network could watch
// is not one to leave lying around. Turning it off, saving a new login and closing the lens cover each end
// every connection; the mute button stops the camera, as it does everywhere. At most maxViewers watch at
// once.
package camstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rtpjpeg"
)

func init() {
	component.Register(component.Network, Get(), component.Order(30))
}

const (
	// Port is where the camera is served.
	Port = "8554"

	// Path is the camera's address on it.
	Path = "/camera"

	// maxViewers is how many may watch at once, maxPending how many connections may be open that have not
	// logged in yet, and maxPendingFrom how many of those one address may have.
	maxViewers     = 2
	maxPending     = 4
	maxPendingFrom = 2

	// maxFPS is the most pictures a second the stream sends: enough for a recorder's detection, and a
	// JPEG encode each on a small CPU.
	maxFPS = 10

	// quality is the JPEG quality of the stream: a recorder's picture, not a still.
	quality = 70
)

// frame is a camera frame, developed when called.
type frame func() image.Image

// sendable is one picture as the stream sends it: an RTP/JPEG frame, or an H.264 access unit.
type sendable interface {
	Packets(seq *uint16, ts, ssrc uint32, mtu int) [][]byte
}

// keyed is a picture a viewer can start at, or not: H.264's keyframes. A picture that does not say is
// one to start at. at is when the camera took it, where the picture knows.
type keyed interface {
	keyframe() bool
	at() time.Time
}

// h264Queue is how many access units a viewer may be behind (two seconds at 30 a second): unlike a JPEG,
// one dropped spoils the picture until the next keyframe, so a viewer that falls this far behind waits
// for one.
const h264Queue = 60

var errFull = errors.New("as many viewers as the stream takes are watching")

// Feature is the switch, the server and the pictures they share.
type Feature struct {
	on *esphome.Switch

	// frames starts the camera and hands its pictures over until release, each still to be developed, so
	// one skipped costs nothing; replaced in tests.
	frames func() (<-chan frame, func(), error)
	// available is whether the device has a camera to stream; h264 whether the board has an encoder to make
	// the stream H.264 with; both replaced in tests.
	available func() bool
	h264      func() bool

	crop      *esphome.Switch
	discovery *esphome.Switch

	// Changed fires when somebody starts or stops watching.
	Changed hook.Hook[struct{}]

	wake   chan struct{}
	guard  guard
	nonces nonces

	// startMu keeps one viewer's start or stop of the camera from crossing another's, without holding mu
	// for the time a camera takes to start.
	startMu sync.Mutex

	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]struct{}
	pending int                 // connections that have not logged in
	waiting map[string]int      // the same, by address
	since   map[net.Conn]waiter // each connection not logged in yet, and when it came
	subs    map[chan sendable]struct{}
	lost    map[chan sendable]bool // H.264 viewers that fell behind, until the next keyframe
	stopPic func()                 // ends the pictures, while anybody watches
	pages   int                    // camera web pages being sent (webpages.go)
	pageGen uint64                 // moves on when open page streams must end: a new login, the lens cover (PageGen)

	sps, pps []byte // the H.264 stream's parameter sets, once the encoder has made them
	forceKey func() // asks the H.264 encoder for a keyframe, while it runs
}

var (
	once   sync.Once
	shared *Feature
)

// Get is the one.
func Get() *Feature {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Feature {
	f := &Feature{
		on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "camera_streaming", Name: "Camera streaming", Icon: "mdi:cctv", Category: esphome.CategoryConfig,
		}},
		frames:    cameraFrames,
		available: offered,
		h264:      hasEncoder,
		wake:      make(chan struct{}, 1),
		conns:     map[net.Conn]struct{}{},
		waiting:   map[string]int{},
		since:     map[net.Conn]waiter{},
		subs:      map[chan sendable]struct{}{},
		lost:      map[chan sendable]bool{},
	}
	f.crop = &esphome.Switch{
		Base: esphome.Base{ObjectID: "camera_stream_crop", Name: "Camera stream crop (1920x1088)", Icon: "mdi:crop",
			Category: esphome.CategoryConfig},
		OnCommand: func(on bool) {
			f.crop.Set(on)
			if err := config.Set().CamStream().Crop(on); err != nil {
				slog.Error("saving the camera stream's crop failed", "err", err)
			}
		},
	}
	f.discovery = &esphome.Switch{
		Base: esphome.Base{ObjectID: "camera_discovery", Name: "Camera discovery (ONVIF)", Icon: "mdi:access-point-network",
			Category: esphome.CategoryConfig},
		OnCommand: func(on bool) {
			f.discovery.Set(on)
			if err := config.Set().CamStream().Discovery(on); err != nil {
				slog.Error("saving the camera's discovery failed", "err", err)
			}
			f.poke()
		},
	}
	f.on.OnCommand = func(on bool) {
		f.on.Set(on)
		if err := config.Set().CamStream().On(on); err != nil {
			slog.Error("saving camera streaming failed", "err", err)
		}
		f.poke()
	}
	return f
}

func (f *Feature) Name() string { return "camera streaming" }

// Entities are the switch and the discovery switch, on a device with a camera only, and on the 2nd gen
// Show 8 the switch between the whole view scaled (1456x1088) and its middle pixel for pixel (1920x1088).
func (f *Feature) Entities() []esphome.Entity {
	if !f.available() {
		return nil
	}
	if CropOffered() {
		return []esphome.Entity{f.on, f.discovery, f.crop}
	}
	return []esphome.Entity{f.on, f.discovery}
}

// Actions: camera_credentials sets the stream's login from Home Assistant, as the setup page does, over
// Home Assistant's link only once it is encrypted with the device's key: a password sent in the clear is
// not one to trust.
func (f *Feature) Actions() []*esphome.Action {
	if !f.available() {
		return nil
	}
	return []*esphome.Action{{
		Name: "camera_credentials",
		Args: []esphome.Arg{{Name: "username", Type: esphome.ArgString}, {Name: "password", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if !security.Encrypted() {
				slog.Warn("camera streaming: login refused while Home Assistant's link has no device key")
				return nil, errors.New("camera_credentials: the device has no API encryption key; set one first")
			}
			user, pass := strings.TrimSpace(c.String("username")), c.String("password")
			if err := security.CheckLogin(user, pass); err != nil {
				return nil, fmt.Errorf("camera_credentials: %w", err)
			}
			if err := config.Set().CamStream().Login(user, pass); err != nil {
				return nil, err
			}
			Poke()
			slog.Info("camera streaming: login set from Home Assistant", "user", user)
			return nil, nil
		},
	}}
}

func (f *Feature) Restore(c config.Config) {
	f.on.Set(c.CamStream.On)
	f.discovery.Set(c.CamStream.Discovery)
	f.crop.Set(c.CamStream.Crop)
}

// poke has the listener looked at again, for a change to the switch.
func (f *Feature) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
	web.Wake() // the ONVIF services are on the web port, and come and go with the stream
}

// Poke is for the setup page once it has saved a login: whoever logged in with the old one is let go.
func Poke() {
	f := Get()
	f.dropAll()
	f.endPages()
	f.poke()
}

// PageGen is the generation a camera page stream started in; once it moves on, the stream ends: the
// login it came in with may be gone, or the lens cover closed.
func PageGen() uint64 {
	f := Get()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pageGen
}

func (f *Feature) endPages() {
	f.mu.Lock()
	f.pageGen++
	f.mu.Unlock()
}

// Offered is whether the device has a camera to stream, for the setup page.
func Offered() bool { return Get().available() }

// Watching is whether anybody is being sent the camera, over the stream or a camera page, for the screen.
func Watching() bool { return Get().watching() }

func (f *Feature) watching() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs) > 0 || f.pages > 0
}

// wanted is whether the camera is to be served: on, with a password, on a device with a camera.
func (f *Feature) wanted() bool {
	c := config.Get().CamStream
	return c.On && c.Pass != "" && f.available()
}

// Run keeps the port open while the camera is to be served, and shut while it is not.
func (f *Feature) Run(ctx context.Context) error {
	var stopDiscovery context.CancelFunc
	defer func() {
		if stopDiscovery != nil {
			stopDiscovery()
		}
	}()
	defer f.close()
	defer onCover(func() { f.dropAll(); f.endPages() })()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		switch want := f.wanted(); {
		case want && f.listener() == nil:
			ln, err := net.Listen("tcp", ":"+Port)
			if err != nil {
				slog.Error("camera streaming: the port could not be opened", "err", err)
			} else {
				f.mu.Lock()
				f.ln = ln
				f.mu.Unlock()
				slog.Info("camera streaming: on", "port", Port)
				go f.serve(ln)
			}
		case !want && f.listener() != nil:
			f.close()
			slog.Info("camera streaming: off")
		}
		// WS-Discovery while the stream is served and its own switch is on.
		switch discover := f.listener() != nil && config.Get().CamStream.Discovery; {
		case discover && stopDiscovery == nil:
			dctx, cancel := context.WithCancel(ctx)
			stopDiscovery = cancel
			go f.discover(dctx)
			slog.Info("camera streaming: answering discovery")
		case !discover && stopDiscovery != nil:
			stopDiscovery()
			stopDiscovery = nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		case <-tick.C:
		}
	}
}

func (f *Feature) listener() net.Listener {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ln
}

// close shuts the port and every connection made through it.
func (f *Feature) close() {
	f.mu.Lock()
	ln := f.ln
	f.ln = nil
	f.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	f.dropAll()
}

// dropAll ends every connection; each session lets its pictures go as it ends.
func (f *Feature) dropAll() {
	f.mu.Lock()
	conns := make([]net.Conn, 0, len(f.conns))
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// serve takes connections until the listener is closed.
func (f *Feature) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if !f.admit(ln, conn, hostOf(conn.RemoteAddr())) {
			_ = conn.Close()
			continue
		}
		go f.session(conn)
	}
}

// waiter is a connection that has not logged in yet: where from, and since when.
type waiter struct {
	from string
	at   time.Time
}

// admit counts a new connection as one that has not logged in, unless its address has as many waiting
// as it may, its address is locked out, or the listener it came in on has been shut since. With every
// place taken, the connection that has waited longest is let go for it: a recorder logs in at once, so
// what has waited longest is what never meant to.
func (f *Feature) admit(ln net.Listener, conn net.Conn, from string) bool {
	if f.guard.refused(from) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ln != ln || f.waiting[from] >= maxPendingFrom {
		return false
	}
	if f.pending >= maxPending {
		var oldest net.Conn
		var at time.Time
		for c, w := range f.since {
			if oldest == nil || w.at.Before(at) {
				oldest, at = c, w.at
			}
		}
		if oldest == nil {
			return false
		}
		f.unwait(oldest)
		_ = oldest.Close()
	}
	f.pending++
	f.waiting[from]++
	f.since[conn] = waiter{from: from, at: time.Now()}
	f.conns[conn] = struct{}{}
	return true
}

// loggedIn moves a connection from waiting to logged in.
func (f *Feature) loggedIn(conn net.Conn) {
	f.mu.Lock()
	f.unwait(conn)
	f.mu.Unlock()
}

// gone forgets a connection.
func (f *Feature) gone(conn net.Conn) {
	f.mu.Lock()
	delete(f.conns, conn)
	f.unwait(conn)
	f.mu.Unlock()
}

// unwait takes a connection off the waiting, once, if it is on it; f.mu is held.
func (f *Feature) unwait(conn net.Conn) {
	w, ok := f.since[conn]
	if !ok {
		return
	}
	delete(f.since, conn)
	f.pending--
	if f.waiting[w.from]--; f.waiting[w.from] <= 0 {
		delete(f.waiting, w.from)
	}
}

// subscribe hands a viewer the pictures, starting the camera for the first one; an H.264 stream is asked
// for a keyframe for each, to start at.
func (f *Feature) subscribe() (<-chan sendable, func(), error) {
	f.startMu.Lock()
	defer f.startMu.Unlock()
	f.mu.Lock()
	n := len(f.subs)
	f.mu.Unlock()
	if n >= maxViewers {
		return nil, nil, errFull
	}
	if n == 0 && f.h264() {
		release, err := camera.Get().Acquire()
		if err != nil {
			return nil, nil, err
		}
		stop := make(chan struct{})
		var once sync.Once
		f.mu.Lock()
		f.stopPic = func() { once.Do(func() { close(stop) }) }
		f.mu.Unlock()
		go f.pumpH264(stop, release)
	} else if n == 0 {
		frames, release, err := f.frames()
		if err != nil {
			return nil, nil, err
		}
		stop := make(chan struct{})
		var once sync.Once
		f.mu.Lock()
		f.stopPic = func() { once.Do(func() { close(stop); release() }) }
		f.mu.Unlock()
		go f.pump(frames, stop)
	}
	depth := 1
	if f.h264() {
		depth = h264Queue
	}
	ch := make(chan sendable, depth)
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	key := f.forceKey
	f.mu.Unlock()
	if key != nil {
		key()
	}
	f.Changed.Emit(struct{}{})
	return ch, func() { f.unsubscribe(ch) }, nil
}

// unsubscribe lets a viewer go, and the camera with the last one.
func (f *Feature) unsubscribe(ch chan sendable) {
	f.startMu.Lock()
	defer f.startMu.Unlock()
	f.mu.Lock()
	if _, ok := f.subs[ch]; !ok {
		f.mu.Unlock()
		return
	}
	delete(f.subs, ch)
	delete(f.lost, ch)
	close(ch)
	var stop func()
	if len(f.subs) == 0 {
		stop, f.stopPic = f.stopPic, nil
	}
	f.mu.Unlock()
	if stop != nil {
		stop()
	}
	f.Changed.Emit(struct{}{})
}

// pump encodes the camera's pictures, at most maxFPS a second, and hands each to every viewer, the newest
// only to one that is behind. When the camera stops by itself, every viewer is let go. A frame that
// comes too soon is kept and sent when its time comes, so the last one always goes out: when the
// sensor stops for the mute, its last frame is the black one, and none follows it.
func (f *Feature) pump(frames <-chan frame, stop <-chan struct{}) {
	var last time.Time
	var held frame
	var due <-chan time.Time
	for {
		var develop frame
		select {
		case <-stop:
			return
		case <-due:
			select {
			case <-stop: // both ready: stopping wins
				return
			default:
			}
			develop, held, due = held, nil, nil
		case next, ok := <-frames:
			if !ok {
				f.letAllGo()
				return
			}
			if wait := time.Second/maxFPS - time.Since(last); wait > 0 {
				held = next // the newest waiting one
				if due == nil {
					due = time.After(wait)
				}
				continue
			}
			develop, held, due = next, nil, nil
		}
		last = time.Now()
		img := develop()
		if img == nil {
			continue
		}
		b, err := encode(eighths(img))
		if err != nil {
			continue
		}
		pic, err := rtpjpeg.Parse(b)
		if err != nil {
			slog.Warn("camera streaming: a picture that cannot be sent", "err", err)
			continue
		}
		f.hand(pic)
	}
}

// hand gives a picture to every viewer: a JPEG viewer that is behind gets the newest only; an H.264 viewer
// that is a whole queue behind loses what is queued and starts again at the next keyframe.
func (f *Feature) hand(pic sendable) {
	k, isKeyed := pic.(keyed)
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		if isKeyed {
			if len(ch) == cap(ch) {
				f.lost[ch] = true
				for len(ch) > 0 {
					<-ch
				}
			}
			if f.lost[ch] {
				if !k.keyframe() {
					continue
				}
				delete(f.lost, ch)
			}
			ch <- pic
			continue
		}
		select {
		case <-ch: // behind: the older picture goes
		default:
		}
		ch <- pic
	}
}

// letAllGo lets every viewer go, the camera having stopped.
func (f *Feature) letAllGo() {
	f.mu.Lock()
	subs := make([]chan sendable, 0, len(f.subs))
	for ch := range f.subs {
		subs = append(subs, ch)
	}
	f.mu.Unlock()
	for _, ch := range subs {
		f.unsubscribe(ch)
	}
}

// offered is a camera, on the Linux image: under Android, the vendor's firewall drops the port, and the
// camera is Android's.
func offered() bool { return camera.Available() && !layout.OnAndroid() }

// cameraFrames starts the camera and hands its pictures over, the newest only.
func cameraFrames() (<-chan frame, func(), error) {
	release, err := camera.Get().Acquire()
	if err != nil {
		return nil, nil, err
	}
	out := make(chan frame, 1)
	stop := camera.Get().Frames.Listen(func(fr *camera.Frame) {
		next := func() image.Image {
			if img := fr.Image(); img != nil {
				return img
			}
			return nil // a typed nil would not compare as nil
		}
		// The stream is slower than the sensor: skip, never queue, but keep the newest (the black
		// frame of a sensor that stopped is the last one; see camera.ListenLatest).
		for {
			select {
			case out <- next:
				return
			default:
			}
			select {
			case <-out:
			default:
			}
		}
	})
	return out, func() { stop(); release() }, nil
}

// encode is a picture as the stream's JPEG.
func encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// eighths is a picture cropped to a size RTP/JPEG can say: a multiple of 8 across and down.
func eighths(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx()&^7, b.Dy()&^7
	if w == b.Dx() && h == b.Dy() {
		return img
	}
	if sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(image.Rect(b.Min.X, b.Min.Y, b.Min.X+w, b.Min.Y+h))
	}
	return img
}

// hostOf is an address without its port.
func hostOf(a net.Addr) string {
	if a == nil {
		return ""
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}
