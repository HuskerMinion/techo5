//go:build !dot

package camera

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/lenscover"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/privacy"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// What every camera shares: users acquire the sensor, frames are handed out through a hook, and the
// sensor stops a little after the last user lets go. The device files (camera.go for the Show's
// OV02B10, camera_spot.go for the Spot's GC0312) supply open, the device's stream and autoExpose,
// convert and Frame.Full, and the tone a frame was leveled with.

// Frame is one picture. Nothing is developed until somebody asks: the sensor hands over more
// frames than anything on the network or the screen keeps up with, and a frame that is dropped
// should cost no more than the copy that kept it.
type Frame struct {
	Seq uint64
	At  time.Time

	mu    sync.Mutex
	raw   []byte // the sensor's frame as it came
	rgba  *image.RGBA
	tone  tone
	toned bool
}

// Image is the frame as a Width x Height picture, developed once however many ask for it.
func (f *Frame) Image() *image.RGBA {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rgba == nil && f.raw != nil {
		f.rgba = render(f.raw, f.level())
	}
	return f.rgba
}

// Luma is the frame as a w x h grid of brightness (0 to 255), read straight off the sensor's own
// frame without developing the picture: enough to see something move, at a tiny fraction of the
// work. Nil for a frame with nothing in it (one sent while the camera was held off).
func (f *Frame) Luma(w, h int) []uint8 {
	if f.raw == nil || w <= 0 || h <= 0 {
		return nil
	}
	return lumaGrid(f.raw, w, h)
}

// level is the tone the frame was measured at, with f.mu held.
func (f *Frame) level() tone {
	if !f.toned {
		f.tone, f.toned = stats(f.raw), true
	}
	return f.tone
}

// Camera is the device. Get returns the one instance.
type Camera struct {
	// Frames fires with every converted frame while the sensor runs. Listeners must not block.
	Frames hook.Hook[*Frame]

	mu      sync.Mutex
	users   int
	fast    int           // users that want every frame; the rest (AcquireSlow) take one every slowEvery
	sent    time.Time     // when the last frame went out, for the slow users
	every   time.Duration // how often the slow users get one (SetSlowEvery); zero is slowEvery
	running bool
	powered bool // the sensor is on (running, and not held off by the mute)
	stop    chan struct{}
	stopped chan struct{}
	idle    *time.Timer
	last    *Frame
	err     error // why a start failed, for callers waiting on a frame. See errFrom.
	seq     uint64

	// errFrom says which start err came from: the stopped channel of the run that failed to open.
	// An error belongs to the start that produced it and to nobody else. A Snapshot arriving while
	// the sensor is already running used to read err straight off the camera on its first tick and
	// hand back whatever a start minutes ago had failed with, without ever giving the healthy stream
	// it was actually waiting on a chance to produce a frame - the camera that "fails, then works
	// next time". A waiter notes the run it is waiting on and looks at err only when this matches, so
	// an old failure is simply not its business. Each run has its own stopped channel, so the channel
	// is the run's name.
	errFrom chan struct{}

	// stopping is the stopped channel of a run that is on its way out: running is already clear,
	// but the goroutine still owns the sensor until it closes. Acquire waits on it. See idleStop.
	stopping chan struct{}

	// owner is what runs the sensor, so that a test can drive the lifecycle on a machine with no
	// camera. Nil is the real thing, run.
	owner func(stop, stopped chan struct{})

	// wedged is the sensor having gone away in a manner nothing here can undo. See ErrNeedsReboot.
	wedged error
}

// ErrNeedsReboot is the sensor refusing to open because the kernel's camera driver still believes
// the privacy latch is on. On the Show 8 and the 1st gen Show 5 (amazon-gating, OV9734), the latch is
// let go in hardware by any press of the mute button, but the driver tells the camera so only on a
// long press: a short press to unmute leaves the camera's own "gating mode" on, and every open fails
// ("Failed to enable CAM, GATING Mode is ON", then a power-down that unbalances the sensor's
// regulator) however long after. Measured on a Show 8, 2026-10-04: opens at 0.15 s, 1.6 s and 72 s
// after a short unmute all failed; a hold of the mute button for a second (Unwedge) brought the
// camera straight back, without muting. The kernel's fix is amazon-oss android_kernel_amazon_mt8163
// fac5c8e ("Fix camera dying when booting with privacy on"). A reboot clears it too. The 2nd gen
// Show 5 (cronos) has another privacy driver and never gets here.
var ErrNeedsReboot = errors.New("the camera is held off since the mute button was tapped: hold the mute button for a second, or restart the device")

