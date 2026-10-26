//go:build !dot && !spot

package display

import (
	"context"
	"errors"
	"image"
	"testing"
)

// Two tiles play live at most; a third shows snapshots and tries again later, without starting a decoder.
func TestLiveTilesAreCapped(t *testing.T) {
	for range cap(tessLiveSlots) {
		tessLiveSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(tessLiveSlots) {
			<-tessLiveSlots
		}
	})
	opened := false
	was := openTessLive
	t.Cleanup(func() { openTessLive = was })
	openTessLive = func(_ context.Context, _, _, _ string, _, _ int, _ bool) (tessLive, error) {
		opened = true
		return nil, errors.New("not here")
	}
	s := &tessCamSet{cams: map[string]*tessCam{}}
	if err := s.live("camera.door", "rtsp://192.0.2.5/x", "", "", image.Pt(320, 240), false); !errors.Is(err, errLiveBusy) || opened {
		t.Errorf("a third live tile: %v, decoder started %v", err, opened)
	}
}
