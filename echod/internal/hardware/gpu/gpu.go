//go:build linux

// Package gpu is echod's side of techo5-gpu (tools/gpu), the helper that owns the GPU on the mainline
// 2nd gen Show 8 and Show 5: it draws the screen (the canvas rotated onto the panel, shown with page
// flips) and develops camera frames in shaders. The helper is a separate process, so a fault in the
// GPU stack ends it and not the daemon: every call here reports an error instead, and the callers fall
// back to doing the work on the cores, as they did before there was a GPU.
//
// The protocol is fixed-size requests and replies on a SOCK_SEQPACKET socket, with buffers passed once
// as memfds (SCM_RIGHTS) and mapped on both sides, so pictures are written in place rather than sent.
package gpu

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// helperPath is where the root filesystem puts the helper; the 2nd gen Show 8's carries it and Mesa.
var helperPath = "/usr/local/bin/techo5-gpu"

// SetHelperPath points at another build of the helper, to try one in place before it is installed.
func SetHelperPath(path string) {
	mu.Lock()
	defer mu.Unlock()
	helperPath, failed = path, time.Time{}
}

const (
	opHello        = 1
	opDisplayOpen  = 2
	opPresent      = 3
	opPresentFrame = 4
	opDevelop      = 5
	opBlank        = 6
	opColor        = 8
	opLift         = 9

	// replyWait bounds one request: a page flip waits for vblank, a 13 MP development is the longest.
	replyWait = 3 * time.Second
	// helloWait bounds the first reply: the helper compiles its shaders before it answers, which with
	// an empty shader cache (a new unit, or a Mesa update) takes several seconds.
	helloWait = 30 * time.Second
	// liftWait bounds a lift: at most one frame drawn again and its page flip.
	liftWait = 500 * time.Millisecond

	// maxSide is the most a picture's side can be: the helper refuses more.
	maxSide = 8192
)

type request struct {
	op uint32
	a  [7]uint32
}

type reply struct {
	wire struct {
		status int32
		a      [3]uint32
		text   [112]byte
	}
	fd int
}

// helper is the running process and its socket.
type helper struct {
	cmd  *exec.Cmd
	sock int
	name string
	done chan struct{} // closed once the process has exited
}

var (
	mu      sync.Mutex
	running *helper
	failed  time.Time // when it last could not be started or died; it is not tried again for a while
)

// RetryAfter is how long a helper that failed is left alone before it is started again. A variable
// for tests.
var RetryAfter = time.Minute

// StatusError is the helper's own answer to a request it could not do, the negative errno it replied
// with: the helper is fine, and goes on serving. Any other error from a call is the transport's (the
// socket, or a process that died or went silent), and the helper is dropped for it.
type StatusError struct{ Errno syscall.Errno }

func (e *StatusError) Error() string { return "gpu: the helper refused: " + e.Errno.Error() }
func (e *StatusError) Unwrap() error { return e.Errno }

// broken is whether err leaves the helper unusable: anything but its own status, or nothing.
func broken(err error) bool {
	var st *StatusError
	return err != nil && !errors.As(err, &st)
}

// Installed is whether the root filesystem carries the helper at all.
func Installed() bool {
	mu.Lock()
	defer mu.Unlock()
	_, err := os.Stat(helperPath)
	return err == nil
}

// Available reports whether the helper is there and answers, starting it if need be.
func Available() bool {
	mu.Lock()
	defer mu.Unlock()
	_, err := ensure()
	return err == nil
}

// Name is the GPU the helper drives, as GL names it.
func Name() string {
	mu.Lock()
	defer mu.Unlock()
	if h, err := ensure(); err == nil {
		return h.name
	}
	return ""
}

