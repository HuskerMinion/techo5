//go:build !dot

package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Camera is a camera's live stream decoded for the camera page: the picture only, scaled to the size
// the page shows it at, as RGBA frames. It is the same decoder as a video's, run the same way (proc.go:
// its own user, bounded, killed with the page; netGuard: kept off the device's own network), with as
// many cores as the picture's size wants (threads). Its sound, when the view has one, is a run of its
// own (OpenCameraSound), so the picture has nothing to keep pace with: its frames are shown as they
// come. Snapshots, which the page shows otherwise, are decoded in the daemon one after another on one
// core; a stream of the same camera comes out smoother for less.
type Camera struct {
	d    *decoder
	w, h int
	// frame is what Next reads each frame into: one picture for the whole stream, rather than one more
	// for the collector each frame (at 24 frames of 896×672 a second, over fifty megabytes a second).
	frame *image.RGBA
	// release lets go of its hold on the decoder's proxy (holdGuard); nil for one read over RTSP.
	release func()
}

// cameraArgs runs the decoder on a camera's stream at its own pace (-re: a playlist's parts arrive
// a few seconds at a time, and decoded as fast as they come they would play in a rush and then stop),
// from the newest part of the playlist, fitted into w×h with its shape kept (the bars black, for a
// stream whose shape the caller did not know) and made the page's pixels. The playlist's address
// carries the stream's token: it is an argument to the decoder, so the image mounts /proc so that only
// root sees another user's arguments (boot.sh), and it is never logged (hideTokens).
func cameraArgs(url string, w, h int, insecure bool, proxy string, filter string) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, netArgs(url, caFileIf(), insecure, proxy)...)
	a = append(a, "-threads", strconv.Itoa(threads(h)), "-re", "-live_start_index", "-1",
		"-fflags", "nobuffer", "-i", url,
		"-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", filter, "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:3")
	return a
}

// fitFilter scales a picture into w×h with its shape kept, the rest black.
func fitFilter(w, h int) string {
	return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", w, h, w, h)
}

// tileFPS is the most frames a second a picture filling a tile is decoded at: a tile is one among many
// on a page that is drawn whole for each.
const tileFPS = 12

// coverFilter scales a picture to cover w×h with its shape kept, cut to it about its middle.
func coverFilter(w, h int) string {
	return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=fast_bilinear,crop=%d:%d", w, h, w, h)
}

// fillFilter is coverFilter at no more than tileFPS.
func fillFilter(w, h int) string {
	return fmt.Sprintf("fps=%d,%s", tileFPS, coverFilter(w, h))
}

// tileFitFilter is fitFilter at no more than tileFPS: a tile showing the whole picture.
func tileFitFilter(w, h int) string {
	return fmt.Sprintf("fps=%d,%s", tileFPS, fitFilter(w, h))
}

// OpenCamera starts decoding a camera's stream at url (Home Assistant's HLS playlist for it) into w×h
// frames. Close ends it.
func OpenCamera(ctx context.Context, url string, w, h int) (*Camera, error) {
	return openCamera(ctx, url, w, h, fitFilter(w, h))
}

// OpenCameraCovering is OpenCamera with the picture covering w×h, cut to its shape rather than barred:
// the camera view with Camera picture set to fill the screen.
func OpenCameraCovering(ctx context.Context, url string, w, h int) (*Camera, error) {
	return openCamera(ctx, url, w, h, coverFilter(w, h))
}

func openCamera(ctx context.Context, url string, w, h int, filter string) (*Camera, error) {
	if w <= 0 || h <= 0 || w&1 != 0 || h&1 != 0 {
		return nil, fmt.Errorf("video: a camera picture of %dx%d", w, h)
	}
	if !Installed() {
		return nil, errors.New("video: this image has no decoder")
	}
	release := holdGuard()
	proxy, err := netGuard(ctx)
	if err != nil {
		release()
		return nil, err
	}
	d, err := start(ctx, cameraArgs(url, w, h, config.Get().Diag.InsecureTLS, proxy, filter), true, false)
	if err != nil {
		release()
		return nil, err
	}
	releaseWhenDone(d, release)
	return &Camera{d: d, w: w, h: h, release: release}, nil
}

