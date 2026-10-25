package home

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
)

// The camera grid: several cameras on the screen at once, from their snapshots, each refreshed on its own.
// A tap on one puts that camera up on its own, live where it can be (display). It is a camera view like any
// other, under the name GridEntity, so everything the screen does for a camera up (keeping the screen on,
// the night, how long it stays) it does for the grid; only the drawing and the tap differ.
//
// Live video would be the better picture, but four streams decoded at once are more than these devices
// can do: the grid shows snapshots, as the camera page did before live video, and live is one tap away.

// GridEntity is the camera view's entity while the grid is up.
const GridEntity = "grid"

// MaxGridCells is the most cameras the grid shows: three across, two down.
const MaxGridCells = 6

// MaxGridFor is the longest the grid is put up for at once.
const MaxGridFor = time.Hour

// gridEvery is the least time between one snapshot of a camera in the grid and the next, and
// gridBackOff the most a camera that keeps failing waits between tries.
var (
	gridEvery   = time.Second
	gridBackOff = 30 * time.Second
)

// fetchCamera is a camera's snapshot from Home Assistant, asked for about width wide so Home Assistant
// scales it rather than the device, and given up on when ctx ends; a variable for the tests.
var fetchCamera = func(ctx context.Context, entity string, width int) ([]byte, error) {
	return hass.Get().FetchURLContext(ctx, "/api/camera_proxy/"+entity+"?width="+strconv.Itoa(width))
}

// decoding lets one snapshot at a time be decoded and scaled: a camera's picture can be 4K, which is tens
// of megabytes decoded, and six of them at once is too much for a device with a gigabyte. The fetches go
// on side by side; only this waits.
var decoding = make(chan struct{}, 1)

// GridCell is one camera in the grid.
type GridCell struct {
	Entity string
	Name   string
	Frame  *image.RGBA // its latest snapshot, scaled to about a cell (the screen fits it exactly); nil until the first
	Error  string      // why there is no picture, when there is none
}

// GridLayout is how many cells across and down a grid of n cameras has.
func GridLayout(n int) (cols, rows int) {
	switch {
	case n <= 1:
		return 1, 1
	case n == 2:
		return 2, 1
	case n <= 4:
		return 2, 2
	}
	return 3, 2
}

var errNoGridCameras = errors.New("no cameras to show: list them with home_cameras, or give the device a Home Assistant token")

// gridCamera is whether entity can be in the grid: one of Home Assistant's cameras, a Reolink recorder's,
// or a go2rtc stream.
func gridCamera(entity string) bool {
	return strings.HasPrefix(entity, "camera.") || isReolink(entity) || isGo2RTC(entity)
}

// gridGo2RTC fetches a go2rtc stream's picture for a cell, from go2rtc itself rather than Home
// Assistant, which has no such camera; a variable for the tests.
var gridGo2RTC = go2rtcSnap

// ShowGrid puts the grid up for d (at most MaxGridFor), with the cameras named (entity ids), or the first
// of the list when none are. The device's own camera is not one of them: its sensor is the screen's, and
// one picture of it is the camera view's.
func (f *Feature) ShowGrid(entities []string, d time.Duration) error {
	d = min(d, MaxGridFor)
	names := map[string]string{}
	var listed []string
	for _, c := range f.Cameras() {
		if c.Entity == LocalCamera {
			continue
		}
		names[c.Entity] = c.Name
		listed = append(listed, c.Entity)
	}
	if len(entities) == 0 {
		entities = listed
	}
	var cells []GridCell
	for _, e := range entities {
		e = strings.TrimSpace(e)
		if !gridCamera(e) || slices.ContainsFunc(cells, func(c GridCell) bool { return c.Entity == e }) {
			continue
		}
		name := names[e]
		if name == "" {
			name = strings.TrimPrefix(e, "camera.")
		}
		cells = append(cells, GridCell{Entity: e, Name: name})
		if len(cells) == MaxGridCells {
			break
		}
	}
	if len(cells) == 0 {
		return errNoGridCameras
	}

	f.mu.Lock()
	f.endCaption("replaced")
	f.cancelStill()
	f.dropWaiting("replaced")
	f.cam = CameraView{Entity: GridEntity, Name: "Cameras", Until: time.Now().Add(d), Grid: cells, span: d, View: f.newView(), gen: f.cam.gen}
	f.camMuted = false
	ctx := f.newCameraFetch()
	gen := f.cam.gen
	f.mu.Unlock()

	slog.Info("camera grid up", "cameras", len(cells), "for", d)
	cols, rows := GridLayout(len(cells))
	fw, fh := cameraFrameSize()
	w, h := fw/cols, fh/rows
	for i, c := range cells {
		f.spawn(func() { f.gridFrames(ctx, gen, i, c.Entity, w, h) })
	}
	f.Changed.Emit(struct{}{})
	return nil
}

// gridFrames keeps cell i of grid view gen in snapshots of its camera, on its own, so that a camera that
// is slow to answer holds up nobody else's. One that keeps failing is asked less often, up to gridBackOff.
func (f *Feature) gridFrames(ctx context.Context, gen uint64, i int, entity string, w, h int) {
	wait := gridEvery
	for f.viewUp(ctx, GridEntity, gen) {
		started := time.Now()
		frame, err := f.gridSnapshot(ctx, entity, w, h)
		f.mu.Lock()
		if f.viewIsUp(GridEntity) && f.cam.gen == gen && i < len(f.cam.Grid) {
			if err != nil {
				f.cam.Grid[i].Error = err.Error()
			} else {
				f.cam.Grid[i].Frame, f.cam.Grid[i].Error = frame, ""
			}
		}
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		if err != nil {
			wait = min(max(wait*2, cameraRetry), gridBackOff)
		} else {
			wait = gridEvery
		}
		if rest := wait - time.Since(started); rest > 0 && !pause(ctx, rest) {
			return
		}
	}
}

// gridSnapshot is a camera's snapshot scaled to fit w by h.
func (f *Feature) gridSnapshot(ctx context.Context, entity string, w, h int) (*image.RGBA, error) {
	var b []byte
	var err error
	switch {
	case isReolink(entity):
		b, err = reolinkSnapAt(entity, w)
	case isGo2RTC(entity):
		b, err = gridGo2RTC(ctx, entity, w)
	default:
		b, err = fetchCamera(ctx, entity, w)
	}
	if err != nil {
		return nil, err
	}
	select {
	case decoding <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-decoding }()
	src, err := decodeWithin(b, maxFramePixels, "camera "+entity)
	if err != nil {
		return nil, err
	}
	return FitInto(src, w, h), nil
}

// FitInto scales a picture to fit w by h, keeping its shape.
func FitInto(src image.Image, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	dw, dh := w, sh*w/sw
	if dh > h {
		dw, dh = sw*h/sh, h
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(dw, 1), max(dh, 1)))
	picscale.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src)
	return dst
}
