//go:build !dot

package video

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"regexp"
	"strconv"
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
}

// cameraArgs runs the decoder on a camera's stream at its own pace (-re: a playlist's parts arrive
// a few seconds at a time, and decoded as fast as they come they would play in a rush and then stop),
// from the newest part of the playlist, fitted into w×h with its shape kept (the bars black, for a
// stream whose shape the caller did not know) and made the page's pixels.
func cameraArgs(url string, w, h int, insecure bool, proxy string) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, netArgs(url, caFileIf(), insecure, proxy)...)
	a = append(a, "-threads", strconv.Itoa(threads(h)), "-re", "-live_start_index", "-1",
		"-fflags", "nobuffer", "-i", url,
		"-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", fitFilter(w, h), "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:3")
	return a
}

// fitFilter scales a picture into w×h with its shape kept, the rest black.
func fitFilter(w, h int) string {
	return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", w, h, w, h)
}

// OpenCamera starts decoding a camera's stream at url (Home Assistant's HLS playlist for it) into w×h
// frames. Close ends it.
func OpenCamera(ctx context.Context, url string, w, h int) (*Camera, error) {
	if w <= 0 || h <= 0 || w&1 != 0 || h&1 != 0 {
		return nil, fmt.Errorf("video: a camera picture of %dx%d", w, h)
	}
	if !Installed() {
		return nil, errors.New("video: this image has no decoder")
	}
	proxy, err := netGuard(ctx)
	if err != nil {
		return nil, err
	}
	d, err := start(ctx, cameraArgs(url, w, h, config.Get().Diag.InsecureTLS, proxy), true, false)
	if err != nil {
		return nil, err
	}
	return &Camera{d: d, w: w, h: h}, nil
}

// Next is the next frame, waiting up to wait for it. A stream that ended, or failed, says why.
func (c *Camera) Next(wait time.Duration) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
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
func (c *Camera) Close() { c.d.stop() }

// hlsToken is the part of a Home Assistant stream's address that opens it.
var hlsToken = regexp.MustCompile(`/api/hls/[^/\s]+/`)

// hideTokens is what ffmpeg said with any stream's token taken out: an HLS error can carry the
// playlist's address, and logs get pasted into issues.
func hideTokens(s string) string { return hlsToken.ReplaceAllString(s, "/api/hls/…/") }

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
	proxy, err := netGuard(ctx)
	if err != nil {
		return nil, err
	}
	d, err := start(ctx, cameraSoundArgs(url, config.Get().Diag.InsecureTLS, proxy), true, false)
	if err != nil {
		return nil, err
	}
	return cameraSound{d}, nil
}

// cameraSound is a camera's sound as it is decoded.
type cameraSound struct{ d *decoder }

func (c cameraSound) Read(p []byte) (int, error) { return c.d.video.Read(p) }
func (c cameraSound) Close() error               { c.d.stop(); return nil }

// SetReadDeadline bounds a read, for the first of the sound: a stream that brings none is given up on.
func (c cameraSound) SetReadDeadline(t time.Time) error { return c.d.video.SetReadDeadline(t) }

// Reason is what ffmpeg said last, without a stream's token: why a run that ended brought no sound.
func (c cameraSound) Reason() string { return hideTokens(c.d.stderr.last()) }

// CameraLiveChanged is the live camera switch having been turned: once neither videos nor live cameras
// may decode, nothing is left listening for the decoder (closeGuard).
func CameraLiveChanged() {
	if !guardAllowed() {
		closeGuard()
	}
}
