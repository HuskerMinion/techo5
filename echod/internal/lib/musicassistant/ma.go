// Package musicassistant asks a Music Assistant server for music: its HTTP API, one POST to /api per
// command with a long-lived token (Music Assistant 2.7 and later). Only the few commands the voice
// assistant needs: search, and play on a player.
package musicassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client is one server: its address (http://host:8095) and a token.
type Client struct {
	URL, Token string
}

var hc = &http.Client{Timeout: 20 * time.Second}

// do runs a command and decodes its result into out, when out is not nil.
func (c Client) do(ctx context.Context, command string, args map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"command": command, "args": args})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/api", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("music assistant did not answer: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return errors.New("music assistant refused the token")
	case http.StatusForbidden:
		return errForbidden
	default:
		// The status only: what the server says about it goes to the chat model and the log otherwise.
		return fmt.Errorf("music assistant answered %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// What did not fit is for the log; a screen says only that it could not be read.
		slog.Warn("music assistant: an answer that could not be read", "command", command, "err", err)
		return ErrUnreadable
	}
	return nil
}

// ErrUnreadable is an answer from the server that this device could not read.
//
//lint:ignore ST1005 it starts with a name, and the screen shows it as it is
var ErrUnreadable = errors.New("Music Assistant sent something this device couldn't read")

// errForbidden is the server refusing this token: a user limited to some players asking about or
// playing on another, or a role without the right.
var errForbidden = errors.New("music assistant does not let this token do that")

