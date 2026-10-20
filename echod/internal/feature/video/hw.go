//go:build !dot

package video

import (
	"context"
	"errors"
	"image"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The hardware decoder. The Show 8 2nd gen's MT8183 has an H.264 decoder and an MDP (a scaler and
// color converter) that a mainline kernel offers through V4L2: the stateless decoder and MDP3 drivers
// ("mtk-vcodec-dec", "mtk-mdp3:m2m"). An H.264 picture is decoded there
// rather than on the cores: ffmpeg still fetches the stream, takes it apart and makes the sound, but
// copies the picture's H.264 out untouched, in Matroska, to GStreamer, which has the decoder decode it
// and the MDP scale it to its place on the panel in the panel's pixels. The cores only move the
// frames. Measured on a Show 8 (1080p30 H.264 High): the decoding took 113% of a core in ffmpeg, and
// 34% this way, all of it moving frames.
//
// GStreamer comes with the image, cut down to the six plugins this uses (tools/linux/build-gst-arm64.sh),
// and runs as ffmpeg does: as the decoder's user, under its limits (wrap). It reads only what ffmpeg
// hands it. The decoder and the MDP are the video group's (boot.sh), which the decoder's user is in.

// gstRoot is the image's GStreamer, when the tests set none; else TECHO5_GST names it.
var gstRoot string

func gstDir() string {
	if gstRoot != "" {
		return gstRoot
	}
	if p := os.Getenv("TECHO5_GST"); p != "" {
		return p
	}
	return "/usr/local/lib/techo5-gst"
}

// v4l2Names is where the kernel names the video devices.
var v4l2Names = "/sys/class/video4linux"

// hwOnce finds whether the hardware is there, once: the devices do not come and go.
var hwOnce = sync.OnceValue(func() bool {
	if _, err := os.Stat(filepath.Join(gstDir(), "bin/gst-launch-1.0")); err != nil {
		return false
	}
	want := map[string]bool{"mtk-vcodec-dec": false, "mtk-mdp3:m2m": false}
	names, _ := filepath.Glob(filepath.Join(v4l2Names, "video*", "name"))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			continue
		}
		if _, ok := want[strings.TrimSpace(string(b))]; ok {
			want[strings.TrimSpace(string(b))] = true
		}
	}
	return want["mtk-vcodec-dec"] && want["mtk-mdp3:m2m"]
})

// hwLimits are the most the decoder takes: 1080p.
const hwMaxW, hwMaxH = 1920, 1088

// HWUsable is whether the picture can go through the hardware: H.264 in 8-bit 4:2:0, progressive, at
// most 1080p, not turned, onto a panel that is not turned either and takes RGBA (the GPU's textures)
// or BGRA. present says whether the hardware is there.
func HWUsable(i Info, s Screen, present bool) bool {
	if !present || i.VideoCodec != "h264" || i.Interlaced || s.Rotated {
		return false
	}
	switch i.PixFmt {
	case "yuv420p", "yuvj420p":
	default:
		return false
	}
	if i.Width <= 0 || i.Height <= 0 || i.Width > hwMaxW || i.Height > hwMaxH || i.Rotation%360 != 0 {
		return false
	}
	return hwPixFmt(s.PixFmt) != ""
}

// hwPixFmt is GStreamer's name for the panel's pixel layout, "" for one the MDP does not make.
func hwPixFmt(p string) string {
	switch p {
	case "rgba":
		return "RGBA"
	case "bgra", "":
		return "BGRA"
	}
	return ""
}

// HWDecodeArgs runs ffmpeg for the hardware: the picture's H.264 copied out in Matroska on file
// descriptor 3, for GStreamer, and the sound as DecodeArgs makes it.
func HWDecodeArgs(d Decoding) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, netArgs(d.URL, d.CAFile, d.Insecure, d.Proxy)...)
	a = append(a, "-i", d.URL)
	a = append(a, "-map", "0:v:"+strconv.Itoa(d.Info.VideoIndex), "-c:v", "copy", "-an", "-sn", "-dn",
		"-f", "matroska", "pipe:3")
	if d.Info.AudioIndex >= 0 {
		a = append(a, "-map", "0:a:"+strconv.Itoa(d.Info.AudioIndex), "-vn", "-sn", "-dn",
			"-af", soundFilter, "-ac", strconv.Itoa(soundChannels), "-ar", strconv.Itoa(soundRate),
			"-f", "s16le", "pipe:1")
	}
	return a
}

