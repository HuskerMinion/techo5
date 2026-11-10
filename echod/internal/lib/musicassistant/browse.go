package musicassistant

import (
	"context"
	"encoding/json"
)

// What the library shows beyond a kind's own list: the favorites of each kind, what was played lately,
// and a player's queue from what is playing on.

// Favorites are the library's favorite items of kind (KindTracks ...), by name, up to limit.
func (c Client) Favorites(ctx context.Context, kind string, limit int) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/"+kind+"/library_items",
		map[string]any{"favorite": true, "limit": limit, "offset": 0, "order_by": "sort_name"}, &out)
	return out, err
}

// RecentlyPlayed are the items played lately, of every kind, the latest first, up to limit.
func (c Client) RecentlyPlayed(ctx context.Context, limit int) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/recently_played_items", map[string]any{"limit": limit}, &out)
	return out, err
}

// Queue is what the queue queueID plays from what is playing now on, up to limit: the one playing
// first. Each is its media item as an entry, named and timed as the queue has it.
func (c Client) Queue(ctx context.Context, queueID string, limit int) ([]Entry, error) {
	var q struct {
		CurrentIndex *int `json:"current_index"`
	}
	if err := c.do(ctx, "player_queues/get", map[string]any{"queue_id": queueID}, &q); err != nil {
		return nil, err
	}
	from := 0
	if q.CurrentIndex != nil && *q.CurrentIndex > 0 {
		from = *q.CurrentIndex
	}
	var items []struct {
		Name      string          `json:"name"`
		Duration  float64         `json:"duration"`
		MediaItem json.RawMessage `json:"media_item"`
	}
	if err := c.do(ctx, "player_queues/items", map[string]any{"queue_id": queueID, "limit": limit, "offset": from}, &items); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		var e Entry
		_ = e.UnmarshalJSON(it.MediaItem)
		if e.Name == "" {
			e.Name = it.Name
		}
		if it.Duration > 0 {
			e.Duration = int(it.Duration)
		}
		if e.Usable() || e.Name != "" {
			out = append(out, e)
		}
	}
	return out, nil
}
