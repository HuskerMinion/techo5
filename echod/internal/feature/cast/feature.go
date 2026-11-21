//go:build !dot && !spot

package cast

import (
	"context"
	"errors"
	"image"
	"image/draw"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/android/firewall"
	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(38))
}

// Port is where phones connect; Service is what they browse for.
const (
	Port    = 8940
	Service = "_techo5cast._tcp"

	// latency is how long the device buffers: what smooths the network, and how far behind the phone
	// the picture and sound are. A quarter second is well past what wi-fi jitter needs, and short enough
	// that a video does not feel far off.
	latency = 250 * time.Millisecond

	// askTimeout is how long a phone waits for somebody at the device to accept it. The phone's own wait
	// for the welcome is longer than this.
	askTimeout = 20 * time.Second

	// titleFor is how long "Casting: title" stays over the picture when a cast starts.
	titleFor = 4 * time.Second
)

// Feature is the device's cast receiver: a switch, a key, the port, and what is on the screen while a
// phone is casting.
type Feature struct {
	// Changed fires when there is something new to draw or the cast started or stopped.
	Changed hook.Hook[struct{}]

	enabled *esphome.Switch
	state   *esphome.TextSensor
	audio   *audioOut

	mu      sync.Mutex
	running context.CancelFunc
	wake    chan struct{}

	// The screen's size, told by the display; a cast is refused until it is known.
	w, h int

	// A phone waiting to be accepted: its name, and where the answer goes. Empty when none is.
	asking string
	answer chan bool

	// The names phones have given, newest last and few: they are a person's, often, and the log says
	// them, so a diagnostics bundle has to know them to take them out (feature/diag).
	seen []string

	// When the pairing code was asked for; zero when it is not on the screen.
	pairing time.Time

	// While casting: the phone, the newest frame, and how many have come.
	phone   string
	title   string
	titleAt time.Time
	scale   int // 2 when frames arrive at half size, to be drawn doubled
	frame   *image.RGBA
	version uint64
	recv    *Receiver

	// What has been shown since the last line in the log.
	shown     int
	painted   int // frames the display drew, which can be fewer: it draws the newest
	lastLog   time.Time
	lastAudio [2]int
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Feature {
	f := &Feature{audio: newAudioOut(speaker.Get()), wake: make(chan struct{}, 1)}
	f.enabled = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "cast", Name: "Cast", Icon: "mdi:cast",
			Category: esphome.CategoryConfig, DeviceID: component.DevicePlayback,
		},
		OnCommand: f.SetEnabled,
	}
	f.state = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "cast_state", Name: "Cast state", Icon: "mdi:cast-connected",
			Category: esphome.CategoryDiagnostic, DeviceID: component.DevicePlayback,
		},
	}
	f.state.Set("off")
	return f
}

func (f *Feature) Name() string { return "cast" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.enabled, f.state} }

func (f *Feature) Restore(c config.Config) {
	f.enabled.Set(c.Cast.Enabled)
}

// SetEnabled turns the receiver on or off: the switch in Home Assistant and the row on the screen.
func (f *Feature) SetEnabled(on bool) {
	f.enabled.Set(on)
	if err := config.Set().Cast().Enabled(on); err != nil {
		slog.Error("saving a setting failed", "setting", f.enabled.ObjectID, "err", err)
	}
	f.rethink()
}

// pairingFor is how long the pairing code stays on the screen if nobody touches it.
const pairingFor = 2 * time.Minute

// ShowPairing puts the pairing code on the screen, or takes it away. There has to be a key to show: one
// is made if there is none, so that turning Cast on at the device is all it takes to pair a phone.
func (f *Feature) ShowPairing(on bool) {
	if on {
		ensureKey()
	}
	f.mu.Lock()
	f.pairing = time.Time{}
	if on {
		f.pairing = time.Now()
	}
	f.mu.Unlock()
	if on {
		time.AfterFunc(pairingFor+100*time.Millisecond, func() { f.Changed.Emit(struct{}{}) })
	}
	f.Changed.Emit(struct{}{})
}

