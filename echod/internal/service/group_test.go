package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fake is a service that does what a test tells it to.
type fake struct {
	name string

	mu       sync.Mutex
	starts   int
	runs     int
	closes   int
	order    *[]string
	startErr error

	// start, when set, is what Start does, so a test can make re-acquiring fail for a while.
	start func() error

	// run is what Run does. Returning an error looks like breaking.
	run func(ctx context.Context) error
}

func (f *fake) Name() string { return f.name }

func (f *fake) Start(ctx context.Context) error {
	f.mu.Lock()
	f.starts++
	if f.order != nil {
		*f.order = append(*f.order, "start:"+f.name)
	}
	err, start := f.startErr, f.start
	f.mu.Unlock()

	if start != nil {
		return start()
	}
	return err
}

func (f *fake) Run(ctx context.Context) error {
	f.mu.Lock()
	f.runs++
	run := f.run
	f.mu.Unlock()

	if run != nil {
		return run(ctx)
	}
	<-ctx.Done()
	return nil
}

func (f *fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closes++
	if f.order != nil {
		*f.order = append(*f.order, "close:"+f.name)
	}
	return nil
}

func (f *fake) counts() (starts, runs, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.runs, f.closes
}

// blocks until canceled, which is what most services do.
func blocks(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Services come up in the order they were added and go down in reverse, because acquiring hardware
// off Android is order-dependent and releasing it should unwind.
func TestStartsInOrderAndClosesInReverse(t *testing.T) {
	var order []string
	a := &fake{name: "a", order: &order, run: blocks}
	b := &fake{name: "b", order: &order, run: blocks}
	c := &fake{name: "c", order: &order, run: blocks}

	g := New()
	g.Add(a)
	g.Add(b)
	g.Add(c)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"start:a", "start:b", "start:c", "close:c", "close:b", "close:a"}
	if len(order) != len(want) {
		t.Fatalf("order was %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order was %v, want %v", order, want)
		}
	}
}

// A required service that cannot be acquired is fatal, and nothing after it is started: the process
// is better off exiting and being restarted by init than limping.
func TestRequiredStartFailureIsFatal(t *testing.T) {
	broken := &fake{name: "broken", startErr: errors.New("no device")}
	after := &fake{name: "after", run: blocks}

	g := New()
	g.Add(broken, Required())
	g.Add(after)

	err := g.Run(context.Background())
	if err == nil {
		t.Fatal("Run returned nil for a failed required service")
	}

	if starts, _, _ := after.counts(); starts != 0 {
		t.Errorf("the service after a fatal one was started %d times", starts)
	}
	if got := statusOf(g, "broken").State; got != StateFailed {
		t.Errorf("state is %q, want failed", got)
	}
}

// An optional service that cannot be acquired is recorded and the rest carry on, which is how a
// device with no speaker still answers.
func TestOptionalStartFailureDegrades(t *testing.T) {
	broken := &fake{name: "broken", startErr: errors.New("no device")}
	after := &fake{name: "after", run: blocks}

	g := New()
	g.Add(broken)
	g.Add(after)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	if starts, _, _ := after.counts(); starts != 1 {
		t.Errorf("the service after an optional failure started %d times, want 1", starts)
	}
	if Healthy(g.Status()) {
		t.Error("the group reports healthy with a failed service")
	}

	cancel()
	<-done
}

