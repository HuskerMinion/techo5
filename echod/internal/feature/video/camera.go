//go:build !dot

package video

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Camera is a camera's live stream decoded for the camera page: the picture only, scaled to the size
// the page shows it at, as RGBA frames. It is the same decoder as a video's, run the same way (proc.go:
// its own user, bounded, killed with the page), but with every core it has: a camera's picture is all
// the page does, it has no sound for it to keep pace with, and its frames arrive as they are made, so
// one that falls behind only shows later. Snapshots, which the page fell back on before, are decoded in
// the daemon one after another on one core; a stream of the same camera comes out smoother for less.
type Camera struct {
	d    *decoder
	w, h int
}

// cameraThreads is every core the Show has.
const cameraThreads = 4

// cameraArgs runs the decoder on a camera's stream at its own pace (-re: a playlist's parts arrive
// a few seconds at a time, and decoded as fast as they come they would play in a rush and then stop),
// from the newest part of the playlist, scaled to w×h and made the page's pixels.
func cameraArgs(url string, w, h int, insecure bool) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, netArgs(url, caFileIf(), insecure, "")...)
	a = append(a, "-threads", strconv.Itoa(cameraThreads), "-re", "-live_start_index", "-1",
		"-fflags", "nobuffer", "-i", url,
		"-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=fast_bilinear", w, h), "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:3")
	return a
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
	d, err := start(ctx, cameraArgs(url, w, h, config.Get().Diag.InsecureTLS), true, false)
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
				return nil, errors.New("video: camera stream: " + why)
			}
		default:
		}
		return nil, fmt.Errorf("video: camera stream: %w", err)
	}
	return img, nil
}

// Close ends the decoding and waits for it.
func (c *Camera) Close() { c.d.stop() }
