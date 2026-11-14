//go:build !dot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// homeLook swaps the look at whether Home Assistant is connected for the test.
func homeLook(t *testing.T, up bool) {
	t.Helper()
	was := linkHome
	linkHome = func() bool { return up }
	t.Cleanup(func() { linkHome = was })
}

// A pipeline that was not ready is Home Assistant away when it is not connected, and no assistant set
// up when it is; the reasons the conversation could tell apart are shown as they are.
func TestAFailedRequestsReasonOnTheScreen(t *testing.T) {
	homeLook(t, false)
	if got := failedShown(voice.FailedNotReady); got != voice.FailedNoHome {
		t.Errorf("not ready with Home Assistant away is shown as %q", got)
	}
	if got := failedShown(voice.FailedTimeout); got != voice.FailedTimeout {
		t.Errorf("a timeout is shown as %q", got)
	}
	homeLook(t, true)
	if got := failedShown(voice.FailedNotReady); got != voice.FailedNoAssistant {
		t.Errorf("not ready with Home Assistant connected is shown as %q", got)
	}
	if got := failedShown(""); got != "" || failedLine("") != "" {
		t.Errorf("no failure is shown as %q, %q", got, failedLine(""))
	}
	seen := map[string]bool{}
	for _, why := range []string{voice.FailedNoHome, voice.FailedNoAssistant, voice.FailedTimeout, voice.FailedError} {
		line := failedLine(why)
		if line == "" || seen[line] {
			t.Errorf("%q has the line %q, empty or another reason's", why, line)
		}
		seen[line] = true
	}
}

// A failed request's line lingers as an answer does, is put away by a tap as an answer is, and is said
// again by the next failure, whatever was put away before it.
func TestAFailedRequestLingersAndIsTappedAway(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{poke: make(chan struct{}, 1)}
	failed := voice.State{Phase: "idle", Failed: voice.FailedTimeout}

	d.changed(failed)
	now := time.Now()
	if !d.answerUp(now) {
		t.Fatal("a request that just failed has nothing up")
	}
	if d.answerUp(now.Add(linger() + time.Second)) {
		t.Error("the failure is still up past the answer time")
	}
	d.clearAnswer()
	if d.answerUp(now) {
		t.Error("the failure is still up after a tap")
	}
	d.changed(failed)
	if !d.answerUp(time.Now()) {
		t.Error("the next failure is not shown after the last was tapped away")
	}
}

// Under the cue with no captions a failure keeps its line and nothing else; with none it is cueTurn.
func TestACueKeepsAFailuresLine(t *testing.T) {
	cue, page, heard, reply := cueFailed("lingering", "turn on the light", "", voice.FailedTimeout, false)
	if cue != "lingering" || page != "idle" || heard != "" || reply != "" {
		t.Errorf("a failure under the light alone: cue %q page %q heard %q reply %q", cue, page, heard, reply)
	}
	cue, _, heard, _ = cueFailed("lingering", "turn on the light", "", voice.FailedTimeout, true)
	if cue != "lingering" || heard != "turn on the light" {
		t.Errorf("a failure under captions: cue %q heard %q", cue, heard)
	}
	if cue, _, _, _ := cueFailed("lingering", "turn on the light", "It is on.", "", false); cue != "" {
		t.Errorf("an answer under the light alone still lingers as %q", cue)
	}
}
