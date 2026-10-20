//go:build linux

// Package camstream makes the 2nd gen Show 8's camera into H.264 on the SoC's own blocks: the ISP's
// pass 2 (the DIP, "mtk-dip") develops the sensor's packed RAW10 frame into NV12 in two planes, and
// the H.264 encoder ("mtk-vcodec-enc") reads those planes where they are, as dmabufs, and hands back
// the frame encoded. The cores copy the raw frame in and the encoded one out; the picture itself never
// passes through them. Both devices are the board kernel's, where its mainline kernel offers them (the
// SCP plans their work).
package camstream

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/v4l2m2m"
)

// Controls (videodev2.h): the DIP's leveling and the encoder's.
const (
	cidBlackLevel     = 0x0098090b
	cidRedBalance     = 0x0098090e
	cidBlueBalance    = 0x0098090f
	cidDigitalGain    = 0x009f0905
	cidGOPSize        = 0x009909cb
	cidBitrateMode    = 0x009909ce
	cidBitrate        = 0x009909cf
	cidHeaderMode     = 0x009909d8 // 1: the sequence header joined with the first frame
	cidForceKeyFrame  = 0x009909e5
	cidH264IPeriod    = 0x00990a66
	cidH264Profile    = 0x00990a6b // 4: High
	cidH264Level      = 0x00990a67 // 13: 4.2, which 1080 lines at 60 a second need
	h264Level42       = 13
	bitrateModeCBR    = 1
	headerJoined      = 1
	h264ProfileHigh   = 4
	encCaptureBuffers = 2 // the frame in the encoder and the next one queued behind it
	// the picture the encoder reads, the one the ISP develops, and a spare
	ispSlots = 3
)

// rawFourcc is the sensor's frame as the ISP takes it: the Show 8's OV13855 starts each cell blue
// (hardware/camera's blueFirst).
var rawFourcc = v4l2m2m.Fourcc("pBAA")

// Available is whether this board has the blocks.
func Available() bool {
	return v4l2m2m.Find("mtk-dip") != "" && v4l2m2m.Find("mtk-vcodec-enc") != ""
}

// Tone is how the ISP levels the frame (hardware/camera's ISPTone): the black level at 10 bits; the
// gains in 1/512.
type Tone struct{ Black, Red, Blue, Gain int }

// Config is the stream: its size (both a multiple of 16, the height of 32, for the encoder), rate,
// bitrate and keyframe spacing in frames. Crop takes the middle W x H of the sensor's frame pixel for
// pixel, instead of the whole frame scaled to W x H: a narrower view, at the sensor's own sharpness.
type Config struct {
	W, H, FPS, Bitrate, GOP int
	Crop                    bool
}

// Stream is one camera's H.264, frame by frame.
type Stream struct {
	mu         sync.Mutex
	cfg        Config
	rawW, rawH int
	inW, inH   int // what the ISP takes: the raw frame, or its middle when cropped
	cropX      int // where the crop starts in the raw frame, in pixels
	cropY      int
	inFmt      v4l2m2m.Format

	isp       *v4l2m2m.Device
	ispIn     *v4l2m2m.Buffer
	ispOut    *v4l2m2m.Buffer // the first of ispOuts, where the first frame is developed
	ispFmt    v4l2m2m.Format
	planeFDs  []int // its planes, as dmabufs
	enc       *v4l2m2m.Device
	encFmt    v4l2m2m.Format
	encOut    []*v4l2m2m.Buffer
	forceKey  bool
	closeOnce sync.Once

	// The ISP's pictures, so that it develops one frame while the encoder encodes the frame before
	// (Encode): pending is the slot the encoder has, -1 when none.
	ispOuts  []*v4l2m2m.Buffer
	planeSet [][]int
	slot     int
	pending  int
	bits     int // the encoder's bitstream buffer for the next picture
}

// New opens the blocks for raw frames of rawW x rawH made into cfg.
func New(rawW, rawH int, cfg Config) (*Stream, error) {
	if cfg.W%16 != 0 || cfg.H%32 != 0 {
		return nil, fmt.Errorf("camstream: %dx%d: the encoder wants the width a multiple of 16 and the height of 32", cfg.W, cfg.H)
	}
	s := &Stream{cfg: cfg, rawW: rawW, rawH: rawH, inW: rawW, inH: rawH, pending: -1}
	if cfg.Crop {
		if cfg.W > rawW || cfg.H > rawH {
			return nil, fmt.Errorf("camstream: a %dx%d crop of a %dx%d frame", cfg.W, cfg.H, rawW, rawH)
		}
		// The crop starts on a whole group of four packed pixels across (RAW10's five bytes) and on an
		// even line, so its first cell has the frame's color order.
		s.inW, s.inH = cfg.W, cfg.H
		s.cropX, s.cropY = (rawW-cfg.W)/2&^3, (rawH-cfg.H)/2&^1
	}
	if err := s.openISP(); err != nil {
		s.Close()
		return nil, fmt.Errorf("camstream: the ISP: %w", err)
	}
	if err := s.openEncoder(); err != nil {
		s.Close()
		return nil, fmt.Errorf("camstream: the encoder: %w", err)
	}
	return s, nil
}

