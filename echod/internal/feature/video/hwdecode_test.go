//go:build !dot

package video

import (
	"bytes"
	"context"
	"errors"
	"image"
	"slices"
	"testing"
	"time"
)

func withDecoders(t *testing.T, ds ...hwDecoder) {
	t.Helper()
	saved := hwDecoders
	hwDecoders = ds
	t.Cleanup(func() { hwDecoders = saved })
}

// A video goes to the first hardware decoder that takes it and starts, in the order registered: one
// that does not take it is not started, one that fails to start is passed over.
func TestHardwareDecodersAreTriedInOrder(t *testing.T) {
	var started []string
	dec := &decoder{done: make(chan struct{})}
	fake := func(name string, usable bool, err error, form frameForm) hwDecoder {
		return hwDecoder{name: name,
			usable: func(Info, Screen) bool { return usable },
			start: func(context.Context, Decoding, bool) (*decoder, frameForm, error) {
				started = append(started, name)
				if err != nil {
					return nil, frameForm{}, err
				}
				return dec, form, nil
			}}
	}
	withDecoders(t,
		fake("refuses", false, nil, frameForm{}),
		fake("fails", true, errors.New("no device"), frameForm{}),
		fake("takes", true, nil, frameForm{placed: true}),
		fake("after", true, nil, frameForm{}))
	s := &session{ctx: context.Background(), id: 1}
	got, form, err := s.startDecoder(Decoding{})
	if err != nil || got != dec || !form.placed {
		t.Fatalf("%v %+v %v, want the third's placed frames", got, form, err)
	}
	if !slices.Equal(started, []string{"fails", "takes"}) {
		t.Fatalf("started %v", started)
	}
}

// With no hardware decoder that takes the video, ffmpeg decodes it on the cores: the whole panel.
func TestWithNoHardwareDecoderTheCoresDecode(t *testing.T) {
	withDecoders(t, hwDecoder{name: "refuses", usable: func(Info, Screen) bool { return false }})
	restore := UseDecoderForTests("/bin/true", nil)
	defer restore()
	s := &session{ctx: context.Background(), id: 1}
	d, form, err := s.startDecoder(Decoding{URL: "http://192.0.2.1/v.mp4", Info: Info{VideoIndex: 0, AudioIndex: -1}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.stop()
	if form != (frameForm{}) {
		t.Fatalf("form %+v on the cores", form)
	}
}

// Placed frames are read into their place only, the bars left black and opaque; YUV frames are the
// decoder's own size, and the queue holds as many of them as the bytes allow.
func TestFrameForms(t *testing.T) {
	s := &session{panelW: 4, panelH: 2, most: 2}
	s.useForm(frameForm{placed: true}, Fit{W: 2, H: 1, X: 1, Y: 1})
	fr := s.newFrame()
	if len(fr.Pix) != 4*2*4 || fr.Pix[3] != 255 || fr.Pix[0] != 0 {
		t.Fatalf("a placed frame starts %v", fr.Pix[:4])
	}
	read := s.frameReader(bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
	if err := read(fr.Pix); err != nil {
		t.Fatal(err)
	}
	if o := (1*4 + 1) * 4; !bytes.Equal(fr.Pix[o:o+8], []byte{1, 2, 3, 4, 5, 6, 7, 8}) || fr.Pix[3] != 255 {
		t.Fatalf("placed pixels %v", fr.Pix)
	}

	y := &YUVFrames{W: 640, H: 360}
	s = &session{panelW: 4, panelH: 2, most: 2}
	s.useForm(frameForm{yuv: y}, Fit{})
	if fr := s.newFrame(); len(fr.Pix) != y.Bytes() || s.most != max(queueBytes/y.Bytes(), 3) {
		t.Fatalf("a YUV frame of %d bytes, %d queued", len(fr.Pix), s.most)
	}
}

// A camera goes to the first camera decoder that takes it and starts, with the stream's input
// options alone; one that fails leaves the camera on the cores, with the whole of cameraArgs.
func TestCameraDecoders(t *testing.T) {
	saved := cameraDecoders
	t.Cleanup(func() { cameraDecoders = saved })
	args := cameraArgs("http://192.0.2.1/cam.m3u8", 64, 48, false, "", fitFilter(64, 48))
	var gotIn []string
	via := &stubReader{}
	cameraDecoders = []cameraDecoder{
		{name: "refuses", usable: func(int, int, string) bool { return false }},
		{name: "takes", usable: func(w, h int, f string) bool { return f == fitFilter(w, h) },
			start: func(_ context.Context, in []string, _, _ int, _ func() (*decoder, error)) (*decoder, cameraReader, error) {
				gotIn = in
				return &decoder{done: make(chan struct{})}, via, nil
			}},
	}
	c, err := startCamera(context.Background(), args, 64, 48, fitFilter(64, 48))
	if err != nil || c.via != via {
		t.Fatalf("%+v (%v), want the hardware's reader", c, err)
	}
	if i := slices.Index(args, "-map"); !slices.Equal(gotIn, args[:i]) {
		t.Fatalf("the hardware was given %q, want the input %q", gotIn, args[:i])
	}

	restore := UseDecoderForTests("/bin/true", nil)
	defer restore()
	cameraDecoders = []cameraDecoder{{name: "fails", usable: func(int, int, string) bool { return true },
		start: func(context.Context, []string, int, int, func() (*decoder, error)) (*decoder, cameraReader, error) {
			return nil, nil, errors.New("no device")
		}}}
	c, err = startCamera(context.Background(), args, 64, 48, fitFilter(64, 48))
	if err != nil || c.via != nil {
		t.Fatalf("%+v (%v), want the cores", c, err)
	}
	c.d.stop()
}

type stubReader struct{}

func (*stubReader) next(*Camera, *image.RGBA, time.Duration) (bool, error) { return false, nil }