func ensure() (*helper, error) {
	if running != nil {
		return running, nil
	}
	if !failed.IsZero() && time.Since(failed) < RetryAfter {
		return nil, errors.New("gpu: helper failed recently")
	}
	if _, err := os.Stat(helperPath); err != nil {
		failed = time.Now()
		return nil, fmt.Errorf("gpu: no helper: %w", err)
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		failed = time.Now()
		return nil, err
	}
	child := os.NewFile(uintptr(fds[1]), "gpu")
	cmd := exec.Command(helperPath)
	cmd.ExtraFiles = []*os.File{child} // fd 3
	cmd.Stderr = os.Stderr
	// Mesa keeps compiled shaders in a cache, by default under ~/.cache, which the read-only root
	// filesystem refuses: on the data partition they are compiled once, not at every start.
	cmd.Env = append(os.Environ(), "MESA_SHADER_CACHE_DIR="+filepath.Join(layout.StateDir, "mesa-shader-cache"))
	if err := cmd.Start(); err != nil {
		child.Close()
		unix.Close(fds[0])
		failed = time.Now()
		return nil, fmt.Errorf("gpu: start: %w", err)
	}
	child.Close()
	h := &helper{cmd: cmd, sock: fds[0], done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(h.done) }()
	r, err := h.callWait(request{op: opHello}, nil, helloWait)
	if err != nil {
		h.stop()
		failed = time.Now()
		return nil, fmt.Errorf("gpu: hello: %w", err)
	}
	h.name = cstring(r.wire.text[:])
	slog.Info("gpu up", "renderer", h.name)
	running = h
	return h, nil
}

func (h *helper) stop() {
	unix.Close(h.sock)
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		// The panel is the kernel's again once the process has gone and its DRM file is closed.
		select {
		case <-h.done:
		case <-time.After(stopWait):
		}
	}
}

// stopWait bounds the wait for a killed helper to exit.
const stopWait = 2 * time.Second

// ResetColor puts the panel's color blocks back to the identity, with the helper run once for it
// (techo5-gpu --reset-color, which touches nothing but the CRTC's CTM and GAMMA_LUT): the kernel keeps
// what a helper set after the helper is gone, so a tint it showed would stay on the panel, under a
// framebuffer that is tinted on the cores too. Call it with no helper running.
func ResetColor() error {
	mu.Lock()
	path := helperPath
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), resetWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--reset-color")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gpu: resetting the panel's color: %w", err)
	}
	return nil
}

// resetWait bounds ResetColor: a few DRM calls.
const resetWait = 3 * time.Second

// drop is called with mu held when a call failed in a way that leaves the helper unusable. Its process
// is killed, which gives the panel back to the kernel's console framebuffer.
func drop(err error) {
	if running == nil {
		return
	}
	slog.Warn("gpu: helper dropped, back to the cores", "err", err)
	running.stop()
	running = nil
	failed = time.Now()
}

// Stop ends the running helper, if there is one, for reason: the panel goes back to the kernel's
// console framebuffer. It is started again on the next call after RetryAfter.
func Stop(reason error) {
	mu.Lock()
	defer mu.Unlock()
	drop(reason)
}

// call sends one request, with fds attached, and waits for its reply. A reply carrying a descriptor
// (a dmabuf of the GPU's) has it in r.fd; else r.fd is -1.
func (h *helper) call(req request, fds []int) (reply, error) {
	return h.callWait(req, fds, replyWait)
}

