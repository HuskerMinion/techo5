//go:build dot

package diag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// The Dot follows its light from the sensor's file: a change goes at once, the next a luxEvery later,
// and once two have gone close together the one after waits luxBusy.
func TestTheDotFollowsItsLight(t *testing.T) {
	was := []time.Duration{luxPoll, luxEvery, luxBusy, luxBusyFor}
	luxPoll, luxEvery, luxBusy, luxBusyFor = 5*time.Millisecond, 20*time.Millisecond, 400*time.Millisecond, 2*time.Second
	t.Cleanup(func() { luxPoll, luxEvery, luxBusy, luxBusyFor = was[0], was[1], was[2], was[3] })

	file := filepath.Join(t.TempDir(), "illuminance0_input")
	d := &Diag{lux: &esphome.Sensor{}, luxPath: file}
	light := func(v string) {
		if err := os.WriteFile(file, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shows := func(want float32, within time.Duration) {
		t.Helper()
		for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
			if d.lux.Get() == want {
				return
			}
		}
		t.Fatalf("lux %v, want %v within %v", d.lux.Get(), want, within)
	}
	light("100")
	// The loop is stopped, and waited for, before the times above are put back (cleanups run last
	// first): it reads them on every tick, and t.Context alone ends it without waiting.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.followLux(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	shows(100, time.Second)
	light("300")
	shows(300, time.Second)
	// Two readings have gone close together: a third change waits luxBusy.
	light("900")
	time.Sleep(luxBusy / 4)
	if got := d.lux.Get(); got != 300 {
		t.Fatalf("a busy room's next reading went after %v (lux %v)", luxBusy/4, got)
	}
	shows(900, 2*luxBusy)
}
