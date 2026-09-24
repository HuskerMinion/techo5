//go:build !dot

package dashboard

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
	"time"
)

func jpegOf(w, h int) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil)
	return b.Bytes()
}

// A picture larger than the screen is not decoded at all.
func TestAPictureLargerThanTheScreenIsRefused(t *testing.T) {
	if _, err := decodeWithin(jpegOf(100, 50), 100, 50); err != nil {
		t.Errorf("a picture the screen's size: %v", err)
	}
	if _, err := decodeWithin(jpegOf(101, 50), 100, 50); err == nil {
		t.Error("a picture wider than the screen was decoded")
	}
}

// A half-size picture anywhere, even off the screen or running off its edge, paints without
// panicking, and only inside the frame.
func TestDoubledPicturesStayInTheFrame(t *testing.T) {
	s := &stream{f: &Feature{}, w: 100, h: 60}
	small := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for _, at := range []image.Point{{0, 0}, {90, 50}, {99, 59}, {100, 0}, {0, 60}, {500, 500}, {-5, 3}} {
		s.paintDoubled(at, small)
	}
	if s.frame != nil && s.frame.Rect != image.Rect(0, 0, 100, 60) {
		t.Errorf("frame %v", s.frame.Rect)
	}
}

// A history keeps its newest points and stays bounded however many arrive.
func TestHistoryIsBounded(t *testing.T) {
	var h []point
	now := time.Now()
	for i := 0; i < 10*historyMost; i++ {
		h = keep(append(h, point{at: now.Add(time.Duration(i-10*historyMost) * time.Second), v: float64(i)}), 24)
	}
	if len(h) > historyMost+1 {
		t.Errorf("%d points kept", len(h))
	}
	if h[len(h)-1].v != float64(10*historyMost-1) {
		t.Errorf("the newest point was lost")
	}
}
