package led

import (
	"context"
	"time"
)

// Until Home Assistant subscribes a voice pipeline the device hears a wake word perfectly well and
// can do nothing with it, so booting and ready have to look different. The wait steps around the
// ring; being ready glides once and stops.
const (
	// WalkStep is how long each position of the boot walk holds. Six positions around the ring, so a
	// revolution takes six of these.
	WalkStep = 250 * time.Millisecond

	// SplashConfirm is how long the comet runs once the pipeline is listening.
	SplashConfirm = 2 * time.Second
)

// Splash animates the ring while the device comes up, then fades out. It steps around the ring
// until ready reports true, then runs the comet for SplashConfirm, so the ring says whether the
// device is merely running or actually able to answer. A nil ready goes straight to the comet.
//
// ctx cancellation ends it wherever it has got to, so a device Home Assistant never talks to does
// not step around forever.
//
// setup, when it reports true, is first run still to do (feature/firstrun): while it is, the ring
// breathes orange instead, the color an Echo is set up in, so a device nobody has set up yet does not
// look like one waiting for its Home Assistant. A short press of the action button then opens the
// setup page (feature/setup). A nil setup never does.
//
// walkFor is how long the walk goes on before it gives up and the ring fades out, without the comet,
// since the device still cannot answer: a Dot used as an AirPlay speaker with no Home Assistant would
// otherwise walk for as long as it is on (techo5-dot#39). Zero leaves the walk out. The orange of
// first run is not limited: it is what the device is waiting for somebody to do.
func Splash(ctx context.Context, r *Ring, ready, setup func() bool, walkFor time.Duration) error {
	fade := func() error {
		// ctx may be done by now, so the fade needs its own.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		return fadeOut(fctx, r, 250*time.Millisecond)
	}
	if ready != nil {
		waiting, needs := walk(HomeAssistant), breathe(Palette{Setup})
		var walked time.Duration
		last := time.Duration(0)
		gaveUp := false
		frame := func(elapsed time.Duration) []Color {
			step := elapsed - last
			last = elapsed
			if setup != nil && setup() {
				return needs(elapsed)
			}
			walked += step
			if walked >= walkFor {
				gaveUp = true
				return nil
			}
			return waiting(elapsed)
		}
		if err := until(ctx, r, frame, func() bool { return gaveUp || ready() }); err != nil {
			return err
		}
		if gaveUp && !ready() {
			return fade()
		}
	}
	if err := play(ctx, r, SplashConfirm, comet(Palette{HomeAssistant})); err != nil {
		return err
	}
	return fade()
}

// until animates frame until ready reports true, or ctx is canceled. A frame that comes back nil is
// not drawn: it is the frame saying it is done, and ready says so next.
func until(ctx context.Context, r *Ring, frame Frame, ready func() bool) error {
	t := time.NewTicker(FrameInterval)
	defer t.Stop()

	start := time.Now()
	for !ready() {
		if f := frame(time.Since(start)); f != nil {
			if err := r.SetSegments(f); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
	return nil
}

// walk lights one segment at a time, hopping two positions per step so it lands on every other
// segment. Holding each position and skipping the one between reads as stepping, where the comet
// glides and has a tail: waiting should not look like a slow version of being ready.
//
// It is not in the catalog. Boot is the one thing on the ring nobody chooses.
func walk(base Color) Frame {
	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		out[2*int(elapsed/WalkStep)%Segments] = base
		return out
	}
}

func fadeOut(ctx context.Context, r *Ring, d time.Duration) error {
	steps := int(d / FrameInterval)
	for i := steps; i > 0; i-- {
		if err := r.SetSegments(around(Palette{HomeAssistant}, float64(i)/float64(steps))); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return r.Off()
		case <-time.After(FrameInterval):
		}
	}
	return r.Off()
}
