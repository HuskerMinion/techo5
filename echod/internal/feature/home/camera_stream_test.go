package home

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// A camera with a stream is heard from it: its ask is answered here, and Home Assistant is not asked to
// send the stream back converted, which came a dozen seconds and more behind the picture. One without a
// stream is asked of Home Assistant as before.
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
		if entity != "camera.porch" {
			return nil
		}
		return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil }
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

	asked := env.player.Ask()
	f.askCameraSound("camera.garage", asked, env)
	if len(answered) != 1 || calls != 1 {
		t.Errorf("a camera without one: answered %v, Home Assistant asked %d times", answered, calls)
	}
}
