package streaming

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// Soloist timestamps use the local server's Unix clock. Both processes are on the same device;
// retaining the timestamp lets the shared player interpolate between events and after seeks.
type soloistPosition struct {
	PositionMS  int64   `json:"position_ms"`
	TimestampMS int64   `json:"timestamp_ms"`
	Speed       float64 `json:"speed"`
}

type soloistProgress struct {
	uri, status string
	position    media.Position
	anchored    bool
	speed       float64
}

func (p *soloistProgress) track(item soloistEntity, changed bool) {
	title := item.Decorations.Identity.Name
	if changed || item.URI != p.uri || title != p.position.Title {
		*p = soloistProgress{uri: item.URI, speed: 1, position: media.Position{Source: home.SpotifyName, Title: title}}
		media.ClearSourcePosition(home.SpotifyName)
	}
	p.position.Dur = time.Duration(item.Decorations.Playback.DurationMS) * time.Millisecond
}

func (p *soloistProgress) update(e soloistEvent, now time.Time) {
	switch e.Type {
	case "auth_state":
		if !e.LoggedIn {
			*p = soloistProgress{}
			media.ClearSourcePosition(home.SpotifyName)
		}
		return
	case "track_changed":
		p.track(e.Item, true)
		return // a following full snapshot supplies the new track's position
	case "playback_state":
		p.track(e.Item, false)
		p.status = e.Status
	case "playback_changed":
		if p.anchored {
			p.position.Pos = p.position.Now(now)
			p.position.At = now
		}
		p.status = e.Status
		p.position.Rate = 0
		if e.Status == "playing" {
			p.position.Rate = p.speed
			if p.position.Rate <= 0 {
				p.position.Rate = 1
			}
		}
	case "position_sync":
	default:
		return
	}
	if anchor := e.Position; anchor != nil {
		if anchor.PositionMS < 0 || anchor.Speed < 0 {
			return
		}
		p.position.Pos = time.Duration(anchor.PositionMS) * time.Millisecond
		p.position.At = now
		if anchor.TimestampMS > 0 {
			p.position.At = time.UnixMilli(anchor.TimestampMS)
		}
		p.position.Rate = anchor.Speed
		if anchor.Speed > 0 {
			p.speed = anchor.Speed
		}
		if p.status == "paused" || p.status == "idle" {
			p.position.Rate = 0
		}
		p.anchored = true
	}
	if !p.anchored || p.position.Title == "" || p.position.Dur <= 0 {
		media.ClearSourcePosition(home.SpotifyName)
		return
	}
	if from := media.Get().Receiving(); from != "" && from != home.SpotifyName {
		return
	}
	media.SetPosition(p.position)
}