var (
	once   sync.Once
	shared *Camera
)

func Get() *Camera {
	once.Do(func() { shared = &Camera{} })
	return shared
}

const (
	// linger is how long the sensor keeps running after its last user let go.
	linger = 5 * time.Second

	// frameWait is how long Snapshot gives the sensor to produce a frame from cold.
	frameWait = 6 * time.Second
)

// stopWait is how long Acquire gives a stop that is still on its way out. A stop takes the best part
// of a mute poll, so this is many times what a healthy one needs and still short enough that a
// caller is answered rather than left hanging. It is a variable so a test can drive a wedged stop
// without sitting through it.
var stopWait = 2 * time.Second

// errStopStuck is a stop that has not finished in stopWait: the run goroutine is somewhere in the
// driver that has not come back, and until it does the sensor is still in its hands.
var errStopStuck = errors.New("the camera is still shutting down: the last stream has not let the sensor go")

// nodes are the device files the camera is driven through. It is a variable so that a test can
// point it at something that exists everywhere and exercise the lifecycle off the device.
var nodes = []string{"/dev/camera-isp", "/dev/kd_camera_hw", "/dev/ion", "/proc/m4u"}

// Available reports whether this device has the camera nodes.
func Available() bool {
	for _, p := range nodes {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// Acquire starts the sensor if it is not running and keeps it running until release is called.
// A muted device refuses: the mute button is the camera's off switch too. So does one whose shutter
// is closed, on the devices that have one — there is nothing behind it to photograph, and saying so
// is more use than powering the sensor up to stream a picture of a piece of plastic.
func (c *Camera) Acquire() (release func(), err error) { return c.acquire(true) }

// AcquireSlow is Acquire for a user that wants a frame now and then rather than every one: what
// watches the room for somebody coming near. While only slow users hold the sensor, a frame goes out
// every slowEvery, and the rest are not even copied; the exposure still follows every one.
func (c *Camera) AcquireSlow() (release func(), err error) { return c.acquire(false) }

// slowEvery is how often the slow users get a frame unless one asks for more (SetSlowEvery).
const slowEvery = 500 * time.Millisecond

// SetSlowEvery is how often the slow users get a frame: more often while a gesture could mean
// something, back to slowEvery after. Zero is slowEvery.
func (c *Camera) SetSlowEvery(d time.Duration) {
	if d <= 0 {
		d = slowEvery
	}
	c.mu.Lock()
	c.every = d
	c.mu.Unlock()
}

// wanted says whether the frame the sensor just made goes out, and marks it sent if so.
func (c *Camera) wanted(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	every := c.every
	if every <= 0 {
		every = slowEvery
	}
	if c.fast == 0 && now.Sub(c.sent) < every-every/8 {
		return false
	}
	c.sent = now
	return true
}

func (c *Camera) acquire(fast bool) (release func(), err error) {
	if !Available() {
		return nil, errors.New("no camera on this device")
	}
	if m, err := privacy.Microphone(); err == nil {
		if muted, err := m.Get(); err == nil && muted {
			return nil, errors.New("privacy is on")
		}
	}
	if lenscover.Get().Covered() {
		return nil, errors.New("the lens cover is closed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// A stop that is under way still owns the sensor. idleStop clears running as soon as it has
	// asked the run goroutine to finish, but that goroutine takes the best part of a mute poll to
	// come out of the stream and close the ISP. Opening in that window puts the sensor in two
	// hands at once, and the goroutine on its way out then tears CSI2 down underneath the new
	// stream; if imgsensor answers the next open with EIO the camera is written off for the rest
	// of the boot. So wait the stop out, with the lock dropped so it can finish.
	//
	// The wait is bounded, because the run goroutine can be inside a driver call that never returns
	// — an ioctl on a sensor the latch took away is the one that has been seen — and an unbounded
	// wait would hang every caller of a camera that is never coming back, including the ones that
	// used to be told no straight away. On expiry the answer is no rather than yes: refusing costs
	// the caller a still, opening a sensor the old stream has not let go of costs the camera for the
	// rest of the boot. A later Acquire waits again rather than being refused on sight, since a
	// driver call that has not returned in stopWait may still return.
	for c.stopping != nil {
		gone := c.stopping
		c.mu.Unlock()
		timer := time.NewTimer(stopWait)
		select {
		case <-gone:
			timer.Stop()
			c.mu.Lock()
			if c.stopping == gone {
				c.stopping = nil
			}
		case <-timer.C:
			c.mu.Lock()
			if c.stopping != gone {
				break // some other stop took its place while this one was waited on
			}
			select {
			case <-gone:
				// It finished as the wait ran out. Calling that stuck would refuse a camera that
				// is free again, so the close wins over the clock.
				c.stopping = nil
			default:
				return nil, errStopStuck
			}
		}
	}

	// Once it is gone it is gone: opening again only produces the same error, and the caller is
	// better told what would fix it than handed a bare I/O error every twenty seconds.
	if c.wedged != nil {
		return nil, c.wedged
	}
	c.users++
	if fast {
		c.fast++
		liveUsers.Add(1)
	}
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	if !c.running {
		c.running = true
		// The new run has a stopped channel of its own, so nothing can mistake the last run's error
		// for this one's; dropping it here is only so a dead error is not kept alive for the life of
		// the process.
		c.err, c.errFrom = nil, nil
		c.stop = make(chan struct{})
		c.stopped = make(chan struct{})
		run := c.owner
		if run == nil {
			run = c.run
		}
		go run(c.stop, c.stopped)
	}
	var done sync.Once
	return func() {
		done.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.users--
			if fast {
				c.fast--
				liveUsers.Add(-1)
			}
			if c.users == 0 {
				c.idle = time.AfterFunc(linger, c.idleStop)
			}
		})
	}, nil
}

// idleStop powers the sensor down once nobody has wanted it for a while. It hands the stop to
// stopping before it lets the lock go: clearing running is not enough, because the goroutine keeps
// hold of the hardware until it returns, and an Acquire arriving meanwhile has to wait rather than
// open a sensor that is still somebody else's.
func (c *Camera) idleStop() {
	c.mu.Lock()
	if c.users != 0 || !c.running {
		c.mu.Unlock()
		return
	}
	stop, stopped := c.stop, c.stopped
	c.running = false
	c.stopping = stopped
	c.mu.Unlock()
	close(stop)
	<-stopped
	c.mu.Lock()
	if c.stopping == stopped {
		c.stopping = nil
	}
	c.mu.Unlock()
}

// Snapshot returns the next frame the sensor produces, starting it if need be.
func (c *Camera) Snapshot(ctx context.Context) (*Frame, error) {
	release, err := c.Acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	// The run this snapshot is waiting on: Acquire either started it or joined one that was already
	// going, and either way it is the only start whose failure is this caller's to hear about.
	c.mu.Lock()
	after, mine := c.seq, c.stopped
	c.mu.Unlock()
	got := make(chan *Frame, 1)
	cancel := c.Frames.Listen(func(f *Frame) {
		if f.Seq > after {
			select {
			case got <- f:
			default:
			}
		}
	})
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, frameWait)
	defer stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case f := <-got:
			return f, nil
		case <-ctx.Done():
			if err := c.startErr(mine); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case <-tick.C:
			if err := c.startErr(mine); err != nil {
				return nil, err
			}
		}
	}
}