func (s *Stream) openISP() error {
	d, err := v4l2m2m.Open("mtk-dip")
	if err != nil {
		return err
	}
	s.isp = d
	if s.inFmt, err = d.SetFormat(v4l2m2m.OutputMplane, s.inW, s.inH, rawFourcc); err != nil {
		return err
	}
	if s.ispFmt, err = d.SetFormat(v4l2m2m.CaptureMplane, s.cfg.W, s.cfg.H, v4l2m2m.Fourcc("NM12")); err != nil {
		return err
	}
	if len(s.ispFmt.Planes) != 2 {
		return errors.New("no two-plane NV12 (an older kernel)")
	}
	in, err := d.Request(v4l2m2m.OutputMplane, v4l2m2m.MemoryMmap, 1, 1)
	if err != nil {
		return err
	}
	s.ispIn = in[0]
	out, err := d.Request(v4l2m2m.CaptureMplane, v4l2m2m.MemoryMmap, ispSlots, 2)
	if err != nil {
		return err
	}
	if len(out) < ispSlots {
		return errors.New("fewer pictures than asked for")
	}
	s.ispOuts = out
	for _, b := range out {
		fds, err := d.Export(v4l2m2m.CaptureMplane, b)
		if err != nil {
			return err
		}
		s.planeSet = append(s.planeSet, fds)
	}
	s.ispOut, s.planeFDs = out[0], s.planeSet[0]
	for _, t := range []uint32{v4l2m2m.OutputMplane, v4l2m2m.CaptureMplane} {
		if err := d.Stream(t, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stream) openEncoder() error {
	d, err := v4l2m2m.Open("mtk-vcodec-enc")
	if err != nil {
		return err
	}
	s.enc = d
	// the bitstream buffer: as large as a raw frame, which no encoded one outgrows
	if _, err := d.SetFormat(v4l2m2m.CaptureMplane, s.cfg.W, s.cfg.H, v4l2m2m.Fourcc("H264"), s.cfg.W*s.cfg.H*3/2); err != nil {
		return err
	}
	f, err := d.SetFormat(v4l2m2m.OutputMplane, s.cfg.W, s.cfg.H, v4l2m2m.Fourcc("NM12"))
	if err != nil {
		return err
	}
	if len(f.Planes) != 2 || f.Planes[0].Size > s.ispFmt.Planes[0].Size || f.Planes[1].Size > s.ispFmt.Planes[1].Size {
		return fmt.Errorf("its planes %+v are larger than the ISP's %+v", f.Planes, s.ispFmt.Planes)
	}
	s.encFmt = f
	_ = d.SetRate(v4l2m2m.OutputMplane, s.cfg.FPS)
	for _, c := range [][2]int32{{cidHeaderMode, headerJoined}, {cidH264Profile, h264ProfileHigh},
		{cidBitrateMode, bitrateModeCBR}, {cidBitrate, int32(s.cfg.Bitrate)},
		{cidGOPSize, int32(s.cfg.GOP)}, {cidH264IPeriod, int32(s.cfg.GOP)}} {
		if err := d.SetCtrl(uint32(c[0]), c[1]); err != nil {
			return fmt.Errorf("control %#x: %w", c[0], err)
		}
	}
	// The level the stream says it keeps to: 4.2 above 30 frames a second, the encoder's highest. Left
	// at the driver's default if refused, which only understates it.
	if s.cfg.FPS > 30 {
		_ = d.SetCtrl(cidH264Level, h264Level42)
	}
	if _, err := d.Request(v4l2m2m.OutputMplane, v4l2m2m.MemoryDMABuf, ispSlots, 2); err != nil {
		return err
	}
	if s.encOut, err = d.Request(v4l2m2m.CaptureMplane, v4l2m2m.MemoryMmap, encCaptureBuffers, 1); err != nil {
		return err
	}
	for _, t := range []uint32{v4l2m2m.OutputMplane, v4l2m2m.CaptureMplane} {
		if err := d.Stream(t, true); err != nil {
			return err
		}
	}
	return nil
}

// Size is the stream's picture.
func (s *Stream) Size() (w, h int) { return s.cfg.W, s.cfg.H }

// ForceKey makes the next frame a keyframe (a viewer has just joined).
func (s *Stream) ForceKey() {
	s.mu.Lock()
	s.forceKey = true
	s.mu.Unlock()
}

// Encode develops raw (packed RAW10, bpl bytes a line) and has the encoder start on it, and returns
// the H.264 of the frame before: NAL units with start codes (the first frame's carries the sequence
// and picture parameter sets), and whether it is a keyframe; nothing for the first frame. The ISP
// develops a frame while the encoder encodes the one before, so a frame takes the longer of the two
// rather than both (1456x1088: 32.6 ms one after the other, too close to 30 frames a second's 33.3).
// The result is the caller's.
func (s *Stream) Encode(raw []byte, bpl int, t Tone) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isp == nil || s.enc == nil {
		return nil, false, errors.New("camstream: closed")
	}
	n := bpl * s.rawH
	if n > len(raw) || !s.cfg.Crop && n > len(s.ispIn.Planes[0]) {
		return nil, false, errors.New("camstream: a frame of another size")
	}
	// The ISP: the frame in, developed into the free slot's two planes
	for _, c := range [][2]int{{cidBlackLevel, t.Black}, {cidRedBalance, t.Red}, {cidBlueBalance, t.Blue}, {cidDigitalGain, t.Gain}} {
		if err := s.isp.SetCtrl(uint32(c[0]), int32(c[1])); err != nil {
			return nil, false, fmt.Errorf("camstream: the ISP's leveling: %w", err)
		}
	}
	slot := s.slot
	used := n
	if s.cfg.Crop {
		// the middle, line by line, into the ISP's lines
		dst, row := s.inFmt.Planes[0].BytesPerLine, s.inW*5/4
		if dst < row || dst*s.inH > len(s.ispIn.Planes[0]) {
			return nil, false, errors.New("camstream: the ISP's frame is too small for the crop")
		}
		for y := range s.inH {
			src := (s.cropY+y)*bpl + s.cropX*5/4
			copy(s.ispIn.Planes[0][y*dst:y*dst+row], raw[src:src+row])
		}
		used = dst * s.inH
	} else {
		copy(s.ispIn.Planes[0], raw[:n])
	}
	if err := s.isp.Queue(v4l2m2m.OutputMplane, v4l2m2m.MemoryMmap, 0, 1, []int{used}, nil); err != nil {
		return nil, false, fmt.Errorf("camstream: the ISP's frame: %w", err)
	}
	if err := s.isp.Queue(v4l2m2m.CaptureMplane, v4l2m2m.MemoryMmap, slot, 2, nil, nil); err != nil {
		return nil, false, fmt.Errorf("camstream: the ISP's picture: %w", err)
	}
	if _, err := s.isp.Dequeue(v4l2m2m.CaptureMplane, v4l2m2m.MemoryMmap); err != nil {
		return nil, false, fmt.Errorf("camstream: the ISP: %w", err)
	}
	if _, err := s.isp.Dequeue(v4l2m2m.OutputMplane, v4l2m2m.MemoryMmap); err != nil {
		return nil, false, err
	}
	// The frame before, done by the encoder meanwhile
	var out []byte
	var key bool
	if s.pending >= 0 {
		var err error
		if out, key, err = s.collect(); err != nil {
			return nil, false, err
		}
	}
	// To the encoder, which works on it until the next call
	if s.forceKey {
		_ = s.enc.SetCtrl(cidForceKeyFrame, 1)
		s.forceKey = false
	}
	if err := s.enc.Queue(v4l2m2m.CaptureMplane, v4l2m2m.MemoryMmap, s.bits, 1, nil, nil); err != nil {
		return nil, false, fmt.Errorf("camstream: the encoder's bitstream buffer: %w", err)
	}
	s.bits = (s.bits + 1) % encCaptureBuffers
	encUsed := []int{s.encFmt.Planes[0].Size, s.encFmt.Planes[1].Size}
	if err := s.enc.Queue(v4l2m2m.OutputMplane, v4l2m2m.MemoryDMABuf, slot, 2, encUsed, s.planeSet[slot]); err != nil {
		return nil, false, fmt.Errorf("camstream: the encoder's picture: %w", err)
	}
	s.pending = slot
	s.slot = (slot + 1) % ispSlots
	return out, key, nil
}

// collect waits for the frame the encoder has and returns its H.264, with s.mu held.
func (s *Stream) collect() ([]byte, bool, error) {
	done, err := s.enc.Dequeue(v4l2m2m.CaptureMplane, v4l2m2m.MemoryMmap)
	if err != nil {
		return nil, false, fmt.Errorf("camstream: the encoder: %w", err)
	}
	if _, err := s.enc.Dequeue(v4l2m2m.OutputMplane, v4l2m2m.MemoryDMABuf); err != nil {
		return nil, false, err
	}
	s.pending = -1
	out := append([]byte(nil), s.encOut[done.Index].Planes[0][:done.Used[0]]...)
	return out, done.Keyframe || hasIDR(out), nil
}

// hasIDR is whether the Annex-B frame holds an IDR slice (NAL type 5).
func hasIDR(b []byte) bool {
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 && b[i+3]&0x1f == 5 {
			return true
		}
	}
	return false
}

// Close lets the blocks go.
func (s *Stream) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, fds := range s.planeSet {
			for _, fd := range fds {
				unix.Close(fd)
			}
		}
		for _, b := range append(append([]*v4l2m2m.Buffer{s.ispIn}, s.ispOuts...), s.encOut...) {
			if b != nil {
				b.Unmap()
			}
		}
		if s.enc != nil {
			s.enc.Close()
			s.enc = nil
		}
		if s.isp != nil {
			s.isp.Close()
			s.isp = nil
		}
	})
}