// PairingShown is whether the pairing code is to be on the screen: asked for, not yet timed out, and no
// cast has taken the screen since.
func (f *Feature) PairingShown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.pairing.IsZero() && time.Since(f.pairing) < pairingFor && f.phone == "" && f.asking == ""
}

// PairingLink is what the code holds, or empty if the device has no address or key to give.
func (f *Feature) PairingLink() string {
	c := config.Get()
	host := hostOf(metrics.Addresses())
	if host == "" || c.Cast.Key == "" {
		return ""
	}
	return PairingURL(host, Port, c.Cast.Key, c.Device.Name)
}

// seenMax is how many phone names are kept for the diagnostics bundle.
const seenMax = 16

// remember keeps a phone's name for SeenNames.
func (f *Feature) remember(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if slices.Contains(f.seen, name) {
		return
	}
	if len(f.seen) == seenMax {
		f.seen = f.seen[1:]
	}
	f.seen = append(f.seen, name)
}

// SeenNames is the names phones have given since the daemon started.
func (f *Feature) SeenNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// Asking is the name of the phone waiting to be accepted, or "" if none is: the screen puts the
// question up while it is not empty.
func (f *Feature) Asking() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asking
}

// Decide answers the question on the screen: true lets the phone cast, false sends it away.
func (f *Feature) Decide(accept bool) {
	f.mu.Lock()
	ch := f.answer
	f.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- accept:
	default:
	}
}

// consent waits for somebody at the device to accept the phone. It always asks: the name is the
// phone's own word and every phone shares the key, so there is nothing to remember a phone by.
func (f *Feature) consent(name string) error {
	f.remember(name)
	ch := make(chan bool, 1)
	f.mu.Lock()
	f.asking, f.answer = name, ch
	f.mu.Unlock()
	f.state.Set("asking: " + name)
	f.Changed.Emit(struct{}{})
	slog.Info("cast: asking", "phone", name)
	defer func() {
		f.mu.Lock()
		f.asking, f.answer = "", nil
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}()

	select {
	case yes := <-ch:
		if !yes {
			f.state.Set("waiting")
			return errors.New("declined")
		}
	case <-time.After(askTimeout):
		f.state.Set("waiting")
		return errors.New("nobody accepted it on the device")
	}
	return nil
}

// Actions: the pairing key is a secret, so it is an action's argument and not an entity's state, which
// Home Assistant keeps. Nobody has to set one: the device makes its own when Cast is first turned on, and
// the pairing code on its screen carries it to a phone. This action is for choosing one, or, with no
// key, for throwing the old one away and making a new one, which unpairs every phone.
func (f *Feature) Actions() []*esphome.Action {
	return []*esphome.Action{{
		Name: "cast_key",
		Args: []esphome.Arg{{Name: "key", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			key := strings.TrimSpace(c.String("key"))
			if key != "" && len(key) < 8 {
				return nil, errors.New("the cast key has to be at least 8 characters, or empty to make a new random one")
			}
			if key == "" {
				key = NewKey()
			}
			slog.Info("cast: key set")
			return nil, config.Set().Cast().Key(key)
		},
	}}
}

// ensureKey gives the device a key if it has none, so that turning Cast on is all the setup there is.
func ensureKey() {
	if config.Get().Cast.Key != "" {
		return
	}
	if err := config.Set().Cast().Key(NewKey()); err != nil {
		slog.Error("saving a setting failed", "setting", "cast key", "err", err)
		return
	}
	slog.Info("cast: made a key; the pairing code on the screen shows it")
}

// SetScreen is told by the display what size a frame is to be.
func (f *Feature) SetScreen(w, h int) {
	f.mu.Lock()
	f.w, f.h = w, h
	f.mu.Unlock()
}

// Active is whether a phone is casting.
func (f *Feature) Active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phone != ""
}

// Stop ends the cast, if there is one: what the screen does to take it down.
func (f *Feature) Stop() {
	f.mu.Lock()
	r := f.recv
	f.mu.Unlock()
	if r != nil {
		r.Stop("stopped on the device")
	}
}

