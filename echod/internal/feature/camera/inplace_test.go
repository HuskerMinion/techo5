package camera

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/jpegenc"
)

// Where the encoder makes the pages' JPEGs, an in-place encoder that takes the frame makes it, in the
// order they are listed; one that does not take it is passed over.
func TestInPlaceEncodersComeFirst(t *testing.T) {
	savedHW, savedEncs := hwEncode, inPlaceEncoders
	t.Cleanup(func() { hwEncode, inPlaceEncoders = savedHW, savedEncs })
	hwEncode = true
	var asked []string
	inPlaceEncoders = []func(*camera.Frame) ([]byte, bool){
		func(*camera.Frame) ([]byte, bool) { asked = append(asked, "declines"); return nil, false },
		func(*camera.Frame) ([]byte, bool) { asked = append(asked, "takes"); return []byte("jpeg"), true },
		func(*camera.Frame) ([]byte, bool) { asked = append(asked, "after"); return nil, false },
	}
	b, err := encodeFrame(&camera.Frame{})
	if err != nil || !bytes.Equal(b, []byte("jpeg")) || len(asked) != 2 || asked[1] != "takes" {
		t.Fatalf("%q (%v) after %v", b, err, asked)
	}
}

// The GPU's path is the one in-place encoder, and a frame the GPU has nothing to develop of is left to
// encodeOnEncoder without the encoder being asked.
func TestGPUPathDeclinesAFrameItCannotDevelop(t *testing.T) {
	savedEnc := encodeDMABuf
	t.Cleanup(func() { encodeDMABuf = savedEnc; gpuRest = time.Time{} })
	encodeDMABuf = func(int, int, int, int, int) ([]byte, error) {
		t.Fatal("the encoder was asked for a frame the GPU did not develop")
		return nil, nil
	}
	if len(inPlaceEncoders) != 1 {
		t.Fatalf("%d in-place encoders, want the GPU's alone", len(inPlaceEncoders))
	}
	if _, ok := inPlaceEncoders[0](&camera.Frame{}); ok {
		t.Fatal("the GPU's path took a frame with nothing in it")
	}
}

// fakeGPUFrame is a frame the GPU develops (or not) into a dmabuf.
type fakeGPUFrame struct {
	fd, stride, w, h int
	ok               bool
	asked            *int
}

func (f fakeGPUFrame) GPUYUYV() (int, int, int, int, bool) {
	*f.asked++
	return f.fd, f.stride, f.w, f.h, f.ok
}

// A frame the GPU develops is encoded from the GPU's buffer, at its size and stride and at
// qualityEncoder, and that JPEG is the page's.
func TestGPUFrameEncodedInPlace(t *testing.T) {
	savedEnc := encodeDMABuf
	t.Cleanup(func() { encodeDMABuf = savedEnc; gpuRest = time.Time{} })
	var got string
	encodeDMABuf = func(fd, w, h, stride, q int) ([]byte, error) {
		got = fmt.Sprintf("fd %d, %dx%d, %d a row, quality %d", fd, w, h, stride, q)
		return []byte("jpeg"), nil
	}
	asked := 0
	f := fakeGPUFrame{fd: 7, stride: 4224, w: 2112, h: 1568, ok: true, asked: &asked}
	b, ok := encodeGPUFrame(f, time.Now)
	want := fmt.Sprintf("fd 7, 2112x1568, 4224 a row, quality %d", qualityEncoder)
	if !ok || string(b) != "jpeg" || got != want {
		t.Fatalf("%q, %v: the encoder was given %q, want %q", b, ok, got, want)
	}
}

// A frame the GPU could not develop, or one whose encode failed, goes to encodeOnEncoder, and so does
// every frame for a minute after, without the GPU or the encoder being asked; then the GPU is tried
// again. Failed encodes are logged once a minute at most, and those refused while jpegenc backs off not
// at all.
func TestGPUPathRestsAfterAFailure(t *testing.T) {
	savedEnc, savedWarned := encodeDMABuf, gpuWarned.Load()
	t.Cleanup(func() { encodeDMABuf = savedEnc; gpuRest = time.Time{}; gpuWarned.Store(savedWarned) })
	gpuRest = time.Time{}
	gpuWarned.Store(0)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	encodes := 0
	var encErr error
	encodeDMABuf = func(int, int, int, int, int) ([]byte, error) {
		encodes++
		if encErr != nil {
			return nil, encErr
		}
		return []byte("jpeg"), nil
	}
	asked := 0
	frame := func(ok bool) fakeGPUFrame {
		return fakeGPUFrame{fd: 7, stride: 4224, w: 2112, h: 1568, ok: ok, asked: &asked}
	}

	// The GPU can't develop it.
	if _, ok := encodeGPUFrame(frame(false), now); ok || asked != 1 || encodes != 0 {
		t.Fatalf("a frame the GPU did not develop: ok=%v, GPU asked %d, encoder %d", ok, asked, encodes)
	}
	clock = clock.Add(gpuRestFor - time.Millisecond)
	if _, ok := encodeGPUFrame(frame(true), now); ok || asked != 1 {
		t.Fatalf("within the minute after: ok=%v, GPU asked %d; want it left alone", ok, asked)
	}
	clock = clock.Add(time.Millisecond)
	if b, ok := encodeGPUFrame(frame(true), now); !ok || string(b) != "jpeg" || asked != 2 {
		t.Fatalf("a minute after: %q, %v, GPU asked %d; want it tried again", b, ok, asked)
	}

	// The encoder fails the GPU's picture: logged, and the GPU rests.
	encErr = errors.New("jpegenc: dmabuf picture buffer: invalid argument")
	if _, ok := encodeGPUFrame(frame(true), now); ok || encodes != 2 || gpuWarned.Load() != clock.UnixNano() {
		t.Fatalf("a failed encode: ok=%v after %d encodes, logged at %d", ok, encodes, gpuWarned.Load())
	}
	clock = clock.Add(gpuRestFor / 2)
	if _, ok := encodeGPUFrame(frame(true), now); ok || asked != 3 || encodes != 2 {
		t.Fatalf("half a minute after: ok=%v, GPU asked %d, encoder %d; want both left alone", ok, asked, encodes)
	}

	// Refused while jpegenc backs off: not logged.
	logged := gpuWarned.Load()
	clock = clock.Add(2 * time.Minute)
	encErr = fmt.Errorf("%w until 12:04:00", jpegenc.ErrBackingOff)
	if _, ok := encodeGPUFrame(frame(true), now); ok || encodes != 3 || gpuWarned.Load() != logged {
		t.Fatalf("refused while backing off: ok=%v after %d encodes, logged again: %v", ok, encodes, gpuWarned.Load() != logged)
	}
}
