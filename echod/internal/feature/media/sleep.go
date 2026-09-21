package media

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The sleep timer: what is playing stops in so many minutes, the way a clock radio has since before
// any of this. It is not one of the device's timers — nothing rings and nothing is announced. The
// music stops, and that is the whole of it.
//
// It is kept in memory rather than the config: a device that restarts in the night should come back
// silent, not with an hour of radio still owed to somebody who is asleep.

// sleepOptions are the lengths offered, in minutes. Off is no timer.
var sleepOptions = []int{15, 30, 45, 60, 90, 120}

type sleeper struct {
	sel *esphome.Select

	mu    sync.Mutex
	until time.Time
	stop  chan struct{}
}

const sleepOff = "Off"

func newSleeper() *sleeper {
	s := &sleeper{}
	opts := []string{sleepOff}
	for _, m := range sleepOptions {
		opts = append(opts, minutesLabel(m))
	}
	s.sel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "sleep_timer",
			Name:     "Sleep timer",
			Icon:     "mdi:sleep",
			Category: esphome.CategoryConfig,
		},
		Options:   opts,
		OnCommand: func(v string) { s.choose(v) },
	}
	s.sel.Set(sleepOff)
	return s
}

// minutesLabel is a length as the list says it: "30 minutes", "1 hour", "1 hour 30 minutes".
func minutesLabel(m int) string {
	h, rest := m/60, m%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d minutes", rest)
	case rest == 0 && h == 1:
		return "1 hour"
	case rest == 0:
		return fmt.Sprintf("%d hours", h)
	case h == 1:
		return fmt.Sprintf("1 hour %d minutes", rest)
	}
	return fmt.Sprintf("%d hours %d minutes", h, rest)
}

// choose sets the timer from a name in the list, or clears it.
func (s *sleeper) choose(label string) {
	if label == sleepOff {
		s.Cancel()
		return
	}
	for _, m := range sleepOptions {
		if minutesLabel(m) == label {
			s.Set(time.Duration(m) * time.Minute)
			return
		}
	}
}

// Set stops whatever is playing in d. Asking again moves the time rather than adding another.
func (s *sleeper) Set(d time.Duration) {
	s.mu.Lock()
	if s.stop != nil {
		close(s.stop)
	}
	stop := make(chan struct{})
	s.until, s.stop = time.Now().Add(d), stop
	s.mu.Unlock()

	slog.Info("sleep timer", "in", d)
	s.set(d)
	safe.Go("sleep timer", func() {
		select {
		case <-stop:
		case <-time.After(d):
			s.fire(stop)
		}
	})
}

// fire stops the music, unless this timer was replaced while it waited.
func (s *sleeper) fire(stop chan struct{}) {
	s.mu.Lock()
	mine := s.stop == stop
	if mine {
		s.until, s.stop = time.Time{}, nil
	}
	s.mu.Unlock()
	if !mine {
		return
	}
	slog.Info("sleep timer: stopping what is playing")
	Get().Stop()
	s.sel.Set(sleepOff)
}

// Cancel leaves the music alone.
func (s *sleeper) Cancel() {
	s.mu.Lock()
	if s.stop != nil {
		close(s.stop)
	}
	was := !s.until.IsZero()
	s.until, s.stop = time.Time{}, nil
	s.mu.Unlock()
	if was {
		slog.Info("sleep timer cancelled")
	}
	s.sel.Set(sleepOff)
}

// Left is how long the music has, zero when no timer is set.
func (s *sleeper) Left() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.until.IsZero() {
		return 0
	}
	return max(time.Until(s.until), 0)
}

// set shows the length on Home Assistant's list.
func (s *sleeper) set(d time.Duration) {
	m := int(d / time.Minute)
	for _, o := range sleepOptions {
		if o == m {
			s.sel.Set(minutesLabel(o))
			return
		}
	}
}
