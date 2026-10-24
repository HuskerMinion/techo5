//go:build panel

// Package camera receives the Android front camera from TECHO5 Panel over a same-UID local socket.
// Android opens the sensor only while a viewer has acquired it; no frame is saved to disk.
package camera

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

var Width, Height = panelOutputSize()

const maxFrame = 2 * 1024 * 1024

func panelOutputSize() (int, int) {
	data, err := os.ReadFile(filepath.Join(layout.StateDir, "camera-ready"))
	if err != nil {
		return 640, 480
	}
	var width, height int
	if _, err := fmt.Fscan(bytes.NewReader(data), &width, &height); err != nil ||
		width < 16 || height < 16 || width > 2040 || height > 2040 || width%8 != 0 || height%8 != 0 {
		return 640, 480
	}
	return width, height
}

var (
	once           sync.Once
	shared         *Camera
	ErrNeedsReboot = errors.New("the Android camera is unavailable")
	ErrOff         = errors.New("the Android camera is off")
	Unwedged       hook.Hook[struct{}]
)

type Frame struct {
	Seq      uint64
	At       time.Time
	RGBA     *image.RGBA
	rawJPEG  []byte
	decodeMu sync.Mutex
	width    int
	height   int
	blank    bool
}

func (f *Frame) Full() *image.RGBA { return f.Image() }
func (f *Frame) Image() *image.RGBA {
	f.decodeMu.Lock()
	defer f.decodeMu.Unlock()
	if f.RGBA == nil && len(f.rawJPEG) > 0 {
		decoded, err := jpeg.Decode(bytes.NewReader(f.rawJPEG))
		if err == nil {
			bounds := decoded.Bounds()
			f.RGBA = image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
			draw.Draw(f.RGBA, f.RGBA.Bounds(), decoded, bounds.Min, draw.Src)
		}
	}
	return f.RGBA
}
func (f *Frame) RawJPEG() []byte { return f.rawJPEG }
func (f *Frame) Blank() bool     { return f.blank }
func (f *Frame) Dims() (int, int) {
	if f.RGBA == nil && len(f.rawJPEG) == 0 {
		return 0, 0
	}
	if f.RGBA == nil {
		return f.width, f.height
	}
	b := f.RGBA.Bounds()
	return b.Dx(), b.Dy()
}

func (f *Frame) Luma(w, h int) []uint8 {
	img := f.Image()
	if img == nil || w <= 0 || h <= 0 {
		return nil
	}
	b := img.Bounds()
	out := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		py := b.Min.Y + y*b.Dy()/h
		for x := 0; x < w; x++ {
			px := b.Min.X + x*b.Dx()/w
			r, g, blue, _ := img.At(px, py).RGBA()
			out[y*w+x] = uint8((299*r + 587*g + 114*blue) / 1000 >> 8)
		}
	}
	return out
}

type Camera struct {
	Frames  hook.Hook[*Frame]
	mu      sync.Mutex
	conn    net.Conn
	users   int
	seq     uint64
	last    *Frame
	stillMu sync.Mutex
	still   *Frame
	stillAt time.Time
}

func Get() *Camera {
	once.Do(func() { shared = &Camera{} })
	return shared
}

func Available() bool {
	_, err := os.Stat(filepath.Join(layout.StateDir, "camera-ready"))
	return err == nil
}

func enabled() bool { return config.Get().CamStream.On }

func (c *Camera) Acquire() (func(), error) {
	if !enabled() {
		return nil, ErrOff
	}
	if !Available() {
		return nil, errors.New("Android camera permission or sensor unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !enabled() {
		return nil, ErrOff
	}
	if c.conn == nil {
		conn, err := panelsock.Dial("@techo5.cam")
		if err != nil {
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(7 * time.Second))
		var status [1]byte
		_, err = io.ReadFull(conn, status[:])
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil || status[0] != 'O' {
			_ = conn.Close()
			if err != nil {
				return nil, fmt.Errorf("Android camera did not open: %w", err)
			}
			return nil, errors.New("Android camera did not open")
		}
		c.conn = conn
		go c.read(conn)
	}
	c.users++
	var releaseOnce sync.Once
	return func() { releaseOnce.Do(func() { c.release() }) }, nil
}

func (c *Camera) AcquireSlow() (func(), error) { return c.Acquire() }
func (c *Camera) SetSlowEvery(time.Duration)   {}
func SetGestureExposure(bool)                  {}
func (c *Camera) Unwedge()                     {}
func (c *Camera) Wedged() error                { return nil }

func (c *Camera) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.users > 0 {
		c.users--
	}
	if c.users == 0 && c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Camera) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Disable stops current capture and discards both the latest preview and cached still. A camera
