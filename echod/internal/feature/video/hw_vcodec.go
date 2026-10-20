//go:build !dot

package video

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The 2nd gen Show 5's hardware decoder. The MT8163's H.264 and H.265 decoders are driven by MediaTek's own
// libraries, from the AP (custom-kernel/cronos: patches/mt8163-vcodec.c, /dev/Vcodec; vcodec-user):
// 32-bit Android libraries, which vcodec-dec runs through the Android runtime the image carries in
// /usr/local/lib/techo5-vcodec, with the unit's own copies from /vendor/lib (they are not the
// image's to carry). ffmpeg still fetches the stream, takes it apart and makes the sound, but copies
// the picture's H.264 or H.265 out untouched (Annex-B) to vcodec-dec, which hands back each picture as NV12 of
// the video's own size; the GPU converts, scales and turns it onto the panel (PresentYUV). On the
// cores, the conversion to the panel cost several times the decoding (720p: 21 ms a frame to
// decode, 60 to scale, pad, turn and convert), so this path is only taken where the GPU draws the
// screen.

// vcodecRoot is the image's Android runtime and vcodec-dec, when the tests set none; else
// TECHO5_VCODEC names it.
var vcodecRoot string

func vcodecDir() string {
	if vcodecRoot != "" {
		return vcodecRoot
	}
	if p := os.Getenv("TECHO5_VCODEC"); p != "" {
		return p
	}
	return "/usr/local/lib/techo5-vcodec"
}

// vendorLib is where the unit keeps MediaTek's libraries (Fire OS's vendor partition).
var vendorLib = "/vendor/lib"

// vcodecOnce finds whether the decoder is there, once: the driver, the runtime and the unit's
// libraries.
var vcodecOnce = sync.OnceValue(func() bool {
	for _, p := range []string{"/dev/Vcodec", filepath.Join(vcodecDir(), "system/bin/linker"),
		filepath.Join(vcodecDir(), "system/bin/vcodec_dec"), filepath.Join(vendorLib, "libvcodecdrv.so")} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
})

// vcodecCodecs is what the image's vcodec-dec decodes, as ffmpeg names the codecs: its etc/codecs, a
// word a codec ("h264 hevc"). A bundle without the file is one from before H.265: H.264 only.
var vcodecCodecs = sync.OnceValue(func() map[string]bool {
	return readCodecs(filepath.Join(vcodecDir(), "etc/codecs"))
})

func readCodecs(path string) map[string]bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{"h264": true}
	}
	m := map[string]bool{}
	for _, w := range strings.Fields(string(b)) {
		m[w] = true
	}
	return m
}

// vcodecHere is what the Show 5's decoder takes on this unit: nothing when it is not there.
func vcodecHere() map[string]bool {
	if !vcodecOnce() {
		return nil
	}
	return vcodecCodecs()
}

// VcodecUsable is whether the picture can go through the Show 5's decoder: H.264 (or H.265, Main,
// where the bundle's vcodec-dec has it) in 8-bit 4:2:0, progressive, at most 1080p, not turned, at a
// known rate (the frames come as they are, not evened out as ffmpeg's fps filter would), onto a
// screen the GPU draws. codecs is what the decoder takes here (vcodecHere), nil when it is not there.
func VcodecUsable(i Info, s Screen, codecs map[string]bool) bool {
	if !codecs[i.VideoCodec] || !s.GPU || i.Interlaced || i.FPS <= 0 {
		return false
	}
	switch i.VideoCodec {
	case "h264", "hevc":
	default:
		return false
	}
	switch i.PixFmt {
	case "yuv420p", "yuvj420p":
	default:
		return false
	}
	return i.Width > 0 && i.Height > 0 && i.Width%2 == 0 && i.Height%2 == 0 &&
		i.Width <= hwMaxW && i.Height <= hwMaxH && i.Rotation%360 == 0
}

// VcodecFrames is the layout of the decoder's frames for the video: its own size, in its place on
// the canvas; full range for JPEG's yuvj420p; BT.709 from 720 lines up, as HD streams are made.
func VcodecFrames(i Info, s Screen) *YUVFrames {
	return &YUVFrames{W: i.Width, H: i.Height, Place: FitIn(i, s), FullRange: i.PixFmt == "yuvj420p",
		BT709: i.Height >= 720}
}

