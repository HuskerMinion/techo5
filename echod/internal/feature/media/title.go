package media

import (
	"log/slog"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"
)

// A name for something Home Assistant plays here. Its play_media carries only a url (the ESPHome media
// player has no field for a title), so the screen named the device's own track as the radio, with the
// last station's song, or not at all. media_title names the next play_media instead: a recording off an
// answering machine shows as "Message from Alex", "today 14:12", "Answering machine".

// Title is what a track is named as: Title large, Subtitle under it, and Source where a station or
// "Bluetooth" is named, empty for the device's own word for it.
type Title struct{ Title, Subtitle, Source string }

// TitleNext names the next play_media that is not an announcement. One shot: that track takes it, and
// the one after it has none again. Anything else of this player's starting first (a station, a phone
// over Bluetooth) or Music Assistant taking the speaker drops it unused.
func (p *Player) TitleNext(t Title) {
	p.nextTitle.Store(&t)
}

// OwnTitle is what the loaded track was named by TitleNext, while it plays or is paused; nil otherwise.
func (p *Player) OwnTitle() *Title { return p.stream.Title() }

// Actions: media_title names the next play_media.
func (p *Player) Actions() []*esphome.Action {
	return []*esphome.Action{{
		// All three arguments, as Home Assistant requires of an ESPHome action; subtitle and source
		// may be empty.
		Name: "media_title",
		Args: []esphome.Arg{
			{Name: "title", Type: esphome.ArgString},
			{Name: "subtitle", Type: esphome.ArgString},
			{Name: "source", Type: esphome.ArgString},
		},
		Run: func(c esphome.Call) (any, error) {
			t := Title{
				Title:    strings.TrimSpace(c.String("title")),
				Subtitle: strings.TrimSpace(c.String("subtitle")),
				Source:   strings.TrimSpace(c.String("source")),
			}
			if t.Title == "" {
				// Nothing to name it: the next play_media is as it would be without the action.
				p.nextTitle.Store(nil)
				return nil, nil
			}
			// Not the words: what is played and to whom stays out of the log.
			slog.Info("media: the next play_media is named", "subtitle", t.Subtitle != "", "source", t.Source != "")
			p.TitleNext(t)
			return nil, nil
		},
	}}
}
