package home

import (
	"context"
	"errors"
	"image"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// A camera with a stream is heard from it: its ask is answered here, and Home Assistant is not asked to
// send the stream back converted, which came a dozen seconds and more behind the picture. One without a
// stream, and one whose stream brings no sound (none in it, or one that cannot be read), is asked of Home
// Assistant as before, rather than left silent.
func TestACameraWithAStreamIsHeardFromIt(t *testing.T) {
	fake := fakeOverFor(t)
	var answered []media.OverToken
	fake.player.Answer = func(tk media.OverToken, open func(context.Context) (io.ReadCloser, error)) bool {
		answered = append(answered, tk)
		return true
	}
	env := fake.env()
	calls := 0
	env.call = func(string, string) error { calls++; return nil }
	prev := streamedSound
	streamedSound = func(entity string) func(context.Context) (io.ReadCloser, error) {
		switch entity {
		case "camera.porch":
			return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("pcm")), nil }
		case "camera.yard": // a stream with no sound in it: ffmpeg ends at once
			return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil }
		case "camera.gate": // one that cannot be opened
			return func(context.Context) (io.ReadCloser, error) { return nil, errors.New("refused") }
		}
		return nil
	}
	t.Cleanup(func() { streamedSound = prev })

	f := &Feature{}
	streamed := env.player.Ask()
	f.askCameraSound("camera.porch", streamed, env)
	if !slices.Equal(answered, []media.OverToken{streamed}) || calls != 0 {
		t.Errorf("a camera with a stream: answered %v, Home Assistant asked %d times", answered, calls)
	}
	if !slices.Contains(fake.settledTokens, streamed) {
		t.Error("the streamed ask was never settled")
	}

	for i, entity := range []string{"camera.garage", "camera.yard", "camera.gate"} {
		f.askCameraSound(entity, env.player.Ask(), env)
		if len(answered) != 1 || calls != i+1 {
			t.Errorf("%s: answered %v, Home Assistant asked %d times", entity, answered, calls)
		}
	}
}

// A frame fetched for a view that has since been closed and opened again does not show in the new one:
// two views of the same camera are told apart by their number, not by the camera.
func TestAFrameShowsOnlyInTheViewItWasFetchedFor(t *testing.T) {
	f := &Feature{}
	f.cam = CameraView{Entity: "camera.porch", Until: time.Now().Add(time.Minute), gen: 2}
	f.showFrame("camera.porch", 1, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
	if f.cam.Frame != nil {
		t.Error("a frame of the view before showed in this one")
	}
	if f.viewUp(context.Background(), "camera.porch", 1) {
		t.Error("the view before still counts as up")
	}
	f.showFrame("camera.porch", 2, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
	if f.cam.Frame == nil {
		t.Error("this view's own frame did not show")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if f.viewUp(ctx, "camera.porch", 2) {
		t.Error("a view whose fetching was called off still counts as up")
	}
}