// Title is what the phone said is playing, for the first moments of a cast; empty after that.
func (f *Feature) Title() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phone == "" || time.Since(f.titleAt) > titleFor {
		return ""
	}
	return f.title
}

// Draw puts the newest frame into dst and reports whether there was one.
func (f *Feature) Draw(dst *image.RGBA) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.frame == nil {
		return false
	}
	draw.Draw(dst, dst.Rect, f.frame, image.Point{}, draw.Src)
	f.painted++
	return true
}

func (f *Feature) rethink() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run holds the port for as long as the switch is on.
func (f *Feature) Run(ctx context.Context) error {
	defer f.close()
	for {
		f.settle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		}
	}
}

func (f *Feature) settle(parent context.Context) {
	want := config.Get().Cast.Enabled
	f.mu.Lock()
	already := f.running != nil
	f.mu.Unlock()
	switch {
	case want && already, !want && !already:
		return
	case !want:
		f.close()
		f.state.Set("off")
		return
	}

	ensureKey()
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(Port)))
	if err != nil {
		slog.Error("cast: listening failed", "port", Port, "err", err)
		f.state.Set("error: " + err.Error())
		return
	}
	ctx, cancel := context.WithCancel(parent)
	f.mu.Lock()
	f.running = cancel
	f.mu.Unlock()

	if err := firewall.Open(firewall.Cast, Port); err != nil {
		slog.Error("opening the cast port failed", "port", Port, "err", err)
	}
	r := &Receiver{Key: func() string { return config.Get().Cast.Key }, Sink: &sink{f: f}}
	f.mu.Lock()
	f.recv = r
	f.mu.Unlock()
	safe.Go("cast listen", func() {
		if err := r.Serve(ctx, ln); err != nil {
			slog.Error("cast listener stopped", "err", err)
		}
	})
	name := config.Get().Device.Name
	safe.Go("cast advertise", func() { advertise(ctx, name) })
	f.state.Set("waiting")
	slog.Info("cast waiting for a phone", "name", name, "port", Port)
}

func (f *Feature) close() {
	f.mu.Lock()
	cancel := f.running
	f.running, f.recv = nil, nil
	f.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	f.Decide(false) // a question still on the screen goes with the port
	if err := firewall.Close(firewall.Cast); err != nil {
		slog.Warn("closing the cast port failed", "err", err)
	}
}

