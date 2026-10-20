//go:build !dot

package video

import (
	"bytes"
	"strings"
	"testing"
)

func TestHWUsable(t *testing.T) {
	ok := Info{VideoCodec: "h264", PixFmt: "yuv420p", Width: 1920, Height: 1080}
	scr := Screen{W: 1280, H: 800, PixFmt: "rgba"}
	if !HWUsable(ok, scr, true) {
		t.Fatal("1080p H.264 4:2:0 refused")
	}
	for name, c := range map[string]struct {
		i Info
		s Screen
		p bool
	}{
		"absent":     {ok, scr, false},
		"mpeg4":      {Info{VideoCodec: "mpeg4", PixFmt: "yuv420p", Width: 640, Height: 480}, scr, true},
		"10-bit":     {Info{VideoCodec: "h264", PixFmt: "yuv420p10le", Width: 1920, Height: 1080}, scr, true},
		"4:2:2":      {Info{VideoCodec: "h264", PixFmt: "yuv422p", Width: 1920, Height: 1080}, scr, true},
		"interlaced": {Info{VideoCodec: "h264", PixFmt: "yuv420p", Width: 1920, Height: 1080, Interlaced: true}, scr, true},
		"4K":         {Info{VideoCodec: "h264", PixFmt: "yuv420p", Width: 3840, Height: 2160}, scr, true},
		"turned":     {Info{VideoCodec: "h264", PixFmt: "yuv420p", Width: 1920, Height: 1080, Rotation: -90}, scr, true},
		"portrait":   {ok, Screen{W: 960, H: 480, Rotated: true, PixFmt: "bgra"}, true},
		"odd panel":  {ok, Screen{W: 1280, H: 800, PixFmt: "argb"}, true},
	} {
		if HWUsable(c.i, c.s, c.p) {
			t.Errorf("%s: taken by the hardware", name)
		}
	}
}

func TestProbePixFmt(t *testing.T) {
	out := `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'http://x/a.mp4':
  Duration: 00:00:20.00, start: 0.000000, bitrate: 6000 kb/s
  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(progressive), 1920x1080 [SAR 1:1 DAR 16:9], 5864 kb/s, 30 fps, 30 tbr, 15360 tbn (default)
  Stream #0:1[0x2](und): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo, fltp, 128 kb/s (default)
`
	i, err := ParseProbe(out)
	if err != nil || i.PixFmt != "yuv420p" || i.Interlaced {
		t.Fatalf("got %+v, %v", i, err)
	}
	i, _ = ParseProbe(strings.Replace(out, "yuv420p(progressive)", "yuv420p(tv, bt709, top first)", 1))
	if i.PixFmt != "yuv420p" || !i.Interlaced {
		t.Fatalf("interlaced: got %q %v", i.PixFmt, i.Interlaced)
	}
	i, _ = ParseProbe(strings.Replace(out, "yuv420p(progressive)", "yuv420p10le(tv, bt2020nc/bt2020/smpte2084)", 1))
	if i.PixFmt != "yuv420p10le" || i.Interlaced {
		t.Fatalf("10-bit: got %q %v", i.PixFmt, i.Interlaced)
	}
}

func TestGstArgsFit(t *testing.T) {
	a := strings.Join(GstArgs(Info{Width: 1920, Height: 1080, FPS: 60}, Screen{W: 1280, H: 800, PixFmt: "rgba"}), " ")
	for _, want := range []string{"format=RGBA,width=1280,height=720", "framerate=30/1", "v4l2slh264dec", "fdsink fd=3"} {
		if !strings.Contains(a, want) {
			t.Errorf("%q lacks %q", a, want)
		}
	}
	if f := strings.Join(HWDecodeArgs(Decoding{URL: "http://x/a", Info: Info{AudioIndex: 0}}), " "); !strings.Contains(f, "-c:v copy -an -sn -dn -f matroska pipe:3") {
		t.Errorf("ffmpeg args: %s", f)
	}
}

func TestReadFramePlace(t *testing.T) {
	const w, h = 6, 4
	pix := bytes.Repeat([]byte{9}, w*h*4)
	src := bytes.Repeat([]byte{1}, 2*2*4)
	if err := readFrame(bytes.NewReader(src), pix, w, &Fit{W: 2, H: 2, X: 2, Y: 1}); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			want := byte(9)
			if x >= 2 && x < 4 && y >= 1 && y < 3 {
				want = 1
			}
			if got := pix[(y*w+x)*4]; got != want {
				t.Fatalf("(%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
	full := make([]byte, w*h*4)
	if err := readFrame(bytes.NewReader(bytes.Repeat([]byte{7}, w*2*4)), full, w, &Fit{W: w, H: 2, Y: 1}); err != nil || full[w*4] != 7 || full[0] != 0 || full[3*w*4] != 0 {
		t.Fatalf("full-width place: %v %v", err, full)
	}
}