// The whole point: a service that breaks is closed, acquired again and run again.
func TestRestartsAfterFailure(t *testing.T) {
	var attempts int
	var mu sync.Mutex

	svc := &fake{name: "flaky"}
	svc.run = func(ctx context.Context) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()

		if n < 3 {
			return errors.New("broke")
		}
		<-ctx.Done()
		return nil
	}

	g := New()
	g.Add(svc, Restart(5*time.Millisecond, 10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if s := statusOf(g, "flaky"); s.State == StateRunning && s.Restarts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never settled: %+v", statusOf(g, "flaky"))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Re-acquired each time, not just re-run: the device may have been taken while it was gone.
	starts, runs, closes := svc.counts()
	if starts < 3 || runs < 3 || closes < 2 {
		t.Errorf("starts=%d runs=%d closes=%d, want at least 3/3/2", starts, runs, closes)
	}

	cancel()
	<-done
}

// A service with no restart policy that breaks stays broken and says so.
func TestFailureWithoutRestartStaysFailed(t *testing.T) {
	svc := &fake{name: "once", run: func(context.Context) error { return errors.New("broke") }}

	g := New()
	g.Add(svc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = g.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for statusOf(g, "once").State != StateFailed {
		if time.Now().After(deadline) {
			t.Fatalf("state is %q, want failed", statusOf(g, "once").State)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, runs, _ := svc.counts(); runs != 1 {
		t.Errorf("ran %d times without a restart policy, want 1", runs)
	}
	if err := statusOf(g, "once").Err; err == nil {
		t.Error("no error recorded for a failed service")
	}
}

// A one-shot service returning nil is finished, not broken.
func TestOneShotStops(t *testing.T) {
	svc := &fake{name: "splash", run: func(context.Context) error { return nil }}

	g := New()
	g.Add(svc, Once())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = g.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for statusOf(g, "splash").State != StateStopped {
		if time.Now().After(deadline) {
			t.Fatalf("state is %q, want stopped", statusOf(g, "splash").State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !Healthy(g.Status()) {
		t.Error("a finished one-shot service makes the group unhealthy")
	}
}

// A service with no Start or Close is still supervised.
type bare struct{ name string }

func (b bare) Name() string                  { return b.name }
func (b bare) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func TestServiceWithoutStartOrClose(t *testing.T) {
	g := New()
	g.Add(bare{name: "plain"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	if got := statusOf(g, "plain").State; got != StateRunning {
		t.Errorf("state is %q, want running", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func statusOf(g *Group, name string) Status {
	for _, s := range g.Status() {
		if s.Name == name {
			return s
		}
	}
	return Status{}
}

// A service whose re-acquire fails is not run.
//
// Run on a service that never got its device back reaches a field nothing filled in — a nil server, a
// nil handle — and panics on its first line. That panic is caught, so a device that is simply not
// there yet reads as a service crashing in a loop, and the backoff is spent on Run rather than on the
// acquire that is actually failing. Every Run has to be paired with a Start that worked.
func TestFailedReacquireDoesNotRun(t *testing.T) {
	const refusals = 3

	var mu sync.Mutex
	var acquired, refused int

	svc := &fake{name: "device"}
	svc.start = func() error {
		mu.Lock()
		defer mu.Unlock()
		if acquired == 1 && refused < refusals {
			refused++
			return errors.New("device busy")
		}
		acquired++
		return nil
	}
	svc.run = func(ctx context.Context) error {
		mu.Lock()
		n, r := acquired, refused
		mu.Unlock()

		if n == 1 {
			return errors.New("broke") // the first life ends, and the restart begins
		}
		if r != refusals {
			t.Errorf("ran after %d of %d refusals", r, refusals)
		}
		<-ctx.Done()
		return nil
	}

	g := New()
	g.Add(svc, Restart(5*time.Millisecond, 10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if s := statusOf(g, "device"); s.State == StateRunning && s.Restarts >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never came back: %+v", statusOf(g, "device"))
		}
		time.Sleep(time.Millisecond)
	}

	// Two acquires that worked, so two runs, and the refusals in between cost nothing but time.
	starts, runs, _ := svc.counts()
	if starts != 2+refusals {
		t.Errorf("started %d times, want %d", starts, 2+refusals)
	}
	if runs != 2 {
		t.Errorf("ran %d times, want 2: one per acquire that worked", runs)
	}

	cancel()
	<-done
}

// A service that asks to be restarted is started again, and the supervisor does not record it as
// having failed: its status carries no error while it waits, which is what tells a reader (and the
// diagnostics that surface it) that nothing went wrong. The capture service does this every time the
// mute button releases the microphone chip.
func TestRestartOnRequestIsNotAFailure(t *testing.T) {
	var attempts int
	var mu sync.Mutex

	svc := &fake{name: "asker"}
	svc.run = func(ctx context.Context) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()

		if n < 3 {
			return fmt.Errorf("%w: handed the device back", ErrRestart)
		}
		<-ctx.Done()
		return nil
	}

	g := New()
	g.Add(svc, Restart(5*time.Millisecond, time.Hour)) // a steady window it can never reach

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if s := statusOf(g, "asker"); s.State == StateRunning && s.Restarts >= 2 {
			break
		}
		if s := statusOf(g, "asker"); s.State == StateRetrying && s.Err != nil {
			t.Fatalf("a requested restart was recorded as a failure: %v", s.Err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("never settled: %+v", statusOf(g, "asker"))
		}
		time.Sleep(time.Millisecond)
	}

	starts, runs, closes := svc.counts()
	if starts < 3 || runs < 3 || closes < 2 {
		t.Errorf("starts=%d runs=%d closes=%d, want at least 3/3/2", starts, runs, closes)
	}

	cancel()
	<-done
}
