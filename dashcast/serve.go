package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// The conversation with a device. The device opens with one line of JSON, a hello; after that it
// sends a line per touch. What comes back is messages framed as a 4-byte big-endian length, then a
// kind byte and its payload:
//
//	kindPicture  2-byte x, 2-byte y, then a JPEG to draw with its top left there
//	kindProblem  a sentence for the screen to show: the key was wrong, the page would not load
//	kindHalf     2-byte x, 2-byte y, then a JPEG at half size, to draw doubled with its top left there
//
// The first picture is the whole screen; after it, only what changed. While most of the screen is
// changing - a page scrolling - the pictures go at half size, a quarter of the work for a device to
// decode, and once it stops the same places follow at full size.
const (
	kindPicture = 1
	kindProblem = 2
	kindHalf    = 3
)

// soft is how much of the screen has to change in one frame before it goes at half size, and
// settle how long after the last such frame the full-size pictures follow.
const (
	soft   = 0.35
	settle = 250 * time.Millisecond
)

type hello struct {
	Key  string `json:"key"`
	Name string `json:"name"` // for the log
	W    int    `json:"w"`
	H    int    `json:"h"`
	Path string `json:"path"` // the dashboard, as in Home Assistant's own address bar: /lovelace/0
}

type touchMsg struct {
	T string `json:"t"` // tap, down, move, up
	X int    `json:"x"`
	Y int    `json:"y"`
}

// quality is the pictures' JPEG quality: text on a dashboard wants it high, and the pictures are
// small, since mostly only what changed is sent.
const quality = 85

func serve(ctx context.Context, b *browser, g *guard, cfg config, c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var h hello
	if json.Unmarshal(line, &h) != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	out := &sender{c: c}

	if subtle.ConstantTimeCompare([]byte(h.Key), []byte(cfg.key)) != 1 {
		slog.Warn("a device with the wrong key", "from", c.RemoteAddr(), "name", h.Name)
		out.problem("The dashboard server's key does not match this device's.")
		return
	}
	if h.W <= 0 || h.H <= 0 || h.W > 4096 || h.H > 4096 {
		return
	}
	if !strings.HasPrefix(h.Path, "/") {
		h.Path = "/" + h.Path
	}
	// Dashboards only: see guard.go.
	switch ok, err := g.allows(ctx, h.Path); {
	case err != nil:
		slog.Warn("could not ask Home Assistant what its dashboards are", "err", err)
		out.problem("Can't reach Home Assistant to check the dashboard.")
		return
	case !ok:
		slog.Warn("a device asked for a page that is not a dashboard", "name", h.Name, "path", h.Path)
		out.problem("That page is not a dashboard, so it is not shown.")
		return
	}
	allowed, _ := g.panels(ctx)
	slog.Info("device connected", "name", h.Name, "from", c.RemoteAddr(), "size", [2]int{h.W, h.H}, "path", h.Path)
	defer slog.Info("device gone", "name", h.Name)

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tab, closeTab, err := b.open(sctx, h.Path, h.W, h.H, allowed)
	if err != nil {
		slog.Warn("opening the dashboard failed", "name", h.Name, "err", err)
		out.problem("The dashboard would not open: " + err.Error())
		return
	}
	defer closeTab()

	d := &differ{}
	chromedp.ListenTarget(tab, func(ev any) {
		f, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		go func() {
			// Acknowledged once handled, which is what paces Chrome: it sends the next frame only
			// after this one is acknowledged, so a slow device holds frames back rather than queuing
			// them.
			defer chromedp.Run(tab, page.ScreencastFrameAck(f.SessionID))
			raw, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				return
			}
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				return
			}
			if err := d.send(d.changes(img), out); err != nil {
				cancel()
			}
		}()
	})
	// PNG from Chrome rather than JPEG: lossless, so two frames of an unchanged page are the same
	// bytes and what changed can be found exactly. The JPEG is made here, of the changed part only.
	if err := chromedp.Run(tab, page.StartScreencast().WithFormat(page.ScreencastFormatPng).
		WithMaxWidth(int64(h.W)).WithMaxHeight(int64(h.H))); err != nil {
		out.problem("The dashboard would not stream: " + err.Error())
		return
	}

	go func() {
		<-sctx.Done()
		c.Close()
	}()
	go keepOnDashboards(sctx, tab, b.cfg.ha+h.Path, g, h.Name)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var t touchMsg
		if json.Unmarshal(line, &t) != nil {
			continue
		}
		if err := chromedp.Run(tab, chromedp.ActionFunc(func(ctx context.Context) error { return touch(ctx, t) })); err != nil {
			slog.Warn("touch", "err", err)
		}
	}
}

