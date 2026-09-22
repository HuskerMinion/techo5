//go:build !dot

package camera

import (
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