// startErr is why the start named by stopped failed, or nil. Anything left over from an earlier
// start is somebody else's error and reads as nil here.
func (c *Camera) startErr(stopped chan struct{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stopped == nil || c.errFrom != stopped {
		return nil
	}
	return c.err
}

// Running reports whether the sensor is powered: something holds it, or it is lingering after the
// last user, and the device is not muted. A screen shows that the camera is in use.
func (c *Camera) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.powered
}

// Last is the most recent frame, if the sensor has produced one since it started.
func (c *Camera) Last() *Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// run owns the hardware from start to stop. Muting while it runs powers the sensor down and hands
// out one black frame, so a view on screen goes dark at once; unmuting brings the sensor back for
// whoever still holds it. On the Show the mute latch cuts the camera's power as well; on the Spot the
// mute is software, and this is the camera's half of it.
func (c *Camera) run(stop, stopped chan struct{}) {
	defer close(stopped)
	for {
		if isMuted() {
			c.emit(&Frame{At: time.Now(), rgba: image.NewRGBA(image.Rect(0, 0, Width, Height))})
			slog.Info("camera held off: muted")
			for isMuted() {
				select {
				case <-stop:
					return
				case <-time.After(mutePoll):
				}
			}
		}
		d, err := open()
		if err != nil {
			if c.startFailed(err, stopped) {
				slog.Error("camera held off by the privacy latch: hold the mute button for a second, or restart",
					"err", err)
				return
			}
			slog.Error("camera start", "err", err)
			return
		}
		c.setPowered(true)
		slog.Info("camera running")
		halt := make(chan struct{})
		watched := make(chan struct{})
		go func() {
			defer close(watched)
			for {
				select {
				case <-stop:
					close(halt)
					return
				case <-time.After(mutePoll):
					if isMuted() {
						close(halt)
						return
					}
				}
			}
		}()
		d.stream(halt, func(bayer []byte) {
			d.autoExpose(bayer)
			if d.skip() || !c.wanted(time.Now()) {
				return
			}
			f := &Frame{At: time.Now(), raw: make([]byte, len(bayer))}
			copy(f.raw, bayer)
			c.emit(f)
		})
		<-watched
		d.close()
		c.setPowered(false)
		slog.Info("camera stopped")
		select {
		case <-stop:
			return
		default: // muted: round again, to wait for the unmute
		}
	}
}

