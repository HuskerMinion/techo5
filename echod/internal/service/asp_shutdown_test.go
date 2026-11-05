package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The resident ASP stop hook must run before waiting for blocked capture Run
// and before invoking any capture Close, also for required Start failure.
func TestShutdownHookBeforeRunWaitAndClose(t *testing.T) {
	for _, kind := range []string{"signal", "update", "early-required-error"} {
		t.Run(kind, func(t *testing.T) {
			g := New()
			started := make(chan struct{})
			released := make(chan struct{})
			var once sync.Once
			holder := &fake{name: "audio", run: func(ctx context.Context) error {
				once.Do(func() { close(started) })
				<-released // intentionally ignores ctx; only resident stop unblocks
				return nil
			}}
			g.Add(holder)
			if kind == "early-required-error" {
				g.Add(&fake{name: "required", startErr: errors.New("missing required hardware")}, Required())
			}
			g.SetBeforeWait(func() { close(released) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- g.Run(ctx) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("capture did not run")
			}
			if kind != "early-required-error" {
				cancel()
			}
			select {
			case err := <-done:
				if kind == "early-required-error" && err == nil {
					t.Fatal("required failure suppressed")
				}
				_, _, closes := holder.counts()
				if closes != 1 {
					t.Fatalf("close count %d", closes)
				}
			case <-time.After(time.Second):
				t.Fatal("hook did not cancel before wait")
			}
		})
	}
}

// On a required service's Start failure, already started services must see a
// canceled context; stopping only the External pipe is not sufficient.
func TestRequiredStartCancelsAlreadyRunningServices(t *testing.T) {
	started := make(chan struct{})
	g := New()
	g.Add(&fake{name: "capture", run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}})
	g.Add(&fake{name: "required", start: func() error {
		<-started
		return errors.New("required hardware missing")
	}}, Required())
	stopHook := make(chan struct{})
	g.SetBeforeWait(func() { close(stopHook) })
	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("required service failure hidden")
		}
	case <-time.After(time.Second):
		t.Fatal("required failure blocked waiting for live service")
	}
	select {
	case <-stopHook:
	default:
		t.Fatal("resident hook was not called")
	}
}