// GstArgs is GStreamer's pipeline: Matroska on its standard input; the picture decoded, scaled to fit
// (its place on the panel, which the session puts it at) in the panel's pixels, at an even rate as
// Filter makes it, on file descriptor 3. The MDP reads the decoder's pictures where they are
// (dmabuf-import); the queues let the decoder, the MDP and the pipe work at once.
func GstArgs(i Info, s Screen) []string {
	fit := FitIn(i, s)
	r := FrameRate(i.FPS)
	return []string{"-q",
		"fdsrc", "fd=0", "!", "matroskademux", "!", "h264parse", "!", "v4l2slh264dec", "!",
		"queue", "max-size-buffers=4", "!", "v4l2convert", "output-io-mode=dmabuf-import", "!",
		"video/x-raw,format=" + hwPixFmt(s.PixFmt) + ",width=" + strconv.Itoa(fit.W) + ",height=" + strconv.Itoa(fit.H), "!",
		"queue", "max-size-buffers=4", "!", "videorate", "!", "video/x-raw,framerate=" + r.String(), "!",
		"queue", "max-size-buffers=4", "!", "fdsink", "fd=3"}
}

// gstEnv is GStreamer's whole environment: its own directory and nothing of the daemon's. No registry
// is kept (the decoder's user may write no file): it loads its six plugins each time, in ~50 ms.
func gstEnv() []string {
	d := gstDir()
	return append(append([]string{}, decoderEnv...),
		"LD_LIBRARY_PATH="+filepath.Join(d, "lib"), "GST_PLUGIN_SYSTEM_PATH="+filepath.Join(d, "plugins"),
		"GST_PLUGIN_PATH=", "GST_REGISTRY=/nonexistent/registry.bin", "GST_REGISTRY_FORK=no")
}

// gstLimits are the decoder's limits (limits) for GStreamer, but for open files: each picture it hands
// between the decoder and the MDP is a file (a dmabuf), and 1080p holds some sixty of them at once.
var gstLimits = []string{"-v 655360", "-f 0", "-c 0", "-n 512"}

// startHW runs ffmpeg and GStreamer, joined by a pipe: the picture comes on the decoder's video (each
// frame fit-sized, not the panel's), the sound on its audio as start makes it.
func startHW(ctx context.Context, ffArgs, gstArgs []string, sound bool) (*decoder, error) {
	cred, err := decoderCred()
	if err != nil {
		return nil, err
	}
	d := &decoder{stderr: &tail{}, done: make(chan struct{})}
	mr, mw, err := os.Pipe() // ffmpeg's Matroska, to GStreamer
	if err != nil {
		return nil, err
	}
	growPipe(mr)
	vr, vw, err := os.Pipe() // GStreamer's frames, to the session
	if err != nil {
		mr.Close()
		mw.Close()
		return nil, err
	}
	growPipe(vr)
	d.video = vr
	closeAfter := []*os.File{mr, mw, vw}
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
	ff.ExtraFiles = []*os.File{mw}
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
	gst := command(filepath.Join(gstDir(), "bin/gst-launch-1.0"), gstArgs, gstEnv(), gstLimits)
	gst.Stdin = mr
	gst.Stdout = d.stderr // gst-launch says what it settled on (-v, sinkSize) on its standard output
	gst.ExtraFiles = []*os.File{vw}
	if err := gst.Start(); err != nil {
		closeAll()
		d.closeReads()
		return nil, err
	}
	if err := ff.Start(); err != nil {
		_ = syscall.Kill(-gst.Process.Pid, syscall.SIGKILL)
		_ = gst.Wait()
		closeAll()
		d.closeReads()
		return nil, err
	}
	closeAll() // the children's now
	d.cmd, d.helper = ff, gst
	go func() {
		d.err = ff.Wait()
		if err := gst.Wait(); d.err == nil {
			d.err = err
		}
		close(d.done)
	}()
	return d, nil
}