// Next is the next frame, waiting up to wait for it. A stream that ended, or failed, says why. The
// frame is the Camera's own and is read over by the next call: a caller that keeps it copies it.
func (c *Camera) Next(wait time.Duration) (*image.RGBA, error) {
	if c.frame == nil {
		c.frame = image.NewRGBA(image.Rect(0, 0, c.w, c.h))
	}
	img := c.frame
	_ = c.d.video.SetReadDeadline(time.Now().Add(wait))
	if _, err := io.ReadFull(c.d.video, img.Pix); err != nil {
		select {
		case <-c.d.done:
			if why := c.d.stderr.last(); why != "" {
				return nil, errors.New("video: camera stream: " + hideTokens(why))
			}
		default:
		}
		return nil, fmt.Errorf("video: camera stream: %w", err)
	}
	return img, nil
}

// Close ends the decoding and waits for it.
func (c *Camera) Close() {
	c.d.stop()
	if c.release != nil {
		c.release() // now, not when the watcher of its end gets to it
	}
}

// hlsToken is the part of a Home Assistant stream's address that opens it, and urlLogin the login in
// an address (a camera's RTSP one carries it: rtspURL).
var (
	hlsToken = regexp.MustCompile(`/api/hls/[^/\s]+/`)
	urlLogin = regexp.MustCompile(`://[^/\s@]*@`)
)

// hideTokens is what ffmpeg said with any stream's token and any address's login taken out: an error
// can carry the address it was opening ("Error opening input file rtsp://user:pass@…"), and logs get
// pasted into issues.
func hideTokens(s string) string {
	return urlLogin.ReplaceAllString(hlsToken.ReplaceAllString(s, "/api/hls/…/"), "://…@")
}

// cameraSoundArgs runs the decoder on a camera's stream for its sound alone: at its own pace from the
// newest part of the playlist, as the picture is, so the two are about as far behind the camera, and as
// the speaker's samples (soundRate, soundChannels, 16-bit) with no header. The playlist's parts carry
// the picture as well, which is fetched with them and dropped.
func cameraSoundArgs(url string, insecure bool, proxy string) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	a = append(a, netArgs(url, caFileIf(), insecure, proxy)...)
	a = append(a, "-re", "-live_start_index", "-1", "-fflags", "nobuffer", "-i", url,
		"-map", "0:a:0", "-vn", "-sn", "-dn",
		"-af", "aresample="+strconv.Itoa(soundRate)+":async=1",
		"-ac", strconv.Itoa(soundChannels), "-ar", strconv.Itoa(soundRate),
		"-f", "s16le", "pipe:3")
	return a
}

// OpenCameraSound starts decoding a camera's sound from its stream at url (Home Assistant's HLS
// playlist for it), for the speaker; ctx ending, or Close, ends it.
func OpenCameraSound(ctx context.Context, url string) (io.ReadCloser, error) {
	if !Installed() {
		return nil, errors.New("video: this image has no decoder")
	}
	release := holdGuard()
	proxy, err := netGuard(ctx)
	if err != nil {
		release()
		return nil, err
	}
	d, err := start(ctx, cameraSoundArgs(url, config.Get().Diag.InsecureTLS, proxy), true, false)
	if err != nil {
		release()
		return nil, err
	}
	releaseWhenDone(d, release)
	return cameraSound{d, release}, nil
}

// cameraSound is a camera's sound as it is decoded.
type cameraSound struct {
	d       *decoder
	release func() // as Camera's
}

func (c cameraSound) Read(p []byte) (int, error) { return c.d.video.Read(p) }
func (c cameraSound) Close() error {
	c.d.stop()
	if c.release != nil {
		c.release()
	}
	return nil
}

// SetReadDeadline bounds a read, for the first of the sound: a stream that brings none is given up on.
func (c cameraSound) SetReadDeadline(t time.Time) error { return c.d.video.SetReadDeadline(t) }

// Reason is what ffmpeg said last, without a stream's token: why a run that ended brought no sound.
func (c cameraSound) Reason() string { return hideTokens(c.d.stderr.last()) }

