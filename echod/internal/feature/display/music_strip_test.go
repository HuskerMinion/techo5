//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The strip takes over from the full page once the setting's time has gone by for a track, straight
// away for a page somebody swiped away, never when the setting keeps the full page, and not while a
// tap on the strip's song has brought the page back.
func TestWhenTheMusicGoesInTheStrip(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{}
	now := time.Unix(1_790_000_000, 0)
	song := home.Radio{Title: "Africa", Now: "Music Assistant"}

	if d.stripDue(now, song, true) {
		t.Fatal("the strip showed with the setting on Full page")
	}
	if d.stripDue(now, song, false) {
		t.Fatal("a page put away became a strip with the setting on Full page")
	}

	if err := config.Set().Screen().MusicStrip(10); err != nil {
		t.Fatal(err)
	}
	if d.stripDue(now, song, true) {
		t.Error("the strip showed before the ten seconds were up")
	}
	if !d.stripDue(now.Add(11*time.Second), song, true) {
		t.Error("the strip did not show after ten seconds")
	}
	if !d.stripDue(now, song, false) {
		t.Error("a page put away did not go in the strip")
	}

	// The next track starts its own ten seconds.
	next := home.Radio{Title: "Rosanna", Now: "Music Assistant"}
	if d.stripDue(now.Add(12*time.Second), next, true) {
		t.Error("a new track went straight into the strip")
	}

	// A tap on the strip's song brings the page back for a while.
	d.stripFullUntil = now.Add(40 * time.Second)
	if d.stripDue(now.Add(30*time.Second), next, true) {
		t.Error("the strip came back while the tap's page should be up")
	}
	if !d.stripDue(now.Add(41*time.Second), next, true) {
		t.Error("the strip did not come back after the tap's page ran out")
	}
}