// A camera's stream (camera.go) goes through the hardware too where it is there. Its size is not known
// before it comes (there is no probe: the page would wait for it), so the MDP is asked for the largest
// picture that fits the page with its shape kept, and the size GStreamer settles on is read from what
// it says (-v); the picture goes in the middle of the page's frame, the rest black, as fitFilter pads it.

// cameraCopyOut is the camera's picture out of ffmpeg for GStreamer: untouched, in Matroska, on fd 3.
func cameraCopyOut() []string {
	return []string{"-map", "0:v:0", "-c:v", "copy", "-an", "-sn", "-dn", "-f", "matroska", "pipe:3"}
}

// CameraGstArgs is GStreamer's pipeline for a camera: its frames as they come, at most w×h, RGBA.
func CameraGstArgs(w, h int) []string {
	return []string{"-v",
		"fdsrc", "fd=0", "!", "matroskademux", "!", "h264parse", "!", "v4l2slh264dec", "!",
		"queue", "max-size-buffers=4", "!", "v4l2convert", "output-io-mode=dmabuf-import", "!",
		"video/x-raw,format=RGBA,width=[2," + strconv.Itoa(w) + "],height=[2," + strconv.Itoa(h) + "],pixel-aspect-ratio=1/1", "!",
		"queue", "max-size-buffers=2", "!", "fdsink", "fd=3"}
}

// reSinkCaps is GStreamer's word on the frames it writes (-v): their size.
var reSinkCaps = regexp.MustCompile(`fdsink0\.GstPad:sink: caps = video/x-raw.*?, width=\(int\)(\d+), height=\(int\)(\d+)`)

// sinkSize is the frames' size, once GStreamer has said it.
func sinkSize(stderr string) (w, h int, ok bool) {
	m := reSinkCaps.FindStringSubmatch(stderr)
	if m == nil {
		return 0, 0, false
	}
	w, _ = strconv.Atoi(m[1])
	h, _ = strconv.Atoi(m[2])
	return w, h, w > 0 && h > 0
}

// openCamera starts a camera's picture from in (its input options), w×h frames: through the
// hardware when it is there, else, or should the hardware take nothing of it, on the cores.
func openCamera(ctx context.Context, in []string, w, h int) (*Camera, error) {
	soft := func() (*decoder, error) {
		return start(ctx, append(append([]string{}, in...), cameraOut(w, h)...), true, false)
	}
	if hwOnce() {
		d, err := startHW(ctx, append(append([]string{}, in...), cameraCopyOut()...), CameraGstArgs(w, h), false)
		if err == nil {
			return &Camera{d: d, w: w, h: h, hw: true, soft: soft}, nil
		}
		slog.Warn("video: the hardware decoder did not start; the camera on the cores", "err", err)
	}
	d, err := soft()
	if err != nil {
		return nil, err
	}
	return &Camera{d: d, w: w, h: h}, nil
}

// read reads the next frame into img, waiting up to wait.
func (c *Camera) read(img *image.RGBA, wait time.Duration) error {
	until := time.Now().Add(wait)
	_ = c.d.video.SetReadDeadline(until)
	if !c.hw {
		_, err := io.ReadFull(c.d.video, img.Pix)
		return err
	}
	for c.place == nil {
		if pw, ph, ok := sinkSize(c.d.stderr.all()); ok {
			if pw > c.w || ph > c.h {
				return errors.New("the hardware made a picture larger than the page")
			}
			c.place = &Fit{W: pw, H: ph, X: even((c.w - pw) / 2), Y: even((c.h - ph) / 2)}
			for i := range img.Pix {
				img.Pix[i] = 0
			}
			for i := 3; i < len(img.Pix); i += 4 {
				img.Pix[i] = 255 // black bars, opaque, as fitFilter pads
			}
			break
		}
		select {
		case <-c.d.done:
			return errors.New("the hardware decoder stopped")
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(until) {
			return os.ErrDeadlineExceeded
		}
	}
	return readFrame(c.d.video, img.Pix, c.w, c.place)
}

// hwStopped is whether the hardware's run has ended: what it could not take (a camera that is not
// H.264) ends it at once. A slow stream is still running, and stays on the hardware.
func (c *Camera) hwStopped() bool {
	select {
	case <-c.d.done:
		return true
	default:
		return false
	}
}
