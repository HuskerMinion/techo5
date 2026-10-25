//go:build !dot

package screen

import (
	"image"
	"testing"
)

// A filter is laid over what is sent, never over the canvas, and a row is filtered again only once it
// is drawn again.
func TestFilterLeavesTheCanvasAndRefiltersChangedRows(t *testing.T) {
	d := &Device{canvas: image.NewRGBA(image.Rect(0, 0, 4, 3))}
	calls := map[int]int{}
	invert := func(row []byte, y int) {
		calls[y]++
		for i := range row {
			row[i] = ^row[i]
		}
	}
	d.SetFilter("invert", invert)
	out := d.source()
	if out == d.canvas {
		t.Fatal("a filtered frame is the canvas itself")
	}
	if d.canvas.Pix[0] != 0 || out.Pix[0] != 0xff {
		t.Fatalf("canvas %d, sent %d: want 0 and 255", d.canvas.Pix[0], out.Pix[0])
	}
	if calls[0] != 1 || calls[1] != 1 || calls[2] != 1 {
		t.Fatalf("first frame filtered rows %v, want each once", calls)
	}

	d.canvas.Pix[1*d.canvas.Stride] = 7 // row 1 drawn again
	out = d.source()
	if calls[0] != 1 || calls[1] != 2 || calls[2] != 1 {
		t.Fatalf("second frame filtered rows %v, want only row 1 again", calls)
	}
	if out.Pix[1*out.Stride] != ^byte(7) {
		t.Fatalf("row 1 sent %d, want the filtered new pixel", out.Pix[1*out.Stride])
	}

	d.SetFilter("", nil)
	if d.source() != d.canvas {
		t.Fatal("with no filter the canvas is not what is sent")
	}
}
