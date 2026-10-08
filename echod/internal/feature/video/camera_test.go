//go:build !dot

package video

import (
	"slices"
	"strings"
	"testing"
)

// A camera's stream is decoded on every core, at its own pace from the newest part of the playlist,
// scaled to the page and made its pixels, with the network as the only thing the decoder may open.
func TestTheCameraArguments(t *testing.T) {
	url := "http://192.0.2.10:8123/api/hls/0123abcd/master_playlist.m3u8"
	a := cameraArgs(url, 640, 480, false)
	has := func(seq ...string) bool {
		for i := 0; i+len(seq) <= len(a); i++ {
			if slices.Equal(a[i:i+len(seq)], seq) {
				return true
			}
		}
		return false
	}
	for _, seq := range [][]string{
		{"-protocol_whitelist", "http,https,tcp,tls,hls,crypto"},
		{"-max_pixels", "2088960"},
		{"-threads", "4"},
		{"-re"},
		{"-live_start_index", "-1"},
		{"-i", url},
		{"-map", "0:v:0", "-an"},
		{"-vf", "scale=640:480:flags=fast_bilinear"},
		{"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:3"},
	} {
		if !has(seq...) {
			t.Errorf("no %q in %q", seq, a)
		}
	}
	// The input's options come before it, or they are taken as the output's.
	in := slices.Index(a, "-i")
	for _, opt := range []string{"-protocol_whitelist", "-threads", "-re", "-live_start_index"} {
		if slices.Index(a, opt) > in {
			t.Errorf("%s is after the input", opt)
		}
	}
	for _, s := range a {
		if strings.Contains(s, "file") || s == "pipe:1" {
			t.Errorf("%q", s)
		}
	}
	// Plain http: no certificate options, which ffmpeg refuses for an input that opens no TLS.
	if has("-tls_verify", "1") {
		t.Error("certificate options for plain http")
	}
	if a := cameraArgs("https://ha.example.com/api/hls/x/master_playlist.m3u8", 640, 480, false); !slices.Contains(a, "-tls_verify") {
		t.Errorf("no certificate check for https: %q", a)
	}
}
