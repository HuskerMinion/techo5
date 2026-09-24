//go:build !dot

package dashboard

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The dashcast protocol, as dashcast/serve.go describes it, inside the encrypted connection secure.go
// makes: a hello line of JSON, then a JSON line per touch; back come length-framed messages, a
// picture to draw at a place or a problem to show.
const (
	kindPicture = 1
	kindProblem = 2
	kindHalf    = 3 // a picture at half size, drawn doubled: sent while most of the page is moving
)

// View is what the page knows about the stream: whether there is a picture to draw (DrawStream
// draws it), and if not, or not any more, why.
type View struct {
	Ready   bool   // a picture has arrived
	Problem string // a sentence for the screen, empty when all is well
	Version uint64 // counts pictures, so a drawer can tell a new one from the last
}

// stream is one connection's worth of dashboard, kept going while the page is up.
type stream struct {
	f    *Feature
	w, h int

	mu      sync.Mutex
	conn    net.Conn
	view    View
	frame   *image.RGBA // painted in place as pictures arrive, under mu
	stopped bool
	enc     *json.Encoder
}

// DrawStream draws the streamed dashboard into dst, and reports whether there was one to draw.
func (f *Feature) DrawStream(dst *image.RGBA) bool {
	f.mu.Lock()
	s := f.stream
	f.mu.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frame == nil || !s.view.Ready {
		return false
	}
	draw.Draw(dst, dst.Rect, s.frame, image.Point{}, draw.Src)
	return true
}

// Stream is the streamed dashboard at w by h, connecting if it is not already. It is for the page
// that is up; Close ends it when the page goes.
func (f *Feature) Stream(w, h int) View {
	f.mu.Lock()
	s := f.stream
	if s == nil || s.w != w || s.h != h {
		if s != nil {
			go s.close()
		}
		s = &stream{f: f, w: w, h: h}
		f.stream = s
		go s.run()
	}
	f.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

// Close ends the stream, if there is one.
func (f *Feature) Close() {
	f.mu.Lock()
	s := f.stream
	f.stream = nil
	f.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// Touch passes a touch on to the streamed page: kind is tap, down, move or up.
func (f *Feature) Touch(kind string, x, y int) {
	f.mu.Lock()
	s := f.stream
	f.mu.Unlock()
	if s == nil {
		return
	}
	// Written by whoever touched, which is the touch reader: small, and the socket's buffer takes it.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enc != nil {
		_ = s.enc.Encode(map[string]any{"t": kind, "x": x, "y": y})
	}
}

func (s *stream) close() {
	s.mu.Lock()
	s.stopped = true
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (s *stream) problem(text string) {
	s.mu.Lock()
	s.view.Problem = text
	s.mu.Unlock()
	s.f.Changed.Emit(struct{}{})
}

// run connects and reconnects until closed, waiting longer each time it fails.
func (s *stream) run() {
	wait := time.Second
	for {
		s.mu.Lock()
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		err := s.once()
		s.mu.Lock()
		stopped = s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		if err != nil {
			slog.Info("dashboard stream", "err", err)
		}
		time.Sleep(wait)
		wait = min(wait*2, 30*time.Second)
	}
}

func (s *stream) once() error {
	cfg := config.Get()
	d := cfg.Dashboard
	if d.Server == "" {
		s.problem("Streaming needs a dashcast server: set one with the dashboard_server action.")
		return errors.New("no server set")
	}
	raw, err := net.DialTimeout("tcp", d.Server, 5*time.Second)
	if err != nil {
		s.problem("Can't reach the dashboard server at " + d.Server + ".")
		return err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := clientHandshake(raw, d.Key)
	if err != nil {
		s.problem("The dashboard server did not accept this device's key.")
		return err
	}
	_ = raw.SetDeadline(time.Time{})

	path := "/" + d.Path
	if d.Path == "" {
		path = "/lovelace/0"
	}
	enc := json.NewEncoder(c)
	if err := enc.Encode(map[string]any{"name": cfg.Device.Name, "w": s.w, "h": s.h, "path": path}); err != nil {
		return err
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.conn, s.enc = c, enc
	s.view.Problem = ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.conn, s.enc = nil, nil
		s.mu.Unlock()
	}()
	slog.Info("dashboard stream connected", "server", d.Server, "path", path)

	r := bufio.NewReaderSize(c, 256<<10)
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > 16<<20 {
			return errors.New("dashboard stream: a message of an impossible size")
		}
		msg := make([]byte, n)
		if _, err := io.ReadFull(r, msg); err != nil {
			return err
		}
		switch msg[0] {
		case kindHalf:
			if len(msg) < 5 {
				continue
			}
			at := image.Pt(int(binary.BigEndian.Uint16(msg[1:3])), int(binary.BigEndian.Uint16(msg[3:5])))
			img, err := jpeg.Decode(bytes.NewReader(msg[5:]))
			if err != nil {
				continue
			}
			s.paintDoubled(at, img)
		case kindProblem:
			s.problem(string(msg[1:]))
		case kindPicture:
			if len(msg) < 5 {
				continue
			}
			at := image.Pt(int(binary.BigEndian.Uint16(msg[1:3])), int(binary.BigEndian.Uint16(msg[3:5])))
			img, err := jpeg.Decode(bytes.NewReader(msg[5:]))
			if err != nil {
				continue
			}
			s.paint(at, img)
		}
	}
}

// paint puts a picture into the frame where it belongs. Pictures are painted as they arrive, however
// fast, and the page draws whatever the frame holds when it next draws: nothing queues behind a slow
// screen, and a picture the screen never showed on its own still shows in the next.
func (s *stream) paint(at image.Point, img image.Image) {
	b := img.Bounds()
	s.mu.Lock()
	if s.frame == nil {
		s.frame = image.NewRGBA(image.Rect(0, 0, s.w, s.h))
	}
	draw.Draw(s.frame, image.Rectangle{Min: at, Max: at.Add(b.Size())}, img, b.Min, draw.Src)
	s.view.Ready = true
	s.view.Version++
	s.mu.Unlock()
	s.f.Changed.Emit(struct{}{})
}

// paintDoubled puts a half-size picture into the frame at twice its size, each pixel as four. It is
// soft, but it is a page moving, and the full-size one follows once it stops.
func (s *stream) paintDoubled(at image.Point, img image.Image) {
	b := img.Bounds()
	// Colors worked out once per pixel of the small picture, outside the lock.
	small := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(small, small.Rect, img, b.Min, draw.Src)

	s.mu.Lock()
	if s.frame == nil {
		s.frame = image.NewRGBA(image.Rect(0, 0, s.w, s.h))
	}
	f := s.frame
	for y := 0; y < small.Rect.Dy(); y++ {
		ty := at.Y + 2*y
		if ty+1 >= s.h {
			break
		}
		src := small.Pix[y*small.Stride : y*small.Stride+small.Rect.Dx()*4]
		r0 := f.Pix[ty*f.Stride : (ty+1)*f.Stride]
		for x := 0; x*4 < len(src); x++ {
			tx := at.X + 2*x
			if tx+1 >= s.w {
				break
			}
			p := src[x*4 : x*4+4]
			o := tx * 4
			copy(r0[o:o+4], p)
			copy(r0[o+4:o+8], p)
		}
		lo, hi := at.X*4, min(at.X+2*small.Rect.Dx(), s.w)*4
		copy(f.Pix[(ty+1)*f.Stride+lo:(ty+1)*f.Stride+hi], r0[lo:hi])
	}
	s.view.Ready = true
	s.view.Version++
	s.mu.Unlock()
	s.f.Changed.Emit(struct{}{})
}
