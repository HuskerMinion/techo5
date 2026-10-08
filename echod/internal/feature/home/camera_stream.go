package home

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A camera's live stream on the camera page, instead of its snapshots. Home Assistant hands out an HLS
// playlist for any camera it can stream (camera/stream), and the video decoder plays it on every core,
// scaled to the page (feature/video, camera.go). On a Show 5, a 640×480 stream at 10 frames a second
// takes a fifth of the CPU and is on screen in under a second once Home Assistant has the stream
// running; its main stream at full size takes two fifths. Snapshots came a few a second at best, each
// decoded and scaled in the daemon on one core.
//
// A snapshot still comes first: it is on screen at once, while the stream starts (a few seconds for a
// camera Home Assistant was not streaming yet), and its shape is the stream's, which the decoder has to
// be told before it makes a frame. A camera that cannot stream, a decoder that is missing, and a stream
// that fails or stalls all leave the page on snapshots, as it was.

const (
	// streamStart is how long the first frame of a stream may take: Home Assistant starts a camera's
	// stream on the first request and hands out the playlist before it has its first part.
	streamStart = 20 * time.Second
	// streamStall is how long a stream may go without a frame once it has started before it is given
	// up for snapshots.
	streamStall = 10 * time.Second
)

// cameraStreams is whether Home Assistant is asked for a camera's stream at all; a variable for the
// tests.
var cameraStreams = true

// streamFrames shows entity's live stream while its view is up. It reports whether the view was seen
// to its end that way; false leaves it to snapshots, from wherever the stream got to.
func (f *Feature) streamFrames(entity string) bool {
	if !cameraStreams || isReolink(entity) || !video.Installed() || !canStream(entity) {
		return false
	}
	first, err := f.snapshot(entity)
	if err != nil {
		return false
	}
	f.showFrame(entity, first, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	url, err := hass.Get().CameraStream(ctx, entity)
	if err != nil {
		slog.Info("camera: no stream, showing snapshots", "entity", entity, "err", err)
		return false
	}
	w, h := first.Bounds().Dx()&^1, first.Bounds().Dy()&^1
	cam, err := video.OpenCamera(ctx, url, w, h)
	if err != nil {
		slog.Info("camera: stream not opened, showing snapshots", "entity", entity, "err", err)
		return false
	}
	defer cam.Close()
	began, frames := time.Now(), 0
	wait := streamStart
	for f.viewUp(entity) {
		frame, err := cam.Next(wait)
		if err != nil {
			slog.Warn("camera: stream ended, showing snapshots", "entity", entity, "frames", frames, "err", err)
			return false
		}
		if frames == 0 {
			slog.Info("camera: streaming", "entity", entity, "size", fmt.Sprintf("%dx%d", w, h),
				"first frame in", time.Since(began).Round(100*time.Millisecond))
		}
		frames++
		wait = streamStall
		f.showFrame(entity, frame, nil)
	}
	slog.Info("camera: stream closed", "entity", entity, "frames", frames,
		"for", time.Since(began).Round(time.Second))
	return true
}

// prewarmStream has Home Assistant start entity's stream, if it can stream it, without watching it: a
// stream nobody watches is kept for half a minute, long enough for the camera tapped on the list to come
// up moving almost at once rather than after the seconds a camera's stream takes to start.
func prewarmStream(entity string) {
	if !cameraStreams || !video.Installed() || !canStream(entity) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := hass.Get().CameraStream(ctx, entity); err != nil {
		slog.Debug("camera stream prewarm", "entity", entity, "err", err)
	}
}

// canStream is whether Home Assistant says it can stream the camera.
func canStream(entity string) bool {
	st, err := hass.Get().State(entity)
	if err != nil {
		return false
	}
	features, _ := st.Attributes["supported_features"].(float64)
	return int(features)&hass.CameraCanStream != 0
}
