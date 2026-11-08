package streaming

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

func TestSoloistProgressPlaybackPauseSeekAndTrackChange(t *testing.T) {
	media.ClearPosition()
	defer media.ClearPosition()
	now := time.Unix(1794090000, 0)
	var progress soloistProgress
	feed := func(raw string, at time.Time) {
		t.Helper()
		var event soloistEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		progress.update(event, at)
	}
	feed(`{"type":"playback_state","status":"playing","item":{"uri":"spotify:track:a","decorations":{"identity":{"name":"Song A"},"playback":{"duration_ms":180000}}},"position":{"position_ms":30000,"speed":1}}`, now)
	p, ok := media.TrackPosition("Song A")
	if !ok || p.Dur != 3*time.Minute || p.Now(now.Add(5*time.Second)) != 35*time.Second {
		t.Fatalf("initial progress: %+v, available=%t", p, ok)
	}
	feed(`{"type":"playback_changed","status":"paused"}`, now.Add(5*time.Second))
	p, _ = media.TrackPosition("Song A")
	if p.Now(now.Add(20*time.Second)) != 35*time.Second || p.Rate != 0 {
		t.Fatal("paused progress kept advancing")
	}
	feed(`{"type":"playback_changed","status":"playing"}`, now.Add(20*time.Second))
	p, _ = media.TrackPosition("Song A")
	if p.Now(now.Add(22*time.Second)) != 37*time.Second {
		t.Fatal("resume lost position")
	}
	feed(`{"type":"position_sync","position":{"position_ms":90000,"speed":1}}`, now.Add(23*time.Second))
	p, _ = media.TrackPosition("Song A")
	if p.Now(now.Add(24*time.Second)) != 91*time.Second {
		t.Fatal("seek did not update anchor")
	}
	feed(`{"type":"track_changed","item":{"uri":"spotify:track:b","decorations":{"identity":{"name":"Song B"},"playback":{"duration_ms":200000}}}}`, now.Add(25*time.Second))
	if _, ok := media.TrackPosition("Song A"); ok {
		t.Fatal("previous track timing retained")
	}
	if _, ok := media.TrackPosition("Song B"); ok {
		t.Fatal("new track used old position")
	}
	feed(`{"type":"position_sync","position":{"position_ms":0,"speed":1}}`, now.Add(26*time.Second))
	p, ok = media.TrackPosition("Song B")
	if !ok || p.Dur != 200*time.Second || p.Pos != 0 {
		t.Fatal("new track duration or zero position lost")
	}
}

func TestSoloistProgressTimestampAndSourceCleanup(t *testing.T) {
	media.ClearPosition()
	defer media.ClearPosition()
	now := time.UnixMilli(1794090000000)
	item := soloistEntity{URI: "spotify:track:test"}
	item.Decorations.Identity.Name = "Timed song"
	item.Decorations.Playback.DurationMS = 60000
	var p soloistProgress
	p.update(soloistEvent{Type: "playback_state", Status: "playing", Item: item, Position: &soloistPosition{PositionMS: 10000, TimestampMS: now.Add(-2 * time.Second).UnixMilli(), Speed: 1}}, now)
	got, ok := media.TrackPosition("Timed song")
	if !ok || got.Now(now) != 12*time.Second {
		t.Fatal("source timestamp ignored")
	}
	media.ClearSourcePosition(home.SpotifyName)
	if _, ok := media.TrackPosition("Timed song"); ok {
		t.Fatal("receiver disconnect retained progress")
	}
	media.SetPosition(media.Position{Source: "another source", Title: "Other song", Dur: time.Minute})
	media.ClearSourcePosition(home.SpotifyName)
	if _, ok := media.TrackPosition("Other song"); !ok {
		t.Fatal("receiver cleared another source's timing")
	}
}
