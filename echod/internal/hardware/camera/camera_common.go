//go:build !dot

package camera

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"os"
	"sync"
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
}

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

// Available reports whether this device has the camera nodes.
func Available() bool {
	for _, p := range []string{"/dev/camera-isp", "/dev/kd_camera_hw", "/dev/ion", "/proc/m4u"} {
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
		go c.run(c.stop, c.stopped)
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

func (c *Camera) idleStop() {
	c.mu.Lock()
	if c.users != 0 || !c.running {
		c.mu.Unlock()
		return
	}
	stop, stopped := c.stop, c.stopped
	c.running = false
	c.mu.Unlock()
	close(stop)
	<-stopped
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
			slog.Error("camera start", "err", err)
			c.mu.Lock()
			c.err = err
			c.running = false
			c.mu.Unlock()
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
