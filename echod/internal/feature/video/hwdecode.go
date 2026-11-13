//go:build !dot

package video

import (
	"bufio"
	"context"
	"image"
	"io"
	"log/slog"
	"slices"
	"time"
)

// Hardware decoders. A board whose SoC decodes video in hardware registers its decoders from a file
// of its own (registerDecoder, registerCameraDecoder, in an init); a video goes to the first that
// takes it and starts, in the order registered, and to ffmpeg on the cores otherwise. Nothing here
// knows any board: with none registered, every picture is decoded on the cores.

// hwDecoder decodes a video's picture, with ffmpeg still fetching the stream and making its sound.
type hwDecoder struct {
	name string // for the tests: which it is
	// usable is whether it takes the video onto the screen (its codec, size, the panel's pixels).
	usable func(i Info, s Screen) bool
	// start runs it. Its frames come in form, rather than as the whole panel's pixels.
	start func(ctx context.Context, d Decoding, sound bool) (*decoder, frameForm, error)
}

// frameForm is how a hardware decoder hands over its frames: only the picture's place of the panel's
// pixels (placed, the rows' other pixels left black), or as YUV in the decoder's own layout for the
// panel's presenter (display video_present.go). The zero form is ffmpeg's: the whole panel.
type frameForm struct {
	placed bool
	yuv    *YUVFrames
}

// YUVFrames is what a video's frames are when they come from a hardware decoder as it makes them:
// NV12 of W x H, to be drawn into Place on the canvas, with their color matrix and range.
type YUVFrames struct {
	W, H      int
	Place     Fit
	FullRange bool
	BT709     bool
}

// Bytes is one frame's size.
func (y *YUVFrames) Bytes() int { return y.W * y.H * 3 / 2 }

var hwDecoders []hwDecoder

// registerDecoder adds a board's decoder for videos, from an init in the board's own file. No board in
// this tree registers one, so ffmpeg decodes on the cores as ever.
//
//lint:ignore U1000 the extension point boards register their decoders for videos through; unused in this tree by design.
func registerDecoder(d hwDecoder) { hwDecoders = append(hwDecoders, d) }

// startDecoder runs the first hardware decoder that takes the video and starts, else ffmpeg on the
// cores, and says which.
func (s *session) startDecoder(d Decoding) (*decoder, frameForm, error) {
	for _, h := range hwDecoders {
		if !h.usable(d.Info, d.Screen) {
			continue
		}
		dec, form, err := h.start(s.ctx, d, s.sound)
		if err == nil {
			slog.Info("video: decoding", "id", s.id, "hardware", true)
			return dec, form, nil
		}
		slog.Warn("video: the hardware decoder did not start; on the cores", "id", s.id, "err", err)
	}
	dec, err := start(s.ctx, DecodeArgs(d), true, s.sound)
	if err != nil {
		return nil, frameForm{}, err
	}
	slog.Info("video: decoding", "id", s.id, "hardware", false)
	return dec, frameForm{}, nil
}

// useForm takes the decoder's form for the frames to come, with s.mu held: placed frames go at fit.
func (s *session) useForm(form frameForm, fit Fit) {
	switch {
	case form.yuv != nil:
		s.yuv = form.yuv
		s.most = max(queueBytes/form.yuv.Bytes(), 3)
	case form.placed:
		s.place = &fit
	}
}

// frameReader reads the decoder's frames, each into a frame's Pix, in the session's form.
func (s *session) frameReader(f io.Reader) func(pix []byte) error {
	size := FrameBytes(s.panelW, s.panelH)
	s.mu.Lock()
	place, yuv := s.place, s.yuv
	s.mu.Unlock()
	if yuv != nil {
		size, place = yuv.Bytes(), nil
	}
	r := f
	if place != nil && place.W != s.panelW {
		r = bufio.NewReaderSize(f, 1<<20) // a row at a time
	}
	return func(pix []byte) error { return readFrame(r, pix[:size], s.panelW, place) }
}

// newFrame is an empty frame of the session's form: placed frames are black, opaque, around their
// place, as ffmpeg pads, since only the place is read into.
func (s *session) newFrame() *Frame {
	size := FrameBytes(s.panelW, s.panelH)
	if s.yuv != nil {
		size = s.yuv.Bytes()
	}
	fr := &Frame{Pix: make([]byte, size)}
	if s.place != nil {
		for i := 3; i < len(fr.Pix); i += 4 {
			fr.Pix[i] = 255
		}
	}
	return fr
}

// readFrame reads one frame into pix, a panel panelW pixels wide: the whole of it, or with place, only
// the picture's place (the rows' other pixels left as they are).
func readFrame(r io.Reader, pix []byte, panelW int, place *Fit) error {
	if place == nil {
		_, err := io.ReadFull(r, pix)
		return err
	}
	stride, row := panelW*4, place.W*4
	if place.W == panelW {
		_, err := io.ReadFull(r, pix[place.Y*stride:(place.Y+place.H)*stride])
		return err
	}
	for y := place.Y; y < place.Y+place.H; y++ {
		o := y*stride + place.X*4
		if _, err := io.ReadFull(r, pix[o:o+row]); err != nil {
			return err
		}
	}
	return nil
}

// cameraDecoder decodes a camera's stream (a page's live picture) in hardware.
type cameraDecoder struct {
	name string
	// usable is whether it takes a w×h picture put there by filter.
	usable func(w, h int, filter string) bool
	// start runs it on in, the decoder's input options (cameraArgs's, without the output), and gives
	// the reader of its frames; soft starts the same camera on the cores, to fall back to.
	start func(ctx context.Context, in []string, w, h int, soft func() (*decoder, error)) (*decoder, cameraReader, error)
}

// cameraReader reads a hardware decoder's camera frames (Camera.Next), and may move the camera to the
// cores (c.d) when the hardware takes nothing of the stream; final is an error Next returns as it is
// (the cores' decoder would not start).
type cameraReader interface {
	next(c *Camera, img *image.RGBA, wait time.Duration) (final bool, err error)
}

var cameraDecoders []cameraDecoder

// registerCameraDecoder adds a board's decoder for camera pages, from an init in the board's own file.
// No board in this tree registers one, so ffmpeg decodes on the cores as ever.
//
//lint:ignore U1000 the extension point boards register their decoders for camera pages through; unused in this tree by design.
func registerCameraDecoder(d cameraDecoder) { cameraDecoders = append(cameraDecoders, d) }

// startCamera runs a camera's stream (args: cameraArgs or cameraRTSPArgs) w×h through the first
// hardware decoder that takes it and starts, else on the cores.
func startCamera(ctx context.Context, args []string, w, h int, filter string) (*Camera, error) {
	soft := func() (*decoder, error) { return start(ctx, args, true, false) }
	in := args[:max(slices.Index(args, "-map"), 0)]
	for _, cd := range cameraDecoders {
		if !cd.usable(w, h, filter) {
			continue
		}
		d, via, err := cd.start(ctx, slices.Clone(in), w, h, soft)
		if err == nil {
			return &Camera{d: d, w: w, h: h, via: via}, nil
		}
		slog.Warn("video: the hardware decoder did not start; the camera on the cores", "err", err)
	}
	d, err := soft()
	if err != nil {
		return nil, err
	}
	return &Camera{d: d, w: w, h: h}, nil
}
