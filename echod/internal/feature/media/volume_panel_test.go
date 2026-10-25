//go:build panel

package media

import (
	"testing"
	"time"
)

// A press of the tablet's buttons while a ring has Android raised moves the music by the press, not
// to the ring's level; with nothing raised Android's level is the music's.
func TestAPressDuringARingMovesTheMusicByThePress(t *testing.T) {
	for _, c := range []struct {
		music, reported, told int
		raised                bool
		want                  int
	}{
		{3, 24, 25, false, 24}, // no ring: Android's level is the music's
		{3, 4, 3, false, 4},
		{3, 24, 25, true, 2}, // a press down under a ring at 25
		{3, 26, 25, true, 4}, // and up
		{3, 25, 25, true, 3}, // Android repeating the ring's level is no press
		{0, 20, 25, true, 0},
		{28, 30, 29, true, 29},
	} {
		if got := fromAndroid(c.music, c.reported, c.told, c.raised); got != c.want {
			t.Errorf("music at %d, Android told %d (raised %v) and reporting %d: music to %d, want %d",
				c.music, c.told, c.raised, c.reported, got, c.want)
		}
	}
}

// A level Android reports just after the daemon sent it a new one is not taken: it may have been
// worked out against the level before (music at 3 reported as 4 just as a raise to 25 goes out would
// put the music at 0). One reported once Android has had a moment is.
func TestAReportCrossingANewLevelIsNotTaken(t *testing.T) {
	sent := time.Unix(1000, 0)
	for _, c := range []struct {
		after time.Duration
		want  bool
	}{
		{0, true},
		{10 * time.Millisecond, true},
		{androidSettle - time.Millisecond, true},
		{androidSettle, false},
		{time.Second, false},
	} {
		if got := crossed(sent, sent.Add(c.after)); got != c.want {
			t.Errorf("a report %v after a level was sent: crossed %v, want %v", c.after, got, c.want)
		}
	}
	if crossed(time.Time{}, sent) {
		t.Error("with no level sent yet, a report was taken for one that crossed it")
	}
}