// Item is a piece of music the server found: an artist, album, track, playlist or radio station.
type Item struct {
	URI       string `json:"uri"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Artists   []struct {
		Name string `json:"name"`
	} `json:"artists"`
	// Provider is where the item is held: a service's instance id, or "library"; Mappings are the
	// services it is found on, a library item's included.
	Provider string `json:"provider"`
	Mappings []struct {
		Instance string `json:"provider_instance"`
		Domain   string `json:"provider_domain"`
	} `json:"provider_mappings"`
}

// From is whether the item is on the service with this instance id or domain, by where it is held,
// the services it is mapped to, or its URI's scheme (a service's own items are "ytmusic--a1://...").
// An instance id is its domain, "--" and a suffix, so a domain alone matches its instances too.
func (i Item) From(instance, domain string) bool {
	is := func(id string) bool {
		return id != "" && (id == instance || id == domain || domain != "" && strings.HasPrefix(id, domain+"--"))
	}
	if is(i.Provider) {
		return true
	}
	if k := strings.Index(i.URI, "://"); k > 0 && is(i.URI[:k]) {
		return true
	}
	for _, m := range i.Mappings {
		if is(m.Instance) || is(m.Domain) {
			return true
		}
	}
	return false
}

// Sourced is whether the item says where it came from at all.
func (i Item) Sourced() bool {
	return i.Provider != "" || len(i.Mappings) > 0 || strings.Contains(i.URI, "://")
}

// By is who it is by, for saying what is playing: the artists' names, or nothing.
func (i Item) By() string {
	var n []string
	for _, a := range i.Artists {
		if a.Name != "" {
			n = append(n, a.Name)
		}
	}
	return strings.Join(n, " and ")
}

// Results is a search's findings by kind.
type Results struct {
	Artists   []Item `json:"artists"`
	Albums    []Item `json:"albums"`
	Tracks    []Item `json:"tracks"`
	Playlists []Item `json:"playlists"`
	Radio     []Item `json:"radio"`
}

// Search looks for query among the given kinds (artist, album, track, playlist, radio; none means all),
// limit of each, in the given services (their instance ids or domains, "library" for the library
// itself; none means the library and every service). Music Assistant 2.10 and later.
func (c Client) Search(ctx context.Context, query string, kinds []string, limit int, providers ...string) (Results, error) {
	args := map[string]any{"search_query": query, "limit": limit}
	if len(kinds) > 0 {
		args["media_types"] = kinds
	}
	if len(providers) > 0 {
		args["providers"] = providers
	}
	var r Results
	err := c.do(ctx, "music/search", args, &r)
	return r, err
}

// Provider is one of the server's music services: YouTube Music, Spotify, a folder of files.
type Provider struct {
	InstanceID string `json:"instance_id"`
	Domain     string `json:"domain"`
	Name       string `json:"name"`
	Available  bool   `json:"available"`
}

// MusicProviders are the music services the server has running, as this token may see them.
func (c Client) MusicProviders(ctx context.Context) ([]Provider, error) {
	var all []Provider
	if err := c.do(ctx, "providers", map[string]any{"provider_type": "music"}, &all); err != nil {
		return nil, err
	}
	var out []Provider
	for _, p := range all {
		if p.Available && p.InstanceID != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// FindProvider is the service a name means: its instance id, domain or display name, ignoring case,
// spaces and punctuation, so "YouTube Music", "youtube music" and "ytmusic" all find YouTube Music.
func FindProvider(providers []Provider, name string) (Provider, bool) {
	want := squash(name)
	if want == "" {
		return Provider{}, false
	}
	for _, p := range providers {
		if squash(p.InstanceID) == want || squash(p.Domain) == want || squash(p.Name) == want {
			return p, true
		}
	}
	// A service renamed in Music Assistant is still found by what it is: "YouTube Music" is ytmusic.
	if d, ok := knownDomains[want]; ok {
		for _, p := range providers {
			if p.Domain == d {
				return p, true
			}
		}
	}
	return Provider{}, false
}

// knownDomains are the Music Assistant services people name aloud, by what they say, squashed.
var knownDomains = map[string]string{
	"youtubemusic": "ytmusic", "youtube": "ytmusic", "ytmusic": "ytmusic",
	"spotify": "spotify", "applemusic": "apple_music", "tidal": "tidal", "qobuz": "qobuz",
	"deezer": "deezer", "soundcloud": "soundcloud", "plex": "plex", "jellyfin": "jellyfin",
	"audible": "audible", "library": "library", "mylibrary": "library",
}

// KnownDomain is the service domain a spoken name means, for a server that will not list its services.
func KnownDomain(name string) (string, bool) {
	d, ok := knownDomains[squash(name)]
	return d, ok
}

// squash keeps letters and digits, lowercased.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Play plays uri on the player queueID, replacing what its queue held.
func (c Client) Play(ctx context.Context, queueID, uri string) error {
	return c.do(ctx, "player_queues/play_media", map[string]any{"queue_id": queueID, "media": uri, "option": "replace"}, nil)
}

// PlayerState is a player as the server sees it: its id there, whether it is connected and what it
// is doing ("playing", "paused", "idle").
type PlayerState struct {
	ID        string `json:"player_id"`
	Available bool   `json:"available"`
	State     string `json:"playback_state"`
	Protocols []struct {
		ID string `json:"output_protocol_id"`
	} `json:"output_protocols"`
}

// Player is the player that is this device, found by its own id there (its Sendspin client id, the
// factory MAC). Music Assistant 2.10 gathers a device's ways in (Sendspin, Home Assistant, DLNA) under
// one player with an id of its own, and limits a user to players by that id, so the device's own id is
// looked for among each player's protocols, in the players this token may use.
func (c Client) Player(ctx context.Context, own string) (PlayerState, error) {
	var all []PlayerState
	if err := c.do(ctx, "players/all", map[string]any{}, &all); err != nil {
		return PlayerState{}, err
	}
	for _, p := range all {
		if strings.EqualFold(p.ID, own) {
			return p, nil
		}
		for _, o := range p.Protocols {
			if strings.EqualFold(o.ID, own) {
				return p, nil
			}
		}
	}
	return PlayerState{}, errors.New("music assistant has no player for this device that this token may use; in Music Assistant, add this device to the user's allowed players")
}

// Stop stops the player id (its own id at the server, as Player gives it).
func (c Client) Stop(ctx context.Context, id string) error {
	return c.do(ctx, "players/cmd/stop", map[string]any{"player_id": id}, nil)
}

// Resume plays the queue of the player id (as Player gives it; a player's queue has its id) again from
// where it stopped: a pause ends Music Assistant's stream to a Sendspin player, so there is nothing left
// to unpause. A server that has no resume for it is asked to play instead.
func (c Client) Resume(ctx context.Context, id string) error {
	err := c.do(ctx, "player_queues/resume", map[string]any{"queue_id": id}, nil)
	if err == nil {
		return nil
	}
	if perr := c.do(ctx, "players/cmd/play", map[string]any{"player_id": id}, nil); perr != nil {
		return fmt.Errorf("%w; and play: %v", err, perr)
	}
	return nil
}

// ErrOvertaken is a play that something asked for since has made unwanted.
var ErrOvertaken = errors.New("something else was asked for meanwhile")

// PlayChecked plays uri on the player that is this device (own: its id, as Player takes it) and waits,
// up to within, for the server to say it is playing there, looking every poll, for as long as wanted
// says it is still wanted (nil for always). A player the server has no connection to is told as that
// before anything is asked of it, since nothing it is given could be heard.
//
// What was playing there is stopped first, so that its "playing" is not taken for this one's. A play
// that does not start in time, or that something else overtook, is stopped too: otherwise the server
// can start it late, over whatever the device went on to play.
func (c Client) PlayChecked(ctx context.Context, own, uri string, within, poll time.Duration, wanted func() bool) error {
	p, err := c.Player(ctx, own)
	if err != nil {
		return err
	}
	if !p.Available {
		return errors.New("this device is not connected to the music library right now, so it cannot play from it")
	}
	if p.State == "playing" {
		_ = c.Stop(ctx, p.ID)
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			if st, err := c.Player(ctx, own); err != nil || st.State != "playing" {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("the music library did not answer in time")
			case <-time.After(poll):
			}
		}
	}
	if err := c.Play(ctx, p.ID, uri); err != nil {
		return fmt.Errorf("the music library would not play it: %v", err)
	}
	// Whatever comes of it, a play that did not come off is not left to start later. The stop gets
	// its own few seconds, since ctx may be what ran out.
	stop := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Stop(sctx, p.ID)
	}
	deadline := time.Now().Add(within)
	for {
		select {
		case <-ctx.Done():
			stop()
			return errors.New("the music library did not answer in time")
		case <-time.After(poll):
		}
		if wanted != nil && !wanted() {
			stop()
			return ErrOvertaken
		}
		if st, err := c.Player(ctx, own); err == nil && st.State == "playing" {
			return nil
		}
		if time.Now().After(deadline) {
			stop()
			return errors.New("the music library took it, but nothing started playing on this device")
		}
	}
}
