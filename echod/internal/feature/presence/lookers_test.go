//go:build !dot

package presence

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// A looker's entities follow presence's own, it is restored with them, and a watch hands it every
// frame and lets it go at its end, before its sensors are cleared.
func TestALookerSeesTheWatch(t *testing.T) {
	saved := lookers
	t.Cleanup(func() { lookers = saved })
	sw := &esphome.Switch{Base: esphome.Base{ObjectID: "fake_looker"}}
	var events []string
	lookers = []looker{{
		entities: func() []esphome.Entity { return []esphome.Entity{sw} },
		restore:  func(config.Config) { events = append(events, "restore") },
		on:       func() bool { return true },
		watch: func() (func(*camera.Frame), func()) {
			events = append(events, "watch")
			return func(*camera.Frame) { events = append(events, "frame") }, func() { events = append(events, "done") }
		},
		ended: func() { events = append(events, "ended") },
	}}
	f := Get()
	if e := f.Entities(); e[len(e)-1] != sw || len(e) != 6 {
		t.Fatalf("entities %v", e)
	}
	restoreLookers(config.Config{})
	if !lookersOn() {
		t.Fatal("a looker that is on does not want the camera")
	}
	look, done := startLookers()
	look(&camera.Frame{})
	look(&camera.Frame{})
	done()
	lookersEnded()
	want := []string{"restore", "watch", "frame", "frame", "done", "ended"}
	if len(events) != len(want) {
		t.Fatalf("events %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events %v, want %v", events, want)
		}
	}
}

// What a looker saw keeps the room near for hold, from the time of the frame.
func TestWhatALookerSawHoldsTheRoom(t *testing.T) {
	f := Get()
	f.mu.Lock()
	savedWatching, savedPresent, savedSeenAt := f.watching, f.present, f.seenAt
	f.watching, f.present, f.seenAt = true, false, time.Time{}
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.watching, f.present, f.seenAt = savedWatching, savedPresent, savedSeenAt
		f.mu.Unlock()
	})
	now := time.Now()
	f.saw(now, "a fake")
	f.mu.Lock()
	near, later := f.seenNear(now.Add(hold)), f.seenNear(now.Add(hold+time.Second))
	f.mu.Unlock()
	if !near || later {
		t.Fatalf("near at hold %v, after %v", near, later)
	}
}
