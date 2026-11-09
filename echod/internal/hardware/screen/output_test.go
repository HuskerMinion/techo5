//go:build !dot && !spot && !panel

package screen

import (
	"errors"
	"image"
	"image/color"
	"testing"
)

// memOutput is an output whose pages are in memory, for the tests.
type memOutput struct {
	pages    [][]byte
	shown    []int
	own      bool // present the frame itself
	drawings int
	left     bool
}

func (o *memOutput) page(i int) ([]byte, error) { return o.pages[i], nil }
func (o *memOutput) show(i int) error           { o.shown = append(o.shown, i); return nil }
func (o *memOutput) present(d *Device, img *image.RGBA, dst []byte, i int) bool {
	if o.own {
		copy(dst, img.Pix)
	}
	return o.own
}
func (o *memOutput) drawing()        { o.drawings++ }
func (o *memOutput) leave(d *Device) { o.left = true }
func (o *memOutput) String() string  { return "memory" }

// useOutputs registers os for a test, and takes them away after it.
func useOutputs(t *testing.T, os ...outputOpener) {
	was := outputs
	outputs = os
	t.Cleanup(func() { outputs = was })
}

// outputDevice is a 4x2 landscape Device of two pages on o, in the canvas's byte order.
func outputDevice(t *testing.T, o *memOutput) *Device {
	was := rotated
	rotated = false
	t.Cleanup(func() { rotated = was })
	const w, h = 4, 2
	o.pages = [][]byte{make([]byte, w*4*h), make([]byte, w*4*h)}
	d := &Device{out: o, pages: 2, panelW: w, panelH: h, line: w * 4, pageBytes: w * 4 * h, shift: [4]uint{0, 8, 16, 24}}
	d.shadow = [][]byte{make([]byte, d.pageBytes), make([]byte, d.pageBytes)}
	d.row = make([]byte, w*4)
	d.canvas = image.NewRGBA(image.Rect(0, 0, w, h))
	return d
}

// Open takes the first output that opens, skipping one this device does not have, and the frames go
// to its pages, shown in turn; Close hands the panel back through it.
func TestAnOutputTakesThePanel(t *testing.T) {
	o := &memOutput{}
	useOutputs(t,
		outputOpener{name: "absent", open: func() (*Device, error) { return nil, errNoOutput }},
		outputOpener{name: "broken", open: func() (*Device, error) { return nil, errors.New("no card") }},
		outputOpener{name: "memory", open: func() (*Device, error) { return outputDevice(t, o), nil }})
	d, err := Open()
	if err != nil || d.out != o {
		t.Fatalf("Open: %v, out %v", err, d)
	}
	d.Canvas().Set(1, 1, color.RGBA{R: 200, A: 255})
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	d.Canvas().Set(2, 0, color.RGBA{B: 50, A: 255}) // a frame that differs, so it is presented too
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	if len(o.shown) != 2 || o.shown[0] == o.shown[1] {
		t.Fatalf("pages shown %v, want the two in turn", o.shown)
	}
	at := 1*d.line + 1*4
	for i, p := range o.pages {
		if p[at] != 200 {
			t.Errorf("page %d: red %d at (1,1), want 200", i, p[at])
		}
	}
	if o.drawings != 2 {
		t.Errorf("%d drawings, want one a frame", o.drawings)
	}
	if err := d.Close(); err != nil || !o.left {
		t.Fatalf("Close: %v, left %v", err, o.left)
	}
}

// An output may present a frame its own way; the package's row copy is then not done.
func TestAnOutputMayPresentItsOwnWay(t *testing.T) {
	o := &memOutput{own: true}
	d := outputDevice(t, o)
	d.Canvas().Set(0, 0, color.RGBA{G: 90, A: 255})
	if err := d.Present(); err != nil || len(o.shown) != 1 {
		t.Fatalf("Present: %v, shown %v", err, o.shown)
	}
	p := o.shown[0]
	if o.pages[p][1] != 90 || d.shadow[p][1] != 0 {
		t.Fatalf("page green %d, shadow %d: want the output's own copy, the shadow untouched", o.pages[0][1], d.shadow[0][1])
	}
}
