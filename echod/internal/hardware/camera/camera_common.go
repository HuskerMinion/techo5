//go:build !dot

package camera

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/privacy"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// What every camera shares: users acquire the sensor, frames are handed out through a hook, and the
// sensor stops a little after the last user lets go. The device files (camera.go for the Show's
// OV02B10, camera_spot.go for the Spot's GC0312) supply open, the device's stream and autoExpose,
// convert and Frame.Full, and the tone a frame was levelled with.

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
	running bool
	powered bool // the sensor is on (running, and not held off by the mute)
	stop    chan struct{}
	stopped chan struct{}
	idle    *time.Timer
	last    *Frame
	err     error // why the last start failed, for callers waiting on a frame
	seq     uint64

	// stopping is the stopped channel of a run that is on its way out: running is already clear,
	// but the goroutine still owns the sensor until it closes. Acquire waits on it. See idleStop.
	stopping chan struct{}

	// owner is what runs the sensor, so that a test can drive the lifecycle on a machine with no
	// camera. Nil is the real thing, run.
	owner func(stop, stopped chan struct{})

	// wedged is the sensor having gone away in a manner nothing here can undo. See ErrNeedsReboot.
	wedged error
}

// ErrNeedsReboot is the sensor refusing to open in a way that only a reboot clears.
//
// The mute latch cuts the camera's power without telling the sensor driver, which goes on believing
// the sensor is powered; the power-down it runs before the next power-on then fails on VCAMD and
// takes the open with it. Every open after that returns EIO, for the life of the boot. The fix
// belongs in the kernel - amazon-gating cutting the camera behind imgsensor's back - and this is
// only about not sitting in the failure: it was found as three and a half hours of the same error
// every twenty seconds, one per still Home Assistant asked for.
var ErrNeedsReboot = errors.New("the camera needs a reboot: the sensor did not come back after the microphone latch")

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
// A muted device refuses: the mute button is the camera's off switch too.
func (c *Camera) Acquire() (release func(), err error) {
	if !Available() {
		return nil, errors.New("no camera on this device")
	}
	if m, err := privacy.Microphone(); err == nil {
		if muted, err := m.Get(); err == nil && muted {
			return nil, errors.New("privacy is on")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// A stop that is under way still owns the sensor. idleStop clears running as soon as it has
	// asked the run goroutine to finish, but that goroutine takes the best part of a mute poll to
	// come out of the stream and close the ISP. Opening in that window puts the sensor in two
	// hands at once, and the goroutine on its way out then tears CSI2 down underneath the new
	// stream; if imgsensor answers the next open with EIO the camera is written off for the rest
	// of the boot. So wait the stop out, with the lock dropped so it can finish.
	for c.stopping != nil {
		gone := c.stopping
		c.mu.Unlock()
		<-gone
		c.mu.Lock()
		if c.stopping == gone {
			c.stopping = nil
		}
	}

	// Once it is gone it is gone: opening again only produces the same error, and the caller is
	// better told what would fix it than handed a bare I/O error every twenty seconds.
	if c.wedged != nil {
		return nil, c.wedged
	}
	c.users++
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	if !c.running {
		c.running = true
		c.err = nil
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
	c.mu.Lock()
	after := c.seq
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
			c.mu.Lock()
			err := c.err
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case <-tick.C:
			c.mu.Lock()
			err := c.err
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
		}
	}
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
			c.mu.Lock()
			c.err = err
			c.running = false
			// EIO here is the sensor believing it is still powered after the latch cut it, which no
			// amount of asking again will change. Said once, loudly, rather than at every poll.
			if errors.Is(err, syscall.EIO) {
				c.wedged = ErrNeedsReboot
				c.mu.Unlock()
				slog.Error("camera will not open again until this device is rebooted",
					"err", err, "why", "the microphone latch cut the sensor's power behind its driver")
				return
			}
			c.mu.Unlock()
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
			if d.skip() {
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