// touch replays one of the device's touches on the page.
func touch(ctx context.Context, t touchMsg) error {
	at := []*input.TouchPoint{{X: float64(t.X), Y: float64(t.Y)}}
	switch t.T {
	case "tap":
		if err := input.DispatchTouchEvent(input.TouchStart, at).Do(ctx); err != nil {
			return err
		}
		return input.DispatchTouchEvent(input.TouchEnd, []*input.TouchPoint{}).Do(ctx)
	case "down":
		return input.DispatchTouchEvent(input.TouchStart, at).Do(ctx)
	case "move":
		return input.DispatchTouchEvent(input.TouchMove, at).Do(ctx)
	case "up":
		return input.DispatchTouchEvent(input.TouchEnd, []*input.TouchPoint{}).Do(ctx)
	}
	return nil
}

// sender writes messages to one device; frames arrive from several goroutines.
type sender struct {
	mu sync.Mutex
	c  net.Conn
}

func (s *sender) send(kind byte, payload ...[]byte) error {
	n := 1
	for _, p := range payload {
		n += len(p)
	}
	msg := make([]byte, 4, 4+n)
	binary.BigEndian.PutUint32(msg, uint32(n))
	msg = append(msg, kind)
	for _, p := range payload {
		msg = append(msg, p...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := s.c.Write(msg)
	return err
}

func (s *sender) problem(text string) { _ = s.send(kindProblem, []byte(text)) }

func (s *sender) picture(at image.Point, img image.Image) error {
	return s.jpeg(kindPicture, at, img)
}

// half sends img at half its size, to be drawn doubled.
func (s *sender) half(at image.Point, img image.Image) error {
	return s.jpeg(kindHalf, at, shrink(img))
}

func (s *sender) jpeg(kind byte, at image.Point, img image.Image) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	var pos [4]byte
	binary.BigEndian.PutUint16(pos[0:], uint16(at.X))
	binary.BigEndian.PutUint16(pos[2:], uint16(at.Y))
	return s.send(kind, pos[:], buf.Bytes())
}

// shrink is img at half size, each pixel the average of the four it stands for.
func shrink(img image.Image) *image.RGBA {
	b := img.Bounds()
	src := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(src, src.Rect, img, b.Min, draw.Src)
	w, h := b.Dx()/2, b.Dy()/2
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i, j := src.PixOffset(2*x, 2*y), src.PixOffset(2*x, 2*y+1)
			o := out.PixOffset(x, y)
			for c := 0; c < 4; c++ {
				sum := int(src.Pix[i+c]) + int(src.Pix[i+4+c]) + int(src.Pix[j+c]) + int(src.Pix[j+4+c])
				out.Pix[o+c] = uint8((sum + 2) / 4)
			}
		}
	}
	return out
}

// differ remembers the last frame and says what in the next one is different.
type differ struct {
	mu   sync.Mutex
	last *image.RGBA

	// blurred is what went at half size and is still owed at full size, and sharpen the timer that
	// will send it once the screen settles.
	blurred []image.Rectangle
	sharpen *time.Timer
}