// CameraLiveChanged is the live camera switch having been turned: once neither videos nor live cameras
// may decode, nothing is left listening for the decoder (closeGuard), as soon as no camera's stream that
// was already open is using it (closeGuardIdle).
func CameraLiveChanged() { closeGuardIdle() }

// CanRTSP is whether this image's decoder reads RTSP, and may be given a camera's login to do it. An
// image built before it did has no RTSP demuxer, and a camera asked of it over RTSP would be nothing at
// all. The login is sent only protected (Digest), never as Basic, which is the password in the clear
// (the setup page promises it, and the talk-back session holds to it too: lib/onvifback). ffmpeg would
// answer a camera that offers only Basic with it, so the image's decoder is built to ignore a Basic
// challenge (build-ffmpeg.sh), and one that is not built that way is not used for RTSP at all.
//
// Read once from the decoder itself, a piece at a time (it is megabytes): the RTSP demuxer's own
// option name is in it only when the demuxer is, and the message the Basic change logs only with it.
// Never where the kernel cannot fence the decoder (rtspFenced: the Spot), since RTSP would not work there.
func CanRTSP() bool {
	if !rtspFenced {
		return false
	}
	rtspOnce.Do(func() { rtspOK = readsRTSP(decoderPath()) })
	return rtspOK
}

// readsRTSP is whether the decoder at path has the RTSP demuxer and ignores a Basic challenge.
func readsRTSP(path string) bool {
	ok, err := fileHas(path, rtspDemuxer, digestOnly)
	return err == nil && ok
}

// What CanRTSP looks for in the decoder.
var (
	rtspDemuxer = []byte("rtsp_transport")
	digestOnly  = []byte("techo5: the camera offers a Basic login, which is ignored")
)

var (
	rtspOnce sync.Once
	rtspOK   bool
)

// fileChunk is how much of a file fileHas reads at a time; a variable for the tests.
var fileChunk = 64 << 10

// fileHas is whether the file at path holds every one of needles, read through once in pieces with
// enough of each piece carried into the next that a needle across the seam is found.
func fileHas(path string, needles ...[]byte) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	carry := 0
	for _, n := range needles {
		carry = max(carry, len(n)-1)
	}
	found := make([]bool, len(needles))
	left := len(needles)
	buf := make([]byte, carry+fileChunk)
	kept := 0
	for left > 0 {
		n, err := io.ReadFull(f, buf[kept:])
		seen := buf[:kept+n]
		for i, needle := range needles {
			if !found[i] && bytes.Contains(seen, needle) {
				found[i] = true
				left--
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return false, err
		}
		kept = min(carry, len(seen))
		copy(buf, seen[len(seen)-kept:])
	}
	return left == 0, nil
}

// rtspFence is whether a decoder reading RTSP can be kept off the device's own network. RTSP does not
// go through the guard's HTTP proxy, so only the kernel's fence (fence.go) will do; where it cannot
// hold (the Spot), a camera is read through Home Assistant's HLS instead.
func rtspFence(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !guardAllowed() {
		return ErrOff
	}
	cred, err := decoderCred()
	if err != nil {
		return err
	}
	if cred == nil {
		return nil // the tests' stand-in
	}
	return fence(cred.Uid)
}

// rtspURL is a camera's RTSP address with its login: the address's own when it has one, else user and
// pass, as config.TalkBack says.
func rtspURL(addr, user, pass string) (string, error) {
	u, err := url.Parse(addr)
	if err != nil || u.Scheme != "rtsp" || u.Host == "" {
		return "", errors.New("video: not an rtsp:// address")
	}
	if u.User == nil && user != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u.String(), nil
}

// rtspInput is how a camera is read over RTSP: one track of it, over the RTSP connection itself,
// nothing held back to look at first. A live camera sets its own pace, so there is no -re. The address
// carries the login: it is an argument to the decoder, which runs as its own user, so the image mounts
// /proc so that only root sees another user's arguments (boot.sh); it is never logged (hideTokens),
// and the decoder answers only a Digest challenge with it (CanRTSP).
func rtspInput(track string) []string {
	return []string{"-protocol_whitelist", "rtsp,rtp,tcp,udp",
		"-rtsp_transport", "tcp", "-allowed_media_types", track, "-timeout", "15000000",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "500000", "-analyzeduration", "0"}
}