// advertise publishes the device for phones to find, and again when its addresses change.
func advertise(ctx context.Context, name string) {
	for {
		ips := metrics.Addresses()
		if len(ips) > 0 {
			addrs := make([]string, 0, len(ips))
			for _, ip := range ips {
				addrs = append(addrs, ip.String())
			}
			srv, err := zeroconf.RegisterProxy(name, Service, "local.", Port, name, addrs, []string{"name=" + name, "v=1"}, nil)
			if err == nil {
				for metrics.AddressKey(metrics.Addresses()) == metrics.AddressKey(ips) {
					if !sleep(ctx, 3*time.Second) {
						srv.Shutdown()
						return
					}
				}
				srv.Shutdown()
				continue
			}
			slog.Debug("cast advertise failed", "err", err)
		}
		if !sleep(ctx, 3*time.Second) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// sink is the Receiver's Sink: the feature's screen and the speaker.
type sink struct{ f *Feature }

func (s *sink) Begin(h Hello) (Welcome, error) {
	f := s.f
	f.mu.Lock()
	w, hh := f.w, f.h
	f.mu.Unlock()
	if w == 0 || hh == 0 {
		return Welcome{}, errors.New("the screen is not ready")
	}
	if h.Audio && (h.Rate != speaker.Rate || h.Channels != speaker.Channels) {
		return Welcome{}, errors.New("audio has to be 48000 Hz stereo")
	}
	if err := f.consent(h.Name); err != nil {
		return Welcome{}, err
	}
	f.mu.Lock()
	f.phone, f.scale, f.frame, f.version = h.Name, h.Scale, image.NewRGBA(image.Rect(0, 0, w, hh)), 0
	f.title, f.titleAt = h.Title, time.Now()
	f.pairing = time.Time{}
	f.shown, f.painted, f.lastLog = 0, 0, time.Now()
	f.mu.Unlock()
	if h.Audio {
		f.audio.open()
		speaker.Sound().Backgrounds().Took(f.audio)
	}
	f.state.Set("casting: " + h.Name)
	f.Changed.Emit(struct{}{})
	if h.Title != "" {
		// The screen redraws on news; there is none when the picture is still, so say when the title is
		// to go.
		time.AfterFunc(titleFor+100*time.Millisecond, func() { f.Changed.Emit(struct{}{}) })
	}
	return Welcome{OK: true, W: w, H: hh, Rate: speaker.Rate, Channel: speaker.Channels,
		LatencyMs: int(latency / time.Millisecond)}, nil
}

func (s *sink) Frame(img image.Image) {
	f := s.f
	f.mu.Lock()
	if f.frame != nil {
		if f.scale == 2 {
			doubleInto(f.frame, img)
		} else {
			b := img.Bounds()
			at := image.Pt((f.frame.Rect.Dx()-b.Dx())/2, (f.frame.Rect.Dy()-b.Dy())/2)
			draw.Draw(f.frame, image.Rectangle{Min: at, Max: at.Add(b.Size())}, img, b.Min, draw.Src)
		}
		f.version++
		f.shown++
	}
	report := f.shown > 0 && time.Since(f.lastLog) >= 5*time.Second
	var n, drawn int
	var since time.Duration
	if report {
		n, drawn, since = f.shown, f.painted, time.Since(f.lastLog)
		f.shown, f.painted, f.lastLog = 0, 0, time.Now()
	}
	f.mu.Unlock()
	if report {
		late, dropped := f.audio.misses()
		slog.Info("cast", "decoded_fps", float64(n)/since.Seconds(), "painted_fps", float64(drawn)/since.Seconds(), "audio_late", late, "audio_dropped", dropped)
	}
	f.Changed.Emit(struct{}{})
}

func (s *sink) Audio(pcm []byte, at time.Time) { s.f.audio.write(pcm, at) }

func (s *sink) AudioMisses() (late, dropped int) { return s.f.audio.misses() }

func (s *sink) End() {
	f := s.f
	speaker.Sound().Backgrounds().Gave(f.audio)
	f.audio.close()
	f.mu.Lock()
	f.phone, f.frame = "", nil
	f.mu.Unlock()
	f.state.Set("waiting")
	f.Changed.Emit(struct{}{})
}

// doubleInto draws img into dst at twice its size, each pixel as four, centered. It is soft, and it is
// a fraction of the decoding a full-size frame costs.
func doubleInto(dst *image.RGBA, img image.Image) {
	b := img.Bounds()
	small := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(small, small.Rect, img, b.Min, draw.Src)
	ox, oy := (dst.Rect.Dx()-2*b.Dx())/2, (dst.Rect.Dy()-2*b.Dy())/2
	for y := 0; y < small.Rect.Dy(); y++ {
		ty := oy + 2*y
		if ty < 0 || ty+1 >= dst.Rect.Dy() {
			continue
		}
		src := small.Pix[y*small.Stride : y*small.Stride+small.Rect.Dx()*4]
		r0 := dst.Pix[ty*dst.Stride : (ty+1)*dst.Stride]
		for x := 0; x*4 < len(src); x++ {
			tx := ox + 2*x
			if tx < 0 || tx+1 >= dst.Rect.Dx() {
				continue
			}
			p := src[x*4 : x*4+4]
			copy(r0[tx*4:tx*4+4], p)
			copy(r0[tx*4+4:tx*4+8], p)
		}
		lo, hi := max(ox, 0)*4, min(ox+2*small.Rect.Dx(), dst.Rect.Dx())*4
		if hi > lo {
			copy(dst.Pix[(ty+1)*dst.Stride+lo:(ty+1)*dst.Stride+hi], r0[lo:hi])
		}
	}
}