// VcodecDecodeArgs runs ffmpeg for the Show 5's decoder: the picture's H.264 or H.265 copied out as
// Annex-B on file descriptor 3, and the sound as DecodeArgs makes it.
func VcodecDecodeArgs(d Decoding) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, netArgs(d.URL, d.CAFile, d.Insecure, d.Proxy)...)
	a = append(a, "-i", d.URL)
	bsf, format := "h264_mp4toannexb", "h264"
	if d.Info.VideoCodec == "hevc" {
		bsf, format = "hevc_mp4toannexb", "hevc"
	}
	a = append(a, "-map", "0:v:"+strconv.Itoa(d.Info.VideoIndex), "-c:v", "copy", "-bsf:v", bsf,
		"-an", "-sn", "-dn", "-f", format, "pipe:3")
	if d.Info.AudioIndex >= 0 {
		a = append(a, "-map", "0:a:"+strconv.Itoa(d.Info.AudioIndex), "-vn", "-sn", "-dn",
			"-af", soundFilter, "-ac", strconv.Itoa(soundChannels), "-ar", strconv.Itoa(soundRate),
			"-f", "s16le", "pipe:1")
	}
	return a
}

// vcodecEnv is vcodec-dec's whole environment: the Android runtime's libraries, then the stand-ins
// for Android's memory and IOMMU libraries, then the unit's own MediaTek libraries.
func vcodecEnv() []string {
	d := vcodecDir()
	return []string{"LD_LIBRARY_PATH=" + filepath.Join(d, "system/lib") + ":" + filepath.Join(d, "shim") + ":" + vendorLib,
		"VCODEC_PROP_FILE=" + filepath.Join(d, "etc/vcodec.prop")}
}

// vcodecLimits are vcodec-dec's: the decoder's, with room for its buffers' mappings.
var vcodecLimits = []string{"-n 256", "-v unlimited"}

// vcodecArgs are vcodec-dec's arguments: the frames' size, and the codec when it is H.265 (H.264 is
// the default, and all a bundle from before H.265 knows).
func vcodecArgs(codec string, y *YUVFrames) []string {
	a := []string{"-s", strconv.Itoa(y.W) + "x" + strconv.Itoa(y.H)}
	if codec == "hevc" {
		a = append([]string{"-c", "hevc"}, a...)
	}
	return a
}

// startVcodec runs ffmpeg and vcodec-dec, joined by a pipe: the picture (codec, as ffmpeg names it)
// comes on the decoder's video as NV12 frames of y's size, the sound (when sound) on its audio, as
// start makes them.
func startVcodec(ctx context.Context, ffArgs []string, codec string, y *YUVFrames, sound bool) (*decoder, error) {
	cred, err := decoderCred()
	if err != nil {
		return nil, err
	}
	d := &decoder{stderr: &tail{}, done: make(chan struct{})}
	hr, hw, err := os.Pipe() // ffmpeg's H.264, to vcodec-dec
	if err != nil {
		return nil, err
	}
	growPipe(hr)
	vr, vw, err := os.Pipe() // vcodec-dec's frames, to the session
	if err != nil {
		hr.Close()
		hw.Close()
		return nil, err
	}
	growPipe(vr)
	d.video = vr
	closeAfter := []*os.File{hr, hw, vw}
	closeAll := func() {
		for _, f := range closeAfter {
			f.Close()
		}
	}
	command := func(path string, args, env, lim []string) *exec.Cmd {
		name, argv := wrapWith(path, args, lim)
		cmd := exec.CommandContext(ctx, name, argv...)
		cmd.Dir = "/"
		cmd.Env = env
		cmd.Stderr = d.stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred, Setpgid: true, Pdeathsig: syscall.SIGKILL}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 2 * time.Second
		return cmd
	}
	ff := command(decoderPath(), ffArgs, decoderEnv, limits)
	ff.ExtraFiles = []*os.File{hw}
	if sound {
		ar, aw, err := os.Pipe()
		if err != nil {
			closeAll()
			vr.Close()
			return nil, err
		}
		growPipe(ar)
		d.audio = ar
		ff.Stdout = aw
		closeAfter = append(closeAfter, aw)
	}
	dir := vcodecDir()
	vd := command(filepath.Join(dir, "system/bin/linker"),
		append([]string{filepath.Join(dir, "system/bin/vcodec_dec")}, vcodecArgs(codec, y)...),
		vcodecEnv(), vcodecLimits)
	vd.Stdin = hr
	vd.Stdout = vw
	if err := vd.Start(); err != nil {
		closeAll()
		d.closeReads()
		return nil, err
	}
	if err := ff.Start(); err != nil {
		_ = syscall.Kill(-vd.Process.Pid, syscall.SIGKILL)
		_ = vd.Wait()
		closeAll()
		d.closeReads()
		return nil, err
	}
	closeAll() // the children's now
	d.cmd, d.helper = ff, vd
	go func() {
		d.err = ff.Wait()
		if err := vd.Wait(); d.err == nil {
			d.err = err
		}
		close(d.done)
	}()
	return d, nil
}
