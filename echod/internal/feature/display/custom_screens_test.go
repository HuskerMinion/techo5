//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// fakeScreens registers two custom screens for a test, one this device does not have, and takes them
// away after it.
func fakeScreens(t *testing.T) {
	was := customScreens
	t.Cleanup(func() { customScreens = was })
	customScreens = nil
	draw := func(r *renderer, s scene) []customZone {
		zones := r.customBar(s, "Two things", true, false)
		return append(zones, customZone{r: image.Rect(0, r.h/2, r.w, r.h), kind: "item", id: s.custom.data.(string)})
	}
	registerScreen(customScreen{id: "maps", label: "Maps", rank: 20, fill: func(_ *Display, v *customView) { v.data = "map" }, draw: draw})
	registerScreen(customScreen{id: "notes", label: "Notes", rank: 10, fill: func(_ *Display, v *customView) { v.data = "note" }, draw: draw,
		key: func(s scene) string { return s.custom.data.(string) }})
	registerScreen(customScreen{id: "elsewhere", label: "Elsewhere", rank: 30, offered: func() bool { return false }})
}

// The custom screens this device has are Idle screen's choices, in their ranks' order, after the clock.
func TestIdleScreenOptions(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	fakeScreens(t)
	if got := idleScreenOptions(); !slices.Equal(got, []string{"Clock", "Notes", "Maps"}) {
		t.Fatalf("options %v", got)
	}
	if got := customAfter("maps", +1); got != "notes" {
		t.Fatalf("after maps, %q", got)
	}
	if _, ok := customByID("elsewhere"); ok {
		t.Fatal("a screen this device does not offer was found")
	}
}

// A custom screen chosen as the idle screen stands in for the clock: not over the music, away for a
// while once put away by hand, and back after another opened by hand has been left alone.
func TestIdleScreen(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	fakeScreens(t)
	d := &Display{poke: make(chan struct{}, 1)}
	shown := func(nowPlaying bool) customView {
		s := scene{now: time.Now(), phase: "idle", nowPlaying: nowPlaying}
		d.customScene(&s, s.now)
		return s.custom
	}
	if got := shown(false).name; got != "" {
		t.Fatalf("no idle screen chosen, %q shown", got)
	}

	d.setIdleScreen(1)
	if got := config.Get().Screen.IdleScreen; got != "notes" {
		t.Fatalf("saved %q", got)
	}
	if got := shown(false); got.name != "notes" || got.data != "note" {
		t.Fatalf("idle screen Notes, %+v shown", got)
	}
	if got := shown(true).name; got != "" {
		t.Fatalf("music playing, %q shown over it", got)
	}

	d.closeCustom()
	if got := shown(false).name; got != "" {
		t.Fatalf("put away, %q shown", got)
	}
	d.mu.Lock()
	d.customs.awayUntil = time.Now().Add(-time.Second)
	d.mu.Unlock()
	if got := shown(false).name; got != "notes" {
		t.Fatalf("away over, %q shown", got)
	}

	d.openCustom("maps")
	if got := shown(false).name; got != "maps" {
		t.Fatalf("Maps opened by hand, %q shown", got)
	}
	d.mu.Lock()
	d.customs.touched = time.Now().Add(-time.Hour)
	d.mu.Unlock()
	if got := shown(false).name; got != "notes" {
		t.Fatalf("Maps left alone, %q shown rather than the idle screen", got)
	}

	// One this device does not have is the clock.
	for _, id := range []string{"elsewhere", "gone"} {
		if err := config.Set().Screen().IdleScreen(id); err != nil {
			t.Fatal(err)
		}
		if got := shown(false).name; got != "" || idleScreenIndex() != 0 {
			t.Fatalf("idle screen %q, %q shown, index %d", id, got, idleScreenIndex())
		}
	}
}

// A custom screen draws with the common bar on every panel: a tab for each, the ×, and its own zones.
func TestCustomScreenDraws(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	fakeScreens(t)
	for _, size := range []image.Point{{showWide, showHigh}, {1366, 768}} {
		r := newRenderer(image.NewRGBA(image.Rectangle{Max: size}))
		s := scene{now: time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC), phase: "idle", custom: customView{name: "maps", data: "map"}}
		r.draw(s)
		customHits.Lock()
		zones := slices.Clone(customHits.zones)
		customHits.Unlock()
		var kinds []string
		for _, z := range zones {
			kinds = append(kinds, z.kind+":"+z.id)
		}
		if !slices.Equal(kinds, []string{"tab:notes", "tab:maps", "close:", "item:map"}) {
			t.Fatalf("%v: zones %v", size, kinds)
		}
		if k := customFrameKey(s, false, false); k != "" {
			t.Fatalf("a screen with no key has frame key %q", k)
		}
		s.custom = customView{name: "notes", data: "note"}
		if customFrameKey(s, false, false) == "" || customFrameKey(s, true, false) != "" {
			t.Fatal("frame key: notes has one, and none while ringing")
		}
	}
}
