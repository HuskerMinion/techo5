package home

import (
	"context"
	"fmt"
	"image"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A camera's live stream on the camera page, instead of its snapshots, when Live camera video is on
// (CameraLive). Home Assistant hands out an HLS playlist for any camera it can stream (camera/stream),
// and the video decoder plays it, scaled to the page (feature/video, camera.go). On a Show 5, a 640×480
// sub stream at 10 to 15 frames a second costs the decoder about a seventh of a core; a 896×672 stream
// at 20 to 24 about two thirds of one. The picture is a few seconds behind the camera, since HLS comes
// in parts a keyframe interval long.
//
// Snapshots keep coming, as they did before, until the stream's first frame: that can take seconds
// (seventeen from cold has been seen) while Home Assistant starts the camera's stream. The first one
// also gives the stream its shape; without one, the stream fills the panel with bars where its shape
// does not. A camera that cannot stream, a decoder that is missing or refuses the stream (H.265, which
// the image cannot decode; a stream over 1080p), and a stream that stalls all leave the page on
// snapshots, and a camera whose stream failed is not tried again for a while.

const (
	// streamStart is how long the first frame of a stream may take: Home Assistant starts a camera's
	// stream on the first request and hands out the playlist before it has its first part.
	streamStart = 25 * time.Second
	// streamStall is how long a stream may go without a frame once it has started before it is given
	// up for snapshots.
	streamStall = 10 * time.Second
	// shapeWait is how long the stream waits for the first snapshot to learn its shape from.
	shapeWait = 5 * time.Second
	// failedFor is how long a camera whose stream failed is shown from snapshots without trying again.
	failedFor = 10 * time.Minute
)

// failedStreams is when each camera's stream last failed.
var failedStreams = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func streamFailed(entity string) {
	failedStreams.Lock()
	failedStreams.at[entity] = time.Now()
	failedStreams.Unlock()
}

func streamFailedLately(entity string) bool {
	failedStreams.Lock()
	defer failedStreams.Unlock()
	at, ok := failedStreams.at[entity]
	return ok && time.Since(at) < failedFor
}

// streamFrames shows entity's live stream while its view gen is up. It reports whether the view was
// seen to its end that way; false leaves it to snapshots, from wherever the stream got to.
func (f *Feature) streamFrames(ctx context.Context, entity string, gen uint64) bool {
	if !CameraLive() || isReolink(entity) || !video.Installed() || streamFailedLately(entity) || !canStream(entity) {
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Snapshots until the stream's first frame, the first of them giving the stream its shape.
	var streaming atomic.Bool
	shape := make(chan image.Point, 1)
	go func() {
		told := false
		for !streaming.Load() && f.viewUp(ctx, entity, gen) {
			frame, err := f.snapshot(entity)
			if streaming.Load() {
				return
			}
			if err != nil {
				slog.Debug("camera frame while the stream starts", "entity", entity, "err", err)
				pause(ctx, 2*time.Second)
				continue
			}
			if !told {
				shape <- frame.Bounds().Size()
				told = true
			}
			f.showFrame(entity, gen, frame, nil)
		}
	}()
	w, h := cameraFrameW, cameraFrameH
	select {
	case p := <-shape:
		w, h = p.X, p.Y
	case <-time.After(shapeWait):
	case <-ctx.Done():
		return true
	}
	w, h = max(w&^1, 2), max(h&^1, 2)

	url, err := hass.Get().CameraStream(ctx, entity)
	if err != nil {
		slog.Info("camera: no stream, showing snapshots", "entity", entity, "err", err)
		streamFailed(entity)
		return false
	}
	cam, err := video.OpenCamera(ctx, url, w, h)
	if err != nil {
		slog.Info("camera: stream not opened, showing snapshots", "entity", entity, "err", err)
		streamFailed(entity)
		return false
	}
	defer cam.Close()
	began, frames := time.Now(), 0
	wait := streamStart
	for f.viewUp(ctx, entity, gen) {
		frame, err := cam.Next(wait)
		if err != nil {
			if !f.viewUp(ctx, entity, gen) {
				break
			}
			slog.Warn("camera: stream ended, showing snapshots", "entity", entity, "frames", frames, "err", err)
			streamFailed(entity)
			return false
		}
		if frames == 0 {
			streaming.Store(true)
			slog.Info("camera: streaming", "entity", entity, "size", fmt.Sprintf("%dx%d", w, h),
				"first frame in", time.Since(began).Round(100*time.Millisecond))
		}
		frames++
		wait = streamStall
		f.showFrame(entity, gen, frame, nil)
	}
	slog.Info("camera: stream closed", "entity", entity, "frames", frames,
		"for", time.Since(began).Round(time.Second))
	return true
}

// streamedSound is how to hear entity from the stream its picture comes from, or nil when there is no
// better way than asking Home Assistant: live video is off, the decoder is missing, Home Assistant
// cannot stream it, its stream failed lately, or it is a recorder's camera, which Home Assistant does
// not have. A variable for the tests.
var streamedSound = func(entity string) func(context.Context) (io.ReadCloser, error) {
	if !CameraLive() || isReolink(entity) || !video.Installed() || streamFailedLately(entity) || !canStream(entity) {
		return nil
	}
	return func(ctx context.Context) (io.ReadCloser, error) {
		url, err := hass.Get().CameraStream(ctx, entity)
		if err != nil {
			return nil, err
		}
		return video.OpenCameraSound(ctx, url)
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

// pause waits d, or less if ctx ends; false when it did.
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
