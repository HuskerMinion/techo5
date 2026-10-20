//go:build !dot

package video

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Show 5's decoder takes H.264, and H.265 only where its bundle says it has it.
func TestVcodecUsable(t *testing.T) {
	scr := Screen{W: 960, H: 480, Rotated: true, PixFmt: "bgra", GPU: true}
	both := map[string]bool{"h264": true, "hevc": true}
	h264 := map[string]bool{"h264": true}
	avc := Info{VideoCodec: "h264", PixFmt: "yuv420p", Width: 1920, Height: 1080, FPS: 30}
	hevc := Info{VideoCodec: "hevc", PixFmt: "yuv420p", Width: 1280, Height: 720, FPS: 25}
	for name, c := range map[string]struct {
		i      Info
		codecs map[string]bool
		s      Screen
		want   bool
	}{
		"h264":                {avc, both, scr, true},
		"h264, older bundle":  {avc, h264, scr, true},
		"hevc":                {hevc, both, scr, true},
		"hevc, older bundle":  {hevc, h264, scr, false},
		"absent":              {avc, nil, scr, false},
		"hevc 10-bit":         {Info{VideoCodec: "hevc", PixFmt: "yuv420p10le", Width: 1280, Height: 720, FPS: 25}, both, scr, false},
		"hevc 4K":             {Info{VideoCodec: "hevc", PixFmt: "yuv420p", Width: 3840, Height: 2160, FPS: 25}, both, scr, false},
		"hevc interlaced":     {Info{VideoCodec: "hevc", PixFmt: "yuv420p", Width: 1280, Height: 720, FPS: 25, Interlaced: true}, both, scr, false},
		"hevc, no GPU":        {hevc, both, Screen{W: 960, H: 480, Rotated: true, PixFmt: "bgra"}, false},
		"mpeg4, listed":       {Info{VideoCodec: "mpeg4", PixFmt: "yuv420p", Width: 640, Height: 480, FPS: 25}, map[string]bool{"mpeg4": true}, scr, false},
		"hevc, no frame rate": {Info{VideoCodec: "hevc", PixFmt: "yuv420p", Width: 1280, Height: 720}, both, scr, false},
	} {
		if got := VcodecUsable(c.i, c.s, c.codecs); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// ffmpeg copies the picture out as Annex-B in its own codec, and vcodec-dec is told when it is H.265
// (an H.264 run is as it was, which an older bundle's vcodec-dec still understands).
func TestVcodecArgs(t *testing.T) {
	for codec, want := range map[string][2]string{"h264": {"h264_mp4toannexb", "h264"}, "hevc": {"hevc_mp4toannexb", "hevc"}} {
		a := VcodecDecodeArgs(Decoding{URL: "https://media.example.com/a.mp4",
			Info: Info{VideoCodec: codec, VideoIndex: 0, AudioIndex: -1}})
		s := strings.Join(a, " ")
		if !strings.Contains(s, "-c:v copy -bsf:v "+want[0]+" -an -sn -dn -f "+want[1]+" pipe:3") {
			t.Errorf("%s: %s", codec, s)
		}
	}
	y := &YUVFrames{W: 1280, H: 720}
	if a := vcodecArgs("h264", y); !slices.Equal(a, []string{"-s", "1280x720"}) {
		t.Errorf("h264: %q", a)
	}
	if a := vcodecArgs("hevc", y); !slices.Equal(a, []string{"-c", "hevc", "-s", "1280x720"}) {
		t.Errorf("hevc: %q", a)
	}
}

// The bundle's etc/codecs says what its vcodec-dec decodes; one without it is H.264 only.
func TestReadCodecs(t *testing.T) {
	d := t.TempDir()
	if m := readCodecs(filepath.Join(d, "none")); !m["h264"] || m["hevc"] {
		t.Errorf("no file: %v", m)
	}
	p := filepath.Join(d, "codecs")
	if err := os.WriteFile(p, []byte("h264 hevc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := readCodecs(p); !m["h264"] || !m["hevc"] || len(m) != 2 {
		t.Errorf("h264 hevc: %v", m)
	}
}