// startFailed books a start that could not open the sensor, for the run named by stopped, and says
// whether the camera is now wedged for the rest of the boot.
//
// The error is filed against that run and no other, so a caller waiting on a later start is not
// handed this one's failure. The stop is handed to stopping as well: clearing running on its own
// leaves users where it was, and the next Acquire, seeing somebody still holding a camera that is
// not running, would start a second run while this one is still between here and the defer
// close(stopped) at the top of run. A failed open owns no hardware, so nothing is at stake today;
// a start that got as far as the ISP before giving up would be a second pair of hands on the
// sensor, which is what took the camera out in #17.
//
// Waiting on stopping cannot hang: every path out of a failed open returns, and run closes stopped
// on the way out whatever happened, so an Acquire waiting on it is always let go. Only one run
// exists at a time, since Acquire will not start another until stopping has closed and been
// cleared, so this never puts some other run's channel in the way; and idleStop, the only other
// writer, turns back as soon as it sees running clear.
func (c *Camera) startFailed(err error, stopped chan struct{}) (wedged bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err, c.errFrom = err, stopped
	c.running = false
	c.stopping = stopped
	// EIO here is the sensor believing it is still powered after the latch cut it, which no amount
	// of asking again will change. Said once, loudly, rather than at every poll.
	if errors.Is(err, syscall.EIO) {
		c.wedged = ErrNeedsReboot
		return true
	}
	return false
}

// mutePoll is how often a running camera checks the mute.
const mutePoll = 300 * time.Millisecond

func isMuted() bool {
	m, err := privacy.Microphone()
	if err != nil {
		return false
	}
	muted, err := m.Get()
	return err == nil && muted
}

func (c *Camera) emit(f *Frame) {
	c.mu.Lock()
	c.seq++
	f.Seq = c.seq
	c.last = f
	c.mu.Unlock()
	c.Frames.Emit(f)
}

