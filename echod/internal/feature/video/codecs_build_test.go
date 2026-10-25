package video

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// H.265, VP8 and VP9 are built into the aarch64 ffmpeg alone (athena's and the Skylight's): every other
// device keeps build-ffmpeg.sh's decoders, which its cores keep up with, and the aarch64 build adds them
// (build-ffmpeg-arm64.sh), with the parsers that go with them.
func TestExtraCodecsOnlyIn64BitFFmpeg(t *testing.T) {
	armv7, err := os.ReadFile("../../../../tools/linux/build-ffmpeg.sh")
	if err != nil {
		t.Fatal(err)
	}
	arm64, err := os.ReadFile("../../../../tools/linux/build-ffmpeg-arm64.sh")
	if err != nil {
		t.Fatal(err)
	}
	lists := func(script []byte, kind string) []string {
		var out []string
		for _, m := range regexp.MustCompile(`--enable-`+kind+`=([a-z0-9_,]+)`).FindAllStringSubmatch(string(script), -1) {
			out = append(out, strings.Split(m[1], ",")...)
		}
		return out
	}
	for _, c := range []string{"hevc", "vp8", "vp9"} {
		if slices.Contains(lists(armv7, "decoder"), c) || slices.Contains(lists(armv7, "parser"), c) {
			t.Errorf("build-ffmpeg.sh (armv7) builds in %s", c)
		}
		if !slices.Contains(lists(arm64, "decoder"), c) || !slices.Contains(lists(arm64, "parser"), c) {
			t.Errorf("build-ffmpeg-arm64.sh does not add %s", c)
		}
	}
	// Both under the LGPL only. The aarch64 build runs on a 32-bit userspace, so nothing of it may be
	// linked from there: it is static.
	for name, script := range map[string][]byte{"build-ffmpeg.sh": armv7, "build-ffmpeg-arm64.sh": arm64} {
		for _, f := range []string{"--enable-gpl", "--enable-nonfree", "--enable-version3"} {
			if strings.Contains(string(script), f) {
				t.Errorf("%s has %s", name, f)
			}
		}
	}
	if !strings.Contains(string(arm64), "--extra-ldflags=-static") || !strings.Contains(string(arm64), "--pkg-config-flags=--static") {
		t.Error("build-ffmpeg-arm64.sh does not link statically")
	}
}
