package led

import (
	"context"
	"testing"
	"time"
)

// A device Home Assistant never connects to stops walking after walkFor, and none at all is no walk
// (techo5-dot#39); first run's orange is not cut short.
func TestTheWaitWalkGivesUp(t *testing.T) {
	never := func() bool { return false }
	for _, walkFor := range []time.Duration{0, 4 * FrameInterval} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		err := Splash(ctx, ringInDir(t), never, nil, walkFor)
		took := time.Since(start)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		// The fade after it is 250 milliseconds; the comet, which must not run, is SplashConfirm.
		if took >= SplashConfirm || took >= 3*time.Second {
			t.Errorf("walkFor %v: the splash took %v, want it given up", walkFor, took)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*FrameInterval)
	defer cancel()
	start := time.Now()
	if err := Splash(ctx, ringInDir(t), never, func() bool { return true }, 0); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 15*FrameInterval {
		t.Errorf("first run's orange stopped after %v, before ctx ended", took)
	}
}
