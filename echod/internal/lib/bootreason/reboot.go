package bootreason

import (
	"sync"
	"sync/atomic"
	"time"
)

// What has to be done before the daemon reboots the device itself. A reboot by the system call ends the
// process where it stands, without the stop that runs everything's last words (feature/detect moves the
// wake clips waiting in RAM to the storage as it stops), so what has to outlive it registers here and
// is run first, in every reboot the daemon makes (Reboot).

var (
	hooksMu sync.Mutex
	hooks   []func()

	resetting atomic.Bool
)

// BeforeReboot has fn run before every reboot the daemon makes, in the order registered. fn must be
// quick: the device is going down.
func BeforeReboot(fn func()) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks = append(hooks, fn)
}

// runHooks runs what BeforeReboot registered.
func runHooks() {
	hooksMu.Lock()
	list := append([]func(){}, hooks...)
	hooksMu.Unlock()
	// Each hook is kept from stopping the reboot: a panic in one is caught, and all of them get at
	// most hookTime. The reboot comes after a slot was put on trial (update), so a hook that never
	// returned would leave the device on the old slot until someone pulled the plug.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, fn := range list {
			func() {
				defer func() { _ = recover() }()
				fn()
			}()
		}
	}()
	select {
	case <-done:
	case <-time.After(hookTime):
	}
}

// hookTime is how long the hooks before a reboot may take, together.
const hookTime = 5 * time.Second

// Resetting says the restart coming is a reset for someone else (feature/firstrun), which deletes what
// the device keeps: nothing is to be saved for it, as it would only be written and then deleted.
func Resetting() { resetting.Store(true) }

// IsReset is whether the restart coming is a reset (Resetting).
func IsReset() bool { return resetting.Load() }