func (h *helper) callWait(req request, fds []int, wait time.Duration) (reply, error) {
	r := reply{fd: -1}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&req)), unsafe.Sizeof(req))
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	if err := unix.Sendmsg(h.sock, b, oob, nil, 0); err != nil {
		return r, fmt.Errorf("gpu: send: %w", err)
	}
	poll := []unix.PollFd{{Fd: int32(h.sock), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(poll, int(wait.Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return r, fmt.Errorf("gpu: poll: %w", err)
		}
		if n == 0 {
			return r, errors.New("gpu: no reply in time")
		}
		break
	}
	rb := unsafe.Slice((*byte)(unsafe.Pointer(&r.wire)), unsafe.Sizeof(r.wire))
	oobBuf := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := unix.Recvmsg(h.sock, rb, oobBuf, unix.MSG_CMSG_CLOEXEC)
	if err != nil {
		return r, fmt.Errorf("gpu: receive: %w", err)
	}
	if n != len(rb) {
		return r, fmt.Errorf("gpu: short reply (%d bytes): the helper went away", n)
	}
	if oobn > 0 {
		if msgs, err := unix.ParseSocketControlMessage(oobBuf[:oobn]); err == nil {
			for _, m := range msgs {
				if got, err := unix.ParseUnixRights(&m); err == nil && len(got) > 0 {
					r.fd = got[0]
				}
			}
		}
	}
	if r.wire.status != 0 {
		if r.fd >= 0 {
			unix.Close(r.fd)
			r.fd = -1
		}
		return r, &StatusError{Errno: syscall.Errno(-r.wire.status)}
	}
	return r, nil
}

// do makes a call on the running helper. The helper's own refusal (StatusError) leaves it running; a
// broken socket, a helper that died or went silent drops it.
func do(req request, fds []int) (reply, error) {
	return doWait(req, fds, replyWait)
}

func doWait(req request, fds []int, wait time.Duration) (reply, error) {
	return call(req, fds, wait, true)
}

// doRunning is do on the helper already running, starting none: the display's calls, which only Open
// may start a helper for.
func doRunning(req request, fds []int, wait time.Duration) (reply, error) {
	return call(req, fds, wait, false)
}

func call(req request, fds []int, wait time.Duration, start bool) (reply, error) {
	r, _, err := callOn(req, fds, wait, start)
	return r, err
}

// callRunning is doRunning, also saying which helper answered.
func callRunning(req request, fds []int, wait time.Duration) (reply, *helper, error) {
	return callOn(req, fds, wait, false)
}

func callOn(req request, fds []int, wait time.Duration, start bool) (reply, *helper, error) {
	mu.Lock()
	defer mu.Unlock()
	if !start && running == nil {
		return reply{}, nil, ErrNoHelper
	}
	h, err := ensure()
	if err != nil {
		return reply{}, nil, err
	}
	r, err := h.callWait(req, fds, wait)
	if broken(err) {
		drop(err)
	}
	return r, h, err
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// buffer is a memfd mapped here, to be passed to the helper the first time it is used.
type buffer struct {
	fd   int
	mem  []byte
	sent *helper // the helper it was given to; another helper (after a restart) gets it again
}

func newBuffer(name string, size int) (*buffer, error) {
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		unix.Close(fd)
		return nil, err
	}
	mem, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &buffer{fd: fd, mem: mem}, nil
}

// fdFor is the buffer's fd if the running helper has not had it yet, else nothing: the helper keeps
// what it was given.
func (b *buffer) fdFor() []int {
	if running != nil && b.sent == running {
		return nil
	}
	return []int{b.fd}
}

func (b *buffer) given() { b.sent = running }

func (b *buffer) close() {
	unix.Munmap(b.mem)
	unix.Close(b.fd)
}

// ---- the screen ----

// Display is the panel driven by the helper: a canvas in shared memory, drawn rotated and flipped.
//
// The canvas outlives the helper: Release gives the panel back to the kernel and keeps the canvas, so
// whatever draws on it goes on drawing, and Open takes the panel again with the same canvas, the same
// helper or a new one. A second buffer of the canvas's size, Staging, holds a picture made from the
// canvas (a filtered copy) for Present to show instead of it.
type Display struct {
	canvas  *buffer
	staging *buffer
	frame   *buffer
	w, h    int
	rotated bool
	panel   image.Point

	// shown is the buffer the helper has as its canvas (canvas or staging), nil before it has one.
	shown *buffer

	// color is the color transform last asked for (SetColor), given again to a restarted helper,
	// in colorBuf; colorCaps what the pipeline has, once a helper has said.
	color     Color
	colorBuf  *buffer
	colorCaps ColorCaps

	// gain is the lift last asked for (SetLift), given again to a restarted helper.
	gain float64
}

// NewDisplay makes a canvas of w x h in memory the helper can share, to be turned a quarter turn onto
// the panel when rotated. Nothing is asked of the helper yet: Open does that. The canvas's pixels are
// the returned picture's: draw on it and Present.
func NewDisplay(w, h int, rotated bool) (*Display, *image.RGBA, error) {
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return nil, nil, fmt.Errorf("gpu: a %dx%d canvas", w, h)
	}
	c, err := newBuffer("techo5-canvas", w*h*4)
	if err != nil {
		return nil, nil, err
	}
	d := &Display{canvas: c, w: w, h: h, rotated: rotated}
	return d, &image.RGBA{Pix: c.mem, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
}

// OpenDisplay is NewDisplay and Open together.
func OpenDisplay(w, h int, rotated bool) (*Display, *image.RGBA, error) {
	d, img, err := NewDisplay(w, h, rotated)
	if err != nil {
		return nil, nil, err
	}
	if err := d.Open(); err != nil {
		d.Close()
		return nil, nil, err
	}
	return d, img, nil
}

// Open takes the panel over for the canvas, starting the helper if need be, and says the panel's
// size (Panel).
func (d *Display) Open() error { return d.open(d.canvas, true) }

// Release gives the panel back to the kernel's console framebuffer, for the cores to draw on: the
// helper is stopped, for reason. The canvas stays as it is, and Open takes the panel again.
func (d *Display) Release(reason error) {
	mu.Lock()
	defer mu.Unlock()
	drop(reason)
	d.shown = nil
	// The screen's lift goes back to none with the helper; a gain kept here would be given to the
	// next helper, drawing the panel too bright at the backlight asked.
	d.gain = 1
	for _, b := range []*buffer{d.canvas, d.staging, d.frame, d.colorBuf} {
		if b != nil {
			b.sent = nil
		}
	}
}

// Close releases the panel and frees the display's buffers, the canvas too: nothing may draw on it after.
func (d *Display) Close() {
	d.Release(errors.New("gpu: display closed"))
	for _, b := range []*buffer{d.canvas, d.staging, d.frame, d.colorBuf} {
		if b != nil {
			b.close()
		}
	}
	d.canvas, d.staging, d.frame, d.colorBuf = nil, nil, nil, nil
}

// Staging is a picture of the canvas's size in memory the helper can share, for a copy of the canvas
// made on its way to the panel (a filter's): Present it in place of the canvas, and nothing is copied
// again.
func (d *Display) Staging() (*image.RGBA, error) {
	b, err := d.stagingBuffer()
	if err != nil {
		return nil, err
	}
	return &image.RGBA{Pix: b.mem, Stride: d.w * 4, Rect: image.Rect(0, 0, d.w, d.h)}, nil
}

// stagingBuffer is the staging buffer, made the first time; under mu, as open reads it.
func (d *Display) stagingBuffer() (*buffer, error) {
	mu.Lock()
	defer mu.Unlock()
	if d.staging == nil {
		b, err := newBuffer("techo5-staging", d.w*d.h*4)
		if err != nil {
			return nil, err
		}
		d.staging = b
	}
	return d.staging, nil
}

// bufferOf is the display's buffer that holds src: the canvas or the staging picture. Any other
// picture of the canvas's size is copied into the staging buffer first.
func (d *Display) bufferOf(src *image.RGBA) (*buffer, error) {
	if src == nil || len(src.Pix) == 0 {
		return d.canvas, nil
	}
	if &src.Pix[0] == &d.canvas.mem[0] {
		return d.canvas, nil
	}
	if src.Rect.Dx() != d.w || src.Rect.Dy() != d.h || src.Stride != d.w*4 {
		return nil, fmt.Errorf("gpu: a %v picture for a %dx%d canvas", src.Rect, d.w, d.h)
	}
	st, err := d.stagingBuffer()
	if err != nil {
		return nil, err
	}
	if &src.Pix[0] != &st.mem[0] {
		copy(st.mem, src.Pix)
	}
	return st, nil
}

// open gives the helper b as its canvas, starting the helper if need be when start says so (Open's
// alone: everything else says ErrNoHelper), and puts back what a new helper does not have: the color
// transform and the lift.
func (d *Display) open(b *buffer, start bool) error {
	rot := uint32(0)
	if d.rotated {
		rot = 1
	}
	mu.Lock()
	defer mu.Unlock()
	if d.canvas == nil {
		return errors.New("gpu: display closed")
	}
	if !start && running == nil {
		return ErrNoHelper
	}
	h, err := ensure()
	if err != nil {
		return err
	}
	fresh := !d.canvas.sentTo(h) && !d.staging.sentTo(h)
	var fds []int
	if b.sent != h {
		fds = []int{b.fd}
	}
	r, err := h.call(request{op: opDisplayOpen, a: [7]uint32{uint32(d.w), uint32(d.h), rot}}, fds)
	if err != nil {
		if broken(err) {
			drop(err)
		}
		return err
	}
	// The helper keeps one canvas: the other buffer, if it had it, is no longer mapped there.
	for _, o := range []*buffer{d.canvas, d.staging} {
		if o != nil && o != b {
			o.sent = nil
		}
	}
	b.sent, d.shown = h, b
	d.panel = image.Pt(int(r.wire.a[0]), int(r.wire.a[1]))
	if !fresh {
		return nil
	}
	// A new helper puts the pipeline back to the identity; what was asked for goes to it again.
	if !d.color.identity() {
		if cerr := d.sendColor(h); cerr != nil {
			slog.Warn("gpu: the color transform could not be given again", "err", cerr)
			if broken(cerr) && !errors.Is(cerr, errNoColor) {
				drop(cerr)
				return cerr
			}
		}
	}
	// And draws without a lift, which the backlight was turned down for.
	if d.gain > 1 {
		if _, lerr := h.call(request{op: opLift, a: [7]uint32{liftWire(d.gain)}}, nil); lerr != nil {
			slog.Warn("gpu: the lift could not be given again", "err", lerr)
			if broken(lerr) {
				drop(lerr)
				return lerr
			}
		}
	}
	return nil
}

// sentTo is whether b (which may be nil) was given to h.
func (b *buffer) sentTo(h *helper) bool { return b != nil && b.sent == h }

// Color is a transform the display pipeline's own color blocks apply to everything on the panel, on
// its way out (the CRTC's CTM and GAMMA_LUT, the SoC's CCORR and GAMMA): no cost to the cores or
// the GPU, and no frame drawn again for it.
type Color struct {
	// Matrix maps each pixel's R, G, B (row-major, out = Matrix * in); nil is the identity.
	Matrix *[9]float64
	// Curves map each channel after the matrix, 256 points over its range to 0..65535; nil is none.
	Curves *[3][256]uint16
}

func (c Color) identity() bool { return c.Matrix == nil && c.Curves == nil }

// ColorCaps is what the panel's pipeline can do.
type ColorCaps struct {
	Matrix   bool // a CTM
	Curves   int  // GAMMA_LUT_SIZE, or 0 for none
	Answered bool // the helper has said
}

// SetColor has the pipeline apply c from the next vblank, returning what it can do. A pipeline
// without one of c's parts gives an error and leaves it as it was, for the caller to draw instead.
func (d *Display) SetColor(c Color) (ColorCaps, error) {
	if err := d.reopen(nil); err != nil {
		return ColorCaps{}, err
	}
	mu.Lock()
	defer mu.Unlock()
	h := running
	if h == nil {
		return ColorCaps{}, ErrNoHelper
	}
	prev := d.color
	d.color = c
	if err := d.sendColor(h); err != nil {
		d.color = prev
		if broken(err) && !errors.Is(err, errNoColor) {
			drop(err)
		}
		return d.colorCaps, err
	}
	return d.colorCaps, nil
}

// sendColor gives d.color to h (mu held).
func (d *Display) sendColor(h *helper) error {
	var flags uint32
	var fds []int
	c := d.color
	if !c.identity() {
		if d.colorBuf == nil {
			b, err := newBuffer("techo5-color", colorSize)
			if err != nil {
				return err
			}
			d.colorBuf = b
		}
		m := d.colorBuf.mem
		if c.Matrix != nil {
			flags |= 1
			for i, v := range c.Matrix {
				binary.LittleEndian.PutUint32(m[i*4:], uint32(int32(math.Round(min(max(v, -32767), 32767)*65536))))
			}
		}
		if c.Curves != nil {
			flags |= 2
			for ch := range c.Curves {
				for i, v := range c.Curves[ch] {
					binary.LittleEndian.PutUint16(m[36+(ch*256+i)*2:], v)
				}
			}
		}
		if d.colorBuf.sent != h {
			fds = []int{d.colorBuf.fd}
		}
	}
	r, err := h.call(request{op: opColor, a: [7]uint32{flags}}, fds)
	switch {
	case errors.Is(err, syscall.ENOSYS): // a helper from before OP_COLOR
		d.colorCaps = ColorCaps{Answered: true}
		return errNoColor
	case err != nil:
		return err
	}
	d.colorCaps = ColorCaps{Matrix: r.wire.a[1]&1 != 0, Curves: int(r.wire.a[0]), Answered: true}
	if fds != nil {
		d.colorBuf.given()
	}
	if c.Matrix != nil && !d.colorCaps.Matrix || c.Curves != nil && d.colorCaps.Curves == 0 {
		return errNoColor
	}
	return nil
}

// colorSize is the helper's struct color: nine int32 then three curves of 256 uint16.
const colorSize = 9*4 + 3*256*2

var errNoColor = errors.New("gpu: the panel's pipeline has no such color block")

// Panel is the panel's size as the helper drives it.
func (d *Display) Panel() image.Point { return d.panel }

// Present shows src, the canvas or the staging picture (or a picture of the canvas's size, copied into
// the staging one), returning once the page flip is done. A helper that was restarted since is given
// the display again first.
func (d *Display) Present(src *image.RGBA) error {
	_, err := d.present(src, false)
	return err
}

// PresentMeasured is Present, also returning the picture's brightest red, green or blue value (0 to
// 255), which the helper reads before drawing it; -1 from a helper that cannot.
func (d *Display) PresentMeasured(src *image.RGBA) (int, error) {
	return d.present(src, true)
}

func (d *Display) present(src *image.RGBA, measure bool) (int, error) {
	b, err := d.bufferOf(src)
	if err != nil {
		return -1, err
	}
	if err := d.reopen(b); err != nil {
		return -1, err
	}
	var flags uint32
	if measure {
		flags = 1
	}
	r, err := doRunning(request{op: opPresent, a: [7]uint32{flags}}, nil, replyWait)
	if err != nil {
		return -1, err
	}
	if !measure {
		return -1, nil
	}
	return int(r.wire.a[0]) - 1, nil
}

// ErrNoHelper is a call on the display while no helper runs: only Open starts one.
var ErrNoHelper = errors.New("gpu: no helper running; Open starts one")

// ErrNoLift is a helper from before the lift.
var ErrNoLift = errors.New("gpu: the helper cannot lift")

// ErrNotCanvas is a redraw asked for while the panel shows something other than the canvas alone (a
// video frame, a camera's): the next frame of it is drawn with the gain instead.
var ErrNotCanvas = errors.New("gpu: the panel does not show the canvas alone")

// SetLift has every present from now draw its pixels gain times as bright (1 is none), for a backlight
// turned down by as much (screen's lift). redraw draws the canvas last presented again with it, and
// returns once that frame is on the panel. It waits liftWait at most: a helper slower than that is
// taken as gone.
func (d *Display) SetLift(gain float64, redraw bool) error {
	if err := d.reopen(nil); err != nil {
		return err
	}
	var flags uint32
	if redraw {
		flags = 1
	}
	_, h, err := callRunning(request{op: opLift, a: [7]uint32{liftWire(gain), flags}}, nil, liftWait)
	if afterLiftCall != nil {
		afterLiftCall()
	}
	switch {
	case errors.Is(err, syscall.ENOSYS):
		return ErrNoLift
	case errors.Is(err, syscall.EAGAIN):
		d.keepGain(h, gain) // set all the same: only the drawing again was refused
		return ErrNotCanvas
	case err != nil:
		return err
	}
	d.keepGain(h, gain)
	return nil
}

// keepGain keeps the gain h was given, for a restarted helper to be given again, while h is still the
// helper running: once it has been released (Release puts the gain back to none, as the screen's lift
// does) or replaced, the gain is not the next helper's.
func (d *Display) keepGain(h *helper, gain float64) {
	mu.Lock()
	defer mu.Unlock()
	if h != nil && h == running {
		d.gain = gain
	}
}

// afterLiftCall is a test's, run where SetLift has its reply (export_test.go).
var afterLiftCall func()

// liftWire is a gain as OP_LIFT has it: in 1/65536, 1 and under being none.
func liftWire(gain float64) uint32 {
	return uint32(math.Round(min(max(gain, 1), 4) * 65536))
}

// reopen gives the display to the helper again when it has not got it, or has another buffer as its
// canvas than b (nil: whichever it has, or the canvas).
func (d *Display) reopen(b *buffer) error {
	mu.Lock()
	if b == nil {
		b = d.shown
		if b == nil {
			b = d.canvas
		}
	}
	again := running == nil || b == nil || d.shown != b || b.sent != running
	mu.Unlock()
	if again {
		if b == nil {
			return errors.New("gpu: display closed")
		}
		return d.open(b, false)
	}
	return nil
}

// FrameLayout is the byte order of a frame given to PresentFrame.
type FrameLayout uint32

const (
	FrameRGBA FrameLayout = 0 // bytes R, G, B, A
	FrameBGRA FrameLayout = 1 // bytes B, G, R, A: the framebuffer's own order, so a frame made for it
	// looks the same whichever of the two draws it
)

// PresentFrame shows a frame already in the panel's orientation and size, four bytes a pixel laid out
// as layout says, with the canvas's rectangle over blended on top of it.
func (d *Display) PresentFrame(frame []byte, w, h int, layout FrameLayout, over image.Rectangle) error {
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide || len(frame) < w*h*4 {
		return fmt.Errorf("gpu: a %dx%d frame of %d bytes", w, h, len(frame))
	}
	if err := d.reopen(nil); err != nil {
		return err
	}
	if d.frame == nil || len(d.frame.mem) != w*h*4 {
		if d.frame != nil {
			d.frame.close()
		}
		f, err := newBuffer("techo5-frame", w*h*4)
		if err != nil {
			d.frame = nil
			return err
		}
		d.frame = f
	}
	copy(d.frame.mem, frame)
	over = over.Intersect(image.Rect(0, 0, d.w, d.h))
	mu.Lock()
	fds := d.frame.fdFor()
	mu.Unlock()
	_, err := doRunning(request{op: opPresentFrame, a: [7]uint32{uint32(w), uint32(h),
		uint32(over.Min.X), uint32(over.Min.Y), uint32(over.Max.X), uint32(over.Max.Y), uint32(layout)}}, fds, replyWait)
	if err == nil {
		mu.Lock()
		d.frame.given()
		mu.Unlock()
	}
	return err
}

// ---- the camera ----

// developer holds the buffers of the last development's size, reused frame after frame.
var dev struct {
	sync.Mutex
	raw, tables, out *buffer
}

func sized(b **buffer, name string, size int) error {
	if *b != nil && len((*b).mem) == size {
		return nil
	}
	if *b != nil {
		(*b).close()
	}
	nb, err := newBuffer(name, size)
	if err != nil {
		*b = nil
		return err
	}
	*b = nb
	return nil
}

// Output is what Develop makes of a frame.
type Output uint32

const (
	RGBA   Output = 0 // w*h*4 bytes
	YUYV   Output = 1 // w*h*2 bytes
	YUV420 Output = 3 // planes Y (w*h), Cb and Cr (w*h/4 each), for a JPEG encoder on the cores
)

// Develop demosaics a packed 10-bit Bayer frame (w x h pixels, bpl bytes a line) through the tone
// tables on the GPU, and reads the result back. The result is a fresh slice the caller keeps.
func Develop(raw []byte, w, h, bpl int, tables *[3][2048]uint8, blueFirst bool, out Output) ([]byte, error) {
	size := map[Output]int{RGBA: w * h * 4, YUYV: w * h * 2, YUV420: w * h * 3 / 2}[out]
	r, err := develop(raw, w, h, bpl, tables, blueFirst, uint32(out), size)
	if err != nil {
		return nil, err
	}
	_ = r
	return append([]byte(nil), dev.out.mem[:size]...), nil
}

// dmabuf is the GPU's YUYV target the last DevelopDMABuf drew into, by the helper that made it.
var dmabuf struct {
	fd, stride int
	w, h       int
	by         *helper
}

// DevelopDMABuf develops a frame to YUYV in a buffer of the GPU's and returns its dmabuf, for a JPEG
// encoder to read where it is: nothing comes back through the cores. The descriptor stays the gpu
// package's; it is valid until the next call, and a caller must be done with it before making one.
func DevelopDMABuf(raw []byte, w, h, bpl int, tables *[3][2048]uint8, blueFirst bool) (fd, stride int, err error) {
	r, err := develop(raw, w, h, bpl, tables, blueFirst, 2, 0)
	if err != nil {
		return -1, 0, err
	}
	mu.Lock()
	defer mu.Unlock()
	if r.fd >= 0 {
		if dmabuf.fd > 0 {
			unix.Close(dmabuf.fd)
		}
		dmabuf.fd, dmabuf.stride, dmabuf.w, dmabuf.h, dmabuf.by = r.fd, int(r.wire.a[1]), w, h, running
	}
	if dmabuf.by != running || dmabuf.w != w || dmabuf.h != h || dmabuf.fd <= 0 {
		return -1, 0, errors.New("gpu: no dmabuf for this size")
	}
	return dmabuf.fd, dmabuf.stride, nil
}

// develop sends a development; out is the out buffer's size, 0 when the result stays on the GPU.
func develop(raw []byte, w, h, bpl int, tables *[3][2048]uint8, blueFirst bool, mode uint32, out int) (reply, error) {
	dev.Lock()
	defer dev.Unlock()
	if err := sized(&dev.raw, "techo5-raw", bpl*h); err != nil {
		return reply{}, err
	}
	if err := sized(&dev.tables, "techo5-tables", 3*2048); err != nil {
		return reply{}, err
	}
	if out == 0 {
		out = 1 // the helper wants a buffer in its place all the same
	}
	if dev.out == nil || len(dev.out.mem) < out {
		if err := sized(&dev.out, "techo5-developed", out); err != nil {
			return reply{}, err
		}
	}
	copy(dev.raw.mem, raw[:bpl*h])
	for i := range tables {
		copy(dev.tables.mem[i*2048:], tables[i][:])
	}
	bf := uint32(0)
	if blueFirst {
		bf = 1
	}
	// The three go together, in this order, whenever the helper lacks any of them (new buffers, or a
	// helper started since); otherwise none, and it uses what it has mapped.
	mu.Lock()
	var fds []int
	if running == nil || dev.raw.sent != running || dev.tables.sent != running || dev.out.sent != running {
		fds = []int{dev.raw.fd, dev.tables.fd, dev.out.fd}
	}
	mu.Unlock()
	r, err := do(request{op: opDevelop, a: [7]uint32{uint32(w), uint32(h), uint32(bpl), bf, mode}}, fds)
	if err != nil {
		return r, err
	}
	mu.Lock()
	dev.raw.given()
	dev.tables.given()
	dev.out.given()
	mu.Unlock()
	return r, nil
}