// switched off in Home Assistant must not serve an image captured before that switch changed.
func (c *Camera) Disable() {
	c.mu.Lock()
	conn := c.conn
	c.conn, c.users, c.last = nil, 0, nil
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	c.stillMu.Lock()
	c.still, c.stillAt = nil, time.Time{}
	c.stillMu.Unlock()
}

func (c *Camera) Last() *Frame {
	if !enabled() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *Camera) read(conn net.Conn) {
	var header [4]byte
	for {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			c.ended(conn, err)
			return
		}
		n := binary.BigEndian.Uint32(header[:])
		if n == 0 || n > maxFrame {
			c.ended(conn, fmt.Errorf("invalid camera frame length %d", n))
			return
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(conn, b); err != nil {
			c.ended(conn, err)
			return
		}
		info, err := jpeg.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			c.ended(conn, err)
			return
		}
		if info.Width != Width || info.Height != Height {
			c.ended(conn, fmt.Errorf("unexpected camera frame size %dx%d", info.Width, info.Height))
			return
		}
		c.emit(&Frame{At: time.Now(), rawJPEG: b, width: info.Width, height: info.Height})
	}
}

func (c *Camera) ended(conn net.Conn, err error) {
	c.mu.Lock()
	unexpected := c.conn == conn && c.users > 0
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	_ = conn.Close()
	if unexpected {
		slog.Warn("Android camera stream ended", "err", err)
		c.emit(&Frame{At: time.Now(), RGBA: image.NewRGBA(image.Rect(0, 0, Width, Height)), width: Width, height: Height, blank: true})
	}
}

func (c *Camera) emit(frame *Frame) {
	c.mu.Lock()
	if !enabled() {
		c.mu.Unlock()
		return
	}
	c.seq++
	frame.Seq = c.seq
	c.last = frame
	c.mu.Unlock()
	c.Frames.Emit(frame)
}

func (c *Camera) ListenLatest(ch chan *Frame) (cancel func()) {
	return c.Frames.Listen(func(frame *Frame) {
		for {
			select {
			case ch <- frame:
				return
			default:
			}
			select {
			case <-ch:
			default:
			}
		}
	})
}

func (c *Camera) Snapshot(ctx context.Context) (*Frame, error) {
	if !enabled() {
		return nil, ErrOff
	}
	if !Available() {
		return nil, ErrNeedsReboot
	}
	c.stillMu.Lock()
	defer c.stillMu.Unlock()
	if !enabled() {
		return nil, ErrOff
	}
	if c.still != nil && time.Since(c.stillAt) < time.Minute {
		return c.still, nil
	}
	conn, err := panelsock.Dial("@techo5.still")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(12 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	var header [5]byte
	if _, err := io.ReadFull(conn, header[:1]); err != nil {
		return nil, err
	}
	if header[0] != 'O' {
		return nil, errors.New("Android full-resolution still unavailable")
	}
	if _, err := io.ReadFull(conn, header[1:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n == 0 || n > 8*1024*1024 {
		return nil, errors.New("invalid Android still length")
	}
	jpg := make([]byte, n)
	if _, err := io.ReadFull(conn, jpg); err != nil {
		return nil, err
	}
	info, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil || info.Width < Width || info.Height < Height || info.Width > 8192 || info.Height > 8192 {
		return nil, errors.New("invalid Android full-resolution still")
	}
	frame := &Frame{At: time.Now(), rawJPEG: jpg, width: info.Width, height: info.Height}
	if !enabled() {
		return nil, ErrOff
	}
	c.still, c.stillAt = frame, time.Now()
	return frame, nil
}
