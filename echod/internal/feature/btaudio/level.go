package btaudio

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A speaker played to has a volume of its own when it takes one from this side over AVRCP (absolute
// volume), as it takes a phone's: a speaker without buttons has nothing else. It was never handed
// one from here, so it stayed where the last phone left it, and the device's volume could only ever
// turn the audio down below that. levelStream hands it the device's volume instead and lets the
// audio go at full scale (speaker.LevelSink); bluez-alsa passes the level on over AVRCP once it
// stops scaling the audio itself. A speaker that does not take a volume keeps the old way: the audio
// is turned down here.

// levelAgain is when, after the speaker first takes the volume, it is handed it again: a speaker says
// its own saved level as its AVRCP settles, a moment after it was handed the device's, and that would
// otherwise stand until the device's volume next changed. Seen on an EIFFELTON EX202, which came
// back from a reconnect at the level it had before rather than the one handed to it.
var levelAgain = []time.Duration{3 * time.Second, 8 * time.Second}

// levelAsk is how long a speaker is given to say it takes its volume from here: AVRCP comes up a
// moment after the audio does.
const levelAsk = 10 * time.Second

// levelStream is the stream to a speaker, with the speaker's volume set from the output's step.
type levelStream struct {
	*bluealsa.Stream
	al   *bluealsa.Client
	name string

	// on is whether the speaker takes its volume from here, once that is known.
	on atomic.Bool
	// last is the step last handed on, -1 for none; to carries the latest step to the goroutine that
	// hands it on, so the write loop never waits on D-Bus.
	last atomic.Int32
	to   chan int

	stop  context.CancelFunc
	close sync.Once
}

// newLevelStream wraps s, the stream to the device at path, and starts finding out whether the
// speaker takes its volume from here. absolute asks bluetoothd (bluez.Adapter.AbsoluteVolume).
func newLevelStream(al *bluealsa.Client, s *bluealsa.Stream, name string, path dbus.ObjectPath,
	absolute func(context.Context, dbus.ObjectPath) (bool, error)) *levelStream {
	ctx, stop := context.WithCancel(context.Background())
	l := &levelStream{Stream: s, al: al, name: name, to: make(chan int, 1), stop: stop}
	l.last.Store(-1)
	safe.Go("bluetooth speaker volume", func() { l.run(ctx, path, absolute) })
	return l
}

// Level is the speaker.LevelSink side: step is the output's, and whether the speaker takes it.
func (l *levelStream) Level(step int) bool {
	if !l.on.Load() {
		return false
	}
	if l.last.Swap(int32(step)) != int32(step) {
		for {
			select {
			case l.to <- step:
				return true
			default:
			}
			select {
			case <-l.to:
			default:
			}
		}
	}
	return true
}

func (l *levelStream) run(ctx context.Context, path dbus.ObjectPath, absolute func(context.Context, dbus.ObjectPath) (bool, error)) {
	if !l.ask(ctx, path, absolute) {
		return
	}
	// The audio is no longer scaled by bluez-alsa, which then passes its level on to the speaker. Only
	// after that does the output stop turning the audio down, so it is never too loud in between.
	if err := l.al.SetSoftVolume(l.PCM, false); err != nil {
		slog.Warn("bluetooth: the speaker keeps the volume set here", "device", l.name, "err", err)
		return
	}
	l.on.Store(true)
	slog.Info("bluetooth: the speaker takes the device's volume", "device", l.name)
	start := time.Now()
	again := time.NewTimer(levelAgain[0])
	defer again.Stop()
	next := 1
	for {
		select {
		case <-ctx.Done():
			return
		case <-again.C:
			l.last.Store(-1) // the next period hands the step on again
			if next < len(levelAgain) {
				again.Reset(levelAgain[next] - time.Since(start))
				next++
			}
		case step := <-l.to:
			if err := l.al.SetLevel(l.PCM, levelFor(step)); err != nil {
				slog.Warn("bluetooth: handing the speaker the volume", "device", l.name, "err", err)
			}
		}
	}
}

// ask is whether the speaker takes its volume from here, asked until levelAsk is up.
func (l *levelStream) ask(ctx context.Context, path dbus.ObjectPath, absolute func(context.Context, dbus.ObjectPath) (bool, error)) bool {
	deadline := time.Now().Add(levelAsk)
	for {
		actx, cancel := context.WithTimeout(ctx, 2*time.Second)
		yes, err := absolute(actx, path)
		cancel()
		if yes {
			return true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			if err != nil {
				slog.Warn("bluetooth: asking whether the speaker takes a volume", "device", l.name, "err", err)
			}
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// Close stops handing on the volume and closes the stream.
func (l *levelStream) Close() error {
	l.close.Do(l.stop)
	return l.Stream.Close()
}
