//go:build !dot

package camstream

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	hwstream "github.com/HuskerMinion/techo5/echod/internal/hardware/camstream"
)

// H.264, where the board has an encoder: the 2nd gen Show 8's ISP develops the sensor's frame and its
// encoder encodes it, both reading the other's buffers in place (hardware/camstream). The stream is
// then the sensor's own rate at the most the encoder takes, a keyframe a second and one for each
// viewer that joins, with the cores only moving bytes; elsewhere it is the JPEGs of camstream.go.

const (
	h264Type  = 96
	h264Clock = 90000
	// stall lets the viewers go when the camera sends nothing for this long.
	stall = 10 * time.Second
)

// The Show 8's stream: the encoder's largest picture with the sensor's shape (1456x1088 of the
// 2112x1568 stream mode: the encoder takes 1088 lines at most), at the sensor's own rate, which the ISP
// and the encoder keep up with (15 ms a frame between them): 60 a second in good light, slower as the
// light falls, since the exposure is lengthened before the picture is amplified (hardware/camera). The
// picture comes first: the bitrate is the most a frame had at 30 a second and more (16 Mbit/s over 60,
// about 270 kbit a frame), and the encoder keeps that share per frame however slow the light makes it.
var ispWhole = hwstream.Config{W: 1456, H: 1088, FPS: 60, Bitrate: 16_000_000, GOP: 60}

// ispCrop is the Show 8's stream with the crop switch on (config.CamStream.Crop): the middle 1920x1088
// of the frame pixel for pixel, a view a tenth narrower at the sensor's own sharpness; the bitrate grows
// with the picture.
var ispCrop = hwstream.Config{W: 1920, H: 1088, FPS: 60, Bitrate: 21_000_000, GOP: 60, Crop: true}

func ispStream() hwstream.Config {
	if config.Get().CamStream.Crop {
		return ispCrop
	}
	return ispWhole
}

// hasEncoder is whether the board has the blocks to make H.264 with.
func hasEncoder() bool { return hwstream.Available() }

// CropOffered is whether the stream can be cropped instead of scaled: the Show 8's.
func CropOffered() bool { return hwstream.Available() }

// Described is the stream as it is sent: its codec, size, rate and bitrate, for ONVIF and the screen.
type Described struct {
	Codec              string // "H264" or "JPEG"
	W, H, FPS, Bitrate int
}

// Stream is the stream this board sends.
func Stream() Described {
	switch {
	case hwstream.Available():
		c := ispStream()
		return Described{"H264", c.W, c.H, c.FPS, c.Bitrate}
	}
	w, h := size()
	return Described{"JPEG", w, h, maxFPS, 0}
}

// encoder makes the camera's frames into H.264 on the board's blocks, in two steps that run side by
// side on consecutive frames (pumpH264): prepare does the cores' part of a frame (its tone statistics),
// encode hands it to the blocks.
type encoder interface {
	// prepare readies a frame for encode; errHeldOff for a frame with nothing in it (the mute button).
	prepare(fr *camera.Frame) (prepared, error)
	// encode makes one prepared frame into its H.264 (Annex-B) and says whether it is a keyframe;
	// nothing for a frame still in the encoder.
	encode(p prepared) (au []byte, key bool, err error)
	forceKey()
	close()
}

// prepared is a frame readied for the encoder: the raw lines and their leveling.
type prepared struct {
	raw       []byte
	w, h, bpl int
	tone      camera.ISPTone
}

var errHeldOff = errors.New("the camera is held off")

func newEncoder() encoder {
	return &ispEncoder{}
}

type ispEncoder struct {
	s *hwstream.Stream
}

func (e *ispEncoder) prepare(fr *camera.Frame) (prepared, error) {
	raw, w, h, bpl, t, ok := fr.ISPRaw()
	if !ok {
		return prepared{}, errHeldOff
	}
	return prepared{raw: raw, w: w, h: h, bpl: bpl, tone: t}, nil
}

func (e *ispEncoder) encode(p prepared) ([]byte, bool, error) {
	if e.s == nil {
		s, err := hwstream.New(p.w, p.h, ispStream())
		if err != nil {
			return nil, false, err
		}
		e.s = s
	}
	return e.s.Encode(p.raw, p.bpl, hwstream.Tone(p.tone))
}

func (e *ispEncoder) forceKey() {
	if e.s != nil {
		e.s.ForceKey()
	}
}

func (e *ispEncoder) close() {
	if e.s != nil {
		e.s.Close()
	}
}

// h264Picture is one access unit, as a picture to send.
type h264Picture struct {
	au    []byte
	key   bool
	taken time.Time
}

