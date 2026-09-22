//go:build !dot

package camera

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// offDevice makes a Camera that Available() says yes to, and whose sensor is a function rather than
// the hardware, so the start and stop lifecycle can be driven on a machine with no camera at all.
func offDevice(t *testing.T, owner func(stop, stopped chan struct{})) *Camera {
	t.Helper()
	was := nodes
	nodes = []string{t.TempDir()}
	t.Cleanup(func() { nodes = was })
	return &Camera{owner: owner}
}

// A stop that is still running owns the sensor until its goroutine returns. idleStop clears running
// and lets the lock go first, so an Acquire landing in that window used to open the sensor a second
// time; the goroutine on its way out then tore CSI2 down underneath the new stream, and an imgsensor
// that answers the next open with EIO writes the camera off until the device is rebooted (#17).
func TestAcquireWaitsForAStopToFinish(t *testing.T) {
	var mu sync.Mutex
	open, most := 0, 0
	c := offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		mu.Lock()
		open++
		if open > most {
			most = open
		}
		mu.Unlock()
		<-stop
		// The teardown the old goroutine is still in when idleStop clears running.
		time.Sleep(time.Millisecond)
		mu.Lock()
		open--
		mu.Unlock()
	})

	for range 50 {
		release, err := c.Acquire()
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		release() // nobody wants it any more: the linger timer is armed

		var stopping sync.WaitGroup
		stopping.Add(1)
		go func() {
			defer stopping.Done()
			c.idleStop()
		}()

		again, err := c.Acquire()
		if err != nil {
			t.Fatalf("acquire during a stop: %v", err)
		}
		again()
		stopping.Wait()
		c.idleStop() // back to cold for the next round
	}

	mu.Lock()
	defer mu.Unlock()
	if most > 1 {
		t.Errorf("%d sensors open at once, want the second start to wait for the first to close", most)
	}
	if open != 0 {
		t.Errorf("%d sensors left open", open)
	}
}

// The camera still powers down when nobody wants it: waiting a stop out must not turn the idle stop
// into something that never happens.
func TestIdleStopStillPowersDown(t *testing.T) {
	ran := make(chan struct{}, 1)
	var c *Camera
	c = offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		c.setPowered(true)
		<-stop
		c.setPowered(false)
		ran <- struct{}{}
	})

	release, err := c.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	c.idleStop()

	select {
	case <-ran:
	default:
		t.Fatal("the sensor goroutine did not stop")
	}
	if c.Running() {
		t.Error("the camera still reports itself powered after the idle stop")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		t.Error("running is still set after the idle stop")
	}
	if c.stopping != nil {
		t.Error("the finished stop was left in the way of the next acquire")
	}
}

// An error belongs to the start it came from. A Snapshot that joins a camera which is already
// running used to read whatever the last failed start had left behind on its first tick and give up
// straight away, never waiting on the healthy stream it had just joined: the camera that fails once
// and then works next time. The stale error is put there by hand rather than provoked, because what
// is under test is who an error belongs to, not the route by which an old one outlives its start.
func TestSnapshotIgnoresAnEarlierStartsError(t *testing.T) {
	// The sensor takes its time over the first frame, so that a Snapshot has ticked and looked at
	// the camera's error a few times before it gets one: the ticks are where the wrong error used
	// to be picked up.
	var c *Camera
	c = offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		select {
		case <-stop:
			return
		case <-time.After(700 * time.Millisecond):
		}
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				c.emit(&Frame{At: time.Now()})
			}
		}
	})

	release, err := c.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	// What an earlier start left behind, named by that start's stopped channel, long closed.
	old := make(chan struct{})
	close(old)
	stale := errors.New("the camera did not open that time")
	c.mu.Lock()
	c.err, c.errFrom = stale, old
	c.mu.Unlock()

	f, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot on a healthy camera: %v, want a frame", err)
	}
	if f == nil {
		t.Fatal("snapshot returned no frame and no error")
	}
}

// The other half of it: the start a caller is actually waiting on still reports its failure at
// once, rather than leaving that caller to sit out the whole frame wait.
func TestSnapshotReportsItsOwnStartsError(t *testing.T) {
	c := offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		<-stop
	})

	release, err := c.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	boom := errors.New("open: no such device")
	c.mu.Lock()
	c.err, c.errFrom = boom, c.stopped
	c.mu.Unlock()

	if _, err := c.Snapshot(context.Background()); !errors.Is(err, boom) {
		t.Errorf("snapshot returned %v, want the error from the start it was waiting on", err)
	}
}

// A start that cannot open the sensor clears running but leaves users where it was, so the next
// Acquire sees somebody holding a camera that is not running and starts again. The failed run is
// still between booking the failure and closing its stopped channel at that point, so that second
// run used to begin on top of the first. It is the same shape as the stop that Acquire already
// waits out, and a failed open has to hand the stop over the same way.
func TestAFailedStartDoesNotLeaveASecondRunRacing(t *testing.T) {
	var mu sync.Mutex
	live, most, starts := 0, 0, 0
	booked := make(chan struct{}) // the failed start has written its failure down
	gone := make(chan struct{})   // the test lets the failed start finish
	second := make(chan struct{}) // a later run has begun

	var c *Camera
	c = offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		mu.Lock()
		starts++
		mine := starts
		live++
		if live > most {
			most = live
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			live--
			mu.Unlock()
		}()
		if mine == 1 {
			// The open failed: book it exactly as run does, then take a while to leave.
			c.startFailed(errors.New("open: no such device"), stopped)
			close(booked)
			<-gone
			return
		}
		close(second)
		<-stop
	})

	release, err := c.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	<-booked

	// The acquire that lands in the window. It has to wait for the failed run to be gone.
	waited := make(chan func(), 1)
	go func() {
		again, err := c.Acquire()
		if err != nil {
			t.Errorf("acquire after a failed start: %v", err)
			again = func() {}
		}
		waited <- again
	}()

	select {
	case <-second:
		t.Error("a second run started while the failed one was still on its way out")
	case <-time.After(50 * time.Millisecond):
	}
	close(gone)

	again := <-waited
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("the acquire that waited never got its own run started")
	}
	release()
	again()
	c.idleStop()

	mu.Lock()
	defer mu.Unlock()
	if most > 1 {
		t.Errorf("%d runs at once, want the start after a failed open to wait for it to finish", most)
	}
	if live != 0 {
		t.Errorf("%d runs left going", live)
	}
}
