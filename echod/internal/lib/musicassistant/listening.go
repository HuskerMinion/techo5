package musicassistant

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Listening to audiobooks and podcasts: the books folded by series, what this token's user is part way
// through, the full item for its details, and the queue's controls for spoken word: a start over, a
// skip of some seconds, and the playback speed.

// AudiobooksBySeries is a page of the library's audiobooks by name, starting at offset, with each series
// folded into one entry of media type "collection" whose Items are its parts in order. offset and limit
// count those entries, not the books in them. A series entry is given its first part's authors and
// picture and its parts' latest added date, so it sorts and shows as a book does (SeriesFilled).
func (c Client) AudiobooksBySeries(ctx context.Context, offset, limit int) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/audiobooks/library_items", map[string]any{"limit": limit, "offset": offset,
		"order_by": "sort_name", "collapse_collections": true}, &out)
	for i := range out {
		out[i] = SeriesFilled(out[i])
	}
	return out, err
}

// SeriesFilled is a series entry with what it lacks of its own taken from its parts: the server sends a
// series with only its name and parts.
func SeriesFilled(e Entry) Entry {
	if e.MediaType != "collection" || len(e.Items) == 0 {
		return e
	}
	if len(e.Authors) == 0 {
		e.Authors = e.Items[0].Authors
	}
	for _, part := range e.Items {
		if e.Image == nil && len(e.Metadata.Images) == 0 {
			if part.Image != nil {
				e.Image = part.Image
			} else if len(part.Metadata.Images) > 0 {
				img := part.Metadata.Images[0]
				for _, x := range part.Metadata.Images {
					if x.Type == "thumb" {
						img = x
						break
					}
				}
				e.Image = &img
			}
		}
		if part.DateAdded > e.DateAdded {
			e.DateAdded = part.DateAdded
		}
	}
	return e
}

// InProgress are the audiobooks and podcast episodes this token's user is part way through, the latest
// played first, up to limit. They come mixed and short (an id, name, picture and media type), so a page
// keeps the kind it shows.
func (c Client) InProgress(ctx context.Context, limit int) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/in_progress_items", map[string]any{"limit": limit, "all_users": false}, &out)
	return out, err
}

// PodcastEpisode is one episode read in full, with its description and where it was left.
func (c Client) PodcastEpisode(ctx context.Context, episode Entry) (Entry, error) {
	var out Entry
	err := c.do(ctx, "music/podcasts/podcast_episode", itemArgs(episode), &out)
	return out, err
}

// PlayFrom plays uri on the queue queueID in place of what it held. An audiobook starts at the start
// of its chapter at position chapter (its place among the chapters, counted from 0, as the server
// numbers them) when chapter is not negative; a podcast episode starts at its beginning with fromStart.
// Otherwise either one goes on from where it was left. The server asks for the player itself here, so a
// token whose user may not use it is refused (IsForbidden).
func (c Client) PlayFrom(ctx context.Context, queueID, uri string, chapter int, fromStart bool) error {
	args := map[string]any{"queue_id": queueID, "media": uri, "option": "replace"}
	if chapter >= 0 {
		args["start_item"] = strconv.Itoa(chapter)
	}
	if fromStart {
		args["start_from_beginning"] = true
	}
	return c.do(ctx, "player_queues/play_media", args, nil)
}

// Skip moves what the queue queueID plays on by seconds, or back for a negative number.
func (c Client) Skip(ctx context.Context, queueID string, seconds int) error {
	return c.do(ctx, "player_queues/skip", map[string]any{"queue_id": queueID, "seconds": seconds}, nil)
}

// SetSpeed sets how fast the audiobook or podcast episode the queue queueID plays goes, from 0.5 to 3
// times; the server refuses it for anything else.
func (c Client) SetSpeed(ctx context.Context, queueID string, speed float64) error {
	return c.do(ctx, "player_queues/set_playback_speed", map[string]any{"queue_id": queueID, "speed": speed}, nil)
}

// QueueNow is what a queue plays as the server has it.
type QueueNow struct {
	// Active is whether the queue is the one its player plays from, and State what it is doing there
	// ("playing", "paused", "idle").
	Active bool
	State  string
	// MediaType is the media type of the item playing (or left paused), URI its address, Name its
	// name, and ItemName the name the queue gives it.
	MediaType, URI, Name, ItemName string
	// Speed is how fast it plays, 1 for as recorded.
	Speed float64
}

// SpokenWord is whether what plays is an audiobook or a podcast episode, which skip and speed are for.
func (q QueueNow) SpokenWord() bool {
	return q.MediaType == "audiobook" || q.MediaType == "podcast_episode"
}

// Now is what the queue queueID plays: its item's media type and address, and the speed.
func (c Client) Now(ctx context.Context, queueID string) (QueueNow, error) {
	var q struct {
		Active  bool    `json:"active"`
		State   string  `json:"state"`
		Speed   float64 `json:"playback_speed"`
		Current *struct {
			Name      string          `json:"name"`
			MediaType string          `json:"media_type"`
			MediaItem json.RawMessage `json:"media_item"`
		} `json:"current_item"`
	}
	if err := c.do(ctx, "player_queues/get", map[string]any{"queue_id": queueID}, &q); err != nil {
		return QueueNow{}, err
	}
	out := QueueNow{Active: q.Active, State: q.State, Speed: q.Speed}
	if out.Speed <= 0 {
		out.Speed = 1
	}
	if q.Current != nil {
		var e Entry
		_ = e.UnmarshalJSON(q.Current.MediaItem)
		out.MediaType, out.URI, out.Name, out.ItemName = e.MediaType, e.URI, e.Name, q.Current.Name
		if out.MediaType == "" {
			out.MediaType = q.Current.MediaType
		}
	}
	return out, nil
}

var (
	// Go's regexp has no back references, so each of the two blocks has its own.
	scriptBlock = regexp.MustCompile(`(?is)<script\b.*?(</script\s*>|$)`)
	styleBlock  = regexp.MustCompile(`(?is)<style\b.*?(</style\s*>|$)`)
	comment     = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)
	breakTags   = regexp.MustCompile(`(?i)<(br|/p|/div|/li|p|li)\b[^>]*>`)
	// anyTag is a tag only where one looks like one: a letter or a slash and a letter after the <, so
	// "a < b and c > d" stays as it is.
	anyTag = regexp.MustCompile(`</?[A-Za-z][^<>]*>`)
)

// maxDescription is how much of a description an entry keeps, in bytes: a podcast's show notes can run
// to tens of kilobytes, and a page shows a few lines of it.
const maxDescription = 4 << 10

// shortDescription is s cut to maxDescription at a character's start, with "…" where it was cut.
func shortDescription(s string) string {
	if len(s) <= maxDescription {
		return s
	}
	cut := maxDescription
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// PlainText is a description that can carry HTML as text to show: scripts, styles and comments left
// out with what is in them, line breaks for breaks and paragraphs, other tags left out, entities
// written as what they stand for, and runs of spaces and blank lines closed up.
func PlainText(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return strings.TrimSpace(s)
	}
	s = scriptBlock.ReplaceAllString(s, "")
	s = styleBlock.ReplaceAllString(s, "")
	s = comment.ReplaceAllString(s, "")
	s = breakTags.ReplaceAllString(s, "\n")
	s = html.UnescapeString(anyTag.ReplaceAllString(s, ""))
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