// Packets is the access unit as RTP (RFC 6184: single NAL units, and FU-A for those larger than a
// packet), the marker on its last.
func (p h264Picture) Packets(seq *uint16, ts, ssrc uint32, mtu int) [][]byte {
	var pay codecs.H264Payloader
	var payloads [][]byte
	for _, n := range nalUnits(p.au) {
		payloads = append(payloads, pay.Payload(uint16(mtu-12), append([]byte{0, 0, 0, 1}, n...))...)
	}
	out := make([][]byte, 0, len(payloads))
	for i, b := range payloads {
		pk := rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: h264Type, SequenceNumber: *seq,
			Timestamp: ts, SSRC: ssrc, Marker: i == len(payloads)-1}, Payload: b}
		*seq++
		if m, err := pk.Marshal(); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func (p h264Picture) keyframe() bool { return p.key }
func (p h264Picture) at() time.Time  { return p.taken }

// sdpH264 is the stream's description, with its parameter sets once the encoder has made them.
func (f *Feature) sdpH264() string {
	f.mu.Lock()
	sps, pps := f.sps, f.pps
	f.mu.Unlock()
	fmtp := "packetization-mode=1"
	if len(sps) > 3 && pps != nil {
		fmtp += fmt.Sprintf(";profile-level-id=%x;sprop-parameter-sets=%s,%s", sps[1:4],
			base64.StdEncoding.EncodeToString(sps), base64.StdEncoding.EncodeToString(pps))
	}
	return fmt.Sprintf("m=video 0 RTP/AVP %d\r\na=rtpmap:%d H264/%d\r\na=fmtp:%d %s\r\n",
		h264Type, h264Type, h264Clock, h264Type, fmtp)
}

// withParameterSets keeps the stream's SPS and PPS as the encoder makes them, and puts them in front of
// a keyframe that lacks them, so a viewer can start at any keyframe.
func (f *Feature) withParameterSets(au []byte, key bool) []byte {
	var sps, pps []byte
	for _, n := range nalUnits(au) {
		switch n[0] & 0x1f {
		case 7:
			sps = n
		case 8:
			pps = n
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if sps != nil {
		f.sps = append([]byte(nil), sps...)
	}
	if pps != nil {
		f.pps = append([]byte(nil), pps...)
	}
	if key && sps == nil && f.sps != nil && f.pps != nil {
		pre := append([]byte{0, 0, 0, 1}, f.sps...)
		pre = append(append(pre, 0, 0, 0, 1), f.pps...)
		return append(pre, au...)
	}
	return au
}

// nalUnits splits an Annex-B access unit into its NAL units, start codes off.
func nalUnits(b []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+2 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end--
				}
				out = append(out, b[start:end])
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// pumpH264 encodes every frame the camera sends on the board's encoder and hands each access unit to
// every viewer, until stop; when the camera stops or is held off, every viewer is let go.
func (f *Feature) pumpH264(stop <-chan struct{}, release func()) {
	defer release()
	frames := make(chan *camera.Frame, 1)
	defer camera.Get().Frames.Listen(func(fr *camera.Frame) {
		select {
		case frames <- fr:
		default: // still encoding the last one
		}
	})()
	enc := newEncoder()
	defer enc.close()
	f.mu.Lock()
	f.forceKey = enc.forceKey
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.forceKey = nil
		f.mu.Unlock()
	}()
	crop := config.Get().CamStream.Crop
	// The cores' part of a frame runs on its own goroutine, a frame ahead of the blocks' part: the two
	// take a frame together only as long as the slower of them.
	type ready struct {
		p   prepared
		at  time.Time
		err error
	}
	readied := make(chan ready, 1)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		defer close(readied)
		for {
			var fr *camera.Frame
			select {
			case <-quit:
				return
			case fr = <-frames:
			case <-time.After(stall):
				slog.Info("camera streaming: the camera stopped sending frames")
				readied <- ready{err: errHeldOff}
				return
			}
			p, err := enc.prepare(fr)
			select {
			case readied <- ready{p, fr.At, err}:
			case <-quit:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var r ready
		var ok bool
		select {
		case <-stop:
			return
		case r, ok = <-readied:
		}
		if !ok || r.err != nil {
			if r.err != nil && !errors.Is(r.err, errHeldOff) {
				slog.Error("camera streaming: a frame", "err", r.err)
			}
			f.letAllGo()
			return
		}
		if config.Get().CamStream.Crop != crop {
			f.letAllGo() // the crop switch: the viewers come back to the stream made anew
			return
		}
		au, key, err := enc.encode(r.p)
		if err != nil {
			slog.Error("camera streaming: encoding", "err", err)
			f.letAllGo()
			return
		}
		if len(au) == 0 {
			continue // the first frame: the encoder hands it back with the next
		}
		f.hand(h264Picture{au: f.withParameterSets(au, key), key: key, taken: r.at})
	}
}
