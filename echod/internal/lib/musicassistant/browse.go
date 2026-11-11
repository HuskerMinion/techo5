package musicassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
		ID        string          `json:"queue_item_id"`
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
		e.QueueItemID = it.ID
		if e.Usable() || e.Name != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// PlayQueueItem plays the queue queueID from its item itemID (an Entry's QueueItemID) on. Music
// Assistant asks for the player itself here: a token whose user may not use that player is refused
// (IsForbidden), where MoveNext and RemoveQueueItem are not.
func (c Client) PlayQueueItem(ctx context.Context, queueID, itemID string) error {
	return c.do(ctx, "player_queues/play_index", map[string]any{"queue_id": queueID, "index": itemID}, nil)
}

// MoveNext moves the item itemID of the queue queueID to play next, after what is playing. The server
// leaves an item that is already on its way to the player (the one playing, and perhaps the next) where
// it is, and says so as an error.
func (c Client) MoveNext(ctx context.Context, queueID, itemID string) error {
	return c.do(ctx, "player_queues/move_item", map[string]any{"queue_id": queueID, "queue_item_id": itemID, "pos_shift": 0}, nil)
}

// RemoveQueueItem takes the item itemID out of the queue queueID. One already on its way to the player
// is left in by the server, without an error.
func (c Client) RemoveQueueItem(ctx context.Context, queueID, itemID string) error {
	return c.do(ctx, "player_queues/delete_item", map[string]any{"queue_id": queueID, "item_id_or_index": itemID}, nil)
}

// SetFavorite makes e one of the library's favorites, or no longer one. Adding takes any item by its
// URI (one from a music service is added to the library too); removing takes it as the library has it,
// by kind and library id, so an item that is not in the library cannot be removed.
func (c Client) SetFavorite(ctx context.Context, e Entry, kind string, on bool) error {
	if on {
		return c.do(ctx, "music/favorites/add_item", map[string]any{"item": e.URI}, nil)
	}
	id, ok := LibraryID(e)
	if !ok {
		return errors.New("not in the library")
	}
	return c.do(ctx, "music/favorites/remove_item", map[string]any{"media_type": kind, "library_item_id": id}, nil)
}

// LibraryID is e's id in the library, from its library URI ("library://track/52") or its own id when
// the library is its provider; ok is false for an item only a music service has.
func LibraryID(e Entry) (string, bool) {
	if rest, found := strings.CutPrefix(e.URI, "library://"); found {
		if i := strings.LastIndex(rest, "/"); i >= 0 && i+1 < len(rest) {
			return rest[i+1:], true
		}
	}
	if e.Provider == "library" && e.ItemID != "" {
		return e.ItemID, true
	}
	return "", false
}

// IsForbidden is whether err is the server refusing this token what was asked: a user limited to some
// players asking about or playing on another, or a role without the right.
func IsForbidden(err error) bool { return errors.Is(err, errForbidden) }

// Browse is what the folder at path holds in the server's browse tree: its folders, each with the path
// to open it by, and its playable items. The empty path is the top, one folder for each provider. The
// tree's own way back up ("..") is left out: the page has a Back of its own.
func (c Client) Browse(ctx context.Context, path string) ([]Entry, error) {
	args := map[string]any{}
	if path != "" {
		args["path"] = path
	}
	var got []Entry
	if err := c.do(ctx, "music/browse", args, &got); err != nil {
		return nil, err
	}
	out := got[:0]
	for _, e := range got {
		if e.Name == ".." {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
