// Package panelscreen is a TECHO5 Panel's screen and touch: one local socket to the app, which owns
// the real display. The daemon draws its own screens as it does on an Echo and sends the parts that
// changed; the app puts them on a surface and sends back where fingers go.
//
// The app speaks first, saying how big a canvas it wants, and both directions are records after that.
// All numbers are little-endian.
//
// App to daemon:
//
//	hello  "T5P1", u16 width, u16 height, u16 density (dpi), u16 flags (0)      12 bytes, once
//	'T'    u8 action (0 down, 1 move, 2 up), u8 pointer, u16 x, u16 y            7 bytes, canvas pixels
//
// Daemon to app:
//
//	'F'    u16 x, u16 y, u16 w, u16 h, then w*h*4 bytes of RGBA, rows top to bottom
//	'B'    u16 level, 0 to 255; 0 is the screen off
//
// docs/screen-protocol.md in the techo5-panel repository is the app's side of the same thing.
package panelscreen

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

// Socket is the abstract socket the app serves the screen on.
const Socket = "@techo5.screen"

const (
	magic    = "T5P1"
	helloLen = 12

	// The canvas the daemon will agree to draw. Smaller is a mistake in the app; larger costs a slow
	// tablet more than it shows, since the app scales the canvas up to the panel anyway.
	minSide = 240
	maxSide = 1920
)

// DialWait is how long a start waits for the app before giving up; the app serves the socket from the
// moment its service starts, so this only covers a slow start. A variable so a test without the app
// does not sit through it at every frame.
var DialWait = 10 * time.Second

// Touch actions, as the app sends them.
const (
	Down = 0
	Move = 1
	Up   = 2
)

// Touch is one finger event, in canvas pixels.
type Touch struct {
	Action  int
	Pointer int
	X, Y    int
}

// Conn is the one connection to the app, shared by the screen and the touchscreen.
type Conn struct {
	c       net.Conn
	r       *bufio.Reader
	wmu     sync.Mutex
	W, H    int
	Density int
}

var (
	mu      sync.Mutex
	current *Conn
)

// Get returns the connection, dialing the app and reading its hello if there is none. Both the
// screen and the touchscreen call it; whichever is first makes the connection.
func Get() (*Conn, error) {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return current, nil
	}

	deadline := time.Now().Add(DialWait)
	var c net.Conn
	for {
		var err error
		c, err = panelsock.Dial(Socket)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("panelscreen: connecting to the app: %w", err)
		}
		time.Sleep(250 * time.Millisecond)
	}

	r := bufio.NewReader(c)
	var h [helloLen]byte
	_ = c.SetReadDeadline(time.Now().Add(DialWait))
	if _, err := io.ReadFull(r, h[:]); err != nil {
		c.Close()
		return nil, fmt.Errorf("panelscreen: reading the app's hello: %w", err)
	}
	_ = c.SetReadDeadline(time.Time{})
	if string(h[:4]) != magic {
		c.Close()
		return nil, fmt.Errorf("panelscreen: the app said %q, not %q", h[:4], magic)
	}
	w := int(binary.LittleEndian.Uint16(h[4:]))
	ht := int(binary.LittleEndian.Uint16(h[6:]))
	if w < minSide || ht < minSide || w > maxSide || ht > maxSide {
		c.Close()
		return nil, fmt.Errorf("panelscreen: the app asked for a %dx%d canvas", w, ht)
	}
	current = &Conn{c: c, r: r, W: w, H: ht, Density: int(binary.LittleEndian.Uint16(h[8:]))}
	slog.Info("panel screen connected", "canvas", fmt.Sprintf("%dx%d", w, ht), "dpi", current.Density)
	return current, nil
}

// Drop forgets a connection that failed, so the next Get makes a new one. It leaves a newer one alone.
func Drop(c *Conn) {
	mu.Lock()
	defer mu.Unlock()
	if current == c {
		current = nil
		_ = c.c.Close()
	}
}

// Frame sends the rectangle r of a canvas laid out as image.RGBA's Pix with the given stride.
func (c *Conn) Frame(pix []byte, stride, x, y, w, h int) error {
	if w <= 0 || h <= 0 {
		return nil
	}
	buf := make([]byte, 9, 9+w*h*4)
	buf[0] = 'F'
	binary.LittleEndian.PutUint16(buf[1:], uint16(x))
	binary.LittleEndian.PutUint16(buf[3:], uint16(y))
	binary.LittleEndian.PutUint16(buf[5:], uint16(w))
	binary.LittleEndian.PutUint16(buf[7:], uint16(h))
	for row := y; row < y+h; row++ {
		at := row*stride + x*4
		buf = append(buf, pix[at:at+w*4]...)
	}
	return c.write(buf)
}

// Backlight asks the app for a brightness, 0 to 255, where 0 turns the screen off.
func (c *Conn) Backlight(level int) error {
	var buf [3]byte
	buf[0] = 'B'
	binary.LittleEndian.PutUint16(buf[1:], uint16(min(max(level, 0), 255)))
	return c.write(buf[:])
}

func (c *Conn) write(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.c.Write(b)
	return err
}

// ReadTouch waits for the next touch record. Only the touchscreen reads.
func (c *Conn) ReadTouch() (Touch, error) {
	kind, err := c.r.ReadByte()
	if err != nil {
		return Touch{}, err
	}
	if kind != 'T' {
		// Nothing else is defined, and without a length there is no stepping over it.
		return Touch{}, fmt.Errorf("panelscreen: unknown record %q from the app", kind)
	}
	var b [6]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return Touch{}, err
	}
	return Touch{
		Action:  int(b[0]),
		Pointer: int(b[1]),
		X:       int(binary.LittleEndian.Uint16(b[2:])),
		Y:       int(binary.LittleEndian.Uint16(b[4:])),
	}, nil
}

// Close lets the connection go.
func (c *Conn) Close() error { Drop(c); return nil }