// send sends a frame's changes: at full size, or at half size while most of the screen is moving.
func (d *differ) send(patches []patch, out *sender) error {
	d.mu.Lock()
	full := d.last.Rect
	d.mu.Unlock()
	area := 0
	for _, p := range patches {
		area += p.img.Bounds().Dx() * p.img.Bounds().Dy()
	}
	moving := len(patches) > 0 && float64(area) > soft*float64(full.Dx()*full.Dy())
	for _, p := range patches {
		if !moving {
			if err := out.picture(p.at, p.img); err != nil {
				return err
			}
			continue
		}
		// Even edges, so the doubled picture lands on the pixels it came from.
		r := image.Rectangle{Min: p.at, Max: p.at.Add(p.img.Bounds().Size())}
		r = image.Rect(r.Min.X&^1, r.Min.Y&^1, min((r.Max.X+1)&^1, full.Max.X), min((r.Max.Y+1)&^1, full.Max.Y))
		d.mu.Lock()
		img := d.last.SubImage(r)
		d.blurred = merge(d.blurred, r)
		d.mu.Unlock()
		if err := out.half(r.Min, img); err != nil {
			return err
		}
	}
	if moving {
		d.mu.Lock()
		if d.sharpen != nil {
			d.sharpen.Stop()
		}
		d.sharpen = time.AfterFunc(settle, func() { d.sharpenNow(out) })
		d.mu.Unlock()
	}
	return nil
}

// sharpenNow sends at full size what went at half, now the screen has stopped moving.
func (d *differ) sharpenNow(out *sender) {
	d.mu.Lock()
	rects := d.blurred
	d.blurred = nil
	last := d.last
	d.mu.Unlock()
	for _, r := range rects {
		if out.picture(r.Min, last.SubImage(r)) != nil {
			return
		}
	}
}

type patch struct {
	at  image.Point
	img image.Image
}

// tile is the grain changes are found at: fine enough that a clock ticking is a small picture,
// coarse enough that comparing is cheap.
const tile = 16

// changes is the parts of img that differ from the last frame, as few rectangles as cover them:
// the whole frame the first time, and nothing when nothing changed. Changes far apart - the clock
// at the top, a sensor at the bottom - go as separate pictures rather than one that spans both.
func (d *differ) changes(img image.Image) []patch {
	b := img.Bounds()
	cur := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(cur, cur.Rect, img, b.Min, draw.Src)

	d.mu.Lock()
	defer d.mu.Unlock()
	prev := d.last
	d.last = cur
	if prev == nil || prev.Rect != cur.Rect {
		return []patch{{at: image.Point{}, img: cur}}
	}

	var rects []image.Rectangle
	for ty := 0; ty < cur.Rect.Dy(); ty += tile {
		for tx := 0; tx < cur.Rect.Dx(); tx += tile {
			r := image.Rect(tx, ty, min(tx+tile, cur.Rect.Dx()), min(ty+tile, cur.Rect.Dy()))
			if !same(prev, cur, r) {
				rects = merge(rects, r)
			}
		}
	}
	out := make([]patch, 0, len(rects))
	for _, r := range rects {
		out = append(out, patch{at: r.Min, img: cur.SubImage(r)})
	}
	return out
}

func same(a, b *image.RGBA, r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		i := a.PixOffset(r.Min.X, y)
		j := i + r.Dx()*4
		if !bytes.Equal(a.Pix[i:j], b.Pix[i:j]) {
			return false
		}
	}
	return true
}

// merge adds r to the rectangles, joining it to any it touches or comes near, so a changed region
// arrives as one picture and unrelated ones stay apart.
func merge(rects []image.Rectangle, r image.Rectangle) []image.Rectangle {
	for {
		joined := false
		for i, have := range rects {
			if have.Inset(-tile).Overlaps(r) {
				r = r.Union(have)
				rects = append(rects[:i], rects[i+1:]...)
				joined = true
				break
			}
		}
		if !joined {
			return append(rects, r)
		}
	}
}

// keepOnDashboards looks at where the page is every second, and takes it back to its dashboard if it
// has got anywhere else: the page's own guard (browser.go) stops the frontend going there, and this
// is for whatever gets past it.
func keepOnDashboards(ctx context.Context, tab context.Context, home string, g *guard, name string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var path string
		if err := chromedp.Run(tab, chromedp.Evaluate(`location.pathname`, &path)); err != nil {
			continue
		}
		if ok, err := g.allows(ctx, path); err != nil || ok {
			continue
		}
		slog.Warn("the page left the dashboards; taking it back", "name", name, "was", path)
		_ = chromedp.Run(tab, chromedp.Navigate(home))
	}
}