func (c *Camera) setPowered(on bool) {
	c.mu.Lock()
	c.powered = on
	c.mu.Unlock()
}

// Wedged is why the sensor cannot be opened at all, or nil. It is ErrNeedsReboot once the mute
// latch has taken the sensor away, and nothing clears it but a reboot.
func (c *Camera) Wedged() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wedged
}

// The exposure held through a sudden change, for gestures (feature/presence). A hand over the lens is
// a sudden change of the whole picture, and the exposure would brighten it away within a third of a
// second, leaving nothing to tell it from a light switched off; held still, the hand stays a hand
// (dark, or bright with the screen's own light on it, and smooth) for as long as it is there.

// gestureExposure is whether the exposure is held through sudden changes (SetGestureExposure).
var gestureExposure atomic.Bool

// liveUsers counts the users that want every frame (a picture being looked at): for them the exposure
// follows the room at once, gestures or not.
var liveUsers atomic.Int32

// SetGestureExposure holds the exposure still for a moment whenever the picture's brightness jumps
// by half or more at once, while on.
func SetGestureExposure(on bool) { gestureExposure.Store(on) }

const (
	aeHoldFor = 2500 * time.Millisecond
	// aeWarm is the frames averaged before a jump is believed: a sensor just started is still finding
	// its exposure, and its first frames' swings are its own.
	aeWarm = 10
	// aeGap is a pause in the frames long enough that the sensor was stopped and started again.
	aeGap = 2 * time.Second
)

// aeHold is one device's exposure hold: the brightness it has been seeing, and until when it holds.
type aeHold struct {
	avg   float64
	n     int       // frames averaged since the sensor started
	last  time.Time // the last frame
	until time.Time
}

// held reports whether the exposure is to be left as it is for this frame of brightness mean.
func (h *aeHold) held(mean float64) bool {
	return h.heldAt(mean, time.Now())
}

func (h *aeHold) heldAt(mean float64, now time.Time) bool {
	if !gestureExposure.Load() || liveUsers.Load() > 0 || now.Sub(h.last) > aeGap {
		*h = aeHold{last: now}
		if gestureExposure.Load() && liveUsers.Load() == 0 {
			h.avg, h.n = mean, 1
		}
		return false
	}
	h.last = now
	if !h.until.IsZero() {
		if now.Before(h.until) {
			return true
		}
		// The hold is over: whatever the picture is now is what the room looks like, and the
		// exposure follows it from here rather than holding again against the old brightness.
		h.until, h.avg, h.n = time.Time{}, mean, aeWarm
		return false
	}
	if h.n >= aeWarm && h.avg > 4 && (mean < h.avg/2 || mean > h.avg*2) {
		h.until = now.Add(aeHoldFor)
		return true
	}
	h.n++
	h.avg += (mean - h.avg) * 0.2
	return false
}

// Unwedged fires when the camera may be opened again after being held off (Unwedge). Listeners must
// not block.
var Unwedged hook.Hook[struct{}]

// Unwedge lets the camera be tried again: the mute button was held, which is what tells the kernel's
// camera driver that the privacy latch is off (see ErrNeedsReboot). If it still will not open, the
// next try finds that out again.
func (c *Camera) Unwedge() {
	c.mu.Lock()
	was := c.wedged != nil
	c.wedged = nil
	c.mu.Unlock()
	if was {
		slog.Info("camera: the mute button was held; trying the camera again")
		Unwedged.Emit(struct{}{})
	}
}

// heapNew returns a new T that is certain to live on the heap. The compat ioctls take pointers
// as uint32 fields inside the argument struct, and a stack object behind such a field can move
// when the stack grows without the field following it; heap objects do not move. Converting to
// uintptr does not make an object escape, so it is forced here: escape analysis cannot see
// through the call to a function variable.
func heapNew[T any]() *T {
	p := new(T)
	escape(unsafe.Pointer(p))
	return p
}

var escape = func(unsafe.Pointer) {}

// ptr32 is the address of a heapNew object as the compat ioctl structures carry it.
func ptr32[T any](p *T) uint32 { return uint32(uintptr(unsafe.Pointer(p))) }