// cameraRTSPArgs reads a camera's picture straight from its RTSP address: the video track alone, put into
// w×h by filter (fitFilter, fillFilter) and made the page's pixels.
func cameraRTSPArgs(addr string, w, h int, filter string) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, rtspInput("video")...)
	a = append(a, "-threads", strconv.Itoa(threads(h)), "-i", addr,
		"-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", filter, "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:3")
	return a
}

// cameraSoundRTSPArgs reads a camera's sound straight from its RTSP address: the audio track alone, the
// camera not asked for its picture at all, as the speaker's samples.
func cameraSoundRTSPArgs(addr string) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	a = append(a, rtspInput("audio")...)
	a = append(a, "-i", addr,
		"-map", "0:a:0", "-vn", "-sn", "-dn",
		"-af", "aresample="+strconv.Itoa(soundRate)+":async=1",
		"-ac", strconv.Itoa(soundChannels), "-ar", strconv.Itoa(soundRate),
		"-f", "s16le", "pipe:3")
	return a
}

// OpenCameraRTSP starts decoding a camera's picture from its RTSP address into w×h frames, where the
// decoder reads RTSP and the fence holds. Close ends it.
func OpenCameraRTSP(ctx context.Context, addr, user, pass string, w, h int) (*Camera, error) {
	return openCameraRTSP(ctx, addr, user, pass, w, h, fitFilter(w, h))
}

// OpenCameraRTSPCovering is OpenCameraRTSP with the picture covering w×h, cut to its shape rather than
// barred: the camera view with Camera picture set to fill the screen.
func OpenCameraRTSPCovering(ctx context.Context, addr, user, pass string, w, h int) (*Camera, error) {
	return openCameraRTSP(ctx, addr, user, pass, w, h, coverFilter(w, h))
}

// OpenCameraRTSPFilling is OpenCameraRTSP with the picture filling w×h, cut to its shape rather than
// barred, as a tile shows it.
func OpenCameraRTSPFilling(ctx context.Context, addr, user, pass string, w, h int) (*Camera, error) {
	return openCameraRTSP(ctx, addr, user, pass, w, h, fillFilter(w, h))
}

// OpenCameraRTSPFitting is OpenCameraRTSP for a tile showing the whole picture, with bars: at no more
// than tileFPS, as one filling its tile.
func OpenCameraRTSPFitting(ctx context.Context, addr, user, pass string, w, h int) (*Camera, error) {
	return openCameraRTSP(ctx, addr, user, pass, w, h, tileFitFilter(w, h))
}

func openCameraRTSP(ctx context.Context, addr, user, pass string, w, h int, filter string) (*Camera, error) {
	if w <= 0 || h <= 0 || w&1 != 0 || h&1 != 0 {
		return nil, fmt.Errorf("video: a camera picture of %dx%d", w, h)
	}
	if !Installed() || !CanRTSP() {
		return nil, errors.New("video: this image's decoder does not read RTSP")
	}
	if err := rtspFence(ctx); err != nil {
		return nil, err
	}
	u, err := rtspURL(addr, user, pass)
	if err != nil {
		return nil, err
	}
	d, err := start(ctx, cameraRTSPArgs(u, w, h, filter), true, false)
	if err != nil {
		return nil, err
	}
	return &Camera{d: d, w: w, h: h}, nil
}

// OpenCameraSoundRTSP starts decoding a camera's sound from its RTSP address, for the speaker, where the
// decoder reads RTSP and the fence holds; ctx ending, or Close, ends it.
func OpenCameraSoundRTSP(ctx context.Context, addr, user, pass string) (io.ReadCloser, error) {
	if !Installed() || !CanRTSP() {
		return nil, errors.New("video: this image's decoder does not read RTSP")
	}
	if err := rtspFence(ctx); err != nil {
		return nil, err
	}
	u, err := rtspURL(addr, user, pass)
	if err != nil {
		return nil, err
	}
	d, err := start(ctx, cameraSoundRTSPArgs(u), true, false)
	if err != nil {
		return nil, err
	}
	return cameraSound{d: d}, nil
}
