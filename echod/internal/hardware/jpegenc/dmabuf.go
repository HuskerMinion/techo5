//go:build linux

package jpegenc

import (
	"errors"
	"fmt"
	"image"
	"time"
)

// EncodeDMABuf makes a JPEG of a w x h YUYV picture held in a dmabuf (the GPU's), stride bytes a row,
// which the encoder reads where it is: no picture passes through the cores. The buffer must hold the
// picture until this returns. The descriptor stays the caller's.
//
// It backs off after a failure as EncodeYUYV does, but on its own: a kernel or a buffer the encoder
// cannot import from leaves EncodeYUYV as it was, so a caller that falls back to it loses nothing.
func EncodeDMABuf(dmabuf, w, h, stride, quality int) ([]byte, error) {
	if !Available() {
		return nil, errors.New("jpegenc: no hardware encoder")
	}
	if dmabuf < 0 || !Fits(image.Rect(0, 0, w, h)) || stride < w*2 {
		return nil, fmt.Errorf("jpegenc: %dx%d, %d bytes a row, is not a picture the encoder takes", w, h, stride)
	}
	if until := failedDMABuf.until(); now().Before(until) {
		return nil, fmt.Errorf("%w until %s", ErrBackingOff, until.Format(time.TimeOnly))
	}
	jpg, err := attemptDMABuf(dmabuf, w, h, stride, quality)
	if err != nil {
		failedDMABuf.set(now().Add(backOff))
	}
	return jpg, err
}

// failedDMABuf is when EncodeDMABuf may try the encoder again after a failure.
var failedDMABuf backoffUntil

// attemptDMABuf is one dmabuf picture through the device; tests replace it.
var attemptDMABuf = encodeDMABufOnDevice

// encodeDMABufOnDevice is one dmabuf picture through the session for its size, quality and stride.
func encodeDMABufOnDevice(dmabuf, w, h, stride, quality int) ([]byte, error) {
	s, err := take(w, h, quality, stride)
	if err != nil {
		return nil, err
	}
	defer give(s)
	s.mu.Lock()
	defer s.mu.Unlock()
	jpg, err := s.encode(dmabuf)
	if err != nil {
		mu.Lock()
		s.broken = true
		mu.Unlock()
	}
	return jpg, err
}
